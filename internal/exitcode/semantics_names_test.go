package exitcode

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var (
	backticked = regexp.MustCompile("`([^`\n]+)`")
	testName   = regexp.MustCompile(`^(Test|Fuzz)[A-Za-z0-9_]+$`)
	testDecl   = regexp.MustCompile(`(?m)^func ((?:Test|Fuzz)[A-Za-z0-9_]+)\(`)
	pathRoots  = []string{"testdata/", "internal/", "cmd/", "scripts/", "schema/", "docs/"}
	fileExts   = map[string]bool{".json": true, ".yaml": true, ".yml": true, ".txt": true, ".md": true, ".sh": true, ".go": true}
)

// declaredTests returns every Test and Fuzz function declared in a _test.go
// file under root.
func declaredTests(t *testing.T, root string) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range testDecl.FindAllStringSubmatch(string(b), -1) {
			found[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return found
}

// missingNames returns every test name and tracked path that doc mentions in
// backticks and the tree does not have. A token is checked only when it has
// the shape of one: Test or Fuzz plus an identifier, or a path under a
// directory the repository owns. A symbol after a package path
// (internal/exitcode.Table), a placeholder (<case>) and a route are left alone.
func missingNames(doc, root string, tests map[string]bool) []string {
	seen := map[string]bool{}
	var missing []string
	for _, m := range backticked.FindAllStringSubmatch(doc, -1) {
		tok := m[1]
		if seen[tok] {
			continue
		}
		seen[tok] = true
		if testName.MatchString(tok) {
			if !tests[tok] {
				missing = append(missing, "no test or fuzz function "+tok)
			}
			continue
		}
		if strings.ContainsAny(tok, " <>{}()=,;|") {
			continue
		}
		rooted := false
		for _, r := range pathRoots {
			if strings.HasPrefix(tok, r) {
				rooted = true
			}
		}
		if !rooted {
			continue
		}
		p := strings.TrimRight(tok, "/")
		if ext := filepath.Ext(filepath.Base(p)); ext != "" && !fileExts[ext] {
			continue // a symbol, such as internal/exitcode.Table
		}
		matches, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(p)))
		if len(matches) == 0 {
			missing = append(missing, "no such path "+tok)
		}
	}
	sort.Strings(missing)
	return missing
}

// docs/semantics.md names real tests and real fixtures. A test that is renamed
// or a fixture that is moved makes the page wrong, and this fails.
func TestSemanticsNamesRealTestsAndFixtures(t *testing.T) {
	root := filepath.Join("..", "..")
	raw, err := os.ReadFile(filepath.Join(root, "docs", "semantics.md"))
	if err != nil {
		t.Fatalf("read semantics doc: %v", err)
	}
	tests := declaredTests(t, root)
	if len(tests) < 50 {
		t.Fatalf("found only %d test functions; the walk is wrong", len(tests))
	}
	if missing := missingNames(string(raw), root, tests); len(missing) != 0 {
		t.Errorf("docs/semantics.md names things the tree does not have:\n  %s", strings.Join(missing, "\n  "))
	}
}

// The check itself must bite: a renamed test and a moved fixture are found, a
// symbol and a placeholder are not.
func TestSemanticsNameCheckBites(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "testdata", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "testdata", "x", "ok.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := map[string]bool{"TestReal": true, "FuzzReal": true}
	doc := "`TestReal` `FuzzReal` `TestRenamed` `FuzzGone` `testdata/x/ok.json` `testdata/x/` `testdata/x/moved.json` " +
		"`testdata/nope/` `internal/exitcode.Table` `testdata/summary/<case>/` `/api/v1/targets` `up{job=\"x\"}` `Test` `schema/*.json`"
	got := missingNames(doc, root, tests)
	want := []string{
		"no such path schema/*.json",
		"no such path testdata/nope/",
		"no such path testdata/x/moved.json",
		"no test or fuzz function FuzzGone",
		"no test or fuzz function TestRenamed",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("missing = %q, want %q", got, want)
	}
}
