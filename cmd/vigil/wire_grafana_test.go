package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Hedronite/vigil/internal/exitcode"
)

const grafanaConfig = `apiVersion: vigil.config/v0
listen: 127.0.0.1:10082
backends:
  - id: prom
    kind: prom
    vantage: lathe
  - id: grafana
    kind: grafana
authorities:
  - id: simplex
    path_ref: /var/lib/vigil/simplex.json
`

// writeBackends writes a config and an overlay with one address per base
// backend id, each under a backend id no other test uses (the bounded client
// is shared per id and the first configuration wins).
func writeBackends(t *testing.T, cfgBody string, addrs map[string]string) (cfg, ov string) {
	t.Helper()
	n := backendSeq.Add(1)
	var overlay strings.Builder
	overlay.WriteString("apiVersion: vigil.overlay/v0\nbackends:\n")
	for _, base := range []string{"prom", "grafana"} {
		addr, ok := addrs[base]
		if !ok {
			continue
		}
		id := fmt.Sprintf("%s%d", base, n)
		cfgBody = strings.ReplaceAll(cfgBody, "id: "+base+"\n", "id: "+id+"\n")
		fmt.Fprintf(&overlay, "  - id: %s\n    address: %s\n", id, addr)
	}
	dir := t.TempDir()
	cfg, ov = filepath.Join(dir, "config.yaml"), filepath.Join(dir, "overlay.yaml")
	if err := os.WriteFile(cfg, []byte(cfgBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ov, []byte(overlay.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg, ov
}

// grafanaServer answers /api/health with one canned response.
func grafanaServer(t *testing.T, status int, ctype, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	hits := new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", ctype)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, hits
}

const healthyBody = `{"commit":"abc","database":"ok","version":"11.0.0"}`

func promReadyServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/-/ready" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGrafanaHealthHealthy(t *testing.T) {
	g, _ := grafanaServer(t, 200, "application/json", healthyBody)
	cfg, ov := writeBackends(t, grafanaConfig, map[string]string{"grafana": g.URL})
	args := []string{"grafana", "health", "--config", cfg, "--overlay", ov, "--now", "2026-11-14T09:00:00Z"}

	code, out, errs := live(t, append(args, "--json")...)
	if code != 0 || errs != "" {
		t.Fatalf("exit=%d stderr=%q", code, errs)
	}
	if strings.Count(out, "\n") != 1 {
		t.Errorf("stdout is not one document: %q", out)
	}
	var v any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if err := loadSchema(t, "vigil.health.v0.schema.json").Validate(v); err != nil {
		t.Fatalf("not vigil.health/v0: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"checked_at":"2026-11-14T09:00:00Z"`) || !strings.Contains(out, `"state":"ok"`) {
		t.Errorf("document: %s", out)
	}
	if strings.Contains(out, "127.0.0.1") || strings.Contains(out, "11.0.0") {
		t.Errorf("stdout carries an address or a backend field: %s", out)
	}
	_, again, _ := live(t, append(args, "--json")...)
	if again != out {
		t.Errorf("two runs differ:\n%s\n%s", out, again)
	}

	code, table, errs := live(t, args...)
	if code != 0 || errs != "" || !strings.Contains(table, "BACKEND") || strings.Contains(table, "{") {
		t.Errorf("table: exit=%d stderr=%q\n%s", code, errs, table)
	}
}

// TestGrafanaHealthUnhealthyIsExit4: every way of not being healthy is one
// error line, exit 4 and empty stdout, with the code that says why. A login
// page is not health.
func TestGrafanaHealthUnhealthyIsExit4(t *testing.T) {
	var landed atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { landed.Add(1) }))
	defer other.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/login", http.StatusFound)
	}))
	defer redirect.Close()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closed.URL
	closed.Close()
	login, _ := grafanaServer(t, 200, "text/html", "<html><body>Sign in to Grafana</body></html>")
	dbDown, _ := grafanaServer(t, 200, "application/json", `{"database":"failing"}`)
	noDB, _ := grafanaServer(t, 200, "application/json", `{"commit":"abc"}`)
	fiveHundred, _ := grafanaServer(t, 500, "application/json", `{"database":"ok"}`)
	big, _ := grafanaServer(t, 200, "application/json", `{"database":"ok","pad":"`+strings.Repeat("a", 17<<10)+`"}`)

	for _, tc := range []struct {
		name, addr, code string
	}{
		{"login page", login.URL, "not_health"},
		{"database down", dbDown.URL, "not_health"},
		{"no database key", noDB.URL, "not_health"},
		{"redirect", redirect.URL, "redirect_refused"},
		{"500", fiveHundred.URL, "http_status"},
		{"oversize", big.URL, "body_cap"},
		{"slow", slow.URL, "timeout"},
		{"unreachable", closedURL, "unreachable"},
	} {
		cfg, ov := writeBackends(t, grafanaConfig, map[string]string{"grafana": tc.addr})
		for _, asJSON := range []bool{true, false} {
			args := []string{"grafana", "health", "--config", cfg, "--overlay", ov}
			if asJSON {
				args = append(args, "--json")
			}
			code, out, errs := live(t, args...)
			if code != exitcode.Unavailable || out != "" {
				t.Errorf("%s (json=%v): exit=%d stdout=%q", tc.name, asJSON, code, out)
			}
			if got := oneErrorLine(t, tc.name, errs); got != tc.code {
				t.Errorf("%s (json=%v): code = %q, want %q", tc.name, asJSON, got, tc.code)
			}
			if strings.Contains(errs, "127.0.0.1") {
				t.Errorf("%s: stderr names the address: %s", tc.name, errs)
			}
		}
	}
	if landed.Load() != 0 {
		t.Error("a redirect was followed")
	}
}

func TestGrafanaHealthBackendSelection(t *testing.T) {
	g, hits := grafanaServer(t, 200, "application/json", healthyBody)
	c, o := writeDocs(t, wireConfig, g.URL) // a config with no grafana backend
	code, out, errs := live(t, "grafana", "health", "--config", c, "--overlay", o)
	if code != exitcode.Usage || out != "" {
		t.Fatalf("exit=%d stdout=%q", code, out)
	}
	if got := oneErrorLine(t, "no backend", errs); got != "no_backend" {
		t.Errorf("code = %q", got)
	}
	two := strings.Replace(grafanaConfig, "authorities:", "  - id: grafana2\n    kind: grafana\nauthorities:", 1)
	c, o = writeBackends(t, two, map[string]string{"grafana": g.URL})
	code, out, errs = live(t, "grafana", "health", "--config", c, "--overlay", o)
	if code != exitcode.Usage || out != "" {
		t.Fatalf("two backends: exit=%d stdout=%q", code, out)
	}
	if got := oneErrorLine(t, "two backends", errs); got != "ambiguous_backend" {
		t.Errorf("code = %q", got)
	}
	if hits.Load() != 0 {
		t.Errorf("Grafana was asked %d time(s) with no usable backend", hits.Load())
	}
}

func TestSourcesRowsAreData(t *testing.T) {
	good, _ := grafanaServer(t, 200, "application/json", healthyBody)
	bad, _ := grafanaServer(t, 500, "text/plain", "boom")
	ready := promReadyServer(t, 200)
	notReady := promReadyServer(t, 503)
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closed.URL
	closed.Close()

	for _, tc := range []struct {
		name         string
		prom, graf   string
		wantStates   string
		wantDetails  []string
		wantNoDetail bool
	}{
		{"both healthy", ready.URL, good.URL, "ok ok", nil, true},
		{"grafana http_status", ready.URL, bad.URL, "ok unavailable", []string{`"detail_code":"http_status"`}, false},
		{"prom not ready", notReady.URL, good.URL, "unavailable ok", []string{`"detail_code":"http_status"`}, false},
		{"both unreachable", closedURL, closedURL, "unavailable unavailable", []string{`"detail_code":"unreachable"`}, false},
	} {
		cfg, ov := writeBackends(t, grafanaConfig, map[string]string{"prom": tc.prom, "grafana": tc.graf})
		code, out, errs := live(t, "sources", "--json", "--config", cfg, "--overlay", ov, "--now", "2026-11-14T09:00:00Z")
		if code != 0 || errs != "" {
			t.Errorf("%s: exit=%d stderr=%q (a backend being down is a row, not a failure)", tc.name, code, errs)
			continue
		}
		var v struct {
			Sources []struct {
				ID, Kind, State string
				LastOK          *string `json:"last_ok"`
				Vantage         string
			}
		}
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatalf("%s: not JSON: %v", tc.name, err)
		}
		var raw any
		_ = json.Unmarshal([]byte(out), &raw)
		if err := loadSchema(t, "vigil.sources.v0.schema.json").Validate(raw); err != nil {
			t.Errorf("%s: not vigil.sources/v0: %v\n%s", tc.name, err, out)
		}
		var states []string
		for _, r := range v.Sources {
			states = append(states, r.State)
			if r.LastOK != nil {
				t.Errorf("%s: last_ok = %v, V1 keeps no state", tc.name, *r.LastOK)
			}
			if r.Kind == "prom" && r.Vantage != "lathe" {
				t.Errorf("%s: prom row vantage = %q", tc.name, r.Vantage)
			}
		}
		if got := strings.Join(states, " "); got != tc.wantStates {
			t.Errorf("%s: states = %q, want %q", tc.name, got, tc.wantStates)
		}
		for _, d := range tc.wantDetails {
			if !strings.Contains(out, d) {
				t.Errorf("%s: missing %s in %s", tc.name, d, out)
			}
		}
		if tc.wantNoDetail && strings.Contains(out, "detail_code") {
			t.Errorf("%s: a healthy row carries a detail code: %s", tc.name, out)
		}
		if strings.Contains(out, "127.0.0.1") {
			t.Errorf("%s: stdout carries an address: %s", tc.name, out)
		}
		if strings.Contains(out, `"id":"grafana`) == false || strings.Contains(out, `"authority`) {
			t.Errorf("%s: rows are the configured backends and nothing else: %s", tc.name, out)
		}
	}
}

func TestSourcesTable(t *testing.T) {
	good, _ := grafanaServer(t, 200, "application/json", healthyBody)
	ready := promReadyServer(t, 200)
	cfg, ov := writeBackends(t, grafanaConfig, map[string]string{"prom": ready.URL, "grafana": good.URL})
	code, out, errs := live(t, "sources", "--config", cfg, "--overlay", ov, "--now", "2026-11-14T09:00:00Z")
	if code != 0 || errs != "" {
		t.Fatalf("exit=%d stderr=%q", code, errs)
	}
	for _, want := range []string{"GENERATED_AT", "2026-11-14T09:00:00Z"} {
		if !strings.Contains(out, want) {
			t.Errorf("table lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "127.0.0.1") || strings.Contains(out, "{") {
		t.Errorf("table leaks:\n%s", out)
	}
}
