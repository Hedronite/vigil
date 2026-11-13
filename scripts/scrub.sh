#!/usr/bin/env bash
# scripts/scrub.sh: keep addresses, machine names and secrets out of the repo.
#
# Usage:
#   scripts/scrub.sh                 scan every tracked file, check overlays
#   scripts/scrub.sh --scan-dir DIR  scan every file under DIR as a fixture
#   scripts/scrub.sh --selftest      prove each rule fires on planted input
#
# Exit: 0 clean, 1 findings, 2 usage or internal error.
#
# Findings print as "path:line: [rule] message". The matched text is never
# printed, so a leak does not also land in a public CI log.
#
# Rules marked "fixture" apply to testdata/**, example*.yaml and files under
# testdata only. testdata/scrub/ is skipped in a tree scan: it holds the
# planted input the self-test expects to fail.
#
# Coverage limit: the machine-name rule checks the values of "host" and
# "instance" keys against the six public slugs. A real name placed under
# another key is not seen here; review covers that.
#
# Written for bash 3.2 (macOS runners): no mapfile, no associative arrays.

set -u
set -o pipefail

SLUGS="castle tower citadel apiary lathe box unmapped"
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
TAGS_SEEN=" "

report() { # path line tag message
	printf '%s:%s: [%s] %s\n' "$1" "$2" "$3" "$4"
	FINDINGS=$((FINDINGS + 1))
	case "$TAGS_SEEN" in *" $3 "*) ;; *) TAGS_SEEN="$TAGS_SEEN$3 " ;; esac
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

# scan_file path displaypath fixture(0|1)
scan_file() {
	local f=$1 shown=$2 fixture=$3 i n ln m tag flags re
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
			# Blank out the exact allowed values, then look for hostnames in
			# what is left. Substitution keeps line numbers.
			while IFS= read -r hit; do
				report "$shown" "${hit%%:*}" "$tag" "${RULE_MSGS[$i]}"
			done < <(sed -E -e "$FQDN_STRIP_SCHEMA" -e "$FQDN_STRIP_ID" -- "$f" 2>/dev/null |
				grep -I -n -o $flags -e "$re" 2>/dev/null)
		else
			while IFS= read -r ln; do
				report "$shown" "$ln" "$tag" "${RULE_MSGS[$i]}"
			done < <(grep -I -n $flags -e "$re" -- "$f" 2>/dev/null | cut -d: -f1)
		fi
		i=$((i + 1))
	done
	if [ "$fixture" = 1 ]; then scan_host_keys "$f" "$shown"; fi
}

# Values of host and instance keys must be public slugs.
scan_host_keys() { # path displaypath
	local hit ln val
	while IFS= read -r hit; do
		ln=${hit%%:*}
		val=${hit#*:}
		val=$(printf '%s' "$val" | sed -E 's/^"?(host|instance)"?[[:space:]]*:[[:space:]]*"?//')
		in_list "$val" "$SLUGS" || report "$2" "$ln" host-slug "host value is not one of the public slugs"
	done < <(grep -I -n -o -E -e '"?(host|instance)"?[[:space:]]*:[[:space:]]*"?[A-Za-z0-9_.:-]+' -- "$1" 2>/dev/null)
}

scan_tracked() {
	local f
	while IFS= read -r f; do
		[ -f "$f" ] || continue
		case "$f" in testdata/scrub/*) continue ;; esac
		if is_fixture_path "$f"; then scan_file "$f" "$f" 1; else scan_file "$f" "$f" 0; fi
	done < <(git ls-files)
}

scan_dir() { # dir: every file is a fixture
	local f
	while IFS= read -r f; do
		scan_file "$f" "$f" 1
	done < <(find "$1" -type f | LC_ALL=C sort)
}

# Only example.overlay.yaml may be tracked; real overlays hold addresses.
check_overlay() { # repo root
	local f base
	while IFS= read -r f; do
		base=${f##*/}
		case "$base" in
		example.overlay.yaml) ;;
		*.overlay.yaml | *.overlay.yml) report "$f" 1 overlay "overlay file is tracked" ;;
		esac
	done < <(git -C "$1" ls-files)
}

selftest() {
	local fail=0 tmp rc out want t
	tmp=$(mktemp -d "${TMPDIR:-/tmp}/scrub-selftest.XXXXXX") || return 2
	trap 'rm -rf "$tmp"' RETURN
	FINDINGS=0
	TAGS_SEEN=" "

	# 1. The planted file must fail, and every file rule must fire on it.
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
	TAGS_SEEN=" "
	scan_dir testdata/scrub >/dev/null

	# 2. Shapes kept out of tracked files are generated here.
	mkdir "$tmp/gen"
	printf 'k = ghp_%s\n' "$(printf 'a%.0s' $(seq 1 36))" >"$tmp/gen/github.txt"
	printf 'k = xoxb-%s\n' "1234567890-abcdefghij" >"$tmp/gen/slack.txt"
	printf '%s%s%s\n' '-----BEGIN ' 'RSA PRIVATE KEY' '-----' >"$tmp/gen/key.txt"
	scan_dir "$tmp/gen" >"$tmp/gen.out"

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
EOF
	scan_dir "$tmp/clean" >"$tmp/clean.out"
	if [ "$FINDINGS" -ne 0 ]; then
		echo "selftest FAIL: clean control produced findings:"
		cat "$tmp/clean.out"
		fail=1
	fi

	# 4. Overlay check in a throwaway repo.
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
		echo "selftest ok: every rule fires on planted input, clean control passes"
		return 0
	fi
	return 1
}

cmd=${1:-tree}
cd "$(dirname "$0")/.." || exit 2

case "$cmd" in
tree)
	scan_tracked
	check_overlay .
	if [ "$FINDINGS" -gt 0 ]; then
		echo "scrub: $FINDINGS finding(s)" >&2
		exit 1
	fi
	echo "scrub: clean"
	;;
--scan-dir)
	[ $# -eq 2 ] && [ -d "$2" ] || {
		echo "usage: scrub.sh --scan-dir DIR" >&2
		exit 2
	}
	scan_dir "$2"
	if [ "$FINDINGS" -gt 0 ]; then
		echo "scrub: $FINDINGS finding(s)" >&2
		exit 1
	fi
	echo "scrub: clean"
	;;
--selftest)
	selftest
	exit $?
	;;
*)
	echo "usage: scrub.sh [--scan-dir DIR | --selftest]" >&2
	exit 2
	;;
esac
