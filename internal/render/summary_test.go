package render_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hedronite/vigil/internal/render"
	"github.com/Hedronite/vigil/internal/simplexdoc"
	"github.com/Hedronite/vigil/internal/summary"
)

const (
	digestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func text(s string) *string { return &s }

func goldenSummary(t *testing.T, name string) *summary.Document {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "testdata", "summary", name, "document.json"))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var doc summary.Document
	mustDecode(t, body, &doc)
	return &doc
}

// drifted carries every optional member the goldens leave out.
func drifted(t *testing.T) *summary.Document {
	t.Helper()
	doc := goldenSummary(t, "simplex-green")
	seconds := 60
	doc.Overall = text("YELLOW")
	doc.Declaration = text(digestA)
	v := &doc.Verdicts[0]
	v.ReceivedAt, v.ValidFor, v.Declaration = text("2026-11-13T18:00:00Z"), &seconds, text(digestB)
	v.Reasons = []string{"clock_skew", "declaration_drift"}
	doc.Sources = append(doc.Sources, summary.Source{ID: "prom", Kind: "prom", State: "ok", Vantage: "lab"})
	doc.DeclarationDrift = &summary.Drift{
		Reference: text(digestA), Drifted: true, Undeclared: []string{"cubit"},
		ByDeclaration: []summary.Group{{Declaration: digestA, Subjects: []string{"scry"}}, {Declaration: digestB, Subjects: []string{"augur", "simplex"}}},
	}
	return doc
}

func table(t *testing.T, doc *summary.Document) string {
	t.Helper()
	var b bytes.Buffer
	if err := render.Summary(&b, doc); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

// TestSummaryGoldens pins the table of each shape a summary takes.
func TestSummaryGoldens(t *testing.T) {
	docs := map[string]*summary.Document{"drift": drifted(t)}
	for _, name := range []string{"simplex-green", "simplex-stale-green", "all-unavailable", "vantage-blind"} {
		docs[name] = goldenSummary(t, name)
	}
	for name, doc := range docs {
		t.Run(name, func(t *testing.T) {
			got := table(t, doc)
			for _, line := range strings.Split(got, "\n") {
				if strings.TrimRight(line, " \t") != line {
					t.Errorf("trailing space on %q", line)
				}
			}
			compareGolden(t, "summary-"+name+".txt", []byte(got))
		})
	}
}

// TestSummaryTablePrintsOnlyWhatTheDocumentHas: a member the document lacks
// has no column and no line, and a null one is a dash.
func TestSummaryTablePrintsOnlyWhatTheDocumentHas(t *testing.T) {
	bare := table(t, goldenSummary(t, "simplex-green"))
	for _, absent := range []string{"RECEIVED_AT", "VALID_FOR", "DETAIL_CODE", "DRIFTED", "IN_GRACE", "SUBJECTS", "unavailable"} {
		if strings.Contains(bare, absent) {
			t.Errorf("a document without it prints %s:\n%s", absent, bare)
		}
	}
	if got := lineFields(t, bare, "DECLARATION"); !equalFields(got, []string{"DECLARATION", "-"}) {
		t.Errorf("null declaration = %v", got)
	}
	if got := lineFields(t, bare, "edge"); !equalFields(got, []string{"edge", "GREEN", "-"}) {
		t.Errorf("a fresh host row = %v, want no state or age", got)
	}

	// A stale source is shown with its age, on the verdict and on every host
	// that came from it, so no row reads as current.
	stale := table(t, goldenSummary(t, "simplex-stale-green"))
	if got := lineFields(t, stale, "edge"); !equalFields(got, []string{"edge", "GREEN", "stale", "180s", "-"}) {
		t.Errorf("a stale host row = %v", got)
	}
	if got := lineFields(t, stale, "simplex"); !equalFields(got, []string{"simplex", "stale", "GREEN", "2026-11-13T17:57:00Z", "180s", "source_stale"}) {
		t.Errorf("a stale verdict row = %v", got)
	}

	// Nothing readable: no colour, no vantage, no hosts and no age.
	none := table(t, goldenSummary(t, "all-unavailable"))
	if got := lineFields(t, none, "OVERALL"); !equalFields(got, []string{"OVERALL", "unavailable"}) {
		t.Errorf("null overall = %v", got)
	}
	for _, absent := range []string{"VANTAGE", "HOST", "AGE", "GREEN"} {
		if strings.Contains(none, absent) {
			t.Errorf("an unreadable summary prints %s:\n%s", absent, none)
		}
	}

	// The data blocks are never printed.
	doc := goldenSummary(t, "scry-unobserved")
	if len(doc.Scry) == 0 {
		t.Fatal("the golden lost its scry block")
	}
	if got := table(t, doc); strings.Contains(got, "observed\"") || strings.Contains(got, "{") {
		t.Errorf("a data block reached the table:\n%s", got)
	}
}

// TestSummaryTableOddInput: a time the renderer cannot read has no age, and a
// document with no hosts has no host table.
func TestSummaryTableOddInput(t *testing.T) {
	doc := &summary.Document{
		GeneratedAt: "not a time",
		Verdicts:    []summary.Verdict{{Source: "simplex", State: "stale", ObservedAt: text("2026-11-13T17:57:00Z")}},
		Hosts:       []simplexdoc.Host{{ID: "lab", Severity: "GREEN"}},
	}
	got := table(t, doc)
	if strings.Contains(got, "AGE") {
		t.Errorf("an age from an unreadable time:\n%s", got)
	}
	if fields := lineFields(t, got, "lab"); !equalFields(fields, []string{"lab", "GREEN", "stale", "-"}) {
		t.Errorf("host row = %v", fields)
	}
	doc.Hosts = nil
	if got := table(t, doc); strings.Contains(got, "HOST") {
		t.Errorf("a host table with no hosts:\n%s", got)
	}
}

type failAfter struct{ left int }

func (f *failAfter) Write(p []byte) (int, error) {
	if f.left <= 0 {
		return 0, errors.New("full")
	}
	f.left--
	return len(p), nil
}

// TestSummaryReportsWriteErrors: a table that was cut short is an error.
func TestSummaryReportsWriteErrors(t *testing.T) {
	doc := drifted(t)
	var counted countingWriter
	if err := render.Summary(&counted, doc); err != nil {
		t.Fatalf("render: %v", err)
	}
	for allowed := 0; allowed < counted.writes; allowed++ {
		if err := render.Summary(&failAfter{left: allowed}, doc); err == nil {
			t.Errorf("failing after %d of %d writes was not reported", allowed, counted.writes)
		}
	}
}

type countingWriter struct{ writes int }

func (c *countingWriter) Write(p []byte) (int, error) {
	c.writes++
	return len(p), nil
}
