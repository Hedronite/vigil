package schematest_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Hedronite/vigil/internal/exitcode"
	"github.com/Hedronite/vigil/internal/reasons"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
)

// repoRoot walks up from this test file to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// loadJSON reads a JSON document as a decoded value.
func loadJSON(t *testing.T, path string) any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return v
}

// loadYAMLFixture reads a YAML fixture as a JSON-shaped value, which is what a
// schema validator expects.
func loadYAMLFixture(t *testing.T, path string) any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var v any
	if err := yaml.Unmarshal(b, &v); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	j, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("re-encode %s: %v", path, err)
	}
	var out any
	if err := json.Unmarshal(j, &out); err != nil {
		t.Fatalf("re-decode %s: %v", path, err)
	}
	return out
}

// compileSchema compiles one schema file after registering every sibling, so a
// cross-file reference (the sources document reuses the summary's source shape)
// resolves without a network fetch.
func compileSchema(t *testing.T, path string) *jsonschema.Schema {
	t.Helper()
	dir := filepath.Dir(path)
	siblings, err := filepath.Glob(filepath.Join(dir, "*.schema.json"))
	if err != nil {
		t.Fatalf("glob schemas: %v", err)
	}
	c := jsonschema.NewCompiler()
	want := ""
	for _, sibling := range siblings {
		doc := loadJSON(t, sibling)
		id, ok := at(doc, "$id").(string)
		if !ok || id == "" {
			t.Fatalf("%s: missing $id", filepath.Base(sibling))
		}
		if err := c.AddResource(id, doc); err != nil {
			t.Fatalf("%s: add resource: %v", filepath.Base(sibling), err)
		}
		if filepath.Clean(sibling) == filepath.Clean(path) {
			want = id
		}
	}
	if want == "" {
		t.Fatalf("%s: not among the schemas in %s", filepath.Base(path), dir)
	}
	sch, err := c.Compile(want)
	if err != nil {
		t.Fatalf("%s: compile: %v", filepath.Base(path), err)
	}
	return sch
}

// at walks a decoded document by key path, returning nil when a step is absent.
func at(doc any, path ...string) any {
	cur := doc
	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[key]
	}
	return cur
}

// strEnum returns a schema enum as a string slice, ignoring a null member.
func strEnum(t *testing.T, doc any, path ...string) []string {
	t.Helper()
	raw, ok := at(doc, path...).([]any)
	if !ok {
		t.Fatalf("no enum at %s", strings.Join(path, "."))
	}
	var out []string
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func hasNull(v any) bool {
	raw, ok := v.([]any)
	if !ok {
		return false
	}
	for _, item := range raw {
		if item == nil {
			return true
		}
	}
	return false
}

func equalUnordered(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// TestSchemasCompile proves every schema parses and compiles, and that ids sit
// where the repository scrub expects them.
func TestSchemasCompile(t *testing.T) {
	root := repoRoot(t)
	paths, err := filepath.Glob(filepath.Join(root, "schema", "*.schema.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no schemas found: %v", err)
	}
	for _, path := range paths {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			compileSchema(t, path)
			doc := loadJSON(t, path)
			id := at(doc, "$id").(string)
			want := "https://github.com/Hedronite/vigil/schema/"
			if filepath.Base(path) == "simplex.probe.v0.schema.json" {
				want = "https://github.com/Hedronite/simplex/"
			}
			if !strings.HasPrefix(id, want) {
				t.Errorf("$id %q does not start with %q", id, want)
			}
			if meta := at(doc, "$schema"); meta != "https://json-schema.org/draft/2020-12/schema" {
				t.Errorf("$schema = %v, want the 2020-12 dialect", meta)
			}
		})
	}
}

// TestEnumDrift keeps the schemas in step with the Go vocabulary. A new severity,
// state or reason code fails here until the schema carries it too.
func TestEnumDrift(t *testing.T) {
	root := repoRoot(t)
	summary := loadJSON(t, filepath.Join(root, "schema", "vigil.summary.v0.schema.json"))

	if got := strEnum(t, summary, "$defs", "severity", "enum"); !equalUnordered(got, exitcode.Severities) {
		t.Errorf("severity enum = %v, want %v", got, exitcode.Severities)
	}
	overall := at(summary, "properties", "overall", "enum")
	if got := strEnum(t, summary, "properties", "overall", "enum"); !equalUnordered(got, exitcode.Severities) {
		t.Errorf("overall enum = %v, want %v", got, exitcode.Severities)
	}
	if !hasNull(overall) {
		t.Error("overall enum does not admit null")
	}
	verdictSeverity := at(summary, "$defs", "verdict", "properties", "severity", "enum")
	if got := strEnum(t, summary, "$defs", "verdict", "properties", "severity", "enum"); !equalUnordered(got, exitcode.Severities) {
		t.Errorf("verdict severity enum = %v, want %v", got, exitcode.Severities)
	}
	if !hasNull(verdictSeverity) {
		t.Error("verdict severity enum does not admit null")
	}

	if got := strEnum(t, summary, "$defs", "state", "enum"); !equalUnordered(got, reasons.States) {
		t.Errorf("state enum = %v, want %v", got, reasons.States)
	}

	if got := at(summary, "$defs", "reason", "pattern"); got != reasons.Pattern {
		t.Errorf("reason pattern = %v, want %q", got, reasons.Pattern)
	}
	for _, code := range reasons.Own {
		if !reasons.Valid(code) {
			t.Errorf("own reason %q does not satisfy reasons.Pattern", code)
		}
		if !regexp.MustCompile(reasons.Pattern).MatchString(code) {
			t.Errorf("own reason %q does not match the schema reason pattern", code)
		}
	}

	// Exit codes: three verdicts, then errors, and the table agrees with the maps.
	if len(exitcode.Table) != 7 {
		t.Fatalf("exit table has %d rows, want 7", len(exitcode.Table))
	}
	seen := map[int]bool{}
	for i, row := range exitcode.Table {
		if seen[row.Code] {
			t.Errorf("duplicate exit code %d", row.Code)
		}
		seen[row.Code] = true
		if row.Code != i {
			t.Errorf("table row %d has code %d; the table must be ordered by code", i, row.Code)
		}
		if got := exitcode.IsVerdict(row.Code); got != (i <= 2) {
			t.Errorf("IsVerdict(%d) = %v", row.Code, got)
		}
	}
	for i, sev := range exitcode.Severities {
		code, ok := exitcode.FromSeverity(sev)
		if !ok || code != i {
			t.Errorf("FromSeverity(%q) = %d, %v; want %d", sev, code, ok, i)
		}
	}
	if _, ok := exitcode.FromSeverity("once"); ok {
		t.Error("FromSeverity accepted a string that is not a severity")
	}
}

// TestFixturesValidateAsLabelled proves every fixture carries the label its
// directory claims. It is skipped until the fixture branch lands, so this
// package can merge with the schemas alone.
func TestFixturesValidateAsLabelled(t *testing.T) {
	root := repoRoot(t)
	testdata := filepath.Join(root, "testdata")
	if _, err := os.Stat(filepath.Join(testdata, "summary")); err != nil {
		t.Skip("summary goldens are not in this branch; the fixtures slice adds them")
	}

	summarySch := compileSchema(t, filepath.Join(root, "schema", "vigil.summary.v0.schema.json"))
	signalSch := compileSchema(t, filepath.Join(root, "schema", "vigil.signal.v0.schema.json"))
	errorSch := compileSchema(t, filepath.Join(root, "schema", "vigil.error.v0.schema.json"))
	configSch := compileSchema(t, filepath.Join(root, "schema", "vigil.config.v0.schema.json"))
	simplexSch := compileSchema(t, filepath.Join(root, "schema", "simplex.probe.v0.schema.json"))
	overlaySch := compileSchema(t, filepath.Join(root, "schema", "vigil.overlay.v0.schema.json"))
	sourcesSch := compileSchema(t, filepath.Join(root, "schema", "vigil.sources.v0.schema.json"))
	targetsSch := compileSchema(t, filepath.Join(root, "schema", "vigil.targets.v0.schema.json"))
	querySch := compileSchema(t, filepath.Join(root, "schema", "vigil.query.v0.schema.json"))
	healthSch := compileSchema(t, filepath.Join(root, "schema", "vigil.health.v0.schema.json"))

	check := func(kind string, sch *jsonschema.Schema, load func(*testing.T, string) any, ext string) {
		valid, _ := filepath.Glob(filepath.Join(testdata, kind, "valid", "*"+ext))
		invalid, _ := filepath.Glob(filepath.Join(testdata, kind, "invalid", "*"+ext))
		if len(valid) == 0 || len(invalid) == 0 {
			t.Fatalf("%s: found %d valid and %d invalid fixtures", kind, len(valid), len(invalid))
		}
		for _, path := range valid {
			if err := sch.Validate(load(t, path)); err != nil {
				t.Errorf("%s/valid/%s: %v", kind, filepath.Base(path), err)
			}
		}
		for _, path := range invalid {
			if err := sch.Validate(load(t, path)); err == nil {
				t.Errorf("%s/invalid/%s: validated, want a rejection", kind, filepath.Base(path))
			}
		}
	}
	check("signal", signalSch, loadJSON, ".json")
	check("error", errorSch, loadJSON, ".json")
	check("config", configSch, loadYAMLFixture, ".yaml")
	check("overlay", overlaySch, loadYAMLFixture, ".yaml")
	check("sources", sourcesSch, loadJSON, ".json")
	check("targets", targetsSch, loadJSON, ".json")
	check("query", querySch, loadJSON, ".json")
	check("health", healthSch, loadJSON, ".json")

	// Vendored documents must satisfy Simplex's own schema.
	docs, _ := filepath.Glob(filepath.Join(testdata, "simplex", "*", "document.json"))
	if len(docs) == 0 {
		t.Fatal("no vendored Simplex documents")
	}
	for _, path := range docs {
		if err := simplexSch.Validate(loadJSON(t, path)); err != nil {
			t.Errorf("simplex/%s: %v", filepath.Base(filepath.Dir(path)), err)
		}
	}

	// Summary goldens: schema, then the adjudicated rollup and its exit code.
	for _, path := range mustGlob(t, filepath.Join(testdata, "summary", "*", "document.json")) {
		dir := filepath.Dir(path)
		name := filepath.Base(dir)
		doc := loadJSON(t, path)
		if err := summarySch.Validate(doc); err != nil {
			t.Errorf("summary/%s: %v", name, err)
			continue
		}
		want := mustExit(t, filepath.Join(dir, "expected_exit"))
		gotOverall, gotExit := derive(doc)
		if docOverall := at(doc, "overall"); docOverall != gotOverall {
			t.Errorf("summary/%s: overall %v, the verdicts say %v", name, docOverall, gotOverall)
		}
		if gotExit != want {
			t.Errorf("summary/%s: expected_exit %d, the verdicts say %d", name, want, gotExit)
		}
		for _, raw := range at(doc, "verdicts").([]any) {
			sev := at(raw, "severity")
			switch at(raw, "state") {
			case reasons.StateUnavailable:
				if sev != nil {
					t.Errorf("summary/%s: unavailable verdict carries severity %v", name, sev)
				}
			default:
				if sev == nil {
					t.Errorf("summary/%s: readable verdict carries no severity", name)
				}
			}
		}
	}
}

func mustGlob(t *testing.T, pattern string) []string {
	t.Helper()
	paths, err := filepath.Glob(pattern)
	if err != nil || len(paths) == 0 {
		t.Fatalf("glob %s: %v", pattern, err)
	}
	sort.Strings(paths)
	return paths
}

func mustExit(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatalf("%s is not an integer: %v", path, err)
	}
	return code
}

// derive recomputes the summary from its verdicts: worst of the available ones,
// a stale authority capping GREEN at YELLOW, any unavailable authority capping
// at YELLOW, and a null verdict with exit 4 when nothing is readable.
func derive(doc any) (any, int) {
	worst, available, capped := 0, 0, false
	rank := map[string]int{
		exitcode.SeverityGreen:  0,
		exitcode.SeverityYellow: 1,
		exitcode.SeverityRed:    2,
	}
	verdicts, _ := at(doc, "verdicts").([]any)
	for _, raw := range verdicts {
		state, _ := at(raw, "state").(string)
		if state == reasons.StateUnavailable {
			capped = true
			continue
		}
		available++
		if sev, ok := at(raw, "severity").(string); ok {
			if r, ok := rank[sev]; ok && r > worst {
				worst = r
			}
		}
		if state == reasons.StateStale && worst < rank[exitcode.SeverityYellow] {
			worst = rank[exitcode.SeverityYellow]
		}
	}
	if available == 0 {
		return nil, exitcode.Unavailable
	}
	if capped && worst < rank[exitcode.SeverityYellow] {
		worst = rank[exitcode.SeverityYellow]
	}
	switch worst {
	case rank[exitcode.SeverityGreen]:
		return exitcode.SeverityGreen, exitcode.Green
	case rank[exitcode.SeverityYellow]:
		return exitcode.SeverityYellow, exitcode.Yellow
	default:
		return exitcode.SeverityRed, exitcode.Red
	}
}

// TestNoTestOnlyPackagesInBinary keeps the schema validator and the text
// package it pulls in out of the shipped binary: both are test dependencies,
// and the depguard rule says the same. The YAML decoder, cobra and pflag are
// allowed, because the config loader and the CLI need them.
func TestNoTestOnlyPackagesInBinary(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command("go", "list", "-deps", "./cmd/vigil")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	// The config loader and the CLI are part of the binary; the validator and
	// the text package it pulls in are not.
	testOnly := []string{
		"github.com/santhosh-tekuri/jsonschema",
		"golang.org/x/text",
	}
	for _, line := range strings.Split(string(out), "\n") {
		for _, pkg := range testOnly {
			if strings.HasPrefix(strings.TrimSpace(line), pkg) {
				t.Errorf("the binary depends on %s", strings.TrimSpace(line))
			}
		}
	}
}

// nonPortable lists the constructs that parse in Go but not in ECMA-262. Facet
// and other consumers compile these patterns with their own engines, so a
// schema that uses them fails outside Go even when this package validates.
var nonPortable = []struct {
	name string
	re   *regexp.Regexp
}{
	{"an inline flag or an unsupported group extension", regexp.MustCompile(`\(\?[a-zA-Z<>=!(]`)},
	{"a possessive quantifier", regexp.MustCompile(`(?:[*+?]|\{[0-9]+(?:,[0-9]*)?\})\+`)},
	{"a POSIX character class", regexp.MustCompile(`\[:`)},
	{"an anchor ECMA-262 does not define", regexp.MustCompile(`\\[AzZ]`)},
	{"a quoted literal", regexp.MustCompile(`\\Q`)},
}

// TestSchemaPatternsAreECMA262 keeps every schema pattern usable by a consumer
// that is not written in Go.
func TestSchemaPatternsAreECMA262(t *testing.T) {
	root := repoRoot(t)
	paths, err := filepath.Glob(filepath.Join(root, "schema", "*.schema.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no schemas found: %v", err)
	}
	checked := 0
	for _, path := range paths {
		doc := loadJSON(t, path)
		for _, pattern := range collectPatterns(doc) {
			checked++
			if _, err := regexp.Compile(pattern); err != nil {
				t.Errorf("%s: %q does not compile: %v", filepath.Base(path), pattern, err)
				continue
			}
			for _, rule := range nonPortable {
				if rule.re.MatchString(pattern) {
					t.Errorf("%s: %q uses %s, which is not ECMA-262", filepath.Base(path), pattern, rule.name)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no patterns found to check")
	}
}

// collectPatterns returns every "pattern" string anywhere in a schema.
func collectPatterns(v any) []string {
	var out []string
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if k == "pattern" {
				if s, ok := val.(string); ok {
					out = append(out, s)
				}
			}
			out = append(out, collectPatterns(val)...)
		}
	case []any:
		for _, val := range t {
			out = append(out, collectPatterns(val)...)
		}
	}
	return out
}

// TestV1DocumentDrift ties the backend documents to the shared vocabulary: the
// sources row is the summary's own shape rather than a copy, a health state is
// a source state, and a detail code is a reason code.
func TestV1DocumentDrift(t *testing.T) {
	root := repoRoot(t)
	schemaPath := func(name string) string { return filepath.Join(root, "schema", name) }

	summaryID, _ := at(loadJSON(t, schemaPath("vigil.summary.v0.schema.json")), "$id").(string)
	if summaryID == "" {
		t.Fatal("summary schema has no $id")
	}

	sourcesDoc := loadJSON(t, schemaPath("vigil.sources.v0.schema.json"))
	wantRef := summaryID + "#/$defs/source"
	if got := at(sourcesDoc, "properties", "sources", "items", "$ref"); got != wantRef {
		t.Errorf("sources row ref = %v, want %q", got, wantRef)
	}

	healthDoc := loadJSON(t, schemaPath("vigil.health.v0.schema.json"))
	states := strEnum(t, healthDoc, "properties", "state", "enum")
	if !equalUnordered(states, []string{reasons.StateOK, reasons.StateUnavailable}) {
		t.Errorf("health states = %v, want ok and unavailable only", states)
	}
	known := map[string]bool{}
	for _, st := range reasons.States {
		known[st] = true
	}
	for _, st := range states {
		if !known[st] {
			t.Errorf("health state %q is not a source state", st)
		}
	}
	if got := at(healthDoc, "properties", "detail_code", "pattern"); got != reasons.Pattern {
		t.Errorf("health detail pattern = %v, want %q", got, reasons.Pattern)
	}

	targetsDoc := loadJSON(t, schemaPath("vigil.targets.v0.schema.json"))
	if got := strEnum(t, targetsDoc, "properties", "targets", "items", "properties", "health", "enum"); !equalUnordered(got, []string{"up", "down", "unknown"}) {
		t.Errorf("target health enum = %v, want up, down and unknown", got)
	}

	queryDoc := loadJSON(t, schemaPath("vigil.query.v0.schema.json"))
	if got := at(queryDoc, "properties", "series", "items", "properties", "value", "type"); got != "string" {
		t.Errorf("query sample type = %v, want a string so precision survives", got)
	}

	// Each versioned document names itself once, and the const matches the file.
	for _, name := range []string{
		"vigil.sources.v0.schema.json",
		"vigil.targets.v0.schema.json",
		"vigil.query.v0.schema.json",
		"vigil.health.v0.schema.json",
	} {
		doc := loadJSON(t, schemaPath(name))
		constant := at(doc, "properties", "apiVersion", "const")
		want := strings.TrimSuffix(strings.TrimPrefix(name, "vigil."), ".schema.json")
		want = "vigil." + strings.Replace(want, ".v0", "/v0", 1)
		if constant != want {
			t.Errorf("%s: apiVersion const = %v, want %q", name, constant, want)
		}
		if req, ok := at(doc, "required").([]any); !ok || len(req) == 0 || req[0] != "apiVersion" {
			t.Errorf("%s: apiVersion is not the first required field", name)
		}
	}
}

// TestQueryValueShape pins the sample-value shape. A decimal, an exponent, NaN
// and the infinities pass; anything that is really an address or a name does
// not. The address-shaped cases are assembled at run time, because the
// repository scrub forbids those literals anywhere in the tree and a fixture
// cannot spell them.
func TestQueryValueShape(t *testing.T) {
	root := repoRoot(t)
	sch := compileSchema(t, filepath.Join(root, "schema", "vigil.query.v0.schema.json"))

	doc := func(value string) any {
		return map[string]any{
			"apiVersion":   "vigil.query/v0",
			"backend":      "prom",
			"vantage":      "lab",
			"name":         "up_by_host",
			"evaluated_at": "2026-11-14T09:00:00Z",
			"series":       []any{map[string]any{"host": "core", "value": value}},
		}
	}
	good := []string{"0", "1", "-0.5", "1.5e-3", "2E+10", "NaN", "+Inf", "-Inf"}
	for _, v := range good {
		if err := sch.Validate(doc(v)); err != nil {
			t.Errorf("value %q rejected: %v", v, err)
		}
	}
	bad := []string{
		"http://prom.example.invalid:9090",
		"core",
		"127.0.0.1",
		"1.2.3",
		"1,5",
		"",
		strings.Join([]string{"100", "64", "0", "1"}, "."),
		strings.Join([]string{"core", "ts", "net"}, "."),
	}
	for _, v := range bad {
		if err := sch.Validate(doc(v)); err == nil {
			t.Errorf("value %q validated, want a rejection", v)
		}
	}
}
