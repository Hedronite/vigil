package server

import (
	"context"
	"crypto/sha256"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hedronite/vigil/internal/simplexdoc"
	"github.com/Hedronite/vigil/internal/summary"
)

func loopbackAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func tokenFile(t *testing.T, secret string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "read.token")
	if err := os.WriteFile(path, []byte(secret), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func tokenHash(t *testing.T, secret string) [32]byte {
	t.Helper()
	return sha256.Sum256([]byte(secret))
}

func startServer(t *testing.T, addr string, hash [32]byte, build DocumentBuilder) (context.CancelFunc, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, Options{Listen: addr, ReadToken: hash, Build: build})
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return cancel, addr
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("server did not start")
	return nil, ""
}

func get(t *testing.T, url, token string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

func TestHealthzOpen(t *testing.T) {
	addr := loopbackAddr(t)
	cancel, _ := startServer(t, addr, tokenHash(t, "s"), staticDoc(t))
	defer cancel()
	code, body := get(t, "http://"+addr+"/healthz", "")
	if code != http.StatusOK || body != healthBody {
		t.Fatalf("healthz = %d %q, want 200 %q", code, body, healthBody)
	}
}

func TestUnauthorizedByteIdentical(t *testing.T) {
	addr := loopbackAddr(t)
	secret := "quiet-secret"
	cancel, _ := startServer(t, addr, tokenHash(t, secret), staticDoc(t))
	defer cancel()
	for _, token := range []string{"", "wrong"} {
		code, body := get(t, "http://"+addr+"/v0/summary", token)
		if code != http.StatusUnauthorized || body != UnauthorizedBody {
			t.Fatalf("token %q: %d %q, want 401 identical body", token, code, body)
		}
	}
}

func TestRoutesAuthAndErrors(t *testing.T) {
	addr := loopbackAddr(t)
	secret := "quiet-secret"
	cancel, _ := startServer(t, addr, tokenHash(t, secret), staticDoc(t))
	defer cancel()
	base := "http://" + addr
	code, _ := get(t, base+"/v0/summary", secret)
	if code != http.StatusOK {
		t.Fatalf("summary status = %d", code)
	}
	code, body := get(t, base+"/v0/hosts", secret)
	if code != http.StatusOK || !strings.Contains(body, `"id":"lab"`) {
		t.Fatalf("hosts = %d %q", code, body)
	}
	code, body = get(t, base+"/v0/hosts/lab", secret)
	if code != http.StatusOK || !strings.Contains(body, `"severity":"GREEN"`) {
		t.Fatalf("host = %d %q", code, body)
	}
	code, body = get(t, base+"/v0/hosts/missing", secret)
	if code != http.StatusNotFound || !strings.Contains(body, `"not_found"`) {
		t.Fatalf("missing host = %d %q", code, body)
	}
	code, _ = get(t, base+"/v0/sources", secret)
	if code != http.StatusOK {
		t.Fatalf("sources status = %d", code)
	}
	code, body = get(t, base+"/nope", secret)
	if code != http.StatusNotFound || !strings.Contains(body, `"not_found"`) {
		t.Fatalf("unknown = %d %q", code, body)
	}
	req, _ := http.NewRequest(http.MethodPost, base+"/v0/summary", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST summary = %d, want 405", resp.StatusCode)
	}
	code, body = get(t, base+"/v0/signals", secret)
	if code != http.StatusNotFound {
		t.Fatalf("signals = %d %q, want 404", code, body)
	}
}

func TestBindRules(t *testing.T) {
	if err := CheckBind("127.0.0.1:10082", false); err != nil {
		t.Fatalf("loopback: %v", err)
	}
	if err := CheckBind("192.0.2.1:10082", false); err == nil {
		t.Fatal("non-loopback without flag should fail")
	}
	if err := CheckBind("192.0.2.1:10082", true); err != nil {
		t.Fatalf("non-loopback with flag: %v", err)
	}
}

func TestTokenModeAndEmpty(t *testing.T) {
	path := tokenFile(t, "x", 0o644)
	if _, err := LoadReadTokenHash(path); err != ErrTokenMode {
		t.Fatalf("mode = %v, want ErrTokenMode", err)
	}
	path = tokenFile(t, "   \n", 0o600)
	if _, err := LoadReadTokenHash(path); err != ErrTokenEmpty {
		t.Fatalf("empty = %v, want ErrTokenEmpty", err)
	}
}

func TestRunReturnsServeError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	err = Run(ctx, Options{Listener: ln, ReadToken: tokenHash(t, "s"), Build: staticDoc(t)})
	if err == nil {
		t.Fatal("expected serve error on a closed listener")
	}
}

func TestShutdown(t *testing.T) {
	addr := loopbackAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, Options{Listen: addr, ReadToken: tokenHash(t, "s"), Build: staticDoc(t)}) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown timed out")
	}
}

func TestZeroBackendDial(t *testing.T) {
	addr := loopbackAddr(t)
	var dials atomic.Int32
	dialer := &net.Dialer{Timeout: 50 * time.Millisecond}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			dials.Add(1)
			return dialer.DialContext(ctx, network, address)
		},
	}
	client := &http.Client{Transport: transport}
	cancel, _ := startServer(t, addr, tokenHash(t, "s"), staticDoc(t))
	defer cancel()
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/v0/sources", nil)
	req.Header.Set("Authorization", "Bearer s")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if dials.Load() != 1 {
		t.Fatalf("dials = %d, want only the listener", dials.Load())
	}
}

func staticDoc(t *testing.T) DocumentBuilder {
	t.Helper()
	return func(_ context.Context, now time.Time) (*summary.Document, error) {
		sev := "GREEN"
		obs := now.Format(time.RFC3339)
		return &summary.Document{
			APIVersion: summary.Version, GeneratedAt: obs, Overall: &sev,
			Hosts:   []simplexdoc.Host{{ID: "lab", Severity: "GREEN", Source: "simplex"}},
			Sources: []summary.Source{{ID: "simplex", Kind: "simplex", State: "ok", ObservedAt: &obs}},
		}, nil
	}
}
