package render_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	jsv6 "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/Hedronite/vigil/internal/grafana"
	"github.com/Hedronite/vigil/internal/promclient"
	"github.com/Hedronite/vigil/internal/render"
	"github.com/Hedronite/vigil/internal/sources"
)

var update = flag.Bool("update", false, "rewrite the golden files instead of comparing")

// repoRoot walks up from this test file to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// mustDecode decodes a fixture into v or fails.
func mustDecode(t *testing.T, b []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
}

// decode is the per-kind fixture decoder for the case table.
func decode[T any](t *testing.T, b []byte) any {
	t.Helper()
	var d T
	mustDecode(t, b, &d)
	return &d
}

// kase is one document: which fixture it comes from and how it renders.
type kase struct {
	name   string // fixture name under testdata/<kind>/valid
	kind   string // document kind: targets, query, health, sources
	schema string // schema file name under schema/
	decode func(t *testing.T, b []byte) any
	// render writes the human table for the decoded document.
	render func(w io.Writer, doc any) error
	// noTableGolden marks a fixture whose table cannot be a tracked file:
	// the scrub's deny-by-default hostname rule reads dotted sample values
	// (-0.5, 1.5e-3) in a table cell as names, and the strip only blanks
	// them in the JSON value shape. Its table is pinned in memory instead.
	noTableGolden bool
}

// cases pins every valid fixture the schemas offer, so a backend fixture
// that stops rendering surfaces here. The names double as the golden names.
func cases() []kase {
	return []kase{
		{
			name: "mixed", kind: "targets", schema: "vigil.targets.v0.schema.json",
			decode: decode[promclient.TargetsDoc],
			render: func(w io.Writer, doc any) error { return render.Targets(w, doc.(*promclient.TargetsDoc)) },
		},
		{
			name: "empty", kind: "targets", schema: "vigil.targets.v0.schema.json",
			decode: decode[promclient.TargetsDoc],
			render: func(w io.Writer, doc any) error { return render.Targets(w, doc.(*promclient.TargetsDoc)) },
		},
		{
			name: "one-host", kind: "query", schema: "vigil.query.v0.schema.json",
			decode: decode[promclient.QueryDoc],
			render: func(w io.Writer, doc any) error { return render.Query(w, doc.(*promclient.QueryDoc)) },
		},
		{
			name: "all-hosts", kind: "query", schema: "vigil.query.v0.schema.json",
			decode: decode[promclient.QueryDoc],
			render: func(w io.Writer, doc any) error { return render.Query(w, doc.(*promclient.QueryDoc)) },
		},
		{
			name: "special-values", kind: "query", schema: "vigil.query.v0.schema.json",
			noTableGolden: true,
			decode:        decode[promclient.QueryDoc],
			render:        func(w io.Writer, doc any) error { return render.Query(w, doc.(*promclient.QueryDoc)) },
		},
		{
			name: "ok", kind: "health", schema: "vigil.health.v0.schema.json",
			decode: decode[grafana.HealthDoc],
			render: func(w io.Writer, doc any) error { return render.Health(w, doc.(*grafana.HealthDoc)) },
		},
		{
			name: "unavailable", kind: "health", schema: "vigil.health.v0.schema.json",
			decode: decode[grafana.HealthDoc],
			render: func(w io.Writer, doc any) error { return render.Health(w, doc.(*grafana.HealthDoc)) },
		},
		{
			name: "backends", kind: "sources", schema: "vigil.sources.v0.schema.json",
			decode: decode[sources.Doc],
			render: func(w io.Writer, doc any) error { return render.Sources(w, doc.(*sources.Doc)) },
		},
		{
			name: "empty", kind: "sources", schema: "vigil.sources.v0.schema.json",
			decode: decode[sources.Doc],
			render: func(w io.Writer, doc any) error { return render.Sources(w, doc.(*sources.Doc)) },
		},
	}
}

// goldenPath returns the golden file path and whether it is missing.
func goldenPath(t *testing.T, name string) (string, bool) {
	t.Helper()
	p := filepath.Join(repoRoot(t), "testdata", "render", name)
	_, err := os.Stat(p)
	return p, os.IsNotExist(err)
}

// compareGolden compares got with the golden, or rewrites it under -update.
func compareGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path, missing := goldenPath(t, name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create golden dir: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	if missing {
		t.Fatalf("golden %s is missing; run go test ./internal/render -update", name)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if !bytes.Equal(want, got) {
		t.Errorf("output differs from %s:\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
	}
}

// TestRenderGoldens renders every valid fixture both ways and pins both
// shapes: the table against a .txt golden, the JSON against a .json golden,
// and byte stability by rendering the JSON twice.
func TestRenderGoldens(t *testing.T) {
	root := repoRoot(t)
	for _, kc := range cases() {
		t.Run(kc.kind+"/"+kc.name, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join(root, "testdata", kc.kind, "valid", kc.name+".json"))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			doc := kc.decode(t, b)

			if !kc.noTableGolden {
				var table bytes.Buffer
				if err := kc.render(&table, doc); err != nil {
					t.Fatalf("render table: %v", err)
				}
				compareGolden(t, kc.kind+"-"+kc.name+".txt", table.Bytes())
			}

			var first, second bytes.Buffer
			if err := render.JSON(&first, doc); err != nil {
				t.Fatalf("render json: %v", err)
			}
			if err := render.JSON(&second, doc); err != nil {
				t.Fatalf("render json again: %v", err)
			}
			if !bytes.Equal(first.Bytes(), second.Bytes()) {
				t.Error("json output is not byte-stable across renders")
			}
		})
	}
}

// compileKind compiles one schema after registering every sibling, so the
// sources document's cross-file reference resolves offline. It mirrors
// internal/schematest.
func compileKind(t *testing.T, root, file string) *jsv6.Schema {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(root, "schema", "*.schema.json"))
	if err != nil {
		t.Fatalf("glob schemas: %v", err)
	}
	c := jsv6.NewCompiler()
	want := ""
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", filepath.Base(f), err)
		}
		var doc any
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatalf("decode %s: %v", filepath.Base(f), err)
		}
		id := docID(doc)
		if id == "" {
			t.Fatalf("%s: missing $id", filepath.Base(f))
		}
		if err := c.AddResource(id, doc); err != nil {
			t.Fatalf("%s: add resource: %v", filepath.Base(f), err)
		}
		if filepath.Base(f) == file {
			want = id
		}
	}
	if want == "" {
		t.Fatalf("%s: no such schema", file)
	}
	sch, err := c.Compile(want)
	if err != nil {
		t.Fatalf("compile %s: %v", file, err)
	}
	return sch
}

func docID(doc any) string {
	m, ok := doc.(map[string]any)
	if !ok {
		return ""
	}
	id, _ := m["$id"].(string)
	return id
}

// TestJSONBytes pins the exact --json bytes for one document per kind, plus
// the special-values document. The goldens live here rather than in tracked
// files: the scrub's host-slug rule cannot parse a compact JSON line (the
// value capture runs past the key it reads), and the bytes must stay the
// one-line shape the command prints.
func TestJSONBytes(t *testing.T) {
	root := repoRoot(t)
	load := func(kind, name string) []byte {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(root, "testdata", kind, "valid", name+".json"))
		if err != nil {
			t.Fatalf("read fixture: %v", err)
		}
		return b
	}
	jsonBytes := func(t *testing.T, kc kase) []byte {
		t.Helper()
		var buf bytes.Buffer
		if err := render.JSON(&buf, kc.decode(t, load(kc.kind, kc.name))); err != nil {
			t.Fatalf("render json: %v", err)
		}
		return buf.Bytes()
	}
	for _, kc := range cases() {
		if kc.kind != "targets" && kc.kind != "query" && kc.kind != "health" && kc.kind != "sources" {
			continue
		}
		switch {
		case kc.kind == "targets" && kc.name == "mixed":
			want := `{"apiVersion":"vigil.targets/v0","backend":"prom","vantage":"lathe","generated_at":"2026-11-14T09:00:00Z","targets":[{"host":"castle","job":"node","health":"up","last_scrape":"2026-11-14T09:00:00Z"},{"host":"unmapped","job":"redacted","health":"unknown","last_scrape":null}]}` + "\n"
			if got := string(jsonBytes(t, kc)); got != want {
				t.Errorf("targets json = %s, want %s", got, want)
			}
		case kc.kind == "query" && kc.name == "one-host":
			want := `{"apiVersion":"vigil.query/v0","backend":"prom","vantage":"lathe","name":"up_by_host","host":"castle","evaluated_at":"2026-11-14T09:00:00Z","series":[{"host":"castle","job":"node","value":"1"}]}` + "\n"
			if got := string(jsonBytes(t, kc)); got != want {
				t.Errorf("query json = %s, want %s", got, want)
			}
		case kc.kind == "query" && kc.name == "all-hosts":
			want := `{"apiVersion":"vigil.query/v0","backend":"prom","vantage":"lathe","name":"up_by_host","evaluated_at":"2026-11-14T09:00:00Z","series":[]}` + "\n"
			if got := string(jsonBytes(t, kc)); got != want {
				t.Errorf("query json = %s, want %s", got, want)
			}
		case kc.kind == "health" && kc.name == "ok":
			want := `{"apiVersion":"vigil.health/v0","backend":"grafana","state":"ok","detail_code":null,"checked_at":"2026-11-14T09:00:00Z"}` + "\n"
			if got := string(jsonBytes(t, kc)); got != want {
				t.Errorf("health json = %s, want %s", got, want)
			}
		case kc.kind == "sources" && kc.name == "backends":
			want := `{"apiVersion":"vigil.sources/v0","generated_at":"2026-11-14T09:00:00Z","sources":[{"id":"prom","kind":"prom","state":"ok","observed_at":"2026-11-14T09:00:00Z","last_ok":"2026-11-14T09:00:00Z","vantage":"lathe"},{"id":"grafana","kind":"grafana","state":"unavailable","observed_at":null,"last_ok":null,"detail_code":"not_health"}]}` + "\n"
			if got := string(jsonBytes(t, kc)); got != want {
				t.Errorf("sources json = %s, want %s", got, want)
			}
		}
	}
}

// TestRenderedJSONConformsToSchema re-marshals each decoded fixture and
// validates the bytes against the document schema, so the struct tags cannot
// drift from the contract.
func TestRenderedJSONConformsToSchema(t *testing.T) {
	root := repoRoot(t)
	schemas := map[string]*jsv6.Schema{}
	for _, kc := range cases() {
		sch, ok := schemas[kc.schema]
		if !ok {
			sch = compileKind(t, root, kc.schema)
			schemas[kc.schema] = sch
		}
		t.Run(kc.kind+"/"+kc.name, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join(root, "testdata", kc.kind, "valid", kc.name+".json"))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			doc := kc.decode(t, b)
			out, err := json.Marshal(doc)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var v any
			if err := json.Unmarshal(out, &v); err != nil {
				t.Fatalf("re-decode: %v", err)
			}
			if err := sch.Validate(v); err != nil {
				t.Errorf("rendered json does not conform to %s: %v", kc.schema, err)
			}
		})
	}
}

// lineFields returns the whitespace-separated fields of the first line whose
// first field is key, or fails.
func lineFields(t *testing.T, s, key string) []string {
	t.Helper()
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		f := strings.Fields(line)
		if len(f) > 0 && f[0] == key {
			return f
		}
	}
	t.Fatalf("no line starts with %q in:\n%s", key, s)
	return nil
}

// blockHasKey reports whether any key-value block line (before the blank
// separator) starts with key.
func blockHasKey(s, key string) bool {
	for _, line := range strings.Split(s, "\n") {
		if line == "" {
			return false
		}
		f := strings.Fields(line)
		if len(f) > 0 && f[0] == key {
			return true
		}
	}
	return false
}

// headerFields returns the fields of the table header line (the line after
// the blank separator).
func headerFields(t *testing.T, s string) []string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range lines {
		if line == "" && i+1 < len(lines) {
			return strings.Fields(lines[i+1])
		}
	}
	t.Fatalf("no table header after a blank line in:\n%s", s)
	return nil
}

// TestTablesPrintOnlyWhatTheDocumentHas pins the field contract beyond the
// goldens: a column or key line exists only when the document carries the
// field, and a present null renders as a dash, never as a value.
func TestTablesPrintOnlyWhatTheDocumentHas(t *testing.T) {
	root := repoRoot(t)
	read := func(kind, name string) []byte {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(root, "testdata", kind, "valid", name+".json"))
		if err != nil {
			t.Fatalf("read fixture: %v", err)
		}
		return b
	}

	// query: not run for one host, so the document has no host key and no
	// series carries a job.
	var qd promclient.QueryDoc
	mustDecode(t, read("query", "all-hosts"), &qd)
	var qbuf bytes.Buffer
	if err := render.Query(&qbuf, &qd); err != nil {
		t.Fatalf("render query: %v", err)
	}
	if got := headerFields(t, qbuf.String()); !equalFields(got, []string{"HOST", "VALUE"}) {
		t.Errorf("all-hosts header = %v, want HOST VALUE", got)
	}
	if blockHasKey(qbuf.String(), "HOST") {
		t.Error("query table prints a HOST key line the document lacks")
	}

	// query: run for one host and its series carries a job, so both appear.
	var qd1 promclient.QueryDoc
	mustDecode(t, read("query", "one-host"), &qd1)
	var qbuf1 bytes.Buffer
	if err := render.Query(&qbuf1, &qd1); err != nil {
		t.Fatalf("render query: %v", err)
	}
	if got := lineFields(t, qbuf1.String(), "HOST"); !equalFields(got, []string{"HOST", "castle"}) {
		t.Errorf("one-host HOST line = %v, want HOST castle", got)
	}
	if got := headerFields(t, qbuf1.String()); !equalFields(got, []string{"HOST", "JOB", "VALUE"}) {
		t.Errorf("one-host header = %v, want HOST JOB VALUE", got)
	}

	// query: no publishable job in any series, so the column stays out.
	var qd2 promclient.QueryDoc
	mustDecode(t, read("query", "special-values"), &qd2)
	var qbuf2 bytes.Buffer
	if err := render.Query(&qbuf2, &qd2); err != nil {
		t.Fatalf("render query: %v", err)
	}
	if got := headerFields(t, qbuf2.String()); !equalFields(got, []string{"HOST", "VALUE"}) {
		t.Errorf("special-values header = %v, want HOST VALUE", got)
	}
	// Dotted sample values render verbatim. This table is pinned in memory,
	// not as a tracked golden: the scrub's hostname rule reads a dotted cell
	// as a name, and the value strip only covers the JSON value shape.
	for key, want := range map[string][]string{
		"castle":  {"castle", "1"},
		"tower":   {"tower", "-0.5"},
		"lathe":   {"lathe", "1.5e-3"},
		"box":     {"box", "NaN"},
		"apiary":  {"apiary", "+Inf"},
		"citadel": {"citadel", "-Inf"},
	} {
		if got := lineFields(t, qbuf2.String(), key); !equalFields(got, want) {
			t.Errorf("special-values row %s = %v, want %v", key, got, want)
		}
	}

	// sources: detail code only on the failed row, vantage only on prom.
	var sd sources.Doc
	mustDecode(t, read("sources", "backends"), &sd)
	var sbuf bytes.Buffer
	if err := render.Sources(&sbuf, &sd); err != nil {
		t.Fatalf("render sources: %v", err)
	}
	want := []string{"ID", "KIND", "STATE", "OBSERVED_AT", "LAST_OK", "DETAIL_CODE", "VANTAGE"}
	if got := headerFields(t, sbuf.String()); !equalFields(got, want) {
		t.Errorf("sources header = %v, want %v", got, want)
	}

	// targets: a null last scrape renders as a dash, never as a value.
	var td promclient.TargetsDoc
	mustDecode(t, read("targets", "mixed"), &td)
	var tbuf bytes.Buffer
	if err := render.Targets(&tbuf, &td); err != nil {
		t.Fatalf("render targets: %v", err)
	}
	got := lineFields(t, tbuf.String(), "unmapped")
	if !equalFields(got, []string{"unmapped", "redacted", "unknown", "-"}) {
		t.Errorf("unmapped row = %v, want unmapped redacted unknown -", got)
	}
}

func equalFields(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestJSONIsOneLine pins the --json shape: the document alone, one line, no
// table decoration, present nulls kept.
func TestJSONIsOneLine(t *testing.T) {
	var buf bytes.Buffer
	d := grafana.HealthDoc{
		APIVersion: "vigil.health/v0",
		Backend:    "grafana",
		State:      "ok",
		DetailCode: nil,
		CheckedAt:  "2026-11-14T09:00:00Z",
	}
	if err := render.JSON(&buf, &d); err != nil {
		t.Fatalf("render json: %v", err)
	}
	s := buf.String()
	if strings.Count(s, "\n") != 1 || !strings.HasSuffix(s, "\n") {
		t.Errorf("json output is not exactly one line: %q", s)
	}
	want := `{"apiVersion":"vigil.health/v0","backend":"grafana","state":"ok","detail_code":null,"checked_at":"2026-11-14T09:00:00Z"}` + "\n"
	if s != want {
		t.Errorf("json line = %q, want %q", s, want)
	}
}

// TestNullabilityRoundTrip pins the nullability contract the producers and
// the schema rely on, in schema field order: a nil field with no omitempty is
// a present null; a nil or empty field with omitempty is an absent key.
func TestNullabilityRoundTrip(t *testing.T) {
	roundTrip := func(v any) string {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return string(b)
	}
	row := sources.Source{
		ID: "prom", Kind: "prom", State: "ok",
		ObservedAt: new("2026-11-14T09:00:00Z"),
		LastOK:     nil,
	}
	wantAbsent := `{"id":"prom","kind":"prom","state":"ok","observed_at":"2026-11-14T09:00:00Z","last_ok":null}`
	if got := roundTrip(row); got != wantAbsent {
		t.Errorf("row with absent detail code and vantage = %s, want %s", got, wantAbsent)
	}
	row.DetailCode = new("not_health")
	row.Vantage = "lathe"
	wantPresent := `{"id":"prom","kind":"prom","state":"ok","observed_at":"2026-11-14T09:00:00Z","last_ok":null,"detail_code":"not_health","vantage":"lathe"}`
	if got := roundTrip(row); got != wantPresent {
		t.Errorf("row with present detail code and vantage = %s, want %s", got, wantPresent)
	}
	row.ObservedAt = nil
	wantNull := `{"id":"prom","kind":"prom","state":"ok","observed_at":null,"last_ok":null,"detail_code":"not_health","vantage":"lathe"}`
	if got := roundTrip(row); got != wantNull {
		t.Errorf("row with null observed at = %s, want %s", got, wantNull)
	}
}
