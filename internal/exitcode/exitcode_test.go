package exitcode

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

func TestTableCoversEveryCodeOnce(t *testing.T) {
	if len(Table) != 7 {
		t.Fatalf("table has %d entries, want 7", len(Table))
	}
	for i, e := range Table {
		if e.Code != i {
			t.Errorf("entry %d has code %d; table must be ordered 0..6", i, e.Code)
		}
		if e.Name == "" || e.Meaning == "" {
			t.Errorf("entry %d is missing a name or meaning", i)
		}
	}
}

func TestVerdictSplit(t *testing.T) {
	for _, c := range []int{Green, Yellow, Red} {
		if !IsVerdict(c) {
			t.Errorf("code %d should be a verdict", c)
		}
	}
	for _, c := range []int{Usage, Unavailable, Startup, Internal} {
		if IsVerdict(c) {
			t.Errorf("code %d is an error and must not be a verdict", c)
		}
	}
}

func TestFromSeverity(t *testing.T) {
	cases := map[string]int{SeverityGreen: Green, SeverityYellow: Yellow, SeverityRed: Red}
	for name, want := range cases {
		got, ok := FromSeverity(name)
		if !ok || got != want {
			t.Errorf("FromSeverity(%q) = %d,%v; want %d,true", name, got, ok, want)
		}
	}
	for _, bad := range []string{"", "green", "UNKNOWN", "info"} {
		if _, ok := FromSeverity(bad); ok {
			t.Errorf("FromSeverity(%q) accepted an unknown severity", bad)
		}
	}
}

// docs/semantics.md prints the exit table. Every row must match Table, and
// no row may be missing or extra.
func TestSemanticsDocMatchesTable(t *testing.T) {
	raw, err := os.ReadFile("../../docs/semantics.md")
	if err != nil {
		t.Fatalf("read semantics doc: %v", err)
	}
	row := regexp.MustCompile(`(?m)^\| ([0-9]) \| (.+) \|$`)
	found := map[int]bool{}
	for _, m := range row.FindAllStringSubmatch(string(raw), -1) {
		code, _ := strconv.Atoi(m[1])
		found[code] = true
	}
	for _, e := range Table {
		if !found[e.Code] {
			t.Errorf("docs/semantics.md has no row for exit code %d", e.Code)
		}
	}
	if len(found) != len(Table) {
		t.Errorf("docs/semantics.md has %d exit rows, Table has %d", len(found), len(Table))
	}
}
