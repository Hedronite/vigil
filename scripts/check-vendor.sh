#!/usr/bin/env bash
# check-vendor.sh: the vendored Simplex files must be exactly what
# testdata/simplex/SOURCE says they are.
#
#   1. schema/simplex.probe.v0.schema.json has the sha256 SOURCE records.
#   2. testdata/simplex holds SOURCE and <case>/{document.json,exit}, nothing else.
#   3. Against the public upstream, byte for byte: the schema at schema_commit,
#      and every case file at documents_commit (upstream testdata/<case>/<file>).
#      So editing a file and its recorded digest together is still caught. A
#      case without a document.json must have none upstream either, so deleting
#      one does not pass.
# A whole case directory removed here is not detected: raw file fetches cannot
# list the upstream directory.
#
# Usage: bash scripts/check-vendor.sh [--offline]
#   --offline  skip check 3, for a machine with no network. Refused in CI.
# Exit: 0 everything matches, 1 a mismatch, 2 the check could not be made.
set -euo pipefail

raw_base=https://raw.githubusercontent.com
vendor_dir=testdata/simplex
source_file=$vendor_dir/SOURCE
schema=schema/simplex.probe.v0.schema.json

die() {
  echo "check-vendor: $*" >&2
  exit 2
}

offline=0
for arg in "$@"; do
  case $arg in
    --offline) offline=1 ;;
    *)
      echo "usage: bash scripts/check-vendor.sh [--offline]" >&2
      exit 2
      ;;
  esac
done
if [ "$offline" -eq 1 ]; then
  if [ -n "${CI:-}" ] || [ -n "${GITHUB_ACTIONS:-}" ]; then
    die "--offline is not allowed in CI: the upstream comparison must run"
  fi
  echo "check-vendor: WARNING: --offline: NOT comparing with the upstream commit" >&2
fi

cd "$(dirname "$0")/.." || die "cannot find the repository root"

if command -v sha256sum >/dev/null 2>&1; then
  digest() { sha256sum <"$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
  digest() { shasum -a 256 <"$1" | cut -d' ' -f1; }
else
  die "neither sha256sum nor shasum is available"
fi

matches() { printf '%s\n' "$1" | LC_ALL=C grep -qE "^($2)\$"; }

failed=0
fail() {
  echo "check-vendor: FAIL: $*" >&2
  failed=1
}

# SOURCE is read strictly: known keys only, each once, and a vendored list that
# names exactly what is checked below.
{ [ -f "$source_file" ] && [ ! -L "$source_file" ]; } || die "$source_file is missing or is not a regular file"
repo='' documents_commit='' schema_commit='' schema_sha256='' vendored=''
seen=' ' in_list=0
while IFS= read -r line || [ -n "$line" ]; do
  line=${line%$'\r'}
  case $line in
    '') continue ;;
    '  - '*)
      [ "$in_list" -eq 1 ] || die "$source_file: a list item outside vendored:"
      vendored="$vendored${line#  - }|"
      continue
      ;;
  esac
  in_list=0
  case $line in
    *:*) key=${line%%:*} ;;
    *) die "$source_file: a line is neither 'key: value' nor a list item" ;;
  esac
  case $key in
    repo | documents_commit | documents_note | schema_commit | schema_sha256 | vendored | excluded | note) ;;
    *) die "$source_file: unknown key '$key'" ;;
  esac
  case $seen in
    *" $key "*) die "$source_file: '$key' appears more than once" ;;
  esac
  seen="$seen$key "
  value=${line#*:}
  value=${value#"${value%%[![:space:]]*}"}
  value=${value%"${value##*[![:space:]]}"}
  case $key in
    repo) repo=$value ;;
    documents_commit) documents_commit=$value ;;
    schema_commit) schema_commit=$value ;;
    schema_sha256) schema_sha256=$value ;;
    vendored)
      [ -z "$value" ] || die "$source_file: vendored takes a list"
      in_list=1
      ;;
  esac
done <"$source_file"

# The values become URLs. A full commit id is required: a branch or tag moves.
matches "$repo" 'Hedronite/[A-Za-z0-9_-][A-Za-z0-9._-]*' || die "$source_file: repo must be a Hedronite repository"
matches "$documents_commit" '[0-9a-f]{40}' || die "$source_file: documents_commit must be 40 hex digits"
matches "$schema_commit" '[0-9a-f]{40}' || die "$source_file: schema_commit must be 40 hex digits"
matches "$schema_sha256" '[0-9a-f]{64}' || die "$source_file: schema_sha256 must be 64 hex digits"
[ "$vendored" = "$schema|$vendor_dir/<case>/document.json|$vendor_dir/<case>/exit|" ] ||
  die "$source_file: the vendored list is not what this script checks"

# Nothing else may be vendored without this script learning to check it.
while IFS= read -r other; do
  fail "$other is vendored but not covered by this script"
done < <(
  for top in schema testdata; do
    [ -d "$top" ] || continue
    find "$top" -type d -name vendor
    find "$top" -name SOURCE ! -path "$source_file"
  done | LC_ALL=C sort
)

# .DS_Store is the one file a desktop adds by itself.
while IFS= read -r entry; do
  case ${entry#"$vendor_dir"/} in
    SOURCE | .DS_Store | */.DS_Store) ;;
    */*/*) fail "$entry is not part of the vendored layout" ;;
    */document.json | */exit)
      { [ -f "$entry" ] && [ ! -L "$entry" ]; } || fail "$entry is not a regular file"
      ;;
    */*) fail "$entry is not named by $source_file" ;;
    *)
      { [ -d "$entry" ] && [ ! -L "$entry" ] && matches "${entry##*/}" '[a-z0-9][a-z0-9-]*'; } ||
        fail "$entry is not a case directory"
      ;;
  esac
done < <(find "$vendor_dir" -mindepth 1 | LC_ALL=C sort)

{ [ -f "$schema" ] && [ ! -L "$schema" ]; } || die "$schema is missing or is not a regular file"
got=$(digest "$schema")
[ "$got" = "$schema_sha256" ] || fail "$schema has sha256 $got, SOURCE records $schema_sha256"

tmp=$(mktemp -d) || die "mktemp failed"
trap 'rm -rf "$tmp"' EXIT

# fetch COMMIT PATH: leaves the body in $tmp/upstream and prints the status.
# -q ignores any curlrc. No credentials and no redirects: the repository is
# public and the answer must come from this URL.
fetch() {
  curl -q --silent --show-error --proto '=https' --max-redirs 0 \
    --retry 3 --retry-delay 2 --connect-timeout 10 --max-time 60 --max-filesize 5000000 \
    --output "$tmp/upstream" --write-out '%{http_code}' "$raw_base/$repo/$1/$2" ||
    die "cannot fetch $repo at $1: $2"
}

# same COMMIT UPSTREAM_PATH LOCAL: the local file is the upstream file.
same() {
  local status
  [ "$offline" -eq 0 ] || return 0
  rm -f "$tmp/upstream"
  status=$(fetch "$1" "$2")
  case $status in
    200) cmp -s "$tmp/upstream" "$3" || fail "$3 differs from $repo at $1: $2" ;;
    404) fail "$3 does not exist in $repo at $1: $2" ;;
    *) die "cannot fetch $repo at $1: $2 (HTTP $status)" ;;
  esac
}

# absent COMMIT UPSTREAM_PATH LOCAL: upstream has no such file either.
absent() {
  local status
  [ "$offline" -eq 0 ] || return 0
  rm -f "$tmp/upstream"
  status=$(fetch "$1" "$2")
  case $status in
    404) ;;
    200) fail "$3 is missing, but $repo at $1 has $2" ;;
    *) die "cannot fetch $repo at $1: $2 (HTTP $status)" ;;
  esac
}

same "$schema_commit" "$schema" "$schema"

documents=0 exits=0
for dir in "$vendor_dir"/*; do
  { [ -d "$dir" ] && [ ! -L "$dir" ]; } || continue
  name=${dir##*/}
  matches "$name" '[a-z0-9][a-z0-9-]*' || continue # reported above
  if [ -f "$dir/exit" ]; then
    exits=$((exits + 1))
    same "$documents_commit" "testdata/$name/exit" "$dir/exit"
  else
    fail "$dir has no exit file"
  fi
  if [ -f "$dir/document.json" ]; then
    documents=$((documents + 1))
    same "$documents_commit" "testdata/$name/document.json" "$dir/document.json"
  else
    absent "$documents_commit" "testdata/$name/document.json" "$dir/document.json"
  fi
done
# Finding nothing means the check is looking in the wrong place.
[ "$documents" -gt 0 ] || die "no vendored documents under $vendor_dir"

if [ "$failed" -ne 0 ]; then
  echo "check-vendor: FAILED" >&2
  exit 1
fi
summary="schema, $documents documents and $exits exit files"
if [ "$offline" -eq 1 ]; then
  echo "check-vendor: ok (offline: $summary present; only the schema digest compared)"
else
  echo "check-vendor: ok: $summary match $repo (schema@${schema_commit:0:12}, cases@${documents_commit:0:12})"
fi
