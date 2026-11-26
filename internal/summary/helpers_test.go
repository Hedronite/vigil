package summary_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Hedronite/vigil/internal/simplexdoc"
	"github.com/Hedronite/vigil/internal/summary"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	green  = "GREEN"
	yellow = "YELLOW"
	red    = "RED"

	digestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// clock is the instant every test combines at.
var clock = time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)

func ago(seconds int) time.Time { return clock.Add(-time.Duration(seconds) * time.Second) }

func ptr[T any](v T) *T { return &v }

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test file")
	}
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// document is a readable authority document observed and received now.
func document(severity string) *summary.AuthorityDocument {
	return &summary.AuthorityDocument{Severity: severity, GeneratedAt: clock, ReceivedAt: clock}
}

func authority(id string, doc *summary.AuthorityDocument) summary.Authority {
	return summary.Authority{ID: id, StaleAfter: 120 * time.Second, Document: doc}
}

// ok, stale and gone are the three states an authority can arrive in.
func ok(id, severity string) summary.Authority { return authority(id, document(severity)) }

func stale(id, severity string) summary.Authority {
	doc := document(severity)
	doc.GeneratedAt, doc.ReceivedAt = ago(600), ago(600)
	return authority(id, doc)
}

func gone(id string) summary.Authority {
	return summary.Authority{ID: id, StaleAfter: 120 * time.Second, DetailCode: "source_unavailable"}
}

// withPlace gives a Simplex input its vantage and hosts.
func withPlace(a summary.Authority, status string, hosts ...simplexdoc.Host) summary.Authority {
	a.Document.Vantage = &simplexdoc.Vantage{ID: "lab", Status: status}
	a.Document.Hosts = hosts
	return a
}

func host(id, severity string, codes ...string) simplexdoc.Host {
	return simplexdoc.Host{ID: id, Severity: severity, Reasons: append([]string{}, codes...), Source: "simplex"}
}

func input(authorities ...summary.Authority) summary.Input {
	return summary.Input{Authorities: authorities, SkewBudget: 10 * time.Second, DeclarationGrace: 240 * time.Second}
}

func schema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	path := filepath.Join(repoRoot(t), "schema", "vigil.summary.v0.schema.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the schema: %v", err)
	}
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode the schema: %v", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("https://example.invalid/summary", doc); err != nil {
		t.Fatalf("add the schema: %v", err)
	}
	compiled, err := c.Compile("https://example.invalid/summary")
	if err != nil {
		t.Fatalf("compile the schema: %v", err)
	}
	return compiled
}

// emit encodes a document, holds it to the published schema and returns it as
// a decoded object. Every document a test builds goes through here.
func emit(t *testing.T, doc *summary.Document) map[string]any {
	t.Helper()
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if err := schema(t).Validate(any(out)); err != nil {
		t.Fatalf("the document does not satisfy vigil.summary/v0: %v\n%s", err, body)
	}
	return out
}

// combine builds a document at the test clock and validates it.
func combine(t *testing.T, in summary.Input) *summary.Document {
	t.Helper()
	doc := summary.Combine(in, clock)
	emit(t, doc)
	return doc
}

func overall(doc *summary.Document) string {
	if doc.Overall == nil {
		return "null"
	}
	return *doc.Overall
}
