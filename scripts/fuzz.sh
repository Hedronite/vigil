#!/usr/bin/env bash
# scripts/fuzz.sh: run every fuzz target in the tree for a short, fixed time.
#
#   scripts/fuzz.sh              run each target for $FUZZTIME (default 20s)
#   scripts/fuzz.sh --list       print the targets found
#   scripts/fuzz.sh --selftest   prove a missing or renamed target is a failure
#
# Go runs one fuzz target per invocation, so this finds every "func FuzzX" in a
# _test.go file and runs each in its own package. The list in EXPECTED is the
# floor: a target that is deleted or renamed is a failure, not a quiet pass.
# Fuzzing is blocking in CI: a crasher fails the job, and the failing input is
# written under testdata/fuzz/ in the package, where the next run replays it.
#
# Exit: 0 every target ran and passed, 1 a target failed or is missing, 2 setup.

set -u
set -o pipefail

cd "$(dirname "$0")/.." || exit 2

: "${FUZZTIME:=20s}"
: "${EXPECTED:=FuzzConfig FuzzCLIArgs FuzzBase FuzzPath}"

# found: "dir name" lines, one per target.
find_targets() {
	grep -rEoH --include='*_test.go' '^func Fuzz[A-Za-z0-9_]*' cmd internal 2>/dev/null |
		while IFS= read -r line; do
			file=${line%%:*}
			name=${line##*func }
			printf '%s %s\n' "$(dirname "$file")" "$name"
		done | sort
}

# check_expected FOUND: every name in EXPECTED must be in FOUND.
check_expected() {
	local found=$1 want missing=0
	for want in $EXPECTED; do
		case "
$found
" in
		*" $want
"*) ;;
		*)
			echo "fuzz: expected target $want was not found" >&2
			missing=1
			;;
		esac
	done
	return "$missing"
}

if [ "${1:-}" = "--selftest" ]; then
	fail=0
	found=$(find_targets) || exit 2
	if ! EXPECTED="$EXPECTED" check_expected "$found" 2>/dev/null; then
		echo "fuzz selftest FAIL: the real tree is missing an expected target"
		fail=1
	fi
	if EXPECTED="FuzzDoesNotExist" check_expected "$found" 2>/dev/null; then
		echo "fuzz selftest FAIL: a missing target was not caught"
		fail=1
	fi
	# A name that is only the start of a real target (FuzzConf of FuzzConfig)
	# or only its end (zzConfig) must not count as found. An unanchored
	# matcher passes the first with a leading anchor missing and the second
	# with a trailing one missing, so both are planted.
	if EXPECTED="FuzzConf" check_expected "$found" 2>/dev/null; then
		echo "fuzz selftest FAIL: a prefix of a target counted as the target"
		fail=1
	fi
	if EXPECTED="zzConfig" check_expected "$found" 2>/dev/null; then
		echo "fuzz selftest FAIL: a suffix of a target counted as the target"
		fail=1
	fi
	if EXPECTED="FuzzConfigX" check_expected "$found" 2>/dev/null; then
		echo "fuzz selftest FAIL: a longer name counted as the target"
		fail=1
	fi
	[ "$fail" -eq 0 ] && echo "fuzz selftest ok: every expected target is found, a missing one fails"
	exit "$fail"
fi

found=$(find_targets) || exit 2
if [ "${1:-}" = "--list" ]; then
	printf '%s\n' "$found"
	exit 0
fi
[ -n "$found" ] || { echo "fuzz: no fuzz targets found" >&2; exit 1; }
check_expected "$found" || exit 1

status=0
ran=0
while read -r dir name; do
	echo "== fuzz $name ($dir) for $FUZZTIME"
	if go test -run "^$" -fuzz "^${name}\$" -fuzztime "$FUZZTIME" "./$dir" </dev/null; then
		ran=$((ran + 1))
	else
		echo "fuzz: $name FAILED" >&2
		status=1
	fi
done <<EOT
$found
EOT
echo "fuzz: ran $ran target(s)"
exit "$status"
