package reasons

import (
	"strings"
	"testing"
)

func TestOwnReasonsMatchPattern(t *testing.T) {
	if len(Own) != 5 {
		t.Fatalf("Own has %d codes, want 5", len(Own))
	}
	for _, r := range Own {
		if !Valid(r) {
			t.Errorf("own reason %q does not match %s", r, Pattern)
		}
	}
}

func TestValid(t *testing.T) {
	good := []string{"a", "watchdog_only", "peer_prometheus_down", "x9", strings.Repeat("a", 48)}
	bad := []string{"", "Upper", "9lead", "_lead", "has space", "dash-ed", "dot.ted", strings.Repeat("a", 49), "line\nbreak"}
	for _, s := range good {
		if !Valid(s) {
			t.Errorf("Valid(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if Valid(s) {
			t.Errorf("Valid(%q) = true, want false", s)
		}
	}
}

func TestStates(t *testing.T) {
	want := []string{"ok", "stale", "unavailable"}
	if len(States) != len(want) {
		t.Fatalf("States = %v, want %v", States, want)
	}
	for i := range want {
		if States[i] != want[i] {
			t.Errorf("States[%d] = %q, want %q", i, States[i], want[i])
		}
	}
}

func TestDetailCodes(t *testing.T) {
	want := []string{
		"unreachable", "timeout", "body_cap", "redirect_refused", "http_status",
		"series_cap", "query_warnings", "query_rejected", "not_health", "no_series",
		"missing_host", "unexpected_host",
	}
	if len(Detail) != len(want) {
		t.Fatalf("Detail has %d codes, want %d", len(Detail), len(want))
	}
	seen := map[string]bool{}
	for i, c := range want {
		if Detail[i] != c {
			t.Errorf("Detail[%d] = %q, want %q", i, Detail[i], c)
		}
		if !Valid(c) {
			t.Errorf("detail code %q does not match %s", c, Pattern)
		}
		if seen[c] {
			t.Errorf("detail code %q listed twice", c)
		}
		seen[c] = true
	}
	for _, o := range Own {
		if seen[o] {
			t.Errorf("detail code %q collides with an own reason", o)
		}
	}
}
