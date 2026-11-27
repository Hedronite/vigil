package schematest_test

import (
	"encoding/json"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Hedronite/vigil/internal/config"
	"github.com/Hedronite/vigil/internal/reasons"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	digestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// absent removes a key instead of setting it.
type absent struct{}

// edit sets the value at path, or removes it. A step that is a number indexes
// an array.
type edit struct {
	path  string
	value any
}

// clone copies the containers in a value. apply inserts a copy, so a later edit
// inside an inserted object cannot reach another case built from the same one.
func clone(v any) any {
	switch node := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(node))
		for k, item := range node {
			out[k] = clone(item)
		}
		return out
	case []any:
		out := make([]any, len(node))
		for i, item := range node {
			out[i] = clone(item)
		}
		return out
	}
	return v
}

func apply(t *testing.T, doc any, e edit) {
	t.Helper()
	steps := strings.Split(e.path, "/")
	cur := doc
	for _, step := range steps[:len(steps)-1] {
		switch node := cur.(type) {
		case map[string]any:
			cur = node[step]
		case []any:
			i, err := strconv.Atoi(step)
			if err != nil || i >= len(node) {
				t.Fatalf("%s: no element %q", e.path, step)
			}
			cur = node[i]
		default:
			t.Fatalf("%s: nothing at %q", e.path, step)
		}
	}
	m, ok := cur.(map[string]any)
	if !ok {
		t.Fatalf("%s: the parent is not an object", e.path)
	}
	if _, drop := e.value.(absent); drop {
		delete(m, steps[len(steps)-1])
		return
	}
	m[steps[len(steps)-1]] = clone(e.value)
}

// mutant is one change set to a valid base document. at is empty when the
// result must validate; otherwise it is the instance location the rejection
// has to name, so a case cannot pass for an unrelated reason.
type mutant struct {
	name  string
	edits []edit
	at    string
}

func runMutants(t *testing.T, schema, base string, cases []mutant) {
	t.Helper()
	root := repoRoot(t)
	sch := compileSchema(t, filepath.Join(root, "schema", schema))
	path := filepath.Join(root, "testdata", filepath.FromSlash(base))
	if err := sch.Validate(loadJSON(t, path)); err != nil {
		t.Fatalf("%s: the base document does not validate: %v", base, err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := loadJSON(t, path)
			for _, e := range tc.edits {
				apply(t, doc, e)
			}
			checkVerdict(t, sch, doc, tc.at)
		})
	}
}

func checkVerdict(t *testing.T, sch *jsonschema.Schema, doc any, at string) {
	t.Helper()
	err := sch.Validate(doc)
	switch {
	case at == "" && err != nil:
		t.Errorf("rejected, want it to validate: %v", err)
	case at != "" && err == nil:
		t.Errorf("validated, want a rejection at %s", at)
	case at != "" && !strings.Contains(err.Error(), "at '"+at+"'"):
		t.Errorf("rejected for another reason, want %s: %v", at, err)
	}
}

const (
	summarySchema = "vigil.summary.v0.schema.json"
	greenGolden   = "summary/simplex-green/document.json"
)

// TestVerdictStateSeverityCoupling: an unavailable verdict has no severity and
// a readable one, ok or stale, always has one.
func TestVerdictStateSeverityCoupling(t *testing.T) {
	var cases []mutant
	for _, state := range reasons.States {
		for _, severity := range []any{"GREEN", "YELLOW", "RED", nil} {
			at := ""
			if (state == reasons.StateUnavailable) != (severity == nil) {
				at = "/verdicts/0/severity"
			}
			name := state + " with null"
			if s, ok := severity.(string); ok {
				name = state + " with " + s
			}
			cases = append(cases, mutant{name: name, at: at, edits: []edit{
				{"verdicts/0/state", state},
				{"verdicts/0/severity", severity},
			}})
		}
	}
	runMutants(t, summarySchema, greenGolden, cases)
}

// detailCases is the state and detail_code table shared by every document that
// carries the pair. ok has no code, unavailable names one.
func detailCases(prefix, at string) []mutant {
	row := func(name, state string, code any, bad bool) mutant {
		m := mutant{name: name, edits: []edit{{prefix + "state", state}, {prefix + "detail_code", code}}}
		if bad {
			m.at = at
			if _, missing := code.(absent); !missing {
				m.at = at + "/detail_code"
			}
		}
		return m
	}
	return []mutant{
		row("ok without a code", reasons.StateOK, absent{}, false),
		row("ok with null", reasons.StateOK, nil, false),
		row("ok with a code", reasons.StateOK, reasons.Timeout, true),
		row("unavailable with a code", reasons.StateUnavailable, reasons.Timeout, false),
		row("unavailable without a code", reasons.StateUnavailable, absent{}, true),
		row("unavailable with null", reasons.StateUnavailable, nil, true),
	}
}

// TestSourceStateDetailCoupling covers the summary's source shape, directly and
// through the sources listing that references it. Stale is left open: it may
// name source_stale or nothing.
func TestSourceStateDetailCoupling(t *testing.T) {
	cases := detailCases("sources/0/", "/sources/0")
	for name, code := range map[string]any{"stale with a code": reasons.SourceStale, "stale with null": nil, "stale without a code": absent{}} {
		cases = append(cases, mutant{name: name, edits: []edit{
			{"sources/0/state", reasons.StateStale},
			{"sources/0/detail_code", code},
		}})
	}
	runMutants(t, summarySchema, greenGolden, cases)
	runMutants(t, "vigil.sources.v0.schema.json", "sources/valid/backends.json", detailCases("sources/0/", "/sources/0"))
}

// TestHealthStateDetailCoupling does the same for the live health document.
func TestHealthStateDetailCoupling(t *testing.T) {
	cases := detailCases("", "")
	// The document root has an empty location, which runMutants reads as
	// "must validate", so the two root-level refusals are spelled out.
	for i := range cases {
		if strings.HasPrefix(cases[i].name, "unavailable without") {
			cases = append(cases[:i], cases[i+1:]...)
			break
		}
	}
	runMutants(t, "vigil.health.v0.schema.json", "health/valid/ok.json", cases)

	root := repoRoot(t)
	sch := compileSchema(t, filepath.Join(root, "schema", "vigil.health.v0.schema.json"))
	doc := loadJSON(t, filepath.Join(root, "testdata", "health", "valid", "unavailable.json"))
	apply(t, doc, edit{"detail_code", absent{}})
	if err := sch.Validate(doc); err == nil || !strings.Contains(err.Error(), "missing property 'detail_code'") {
		t.Errorf("unavailable without a code: %v, want detail_code reported missing", err)
	}
}

// TestDocumentSlugsStartWithALetter: every document refuses the leading digit
// that config, overlay and the CLI already refuse.
func TestDocumentSlugsStartWithALetter(t *testing.T) {
	check := func(schema, base string, extra []edit, fields map[string]string) {
		var cases []mutant
		for field, at := range fields {
			for value, bad := range map[string]bool{"9lab": true, "lab9": false} {
				m := mutant{name: schema + " " + field + " " + value}
				m.edits = append(append(m.edits, extra...), edit{field, value})
				if bad {
					m.at = at
				}
				cases = append(cases, m)
			}
		}
		runMutants(t, schema, base, cases)
	}
	check(summarySchema, greenGolden, nil, map[string]string{
		"verdicts/0/source": "/verdicts/0/source",
		"vantage/id":        "/vantage/id",
		"hosts/0/id":        "/hosts/0/id",
		"sources/0/id":      "/sources/0/id",
		"sources/0/vantage": "/sources/0/vantage",
	})
	runMutants(t, summarySchema, greenGolden, []mutant{
		{name: "cluster_alias digit-first", edits: []edit{
			{"scry", map[string]any{"cluster_alias": "9lab"}},
		}},
		{name: "cluster_alias letter-first", edits: []edit{
			{"scry", map[string]any{"cluster_alias": "lab9"}},
		}},
		{name: "cluster_alias leading hyphen", at: "/scry/cluster_alias", edits: []edit{
			{"scry", map[string]any{"cluster_alias": "-lab"}},
		}},
	})
	check("vigil.query.v0.schema.json", "query/valid/one-host.json", nil, map[string]string{
		"vantage":       "/vantage",
		"host":          "/host",
		"series/0/host": "/series/0/host",
	})
	check("vigil.targets.v0.schema.json", "targets/valid/mixed.json", nil, map[string]string{
		"vantage":        "/vantage",
		"targets/0/host": "/targets/0/host",
	})
	check("vigil.sources.v0.schema.json", "sources/valid/backends.json", nil, map[string]string{
		"sources/0/id": "/sources/0/id",
	})
}

// TestQueryValueLength: the bound admits every float64 in every notation
// Prometheus uses and refuses an unbounded run of digits.
func TestQueryValueLength(t *testing.T) {
	const bound = 512
	at := "/series/0/value"
	value := func(name, v, at string) mutant {
		return mutant{name: name, at: at, edits: []edit{{"series/0/value", v}}}
	}
	cases := []mutant{
		value("at the bound", strings.Repeat("9", bound), ""),
		value("one over", strings.Repeat("9", bound+1), at),
		value("a hundred thousand digits", strings.Repeat("9", 100_000), at),
	}
	for name, f := range map[string]float64{
		"largest":           -math.MaxFloat64,
		"smallest":          -math.SmallestNonzeroFloat64,
		"smallest normal":   -math.Float64frombits(0x0010000000000000),
		"largest subnormal": -math.Float64frombits(0x000fffffffffffff),
	} {
		for _, format := range []byte{'f', 'e', 'g'} {
			cases = append(cases, value(name+" "+string(format), strconv.FormatFloat(f, format, -1, 64), ""))
		}
	}
	runMutants(t, "vigil.query.v0.schema.json", "query/valid/one-host.json", cases)
}

// TestSummaryFreshnessAndDriftFields: the new fields are optional, closed and
// shaped. The goldens carry none of them and stay valid.
func TestSummaryFreshnessAndDriftFields(t *testing.T) {
	drift := func() map[string]any {
		return map[string]any{
			"reference":  digestA,
			"in_grace":   false,
			"drifted":    true,
			"undeclared": []any{"scry"},
			"by_declaration": []any{
				map[string]any{"declaration": digestA, "subjects": []any{"simplex"}},
				map[string]any{"declaration": digestB, "subjects": []any{"augur"}},
			},
		}
	}
	ok := func(name string, edits ...edit) mutant { return mutant{name: name, edits: edits} }
	bad := func(name, at string, edits ...edit) mutant { return mutant{name: name, edits: edits, at: at} }
	set := func(path string, v any) edit { return edit{path, v} }
	with := set("declaration_drift", drift())

	cases := []mutant{
		ok("declaration", set("declaration", digestA)),
		ok("declaration null", set("declaration", nil)),
		bad("declaration short", "/declaration", set("declaration", "sha256:aa")),
		bad("declaration upper case", "/declaration", set("declaration", "sha256:"+strings.Repeat("A", 64))),

		ok("drift", set("declaration_drift", drift())),
		ok("drift with nothing to list", set("declaration_drift", map[string]any{
			"reference": nil, "in_grace": false, "drifted": false, "undeclared": []any{}, "by_declaration": []any{},
		})),
		bad("drifted without a reference", "/declaration_drift/drifted", with, set("declaration_drift/reference", nil)),
		bad("drift unknown key", "/declaration_drift", with, set("declaration_drift/severity", "YELLOW")),
		bad("reference not a digest", "/declaration_drift/reference", with, set("declaration_drift/reference", "main")),
		bad("in_grace not a boolean", "/declaration_drift/in_grace", with, set("declaration_drift/in_grace", "no")),
		bad("drifted not a boolean", "/declaration_drift/drifted", with, set("declaration_drift/drifted", 1)),
		bad("undeclared not a slug", "/declaration_drift/undeclared/0", with, set("declaration_drift/undeclared", []any{"9x"})),
		bad("undeclared repeated", "/declaration_drift/undeclared", with, set("declaration_drift/undeclared", []any{"scry", "scry"})),
		bad("group without subjects", "/declaration_drift/by_declaration/0/subjects", with, set("declaration_drift/by_declaration/0/subjects", []any{})),
		bad("group subject repeated", "/declaration_drift/by_declaration/0/subjects", with, set("declaration_drift/by_declaration/0/subjects", []any{"augur", "augur"})),
		bad("group subject not a slug", "/declaration_drift/by_declaration/0/subjects/0", with, set("declaration_drift/by_declaration/0/subjects", []any{"a.b"})),
		bad("group digest null", "/declaration_drift/by_declaration/0/declaration", with, set("declaration_drift/by_declaration/0/declaration", nil)),
		bad("group unknown key", "/declaration_drift/by_declaration/0", with, set("declaration_drift/by_declaration/0/count", 1)),
		bad("group without a digest", "/declaration_drift/by_declaration/0", with, set("declaration_drift/by_declaration/0/declaration", absent{})),
	}
	for _, key := range []string{"reference", "in_grace", "drifted", "undeclared", "by_declaration"} {
		cases = append(cases, bad("drift without "+key, "/declaration_drift", with, set("declaration_drift/"+key, absent{})))
	}
	for _, list := range []string{"verdicts/0", "sources/0"} {
		cases = append(cases,
			ok(list+" all three", set(list+"/received_at", "2026-11-13T18:00:00Z"), set(list+"/valid_for", 60), set(list+"/declaration", digestA)),
			ok(list+" all three null", set(list+"/received_at", nil), set(list+"/valid_for", nil), set(list+"/declaration", nil)),
			bad(list+" received_at offset", "/"+list+"/received_at", set(list+"/received_at", "2026-11-13T18:00:00+01:00")),
			bad(list+" valid_for zero", "/"+list+"/valid_for", set(list+"/valid_for", 0)),
			bad(list+" valid_for fraction", "/"+list+"/valid_for", set(list+"/valid_for", 1.5)),
			bad(list+" valid_for duration", "/"+list+"/valid_for", set(list+"/valid_for", "60s")),
			bad(list+" declaration bad", "/"+list+"/declaration", set(list+"/declaration", "sha256:zz")),
			bad(list+" still closed", "/"+list, set(list+"/observed", true)),
		)
	}
	for _, code := range []string{reasons.ClockSkew, reasons.DeclarationDrift} {
		cases = append(cases, ok("reason "+code, set("verdicts/0/reasons", []any{code})))
	}
	runMutants(t, summarySchema, greenGolden, cases)
}

// TestFreshnessDefaultsMatchTheLoader keeps the schema's documented defaults
// and the loader's constants the same.
func TestFreshnessDefaultsMatchTheLoader(t *testing.T) {
	doc := loadJSON(t, filepath.Join(repoRoot(t), "schema", "vigil.config.v0.schema.json"))
	field := func(name, key string) any { return at(doc, "properties", "freshness", "properties", name, key) }
	if got := field("stale_after", "default"); got != config.DefaultStaleAfter {
		t.Errorf("stale_after default = %v, want %q", got, config.DefaultStaleAfter)
	}
	if got := field("skew_budget", "default"); got != config.DefaultSkewBudget {
		t.Errorf("skew_budget default = %v, want %q", got, config.DefaultSkewBudget)
	}
	if got := field("declaration_grace_s", "default"); got != float64(config.DefaultDeclarationGraceS) {
		t.Errorf("declaration_grace_s default = %v, want %d", got, config.DefaultDeclarationGraceS)
	}
}

// TestEditsDoNotShareValues: one edit applied to two documents, then changed in
// one of them, leaves the other and the edit itself alone. Without this a case
// can write into the next one's document, which makes a control fail at random
// or a refusal pass for another case's reason.
func TestEditsDoNotShareValues(t *testing.T) {
	shared := edit{"block", map[string]any{"list": []any{map[string]any{"id": "lab"}}}}
	first, second := map[string]any{}, map[string]any{}
	apply(t, first, shared)
	apply(t, second, shared)
	apply(t, first, edit{"block/list/0/id", "9lab"})
	apply(t, first, edit{"block/extra", true})
	for name, doc := range map[string]any{"second document": second, "edit": map[string]any{"block": shared.value}} {
		got, err := json.Marshal(doc)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if want := `{"block":{"list":[{"id":"lab"}]}}`; string(got) != want {
			t.Errorf("%s = %s, want %s", name, got, want)
		}
	}
}
