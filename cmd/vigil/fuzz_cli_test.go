package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Hedronite/vigil/internal/exitcode"
)

// usageMessages is every message the command layer prints for a usage error.
// It is fixed text: a message outside this set means text from the input, a
// backend or an error reached stderr.
var usageMessages = map[string]bool{
	"a command is required":                                            true,
	"a subcommand is required":                                         true,
	"a query name is required":                                         true,
	"the query name is not a configured name":                          true,
	"--host must be a host slug":                                       true,
	"a path is required here, not a URL":                               true,
	"--now must be an RFC 3339 time":                                   true,
	"unknown command; see vigil --help":                                true,
	"unknown flag; see vigil --help":                                   true,
	"a flag is missing its value":                                      true,
	"a flag value is invalid":                                          true,
	"unexpected arguments; no command takes a URL, selector or PromQL": true,
	"usage error; see vigil --help":                                    true,
}

// FuzzCLIArgs feeds arbitrary argument lists to the command tree with handlers
// that always succeed. Whatever the arguments, the parser never panics, exits 0
// or 3 only, and a failure is one fixed-text vigil.error/v0 line with empty
// stdout that never repeats a URL-, address- or selector-shaped argument.
func FuzzCLIArgs(f *testing.F) {
	for _, s := range []string{
		"version", "prom targets", "prom query up_all", "prom query up_by_host --host castle",
		"sources --json", "grafana health --now 2026-11-14T09:00:00Z", "--help", "prom --help",
		"sources http://leak.example.invalid/x", "prom query http://leak.example.invalid/x", "prom query up_all --host http://leak.example.invalid", "prom query up(x)", "prom query up_all --host a{b}", "prom query up{job=\"x\"}", "--config https://leak.example.invalid sources",
		"--overlay //leak/x sources", "prom query rate(x[5m]) --host 192.0.2.7:9090", "--now yesterday sources",
		"--nope", "-x", "", "prom", "grafana", "help", "completion bash", "sources --config", "--json=maybe sources",
		"prom query a b", "version extra", "sources --host castle", "--now 2026-11-14T09:00:00Z --json prom targets",
	} {
		f.Add(s)
	}
	h := Handlers{}
	ok := func(_ context.Context, i Invocation) error {
		_, _ = i.Stdout.Write([]byte("ok\n"))
		return nil
	}
	h.PromTargets, h.PromQuery, h.GrafanaHealth, h.Sources = ok, ok, ok, ok

	f.Fuzz(func(t *testing.T, line string) {
		args := strings.Split(line, " ")
		var out, errb bytes.Buffer
		code := run(context.Background(), args, &out, &errb, h)
		switch code {
		case exitcode.Green:
			if errb.Len() != 0 {
				t.Fatalf("exit 0 with stderr %q", errb.String())
			}
			// No valid invocation holds a URL, a selector or an expression, so
			// a run that exits 0 with one means a value that should have been
			// refused got through. Asking for help is the exception: cobra
			// answers -h, --help and the help command before it checks the
			// other arguments. The help text is static, and it must not repeat
			// any of them.
			for _, a := range args {
				if !suspicious(a) {
					continue
				}
				if !helpAsked(args) {
					t.Fatalf("exit 0 with the URL-, selector- or expression-shaped argument %q", a)
				}
				if strings.Contains(out.String(), a) {
					t.Fatalf("the help text repeats the argument %q", a)
				}
			}
		case exitcode.Usage:
			if out.Len() != 0 {
				t.Fatalf("usage error with stdout %q", out.String())
			}
			s := errb.String()
			if !strings.HasSuffix(s, "\n") || strings.Count(s, "\n") != 1 {
				t.Fatalf("stderr is not one line: %q", s)
			}
			var doc struct {
				APIVersion string `json:"apiVersion"`
				Error      struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			dec := json.NewDecoder(strings.NewReader(s))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&doc); err != nil {
				t.Fatalf("stderr is not an error document: %v (%q)", err, s)
			}
			if doc.APIVersion != "vigil.error/v0" || doc.Error.Code != "usage" {
				t.Fatalf("document = %+v", doc)
			}
			if !usageMessages[doc.Error.Message] {
				t.Fatalf("message is not fixed text: %q", doc.Error.Message)
			}
			for _, a := range args {
				if len(a) >= 3 && strings.ContainsAny(a, "/:@.{([") && strings.Contains(s, a) {
					t.Fatalf("stderr repeats the argument %q: %s", a, s)
				}
			}
		default:
			t.Fatalf("exit %d: handlers that succeed leave only 0 and 3", code)
		}
	})
}

// suspicious reports whether an argument looks like a URL, a selector or an
// expression: nothing a valid invocation holds.
func suspicious(a string) bool {
	return strings.Contains(a, "://") || strings.ContainsAny(a, "{([\"?#")
}

// helpAsked reports whether the arguments ask for help.
func helpAsked(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "help" {
			return true
		}
	}
	return false
}
