package grafana_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Hedronite/vigil/internal/config"
	"github.com/Hedronite/vigil/internal/exitcode"
	"github.com/Hedronite/vigil/internal/grafana"
	"github.com/Hedronite/vigil/internal/reasons"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var fixedNow = time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)

func repoRoot(tb testing.TB) string {
	tb.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		tb.Fatal("cannot locate the test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "internal", "grafana", "testdata", "grafana", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(b)
}

// serve answers with a fixed status and body.
func serve(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fixtureConfig(base string) (*config.Config, *config.Overlay) {
	cfg := &config.Config{
		APIVersion: config.ConfigMajor,
		Listen:     config.DefaultListen,
		Backends:   []config.Backend{{ID: "grafana", Kind: "grafana"}},
	}
	ov := &config.Overlay{
		APIVersion: config.OverlayMajor,
		Backends:   []config.OverlayBackend{{ID: "grafana", Address: base}},
	}
	return cfg, ov
}

func code(t *testing.T, err error) (int, string) {
	t.Helper()
	ee, ok := exitcode.As(err)
	if !ok {
		t.Fatalf("error %v is not an *exitcode.Error", err)
	}
	return ee.Exit, ee.Code
}

// TestHealth covers the healthy answer and each way a backend fails to be
// healthy. An unhealthy answer is a command failure with no document.
func TestHealth(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string // "" means healthy
	}{
		{"healthy", http.StatusOK, fixture(t, "healthy.json"), ""},
		{"database down", http.StatusOK, fixture(t, "db-down.json"), reasons.NotHealth},
		{"login page", http.StatusOK, fixture(t, "login.html"), reasons.NotHealth},
		{"empty body", http.StatusOK, "", reasons.NotHealth},
		{"server error", http.StatusInternalServerError, `{"database":"ok"}`, reasons.HTTPStatus},
		{"not found", http.StatusNotFound, "no such path", reasons.HTTPStatus},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			srv := serve(t, tc.status, tc.body)
			cfg, ov := fixtureConfig(srv.URL)
			doc, err := grafana.Health(context.Background(), cfg, ov, fixedNow)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("healthy backend returned %v", err)
				}
				if doc.State != reasons.StateOK || doc.Backend != "grafana" || doc.DetailCode != nil {
					t.Fatalf("document = %+v", doc)
				}
				validateHealth(t, doc)
				return
			}
			if err == nil {
				t.Fatalf("returned a document for a %s backend", tc.name)
			}
			exit, got := code(t, err)
			if exit != exitcode.Unavailable || got != tc.want {
				t.Fatalf("got %d/%s, want 4/%s", exit, got, tc.want)
			}
		})
	}
}

// TestHealthFailures covers the transport-level failures.
func TestHealthFailures(t *testing.T) {
	t.Run("redirect refused", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/login", http.StatusFound)
		}))
		t.Cleanup(srv.Close)
		cfg, ov := fixtureConfig(srv.URL)
		_, err := grafana.Health(context.Background(), cfg, ov, fixedNow)
		if exit, got := code(t, err); exit != exitcode.Unavailable || got != reasons.RedirectRefused {
			t.Fatalf("got %d/%s", exit, got)
		}
	})
	t.Run("oversize body", func(t *testing.T) {
		srv := serve(t, http.StatusOK, `{"database":"ok","padding":"`+strings.Repeat("x", 20<<10)+`"}`)
		cfg, ov := fixtureConfig(srv.URL)
		_, err := grafana.Health(context.Background(), cfg, ov, fixedNow)
		if exit, got := code(t, err); exit != exitcode.Unavailable || got != reasons.BodyCap {
			t.Fatalf("got %d/%s", exit, got)
		}
	})
	t.Run("slow backend", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(2500 * time.Millisecond)
			_, _ = w.Write([]byte(`{"database":"ok"}`))
		}))
		t.Cleanup(srv.Close)
		cfg, ov := fixtureConfig(srv.URL)
		_, err := grafana.Health(context.Background(), cfg, ov, fixedNow)
		if exit, got := code(t, err); exit != exitcode.Unavailable || got != reasons.Timeout {
			t.Fatalf("got %d/%s", exit, got)
		}
	})
	t.Run("no address", func(t *testing.T) {
		cfg, ov := fixtureConfig("http://unused.invalid")
		ov.Backends = nil
		state, detail, err := grafana.Check(context.Background(), cfg, ov, cfg.Backends[0])
		if err != nil || state != reasons.StateUnavailable || detail != "no_address" {
			t.Fatalf("got %q/%q/%v", state, detail, err)
		}
	})
	t.Run("no backend configured", func(t *testing.T) {
		cfg, ov := fixtureConfig("http://unused.invalid")
		cfg.Backends = nil
		_, err := grafana.Health(context.Background(), cfg, ov, fixedNow)
		if exit, got := code(t, err); exit != exitcode.Usage || got != "no_backend" {
			t.Fatalf("got %d/%s", exit, got)
		}
	})
}

func validateHealth(t *testing.T, doc any) {
	t.Helper()
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if err := schemaFor(t, "vigil.health.v0.schema.json").Validate(decoded); err != nil {
		t.Fatalf("document does not match the schema: %v", err)
	}
}

func schemaFor(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	root := repoRoot(t)
	paths, err := filepath.Glob(filepath.Join(root, "schema", "*.schema.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("glob schemas: %v", err)
	}
	c := jsonschema.NewCompiler()
	want := ""
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var raw any
		if err := json.Unmarshal(b, &raw); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		id, _ := raw.(map[string]any)["$id"].(string)
		if err := c.AddResource(id, raw); err != nil {
			t.Fatalf("add %s: %v", path, err)
		}
		if filepath.Base(path) == name {
			want = id
		}
	}
	sch, err := c.Compile(want)
	if err != nil {
		t.Fatalf("compile %s: %v", name, err)
	}
	return sch
}
