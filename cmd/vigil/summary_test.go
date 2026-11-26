package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hedronite/vigil/internal/exitcode"
	"github.com/Hedronite/vigil/internal/summary"
)

const (
	// pinned is 30s after the vendored documents were generated.
	pinned = "2026-11-13T18:00:30Z"
	// pathMarker is in every path a test reads, so any echo of a path shows.
	pathMarker = "private-pathMarker"

	digestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	summaryConfig = `apiVersion: vigil.config/v0
authorities:
  - id: simplex
    path_ref: env:VIGIL_TEST_SIMPLEX
`
)

var generated = time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)

func writeText(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func vendoredDoc(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "testdata", "simplex", name, "document.json"))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(body)
}

// publish puts a document where env:VIGIL_TEST_SIMPLEX points, stamped as
// written at modified.
func publish(t *testing.T, body string, modified time.Time) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), pathMarker)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "simplex.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	t.Setenv("VIGIL_TEST_SIMPLEX", path)
	return path
}

// summarise runs `simplex summary --json` and holds stdout to the schema.
func summarise(t *testing.T, config string, more ...string) (int, summary.Document, string) {
	t.Helper()
	args := append([]string{"simplex", "summary", "--config", writeText(t, "config.yaml", config), "--json"}, more...)
	code, out, errs := live(t, args...)
	if errs != "" {
		t.Fatalf("exit %d with stderr %q", code, errs)
	}
	if strings.Contains(out, pathMarker) {
		t.Errorf("stdout names a path: %s", out)
	}
	if strings.Count(out, "\n") != 1 {
		t.Fatalf("stdout is not one line: %q", out)
	}
	var raw any
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	if err := loadSchema(t, "vigil.summary.v0.schema.json").Validate(raw); err != nil {
		t.Fatalf("stdout is not vigil.summary/v0: %v\n%s", err, out)
	}
	var doc summary.Document
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return code, doc, out
}

func colour(doc summary.Document) string {
	if doc.Overall == nil {
		return "null"
	}
	return *doc.Overall
}

// TestSimplexSummaryVerdicts: the exit code is the document's colour, and the
// config's bounds are the ones applied.
func TestSimplexSummaryVerdicts(t *testing.T) {
	declare := func(digest string) string {
		return strings.Replace(vendoredDoc(t, "green"), `"declaration": null`, `"declaration": "`+digest+`"`, 1)
	}
	cases := []struct {
		name    string
		config  string
		doc     string
		now     string
		written time.Duration // after generated_at
		exit    int
		overall string
		state   string
		reasons []string
	}{
		{name: "green", exit: 0, overall: "GREEN", state: "ok", reasons: []string{}},
		{name: "yellow", doc: vendoredDoc(t, "one-prom-down"), exit: 1, overall: "YELLOW", state: "ok", reasons: []string{}},
		{name: "red", doc: vendoredDoc(t, "both-prom-down"), exit: 2, overall: "RED", state: "ok", reasons: []string{}},
		{name: "blind vantage", doc: vendoredDoc(t, "blind"), exit: 2, overall: "RED", state: "ok", reasons: []string{}},
		{name: "stale green", now: "2026-11-13T18:10:00Z", exit: 1, overall: "YELLOW", state: "stale", reasons: []string{"source_stale"}},
		{name: "stale red", doc: vendoredDoc(t, "blind"), now: "2026-11-13T18:10:00Z", exit: 2, overall: "RED", state: "stale", reasons: []string{"source_stale"}},
		{name: "global stale_after", config: summaryConfig + "freshness:\n  stale_after: 20s\n", exit: 1, overall: "YELLOW", state: "stale", reasons: []string{"source_stale"}},
		{name: "authority stale_after", config: summaryConfig + "    stale_after: 20s\n", exit: 1, overall: "YELLOW", state: "stale", reasons: []string{"source_stale"}},
		{name: "authority overrides global", config: summaryConfig + "    stale_after: 600s\nfreshness:\n  stale_after: 20s\n", exit: 0, overall: "GREEN", state: "ok", reasons: []string{}},
		{name: "clock skew colours nothing", now: "2026-11-13T18:01:10Z", written: time.Minute, exit: 0, overall: "GREEN", state: "ok", reasons: []string{"clock_skew"}},
		{name: "skew budget from config", config: summaryConfig + "freshness:\n  skew_budget: 300s\n", now: "2026-11-13T18:01:10Z", written: time.Minute, exit: 0, overall: "GREEN", state: "ok", reasons: []string{}},
		{name: "same declaration", config: summaryConfig + "declaration: " + digestA + "\n", doc: declare(digestA), exit: 0, overall: "GREEN", state: "ok", reasons: []string{}},
		{name: "drift", config: summaryConfig + "declaration: " + digestA + "\n", doc: declare(digestB), exit: 1, overall: "YELLOW", state: "ok", reasons: []string{"declaration_drift"}},
		{name: "drift has no grace window here", config: summaryConfig + "declaration: " + digestA + "\nfreshness:\n  declaration_grace_s: 86400\n", doc: declare(digestB), exit: 1, overall: "YELLOW", state: "ok", reasons: []string{"declaration_drift"}},
		{name: "no reference, no drift", doc: declare(digestB), exit: 0, overall: "GREEN", state: "ok", reasons: []string{}},
		{name: "augur is not read yet", config: summaryConfig + "  - id: augur\n    path_ref: env:VIGIL_TEST_UNSET\n", exit: 0, overall: "GREEN", state: "ok", reasons: []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config, body, now := tc.config, tc.doc, tc.now
			if config == "" {
				config = summaryConfig
			}
			if body == "" {
				body = vendoredDoc(t, "green")
			}
			if now == "" {
				now = pinned
			}
			publish(t, body, generated.Add(tc.written))
			code, doc, _ := summarise(t, config, "--now", now)
			if code != tc.exit || colour(doc) != tc.overall {
				t.Errorf("exit %d overall %s, want %d %s", code, colour(doc), tc.exit, tc.overall)
			}
			if len(doc.Verdicts) != 1 || doc.Verdicts[0].Source != "simplex" {
				t.Fatalf("verdicts = %+v, want simplex alone", doc.Verdicts)
			}
			if v := doc.Verdicts[0]; v.State != tc.state || !slices.Equal(v.Reasons, tc.reasons) {
				t.Errorf("verdict %s %v, want %s %v", v.State, v.Reasons, tc.state, tc.reasons)
			}
			if doc.GeneratedAt != now || len(doc.Sources) != 1 || len(doc.Hosts) == 0 || doc.Vantage == nil {
				t.Errorf("document = %+v", doc)
			}
			if doc.DeclarationDrift == nil || doc.DeclarationDrift.InGrace {
				t.Errorf("drift = %+v, want a block with no grace window", doc.DeclarationDrift)
			}
		})
	}
}

// TestSimplexSummaryUnavailable: with nothing readable there is no colour. The
// document is still printed, the exit is 4 and no path is named anywhere.
func TestSimplexSummaryUnavailable(t *testing.T) {
	green := vendoredDoc(t, "green")
	cases := []struct {
		name   string
		config string
		setup  func(t *testing.T)
		code   string
	}{
		{name: "missing file", setup: func(t *testing.T) {
			t.Setenv("VIGIL_TEST_SIMPLEX", filepath.Join(t.TempDir(), pathMarker, "none.json"))
		}},
		{name: "variable not set", config: strings.Replace(summaryConfig, "VIGIL_TEST_SIMPLEX", "VIGIL_TEST_UNSET", 1)},
		{name: "literal path_ref", config: strings.Replace(summaryConfig, "env:VIGIL_TEST_SIMPLEX", "/nonexistent/"+pathMarker+"/simplex.json", 1)},
		{name: "relative value", setup: func(t *testing.T) { t.Setenv("VIGIL_TEST_SIMPLEX", pathMarker+"/simplex.json") }},
		{name: "malformed", setup: func(t *testing.T) { publish(t, "{", generated) }},
		{name: "a directory", setup: func(t *testing.T) { t.Setenv("VIGIL_TEST_SIMPLEX", filepath.Dir(publish(t, green, generated))) }},
		{name: "unobserved green", setup: func(t *testing.T) {
			publish(t, strings.Replace(green, `"observed": true`, `"observed": false`, 1), generated)
		}},
		{name: "another major", code: "schema_major", setup: func(t *testing.T) {
			publish(t, strings.Replace(green, "simplex.probe/v0", "simplex.probe/v1", 1), generated)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config, want := tc.config, tc.code
			if config == "" {
				config = summaryConfig
			}
			if want == "" {
				want = "source_unavailable"
			}
			if tc.setup != nil {
				tc.setup(t)
			}
			code, doc, _ := summarise(t, config, "--now", pinned)
			if code != exitcode.Unavailable || doc.Overall != nil || doc.Vantage != nil || len(doc.Hosts) != 0 {
				t.Fatalf("exit %d overall %s %+v", code, colour(doc), doc)
			}
			if v := doc.Verdicts[0]; v.State != "unavailable" || v.Severity != nil {
				t.Errorf("verdict = %+v", v)
			}
			if got := doc.Sources[0].DetailCode; got == nil || *got != want {
				t.Errorf("detail_code = %v, want %s", got, want)
			}

			// The table form says the same and names no path either.
			tcode, out, errs := live(t, "simplex", "summary", "--config", writeText(t, "config.yaml", config), "--now", pinned)
			if tcode != exitcode.Unavailable || errs != "" || !strings.Contains(out, want) || strings.Contains(out, pathMarker) {
				t.Errorf("table: exit %d stderr %q stdout %q", tcode, errs, out)
			}
		})
	}
}

// TestSimplexSummaryErrors: a command that cannot build a verdict prints one
// error line and nothing on stdout, and never exits as one.
func TestSimplexSummaryErrors(t *testing.T) {
	publish(t, vendoredDoc(t, "green"), generated)
	good := writeText(t, "config.yaml", summaryConfig)
	cases := []struct {
		name string
		args []string
		exit int
		code string
	}{
		{"no config", nil, 3, "missing_config"},
		{"no such config", []string{"--config", filepath.Join(t.TempDir(), pathMarker+".yaml")}, 3, "config_invalid"},
		{"invalid config", []string{"--config", writeText(t, "bad.yaml", summaryConfig+"nope: 1\n")}, 3, "config_invalid"},
		{"no simplex authority", []string{"--config", writeText(t, "augur.yaml", strings.Replace(summaryConfig, "id: simplex", "id: augur", 1))}, 3, "missing_authority"},
		{"wildcard listen", []string{"--config", writeText(t, "wild.yaml", summaryConfig+"listen: 0.0.0.0:10082\n")}, 5, "wildcard_listen"},
		{"bad overlay", []string{"--config", good, "--overlay", writeText(t, "overlay.yaml", "apiVersion: nope\n")}, 3, "overlay_invalid"},
		{"missing overlay file", []string{"--config", good, "--overlay", filepath.Join(t.TempDir(), "none.yaml")}, 3, "overlay_invalid"},
		{"bad now", []string{"--config", good, "--now", "yesterday"}, 3, "usage"},
		{"url config", []string{"--config", "https://example.invalid/config"}, 3, "usage"},
		{"extra argument", []string{"--config", good, "extra"}, 3, "usage"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, form := range [][]string{nil, {"--json"}} {
				args := append(append([]string{"simplex", "summary"}, tc.args...), form...)
				code, out, errs := live(t, args...)
				if code != tc.exit || out != "" {
					t.Fatalf("exit %d stdout %q, want %d and nothing", code, out, tc.exit)
				}
				if got := oneErrorLine(t, tc.name, errs); got != tc.code {
					t.Errorf("code %q, want %q", got, tc.code)
				}
				if strings.Contains(errs, pathMarker) || strings.Contains(errs, "example.invalid") {
					t.Errorf("stderr repeats input: %q", errs)
				}
			}
		})
	}
	code, out, errs := live(t, "simplex")
	if code != exitcode.Usage || out != "" || oneErrorLine(t, "simplex", errs) != "usage" {
		t.Errorf("a bare group: exit %d stdout %q", code, out)
	}
}

// TestSimplexSummaryTable: the default output is the rendered document, with no
// path in it.
func TestSimplexSummaryTable(t *testing.T) {
	publish(t, vendoredDoc(t, "blind"), generated)
	code, out, errs := live(t, "simplex", "summary", "--config", writeText(t, "config.yaml", summaryConfig), "--now", pinned)
	if code != exitcode.Red || errs != "" {
		t.Fatalf("exit %d stderr %q", code, errs)
	}
	for _, want := range []string{"OVERALL", "RED", "VANTAGE_STATUS", "blind", "vantage_blind", "SOURCE", "HOST", "simplex"} {
		if !strings.Contains(out, want) {
			t.Errorf("the table lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, pathMarker) || strings.Contains(out, "{") {
		t.Errorf("the table holds a path or raw JSON:\n%s", out)
	}
}

// TestSimplexSummaryClock: --now pins both the document and the ages, and
// without it the real clock is used.
func TestSimplexSummaryClock(t *testing.T) {
	publish(t, vendoredDoc(t, "green"), generated)
	_, _, first := summarise(t, summaryConfig, "--now", pinned)
	_, _, second := summarise(t, summaryConfig, "--now", pinned)
	if first != second {
		t.Errorf("two runs at one instant differ:\n%s\n%s", first, second)
	}
	_, offset, _ := summarise(t, summaryConfig, "--now", "2026-11-13T19:00:30+01:00")
	if offset.GeneratedAt != pinned {
		t.Errorf("an offset time gave generated_at %s, want %s", offset.GeneratedAt, pinned)
	}

	before := time.Now().UTC().Truncate(time.Second)
	live := strings.Replace(vendoredDoc(t, "green"), "2026-11-13T18:00:00Z", before.Format(time.RFC3339), 1)
	publish(t, live, before)
	code, doc, _ := summarise(t, summaryConfig)
	at, err := time.Parse(time.RFC3339Nano, doc.GeneratedAt)
	if err != nil {
		t.Fatalf("generated_at %q: %v", doc.GeneratedAt, err)
	}
	if code != exitcode.Green || at.Before(before) || at.Sub(before) > time.Minute {
		t.Errorf("exit %d generated_at %s, want GREEN as of now", code, doc.GeneratedAt)
	}
}

// TestSimplexSummaryContactsNoBackend: the command reads files only, even with
// backends configured and an overlay that points at a live server.
func TestSimplexSummaryContactsNoBackend(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(srv.Close)
	publish(t, vendoredDoc(t, "green"), generated)
	config := summaryConfig + "backends:\n  - id: prom\n    kind: prom\n  - id: grafana\n    kind: grafana\n"
	overlay := writeText(t, "overlay.yaml", "apiVersion: vigil.overlay/v0\nbackends:\n  - id: prom\n    address: "+srv.URL+"\n  - id: grafana\n    address: "+srv.URL+"\n")
	code, doc, _ := summarise(t, config, "--overlay", overlay, "--now", pinned)
	if code != exitcode.Green || len(doc.Sources) != 1 {
		t.Errorf("exit %d sources %+v", code, doc.Sources)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("%d requests reached a backend", n)
	}
}

// TestSimplexSummaryInterrupted: a cancelled run has no verdict. It says so
// instead of reporting the source as unavailable.
func TestSimplexSummaryInterrupted(t *testing.T) {
	publish(t, vendoredDoc(t, "green"), generated)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errb bytes.Buffer
	code := run(ctx, []string{"simplex", "summary", "--config", writeText(t, "config.yaml", summaryConfig), "--json"}, &out, &errb, liveHandlers())
	if code != exitcode.Unavailable || out.Len() != 0 {
		t.Fatalf("exit %d stdout %q", code, out.String())
	}
	if got := oneErrorLine(t, "interrupted", errb.String()); got != "interrupted" {
		t.Errorf("code %q", got)
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

// TestVerdictNeedsItsDocument: a verdict code means the document reached
// stdout. If it did not, the exit is an error, never 0 to 2.
func TestVerdictNeedsItsDocument(t *testing.T) {
	publish(t, vendoredDoc(t, "green"), generated)
	config := writeText(t, "config.yaml", summaryConfig)
	for _, now := range []string{pinned, "2026-11-13T18:10:00Z"} {
		var errb bytes.Buffer
		code := run(context.Background(), []string{"simplex", "summary", "--config", config, "--now", now}, brokenWriter{}, &errb, liveHandlers())
		if code != exitcode.Internal {
			t.Errorf("exit %d, want %d", code, exitcode.Internal)
		}
		if got := oneErrorLine(t, "broken stdout", errb.String()); got != "internal" {
			t.Errorf("code %q", got)
		}
	}
}

// TestVerdictOf: only a colour or its absence leaves as a document's code.
func TestVerdictOf(t *testing.T) {
	text := func(s string) *string { return &s }
	for _, tc := range []struct {
		overall *string
		want    int
	}{{text("GREEN"), 0}, {text("YELLOW"), 1}, {text("RED"), 2}, {nil, 4}, {text("BLUE"), 6}} {
		h := Handlers{Validate: func(_ context.Context, inv Invocation) error {
			if _, err := inv.Stdout.Write([]byte("document\n")); err != nil {
				return err
			}
			return verdictOf(&summary.Document{Overall: tc.overall})
		}}
		code, out, errs := call(t, h, "validate")
		printed := tc.want != exitcode.Internal
		if code != tc.want || (out == "document\n") != printed || (errs == "") != printed {
			t.Errorf("%v: exit %d stdout %q stderr %q, want %d", tc.overall, code, out, errs, tc.want)
		}
	}
}

// TestSimplexSummaryHelp: the subcommand has help of its own.
func TestSimplexSummaryHelp(t *testing.T) {
	code, out, errs := call(t, defaultHandlers(), "simplex", "summary", "--help")
	if code != exitcode.Green || errs != "" || !strings.Contains(out, "vigil simplex summary") || !strings.Contains(out, "vigil.summary/v0") {
		t.Errorf("exit %d stdout %q stderr %q", code, out, errs)
	}
}
