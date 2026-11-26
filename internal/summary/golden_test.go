package summary_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Hedronite/vigil/internal/simplexdoc"
	"github.com/Hedronite/vigil/internal/summary"
)

// goldenHosts is the three-host mesh most goldens carry.
func goldenHosts() []simplexdoc.Host {
	return []simplexdoc.Host{
		host("edge", green),
		host("lab", green),
		host("spoke", green, "expected_dark", "tcp_connect_refused", "tcp_connect_ok"),
	}
}

func goldenSimplex(severity string) summary.Authority {
	return withPlace(ok("simplex", severity), "ok", goldenHosts()...)
}

// row is an informational or backend row that could not be read.
func row(id string) summary.Source {
	return summary.Source{ID: id, Kind: id, State: "unavailable"}
}

// goldenInputs holds what each golden is built from, written from the case's
// description and not read back from its document.
func goldenInputs() map[string]summary.Input {
	late := stale("simplex", green)
	late.Document.GeneratedAt, late.Document.ReceivedAt = ago(180), ago(180)

	major := gone("simplex")
	major.DetailCode = "schema_major"

	blind := withPlace(ok("simplex", red), "blind", host("edge", red, "vantage_blind"), host("lab", red, "vantage_blind"))
	both := withPlace(ok("simplex", yellow), "unchecked",
		host("edge", yellow, "tcp_connect_ok", "prom_pair_degraded"), host("lab", green))

	grafana := input(goldenSimplex(green))
	grafana.Sources = []summary.Source{row("grafana")}
	scry := input(goldenSimplex(green))
	scry.Sources = []summary.Source{row("scry")}
	scry.Scry = json.RawMessage(`{"observed":false}`)

	return map[string]summary.Input{
		"all-unavailable":          input(gone("simplex"), gone("augur")),
		"augur-unavailable":        input(goldenSimplex(green), gone("augur")),
		"grafana-down":             grafana,
		"schema-major-mismatch":    input(major, ok("augur", green)),
		"scry-unobserved":          scry,
		"signal-present":           input(goldenSimplex(green)),
		"simplex-green":            input(goldenSimplex(green)),
		"simplex-stale-green":      input(withPlace(late, "ok", goldenHosts()...)),
		"simplex-yellow-augur-red": input(both, ok("augur", red)),
		"vantage-blind":            input(blind),
	}
}

// additive are the members the goldens predate. They are checked by the
// freshness and drift tests; here they are set aside, and nothing else is.
var additive = []string{"received_at", "valid_for", "declaration", "declaration_drift"}

func strip(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	for _, key := range additive {
		delete(doc, key)
	}
	for _, list := range []string{"verdicts", "sources"} {
		items, isList := doc[list].([]any)
		if !isList {
			t.Fatalf("%s is not a list", list)
		}
		for _, item := range items {
			entry, isObject := item.(map[string]any)
			if !isObject {
				t.Fatalf("%s holds a non-object", list)
			}
			for _, key := range additive {
				delete(entry, key)
			}
		}
	}
	return doc
}

// TestGoldens builds every golden under testdata/summary, found rather than
// listed, so a new one without an input fails.
func TestGoldens(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "testdata", "summary")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the goldens: %v", err)
	}
	inputs := goldenInputs()
	if len(entries) != 10 || len(inputs) != 10 {
		t.Fatalf("%d goldens and %d inputs, want 10 of each", len(entries), len(inputs))
	}
	for _, entry := range entries {
		name := entry.Name()
		t.Run(name, func(t *testing.T) {
			in, known := inputs[name]
			if !known {
				t.Fatal("no input for this golden")
			}
			body, err := os.ReadFile(filepath.Join(dir, name, "document.json"))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			for _, key := range additive {
				if strings.Contains(string(body), `"`+key+`"`) {
					t.Fatalf("the golden now carries %s, which this test would set aside", key)
				}
			}
			var want map[string]any
			if err := json.Unmarshal(body, &want); err != nil {
				t.Fatalf("decode: %v", err)
			}
			doc := summary.Combine(in, clock)
			if got := strip(t, emit(t, doc)); !reflect.DeepEqual(got, want) {
				encoded, err := json.Marshal(got)
				if err != nil {
					t.Fatalf("encode: %v", err)
				}
				t.Errorf("built\n%s\nwant\n%s", encoded, body)
			}
			code, err := os.ReadFile(filepath.Join(dir, name, "expected_exit"))
			if err != nil {
				t.Fatalf("read expected_exit: %v", err)
			}
			if got := strconv.Itoa(summary.ExitCode(doc)); got != strings.TrimSpace(string(code)) {
				t.Errorf("exit %s, want %s", got, code)
			}
		})
	}
}

// TestVendoredSimplexCases runs every vendored case through the reader and the
// combiner: a fresh document's colour is the summary's, and no file means no
// verdict.
func TestVendoredSimplexCases(t *testing.T) {
	dir, err := filepath.Abs(filepath.Join(repoRoot(t), "testdata", "simplex"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the cases: %v", err)
	}
	cases := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		cases++
		t.Run(entry.Name(), func(t *testing.T) {
			seen, readErr := simplexdoc.Read(t.Context(), filepath.Join(dir, entry.Name(), "document.json"), time.Now())
			var now time.Time
			if readErr == nil {
				now = seen.Document.GeneratedAt
				seen.ReceivedAt = now
			}
			doc := summary.Combine(input(summary.FromSimplex(seen, readErr, 120*time.Second)), now.Add(time.Second))
			emit(t, doc)
			if readErr != nil {
				if doc.Overall != nil || summary.ExitCode(doc) != 4 || doc.Vantage != nil || len(doc.Hosts) != 0 {
					t.Errorf("no document gave %s %+v", overall(doc), doc.Vantage)
				}
				return
			}
			if overall(doc) != seen.Document.Overall || doc.Verdicts[0].State != "ok" {
				t.Errorf("overall %s state %s, the document says %s", overall(doc), doc.Verdicts[0].State, seen.Document.Overall)
			}
			if !reflect.DeepEqual(doc.Hosts, seen.Document.Hosts) || doc.Vantage == nil || *doc.Vantage != seen.Document.Vantage {
				t.Errorf("hosts or vantage were not carried as published")
			}
			if !reflect.DeepEqual(doc.Verdicts[0].ValidFor, seen.Document.ValidFor) {
				t.Errorf("valid_for = %v, want the producer's %v", doc.Verdicts[0].ValidFor, seen.Document.ValidFor)
			}
		})
	}
	if cases != 13 {
		t.Errorf("%d cases, want 13", cases)
	}
}
