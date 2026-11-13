#!/usr/bin/env bash
# scripts/scrub.sh: keep addresses, machine names and secrets out of the repo.
#
# Usage:
#   scripts/scrub.sh                 scan every tracked file, check overlays
#   scripts/scrub.sh --scan-dir DIR  scan every file under DIR as a fixture
#   scripts/scrub.sh --selftest      prove each rule fires on planted input
#
# Exit: 0 clean, 1 findings, 2 usage or internal error. The script fails
# closed: any grep, sed or git call that errors makes the run exit 2, never 0.
#
# Findings print as "path:line: [rule] message". The matched text is never
# printed, so a leak does not also land in a public CI log.
#
# Rules marked "fixture" apply to testdata/**, example*.yaml and files under
# testdata only. testdata/scrub/ is skipped in a tree scan: it holds the
# planted input the self-test expects to fail.
#
# Coverage limits: the machine-name rule checks the values of "host",
# "instance" and "id" keys against the public slugs, the product ids and
# hex ids. The hostname rule treats any dotted name in a fixture as a
# hostname, after stripping schema names, file names, version numbers and
# the values of "kind" and "source" (open vocabularies that allow dots; the
# TLD rule still runs over those). A real name placed under any other key
# with no dot is not seen here; review covers that.
#
# testdata/scrub/ is pinned by content hash (PLANTED_PINS): a changed,
# added or missing file there fails the scan, so nothing real can hide in
# the directory the tree scan skips. Update the pins in the same commit as
# any deliberate change to a planted file.
#
# Binary files are scanned as text (grep -a).
#
# Written for bash 3.2 (macOS runners): no mapfile, no associative arrays, and
# no process substitution, which leaks descriptors in a long loop there.
# Every loop reads a plain temp file instead.

set -u
set -o pipefail

SLUGS="castle tower citadel apiary lathe box unmapped"
# Ids that are not machines: the authorities, sources and backends.
IDS_OK="simplex augur cubit scry grafana prom"

PLANTED_PINS="planted-bad.txt:5c1ba7b6a28889d0581321057c5beaf2637ff6fca655340e85bcba664c61949c planted-id.json:43f52fbced451f761d49a4584cff16b5b89ce7c7cb3906e933979a15dcafeab4"

# Deny-by-default hostname shape: two or more labels joined by dots.
FQDN_ANY_RE='(^|[^A-Za-z0-9_.-])[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)+([^A-Za-z0-9_-]|$)'
# Exact values the fixture hostname rule lets through (PM ruling, G1):
#   the 2020-12 meta-schema URL, "$id" values under the two Hedronite repos
#   the schemas live in, and the reserved example.invalid (not a listed TLD,
#   so it never matches). Nothing else, including example.com.
FQDN_STRIP_SCHEMA='s#https://json-schema\.org/draft/2020-12/schema##g'
FQDN_STRIP_ID='s#"\$id"[[:space:]]*:[[:space:]]*"https://github\.com/Hedronite/(vigil|simplex)/[^"]*"##g'

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
add_rule ts-net all -E '[A-Za-z0-9-]+\.ts\.net' "tailnet hostname"
add_rule token-github all -E '(gh[pousr]_[A-Za-z0-9]{36}|github_pat_[A-Za-z0-9_]{22,})' "GitHub token shape"
add_rule token-slack all -E 'xox[abprs]-[A-Za-z0-9-]{10,}' "Slack token shape"
add_rule token-aws all -E 'AKIA[0-9A-Z]{16}' "AWS key id shape"
add_rule token-privkey all -E '-----BEGIN [A-Z ]*PRIVATE KEY-----' "private key block"
add_rule token-bearer all -E '[Bb]earer[[:space:]]+[A-Za-z0-9._~+/-]{24,}' "bearer credential"
add_rule token-generic all -Ei '(token|secret|passw(or)?d|api[_-]?key)["'"'"']?[[:space:]]*[:=][[:space:]]*["'"'"']?[A-Za-z0-9+_-]{32,}' "credential assignment"
add_rule fqdn fixture -Ei '(^|[^A-Za-z0-9.-])[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9-]+)*\.(com|net|org|io|dev|app|ai|co|me|xyz|cloud|local|lan|internal|corp|home|arpa)([^A-Za-z0-9-]|$)' "hostname in fixture or example"

FINDINGS=0
INTERNAL=0
TAGS_SEEN=" "
TMPD=$(mktemp -d "${TMPDIR:-/tmp}/scrub.XXXXXX") || exit 2
trap 'rm -rf "$TMPD"' EXIT

# Names that are not hosts, blanked before the deny-by-default hostname
# check. Each command runs twice so adjacent tokens both strip.
cat >"$TMPD/strip.sed" <<'SED'
s#([0-9]{2})\.[0-9]{1,9}Z#\1Z#g
s#(^|[^A-Za-z0-9_.-])([A-Za-z0-9_-]+\.)*example\.invalid([^A-Za-z0-9_-]|$)#\1\3#g
s#(^|[^A-Za-z0-9_.-])([A-Za-z0-9_-]+\.)*example\.invalid([^A-Za-z0-9_-]|$)#\1\3#g
s#"(kind|source)"[[:space:]]*:[[:space:]]*"[a-z][a-z0-9_.-]{0,63}"#"\1":""#g
s#(^|[^A-Za-z0-9_.-])(vigil|simplex|augur|cubit|scry)\.(summary|signal|error|config|overlay|probe|verdict|status)(/v[0-9]+)?([^A-Za-z0-9._-]|$)#\1\5#g
s#(^|[^A-Za-z0-9_.-])(vigil|simplex|augur|cubit|scry)\.(summary|signal|error|config|overlay|probe|verdict|status)(/v[0-9]+)?([^A-Za-z0-9._-]|$)#\1\5#g
s#(^|[^A-Za-z0-9_.-])([A-Za-z0-9_-]+\.)+(json|yaml|yml|txt|bin|token)([^A-Za-z0-9._-]|$)#\1\4#g
s#(^|[^A-Za-z0-9_.-])([A-Za-z0-9_-]+\.)+(json|yaml|yml|txt|bin|token)([^A-Za-z0-9._-]|$)#\1\4#g
s#(^|[^A-Za-z0-9_.-])v?[0-9]+(\.[0-9]+)+([^A-Za-z0-9_-]|$)#\1\3#g
s#(^|[^A-Za-z0-9_.-])v?[0-9]+(\.[0-9]+)+([^A-Za-z0-9_-]|$)#\1\3#g
SED

report() { # path line tag message
	printf '%s:%s: [%s] %s\n' "$1" "$2" "$3" "$4"
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
	for w in $2; do [ "$w" = "$1" ] && return 0; done
	return 1
}

# grep_to OUT desc ARGS...: grep into OUT; exit 1 (no match) is fine, 2+ is an error.
grep_to() {
	local out=$1 desc=$2 rc
	shift 2
	grep "$@" >"$out" 2>"$TMPD/err"
	rc=$?
	if [ "$rc" -gt 1 ]; then
		internal "grep failed ($desc)"
		: >"$out"
	fi
}

# emit_hits FILE shown tag message: report the line number of each hit in FILE.
emit_hits() {
	local hit
	while IFS= read -r hit; do
		report "$2" "${hit%%:*}" "$3" "$4"
	done <"$1"
}

# scan_fqdn file shown message flags tld_regex
# Two passes, one report per line. Pass 1: the TLD list, run over text with
# only the exact allowlist blanked (so it also covers kind and source values).
# Pass 2: any dotted name, run after the non-host names are blanked too.
scan_fqdn() {
	local f=$1 shown=$2 msg=$3 flags=$4 re=$5
	if ! sed -E -e "$FQDN_STRIP_SCHEMA" -e "$FQDN_STRIP_ID" -- "$f" >"$TMPD/s1" 2>"$TMPD/err"; then
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

# Values of host, instance and id keys: a public slug, a product id, a
# 32-digit hex id, or an upper-case placeholder.
host_value_ok() {
	in_list "$1" "$SLUGS $IDS_OK" && return 0
	local re='^([0-9a-f]{32}|[A-Z0-9_]+)$'
	[[ $1 =~ $re ]]
}

scan_host_keys() { # path displaypath
	local hit ln val
	grep_to "$TMPD/hostkeys" "$2 host-slug" -a -n -o -E -e '(^|[^A-Za-z0-9_.$-])"?(host|instance|id)"?[[:space:]]*:[[:space:]]*"?[A-Za-z0-9_.:-]+' -- "$1"
	while IFS= read -r hit; do
		ln=${hit%%:*}
		val=${hit#*:}
		val=$(printf '%s' "$val" | sed -E 's/^.*(host|instance|id)"?[[:space:]]*:[[:space:]]*"?//')
		host_value_ok "$val" || report "$2" "$ln" host-slug "host or id value is not a public slug or known id"
	done <"$TMPD/hostkeys"
}

# scan_list LISTFILE mode: mode "tracked" decides fixture scope per path,
# mode "fixtures" treats every file as a fixture.
scan_list() {
	local f mode=$2
	while IFS= read -r f; do
		[ -f "$f" ] || continue
		if [ "$mode" = fixtures ]; then
			scan_file "$f" "$f" 1
		else
			case "$f" in testdata/scrub/*) continue ;; esac
			if is_fixture_path "$f"; then scan_file "$f" "$f" 1; else scan_file "$f" "$f" 0; fi
		fi
	done <"$1"
}

sha256_of() {
	if command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	else
		sha256sum "$1" | cut -d' ' -f1
	fi
}

# check_planted_pins DIR: DIR must hold exactly the pinned files, unchanged.
check_planted_pins() {
	local d=$1 f name want got pin seen=" " lst
	if [ ! -d "$d" ]; then
		report "$d" 1 planted-pin "planted directory is missing"
		return
	fi
	lst=$(mktemp "$TMPD/list.XXXXXX") || { internal "mktemp"; return; }
	find "$d" -type f >"$lst" 2>"$TMPD/err" || { internal "find failed ($d)"; return; }
	while IFS= read -r f; do
		name=${f#"$d"/}
		want=""
		for pin in $PLANTED_PINS; do
			[ "${pin%%:*}" = "$name" ] && want=${pin#*:}
		done
		if [ -z "$want" ]; then
			report "$f" 1 planted-pin "unexpected file in the planted directory"
			continue
		fi
		got=$(sha256_of "$f") || { internal "hash failed ($f)"; continue; }
		[ "$got" = "$want" ] || report "$f" 1 planted-pin "planted file changed"
		seen="$seen$name "
	done <"$lst"
	for pin in $PLANTED_PINS; do
		case "$seen" in *" ${pin%%:*} "*) ;; *) report "$d/${pin%%:*}" 1 planted-pin "planted file is missing" ;; esac
	done
}

scan_tracked() {
	local lst
	lst=$(mktemp "$TMPD/list.XXXXXX") || { internal "mktemp"; return; }
	git ls-files >"$lst" 2>"$TMPD/err" || { internal "git ls-files failed"; return; }
	scan_list "$lst" tracked
}

scan_dir() { # dir: every file is a fixture
	local lst
	lst=$(mktemp "$TMPD/list.XXXXXX") || { internal "mktemp"; return; }
	find "$1" -type f >"$lst" 2>"$TMPD/err" || { internal "find failed ($1)"; return; }
	LC_ALL=C sort -o "$lst" "$lst" || { internal "sort failed"; return; }
	scan_list "$lst" fixtures
}

# Only example.overlay.yaml may be tracked; real overlays hold addresses.
check_overlay() { # repo root
	local f base lst
	lst=$(mktemp "$TMPD/list.XXXXXX") || { internal "mktemp"; return; }
	git -C "$1" ls-files >"$lst" 2>"$TMPD/err" || { internal "git ls-files failed ($1)"; return; }
	while IFS= read -r f; do
		base=${f##*/}
		case "$base" in
		example.overlay.yaml) ;;
		*.overlay.yaml | *.overlay.yml) report "$f" 1 overlay "overlay file is tracked" ;;
		esac
	done <"$lst"
}

selftest() {
	local fail=0 tmp want p i
	tmp=$(mktemp -d "${TMPDIR:-/tmp}/scrub-selftest.XXXXXX") || return 2
	trap 'rm -rf "$tmp"' RETURN
	FINDINGS=0
	INTERNAL=0
	TAGS_SEEN=" "

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
	scan_dir "$tmp/gen" >"$tmp/gen.out"

	printf 'bin\0 100.66.%s.2\n' 1 >"$tmp/gen/blob.bin"
	scan_dir "$tmp/gen" >"$tmp/gen2.out"
	for want in cgnat rfc1918-10 rfc1918-172 rfc1918-192 tailnet-ula ts-net token-github token-slack token-aws token-privkey token-bearer token-generic fqdn host-slug; do
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
"host": "box"
"instance": "lathe"
"id": "simplex"
"id": "00000000000000000000000000000000"
"id": "XYZ"
"kind": "deploy.start", "source": "ci.build"
"generated_at": "2026-11-13T18:00:00.123456Z"
tool 1.27.3, schema v0.1.0, file config.yaml and document.json
"apiVersion": "vigil.summary/v0", "x": "simplex.probe/v0"
listen: localhost:10082
{"hostname": "x", "ghost": "y", "localhost": "z"}
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
	# extra, or a missing file fails.
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
	rm "$tmp/pin/parked.txt" "$tmp/pin/planted-id.json"
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

	if [ "$fail" -eq 0 ]; then
		echo "selftest ok: every rule fires on planted input, clean control passes, large and missing trees fail closed"
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
cd "$(dirname "$0")/.." || exit 2

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
