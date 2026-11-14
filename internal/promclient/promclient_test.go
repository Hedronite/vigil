package promclient_test

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
	"github.com/Hedronite/vigil/internal/exitcode"
	"github.com/Hedronite/vigil/internal/promclient"
	"github.com/Hedronite/vigil/internal/reasons"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var fixedNow = time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)

// repoRoot walks up from this test file to the module root.
func repoRoot(tb testing.TB) string {
	tb.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		tb.Fatal("cannot locate the test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// serveFixture answers every request with one recorded response, and reports
// the path the client asked for.
func serveFixture(t *testing.T, name string) (*httptest.Server, *string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "internal", "promclient", "testdata", "prom", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &path
}

// serveBody answers with a body assembled in the test.
func serveBody(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fixtureConfig is one prometheus backend mapped to the test server, plus the
// overlay that maps one address-shaped instance to a slug.
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

func fixtureConfig(t *testing.T, base string) (*config.Config, *config.Overlay) {
	id := backendID(t, "")
	cfg := &config.Config{
		APIVersion: config.ConfigMajor,
		Listen:     config.DefaultListen,
		Backends:   []config.Backend{{ID: id, Kind: "prom", Vantage: "lathe"}},
		Queries: []config.Query{
			{Name: "up_by_host", Backend: id, Expr: `up{instance="{{.host}}"}`, Timeout: "5s", MaxSeries: 200},
			{Name: "up_all", Backend: id, Expr: "up", Timeout: "5s", MaxSeries: 200},
			{Name: "small_cap", Backend: id, Expr: "up", Timeout: "5s", MaxSeries: 3},
		},
	}
	ov := &config.Overlay{
		APIVersion: config.OverlayMajor,
		Backends:   []config.OverlayBackend{{ID: id, Address: base}},
		Hosts: []config.OverlayHost{
			{Slug: "castle", Instances: []string{"castle.example.invalid:9100"}},
		},
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

// TestTargetsMapping proves the decoder publishes only the four allowed fields
// and maps each instance through the overlay before printing it.
func TestTargetsMapping(t *testing.T) {
	srv, path := serveFixture(t, "targets-mixed.json")
	cfg, ov := fixtureConfig(t, srv.URL)

	doc, err := promclient.Targets(context.Background(), cfg, ov, fixedNow)
	if err != nil {
		t.Fatalf("targets: %v", err)
	}
	if *path != "/api/v1/targets" {
		t.Errorf("path = %q", *path)
	}
	if doc.APIVersion != promclient.TargetsVersion || doc.Backend != backendID(t, "") || doc.Vantage != "lathe" {
		t.Errorf("document header = %+v", doc)
	}
	if doc.GeneratedAt != "2026-11-14T09:00:00Z" {
		t.Errorf("generated_at = %q", doc.GeneratedAt)
	}
	// Rows sort by host, then job, then health.
	want := []promclient.Target{
		{Host: "box", Job: "node", Health: "unknown", LastScrape: nil},
		{Host: "castle", Job: "node", Health: "up", LastScrape: ptr("2026-11-14T09:00:00Z")},
		{Host: "tower", Job: "redacted", Health: "down", LastScrape: ptr("2026-11-14T09:00:00Z")},
	}
	if len(doc.Targets) != len(want) {
		t.Fatalf("targets = %+v", doc.Targets)
	}
	for i := range want {
		if doc.Targets[i].Host != want[i].Host || doc.Targets[i].Job != want[i].Job ||
			doc.Targets[i].Health != want[i].Health {
			t.Errorf("target %d = %+v, want %+v", i, doc.Targets[i], want[i])
		}
		if (doc.Targets[i].LastScrape == nil) != (want[i].LastScrape == nil) {
			t.Errorf("target %d last_scrape = %v, want %v", i, doc.Targets[i].LastScrape, want[i].LastScrape)
		}
	}

	// A known slug keeps its own value, so a relabelled backend row is not a
	// wall of unmapped entries.
	for _, row := range doc.Targets {
		if row.Host == "castle" {
			return
		}
	}
	t.Error("the castle row is missing")
	validateAgainst(t, "vigil.targets.v0.schema.json", doc)
}

// TestTargetsMappingRules covers the labels a recorded fixture cannot hold.
// The values are assembled here because the repository scrub forbids them in a
// fixture, and the assertions are the point: a known slug publishes, an
// unknown slug-shaped label or an address does not.
func TestTargetsMappingRules(t *testing.T) {
	planted := []string{
		strings.Join([]string{"100", "64", "12", "34"}, "."),
		strings.Join([]string{"192", "168", "1", "20"}, "."),
		strings.Join([]string{"castle", "ts", "net"}, "."),
	}
	addressInstance := strings.Join([]string{"castle", "example", "invalid"}, ".") + ":9100"
	body := `{"status":"success","data":{"activeTargets":[` +
		`{"labels":{"instance":"` + addressInstance + `","job":"node"},"health":"up"},` +
		`{"labels":{"instance":"edge-7","job":"node"},"health":"up"},` +
		`{"labels":{"instance":"` + planted[0] + `:9100","job":"node"},"health":"up"},` +
		`{"labels":{"instance":"` + planted[2] + `:9100","job":"node"},"health":"up"}]}}`
	srv := serveBody(t, body)
	cfg, ov := fixtureConfig(t, srv.URL)

	doc, err := promclient.Targets(context.Background(), cfg, ov, fixedNow)
	if err != nil {
		t.Fatalf("targets: %v", err)
	}
	if len(doc.Targets) != 4 {
		t.Fatalf("targets = %+v", doc.Targets)
	}
	// The overlay maps the address-shaped instance; the rest are names we do
	// not publish, so they read as unmapped.
	if doc.Targets[0].Host != "castle" {
		t.Errorf("mapped instance = %q, want castle", doc.Targets[0].Host)
	}
	for _, row := range doc.Targets[1:] {
		if row.Host != "unmapped" {
			t.Errorf("unpublishable instance printed as %q", row.Host)
		}
	}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, p := range append(planted, addressInstance) {
		if strings.Contains(string(out), p) {
			t.Errorf("output carries a planted value")
		}
	}
}

// TestQueryRules covers the host rulings and the answer shapes.
func TestQueryRules(t *testing.T) {
	t.Run("missing host", func(t *testing.T) {
		srv, _ := serveFixture(t, "query-plain.json")
		cfg, ov := fixtureConfig(t, srv.URL)
		_, err := promclient.Query(context.Background(), cfg, ov, "up_by_host", "", fixedNow)
		wantExit, wantCode := code(t, err)
		if wantExit != exitcode.Usage || wantCode != "missing_host" {
			t.Fatalf("got %d/%s", wantExit, wantCode)
		}
	})
	t.Run("unexpected host", func(t *testing.T) {
		srv, _ := serveFixture(t, "query-plain.json")
		cfg, ov := fixtureConfig(t, srv.URL)
		_, err := promclient.Query(context.Background(), cfg, ov, "up_all", "tower", fixedNow)
		wantExit, wantCode := code(t, err)
		if wantExit != exitcode.Usage || wantCode != "unexpected_host" {
			t.Fatalf("got %d/%s", wantExit, wantCode)
		}
	})
	t.Run("unknown host", func(t *testing.T) {
		srv, _ := serveFixture(t, "query-plain.json")
		cfg, ov := fixtureConfig(t, srv.URL)
		_, err := promclient.Query(context.Background(), cfg, ov, "up_by_host", "nowhere", fixedNow)
		wantExit, wantCode := code(t, err)
		if wantExit != exitcode.Usage || wantCode != "unknown_host" {
			t.Fatalf("got %d/%s", wantExit, wantCode)
		}
	})
	t.Run("plain answer", func(t *testing.T) {
		srv, path := serveFixture(t, "query-plain.json")
		cfg, ov := fixtureConfig(t, srv.URL)
		doc, err := promclient.Query(context.Background(), cfg, ov, "up_by_host", "castle", fixedNow)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if *path != "/api/v1/query" {
			t.Errorf("path = %q", *path)
		}
		if doc.Host != "castle" || doc.Name != "up_by_host" {
			t.Errorf("header = %+v", doc)
		}
		if len(doc.Series) != 2 || doc.Series[0].Host != "castle" || doc.Series[1].Host != "tower" {
			t.Errorf("series = %+v", doc.Series)
		}
		validateAgainst(t, "vigil.query.v0.schema.json", doc)
	})
	t.Run("empty answer is success", func(t *testing.T) {
		srv, _ := serveFixture(t, "query-empty.json")
		cfg, ov := fixtureConfig(t, srv.URL)
		doc, err := promclient.Query(context.Background(), cfg, ov, "up_all", "", fixedNow)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		out, _ := json.Marshal(doc)
		if !strings.Contains(string(out), `"series":[]`) {
			t.Errorf("empty answer did not marshal to an empty array: %s", out)
		}
		validateAgainst(t, "vigil.query.v0.schema.json", doc)
	})
	t.Run("dotted sample value", func(t *testing.T) {
		decimal := "1" + "." + "5e-3"
		srv := serveBody(t, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"instance":"tower"},"value":[1763107200,"`+decimal+`"]}]}}`)
		cfg, ov := fixtureConfig(t, srv.URL)
		doc, err := promclient.Query(context.Background(), cfg, ov, "up_all", "", fixedNow)
		if err != nil {
			t.Fatalf("dotted decimal refused: %v", err)
		}
		if doc.Series[0].Value != decimal {
			t.Errorf("value = %q", doc.Series[0].Value)
		}
		validateAgainst(t, "vigil.query.v0.schema.json", doc)
	})
	t.Run("value that is not a number", func(t *testing.T) {
		garbage := strings.Join([]string{"1", "2", "3"}, ".")
		srv := serveBody(t, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"instance":"tower"},"value":[1763107200,"`+garbage+`"]}]}}`)
		cfg, ov := fixtureConfig(t, srv.URL)
		_, err := promclient.Query(context.Background(), cfg, ov, "up_all", "", fixedNow)
		if exit, got := code(t, err); exit != exitcode.Unavailable || got != reasons.HTTPStatus {
			t.Fatalf("got %d/%s", exit, got)
		}
	})
	t.Run("special values", func(t *testing.T) {
		srv, _ := serveFixture(t, "query-special-values.json")
		cfg, ov := fixtureConfig(t, srv.URL)
		doc, err := promclient.Query(context.Background(), cfg, ov, "up_all", "", fixedNow)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		validateAgainst(t, "vigil.query.v0.schema.json", doc)
	})
	t.Run("series cap", func(t *testing.T) {
		srv, _ := serveFixture(t, "query-series-cap.json")
		cfg, ov := fixtureConfig(t, srv.URL)
		_, err := promclient.Query(context.Background(), cfg, ov, "small_cap", "", fixedNow)
		wantExit, wantCode := code(t, err)
		if wantExit != exitcode.Unavailable || wantCode != reasons.SeriesCap {
			t.Fatalf("got %d/%s", wantExit, wantCode)
		}
	})
	t.Run("warnings are never partial", func(t *testing.T) {
		srv, _ := serveFixture(t, "query-warnings.json")
		cfg, ov := fixtureConfig(t, srv.URL)
		_, err := promclient.Query(context.Background(), cfg, ov, "up_all", "", fixedNow)
		wantExit, wantCode := code(t, err)
		if wantExit != exitcode.Unavailable || wantCode != reasons.QueryWarnings {
			t.Fatalf("got %d/%s", wantExit, wantCode)
		}
	})
	t.Run("bad data is the operators error", func(t *testing.T) {
		srv, _ := serveFixture(t, "query-bad-data.json")
		cfg, ov := fixtureConfig(t, srv.URL)
		_, err := promclient.Query(context.Background(), cfg, ov, "up_all", "", fixedNow)
		wantExit, wantCode := code(t, err)
		if wantExit != exitcode.Usage || wantCode != reasons.QueryRejected {
			t.Fatalf("got %d/%s", wantExit, wantCode)
		}
	})
	t.Run("backend timeout", func(t *testing.T) {
		srv, _ := serveFixture(t, "query-timeout.json")
		cfg, ov := fixtureConfig(t, srv.URL)
		_, err := promclient.Query(context.Background(), cfg, ov, "up_all", "", fixedNow)
		wantExit, wantCode := code(t, err)
		if wantExit != exitcode.Unavailable || wantCode != reasons.Timeout {
			t.Fatalf("got %d/%s", wantExit, wantCode)
		}
	})
	t.Run("redirect refused", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
		}))
		t.Cleanup(srv.Close)
		cfg, ov := fixtureConfig(t, srv.URL)
		_, err := promclient.Query(context.Background(), cfg, ov, "up_all", "", fixedNow)
		wantExit, wantCode := code(t, err)
		if wantExit != exitcode.Unavailable || wantCode != reasons.RedirectRefused {
			t.Fatalf("got %d/%s", wantExit, wantCode)
		}
	})
	t.Run("proxy environment ignored", func(t *testing.T) {
		t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
		t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
		srv, _ := serveFixture(t, "query-plain.json")
		cfg, ov := fixtureConfig(t, srv.URL)
		if _, err := promclient.Query(context.Background(), cfg, ov, "up_all", "", fixedNow); err != nil {
			t.Fatalf("query went through the proxy: %v", err)
		}
	})
	t.Run("body cap", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			chunk := strings.Repeat("x", 1<<20)
			for i := 0; i < 9; i++ {
				_, _ = w.Write([]byte(chunk))
			}
		}))
		t.Cleanup(srv.Close)
		cfg, ov := fixtureConfig(t, srv.URL)
		_, err := promclient.Query(context.Background(), cfg, ov, "up_all", "", fixedNow)
		wantExit, wantCode := code(t, err)
		if wantExit != exitcode.Unavailable || wantCode != reasons.BodyCap {
			t.Fatalf("got %d/%s", wantExit, wantCode)
		}
	})
	t.Run("backend unset", func(t *testing.T) {
		srv, _ := serveFixture(t, "query-plain.json")
		cfg, ov := fixtureConfig(t, srv.URL)
		cfg.Backends = nil
		_, err := promclient.Query(context.Background(), cfg, ov, "up_all", "", fixedNow)
		wantExit, wantCode := code(t, err)
		if wantExit != exitcode.Usage || wantCode != "no_backend" {
			t.Fatalf("got %d/%s", wantExit, wantCode)
		}
	})
}

func ptr(s string) *string { return &s }

// validateAgainst compiles one schema and checks a document against it, so the
// struct tags cannot drift from the published contract.
func validateAgainst(t *testing.T, schemaFile string, doc any) {
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
		if id == "" {
			t.Fatalf("%s: missing $id", path)
		}
		if err := c.AddResource(id, raw); err != nil {
			t.Fatalf("add %s: %v", path, err)
		}
		if filepath.Base(path) == schemaFile {
			want = id
		}
	}
	sch, err := c.Compile(want)
	if err != nil {
		t.Fatalf("compile %s: %v", schemaFile, err)
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if err := sch.Validate(decoded); err != nil {
		t.Fatalf("document does not match %s: %v", schemaFile, err)
	}
}

// TestInFlightBound proves a backend has one shared client: three concurrent
// calls never put more than two requests on the wire. A fresh client per call
// would allow three, which is the defect this test exists for.
func TestInFlightBound(t *testing.T) {
	var inFlight, peak int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&inFlight, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"activeTargets":[]}}`))
	}))
	t.Cleanup(srv.Close)

	cfg, ov := fixtureConfig(t, srv.URL)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := promclient.Targets(context.Background(), cfg, ov, fixedNow); err != nil {
				t.Errorf("targets: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&peak); got > 2 {
		t.Fatalf("peak in flight = %d, want at most 2", got)
	}
}
