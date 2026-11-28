package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hedronite/vigil/internal/exitcode"
	"github.com/Hedronite/vigil/internal/server"
	"github.com/Hedronite/vigil/internal/signals"
)

func postSignal(t *testing.T, addr, token string, body []byte) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/v0/signals", bytes.NewReader(body))
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

func TestServeSignalTokenUnusable(t *testing.T) {
	publish(t, vendoredDoc(t, "green"), generated)
	dir := t.TempDir()
	readPath := filepath.Join(dir, "read.token")
	sigPath := filepath.Join(dir, "signal.token")
	if err := os.WriteFile(readPath, []byte("r"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sigPath, []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIGIL_READ_TOKEN_FILE", readPath)
	t.Setenv("VIGIL_SIGNAL_TOKEN_FILE", sigPath)
	cfg := `apiVersion: vigil.config/v0
listen: "127.0.0.1:10082"
authorities:
  - id: simplex
    path_ref: env:VIGIL_TEST_SIMPLEX
tokens:
  read_file: env:VIGIL_READ_TOKEN_FILE
  signal_file: env:VIGIL_SIGNAL_TOKEN_FILE
`
	path := writeText(t, "config.yaml", cfg)
	code, _, errs := call(t, liveHandlers(), "serve", "--config", path)
	if code != exitcode.Startup || !strings.Contains(errs, "token_unusable") {
		t.Fatalf("unusable signal token: exit=%d stderr=%q", code, errs)
	}
}

func TestServeSignalTokenBadLocator(t *testing.T) {
	publish(t, vendoredDoc(t, "green"), generated)
	readPath := writeText(t, "read.token", "r")
	t.Setenv("VIGIL_READ_TOKEN_FILE", readPath)
	t.Setenv("VIGIL_SIGNAL_TOKEN_FILE", "")
	cfg := `apiVersion: vigil.config/v0
listen: "127.0.0.1:10082"
authorities:
  - id: simplex
    path_ref: env:VIGIL_TEST_SIMPLEX
tokens:
  read_file: env:VIGIL_READ_TOKEN_FILE
  signal_file: env:VIGIL_NO_SUCH_SIGNAL_PATH
`
	path := writeText(t, "config.yaml", cfg)
	code, _, errs := call(t, liveHandlers(), "serve", "--config", path)
	if code != exitcode.Startup || !strings.Contains(errs, "token_unusable") {
		t.Fatalf("bad signal locator: exit=%d stderr=%q", code, errs)
	}
}

func TestServeSignalTokenMissingFile(t *testing.T) {
	publish(t, vendoredDoc(t, "green"), generated)
	dir := t.TempDir()
	readPath := filepath.Join(dir, "read.token")
	t.Setenv("VIGIL_READ_TOKEN_FILE", readPath)
	if err := os.WriteFile(readPath, []byte("r"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIGIL_SIGNAL_TOKEN_FILE", filepath.Join(dir, "absent.token"))
	cfg := `apiVersion: vigil.config/v0
listen: "127.0.0.1:10082"
authorities:
  - id: simplex
    path_ref: env:VIGIL_TEST_SIMPLEX
tokens:
  read_file: env:VIGIL_READ_TOKEN_FILE
  signal_file: env:VIGIL_SIGNAL_TOKEN_FILE
`
	path := writeText(t, "config.yaml", cfg)
	code, _, errs := call(t, liveHandlers(), "serve", "--config", path)
	if code != exitcode.Startup || !strings.Contains(errs, "token_unusable") {
		t.Fatalf("missing signal file: exit=%d stderr=%q", code, errs)
	}
}

func TestServeSignalTokenEmpty(t *testing.T) {
	publish(t, vendoredDoc(t, "green"), generated)
	dir := t.TempDir()
	readPath := filepath.Join(dir, "read.token")
	sigPath := filepath.Join(dir, "signal.token")
	if err := os.WriteFile(readPath, []byte("r"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sigPath, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIGIL_READ_TOKEN_FILE", readPath)
	t.Setenv("VIGIL_SIGNAL_TOKEN_FILE", sigPath)
	cfg := `apiVersion: vigil.config/v0
listen: "127.0.0.1:10082"
authorities:
  - id: simplex
    path_ref: env:VIGIL_TEST_SIMPLEX
tokens:
  read_file: env:VIGIL_READ_TOKEN_FILE
  signal_file: env:VIGIL_SIGNAL_TOKEN_FILE
`
	path := writeText(t, "config.yaml", cfg)
	code, _, errs := call(t, liveHandlers(), "serve", "--config", path)
	if code != exitcode.Startup || !strings.Contains(errs, "token_unusable") {
		t.Fatalf("empty signal token: exit=%d stderr=%q", code, errs)
	}
}

func TestServeMissingSignalTokenConfig(t *testing.T) {
	publish(t, vendoredDoc(t, "green"), generated)
	readPath := writeText(t, "read.token", "r")
	t.Setenv("VIGIL_READ_TOKEN_FILE", readPath)
	cfg := `apiVersion: vigil.config/v0
listen: "127.0.0.1:10082"
authorities:
  - id: simplex
    path_ref: env:VIGIL_TEST_SIMPLEX
tokens:
  read_file: env:VIGIL_READ_TOKEN_FILE
`
	path := writeText(t, "config.yaml", cfg)
	code, _, errs := call(t, liveHandlers(), "serve", "--config", path)
	if code != exitcode.Startup || !strings.Contains(errs, "missing_signal_token") {
		t.Fatalf("missing signal: exit=%d stderr=%q", code, errs)
	}
}

func TestServeSignalFloodSummaryStable(t *testing.T) {
	cfg, readTok, sigTok := serveFixture(t)
	addr := listenFromConfig(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if code := run(ctx, []string{"serve", "--config", cfg, "--now", pinned}, io.Discard, io.Discard, liveHandlers()); code != 0 {
			t.Errorf("serve exit %d", code)
		}
	}()
	waitListen(t, addr)
	before, code := fetchSummary(t, addr, readTok)
	if code != http.StatusOK {
		t.Fatalf("summary before %d", code)
	}
	payload := []byte(`{"apiVersion":"vigil.signal/v0","source":"deploy","kind":"roll","severity":"info"}`)
	alt := []byte(`{"apiVersion":"vigil.signal/v0","source":"deploy","kind":"roll","severity":"notice","ttl":120}`)
	for i := 0; i < signals.Capacity+50; i++ {
		body := payload
		if i&1 == 1 {
			body = alt
		}
		st, out := postSignal(t, addr, sigTok, body)
		if st != http.StatusCreated {
			t.Fatalf("post %d: %s", st, out)
		}
	}
	after, code := fetchSummary(t, addr, readTok)
	if code != http.StatusOK {
		t.Fatalf("summary after %d", code)
	}
	if before != after {
		t.Fatalf("summary changed under flood\nbefore: %s\nafter: %s", before, after)
	}
	var got struct {
		Verdicts []any `json:"verdicts"`
		Sources  []any `json:"sources"`
		Overall  *string
	}
	if err := json.Unmarshal([]byte(after), &got); err != nil {
		t.Fatal(err)
	}
	var prev struct {
		Verdicts []any `json:"verdicts"`
		Sources  []any `json:"sources"`
		Overall  *string
	}
	if err := json.Unmarshal([]byte(before), &prev); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got.Verdicts) != fmt.Sprint(prev.Verdicts) || fmt.Sprint(got.Sources) != fmt.Sprint(prev.Sources) {
		t.Fatalf("verdicts or sources changed")
	}
}

func TestServeSignalPostAuth(t *testing.T) {
	cfg, readTok, sigTok := serveFixture(t)
	addr := listenFromConfig(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = run(ctx, []string{"serve", "--config", cfg, "--now", pinned}, io.Discard, io.Discard, liveHandlers())
	}()
	waitListen(t, addr)
	body := []byte(`{"apiVersion":"vigil.signal/v0","source":"d","kind":"k","severity":"info"}`)
	for _, tok := range []string{"", readTok} {
		code, out := postSignal(t, addr, tok, body)
		if code != http.StatusUnauthorized || out != server.UnauthorizedBody {
			t.Fatalf("token %q: %d %q", tok, code, out)
		}
	}
	code, _ := postSignal(t, addr, sigTok, body)
	if code != http.StatusCreated {
		t.Fatalf("signal token post = %d", code)
	}
}
