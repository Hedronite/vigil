package main

import (
	"strings"
	"testing"
)

// TestHelpAndErrorsDoNotEchoArguments puts a marker in every argument slot and
// checks it never comes back on stdout or stderr, in help, in a usage error or
// in version output.
func TestHelpAndErrorsDoNotEchoArguments(t *testing.T) {
	const m = "leakmarker"
	for _, args := range [][]string{
		{"--help", m + ".example.invalid"},
		{"help", "http://" + m + ".example.invalid/x"},
		{"prom", "--help", m},
		{"-hh", "\"" + m},
		{"version", m},
		{m},
		{"prom", m},
		{"prom", "query", "http://" + m + ".example.invalid"},
		{"sources", "--nope=" + m},
		{"sources", "--config=https://" + m + ".example.invalid"},
		{"--now", m, "sources"},
	} {
		_, out, errs := call(t, defaultHandlers(), args...)
		if strings.Contains(out, m) || strings.Contains(errs, m) {
			t.Errorf("%q echoed the marker: stdout=%q stderr=%q", args, out, errs)
		}
	}
}
