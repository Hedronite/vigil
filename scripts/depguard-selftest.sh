#!/usr/bin/env bash
# scripts/depguard-selftest.sh: prove the depguard rules in .golangci.yml bite.
#
# Works on a throwaway copy of the tree, so the checkout is never touched.
# Needs golangci-lint v2 and go on PATH, and network access to fetch the
# schema validator module into the copy.
#
# Expects:
#   os/exec in a non-test file              -> depguard finding
#   jsonschema in a non-test file           -> depguard finding
#   jsonschema in a _test.go file           -> no depguard finding
#   os/exec in a _test.go file              -> no depguard finding
#
# Exit: 0 all four behave, 1 a rule is wrong, 2 setup failed.

set -u
set -o pipefail

cd "$(dirname "$0")/.." || exit 2
root=$(pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/depguard-selftest.XXXXXX") || exit 2
trap 'rm -rf "$tmp"' EXIT

mkdir "$tmp/tree"
cp go.mod .golangci.yml "$tmp/tree/" || exit 2
[ -f go.sum ] && cp go.sum "$tmp/tree/"
mkdir -p "$tmp/tree/internal/probe"
cd "$tmp/tree" || exit 2

mod=$(awk '$1=="module"{print $2}' go.mod)
validator=github.com/santhosh-tekuri/jsonschema/v6

if ! go get "$validator@v6.0.3" >"$tmp/get.log" 2>&1; then
	echo "depguard-selftest: could not fetch the validator module" >&2
	cat "$tmp/get.log" >&2
	exit 2
fi

# Output is captured before grep: with pipefail, "grep -q" closing the pipe
# early would make a real finding read as a failed pipeline.
lint_has_depguard() {
	local out
	out=$(golangci-lint run --enable-only depguard ./... 2>&1)
	case "$out" in *'(depguard)'*) return 0 ;; esac
	return 1
}

fail=0
expect_hit() { # label
	if ! lint_has_depguard; then
		echo "depguard-selftest FAIL: $1 was not rejected"
		fail=1
	else
		echo "depguard-selftest ok: $1 rejected"
	fi
}
expect_clean() { # label
	if lint_has_depguard; then
		echo "depguard-selftest FAIL: $1 was rejected but tests may use it"
		fail=1
	else
		echo "depguard-selftest ok: $1 allowed"
	fi
}
reset() { rm -f internal/probe/*.go; }

reset
printf 'package probe\n\nimport _ "os/exec"\n' >internal/probe/probe.go
expect_hit "os/exec in a non-test file"

reset
printf 'package probe\n\nimport _ "%s"\n' "$validator" >internal/probe/probe.go
expect_hit "jsonschema in a non-test file"

reset
printf 'package probe\n' >internal/probe/probe.go
printf 'package probe\n\nimport _ "%s"\n' "$validator" >internal/probe/probe_test.go
expect_clean "jsonschema in a _test.go file"

reset
printf 'package probe\n' >internal/probe/probe.go
printf 'package probe\n\nimport _ "os/exec"\n' >internal/probe/probe_test.go
expect_clean "os/exec in a _test.go file"

# The real tree must be clean under the same rules.
cd "$root" || exit 2
if lint_has_depguard; then
	echo "depguard-selftest FAIL: the real tree violates depguard"
	fail=1
fi

exit "$fail"
