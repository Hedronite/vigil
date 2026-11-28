package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Hedronite/vigil/internal/config"
	"github.com/Hedronite/vigil/internal/reasons"
	"github.com/Hedronite/vigil/internal/simplexdoc"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func greenDoc(t *testing.T) []byte {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	path := filepath.Join(root, "testdata", "simplex", "green", "document.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func pushOpts(t *testing.T, testimonySecret string) Options {
	t.Helper()
	cfg := &config.Config{
		Authorities: []config.Authority{{ID: "simplex", Via: "push"}},
	}
	st := NewState()
	return Options{
		ReadToken:      tokenHash(t, "read"),
		SignalToken:    tokenHash(t, "sig"),
		TestimonyToken: tokenHash(t, testimonySecret),
		Testimony:      NewTestimonyStore(),
		Config:         cfg,
		State:          st,
		Now:            func() time.Time { return time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC) },
	}
}

func postTestimonyHTTP(t *testing.T, opts Options, source, token string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v0/testimony/"+source, bytes.NewReader(body))
	req.SetPathValue("source", source)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	testimonyRoutes(opts).ServeHTTP(rec, req)
	return rec
}

func TestPushAuthorityConfiguredNilAndNonSimplex(t *testing.T) {
	if pushAuthorityConfigured(nil, "simplex") {
		t.Fatal("nil config")
	}
	cfg := &config.Config{Authorities: []config.Authority{{ID: "simplex", Via: "push"}}}
	if !pushAuthorityConfigured(cfg, "simplex") {
		t.Fatal("want push simplex")
	}
	if pushAuthorityConfigured(cfg, "augur") {
		t.Fatal("augur path not push route")
	}
}

func TestTestimonyUnknownSource(t *testing.T) {
	opts := pushOpts(t, "t")
	for _, src := range []string{"augur", "cubit", "scry", "missing"} {
		rec := postTestimonyHTTP(t, opts, src, "t", greenDoc(t))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: %d", src, rec.Code)
		}
		assertVigilCode(t, rec.Body.String(), "unknown_source")
	}
	cfg := &config.Config{Authorities: []config.Authority{{ID: "simplex", Via: "file", PathRef: "env:X"}}}
	opts.Config = cfg
	rec := postTestimonyHTTP(t, opts, "simplex", "t", greenDoc(t))
	assertVigilCode(t, rec.Body.String(), "unknown_source")
}

func TestTestimonyUnauthorizedIdentical(t *testing.T) {
	opts := pushOpts(t, "witness")
	for _, tok := range []string{"", "wrong", "read"} {
		rec := postTestimonyHTTP(t, opts, "simplex", tok, greenDoc(t))
		if rec.Code != http.StatusUnauthorized || rec.Body.String() != UnauthorizedBody {
			t.Fatalf("token %q: %d %q", tok, rec.Code, rec.Body.String())
		}
	}
}

func TestPostTestimonyRequiresStateAndStore(t *testing.T) {
	opts := pushOpts(t, "witness")
	opts.State = nil
	rec := postTestimonyHTTP(t, opts, "simplex", "witness", greenDoc(t))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	assertVigilCode(t, rec.Body.String(), "internal")
}

func TestTestimonyBodyCap(t *testing.T) {
	opts := pushOpts(t, "witness")
	big := bytes.Repeat([]byte("x"), simplexdoc.MaxBytes+1)
	rec := postTestimonyHTTP(t, opts, "simplex", "witness", big)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", rec.Code)
	}
	assertVigilCode(t, rec.Body.String(), reasons.BodyCap)
}

func TestTestimonyBodyExactlyMaxBytesStillChecked(t *testing.T) {
	opts := pushOpts(t, "witness")
	body := bytes.Repeat([]byte("x"), simplexdoc.MaxBytes)
	rec := postTestimonyHTTP(t, opts, "simplex", "witness", body)
	if rec.Code == http.StatusRequestEntityTooLarge {
		t.Fatal("exact max should not be body_cap")
	}
}

func TestTestimonyAcceptReceiptSchema(t *testing.T) {
	opts := pushOpts(t, "witness")
	rec := postTestimonyHTTP(t, opts, "simplex", "witness", greenDoc(t))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body %s", rec.Code, rec.Body.String())
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	schemaPath := filepath.Join(filepath.Dir(file), "..", "..", "schema", "vigil.receipt.v0.schema.json")
	sch, err := jsonschema.NewCompiler().Compile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(doc); err != nil {
		t.Fatal(err)
	}
}

func TestTestimonyStaleReplay(t *testing.T) {
	opts := pushOpts(t, "witness")
	body := greenDoc(t)
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	opts.Now = func() time.Time { return now }
	rec := postTestimonyHTTP(t, opts, "simplex", "witness", body)
	if rec.Code != http.StatusAccepted {
		t.Fatal(rec.Body.String())
	}
	newer := bytes.Replace(body, []byte(`2026-11-13T18:00:00Z`), []byte(`2026-11-13T18:05:00Z`), 1)
	rec = postTestimonyHTTP(t, opts, "simplex", "witness", newer)
	if rec.Code != http.StatusAccepted {
		t.Fatal(rec.Body.String())
	}
	rec = postTestimonyHTTP(t, opts, "simplex", "witness", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("replay status = %d", rec.Code)
	}
	assertVigilCode(t, rec.Body.String(), "stale_testimony")
}

func TestTestimonyIdempotent(t *testing.T) {
	opts := pushOpts(t, "witness")
	body := greenDoc(t)
	rec1 := postTestimonyHTTP(t, opts, "simplex", "witness", body)
	rec2 := postTestimonyHTTP(t, opts, "simplex", "witness", body)
	if rec1.Body.String() != rec2.Body.String() {
		t.Fatalf("receipts differ:\n%s\n%s", rec1.Body, rec2.Body)
	}
}

func TestTestimonyDecodeErrorsLeaveStoreEmpty(t *testing.T) {
	opts := pushOpts(t, "witness")
	badMajor := []byte(`{"apiVersion":"simplex.probe/v1","generated_at":"2026-11-13T18:00:00Z"}`)
	rec := postTestimonyHTTP(t, opts, "simplex", "witness", badMajor)
	assertVigilCode(t, rec.Body.String(), reasons.SchemaMajor)
	obs, err := opts.Testimony.Observation("simplex", opts.State, opts.now())
	if err != nil {
		t.Fatal(err)
	}
	if obs != nil {
		t.Fatal("store changed")
	}
}

func TestTestimonyStoreAcceptUnit(t *testing.T) {
	st := NewState()
	store := NewTestimonyStore()
	body := greenDoc(t)
	doc, code, status := DecodeTestimonyBody(body)
	if status != 0 {
		t.Fatalf("decode %s status %d", code, status)
	}
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	if _, err := store.Accept("simplex", body, doc, st, now); err != nil {
		t.Fatal(err)
	}
	obs, err := store.Observation("simplex", st, now)
	if err != nil || obs == nil || obs.Document.Overall != "GREEN" {
		t.Fatalf("obs = %+v err %v", obs, err)
	}
}

func TestTestimonyWrongMethod(t *testing.T) {
	opts := pushOpts(t, "witness")
	req := httptest.NewRequest(http.MethodGet, "/v0/testimony/simplex", nil)
	req.SetPathValue("source", "simplex")
	rec := httptest.NewRecorder()
	testimonyRoutes(opts).ServeHTTP(rec, req)
	assertVigilCode(t, rec.Body.String(), "method_not_allowed")
}
