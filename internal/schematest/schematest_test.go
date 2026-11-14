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

// compileSchema compiles one schema file, registering it under its own $id.
func compileSchema(t *testing.T, path string) *jsonschema.Schema {
	t.Helper()
	doc := loadJSON(t, path)
	id, ok := at(doc, "$id").(string)
	if !ok || id == "" {
		t.Fatalf("%s: missing $id", filepath.Base(path))
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatalf("%s: add resource: %v", filepath.Base(path), err)
	}
	sch, err := c.Compile(id)
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

// TestNoTestOnlyPackagesInBinary keeps the schema validator, the YAML decoder
// and the text package they pull in out of the shipped binary: all three are
// test dependencies, and the depguard rule says the same.
func TestNoTestOnlyPackagesInBinary(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command("go", "list", "-deps", "./cmd/vigil")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	testOnly := []string{
		"github.com/santhosh-tekuri/jsonschema",
		"go.yaml.in/yaml",
		"gopkg.in/yaml",
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
