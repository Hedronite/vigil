package summary_test

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Hedronite/vigil/internal/simplexdoc"
	"github.com/Hedronite/vigil/internal/summary"
)

// declared is a fresh authority on a declaration; empty means none.
func declared(id, severity, digest string) summary.Authority {
	a := ok(id, severity)
	if digest != "" {
		a.Document.Declaration = ptr(digest)
	}
	return a
}

func witness(id, state, digest string) summary.Source {
	s := summary.Source{ID: id, Kind: id, State: state}
	if digest != "" {
		s.Declaration = ptr(digest)
	}
	return s
}

func encode(t *testing.T, v any) string {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return string(body)
}

// TestDeclarationDrift: the reference is Vigil's own declaration, a witness
// that differs from it is drift, and after the grace window an authority's
// drift is worth YELLOW and never more.
func TestDeclarationDrift(t *testing.T) {
	staleB := declared("augur", green, digestB)
	staleB.Document.GeneratedAt, staleB.Document.ReceivedAt = ago(600), ago(600)
	cases := []struct {
		name      string
		reference string
		authority []summary.Authority
		rows      []summary.Source
		changed   *time.Time
		overall   string
		inGrace   bool
		drifted   bool
		reasoned  []string // verdicts that gain declaration_drift
		lists     string   // undeclared | by_declaration
	}{
		{
			name: "same declaration", reference: digestA, authority: []summary.Authority{declared("simplex", green, digestA)},
			overall: green, lists: `[]|[{"declaration":"A","subjects":["simplex"]}]`,
		},
		{
			name: "no reference", authority: []summary.Authority{declared("simplex", green, digestB)},
			overall: green, lists: `[]|[{"declaration":"B","subjects":["simplex"]}]`,
		},
		{
			name: "no reference, nothing declared", authority: []summary.Authority{declared("simplex", green, "")},
			overall: green, lists: `["simplex"]|[]`,
		},
		{
			name: "undeclared is not drift", reference: digestA, authority: []summary.Authority{declared("simplex", green, "")},
			overall: green, lists: `["simplex"]|[]`,
		},
		{
			name: "drift without a known change", reference: digestA, authority: []summary.Authority{declared("simplex", green, digestB)},
			overall: yellow, drifted: true, reasoned: []string{"simplex"}, lists: `[]|[{"declaration":"B","subjects":["simplex"]}]`,
		},
		{
			name: "drift inside the window", reference: digestA, authority: []summary.Authority{declared("simplex", green, digestB)},
			changed: ptr(ago(240)), overall: green, inGrace: true, drifted: true, lists: `[]|[{"declaration":"B","subjects":["simplex"]}]`,
		},
		{
			name: "drift at the instant of the change", reference: digestA, authority: []summary.Authority{declared("simplex", green, digestB)},
			changed: ptr(clock), overall: green, inGrace: true, drifted: true, lists: `[]|[{"declaration":"B","subjects":["simplex"]}]`,
		},
		{
			name: "drift one past the window", reference: digestA, authority: []summary.Authority{declared("simplex", green, digestB)},
			changed: ptr(ago(241)), overall: yellow, drifted: true, reasoned: []string{"simplex"}, lists: `[]|[{"declaration":"B","subjects":["simplex"]}]`,
		},
		{
			name: "a change after now opens no window", reference: digestA, authority: []summary.Authority{declared("simplex", green, digestB)},
			changed: ptr(ago(-1)), overall: yellow, drifted: true, reasoned: []string{"simplex"}, lists: `[]|[{"declaration":"B","subjects":["simplex"]}]`,
		},
		{
			name: "yellow stays yellow", reference: digestA, authority: []summary.Authority{declared("simplex", yellow, digestB)},
			overall: yellow, drifted: true, reasoned: []string{"simplex"}, lists: `[]|[{"declaration":"B","subjects":["simplex"]}]`,
		},
		{
			name: "red is preserved", reference: digestA, authority: []summary.Authority{declared("simplex", red, digestB)},
			overall: red, drifted: true, reasoned: []string{"simplex"}, lists: `[]|[{"declaration":"B","subjects":["simplex"]}]`,
		},
		{
			name: "only the drifting verdict says so", reference: digestA,
			authority: []summary.Authority{declared("simplex", green, digestA), declared("augur", green, digestB)},
			overall:   yellow, drifted: true, reasoned: []string{"augur"},
			lists: `[]|[{"declaration":"A","subjects":["simplex"]},{"declaration":"B","subjects":["augur"]}]`,
		},
		{
			name: "subjects are sorted and grouped", reference: digestA,
			authority: []summary.Authority{declared("simplex", green, digestA), declared("augur", green, digestA)},
			rows:      []summary.Source{witness("scry", "ok", ""), witness("cubit", "ok", "")},
			overall:   green, lists: `["cubit","scry"]|[{"declaration":"A","subjects":["augur","simplex"]}]`,
		},
		{
			name: "a stale witness is not compared", reference: digestA, authority: []summary.Authority{declared("simplex", green, digestA), staleB},
			rows:    []summary.Source{witness("scry", "stale", digestB), witness("cubit", "unavailable", digestB)},
			overall: yellow, lists: `[]|[{"declaration":"A","subjects":["simplex"]}]`,
		},
		{
			name: "informational drift is listed and colours nothing", reference: digestA,
			authority: []summary.Authority{declared("simplex", green, digestA)},
			rows:      []summary.Source{witness("scry", "ok", digestB), witness("cubit", "ok", digestB)},
			overall:   green, drifted: true,
			lists: `[]|[{"declaration":"A","subjects":["simplex"]},{"declaration":"B","subjects":["cubit","scry"]}]`,
		},
		{
			name: "a witness handed in twice is listed once", reference: digestA,
			authority: []summary.Authority{declared("simplex", green, digestA)},
			rows: []summary.Source{
				witness("scry", "ok", digestB), witness("scry", "ok", digestB),
				witness("cubit", "ok", ""), witness("cubit", "ok", ""),
			},
			overall: green, drifted: true,
			lists: `["cubit"]|[{"declaration":"A","subjects":["simplex"]},{"declaration":"B","subjects":["scry"]}]`,
		},
		{
			name: "backends are not witnesses", reference: digestA, authority: []summary.Authority{declared("simplex", green, digestA)},
			rows:    []summary.Source{witness("prom", "ok", digestB), witness("grafana", "ok", "")},
			overall: green, lists: `[]|[{"declaration":"A","subjects":["simplex"]}]`,
		},
		{
			name: "stale and unavailable authorities are not compared", reference: digestA, authority: []summary.Authority{gone("simplex"), staleOnly()},
			overall: yellow, lists: `[]|[]`,
		},
		{
			name: "drift cannot colour a null overall", reference: digestA, authority: []summary.Authority{gone("simplex")},
			rows: []summary.Source{witness("scry", "ok", digestB)}, overall: "null", drifted: true,
			lists: `[]|[{"declaration":"B","subjects":["scry"]}]`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := input(tc.authority...)
			if tc.reference != "" {
				in.Declaration = ptr(tc.reference)
			}
			in.Sources, in.VigilDeclChangedAt = tc.rows, tc.changed
			doc := combine(t, in)
			drift := doc.DeclarationDrift
			if overall(doc) != tc.overall || drift.InGrace != tc.inGrace || drift.Drifted != tc.drifted {
				t.Errorf("overall %s in_grace %v drifted %v, want %s %v %v", overall(doc), drift.InGrace, drift.Drifted, tc.overall, tc.inGrace, tc.drifted)
			}
			if encode(t, doc.Declaration) != encode(t, in.Declaration) || encode(t, drift.Reference) != encode(t, in.Declaration) {
				t.Errorf("declaration %v reference %v", doc.Declaration, drift.Reference)
			}
			short := strings.NewReplacer(digestA, "A", digestB, "B")
			if got := short.Replace(encode(t, drift.Undeclared) + "|" + encode(t, drift.ByDeclaration)); got != tc.lists {
				t.Errorf("lists\n%s\nwant\n%s", got, tc.lists)
			}
			reasoned := []string{}
			for _, v := range doc.Verdicts {
				if slices.Contains(v.Reasons, "declaration_drift") {
					reasoned = append(reasoned, v.Source)
					if v.State != "ok" || v.Severity == nil {
						t.Errorf("%s: drift changed the verdict to %s", v.Source, v.State)
					}
				}
			}
			if !slices.Equal(reasoned, append([]string{}, tc.reasoned...)) {
				t.Errorf("declaration_drift on %v, want %v", reasoned, tc.reasoned)
			}
		})
	}
}

// staleOnly is a stale augur on another declaration.
func staleOnly() summary.Authority {
	a := stale("augur", green)
	a.Document.Declaration = ptr(digestB)
	return a
}

// TestFromSimplex: the reader's answer becomes the combiner's input.
func TestFromSimplex(t *testing.T) {
	seen := &simplexdoc.Observation{
		ReceivedAt: ago(3),
		Document: &simplexdoc.Document{
			GeneratedAt: ago(5), Overall: red, Vantage: simplexdoc.Vantage{ID: "lab", Status: "blind"},
			Hosts: []simplexdoc.Host{host("lab", red, "vantage_blind")}, ValidFor: ptr(60), Declaration: ptr(digestA),
		},
	}
	a := summary.FromSimplex(seen, nil, 90*time.Second)
	if a.ID != "simplex" || a.StaleAfter != 90*time.Second || a.Document == nil || a.DetailCode != "" {
		t.Fatalf("authority = %+v", a)
	}
	d := a.Document
	if d.Severity != red || !d.GeneratedAt.Equal(ago(5)) || !d.ReceivedAt.Equal(ago(3)) || *d.ValidFor != 60 ||
		*d.Declaration != digestA || d.Vantage.Status != "blind" || len(d.Hosts) != 1 {
		t.Errorf("document = %+v", d)
	}

	major := &simplexdoc.UnavailableError{DetailCode: "schema_major", Cause: errors.New("another version")}
	for name, tc := range map[string]struct {
		seen *simplexdoc.Observation
		err  error
		code string
	}{
		"schema major":     {err: major, code: "schema_major"},
		"some other error": {err: errors.New("boom"), code: "source_unavailable"},
		"nothing at all":   {code: "source_unavailable"},
		"error wins":       {seen: seen, err: major, code: "schema_major"},
		"no document":      {seen: &simplexdoc.Observation{}, code: "source_unavailable"},
	} {
		got := summary.FromSimplex(tc.seen, tc.err, time.Minute)
		if got.Document != nil || got.DetailCode != tc.code {
			t.Errorf("%s = %+v, want %s and no document", name, got, tc.code)
		}
	}
}

// TestPackageIsPure: the combiner reads no clock, file, network or
// environment. Its answer is a function of its arguments.
func TestPackageIsPure(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	banned := []string{"os", "io", "io/fs", "net", "net/http", "syscall", "context", "math/rand", "math/rand/v2", "crypto/rand"}
	checked := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		checked++
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		for _, imp := range f.Imports {
			if name := strings.Trim(imp.Path.Value, `"`); slices.Contains(banned, name) {
				t.Errorf("%s imports %s", path, name)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if sel, isSel := n.(*ast.SelectorExpr); isSel {
				if pkg, isIdent := sel.X.(*ast.Ident); isIdent && pkg.Name == "time" {
					if slices.Contains([]string{"Now", "Since", "Until", "Sleep", "After", "Tick", "NewTimer", "NewTicker"}, sel.Sel.Name) {
						t.Errorf("%s calls time.%s", path, sel.Sel.Name)
					}
				}
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no source files checked")
	}
}
