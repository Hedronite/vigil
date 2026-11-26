package simplexdoc_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Hedronite/vigil/internal/reasons"
	"github.com/Hedronite/vigil/internal/simplexdoc"
)

const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func casesDir(tb testing.TB) string {
	tb.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		tb.Fatal("cannot locate the test file")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "simplex")
}

func vendored(tb testing.TB, name string) []byte {
	tb.Helper()
	body, err := os.ReadFile(filepath.Join(casesDir(tb), name, "document.json"))
	if err != nil {
		tb.Fatalf("read %s: %v", name, err)
	}
	return body
}

// wantUnavailable checks that err is the structured refusal with this code.
func wantUnavailable(t *testing.T, err error, code string) {
	t.Helper()
	var gone *simplexdoc.UnavailableError
	if !errors.As(err, &gone) {
		t.Fatalf("error = %v, want an *UnavailableError", err)
	}
	if gone.DetailCode != code {
		t.Errorf("detail code = %q, want %q (%v)", gone.DetailCode, code, err)
	}
	if gone.Error() == "" || errors.Unwrap(gone) == nil {
		t.Errorf("refusal carries no message or cause: %v", gone)
	}
}

// reference is what a document says, read by a second route so the decoder is
// not checked against itself.
type reference struct {
	GeneratedAt string             `json:"generated_at"`
	Overall     string             `json:"overall"`
	Vantage     simplexdoc.Vantage `json:"vantage"`
	Observed    *bool              `json:"observed"`
	ValidFor    *int               `json:"valid_for"`
	Declaration *string            `json:"declaration"`
	Hosts       []struct {
		ID       string   `json:"id"`
		Severity string   `json:"severity"`
		Reasons  []string `json:"reasons"`
	} `json:"hosts"`
}

func checkAgainst(t *testing.T, body []byte, doc *simplexdoc.Document) {
	t.Helper()
	var want reference
	if err := json.Unmarshal(body, &want); err != nil {
		t.Fatalf("reference decode: %v", err)
	}
	if got := doc.GeneratedAt.Format(time.RFC3339Nano); got != want.GeneratedAt {
		t.Errorf("generated_at = %s, want %s", got, want.GeneratedAt)
	}
	if doc.Overall != want.Overall || doc.Vantage != want.Vantage {
		t.Errorf("overall/vantage = %s/%+v, want %s/%+v", doc.Overall, doc.Vantage, want.Overall, want.Vantage)
	}
	if !reflect.DeepEqual(doc.Observed, want.Observed) || !reflect.DeepEqual(doc.ValidFor, want.ValidFor) ||
		!reflect.DeepEqual(doc.Declaration, want.Declaration) {
		t.Errorf("header = %v/%v/%v, want %v/%v/%v", doc.Observed, doc.ValidFor, doc.Declaration,
			want.Observed, want.ValidFor, want.Declaration)
	}
	if len(doc.Hosts) != len(want.Hosts) {
		t.Fatalf("%d hosts, want %d", len(doc.Hosts), len(want.Hosts))
	}
	for i, h := range want.Hosts {
		expect := simplexdoc.Host{ID: h.ID, Severity: h.Severity, Reasons: h.Reasons, Source: "simplex"}
		if !reflect.DeepEqual(doc.Hosts[i], expect) {
			t.Errorf("hosts[%d] = %#v, want %#v", i, doc.Hosts[i], expect)
		}
	}
}

// TestVendoredCases runs every case under testdata/simplex, found rather than
// named: a case with a document decodes and passes its content through, and a
// case without one is the absent file.
func TestVendoredCases(t *testing.T) {
	entries, err := os.ReadDir(casesDir(t))
	if err != nil {
		t.Fatalf("read the cases: %v", err)
	}
	now := time.Now().Add(time.Hour)
	cases, documents := 0, 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		cases++
		path := filepath.Join(casesDir(t), entry.Name(), "document.json")
		abs, err := filepath.Abs(path)
		if err != nil {
			t.Fatalf("abs: %v", err)
		}
		body, readErr := os.ReadFile(abs)
		if readErr == nil {
			documents++
		}
		t.Run(entry.Name(), func(t *testing.T) {
			seen, err := simplexdoc.Read(t.Context(), abs, now)
			if readErr != nil {
				wantUnavailable(t, err, reasons.SourceUnavailable)
				if !errors.Is(err, os.ErrNotExist) {
					t.Errorf("error = %v, want it to say the file does not exist", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			checkAgainst(t, body, seen.Document)
		})
	}
	if cases != 13 || documents != 11 {
		t.Errorf("found %d cases and %d documents, want 13 and 11", cases, documents)
	}
}

// TestVendoredFacts pins the cases the contract calls out by name.
func TestVendoredFacts(t *testing.T) {
	decode := func(name string) *simplexdoc.Document {
		doc, err := simplexdoc.Decode(vendored(t, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return doc
	}
	blind := decode("blind")
	if blind.Vantage.Status != "blind" || blind.Overall != "RED" || blind.Observed == nil || *blind.Observed {
		t.Errorf("blind = %s %+v observed %v, want a RED verdict from a blind, unobserving vantage", blind.Overall, blind.Vantage, blind.Observed)
	}
	dark := decode("expected-dark-timeout")
	if dark.Overall != "GREEN" || dark.Observed == nil || !*dark.Observed {
		t.Errorf("expected-dark-timeout = %s observed %v, want GREEN and observed", dark.Overall, dark.Observed)
	}
	codes := map[string]bool{}
	for _, h := range decode("enrichment").Hosts {
		for _, r := range h.Reasons {
			if strings.HasPrefix(r, "ts_") {
				codes[r] = true
			}
		}
	}
	if len(codes) != 5 {
		t.Errorf("enrichment carries %v, want five ts_ codes passed through", codes)
	}
}

// parts is a small document Decode accepts, as values a case can change: the
// document, its vantage and its first host.
func parts() (doc, vantage, host map[string]any) {
	vantage = map[string]any{"id": "lab", "status": "ok"}
	host = map[string]any{"id": "edge", "severity": "GREEN", "reasons": []any{}}
	doc = map[string]any{
		"apiVersion":   "simplex.probe/v0",
		"generated_at": "2026-11-13T18:00:00Z",
		"observed":     true,
		"valid_for":    120,
		"declaration":  digest,
		"vantage":      vantage,
		"overall":      "GREEN",
		"hosts": []any{
			host,
			map[string]any{"id": "lab", "severity": "GREEN", "reasons": []any{"tcp_connect_ok", "held"}},
		},
		"checks": []any{},
	}
	return doc, vantage, host
}

func green() map[string]any {
	doc, _, _ := parts()
	return doc
}

func encode(t *testing.T, v any) []byte {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return body
}

type drop struct{}

// with returns green with one member set or removed. The path names a member;
// "vantage.<member>" and "hosts.<member>" reach the vantage and the first host.
func with(t *testing.T, path string, value any) []byte {
	t.Helper()
	doc, vantage, host := parts()
	target, key := doc, path
	if rest, ok := strings.CutPrefix(path, "vantage."); ok {
		target, key = vantage, rest
	}
	if rest, ok := strings.CutPrefix(path, "hosts."); ok {
		target, key = host, rest
	}
	if _, gone := value.(drop); gone {
		delete(target, key)
	} else {
		target[key] = value
	}
	return encode(t, doc)
}

// TestDecodeAccepts: what the contract allows, absent header members included.
func TestDecodeAccepts(t *testing.T) {
	cases := map[string][]byte{
		"base":                  encode(t, green()),
		"no observed":           with(t, "observed", drop{}),
		"no valid_for":          with(t, "valid_for", drop{}),
		"no declaration":        with(t, "declaration", drop{}),
		"null declaration":      with(t, "declaration", nil),
		"valid_for one":         with(t, "valid_for", 1),
		"fractional second":     with(t, "generated_at", "2026-11-13T18:00:00.123456789Z"),
		"unchecked":             with(t, "vantage.status", "unchecked"),
		"yellow unobserved":     []byte(strings.Replace(string(with(t, "observed", false)), `"overall":"GREEN"`, `"overall":"YELLOW"`, 1)),
		"reason unknown to us":  with(t, "hosts.reasons", []any{"a_reason_added_later", "x9"}),
		"host id of 32":         with(t, "hosts.id", "a"+strings.Repeat("b", 31)),
		"no checks":             with(t, "checks", drop{}),
		"checks of any shape":   with(t, "checks", "not read"),
		"extra top member":      with(t, "added_later", map[string]any{"x": []any{1}}),
		"extra vantage member":  with(t, "vantage.added_later", 1),
		"extra host member":     with(t, "hosts.added_later", 1),
		"surrounding space":     append(append([]byte("\n\t "), encode(t, green())...), " \n"...),
		"host source ignored":   with(t, "hosts.source", "augur"),
		"other-case member":     with(t, "Overall", "RED"),
		"other-case version":    with(t, "APIVersion", "simplex.probe/v9"),
		"other-case in a host":  with(t, "hosts.Severity", "RED"),
		"other-case in vantage": with(t, "vantage.Status", "blind"),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			doc, err := simplexdoc.Decode(body)
			if err != nil {
				t.Fatalf("decode: %v\n%s", err, body)
			}
			if doc.Overall == "RED" || doc.Vantage.Status == "blind" || doc.Hosts[0].Severity != "GREEN" || doc.Hosts[0].Source != "simplex" {
				t.Errorf("a member that is not one of ours was read: %s %+v %+v", doc.Overall, doc.Vantage, doc.Hosts[0])
			}
			if doc.Hosts[0].Reasons == nil {
				t.Error("reasons decoded to nil; the summary needs an array")
			}
		})
	}
}

// TestDecodeUnknownMajor: another version of the contract is its own code, and
// it is judged before anything that version may have changed.
func TestDecodeUnknownMajor(t *testing.T) {
	for _, body := range []string{
		`{"apiVersion":"simplex.probe/v1"}`,
		`{"apiVersion":"simplex.probe/v2","overall":5,"hosts":"gone"}`,
		`{"apiVersion":"simplex.probe/v1alpha1"}`,
		`{"apiVersion":"simplex.probe/v00"}`,
		`{"apiVersion":"augur.status/v0"}`,
	} {
		doc, err := simplexdoc.Decode([]byte(body))
		if doc != nil {
			t.Errorf("%s: a document came back", body)
		}
		wantUnavailable(t, err, reasons.SchemaMajor)
	}
}

// TestDecodeRefuses: everything else is source_unavailable, and nothing of the
// document comes back with the refusal.
func TestDecodeRefuses(t *testing.T) {
	base := string(encode(t, green()))
	swap := func(old, new string) []byte {
		if !strings.Contains(base, old) {
			t.Fatalf("base has no %s", old)
		}
		return []byte(strings.Replace(base, old, new, 1))
	}
	host := func(id string) map[string]any {
		return map[string]any{"id": id, "severity": "GREEN", "reasons": []any{}}
	}
	cases := map[string][]byte{
		"empty file":          {},
		"not json":            []byte("not json"),
		"truncated":           []byte(base[:len(base)/2]),
		"trailing token":      []byte(base + "{}"),
		"trailing garbage":    []byte(base + "x"),
		"top null":            []byte("null"),
		"top array":           []byte("[" + base + "]"),
		"invalid utf-8":       swap(`"tcp_connect_ok"`, "\"tcp\xff\""),
		"duplicate member":    swap(`"overall":"GREEN"`, `"overall":"RED","overall":"GREEN"`),
		"duplicate in a host": swap(`"id":"edge"`, `"id":"core","id":"edge"`),
		"duplicate ignored":   swap(`"checks":[]`, `"checks":[{"a":1,"a":2}]`),
		"too deep":            swap(`"checks":[]`, `"checks":`+strings.Repeat("[", 20000)+strings.Repeat("]", 20000)),

		"no apiVersion":      with(t, "apiVersion", drop{}),
		"null apiVersion":    with(t, "apiVersion", nil),
		"numeric apiVersion": with(t, "apiVersion", 0),
		"empty apiVersion":   with(t, "apiVersion", ""),
		"shapeless version":  with(t, "apiVersion", "simplex.probe"),
		"wordless version":   with(t, "apiVersion", "/v0"),

		"no generated_at":      with(t, "generated_at", drop{}),
		"offset time":          with(t, "generated_at", "2026-11-13T18:00:00+00:00"),
		"comma fraction":       with(t, "generated_at", "2026-11-13T18:00:00,5Z"),
		"month thirteen":       with(t, "generated_at", "2026-13-13T18:00:00Z"),
		"february thirtieth":   with(t, "generated_at", "2026-02-30T18:00:00Z"),
		"numeric time":         with(t, "generated_at", 1),
		"no overall":           with(t, "overall", drop{}),
		"overall null":         with(t, "overall", nil),
		"overall lower case":   with(t, "overall", "green"),
		"no vantage":           with(t, "vantage", drop{}),
		"vantage null":         with(t, "vantage", nil),
		"vantage no id":        with(t, "vantage.id", drop{}),
		"vantage digit slug":   with(t, "vantage.id", "9lab"),
		"vantage no status":    with(t, "vantage.status", drop{}),
		"vantage other status": with(t, "vantage.status", "dark"),

		"no hosts":             with(t, "hosts", drop{}),
		"hosts null":           with(t, "hosts", nil),
		"hosts empty":          with(t, "hosts", []any{}),
		"host null":            with(t, "hosts", []any{nil}),
		"host twice":           with(t, "hosts", []any{host("edge"), host("edge")}),
		"host digit slug":      with(t, "hosts.id", "9lab"),
		"host dotted":          with(t, "hosts.id", "edge.example"),
		"host id of 33":        with(t, "hosts.id", "a"+strings.Repeat("b", 32)),
		"host no severity":     with(t, "hosts.severity", drop{}),
		"host bad severity":    with(t, "hosts.severity", "BLUE"),
		"host no reasons":      with(t, "hosts.reasons", drop{}),
		"host reasons null":    with(t, "hosts.reasons", nil),
		"reason null":          with(t, "hosts.reasons", []any{nil}),
		"reason upper case":    with(t, "hosts.reasons", []any{"Held"}),
		"reason leading digit": with(t, "hosts.reasons", []any{"9x"}),
		"reason of 49":         with(t, "hosts.reasons", []any{strings.Repeat("a", 49)}),
		"reason twice":         with(t, "hosts.reasons", []any{"held", "held"}),

		"observed null": with(t, "observed", nil),
		// A null would read as false, which only a GREEN would give away.
		"observed null, not green": []byte(strings.Replace(string(with(t, "observed", nil)), `"overall":"GREEN"`, `"overall":"YELLOW"`, 1)),
		"observed string":          with(t, "observed", "false"),
		"unobserved green":         with(t, "observed", false),
		"valid_for zero":           with(t, "valid_for", 0),
		"valid_for negative":       with(t, "valid_for", -1),
		"valid_for null":           with(t, "valid_for", nil),
		"valid_for string":         with(t, "valid_for", "120"),
		"valid_for fraction":       swap(`"valid_for":120`, `"valid_for":120.0`),
		"valid_for overflow":       swap(`"valid_for":120`, `"valid_for":99999999999999999999`),
		"declaration empty":        with(t, "declaration", ""),
		"declaration short":        with(t, "declaration", "sha256:aa"),
		"declaration upper":        with(t, "declaration", "sha256:"+strings.Repeat("A", 64)),
		"declaration numeric":      with(t, "declaration", 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			doc, err := simplexdoc.Decode(body)
			if doc != nil {
				t.Errorf("a document came back: %+v", doc)
			}
			wantUnavailable(t, err, reasons.SourceUnavailable)
		})
	}
}

// FuzzDecodeSimplexDoc: whatever the bytes, the answer is a refusal with one of
// two codes or a document that keeps every promise.
func FuzzDecodeSimplexDoc(f *testing.F) {
	paths, err := filepath.Glob(filepath.Join(casesDir(f), "*", "document.json"))
	if err != nil || len(paths) != 11 {
		f.Fatalf("found %d vendored documents: %v", len(paths), err)
	}
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			f.Fatalf("read %s: %v", path, err)
		}
		f.Add(body)
		f.Add(body[:len(body)/2])
	}
	for _, seed := range []string{
		``, `null`, `[]`, `{}`, `{"apiVersion":"simplex.probe/v0"}`, `{"apiVersion":"simplex.probe/v1"}`,
		`{"apiVersion":"simplex.probe/v0","observed":false,"overall":"GREEN"}`,
		`{"apiVersion":"simplex.probe/v0","hosts":[null],"vantage":null}`,
		`{"apiVersion":"simplex.probe/v0","apiVersion":"simplex.probe/v0"}`,
		"{\"apiVersion\":\"\xff\"}",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		doc, err := simplexdoc.Decode(body)
		if err != nil {
			var gone *simplexdoc.UnavailableError
			if doc != nil || !errors.As(err, &gone) {
				t.Fatalf("doc %v with error %v, want no document and an *UnavailableError", doc, err)
			}
			if gone.DetailCode != reasons.SourceUnavailable && gone.DetailCode != reasons.SchemaMajor {
				t.Fatalf("detail code %q", gone.DetailCode)
			}
			return
		}
		checkPromises(t, doc)
	})
}

func checkPromises(t *testing.T, doc *simplexdoc.Document) {
	t.Helper()
	severity := map[string]bool{"GREEN": true, "YELLOW": true, "RED": true}
	if !severity[doc.Overall] || doc.GeneratedAt.Location() != time.UTC || len(doc.Hosts) == 0 {
		t.Fatalf("accepted %+v", doc)
	}
	if doc.Observed != nil && !*doc.Observed && doc.Overall == "GREEN" {
		t.Fatal("accepted a GREEN that observed nothing")
	}
	if doc.ValidFor != nil && *doc.ValidFor < 1 {
		t.Fatalf("accepted valid_for %d", *doc.ValidFor)
	}
	if doc.Declaration != nil && len(*doc.Declaration) != len(digest) {
		t.Fatalf("accepted declaration %q", *doc.Declaration)
	}
	hosts := map[string]bool{}
	for _, h := range doc.Hosts {
		if hosts[h.ID] || !severity[h.Severity] || h.Reasons == nil || h.Source != "simplex" {
			t.Fatalf("accepted host %+v", h)
		}
		hosts[h.ID] = true
		codes := map[string]bool{}
		for _, r := range h.Reasons {
			if codes[r] || !reasons.Valid(r) {
				t.Fatalf("accepted reason %q on %s", r, h.ID)
			}
			codes[r] = true
		}
	}
}
