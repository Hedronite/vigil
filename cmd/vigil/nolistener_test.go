package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// listenerCalls names, per import path, the identifiers that open a listener
// or start a server. V1 has no listener; V3 adds one on purpose and relaxes
// this test with it.
var listenerCalls = map[string][]string{
	"net": {
		"Listen", "ListenTCP", "ListenUDP", "ListenUnix", "ListenUnixgram",
		"ListenPacket", "ListenIP", "ListenMulticastUDP", "ListenConfig", "Listener",
	},
	"net/http": {
		"ListenAndServe", "ListenAndServeTLS", "Serve", "ServeTLS", "Server",
	},
	"net/http/httptest": {"NewServer", "NewTLSServer", "NewUnstartedServer", "Server"},
	"crypto/tls":        {"Listen", "NewListener"},
	"syscall":           {"Listen"},
}

// scanListeners returns "file:line pkg.Name" for every listener construct in
// the non-test Go files under root.
func scanListeners(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", "vendor", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Errorf("parse %s: %v", p, err)
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		local := map[string]string{} // local name -> import path
		for _, imp := range f.Imports {
			ip := strings.Trim(imp.Path.Value, `"`)
			if _, watched := listenerCalls[ip]; !watched {
				continue
			}
			name := path.Base(ip)
			if imp.Name != nil {
				name = imp.Name.Name
			}
			if name == "." {
				found = append(found, rel+" dot-import of "+ip)
				continue
			}
			local[name] = ip
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			ip, ok := local[id.Name]
			if !ok {
				return true
			}
			for _, banned := range listenerCalls[ip] {
				if sel.Sel.Name == banned {
					found = append(found, rel+":"+strconv.Itoa(fset.Position(sel.Pos()).Line)+" "+id.Name+"."+banned)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(found)
	return found
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// TestNoListenerBeforeV3 fails if any non-test code opens a listener or starts
// a server. Vigil V1 only ever dials out.
func TestNoListenerBeforeV3(t *testing.T) {
	if hits := scanListeners(t, moduleRoot(t)); len(hits) != 0 {
		t.Errorf("listener code before V3:\n  %s", strings.Join(hits, "\n  "))
	}
}

// TestNoListenerScannerBites plants every construct the scanner must catch,
// plus a clean file, so a scanner that went blind would fail here.
func TestNoListenerScannerBites(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a/netlisten.go", "package a\n\nimport \"net\"\n\nfunc f() { _, _ = net.Listen(\"tcp\", \"127.0.0.1:0\") }\n")
	write("a/aliased.go", "package a\n\nimport nn \"net\"\n\nfunc g() { _, _ = nn.ListenTCP(\"tcp\", nil) }\n")
	write("a/listenandserve.go", "package a\n\nimport \"net/http\"\n\nfunc h() { _ = http.ListenAndServe(\"127.0.0.1:0\", nil) }\n")
	write("a/server.go", "package a\n\nimport \"net/http\"\n\nvar s = &http.Server{}\n")
	write("a/httptest.go", "package a\n\nimport \"net/http/httptest\"\n\nvar s = httptest.NewServer(nil)\n")
	write("a/tls.go", "package a\n\nimport \"crypto/tls\"\n\nfunc i() { _, _ = tls.Listen(\"tcp\", \"\", nil) }\n")
	write("a/dot.go", "package a\n\nimport . \"net\"\n\nvar _ = IPv4len\n")
	want := []string{
		"a/aliased.go:5 nn.ListenTCP", "a/dot.go dot-import of net", "a/httptest.go:5 httptest.NewServer",
		"a/listenandserve.go:5 http.ListenAndServe", "a/netlisten.go:5 net.Listen",
		"a/server.go:5 http.Server", "a/tls.go:5 tls.Listen",
	}
	got := scanListeners(t, dir)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("scanner found:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}

	// Test files, testdata and clean code are not findings.
	clean := t.TempDir()
	if err := os.WriteFile(filepath.Join(clean, "ok.go"), []byte("package ok\n\nimport \"net\"\n\nfunc d() { _, _ = net.Dial(\"tcp\", \"x\"); _ = net.ParseIP }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clean, "ok_test.go"), []byte("package ok\n\nimport \"net\"\n\nfunc e() { _, _ = net.Listen(\"tcp\", \"\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if hits := scanListeners(t, clean); len(hits) != 0 {
		t.Errorf("clean tree flagged: %v", hits)
	}
}
