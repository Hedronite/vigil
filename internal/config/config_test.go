package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Hedronite/vigil/internal/config"
	"github.com/Hedronite/vigil/internal/exitcode"
)

func repoRoot(tb testing.TB) string {
	tb.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		tb.Fatal("cannot locate the test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func codeOf(t *testing.T, err error) int {
	t.Helper()
	var cfgErr *config.Error
	if !errors.As(err, &cfgErr) {
		t.Fatalf("error %v is not a *config.Error", err)
	}
	return cfgErr.Code
}

// wildcardFixture names the fixtures whose defect is a wildcard listen, which
// refuses to start (5) rather than reading as a typo (3).
func wildcardFixture(name string) bool {
	switch {
	case name == "listen-wildcard.yaml", name == "listen-empty-host.yaml":
		return true
	case strings.HasPrefix(name, "listen-unspecified"):
		return true
	case strings.HasPrefix(name, "listen-mapped"):
		return true
	}
	return false
}

// TestConfigFixtures proves the loader and the schema agree on every fixture:
// a valid fixture loads, an invalid one is refused with the code the fixture's
// defect implies.
func TestConfigFixtures(t *testing.T) {
	root := repoRoot(t)
	valid, _ := filepath.Glob(filepath.Join(root, "testdata", "config", "valid", "*.yaml"))
	invalid, _ := filepath.Glob(filepath.Join(root, "testdata", "config", "invalid", "*.yaml"))
	if len(valid) == 0 || len(invalid) == 0 {
		t.Fatalf("found %d valid and %d invalid config fixtures", len(valid), len(invalid))
	}
	for _, path := range valid {
		if _, err := config.Load(path); err != nil {
			t.Errorf("valid/%s: %v", filepath.Base(path), err)
		}
	}
	for _, path := range invalid {
		name := filepath.Base(path)
		_, err := config.Load(path)
		if err == nil {
			t.Errorf("invalid/%s: loaded, want a refusal", name)
			continue
		}
		want := exitcode.Usage
		if wildcardFixture(name) {
			want = exitcode.Startup
		}
		if got := codeOf(t, err); got != want {
			t.Errorf("invalid/%s: exit %d, want %d (%v)", name, got, want, err)
		}
	}
}

// TestOverlayFixtures does the same for the overlay.
func TestOverlayFixtures(t *testing.T) {
	root := repoRoot(t)
	for _, path := range mustGlob(t, filepath.Join(root, "testdata", "overlay", "valid", "*.yaml")) {
		if _, err := config.LoadOverlay(path); err != nil {
			t.Errorf("overlay/valid/%s: %v", filepath.Base(path), err)
		}
	}
	for _, path := range mustGlob(t, filepath.Join(root, "testdata", "overlay", "invalid", "*.yaml")) {
		if _, err := config.LoadOverlay(path); err == nil {
			t.Errorf("overlay/invalid/%s: loaded, want a refusal", filepath.Base(path))
		}
	}
}

func mustGlob(t *testing.T, pattern string) []string {
	t.Helper()
	paths, err := filepath.Glob(pattern)
	if err != nil || len(paths) == 0 {
		t.Fatalf("glob %s: %v", pattern, err)
	}
	return paths
}

const minimal = `apiVersion: vigil.config/v0
authorities:
  - id: simplex
    path_ref: /var/lib/vigil/simplex.json
`

// TestDefaults proves a config that says nothing optional still yields the
// frozen defaults.
func TestDefaults(t *testing.T) {
	dir := t.TempDir()
	c, err := config.Load(writeFile(t, dir, "c.yaml", minimal+`queries:
  - name: up_by_host
    backend: prom
    expr: 'up{job="node"}'
`+`backends:
  - id: prom
    kind: prom
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Listen != config.DefaultListen {
		t.Errorf("listen = %q, want %q", c.Listen, config.DefaultListen)
	}
	q, ok := c.Query("up_by_host")
	if !ok {
		t.Fatal("query not found")
	}
	if q.Timeout != config.DefaultQueryTimeout || q.MaxSeries != config.DefaultMaxSeries {
		t.Errorf("query defaults = %q/%d, want %q/%d", q.Timeout, q.MaxSeries, config.DefaultQueryTimeout, config.DefaultMaxSeries)
	}
}

// TestRefusals covers the rules that the fixtures do not, each with the exit
// code the operator will see.
func TestRefusals(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{
			name: "duplicate backend id",
			body: minimal + "backends:\n  - id: prom\n    kind: prom\n  - id: prom\n    kind: grafana\n",
			want: exitcode.Usage,
		},
		{
			name: "duplicate query name",
			body: minimal + "backends:\n  - id: prom\n    kind: prom\nqueries:\n  - name: q\n    backend: prom\n    expr: up\n  - name: q\n    backend: prom\n    expr: up\n",
			want: exitcode.Usage,
		},
		{
			name: "query names an undeclared backend",
			body: minimal + "queries:\n  - name: q\n    backend: prom\n    expr: up\n",
			want: exitcode.Usage,
		},
		{
			name: "second template variable",
			body: minimal + `backends:
  - id: prom
    kind: prom
queries:
  - name: q
    backend: prom
    expr: 'rate(x[5m]){{.pod}}'
`,
			want: exitcode.Usage,
		},
		{
			name: "bare template",
			body: minimal + `backends:
  - id: prom
    kind: prom
queries:
  - name: q
    backend: prom
    expr: 'up{{host}}'
`,
			want: exitcode.Usage,
		},
		{
			name: "locator traversal",
			body: `apiVersion: vigil.config/v0
authorities:
  - id: simplex
    path_ref: /var/lib/../etc/shadow
`,
			want: exitcode.Usage,
		},
		{
			name: "locator is a URL",
			body: `apiVersion: vigil.config/v0
authorities:
  - id: simplex
    path_ref: https://example.invalid/simplex.json
`,
			want: exitcode.Usage,
		},
		{
			name: "wildcard listen",
			body: `apiVersion: vigil.config/v0
listen: "0.0.0.0:10082"
authorities:
  - id: simplex
    path_ref: env:VIGIL_SIMPLEX_PATH
`,
			want: exitcode.Startup,
		},
		{
			name: "mapped unspecified listen",
			body: `apiVersion: vigil.config/v0
listen: "[::ffff:0:0]:10082"
authorities:
  - id: simplex
    path_ref: env:VIGIL_SIMPLEX_PATH
`,
			want: exitcode.Startup,
		},
		{
			// A zero-padded address is malformed rather than unspecified: it
			// never names an interface, so it is a typo to refuse at 3.
			name: "zero-padded wildcard",
			body: `apiVersion: vigil.config/v0
listen: "00.00.00.00:10082"
authorities:
  - id: simplex
    path_ref: env:VIGIL_SIMPLEX_PATH
`,
			want: exitcode.Usage,
		},
		{
			name: "port zero",
			body: `apiVersion: vigil.config/v0
listen: "127.0.0.1:0"
authorities:
  - id: simplex
    path_ref: env:VIGIL_SIMPLEX_PATH
`,
			want: exitcode.Usage,
		},
		{
			name: "unknown key",
			body: minimal + "listen_extra: true\n",
			want: exitcode.Usage,
		},
		{
			name: "authority id outside the contract",
			body: `apiVersion: vigil.config/v0
authorities:
  - id: grafana
    path_ref: env:VIGIL_GRAFANA_PATH
`,
			want: exitcode.Usage,
		},
	}
	dir := t.TempDir()
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := config.Load(writeFile(t, dir, strings.ReplaceAll(tc.name, " ", "-")+".yaml", tc.body))
			if err == nil {
				t.Fatal("loaded, want a refusal")
			}
			if got := codeOf(t, err); got != tc.want {
				t.Fatalf("exit %d, want %d (%v)", got, tc.want, err)
			}
		})
	}
}

// TestListenSpellings proves the loader rejects every wildcard spelling and
// accepts the loopback controls, matching the schema's fixtures.
func TestListenSpellings(t *testing.T) {
	dir := t.TempDir()
	body := func(listen string) string {
		return "apiVersion: vigil.config/v0\nlisten: \"" + listen + "\"\nauthorities:\n  - id: simplex\n    path_ref: env:VIGIL_SIMPLEX_PATH\n"
	}
	cases := map[string]bool{ // true = accepted
		"127.0.0.1:10082":            true,
		"[::1]:10082":                true,
		":10082":                     false,
		"0.0.0.0:10082":              false,
		"[::]:10082":                 false,
		"[0::]:10082":                false,
		"[::0]:10082":                false,
		"[0000::]:10082":             false,
		"[0:0::]:10082":              false,
		"[0:0:0:0:0:0:0:0]:10082":    false,
		"[::ffff:0.0.0.0]:10082":     false,
		"[::ffff:0:0]:10082":         false,
		"[::FFFF:0000:0000]:10082":   false,
		"[0:0:0:0:0:ffff:0:0]:10082": false,
		"localhost:10082":            false,
		"127.0.0.1:0":                false,
		"127.0.0.1:65536":            false,
	}
	i := 0
	for listen, wantOK := range cases {
		i++
		_, err := config.Load(writeFile(t, dir, "l"+string(rune('a'+i%26))+string(rune('a'+i/26))+".yaml", body(listen)))
		if wantOK && err != nil {
			t.Errorf("%s: %v", listen, err)
		}
		if !wantOK && err == nil {
			t.Errorf("%s: accepted, want a refusal", listen)
		}
	}
}

// TestOverlayRules covers the overlay rules that the fixtures do not.
func TestOverlayRules(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{"dot segment", `apiVersion: vigil.overlay/v0
backends:
  - id: prom
    address: http://prom.example.invalid:9090/../admin
`, exitcode.Usage},
		{"credentials", `apiVersion: vigil.overlay/v0
backends:
  - id: prom
    address: http://user:pass@prom.example.invalid:9090
`, exitcode.Usage},
		{"query string", `apiVersion: vigil.overlay/v0
backends:
  - id: prom
    address: http://prom.example.invalid:9090/?x=1
`, exitcode.Usage},
		{"duplicate slug", `apiVersion: vigil.overlay/v0
backends:
  - id: prom
    address: http://prom.example.invalid:9090
hosts:
  - slug: castle
    instances: [castle.example.invalid:9100]
  - slug: castle
    instances: [castle.example.invalid:9101]
`, exitcode.Usage},
		{"instance claimed twice", `apiVersion: vigil.overlay/v0
backends:
  - id: prom
    address: http://prom.example.invalid:9090
hosts:
  - slug: castle
    instances: [castle.example.invalid:9100]
  - slug: tower
    instances: [castle.example.invalid:9100]
`, exitcode.Usage},
		{"no instances", `apiVersion: vigil.overlay/v0
backends:
  - id: prom
    address: http://prom.example.invalid:9090
hosts:
  - slug: castle
    instances: []
`, exitcode.Usage},
		{"instance is a path", `apiVersion: vigil.overlay/v0
backends:
  - id: prom
    address: http://prom.example.invalid:9090
hosts:
  - slug: castle
    instances: ["/etc/passwd"]
`, exitcode.Usage},
	}
	dir := t.TempDir()
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := config.LoadOverlay(writeFile(t, dir, strings.ReplaceAll(tc.name, " ", "-")+".yaml", tc.body))
			if err == nil {
				t.Fatal("loaded, want a refusal")
			}
			if got := codeOf(t, err); got != tc.want {
				t.Fatalf("exit %d, want %d (%v)", got, tc.want, err)
			}
		})
	}
}

// TestBind checks the cross-document references.
func TestBind(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Load(writeFile(t, dir, "c.yaml", minimal+`backends:
  - id: prom
    kind: prom
    vantage: lathe
queries:
  - name: q
    backend: prom
    expr: up
`))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if b, ok := cfg.Backend("prom"); !ok || b.Vantage != "lathe" {
		t.Fatalf("backend vantage = %+v", b)
	}

	good, err := config.LoadOverlay(writeFile(t, dir, "o.yaml", `apiVersion: vigil.overlay/v0
backends:
  - id: prom
    address: http://prom.example.invalid:9090
hosts:
  - slug: castle
    instances: [castle.example.invalid:9100]
`))
	if err != nil {
		t.Fatalf("load overlay: %v", err)
	}
	if err := good.Bind(cfg); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if slug, ok := good.SlugFor("castle.example.invalid:9100"); !ok || slug != "castle" {
		t.Fatalf("slug = %q, %v", slug, ok)
	}
	if addr, ok := good.Address("prom"); !ok || addr != "http://prom.example.invalid:9090" {
		t.Fatalf("address = %q, %v", addr, ok)
	}

	undeclared, err := config.LoadOverlay(writeFile(t, dir, "o2.yaml", `apiVersion: vigil.overlay/v0
backends:
  - id: grafana
    address: http://grafana.example.invalid:3000
`))
	if err != nil {
		t.Fatalf("load overlay: %v", err)
	}
	if err := undeclared.Bind(cfg); err == nil {
		t.Fatal("bound an overlay naming an undeclared backend")
	}

}

// FuzzConfig proves the loader never panics and never returns a half-valid
// config: anything it accepts satisfies the rules again.
func FuzzConfig(f *testing.F) {
	root := repoRoot(f)
	for _, pattern := range []string{
		filepath.Join(root, "testdata", "config", "valid", "*.yaml"),
		filepath.Join(root, "testdata", "config", "invalid", "*.yaml"),
	} {
		paths, _ := filepath.Glob(pattern)
		for _, path := range paths {
			if b, err := os.ReadFile(path); err == nil {
				f.Add(b)
			}
		}
	}
	f.Add([]byte(minimal))
	f.Add([]byte(""))
	f.Add([]byte("apiVersion: vigil.config/v0\nlisten: \"[::]:1\"\n"))

	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(dir, "fuzz.yaml")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Skipf("write: %v", err)
		}
		c, err := config.Load(path)
		if err != nil {
			var cfgErr *config.Error
			if !errors.As(err, &cfgErr) {
				t.Fatalf("untyped error: %v", err)
			}
			if cfgErr.Code != exitcode.Usage && cfgErr.Code != exitcode.Startup {
				t.Fatalf("unexpected exit code %d", cfgErr.Code)
			}
			return
		}
		if c.APIVersion != config.ConfigMajor {
			t.Fatalf("accepted major %q", c.APIVersion)
		}
		if c.Listen == "" {
			t.Fatal("accepted a config with no listen address")
		}
		if _, err := config.Load(path); err != nil {
			t.Fatalf("accepted then refused: %v", err)
		}
	})
}
