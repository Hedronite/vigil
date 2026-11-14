package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/Hedronite/vigil/internal/exitcode"
)

// call runs the CLI with the given handlers.
func call(t *testing.T, h Handlers, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(context.Background(), args, &out, &errb, h)
	return code, out.String(), errb.String()
}

// spy returns Handlers that record every invocation.
func spy(calls *[]string, inv *Invocation) Handlers {
	rec := func(name string) Handler {
		return func(_ context.Context, i Invocation) error {
			*calls = append(*calls, name)
			if inv != nil {
				*inv = i
			}
			_, _ = i.Stdout.Write([]byte("document-" + name + "\n"))
			return nil
		}
	}
	return Handlers{
		PromTargets: rec("prom-targets"), PromQuery: rec("prom-query"),
		GrafanaHealth: rec("grafana-health"), Sources: rec("sources"),
	}
}

var errorSchema = func() *jsonschema.Schema {
	b, err := os.ReadFile(filepath.Join("..", "..", "schema", "vigil.error.v0.schema.json"))
	if err != nil {
		panic(err)
	}
	var doc any
	if err := json.Unmarshal(b, &doc); err != nil {
		panic(err)
	}
	id := doc.(map[string]any)["$id"].(string)
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		panic(err)
	}
	s, err := c.Compile(id)
	if err != nil {
		panic(err)
	}
	return s
}()

// oneErrorLine asserts stderr is exactly one valid vigil.error/v0 line and
// returns its code.
func oneErrorLine(t *testing.T, label, stderr string) string {
	t.Helper()
	if !strings.HasSuffix(stderr, "\n") || strings.Count(stderr, "\n") != 1 {
		t.Fatalf("%s: stderr = %q, want exactly one line", label, stderr)
	}
	var v any
	dec := json.NewDecoder(strings.NewReader(stderr))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("%s: stderr is not JSON: %v (%q)", label, err, stderr)
	}
	if err := errorSchema.Validate(v); err != nil {
		t.Fatalf("%s: stderr is not vigil.error/v0: %v (%q)", label, err, stderr)
	}
	return v.(map[string]any)["error"].(map[string]any)["code"].(string)
}

func TestVersion(t *testing.T) {
	code, out, errs := call(t, defaultHandlers(), "version")
	if code != exitcode.Green {
		t.Fatalf("exit = %d, want %d", code, exitcode.Green)
	}
	if !strings.HasPrefix(out, "vigil ") || strings.Count(out, "\n") != 1 {
		t.Errorf("stdout = %q, want one line starting with \"vigil \"", out)
	}
	if errs != "" {
		t.Errorf("stderr = %q, want empty", errs)
	}
}

func TestRunRecoveredVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runRecovered(context.Background(), []string{"version"}, &out, &errb); code != exitcode.Green {
		t.Fatalf("exit = %d", code)
	}
	if !strings.HasPrefix(out.String(), "vigil ") {
		t.Errorf("stdout = %q", out.String())
	}
}

func TestUsageErrorsNeverExitAsVerdict(t *testing.T) {
	cases := [][]string{
		nil, {}, {"nope"}, {"version", "extra"}, {"--json"}, {"prom"}, {"grafana"},
		{"prom", "nope"}, {"grafana", "nope"}, {"--nope"}, {"-x"}, {"sources", "--nope"},
		{"--now", "yesterday", "sources"}, {"--now"}, {"prom", "query"}, {"prom", "query", "a", "b"},
		{"--json=maybe", "sources"},
	}
	for _, args := range cases {
		var calls []string
		code, out, errs := call(t, spy(&calls, nil), args...)
		label := strings.Join(args, " ")
		if code != exitcode.Usage {
			t.Errorf("args %q: exit = %d, want %d", label, code, exitcode.Usage)
		}
		if exitcode.IsVerdict(code) {
			t.Errorf("args %q: error path exited with verdict code %d", label, code)
		}
		if out != "" {
			t.Errorf("args %q: stdout = %q, want empty", label, out)
		}
		if got := oneErrorLine(t, label, errs); got != "usage" {
			t.Errorf("args %q: code = %q, want usage", label, got)
		}
		if len(calls) != 0 {
			t.Errorf("args %q: a handler ran: %v", label, calls)
		}
	}
}

// TestURLShapedArgumentsExitUsage feeds URL-, selector- and PromQL-shaped
// values to every command and every value-taking flag. All must exit 3 before
// any handler runs, and the error must not echo the value.
func TestURLShapedArgumentsExitUsage(t *testing.T) {
	const marker = "leakmarker"
	values := []string{
		"http://" + marker + ".example.invalid/x",
		"https://" + marker + ".example.invalid:9090",
		"//" + marker + ".example.invalid/x",
		marker + ".example.invalid:9090",
		"192.0.2.7:9090",
		"[2001:db8::1]:9090",
		"user@" + marker,
		"up{job=\"" + marker + "\"}",
		"rate(" + marker + "[5m])",
		"/api/v1/query?query=" + marker,
		"../" + marker,
		marker + "/" + marker,
		"UP",
	}
	commands := [][]string{
		{"version"}, {"prom", "targets"}, {"grafana", "health"}, {"sources"}, {"prom"}, {"grafana"}, {},
	}
	for _, v := range values {
		for _, cmd := range commands {
			args := append(append([]string{}, cmd...), v)
			var calls []string
			code, out, errs := call(t, spy(&calls, nil), args...)
			if code != exitcode.Usage || out != "" || len(calls) != 0 {
				t.Errorf("%q: exit=%d stdout=%q handlers=%v", args, code, out, calls)
			}
			oneErrorLine(t, strings.Join(args, " "), errs)
			if strings.Contains(errs, marker) || strings.Contains(errs, "192.0.2") {
				t.Errorf("%q: stderr echoes the value: %s", args, errs)
			}
		}
		// prom query: as the name, and as the host.
		for _, args := range [][]string{
			{"prom", "query", v},
			{"prom", "query", "up_total", "--host", v},
			{"prom", "query", "up_total", "--host=" + v},
		} {
			var calls []string
			code, out, errs := call(t, spy(&calls, nil), args...)
			if code != exitcode.Usage || out != "" || len(calls) != 0 {
				t.Errorf("%q: exit=%d stdout=%q handlers=%v", args, code, out, calls)
			}
			oneErrorLine(t, strings.Join(args, " "), errs)
			if strings.Contains(errs, marker) || strings.Contains(errs, "192.0.2") {
				t.Errorf("%q: stderr echoes the value: %s", args, errs)
			}
		}
		// --config and --overlay take paths; a URL is not one.
		if !strings.Contains(v, "://") {
			continue
		}
		for _, flag := range []string{"--config", "--overlay"} {
			for _, cmd := range [][]string{{"sources"}, {"prom", "targets"}, {"grafana", "health"}, {"prom", "query", "up_total"}} {
				args := append(append([]string{}, cmd...), flag, v)
				var calls []string
				code, out, errs := call(t, spy(&calls, nil), args...)
				if code != exitcode.Usage || out != "" || len(calls) != 0 {
					t.Errorf("%q: exit=%d stdout=%q handlers=%v", args, code, out, calls)
				}
				oneErrorLine(t, strings.Join(args, " "), errs)
				if strings.Contains(errs, marker) {
					t.Errorf("%q: stderr echoes the value: %s", args, errs)
				}
			}
		}
	}
}

func TestValidArgumentsReachTheHandler(t *testing.T) {
	var calls []string
	var inv Invocation
	h := spy(&calls, &inv)

	code, out, errs := call(t, h, "prom", "query", "up_total", "--host", "edge-1",
		"--config", "/etc/vigil/config.yaml", "--overlay", "relative/overlay.yaml",
		"--json", "--now", "2026-11-14T10:00:00Z")
	if code != 0 || errs != "" || out != "document-prom-query\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, out, errs)
	}
	want := Invocation{
		Name: "up_total", Host: "edge-1", ConfigPath: "/etc/vigil/config.yaml",
		OverlayPath: "relative/overlay.yaml", JSON: true,
		Now: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC),
	}
	inv.Stdout = nil
	if inv != want {
		t.Errorf("invocation = %+v, want %+v", inv, want)
	}

	for _, c := range []struct {
		args []string
		name string
	}{
		{[]string{"prom", "targets"}, "prom-targets"},
		{[]string{"grafana", "health"}, "grafana-health"},
		{[]string{"sources"}, "sources"},
		{[]string{"prom", "query", "up_total"}, "prom-query"},
	} {
		calls = nil
		code, out, _ := call(t, h, c.args...)
		if code != 0 || out != "document-"+c.name+"\n" || len(calls) != 1 || calls[0] != c.name {
			t.Errorf("%v: exit=%d stdout=%q calls=%v", c.args, code, out, calls)
		}
	}
}

func TestStubsAreNotVerdicts(t *testing.T) {
	for _, args := range [][]string{
		{"prom", "targets"}, {"prom", "query", "up_total"}, {"grafana", "health"}, {"sources"},
	} {
		code, out, errs := call(t, defaultHandlers(), args...)
		if code != exitcode.Internal || out != "" {
			t.Errorf("%v: exit=%d stdout=%q", args, code, out)
		}
		if got := oneErrorLine(t, strings.Join(args, " "), errs); got != "not_implemented" {
			t.Errorf("%v: code = %q", args, got)
		}
	}
}

func TestHandlerErrorsMapToExitAndCode(t *testing.T) {
	for _, tc := range []struct {
		err  error
		exit int
		code string
	}{
		{exitcode.New(exitcode.Unavailable, "unreachable"), exitcode.Unavailable, "unreachable"},
		{exitcode.New(exitcode.Usage, "query_rejected"), exitcode.Usage, "query_rejected"},
		{exitcode.New(exitcode.Startup, "wildcard_listen"), exitcode.Startup, "wildcard_listen"},
	} {
		h := Handlers{Sources: func(_ context.Context, i Invocation) error {
			_, _ = i.Stdout.Write([]byte("partial document\n"))
			return tc.err
		}}
		code, out, errs := call(t, h, "sources")
		if code != tc.exit {
			t.Errorf("%v: exit = %d, want %d", tc.err, code, tc.exit)
		}
		if out != "" {
			t.Errorf("%v: stdout = %q; a failed command prints nothing on stdout", tc.err, out)
		}
		if got := oneErrorLine(t, tc.code, errs); got != tc.code {
			t.Errorf("code = %q, want %q", got, tc.code)
		}
	}
}

func TestUnknownHandlerErrorIsInternalAndSilent(t *testing.T) {
	h := Handlers{Sources: func(context.Context, Invocation) error {
		return errors.New("dial tcp 192.0.2.9:9090: connect: refused")
	}}
	code, out, errs := call(t, h, "sources")
	if code != exitcode.Internal || out != "" {
		t.Fatalf("exit=%d stdout=%q", code, out)
	}
	if got := oneErrorLine(t, "plain error", errs); got != "internal" {
		t.Errorf("code = %q, want internal", got)
	}
	if strings.Contains(errs, "192.0.2") || strings.Contains(errs, "dial") {
		t.Errorf("stderr leaks the error text: %s", errs)
	}
}

func TestHelpIsASuccess(t *testing.T) {
	for _, args := range [][]string{
		{"--help"}, {"help"}, {"prom", "--help"}, {"prom", "query", "--help"}, {"grafana", "health", "--help"},
		{"sources", "--help"}, {"prom", "targets", "--help"}, {"version", "--help"},
	} {
		var calls []string
		code, out, errs := call(t, spy(&calls, nil), args...)
		if code != 0 || errs != "" || !strings.Contains(out, "vigil") || len(calls) != 0 {
			t.Errorf("%v: exit=%d stdout=%q stderr=%q handlers=%v", args, code, out, errs, calls)
		}
	}
	_, out, _ := call(t, defaultHandlers(), "--help")
	for _, want := range []string{"--config", "--overlay", "--json", "--now", "prom", "grafana", "sources", "version"} {
		if !strings.Contains(out, want) {
			t.Errorf("root help lacks %q", want)
		}
	}
	for _, banned := range []string{"completion", "serve"} {
		if strings.Contains(out, banned) {
			t.Errorf("root help lists %q", banned)
		}
	}
}

func TestPanicExitsInternalWithOneJSONLine(t *testing.T) {
	var errb bytes.Buffer
	code := func() (c int) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic escaped: %v", r)
			}
		}()
		return runRecoveredWith(func() int { panic("boom at 192.0.2.5") }, &errb)
	}()
	if code != exitcode.Internal {
		t.Fatalf("exit = %d, want %d", code, exitcode.Internal)
	}
	if got := oneErrorLine(t, "panic", errb.String()); got != "internal" {
		t.Errorf("code = %q", got)
	}
	if strings.Contains(errb.String(), "boom") || strings.Contains(errb.String(), "192.0.2") {
		t.Errorf("stderr leaks the panic value: %s", errb.String())
	}
}

func TestCommandSetIsExactlyTheSix(t *testing.T) {
	var out bytes.Buffer
	root := newRoot(&out, defaultHandlers())
	var got []string
	for _, c := range root.Commands() {
		if len(c.Commands()) == 0 {
			got = append(got, c.Name())
			continue
		}
		for _, s := range c.Commands() {
			got = append(got, c.Name()+" "+s.Name())
		}
	}
	want := map[string]bool{
		"version": true, "prom targets": true, "prom query": true,
		"grafana health": true, "sources": true, "help": true,
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected command %q", g)
		}
		delete(want, g)
	}
	for w := range want {
		if w != "help" {
			t.Errorf("missing command %q", w)
		}
	}
}
