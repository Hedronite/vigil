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
