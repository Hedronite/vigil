package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/Hedronite/vigil/internal/config"
	"github.com/Hedronite/vigil/internal/exitcode"
)

const marker = "leakmarker"

// loadSchema compiles one schema. Every schema in the directory is registered
// under its own $id first, because some reference another (vigil.sources/v0
// reuses the summary's source shape).
func loadSchema(t *testing.T, file string) *jsonschema.Schema {
	t.Helper()
	dir := filepath.Join("..", "..", "schema")
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no schemas found: %v", err)
	}
	c := jsonschema.NewCompiler()
	var want string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var doc any
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatal(err)
		}
		id := doc.(map[string]any)["$id"].(string)
		if err := c.AddResource(id, doc); err != nil {
			t.Fatal(err)
		}
		if filepath.Base(f) == file {
			want = id
		}
	}
	if want == "" {
		t.Fatalf("no schema file %s", file)
	}
	s, err := c.Compile(want)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func fixtureBody(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "internal", "promclient", "testdata", "prom", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// promServer answers the two Prometheus paths with recorded responses and
// counts the requests it saw.
func promServer(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	targets, query := fixtureBody(t, "targets-mixed.json"), fixtureBody(t, "query-plain.json")
	n := new(int)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*n++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/targets":
			_, _ = w.Write(targets)
		case "/api/v1/query":
			_, _ = w.Write(query)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, n
}

const wireConfig = `apiVersion: vigil.config/v0
listen: 127.0.0.1:10082
backends:
  - id: prom
    kind: prom
    vantage: lathe
authorities:
  - id: simplex
    path_ref: /var/lib/vigil/simplex.json
queries:
  - name: up_by_host
    backend: prom
    expr: 'up{instance="{{.host}}"}'
  - name: up_all
    backend: prom
    expr: up
`

// backendSeq gives every test its own backend id. The bounded client is
// shared per backend id for the life of the process and the first
// configuration wins, so two tests that reused one id would talk to the first
// test's server.
var backendSeq atomic.Int32

// writeDocs writes a config and an overlay that points at address, under a
// backend id no other test uses.
func writeDocs(t *testing.T, cfgBody, address string) (cfg, ov string) {
	t.Helper()
	id := fmt.Sprintf("prom%d", backendSeq.Add(1))
	cfgBody = strings.NewReplacer("id: prom\n", "id: "+id+"\n", "backend: prom\n", "backend: "+id+"\n").Replace(cfgBody)
	dir := t.TempDir()
	cfg = filepath.Join(dir, "config.yaml")
	ov = filepath.Join(dir, "overlay.yaml")
	if err := os.WriteFile(cfg, []byte(cfgBody), 0o600); err != nil {
		t.Fatal(err)
	}
	overlay := fmt.Sprintf("apiVersion: vigil.overlay/v0\nbackends:\n  - id: %s\n    address: %s\n", id, address)
	if err := os.WriteFile(ov, []byte(overlay), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg, ov
}

func live(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(context.Background(), args, &out, &errb, liveHandlers())
	return code, out.String(), errb.String()
}

func TestPromTargetsJSON(t *testing.T) {
	srv, _ := promServer(t)
	cfg, ov := writeDocs(t, wireConfig, srv.URL)
	code, out, errs := live(t, "prom", "targets", "--json", "--config", cfg, "--overlay", ov, "--now", "2026-11-14T09:00:00Z")
	if code != 0 || errs != "" {
		t.Fatalf("exit=%d stderr=%q", code, errs)
	}
	if strings.Count(out, "\n") != 1 {
		t.Errorf("stdout is not one document: %q", out)
	}
	var v any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	if err := loadSchema(t, "vigil.targets.v0.schema.json").Validate(v); err != nil {
		t.Fatalf("stdout is not vigil.targets/v0: %v\n%s", err, out)
	}
	for _, leak := range []string{"example.invalid", "scrapeUrl", "lastError", "discoveredLabels", "connection refused", "127.0.0.1"} {
		if strings.Contains(out, leak) {
			t.Errorf("stdout leaks %q:\n%s", leak, out)
		}
	}
	if !strings.Contains(out, `"vantage":"lathe"`) || !strings.Contains(out, `"backend":"prom`) {
		t.Errorf("document lacks backend or vantage: %s", out)
	}
	// The same inputs print the same bytes.
	_, again, _ := live(t, "prom", "targets", "--json", "--config", cfg, "--overlay", ov, "--now", "2026-11-14T09:00:00Z")
	if again != out {
		t.Errorf("two runs differ:\n%s\n%s", out, again)
	}
}

func TestPromTargetsTable(t *testing.T) {
	srv, _ := promServer(t)
	cfg, ov := writeDocs(t, wireConfig, srv.URL)
	code, out, errs := live(t, "prom", "targets", "--config", cfg, "--overlay", ov, "--now", "2026-11-14T09:00:00Z")
	if code != 0 || errs != "" {
		t.Fatalf("exit=%d stderr=%q", code, errs)
	}
	// The table is internal/render's, called as it is: header block, a blank
	// line, then the rows.
	for _, want := range []string{"BACKEND", "VANTAGE", "lathe", "GENERATED_AT  2026-11-14T09:00:00Z", "TARGET", "JOB", "HEALTH", "LAST_SCRAPE"} {
		if !strings.Contains(out, want) {
			t.Errorf("table lacks %q:\n%s", want, out)
		}
	}
	for _, leak := range []string{"example.invalid", "scrapeUrl", "127.0.0.1", "{"} {
		if strings.Contains(out, leak) {
			t.Errorf("table leaks %q:\n%s", leak, out)
		}
	}
}

func TestPromQueryJSON(t *testing.T) {
	srv, _ := promServer(t)
	cfg, ov := writeDocs(t, wireConfig, srv.URL)
	for _, args := range [][]string{
		{"prom", "query", "up_all"},
		{"prom", "query", "up_by_host", "--host", "castle"},
	} {
		full := append(append([]string{}, args...), "--json", "--config", cfg, "--overlay", ov, "--now", "2026-11-14T09:00:00Z")
		code, out, errs := live(t, full...)
		if code != 0 || errs != "" {
			t.Fatalf("%v: exit=%d stderr=%q", args, code, errs)
		}
		var v any
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatalf("%v: not JSON: %v", args, err)
		}
		if err := loadSchema(t, "vigil.query.v0.schema.json").Validate(v); err != nil {
			t.Fatalf("%v: not vigil.query/v0: %v\n%s", args, err, out)
		}
		if !strings.Contains(out, `"evaluated_at":"2026-11-14T09:00:00Z"`) {
			t.Errorf("%v: --now did not pin evaluated_at: %s", args, out)
		}
		if strings.Contains(out, "127.0.0.1") {
			t.Errorf("%v: stdout leaks an address: %s", args, out)
		}
	}
	code, out, _ := live(t, "prom", "query", "up_all", "--config", cfg, "--overlay", ov)
	if code != 0 || !strings.Contains(out, "VALUE") || strings.Contains(out, "{") {
		t.Errorf("query table: exit=%d\n%s", code, out)
	}
}

func TestPromQueryHostRules(t *testing.T) {
	srv, hits := promServer(t)
	cfg, ov := writeDocs(t, wireConfig, srv.URL)
	for _, tc := range []struct {
		args []string
		code string
	}{
		{[]string{"prom", "query", "up_by_host"}, "missing_host"},
		{[]string{"prom", "query", "up_all", "--host", "castle"}, "unexpected_host"},
		{[]string{"prom", "query", "up_by_host", "--host", "nosuchhost"}, "unknown_host"},
		{[]string{"prom", "query", "no_such_query"}, "unknown_query"},
	} {
		full := append(append([]string{}, tc.args...), "--config", cfg, "--overlay", ov)
		code, out, errs := live(t, full...)
		if code != exitcode.Usage || out != "" {
			t.Errorf("%v: exit=%d stdout=%q", tc.args, code, out)
		}
		if got := oneErrorLine(t, tc.code, errs); got != tc.code {
			t.Errorf("%v: code = %q, want %q", tc.args, got, tc.code)
		}
	}
	if *hits != 0 {
		t.Errorf("the backend was asked %d time(s) for a rejected query", *hits)
	}
}

func TestBackendCommandsNeedConfigAndOverlay(t *testing.T) {
	srv, hits := promServer(t)
	cfg, ov := writeDocs(t, wireConfig, srv.URL)
	for _, tc := range []struct {
		args []string
		code string
	}{
		{[]string{"prom", "targets"}, "missing_config"},
		{[]string{"prom", "targets", "--config", cfg}, "missing_overlay"},
		{[]string{"prom", "query", "up_all", "--overlay", ov}, "missing_config"},
		{[]string{"prom", "targets", "--config", cfg, "--overlay", filepath.Join(t.TempDir(), "absent.yaml")}, "overlay_invalid"},
		{[]string{"prom", "targets", "--config", filepath.Join(t.TempDir(), marker+".yaml"), "--overlay", ov}, "config_invalid"},
	} {
		code, out, errs := live(t, tc.args...)
		if code != exitcode.Usage || out != "" {
			t.Errorf("%v: exit=%d stdout=%q", tc.args, code, out)
		}
		if got := oneErrorLine(t, tc.code, errs); got != tc.code {
			t.Errorf("%v: code = %q, want %q", tc.args, got, tc.code)
		}
		if strings.Contains(errs, marker) || strings.Contains(errs, os.TempDir()) {
			t.Errorf("%v: stderr echoes a path: %s", tc.args, errs)
		}
	}
	if *hits != 0 {
		t.Errorf("the backend was asked %d time(s) with no usable config", *hits)
	}
}

// TestOverlayMustMatchTheConfig: an overlay address for a backend the config
// never declared is refused before any call.
func TestOverlayMustMatchTheConfig(t *testing.T) {
	srv, hits := promServer(t)
	dir := t.TempDir()
	cfg, ov := filepath.Join(dir, "c.yaml"), filepath.Join(dir, "o.yaml")
	if err := os.WriteFile(cfg, []byte(wireConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	overlay := "apiVersion: vigil.overlay/v0\nbackends:\n  - id: prom\n    address: " + srv.URL + "\n  - id: stray\n    address: " + srv.URL + "\n"
	if err := os.WriteFile(ov, []byte(overlay), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errs := live(t, "prom", "targets", "--config", cfg, "--overlay", ov)
	if code != exitcode.Usage || out != "" {
		t.Fatalf("exit=%d stdout=%q", code, out)
	}
	if got := oneErrorLine(t, "bind", errs); got != "overlay_invalid" {
		t.Errorf("code = %q", got)
	}
	if *hits != 0 {
		t.Errorf("the backend was asked %d time(s)", *hits)
	}
}

// TestConfigErrorsDoNotEchoValues plants an address, a URL and a path as the
// bad values; the loader's own message quotes them, and the command must not.
func TestConfigErrorsDoNotEchoValues(t *testing.T) {
	srv, _ := promServer(t)
	for name, body := range map[string]string{
		"listen-garbage": strings.Replace(wireConfig, "listen: 127.0.0.1:10082", "listen: http://"+marker+".example.invalid:1", 1),
		"locator-url":    strings.Replace(wireConfig, "path_ref: /var/lib/vigil/simplex.json", "path_ref: https://"+marker+".example.invalid/x", 1),
		"unknown-key":    wireConfig + marker + ": 1\n",
		"query-name-url": strings.Replace(wireConfig, "name: up_all", "name: http://"+marker+".example.invalid/x", 1),
		"dup-backend":    strings.Replace(wireConfig, "backends:\n  - id: prom\n    kind: prom\n    vantage: lathe\n", "backends:\n  - id: prom\n    kind: prom\n  - id: prom\n    kind: prom\n", 1),
	} {
		cfg, ov := writeDocs(t, body, srv.URL)
		code, out, errs := live(t, "prom", "targets", "--config", cfg, "--overlay", ov)
		if code != exitcode.Usage || out != "" {
			t.Errorf("%s: exit=%d stdout=%q", name, code, out)
		}
		if got := oneErrorLine(t, name, errs); got != "config_invalid" {
			t.Errorf("%s: code = %q", name, got)
		}
		if strings.Contains(errs, marker) || strings.Contains(errs, "example.invalid") {
			t.Errorf("%s: stderr echoes a value: %s", name, errs)
		}
	}
}

func TestWildcardListenIsStartupFailure(t *testing.T) {
	srv, hits := promServer(t)
	for _, listen := range []string{"0.0.0.0:10082", "[::]:10082", ":10082", "[::ffff:0.0.0.0]:10082"} {
		body := strings.Replace(wireConfig, "listen: 127.0.0.1:10082", `listen: "`+listen+`"`, 1)
		cfg, ov := writeDocs(t, body, srv.URL)
		for _, args := range [][]string{{"prom", "targets"}, {"prom", "query", "up_all"}} {
			full := append(append([]string{}, args...), "--config", cfg, "--overlay", ov)
			code, out, errs := live(t, full...)
			if code != exitcode.Startup || out != "" {
				t.Errorf("listen %q %v: exit=%d stdout=%q", listen, args, code, out)
			}
			if got := oneErrorLine(t, listen, errs); got != "wildcard_listen" {
				t.Errorf("listen %q: code = %q", listen, got)
			}
		}
	}
	if *hits != 0 {
		t.Errorf("the backend was asked %d time(s) with a wildcard listen", *hits)
	}
}

func TestUnreachableBackendIsExit4(t *testing.T) {
	srv, _ := promServer(t)
	addr := srv.URL
	srv.Close() // nothing listens any more
	cfg, ov := writeDocs(t, wireConfig, addr)
	for _, args := range [][]string{{"prom", "targets"}, {"prom", "query", "up_all"}} {
		full := append(append([]string{}, args...), "--json", "--config", cfg, "--overlay", ov)
		code, out, errs := live(t, full...)
		if code != exitcode.Unavailable || out != "" {
			t.Errorf("%v: exit=%d stdout=%q", args, code, out)
		}
		if got := oneErrorLine(t, "unreachable", errs); got != "unreachable" {
			t.Errorf("%v: code = %q", args, got)
		}
		if strings.Contains(errs, "127.0.0.1") {
			t.Errorf("%v: stderr names the address: %s", args, errs)
		}
	}
}

func TestBackendRedirectIsRefused(t *testing.T) {
	var landed int
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { landed++ }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/api/v1/targets", http.StatusFound)
	}))
	defer srv.Close()
	cfg, ov := writeDocs(t, wireConfig, srv.URL)
	code, out, errs := live(t, "prom", "targets", "--config", cfg, "--overlay", ov)
	if code != exitcode.Unavailable || out != "" {
		t.Fatalf("exit=%d stdout=%q", code, out)
	}
	if got := oneErrorLine(t, "redirect", errs); got != "redirect_refused" {
		t.Errorf("code = %q", got)
	}
	if landed != 0 {
		t.Error("the redirect was followed")
	}
}

func TestNowDefaultsToTheClock(t *testing.T) {
	srv, _ := promServer(t)
	cfg, ov := writeDocs(t, wireConfig, srv.URL)
	before := time.Now().UTC().Add(-time.Second)
	_, out, _ := live(t, "prom", "targets", "--json", "--config", cfg, "--overlay", ov)
	var doc struct {
		GeneratedAt string `json:"generated_at"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	got, err := time.Parse(time.RFC3339, doc.GeneratedAt)
	if err != nil || got.Before(before) || got.After(time.Now().UTC().Add(time.Second)) {
		t.Errorf("generated_at = %q, want about now", doc.GeneratedAt)
	}
}

// TestConfigFailureNeverEchoesAHostileField drives the mapping directly: the
// loader names fields, but a field path built from a bad key must not carry a
// value into the message.
func TestConfigFailureNeverEchoesAHostileField(t *testing.T) {
	for _, field := range []string{
		"http://" + marker + ".example.invalid/x", "queries." + marker + " x", marker + "\n", "192.0.2.7:9090/" + marker,
		strings.Repeat("a", 200),
	} {
		err := configFailure("config", &config.Error{Code: exitcode.Usage, File: "/p/" + marker, Field: field, Msg: "got " + marker})
		ee, ok := exitcode.As(err)
		if !ok || ee.Exit != exitcode.Usage || ee.Code != "config_invalid" {
			t.Fatalf("field %q: %v", field, err)
		}
		if strings.Contains(ee.Message, marker) || strings.Contains(ee.Message, "192.0.2") || strings.Contains(ee.Message, "/") {
			t.Errorf("field %q: message echoes it: %q", field, ee.Message)
		}
	}
	err := configFailure("config", &config.Error{Code: exitcode.Usage, Field: "queries.up_all.backend"})
	if ee, _ := exitcode.As(err); ee == nil || ee.Message != "the config is invalid at queries.up_all.backend" {
		t.Errorf("a clean field path should be kept: %v", err)
	}
}
