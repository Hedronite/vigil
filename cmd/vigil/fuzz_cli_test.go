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
// that record what they were given and always succeed. Whatever the arguments,
// the parser never panics and exits 0 or 3 only. A failure is one fixed-text
// vigil.error/v0 line with empty stdout whose text is chosen from the
// fixed set above, so it cannot repeat an argument. A success hands the handler only values of the right shape: a
// query name, a host slug, and file names that are not URLs. Checking what the
// handler received, and not the raw arguments, is the property that matters:
// flags may repeat and the last value wins, so an earlier bad value that was
// overridden never reaches anything.
func FuzzCLIArgs(f *testing.F) {
	for _, s := range []string{
		"version", "prom targets", "prom query up_all", "prom query up_by_host --host castle",
		"sources --json", "grafana health --now 2026-11-14T09:00:00Z", "--help", "prom --help",
		"sources http://leak.example.invalid/x", "prom query up{job=\"x\"}", "--config https://leak.example.invalid sources",
		"--overlay //leak/x sources", "prom query rate(x[5m]) --host 192.0.2.7:9090", "--now yesterday sources",
		"--nope", "-x", "", "prom", "grafana", "help", "completion bash", "sources --config", "--json=maybe sources",
		"prom query a b", "version extra", "sources --host castle", "--now 2026-11-14T09:00:00Z --json prom targets",
		"prom query http://leak.example.invalid/x", "prom query up_all --host http://leak.example.invalid",
		"prom query up(x)", "prom query up_all --host a{b}",
		"prom query a --host \" --host ", "-hh \"", "\"000000 -h", "--overlay \" sources",
		"prom query up_all --config ? --overlay #", "prom query up_all --host Castle", "prom query UP",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, line string) {
		args := strings.Split(line, " ")
		var got *Invocation
		rec := func(_ context.Context, i Invocation) error {
			i.Stdout = nil
			got = &i
			return nil
		}
		h := Handlers{PromTargets: rec, PromQuery: rec, GrafanaHealth: rec, Sources: rec}

		var out, errb bytes.Buffer
		code := run(context.Background(), args, &out, &errb, h)
		switch code {
		case exitcode.Green:
			if errb.Len() != 0 {
				t.Fatalf("exit 0 with stderr %q", errb.String())
			}
			if got == nil {
				// Help or version: static text, which cannot hold an
				// argument. TestHelpAndErrorsDoNotEchoArguments pins that
				// with a marker; a substring check here would trip on
				// words the help text really contains.
				return
			}
			if got.Name != "" && !queryNamePattern.MatchString(got.Name) {
				t.Fatalf("handler got the query name %q", got.Name)
			}
			if got.Host != "" && !hostSlugPattern.MatchString(got.Host) {
				t.Fatalf("handler got the host %q", got.Host)
			}
			for _, p := range []string{got.ConfigPath, got.OverlayPath} {
				if !plainPath(p) {
					t.Fatalf("handler got the path %q", p)
				}
			}
		case exitcode.Usage:
			if out.Len() != 0 {
				t.Fatalf("usage error with stdout %q", out.String())
			}
			if got != nil {
				t.Fatal("a handler ran for a usage error")
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
		default:
			t.Fatalf("exit %d: handlers that succeed leave only 0 and 3", code)
		}
	})
}
