package exitcode

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrorCarriesCodeOnly(t *testing.T) {
	e := Newf(Unavailable, "unreachable", "backend did not answer")
	if e.Error() != "unreachable" {
		t.Errorf("Error() = %q, want the code alone", e.Error())
	}
	wrapped := fmt.Errorf("prom targets: %w", e)
	got, ok := As(wrapped)
	if !ok || got != e {
		t.Fatalf("As(wrapped) = %v, %v; want the original error", got, ok)
	}
	if _, ok := As(errors.New("plain")); ok {
		t.Error("As accepted a plain error")
	}
}

func TestErrorNeverUsesVerdictCode(t *testing.T) {
	for _, code := range []int{Usage, Unavailable, Startup, Internal} {
		if IsVerdict(New(code, "x").Exit) {
			t.Errorf("exit %d is a verdict code", code)
		}
	}
}
