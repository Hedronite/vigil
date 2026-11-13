package exitcode

import "testing"

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
