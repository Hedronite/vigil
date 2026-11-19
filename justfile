# Front-door recipes for Go-TOPS (GO_TOPS.md §4.1).
gotops := "go tool -modfile=tools/go.mod gotops"

# Validate go-tops.yaml and go-tops.lock.
check:
    {{gotops}} check

# The merge gate: every enabled layer at tier 1 and below.
gate:
    {{gotops}} gate

# Print the last report.
report:
    {{gotops}} report --format text

# Re-check every tool pin.
lock-verify:
    {{gotops}} lock --verify
