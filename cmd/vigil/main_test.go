package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Hedronite/vigil/internal/exitcode"
)

func TestVersion(t *testing.T) {
	var out, errb bytes.Buffer
	code := runRecovered([]string{"version"}, &out, &errb)
	if code != exitcode.Green {
		t.Fatalf("exit = %d, want %d", code, exitcode.Green)
	}
	if !strings.HasPrefix(out.String(), "vigil ") {
		t.Errorf("stdout = %q, want a line starting with \"vigil \"", out.String())
	}
	if errb.Len() != 0 {
		t.Errorf("stderr = %q, want empty", errb.String())
	}
}

func TestUsageErrorsNeverExitAsVerdict(t *testing.T) {
	cases := [][]string{nil, {}, {"nope"}, {"version", "extra"}, {"--json"}}
	for _, args := range cases {
		var out, errb bytes.Buffer
		code := runRecovered(args, &out, &errb)
		if code != exitcode.Usage {
			t.Errorf("args %v: exit = %d, want %d", args, code, exitcode.Usage)
		}
		if exitcode.IsVerdict(code) {
			t.Errorf("args %v: error path exited with verdict code %d", args, code)
		}
		if out.Len() != 0 {
			t.Errorf("args %v: stdout = %q, want empty", args, out.String())
		}
	}
}

func TestPanicExitsInternal(t *testing.T) {
	var errb bytes.Buffer
	code := func() (c int) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic escaped: %v", r)
			}
		}()
		return runRecoveredWith(func() int { panic("boom") }, &errb)
	}()
	if code != exitcode.Internal {
		t.Fatalf("exit = %d, want %d", code, exitcode.Internal)
	}
	if !strings.Contains(errb.String(), "internal error") {
		t.Errorf("stderr = %q, want an internal error line", errb.String())
	}
}
