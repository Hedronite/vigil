#!/usr/bin/env bash
# scripts/scrub.sh: keep addresses, machine names and secrets out of the repo.
#
# Usage:
#   scripts/scrub.sh                 scan every tracked file, check overlays
#   scripts/scrub.sh --scan-dir DIR  scan every file under DIR as a fixture
#   scripts/scrub.sh --selftest      prove each rule fires on planted input
#
# Exit: 0 clean, 1 findings, 2 usage or internal error. The script fails
# closed: any grep, sed, git, open or redirect that errors makes the run
# exit 2, never 0. Lists of paths are NUL-separated, so a name with an
# accent, a quote or a newline is scanned like any other. The number of
# paths scanned must equal the number listed. A tracked symlink or other
# non-regular file is a finding: the scan never follows or skips it.
#
# Findings print as "path:line: [rule] message". The matched text is never
# printed, so a leak does not also land in a public CI log.
#
# Rules marked "fixture" apply to testdata/**, example*.yaml and files under
# testdata only. testdata/scrub/ is skipped in a tree scan: it holds the
# planted input the self-test expects to fail.
#
# Coverage limits: the machine-name rule checks the values of "host",
# "instance" and "id" keys (any case, bare, single- or double-quoted) against
# the public slugs, the product ids, 32-digit hex ids and a short list of
# placeholders; an empty, list or block value is a finding. The hostname
# rule treats any dotted name in a fixture as a hostname, after blanking
# the exact schema URL values, "$id" values under the two Hedronite repos,
# "apiVersion" values of the form <product>.<name>/v<N> for the five
# products (anchored on the key, so a new schema needs no scrub edit and the
# same name under any other key is still a finding), file names, version
# numbers and the values of "kind" and "source" (open vocabularies that
# allow dots; the TLD rule still runs over those). Bytes
# above 0x7f count as name characters, so an accented or lookalike name is
# still a name. A real name placed under any other key with no dot is not
# seen here; review covers that.
#
# testdata/scrub/ is pinned by content hash (PLANTED_PINS): a changed,
# added, linked or missing file there fails the scan, so nothing real can
# hide in the directory the tree scan skips. Update the pins in the same
# commit as any deliberate change to a planted file.
#
# Binary files are scanned as text (grep -a).
#
# Written for bash 3.2 (macOS runners): no mapfile, no associative arrays, and
# no process substitution, which leaks descriptors in a long loop there.
# Every loop reads a plain temp file on an explicit descriptor (read -u), so
# a failed open is caught at the exec and never reads as an empty list.

set -u
set -o pipefail
export LC_ALL=C

# Descriptor floor (PM ruling, G1 round 3). bash 3.2 can fail a redirect or a
# dup half-way, print a diagnostic, and carry on, so no check below can be
# trusted at a very low limit. Refuse to run under the floor instead.
FD_FLOOR=64
fd_lim=$(ulimit -n) || {
	echo "scrub: cannot read the descriptor limit" >&2
	exit 2
}
if [[ $fd_lim =~ ^[0-9]+$ ]] && [ "$fd_lim" -lt "$FD_FLOOR" ]; then
	echo "scrub: descriptor limit $fd_lim is below $FD_FLOOR; raise it with ulimit -n $FD_FLOOR and run again" >&2
	exit 2
fi

SELF_DIR=$(cd "$(dirname "$0")" && pwd) || exit 2
SELF=$SELF_DIR/$(basename "$0")

SLUGS="castle tower citadel apiary lathe box unmapped"
# Ids that are not machines: the authorities, sources and backends.
IDS_OK="simplex augur cubit scry grafana prom"
# Placeholder values, exact and case-sensitive.
PLACEHOLDERS="XYZ PLACEHOLDER EXAMPLE"

PLANTED_PINS="planted-bad.txt:5c1ba7b6a28889d0581321057c5beaf2637ff6fca655340e85bcba664c61949c planted-id.json:43f52fbced451f761d49a4584cff16b5b89ce7c7cb3906e933979a15dcafeab4"

# Deny-by-default hostname shape: two or more labels joined by dots. The
# left edge allows a dot, so a leading-dot name (a cookie domain) matches.
FQDN_ANY_RE='(^|[^A-Za-z0-9_-])[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)+([^A-Za-z0-9_-]|$)'
# Exact values the fixture hostname rule lets through (PM ruling, G1):
#   the 2020-12 meta-schema URL as a "$schema" value, and a "$id" value of
#   the form https://github.com/Hedronite/{vigil,simplex}/schema/<file>.json.
#   Both are anchored on the key and the closing quote, so nothing can ride
#   along inside them. The reserved example.invalid never matches a rule.
FQDN_STRIP_SCHEMA='s#"\$schema"[[:space:]]*:[[:space:]]*"https://json-schema\.org/draft/2020-12/schema"##g'
FQDN_STRIP_ID='s#"\$id"[[:space:]]*:[[:space:]]*"https://github\.com/Hedronite/(vigil|simplex)/schema/[A-Za-z0-9_.-]+\.json"##g'
# Key/value pairs for the machine-name rule. Bare, single- or double-quoted
# keys; any case (grep -i). The edge excludes "$", ":" and "/" so "$id",
# "localhost:" and URLs are not keys.
HOST_KEY_RE='(^|[^A-Za-z0-9_.$:/"'"'"'-])["'"'"']?(host|instance|id)["'"'"']?[[:space:]]*:[[:space:]]*[^[:space:]]*'

RULE_TAGS=()
RULE_SCOPES=()
RULE_FLAGS=()
RULE_RES=()
RULE_MSGS=()

add_rule() { # tag scope flags regex message
	RULE_TAGS+=("$1")
	RULE_SCOPES+=("$2")
	RULE_FLAGS+=("$3")
	RULE_RES+=("$4")
	RULE_MSGS+=("$5")
}

L='(^|[^0-9.])'    # left edge of an address literal
R='([^0-9]|$)'     # right edge
OCT='[0-9]{1,3}'

add_rule cgnat all -E "${L}100\.(6[4-9]|[7-9][0-9]|1[01][0-9]|12[0-7])\.${OCT}\.${OCT}${R}" "shared-address-space (CGNAT) literal"
add_rule rfc1918-10 all -E "${L}10\.${OCT}\.${OCT}\.${OCT}${R}" "private 10/8 literal"
add_rule rfc1918-172 all -E "${L}172\.(1[6-9]|2[0-9]|3[01])\.${OCT}\.${OCT}${R}" "private 172.16/12 literal"
add_rule rfc1918-192 all -E "${L}192\.168\.${OCT}\.${OCT}${R}" "private 192.168/16 literal"
add_rule tailnet-ula all -Ei 'fd7a:115c:'a1e0 "tailnet IPv6 literal"
add_rule ts-net all -Ei '[A-Za-z0-9-]+\.ts\.net' "tailnet hostname"
add_rule token-github all -E '(gh[pousr]_[A-Za-z0-9]{36}|github_pat_[A-Za-z0-9_]{22,})' "GitHub token shape"
add_rule token-slack all -E 'xox[abprs]-[A-Za-z0-9-]{10,}' "Slack token shape"
add_rule token-aws all -E 'AKIA[0-9A-Z]{16}' "AWS key id shape"
add_rule token-privkey all -E '-----BEGIN [A-Z ]*PRIVATE KEY-----' "private key block"
add_rule token-bearer all -E '[Bb]earer[[:space:]]+[A-Za-z0-9._~+/-]{24,}' "bearer credential"
add_rule token-generic all -Ei '(token|secret|passw(or)?d|api[_-]?key)["'"'"']?[[:space:]]*[:=][[:space:]]*["'"'"']?[A-Za-z0-9+_-]{32,}' "credential assignment"
add_rule json-escape fixture -E '\\(u00|x|U000000)([2-6][0-9A-Fa-f]|7[0-9A-Ea-e])' "escape of a printable ASCII character (\\u, \\x or \\U)"
add_rule fqdn fixture -Ei '(^|[^A-Za-z0-9-])[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9-]+)*\.(com|net|org|io|dev|app|ai|co|me|xyz|cloud|local|lan|internal|corp|home|arpa)([^A-Za-z0-9-]|$)' "hostname in fixture or example"

FINDINGS=0
INTERNAL=0
GREP_RC=0
TAGS_SEEN=" "
TMPD=$(mktemp -d "${TMPDIR:-/tmp}/scrub.XXXXXX") || exit 2
trap 'rm -rf "$TMPD"' EXIT

# Names that are not hosts, blanked before the deny-by-default hostname
# check. Each command runs twice so adjacent tokens both strip.
cat >"$TMPD/strip.sed" <<'SED' || exit 2
s#([0-9]{2})\.[0-9]{1,9}Z#\1Z#g
s#(^|[^A-Za-z0-9_.-])([A-Za-z0-9_-]+\.)*example\.invalid([^A-Za-z0-9_-]|$)#\1\3#g
s#(^|[^A-Za-z0-9_.-])([A-Za-z0-9_-]+\.)*example\.invalid([^A-Za-z0-9_-]|$)#\1\3#g
s#"(kind|source)"[[:space:]]*:[[:space:]]*"[a-z][a-z0-9_.-]{0,63}"#"\1":""#g
s#(^|[^A-Za-z0-9_.-])(["']?apiVersion["']?[[:space:]]*:[[:space:]]*["']?)(vigil|simplex|augur|scry|cubit)\.[a-z]+/v[0-9]+(["']?)([^A-Za-z0-9._/-]|$)#\1\2\4\5#g
s#(^|[^A-Za-z0-9_.-])(["']?apiVersion["']?[[:space:]]*:[[:space:]]*["']?)(vigil|simplex|augur|scry|cubit)\.[a-z]+/v[0-9]+(["']?)([^A-Za-z0-9._/-]|$)#\1\2\4\5#g
s#(^|[^A-Za-z0-9_.-])([A-Za-z0-9_-]+\.)+(json|yaml|yml|txt|bin|token)([^A-Za-z0-9._-]|$)#\1\4#g
s#(^|[^A-Za-z0-9_.-])([A-Za-z0-9_-]+\.)+(json|yaml|yml|txt|bin|token)([^A-Za-z0-9._-]|$)#\1\4#g
s#(^|[^A-Za-z0-9_.-])v?[0-9]+(\.[0-9]+)+([^A-Za-z0-9_-]|$)#\1\3#g
s#(^|[^A-Za-z0-9_.-])v?[0-9]+(\.[0-9]+)+([^A-Za-z0-9_-]|$)#\1\3#g
SED

report() { # path line tag message
	local p=${1//[[:cntrl:]]/?}
	printf '%s:%s: [%s] %s\n' "$p" "$2" "$3" "$4"
	FINDINGS=$((FINDINGS + 1))
	case "$TAGS_SEEN" in *" $3 "*) ;; *) TAGS_SEEN="$TAGS_SEEN$3 " ;; esac
}

internal() { # message
	echo "scrub: internal error: $1" >&2
	INTERNAL=1
}

is_fixture_path() {
	case "$1" in
	testdata/scrub/*) return 1 ;;
	testdata/* | */testdata/* | example.* | */example.* | *example*.yaml | *example*.yml) return 0 ;;
	esac
	return 1
}

in_list() { # word list
	local w
	for w in $2; do [ "$w" = "$1" ] && return 0; done
	return 1
}

# grep_to OUT desc ARGS...: grep into OUT. Exit 1 (no match) is fine; 2 or
# more is an error. The rc is captured inside the redirected group, so a
# redirect that fails to open leaves it at 99: that is an error too, never
# a no-match.
grep_to() {
	local out=$1 desc=$2
	shift 2
	GREP_RC=99
	{
		grep "$@"
		GREP_RC=$?
	} >"$out" 2>"$TMPD/err"
	if [ "$GREP_RC" -gt 1 ]; then
		internal "grep failed ($desc)"
		: >"$out" 2>/dev/null
	fi
}

# emit_hits FILE shown tag message: report the line number of each hit in FILE.
emit_hits() {
	local hit lines=() n=0 i
	exec 5<"$1" || { internal "cannot read the hit list ($2)"; return; }
	while IFS= read -r -u 5 hit; do
		lines[n]=$hit
		n=$((n + 1))
	done
	exec 5<&-
	i=0
	while [ "$i" -lt "$n" ]; do
		report "$2" "${lines[$i]%%:*}" "$3" "$4"
		i=$((i + 1))
	done
}

# scan_fqdn file shown message flags tld_regex
# Two passes, one report per line. Pass 1: the TLD list, run over text with
# only the exact allowlist blanked (so it also covers kind and source values).
# Pass 2: any dotted name, run after the non-host names are blanked too.
# Bytes above 0x7f become "x" first, so an accented name is still a name.
scan_fqdn() {
	local f=$1 shown=$2 msg=$3 flags=$4 re=$5
	if ! sed -E -e "$FQDN_STRIP_SCHEMA" -e "$FQDN_STRIP_ID" -- "$f" 2>"$TMPD/err" | tr '\200-\377' 'x' >"$TMPD/s1"; then
		internal "sed failed ($shown)"
		return
	fi
	if ! sed -E -f "$TMPD/strip.sed" -- "$TMPD/s1" >"$TMPD/s2" 2>"$TMPD/err"; then
		internal "sed failed on the strip program ($shown)"
		return
	fi
	grep_to "$TMPD/h1" "$shown fqdn-tld" -a -n -o $flags -e "$re" -- "$TMPD/s1"
	grep_to "$TMPD/h2" "$shown fqdn-any" -a -n -o -E -e "$FQDN_ANY_RE" -- "$TMPD/s2"
	cat "$TMPD/h1" "$TMPD/h2" | cut -d: -f1 | sort -un >"$TMPD/hl" || { internal "sort failed ($shown)"; return; }
	emit_hits "$TMPD/hl" "$shown" fqdn "$msg"
}

# scan_file path displaypath fixture(0|1)
scan_file() {
	local f=$1 shown=$2 fixture=$3 i n tag flags re
	n=${#RULE_TAGS[@]}
	i=0
	while [ "$i" -lt "$n" ]; do
		tag=${RULE_TAGS[$i]}
		if [ "${RULE_SCOPES[$i]}" = fixture ] && [ "$fixture" != 1 ]; then
			i=$((i + 1))
			continue
		fi
		flags=${RULE_FLAGS[$i]}
		re=${RULE_RES[$i]}
		if [ "$tag" = fqdn ]; then
			scan_fqdn "$f" "$shown" "${RULE_MSGS[$i]}" "$flags" "$re"
			i=$((i + 1))
			continue
		fi
		grep_to "$TMPD/hits" "$shown $tag" -a -n $flags -e "$re" -- "$f"
		emit_hits "$TMPD/hits" "$shown" "$tag" "${RULE_MSGS[$i]}"
		i=$((i + 1))
	done
	if [ "$fixture" = 1 ]; then scan_host_keys "$f" "$shown"; fi
}

# A host, instance or id value is a public slug (any case), a product id, a
# 32-digit hex id, or one of the exact placeholders.
host_value_ok() {
	local v=$1 w ok=1 re='^[0-9a-f]{32}$'
	in_list "$v" "$PLACEHOLDERS" && return 0
	[[ $v =~ $re ]] && return 0
	shopt -s nocasematch
	for w in $SLUGS $IDS_OK; do
		if [[ $v == "$w" ]]; then ok=0; fi
	done
	shopt -u nocasematch
	return "$ok"
}

scan_host_keys() { # path displaypath
	local hit ln v lines=() n=0 i
	grep_to "$TMPD/hostkeys" "$2 host-slug" -a -n -o -i -E -e "$HOST_KEY_RE" -- "$1"
	exec 4<"$TMPD/hostkeys" || { internal "cannot read the host-key list ($2)"; return; }
	while IFS= read -r -u 4 hit; do
		lines[n]=$hit
		n=$((n + 1))
	done
	exec 4<&-
	i=0
	while [ "$i" -lt "$n" ]; do
		hit=${lines[$i]}
		i=$((i + 1))
		ln=${hit%%:*}
		v=${hit#*:}   # drop the line number
		v=${v#*:}     # drop the key; the value is what follows the first colon
		v=${v#"${v%%[![:space:]]*}"}   # trim leading space
		while [ -n "$v" ]; do
			case ${v: -1} in , | \} | \]) v=${v%?} ;; *) break ;; esac
		done
		case $v in \"*\" | \'*\') [ "${#v}" -ge 2 ] && [ "${v:0:1}" = "${v: -1}" ] && v=${v:1:${#v}-2} ;; esac
		if [ -z "$v" ]; then
			report "$2" "$ln" host-slug "host or id key has an empty, list or block value"
		elif ! host_value_ok "$v"; then
			report "$2" "$ln" host-slug "host or id value is not a public slug or known id"
		fi
	done
}

# count_nul LISTFILE: print the number of NUL-terminated entries.
count_nul() {
	local n
	n=$(tr -cd '\0' <"$1" | wc -c) || return 1
	n=${n// /}
	[[ $n =~ ^[0-9]+$ ]] || return 1
	printf '%s' "$n"
}

# scan_name path fixture(0|1): run the content rules over a tracked path, so
# an address or a tailnet name cannot hide in a file name.
scan_name() {
	printf '%s\n' "$1" >"$TMPD/name" || { internal "cannot write the name file"; return; }
	scan_file "$TMPD/name" "$1" "$2"
}

# scan_list LISTFILE mode: LISTFILE holds NUL-terminated paths. Mode
# "tracked" decides fixture scope per path, mode "fixtures" treats every
# file as a fixture. Every listed path is accounted for: scanned, reported,
# or skipped as the planted directory (checked on its own). The count must
# match, or the run is an internal error.
scan_list() {
	local lst=$1 mode=$2 f expected handled=0
	expected=$(count_nul "$lst") || { internal "cannot count the file list"; return; }
	exec 3<"$lst" || { internal "cannot open the file list"; return; }
	while IFS= read -r -d '' -u 3 f; do
		handled=$((handled + 1))
		if [ "$mode" = tracked ]; then
			case "$f" in testdata/scrub/*) continue ;; esac
			if is_fixture_path "$f"; then scan_name "$f" 1; else scan_name "$f" 0; fi
		fi
		if [ -L "$f" ]; then
			report "$f" 1 symlink "symbolic link is not scanned; replace it with the file"
			continue
		fi
		if [ -d "$f" ]; then
			report "$f" 1 symlink "directory or submodule entry is not scanned"
			continue
		fi
		if [ ! -e "$f" ]; then
			internal "a listed path is missing on disk (restore or stage the deletion)"
			continue
		fi
		if [ ! -f "$f" ]; then
			report "$f" 1 symlink "not a regular file"
			continue
		fi
		if [ "$mode" = fixtures ]; then
			scan_file "$f" "$f" 1
		elif is_fixture_path "$f"; then
			scan_file "$f" "$f" 1
		else
			scan_file "$f" "$f" 0
		fi
	done
	exec 3<&-
	if [ "$handled" -ne "$expected" ]; then
		internal "accounted for $handled of $expected listed paths"
	fi
}

sha256_of() {
	if command -v shasum >/dev/null 2>&1; then
		shasum -a 256 -- "$1" | cut -d' ' -f1
	else
		sha256sum -- "$1" | cut -d' ' -f1
	fi
}

# check_planted_pins DIR: DIR must hold exactly the pinned files, unchanged.
check_planted_pins() {
	local d=$1 f name want got pin seen=" " lst expected handled=0 re='^[0-9a-f]{64}$'
	if [ ! -d "$d" ]; then
		report "$d" 1 planted-pin "planted directory is missing"
		return
	fi
	lst=$(mktemp "$TMPD/list.XXXXXX") || { internal "mktemp"; return; }
	find "$d" ! -type d -print0 >"$lst" 2>"$TMPD/err" || { internal "find failed ($d)"; return; }
	expected=$(count_nul "$lst") || { internal "cannot count the planted list"; return; }
	exec 6<"$lst" || { internal "cannot open the planted list"; return; }
	while IFS= read -r -d '' -u 6 f; do
		handled=$((handled + 1))
		name=${f#"$d"/}
		if [ -L "$f" ] || [ ! -f "$f" ]; then
			report "$f" 1 planted-pin "link or special file in the planted directory"
			continue
		fi
		want=""
		for pin in $PLANTED_PINS; do
			[ "${pin%%:*}" = "$name" ] && want=${pin#*:}
		done
		if [ -z "$want" ]; then
			report "$f" 1 planted-pin "unexpected file in the planted directory"
			continue
		fi
		got=$(sha256_of "$f") || { internal "hash failed ($f)"; continue; }
		[[ $got =~ $re ]] || { internal "hash output malformed ($f)"; continue; }
		[ "$got" = "$want" ] || report "$f" 1 planted-pin "planted file changed"
		seen="$seen$name "
	done
	exec 6<&-
	[ "$handled" -eq "$expected" ] || internal "accounted for $handled of $expected planted paths"
	for pin in $PLANTED_PINS; do
		case "$seen" in *" ${pin%%:*} "*) ;; *) report "$d/${pin%%:*}" 1 planted-pin "planted file is missing" ;; esac
	done
}

scan_tracked() {
	local lst
	lst=$(mktemp "$TMPD/list.XXXXXX") || { internal "mktemp"; return; }
	git -c core.quotePath=false ls-files -z >"$lst" 2>"$TMPD/err" || { internal "git ls-files failed"; return; }
	if [ ! -s "$lst" ]; then
		internal "git lists no tracked files"
		return
	fi
	scan_list "$lst" tracked
}

scan_dir() { # dir: every file is a fixture
	local lst
	lst=$(mktemp "$TMPD/list.XXXXXX") || { internal "mktemp"; return; }
	find "$1" ! -type d -print0 >"$lst" 2>"$TMPD/err" || { internal "find failed ($1)"; return; }
	scan_list "$lst" fixtures
}

# Only example.overlay.yaml may be tracked; real overlays hold addresses.
check_overlay() { # repo root
	local f base lst expected handled=0
	lst=$(mktemp "$TMPD/list.XXXXXX") || { internal "mktemp"; return; }
	git -C "$1" -c core.quotePath=false ls-files -z >"$lst" 2>"$TMPD/err" || { internal "git ls-files failed ($1)"; return; }
	expected=$(count_nul "$lst") || { internal "cannot count the overlay list"; return; }
	exec 7<"$lst" || { internal "cannot open the overlay list"; return; }
	shopt -s nocasematch
	while IFS= read -r -d '' -u 7 f; do
		handled=$((handled + 1))
		base=${f##*/}
		case "$base" in
		example.overlay.yaml) ;;
		*.overlay.yaml | *.overlay.yml) report "$f" 1 overlay "overlay file is tracked" ;;
		esac
	done
	shopt -u nocasematch
	exec 7<&-
	[ "$handled" -eq "$expected" ] || internal "accounted for $handled of $expected overlay paths"
}

# run_dir_rc DIR: scan DIR as fixtures in a subshell; print the rc
# (0 clean, 1 findings, 2 internal error) of what the scan decided.
rc_of_state() {
	if [ "$INTERNAL" -ne 0 ]; then return 2; fi
	if [ "$FINDINGS" -gt 0 ]; then return 1; fi
	return 0
}

dir_rc() { # dir
	(
		FINDINGS=0
		INTERNAL=0
		scan_dir "$1"
		rc_of_state
	) >"$TMPD/dir_rc.out" 2>&1
}

tracked_rc() { # repo dir
	(
		cd "$1" || exit 2
		FINDINGS=0
		INTERNAL=0
		scan_tracked
		check_overlay .
		rc_of_state
	) >"$TMPD/tracked_rc.out" 2>&1
}

# rc_at N ARGS...: run this script under ulimit -n N and print its exit code
# (99 when the limit cannot be set, for example above the caller's own).
rc_at() {
	local n=$1 rc
	shift
	(
		ulimit -n "$n" 2>/dev/null || exit 99
		exec bash "$SELF" "$@"
	) >/dev/null 2>&1
	rc=$?
	printf '%s' "$rc"
}

selftest() {
	local fail=0 tmp want p i n rc cg ten tsn
	tmp=$(mktemp -d "${TMPDIR:-/tmp}/scrub-selftest.XXXXXX") || return 2
	trap 'rm -rf "$tmp"' RETURN
	FINDINGS=0
	INTERNAL=0
	TAGS_SEEN=" "
	# Address literals are built here so this file never holds one.
	cg="100.64.$((6 + 6)).34"
	ten="1$((0 * 7))"
	ten="$ten.1.2.3"
	tsn="box.TS.N""ET"

	# 1. The planted files must fail, and every file rule must fire on them.
	if [ ! -d testdata/scrub ]; then
		echo "selftest: testdata/scrub is missing" >&2
		return 2
	fi
	scan_dir testdata/scrub >"$tmp/planted.out"
	if [ "$FINDINGS" -eq 0 ]; then
		echo "selftest FAIL: planted input produced no findings"
		fail=1
	fi

	# Each planted file must fail on its own. planted-id.json holds only a
	# github.com "$id" outside the two allowed repos, and a bare example.com.
	for p in testdata/scrub/*; do
		[ -f "$p" ] || continue
		FINDINGS=0
		mkdir "$tmp/one"
		cp "$p" "$tmp/one/"
		scan_dir "$tmp/one" >/dev/null
		rm -rf "$tmp/one"
		if [ "$FINDINGS" -eq 0 ]; then
			echo "selftest FAIL: $p passed the scrub"
			fail=1
		fi
	done
	FINDINGS=0
	scan_dir testdata/scrub >/dev/null

	# 2. Shapes kept out of tracked files are generated here.
	mkdir "$tmp/gen"
	printf 'k = ghp_%s\n' "$(printf 'a%.0s' $(seq 1 36))" >"$tmp/gen/github.txt"
	printf 'k = xoxb-%s\n' "1234567890-abcdefghij" >"$tmp/gen/slack.txt"
	printf '%s%s%s\n' '-----BEGIN ' 'RSA PRIVATE KEY' '-----' >"$tmp/gen/key.txt"
	printf '{"%s": "x"}\n' '\u0068ost' >"$tmp/gen/escape.json"
	scan_dir "$tmp/gen" >"$tmp/gen.out"

	printf 'bin\0 100.66.%s.2\n' 1 >"$tmp/gen/blob.bin"
	scan_dir "$tmp/gen" >"$tmp/gen2.out"
	for want in cgnat rfc1918-10 rfc1918-172 rfc1918-192 tailnet-ula ts-net token-github token-slack token-aws token-privkey token-bearer token-generic fqdn host-slug json-escape; do
		case "$TAGS_SEEN" in
		*" $want "*) ;;
		*)
			echo "selftest FAIL: rule $want did not fire"
			fail=1
			;;
		esac
	done

	# 3. A clean control with boundary values must produce nothing.
	FINDINGS=0
	mkdir "$tmp/clean"
	cat >"$tmp/clean/control.txt" <<'EOF'
100.63.255.1 100.128.0.1 172.15.0.1 172.32.0.1 11.0.0.1 1.100.64.2.3
192.0.2.1 198.51.100.7 203.0.113.9 127.0.0.1 localhost
example.invalid host.example.invalid
"$schema": "https://json-schema.org/draft/2020-12/schema"
"$id": "https://github.com/Hedronite/vigil/schema/vigil.summary.v0.schema.json"
  "$id":   "https://github.com/Hedronite/simplex/schema/simplex.probe.v0.schema.json"
host: castle
Host: Castle
HOST: 'tower'
"host": "box"
'host': 'box'
"instance": "lathe"
"id": "simplex"
"id": "00000000000000000000000000000000"
"id": "XYZ"
- id: apiary
{"Id": "citadel", "host": "box"}
"kind": "deploy.start", "source": "ci.build"
"generated_at": "2026-11-13T18:00:00.123456Z"
tool 1.27.3, schema v0.1.0, file config.yaml and document.json
"apiVersion": "vigil.summary/v0", "n": 1
{"apiVersion": "vigil.sources/v0"} {"apiVersion": "vigil.targets/v0"} {"apiVersion": "vigil.query/v0"}
{"apiVersion": "vigil.health/v0"} {"apiVersion": "vigil.config/v0"} {"apiVersion": "vigil.overlay/v0"}
{"apiVersion": "vigil.error/v0"} {"apiVersion": "vigil.signal/v0"} {"apiVersion": "simplex.probe/v0"}
{"apiVersion": "augur.verdict/v0"} {"apiVersion": "scry.status/v0"} {"apiVersion": "cubit.status/v1"}
apiVersion: vigil.config/v0
apiVersion: 'vigil.overlay/v0'
  apiVersion:   vigil.query/v0
listen: localhost:10082
{"hostname": "x", "ghost": "y", "localhost": "z", "hosts": ["castle"]}
{"s": "caf\u00e9 \u0001 \u007f \u0000 \n \t"}
s: "\x00 \x01 \x1f \x7f \x80 \xff \U00000001 \U0000007f \U0001F600 \U000000e9 \u00e9"
EOF
	scan_dir "$tmp/clean" >"$tmp/clean.out"
	if [ "$FINDINGS" -ne 0 ] || [ "$INTERNAL" -ne 0 ]; then
		echo "selftest FAIL: clean control produced findings or an internal error:"
		cat "$tmp/clean.out"
		fail=1
	fi

	# 4. Fail closed on a large tree: hundreds of files must scan with no
	# internal error (descriptor leaks on bash 3.2 once made the scan abort
	# yet report clean).
	INTERNAL=0
	FINDINGS=0
	mkdir "$tmp/big"
	i=0
	while [ "$i" -lt 600 ]; do
		printf 'host: castle\n{"id": "box"}\n' >"$tmp/big/f$i.json"
		i=$((i + 1))
	done
	scan_dir "$tmp/big" >"$tmp/big.out" 2>"$tmp/big.err"
	if [ "$INTERNAL" -ne 0 ] || [ "$FINDINGS" -ne 0 ]; then
		echo "selftest FAIL: 600-file tree did not scan clean (internal=$INTERNAL findings=$FINDINGS)"
		fail=1
	fi

	# 5. A scan that cannot run must not read as clean.
	INTERNAL=0
	scan_dir "$tmp/does-not-exist" >/dev/null 2>"$tmp/missing.err"
	if [ "$INTERNAL" -eq 0 ]; then
		echo "selftest FAIL: scanning a missing directory was not an internal error"
		fail=1
	fi
	INTERNAL=0

	# 5b. The planted directory is pinned: pristine passes; a changed, an
	# extra, a linked or a missing file fails.
	FINDINGS=0
	check_planted_pins testdata/scrub >/dev/null
	if [ "$FINDINGS" -ne 0 ]; then
		echo "selftest FAIL: the planted directory does not match its pins"
		fail=1
	fi
	mkdir "$tmp/pin"
	cp testdata/scrub/* "$tmp/pin/"
	printf 'extra\n' >>"$tmp/pin/planted-bad.txt"
	FINDINGS=0
	check_planted_pins "$tmp/pin" >/dev/null
	[ "$FINDINGS" -ge 1 ] || { echo "selftest FAIL: a changed planted file was not caught"; fail=1; }
	cp testdata/scrub/planted-bad.txt "$tmp/pin/planted-bad.txt"
	printf 'x\n' >"$tmp/pin/parked.txt"
	FINDINGS=0
	check_planted_pins "$tmp/pin" >/dev/null
	[ "$FINDINGS" -ge 1 ] || { echo "selftest FAIL: an extra planted file was not caught"; fail=1; }
	rm "$tmp/pin/parked.txt"
	ln -s nowhere "$tmp/pin/link"
	FINDINGS=0
	check_planted_pins "$tmp/pin" >/dev/null
	[ "$FINDINGS" -ge 1 ] || { echo "selftest FAIL: a link in the planted directory was not caught"; fail=1; }
	rm "$tmp/pin/link" "$tmp/pin/planted-id.json"
	FINDINGS=0
	check_planted_pins "$tmp/pin" >/dev/null
	[ "$FINDINGS" -ge 1 ] || { echo "selftest FAIL: a missing planted file was not caught"; fail=1; }

	# 6. Overlay check in a throwaway repo.
	git init -q "$tmp/repo" || return 2
	: >"$tmp/repo/example.overlay.yaml"
	: >"$tmp/repo/prod.overlay.yaml"
	git -C "$tmp/repo" add -A
	FINDINGS=0
	TAGS_SEEN=" "
	check_overlay "$tmp/repo" >"$tmp/overlay.out"
	if [ "$FINDINGS" -ne 1 ]; then
		echo "selftest FAIL: overlay check expected 1 finding, got $FINDINGS"
		fail=1
	fi
	git -C "$tmp/repo" rm -q -f --cached prod.overlay.yaml
	FINDINGS=0
	check_overlay "$tmp/repo" >"$tmp/overlay2.out"
	if [ "$FINDINGS" -ne 0 ]; then
		echo "selftest FAIL: overlay check flagged only the example file"
		fail=1
	fi

	# 7. Tracked paths with accents, quotes or newlines are scanned (git
	# prints such names C-quoted unless told not to); links are findings.
	mkdir "$tmp/trk"
	git init -q "$tmp/trk/ok" || return 2
	mkdir "$tmp/trk/ok/internal"
	printf 'package x\n' >"$tmp/trk/ok/internal/plain.go"
	git -C "$tmp/trk/ok" add -A
	tracked_rc "$tmp/trk/ok"
	[ "$?" -eq 0 ] || { echo "selftest FAIL: a clean tracked tree did not scan clean"; cat "$TMPD/tracked_rc.out"; fail=1; }
	i=0
	for want in $'internal/caf\303\251.go' 'internal/a"b.go' $'internal/a\nb.go' 'internal/sp ace.go' '-dash.go'; do
		i=$((i + 1))
		git init -q "$tmp/trk/n$i" || return 2
		mkdir -p "$tmp/trk/n$i/internal"
		printf 'x = "%s"\n' "$cg" >"$tmp/trk/n$i/$want"
		git -C "$tmp/trk/n$i" add -A
		tracked_rc "$tmp/trk/n$i"
		[ "$?" -eq 1 ] || { echo "selftest FAIL: an address in a tracked file with an odd name ($i) was not a finding"; fail=1; }
	done
	git init -q "$tmp/trk/up" || return 2
	mkdir "$tmp/trk/up/docs"
	printf 'ssh %s\n' "$tsn" >"$tmp/trk/up/docs/runbook.md"
	git -C "$tmp/trk/up" add -A
	tracked_rc "$tmp/trk/up"
	[ "$?" -eq 1 ] || { echo "selftest FAIL: an upper-case tailnet name in a tracked file was not a finding"; fail=1; }
	i=0
	for want in "$cg.txt" "internal/$ten.go" "docs/$tsn.md" "testdata/case/prom.corp.de.json" "docs/$ten"; do
		i=$((i + 1))
		git init -q "$tmp/trk/nm$i" || return 2
		mkdir -p "$tmp/trk/nm$i/docs" "$tmp/trk/nm$i/internal" "$tmp/trk/nm$i/testdata/case"
		printf 'clean\n' >"$tmp/trk/nm$i/$want"
		git -C "$tmp/trk/nm$i" add -A
		tracked_rc "$tmp/trk/nm$i"
		[ "$?" -eq 1 ] || { echo "selftest FAIL: an address or hostname in a tracked file name ($i) was not a finding"; fail=1; }
	done
	git init -q "$tmp/trk/ov" || return 2
	: >"$tmp/trk/ov/prod-caf$(printf '\303\251').overlay.yaml"
	git -C "$tmp/trk/ov" add -A
	tracked_rc "$tmp/trk/ov"
	[ "$?" -eq 1 ] || { echo "selftest FAIL: an overlay with an accented name was not a finding"; fail=1; }
	git init -q "$tmp/trk/dl" || return 2
	ln -s "$ten" "$tmp/trk/dl/dangling"
	git -C "$tmp/trk/dl" add -A
	tracked_rc "$tmp/trk/dl"
	[ "$?" -eq 1 ] || { echo "selftest FAIL: a tracked dangling symlink was not a finding"; fail=1; }
	git init -q "$tmp/trk/ln" || return 2
	printf 'ok\n' >"$tmp/trk/ln/real.txt"
	ln -s real.txt "$tmp/trk/ln/alias.txt"
	git -C "$tmp/trk/ln" add -A
	tracked_rc "$tmp/trk/ln"
	[ "$?" -eq 1 ] || { echo "selftest FAIL: a tracked symlink to a file was not a finding"; fail=1; }

	# 8. Each shape below must be a finding in a fixture directory.
	i=0
	while IFS= read -r want; do
		i=$((i + 1))
		mkdir "$tmp/shape$i"
		printf '%s\n' "$want" >"$tmp/shape$i/case.json"
		dir_rc "$tmp/shape$i"
		[ "$?" -eq 1 ] || { echo "selftest FAIL: shape $i passed the scrub: $(printf '%s' "$want" | tr -c '[:print:]' '?' | cut -c1-6)..."; fail=1; }
	done <<'EOF'
{"domain": ".prom.corp.de"}
{"domain": ".prom.corp.com"}
{"domain": ".prom.lab.de"}
{"ho\u0073t": "somemachine"}
{"\u0068ost": "castle"}
{"a": "\u0031"}
url: "prom\x2ecorp\x2ede"
"\x68ost": somemachine
"\U00000068ost": somemachine
{"a": "\U00000031"}
host: 'somemachine'
{"Host": "somemachine"}
HOST: somemachine
{"host": "SOMEMACHINE"}
{"host": 'somemachine'}
{"id": ["somemachine"]}
{"id": "some machine"}
{"id":
{"$schema": "https://json-schema.org/draft/2020-12/schema.prom.corp.de"}
{"$schema": "https://json-schema.org/draft/2020-12/schema/x"}
{"url": "https://json-schema.org/draft/2020-12/schema"}
{"$id": "https://github.com/Hedronite/vigil/@prom.corp.de/x"}
{"$id": "https://github.com/Hedronite/vigil/schema/v0/x.json"}
{"$id": "https://github.com/Hedronite/vigil/schema/x.json.prom.corp.de"}
{"$id": "https://github.com/Hedronite/vigil/other/x.json"}
{"$id": "https://github.com/Hedronite/vigil/schema/x.json", "n": "prom.corp.de"}
{"note": "vigil.summary/v0"}
{"x": "simplex.probe/v0", "apiVersion": "vigil.summary/v0"}
{"apiVersion": "vigil.summary/v0", "note": "vigil.config/v0"}
{"apiVersion": "acme.summary/v0"}
{"apiVersion": "vigil.Summary/v0"}
{"apiVersion": "vigil.summary"}
{"apiVersion": "vigil.summary/v0.prom.corp.de"}
{"apiVersion": "vigil.summary/v0".prom.corp.de}
{"xapiVersion": "vigil.summary/v0"}
{"a.apiVersion": "vigil.summary/v0"}
{"apiVersions": "vigil.summary/v0"}
EOF
	# The accent shape above is written as a literal here: build it for real.
	mkdir "$tmp/accent"
	printf '{"x": "caf\303\251.com"}\n' >"$tmp/accent/case.json"
	dir_rc "$tmp/accent"
	[ "$?" -eq 1 ] || { echo "selftest FAIL: an accented hostname passed the scrub"; fail=1; }
	mkdir "$tmp/yamllist"
	printf 'host:\n  - somemachine\n' >"$tmp/yamllist/case.yaml"
	dir_rc "$tmp/yamllist"
	[ "$?" -eq 1 ] || { echo "selftest FAIL: a YAML list under host: passed the scrub"; fail=1; }
	mkdir "$tmp/lnk"
	ln -s nowhere "$tmp/lnk/alias.json"
	dir_rc "$tmp/lnk"
	[ "$?" -eq 1 ] || { echo "selftest FAIL: a symlink in a scanned directory was not a finding"; fail=1; }

	# 9. The descriptor floor. Below FD_FLOOR the scan refuses to run (exit
	# 2) on every entry point; at or above it the clean tree scans clean and
	# a planted address is a finding.
	mkdir "$tmp/ul"
	printf 'x = "1"\n' >"$tmp/ul/p.txt"
	git init -q "$tmp/ulrepo" || return 2
	mkdir "$tmp/ulrepo/testdata"
	cp -R testdata/scrub "$tmp/ulrepo/testdata/scrub"
	printf 'x = "%s"\n' "$cg" >"$tmp/ulrepo/p.go"
	git -C "$tmp/ulrepo" add -A
	for n in 4 12 40 63; do
		rc=$(rc_at "$n" tree)
		[ "$rc" = 2 ] || { echo "selftest FAIL: tree scan at ulimit -n $n exited $rc, want 2"; fail=1; }
		rc=$(rc_at "$n" --scan-dir "$tmp/ul")
		[ "$rc" = 2 ] || { echo "selftest FAIL: --scan-dir at ulimit -n $n exited $rc, want 2"; fail=1; }
	done
	for n in 64 100; do
		rc=$(rc_at "$n" tree)
		case "$rc" in
		99) ;;
		0) ;;
		*) echo "selftest FAIL: clean tree at ulimit -n $n exited $rc, want 0"; fail=1 ;;
		esac
		rc=$( (export SCRUB_ROOT="$tmp/ulrepo"; rc_at "$n" tree) )
		case "$rc" in
		99) ;;
		1) ;;
		*) echo "selftest FAIL: planted address at ulimit -n $n exited $rc, want 1"; fail=1 ;;
		esac
	done

	if [ "$fail" -eq 0 ]; then
		echo "selftest ok: every rule fires on planted input, clean control passes, odd paths and links are findings, the descriptor floor holds"
		return 0
	fi
	return 1
}

finish() {
	if [ "$INTERNAL" -ne 0 ]; then
		echo "scrub: did not complete; treating as failure" >&2
		exit 2
	fi
	if [ "$FINDINGS" -gt 0 ]; then
		echo "scrub: $FINDINGS finding(s)" >&2
		exit 1
	fi
	echo "scrub: clean"
}

cmd=${1:-tree}
# SCRUB_ROOT is for the self-test: it points the tree scan at a throwaway repo.
cd "${SCRUB_ROOT:-$SELF_DIR/..}" || exit 2

case "$cmd" in
tree)
	scan_tracked
	check_overlay .
	check_planted_pins testdata/scrub
	finish
	;;
--scan-dir)
	[ $# -eq 2 ] && [ -d "$2" ] || {
		echo "usage: scrub.sh --scan-dir DIR" >&2
		exit 2
	}
	scan_dir "$2"
	finish
	;;
--selftest)
	selftest
	rc=$?
	if [ "$rc" -eq 0 ] && [ "$INTERNAL" -ne 0 ]; then rc=2; fi
	exit "$rc"
	;;
*)
	echo "usage: scrub.sh [--scan-dir DIR | --selftest]" >&2
	exit 2
	;;
esac
