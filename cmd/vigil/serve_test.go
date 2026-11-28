package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hedronite/vigil/internal/exitcode"
	"github.com/Hedronite/vigil/internal/server"
)

func serveFixture(t *testing.T) (configPath, readSecret, signalSecret string) {
	t.Helper()
	addr := loopbackListen(t)
	dir := t.TempDir()
	readSecret = "facet-read-token"
	signalSecret = "facet-signal-token"
	readPath := filepath.Join(dir, "read.token")
	sigPath := filepath.Join(dir, "signal.token")
	if err := os.WriteFile(readPath, []byte(readSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sigPath, []byte(signalSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIGIL_READ_TOKEN_FILE", readPath)
	t.Setenv("VIGIL_SIGNAL_TOKEN_FILE", sigPath)
	publish(t, vendoredDoc(t, "green"), generated)
	cfg := `apiVersion: vigil.config/v0
listen: "` + addr + `"
backends:
  - id: prom
    kind: prom
  - id: grafana
    kind: grafana
authorities:
  - id: simplex
    path_ref: env:VIGIL_TEST_SIMPLEX
tokens:
  read_file: env:VIGIL_READ_TOKEN_FILE
  signal_file: env:VIGIL_SIGNAL_TOKEN_FILE
`
	return writeText(t, "config.yaml", cfg), readSecret, signalSecret
}

func loopbackListen(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func listenFromConfig(t *testing.T, path string) string {
	t.Helper()
	for _, line := range strings.Split(readFile(t, path), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "listen:") {
			return strings.Trim(strings.TrimPrefix(line, "listen:"), `" `)
		}
	}
	t.Fatal("no listen in config")
	return ""
}

func waitListen(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			if closeErr := c.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("listener %s did not open", addr)
}

func fetchSummary(t *testing.T, addr, token string) (string, int) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/v0/summary", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(body), resp.StatusCode
}

func TestServeSummaryMatchesCLI(t *testing.T) {
	cfg, secret, _ := serveFixture(t)
	addr := listenFromConfig(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan int, 1)
	go func() {
		errc <- run(ctx, []string{"serve", "--config", cfg, "--now", pinned}, io.Discard, io.Discard, liveHandlers())
	}()
	waitListen(t, addr)
	_, _, cliOut := summarise(t, summaryConfig, "--now", pinned)
	httpOut, code := fetchSummary(t, addr, secret)
	if code != http.StatusOK {
		t.Fatalf("http status %d", code)
	}
	if cliOut != httpOut {
		t.Fatalf("summary mismatch\ncli: %s\nhttp: %s", cliOut, httpOut)
	}
	cancel()
	if c := <-errc; c != 0 {
		t.Fatalf("serve exit %d", c)
	}
}

func TestServeStartupWildcardListen(t *testing.T) {
	dir := t.TempDir()
	readPath := filepath.Join(dir, "read.token")
	sigPath := filepath.Join(dir, "signal.token")
	if err := os.WriteFile(readPath, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sigPath, []byte("sig"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIGIL_READ_TOKEN_FILE", readPath)
	t.Setenv("VIGIL_SIGNAL_TOKEN_FILE", sigPath)
	publish(t, vendoredDoc(t, "green"), generated)
	cfg := `apiVersion: vigil.config/v0
listen: "0.0.0.0:10082"
authorities:
  - id: simplex
    path_ref: env:VIGIL_TEST_SIMPLEX
tokens:
  read_file: env:VIGIL_READ_TOKEN_FILE
  signal_file: env:VIGIL_SIGNAL_TOKEN_FILE
`
	path := writeText(t, "config.yaml", cfg)
	code, _, errs := call(t, liveHandlers(), "serve", "--config", path)
	if code != exitcode.Startup || !strings.Contains(errs, "wildcard_listen") {
		t.Fatalf("wildcard: exit=%d stderr=%q", code, errs)
	}
}

func TestServeNoBackendDialWithOverlay(t *testing.T) {
	cfg, secret, _ := serveFixture(t)
	addr := listenFromConfig(t, cfg)
	overlay := writeText(t, "overlay.yaml", `apiVersion: vigil.overlay/v0
backends:
  - id: prom
    address: http://192.0.2.1:9090
  - id: grafana
    address: http://192.0.2.2:3000
hosts:
  - slug: lab
    instances: ["lab:9100"]
`)
	var dials atomic.Int32
	transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		dials.Add(1)
		return (&net.Dialer{Timeout: 20 * time.Millisecond}).DialContext(ctx, network, address)
	}}
	client := &http.Client{Transport: transport}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if code := run(ctx, []string{"serve", "--config", cfg, "--overlay", overlay, "--now", pinned}, io.Discard, io.Discard, liveHandlers()); code != 0 {
			t.Errorf("serve exit %d", code)
		}
	}()
	waitListen(t, addr)
	req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/v0/sources", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if dials.Load() != 1 {
		t.Fatalf("dials=%d want 1 (listener only)", dials.Load())
	}
	cancel()
}

func TestServeShutdownExitZero(t *testing.T) {
	cfg, _, _ := serveFixture(t)
	addr := listenFromConfig(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	codec := make(chan int, 1)
	go func() {
		codec <- run(ctx, []string{"serve", "--config", cfg, "--now", pinned}, io.Discard, io.Discard, liveHandlers())
	}()
	waitListen(t, addr)
	cancel()
	select {
	case code := <-codec:
		if code != 0 {
			t.Fatalf("exit %d, want 0", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown timeout")
	}
}

func TestServeMissingReadTokenConfig(t *testing.T) {
	publish(t, vendoredDoc(t, "green"), generated)
	cfg := `apiVersion: vigil.config/v0
listen: "127.0.0.1:10082"
authorities:
  - id: simplex
    path_ref: env:VIGIL_TEST_SIMPLEX
`
	path := writeText(t, "config.yaml", cfg)
	code, _, errs := call(t, liveHandlers(), "serve", "--config", path)
	if code != exitcode.Startup || !strings.Contains(errs, "missing_read_token") {
		t.Fatalf("missing token: exit=%d stderr=%q", code, errs)
	}
}

func TestServeOverlayRejectedBeforeListen(t *testing.T) {
	cfg, _, _ := serveFixture(t)
	bad := writeText(t, "overlay.yaml", `apiVersion: vigil.overlay/v0
backends:
  - id: unknown
    address: http://example.invalid:9090
hosts:
  - slug: lab
    instances: ["lab:9100"]
`)
	code, _, errs := call(t, liveHandlers(), "serve", "--config", cfg, "--overlay", bad)
	if code != exitcode.Usage || !strings.Contains(errs, "overlay_invalid") {
		t.Fatalf("overlay: exit=%d stderr=%q", code, errs)
	}
}

func TestServeNonLoopbackNeedsFlag(t *testing.T) {
	dir := t.TempDir()
	readPath := filepath.Join(dir, "read.token")
	sigPath := filepath.Join(dir, "signal.token")
	if err := os.WriteFile(readPath, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sigPath, []byte("sig"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIGIL_READ_TOKEN_FILE", readPath)
	t.Setenv("VIGIL_SIGNAL_TOKEN_FILE", sigPath)
	publish(t, vendoredDoc(t, "green"), generated)
	cfg := `apiVersion: vigil.config/v0
listen: "192.0.2.5:10082"
authorities:
  - id: simplex
    path_ref: env:VIGIL_TEST_SIMPLEX
tokens:
  read_file: env:VIGIL_READ_TOKEN_FILE
  signal_file: env:VIGIL_SIGNAL_TOKEN_FILE
`
	path := writeText(t, "config.yaml", cfg)
	code, _, errs := call(t, liveHandlers(), "serve", "--config", path)
	if code != exitcode.Startup || !strings.Contains(errs, "non_loopback_listen") {
		t.Fatalf("non-loopback: exit=%d stderr=%q", code, errs)
	}
}

func TestServeBindFailure(t *testing.T) {
	cfg, _, _ := serveFixture(t)
	addr := listenFromConfig(t, cfg)
	hold, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := hold.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	code, _, errs := call(t, liveHandlers(), "serve", "--config", cfg, "--now", pinned)
	if code != exitcode.Startup || !strings.Contains(errs, "bind_failed") {
		t.Fatalf("bind: exit=%d stderr=%q", code, errs)
	}
}

func TestServeUnauthorizedBody(t *testing.T) {
	if server.UnauthorizedBody != `{"apiVersion":"vigil.error/v0","error":{"code":"unauthorized"}}`+"\n" {
		t.Fatalf("401 body drift: %q", server.UnauthorizedBody)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
