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

	"github.com/Hedronite/vigil/internal/config"
	"github.com/Hedronite/vigil/internal/signals"
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
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
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

func startServer(t *testing.T, addr string, read, signal [32]byte, build DocumentBuilder) (context.CancelFunc, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	store := signals.NewStore()
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, Options{
			Listen: addr, ReadToken: read, SignalToken: signal, Build: build,
			Signals: store, Limiter: &signals.Limiter{},
		})
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			if closeErr := conn.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
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
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

func TestHealthzOpen(t *testing.T) {
	addr := loopbackAddr(t)
	cancel, _ := startServer(t, addr, tokenHash(t, "s"), tokenHash(t, "s"), staticDoc(t))
	defer cancel()
	code, body := get(t, "http://"+addr+"/healthz", "")
	if code != http.StatusOK || body != healthBody {
		t.Fatalf("healthz = %d %q, want 200 %q", code, body, healthBody)
	}
}

func TestUnauthorizedByteIdentical(t *testing.T) {
	addr := loopbackAddr(t)
	secret := "quiet-secret"
	cancel, _ := startServer(t, addr, tokenHash(t, secret), tokenHash(t, "sig"), staticDoc(t))
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
	cancel, _ := startServer(t, addr, tokenHash(t, secret), tokenHash(t, "sig"), staticDoc(t))
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
	req, err := http.NewRequest(http.MethodPost, base+"/v0/summary", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST summary = %d, want 405", resp.StatusCode)
	}
	code, body = get(t, base+"/v0/signals", secret)
	if code != http.StatusOK || body != "[]\n" {
		t.Fatalf("signals GET = %d %q, want empty list", code, body)
	}
	reqSig, err := http.NewRequest(http.MethodPost, base+"/v0/signals", strings.NewReader(`{"apiVersion":"vigil.signal/v0","source":"deploy","kind":"k","severity":"info"}`))
	if err != nil {
		t.Fatal(err)
	}
	reqSig.Header.Set("Authorization", "Bearer "+secret)
	respSig, err := http.DefaultClient.Do(reqSig)
	if err != nil {
		t.Fatal(err)
	}
	if err := respSig.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if respSig.StatusCode != http.StatusUnauthorized {
		t.Fatalf("POST with read token = %d, want 401", respSig.StatusCode)
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

func TestRunRejectsZeroTestimonyWhenPushConfigured(t *testing.T) {
	addr := loopbackAddr(t)
	ctx := context.Background()
	cfg := &config.Config{Authorities: []config.Authority{{ID: "simplex", Via: "push"}}}
	err := Run(ctx, Options{
		Listen: addr, ReadToken: tokenHash(t, "r"), SignalToken: tokenHash(t, "s"),
		Config: cfg, Testimony: NewTestimonyStore(), State: NewState(),
		Build: staticDoc(t), Signals: signals.NewStore(), Limiter: &signals.Limiter{},
	})
	if err == nil {
		t.Fatal("expected missing testimony token hash")
	}
}

func TestRunRejectsZeroSignalToken(t *testing.T) {
	addr := loopbackAddr(t)
	ctx := context.Background()
	err := Run(ctx, Options{
		Listen: addr, ReadToken: tokenHash(t, "r"), Build: staticDoc(t),
		Limiter: &signals.Limiter{},
	})
	if err == nil {
		t.Fatal("expected missing signal token hash")
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
	store := signals.NewStore()
	err = Run(ctx, Options{
		Listener: ln, ReadToken: tokenHash(t, "s"), SignalToken: tokenHash(t, "s"),
		Build: staticDoc(t), Signals: store, Limiter: &signals.Limiter{},
	})
	if err == nil {
		t.Fatal("expected serve error on a closed listener")
	}
}

func TestShutdown(t *testing.T) {
	addr := loopbackAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	store := signals.NewStore()
	go func() {
		done <- Run(ctx, Options{
			Listen: addr, ReadToken: tokenHash(t, "s"), SignalToken: tokenHash(t, "s"),
			Build: staticDoc(t), Signals: store, Limiter: &signals.Limiter{},
		})
	}()
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
	cancel, _ := startServer(t, addr, tokenHash(t, "s"), tokenHash(t, "s"), staticDoc(t))
	defer cancel()
	req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/v0/sources", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer s")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
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
