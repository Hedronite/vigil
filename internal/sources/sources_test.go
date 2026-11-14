package sources_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hedronite/vigil/internal/config"
	"github.com/Hedronite/vigil/internal/reasons"
	"github.com/Hedronite/vigil/internal/sources"
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

// backends serves a Prometheus readiness endpoint and a Grafana health
// endpoint from one handler, keyed by the base URL path prefix.
func backends(t *testing.T, promStatus int, grafanaBody string) (promURL, grafanaURL string) {
	t.Helper()
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/-/ready" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(promStatus)
	}))
	t.Cleanup(prom.Close)
	graf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(grafanaBody))
	}))
	t.Cleanup(graf.Close)
	return prom.URL, graf.URL
}

// backendID is unique per test. The client registry is process-wide and keeps
// the first configuration for an id, so two tests that share an id would share
// a client pointed at the first test's server.
func backendID(t *testing.T, suffix string) string {
	name := strings.ToLower(strings.NewReplacer("/", "-", "_", "-", " ", "-", ".", "-").Replace(t.Name()))
	room := 31 - len("b-") - len(suffix)
	if len(name) > room {
		name = name[:room]
	}
	return "b-" + name + suffix
}

func cfgFor(t *testing.T, promURL, grafanaURL string) (*config.Config, *config.Overlay) {
	promID, grafanaID := backendID(t, "-p"), backendID(t, "-g")
	cfg := &config.Config{
		APIVersion: config.ConfigMajor,
		Listen:     config.DefaultListen,
		Backends: []config.Backend{
			{ID: promID, Kind: "prom", Vantage: "lab"},
			{ID: grafanaID, Kind: "grafana"},
		},
	}
	ov := &config.Overlay{
		APIVersion: config.OverlayMajor,
		Backends: []config.OverlayBackend{
			{ID: promID, Address: promURL},
			{ID: grafanaID, Address: grafanaURL},
		},
	}
	return cfg, ov
}

// TestSourcesRows proves one row per backend, with state as data: a down
// backend never fails the command.
func TestSourcesRows(t *testing.T) {
	t.Run("both healthy", func(t *testing.T) {
		prom, graf := backends(t, http.StatusOK, `{"database":"ok"}`)
		cfg, ov := cfgFor(t, prom, graf)
		doc, err := sources.Sources(context.Background(), cfg, ov, fixedNow)
		if err != nil {
			t.Fatalf("sources: %v", err)
		}
		if len(doc.Sources) != 2 {
			t.Fatalf("rows = %+v", doc.Sources)
		}
		for _, row := range doc.Sources {
			if row.State != reasons.StateOK || row.DetailCode != nil {
				t.Errorf("row %+v is not ok", row)
			}
			if row.LastOK != nil {
				t.Errorf("row %q carries a last_ok, but V1 keeps no state", row.ID)
			}
		}
		if doc.Sources[0].Vantage != "lab" || doc.Sources[1].Vantage != "" {
			t.Errorf("vantage rows = %q/%q", doc.Sources[0].Vantage, doc.Sources[1].Vantage)
		}
		validate(t, doc)
	})

	t.Run("backend down is a row state", func(t *testing.T) {
		prom, graf := backends(t, http.StatusServiceUnavailable, `<html>login</html>`)
		cfg, ov := cfgFor(t, prom, graf)
		doc, err := sources.Sources(context.Background(), cfg, ov, fixedNow)
		if err != nil {
			t.Fatalf("a down backend failed the command: %v", err)
		}
		if doc.Sources[0].State != reasons.StateUnavailable || *doc.Sources[0].DetailCode != reasons.HTTPStatus {
			t.Errorf("prom row = %+v", doc.Sources[0])
		}
		if doc.Sources[1].State != reasons.StateUnavailable || *doc.Sources[1].DetailCode != reasons.NotHealth {
			t.Errorf("grafana row = %+v", doc.Sources[1])
		}
		validate(t, doc)
	})

	t.Run("missing address", func(t *testing.T) {
		prom, graf := backends(t, http.StatusOK, `{"database":"ok"}`)
		cfg, ov := cfgFor(t, prom, graf)
		ov.Backends = ov.Backends[:1]
		doc, err := sources.Sources(context.Background(), cfg, ov, fixedNow)
		if err != nil {
			t.Fatalf("sources: %v", err)
		}
		if *doc.Sources[1].DetailCode != "no_address" {
			t.Errorf("grafana row = %+v", doc.Sources[1])
		}
	})

	t.Run("no backends is an empty table", func(t *testing.T) {
		cfg, ov := cfgFor(t, "http://unused.invalid", "http://unused.invalid")
		cfg.Backends = nil
		doc, err := sources.Sources(context.Background(), cfg, ov, fixedNow)
		if err != nil {
			t.Fatalf("sources: %v", err)
		}
		if len(doc.Sources) != 0 {
			t.Fatalf("rows = %+v", doc.Sources)
		}
		validate(t, doc)
	})
}

func validate(t *testing.T, doc any) {
	t.Helper()
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if err := schema(t).Validate(decoded); err != nil {
		t.Fatalf("document does not match vigil.sources/v0: %v", err)
	}
}

func schema(t *testing.T) *jsonschema.Schema {
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
		if filepath.Base(path) == "vigil.sources.v0.schema.json" {
			want = id
		}
	}
	sch, err := c.Compile(want)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return sch
}

// TestInFlightBound proves each backend has one shared client: three concurrent
// runs never put more than two requests on one backend's wire.
func TestInFlightBound(t *testing.T) {
	var inFlight, peak int32
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&inFlight, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(prom.Close)

	cfg, ov := cfgFor(t, prom.URL, "http://unused.invalid")
	cfg.Backends = cfg.Backends[:1]

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := sources.Sources(context.Background(), cfg, ov, fixedNow); err != nil {
				t.Errorf("sources: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&peak); got > 2 {
		t.Fatalf("peak in flight = %d, want at most 2", got)
	}
}
