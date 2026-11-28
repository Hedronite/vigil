package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hedronite/vigil/internal/config"
	"github.com/Hedronite/vigil/internal/exitcode"
	"github.com/Hedronite/vigil/internal/server"
	"github.com/Hedronite/vigil/internal/signals"
	"github.com/Hedronite/vigil/internal/summary"
)

func pushServeFixture(t *testing.T) (configPath, readSecret, testimonySecret, addr string) {
	t.Helper()
	addr = loopbackListen(t)
	dir := t.TempDir()
	readSecret = "facet-read-token"
	signalSecret := "facet-signal-token"
	testimonySecret = "facet-testimony-token"
	for name, secret := range map[string]string{
		"read.token": readSecret, "signal.token": signalSecret, "testimony.token": testimonySecret,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(secret), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("VIGIL_READ_TOKEN_FILE", filepath.Join(dir, "read.token"))
	t.Setenv("VIGIL_SIGNAL_TOKEN_FILE", filepath.Join(dir, "signal.token"))
	t.Setenv("VIGIL_TESTIMONY_TOKEN_FILE", filepath.Join(dir, "testimony.token"))
	cfg := `apiVersion: vigil.config/v0
listen: "` + addr + `"
authorities:
  - id: simplex
    via: push
tokens:
  read_file: env:VIGIL_READ_TOKEN_FILE
  signal_file: env:VIGIL_SIGNAL_TOKEN_FILE
  testimony_file: env:VIGIL_TESTIMONY_TOKEN_FILE
`
	return writeText(t, "config.yaml", cfg), readSecret, testimonySecret, addr
}

func startPushServe(t *testing.T, cfgPath string, nowFn func() time.Time) context.CancelFunc {
	t.Helper()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	read, sig, err := loadServeTokenHashes(cfg)
	if err != nil {
		t.Fatal(err)
	}
	testimonyHash, err := loadTestimonyTokenHash(cfg)
	if err != nil {
		t.Fatal(err)
	}
	st := server.NewState()
	testimony := server.NewTestimonyStore()
	started := nowFn()
	build := func(callCtx context.Context, _ time.Time) (*summary.Document, error) {
		return serveSummary(callCtx, cfg, st, testimony, started, nowFn())
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Run(ctx, server.Options{
			Listen: cfg.Listen, ReadToken: read, SignalToken: sig, TestimonyToken: testimonyHash,
			Testimony: testimony, Config: cfg, State: st, Build: build,
			Signals: signals.NewStore(), Limiter: &signals.Limiter{}, Now: nowFn,
		})
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-errCh; err != nil {
			t.Errorf("serve: %v", err)
		}
	})
	return cancel
}

func postTestimonyURL(t *testing.T, addr, token string, body []byte) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/v0/testimony/simplex", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b)
}

func getSummaryDoc(t *testing.T, addr, readToken string) summary.Document {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/v0/summary", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+readToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("summary %d: %s", resp.StatusCode, b)
	}
	var doc summary.Document
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestServeTestimonyEndToEnd(t *testing.T) {
	cfgPath, readTok, witTok, addr := pushServeFixture(t)
	start := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	var nowMu sync.Mutex
	cur := start
	nowFn := func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return cur
	}
	startPushServe(t, cfgPath, nowFn)
	waitListen(t, addr)
	green := []byte(vendoredDoc(t, "green"))
	code, _ := postTestimonyURL(t, addr, witTok, green)
	if code != http.StatusAccepted {
		t.Fatalf("post green: %d", code)
	}
	doc := getSummaryDoc(t, addr, readTok)
	if doc.Overall == nil || *doc.Overall != "GREEN" {
		t.Fatalf("overall = %v", doc.Overall)
	}
	nowMu.Lock()
	cur = start.Add(121 * time.Second)
	nowMu.Unlock()
	doc = getSummaryDoc(t, addr, readTok)
	if doc.Overall == nil || *doc.Overall != "YELLOW" {
		t.Fatalf("stale overall = %v", doc.Overall)
	}
	redBody := bytes.Replace([]byte(vendoredDoc(t, "both-prom-down")),
		[]byte(`"generated_at": "2026-11-13T18:00:00Z"`),
		[]byte(`"generated_at": "2026-11-13T18:05:00Z"`), 1)
	code, _ = postTestimonyURL(t, addr, witTok, redBody)
	if code != http.StatusAccepted {
		t.Fatalf("post red: %d", code)
	}
	nowMu.Lock()
	cur = start.Add(130 * time.Second)
	nowMu.Unlock()
	doc = getSummaryDoc(t, addr, readTok)
	if doc.Overall == nil || *doc.Overall != "RED" {
		t.Fatalf("red overall = %v", doc.Overall)
	}
	code, body := postTestimonyURL(t, addr, witTok, green)
	if code != http.StatusConflict || !strings.Contains(body, "stale_testimony") {
		t.Fatalf("replay: %d %s", code, body)
	}
}

func TestServeTestimonyNeverUsesPathRef(t *testing.T) {
	cfgPath, _, witTok, addr := pushServeFixture(t)
	path := filepath.Join(t.TempDir(), "must-not-exist.json")
	t.Setenv("VIGIL_TEST_SIMPLEX", path)
	nowFn := func() time.Time { return generated }
	startPushServe(t, cfgPath, nowFn)
	waitListen(t, addr)
	code, _ := postTestimonyURL(t, addr, witTok, []byte(vendoredDoc(t, "green")))
	if code != http.StatusAccepted {
		t.Fatalf("post: %d", code)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("serve opened a path_ref file")
	}
}

func TestServeSummaryPushStillUnavailableOneShot(t *testing.T) {
	cfg := `apiVersion: vigil.config/v0
authorities:
  - id: simplex
    via: push
tokens:
  testimony_file: env:VIGIL_TESTIMONY_TOKEN
`
	t.Setenv("VIGIL_TESTIMONY_TOKEN", writeText(t, "t.token", "x"))
	code, doc, _ := summarise(t, cfg, "--now", pinned)
	if code != exitcode.Unavailable {
		t.Fatalf("exit = %d", code)
	}
	if doc.Sources[0].DetailCode == nil || *doc.Sources[0].DetailCode != "source_unavailable" {
		t.Fatalf("sources = %+v", doc.Sources)
	}
}
