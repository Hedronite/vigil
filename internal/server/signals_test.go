package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hedronite/vigil/internal/signals"
)

func TestPostSignalBodyCap(t *testing.T) {
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	store := signals.NewStore()
	sig := tokenHash(t, "sig")
	h := signalRoutes(Options{
		SignalToken: sig, Signals: store, Limiter: &signals.Limiter{}, Now: func() time.Time { return now },
	})
	big := bytes.Repeat([]byte("x"), signals.MaxBody+10)
	req := httptest.NewRequest(http.MethodPost, "/v0/signals", bytes.NewReader(big))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Authorization", "Bearer sig")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", rec.Code)
	}
	assertVigilCode(t, rec.Body.String(), "body_cap")
}

func TestPostSignalNilDeps(t *testing.T) {
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	body := []byte(`{"apiVersion":"vigil.signal/v0","source":"d","kind":"k","severity":"info"}`)
	req := httptest.NewRequest(http.MethodPost, "/v0/signals", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Authorization", "Bearer sig")
	rec := httptest.NewRecorder()
	signalRoutes(Options{
		SignalToken: tokenHash(t, "sig"), Now: func() time.Time { return now },
	}).ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("nil deps = %d", rec.Code)
	}
	store := signals.NewStore()
	req2 := httptest.NewRequest(http.MethodPost, "/v0/signals", bytes.NewReader(body))
	req2.RemoteAddr = "127.0.0.1:1"
	req2.Header.Set("Authorization", "Bearer sig")
	rec2 := httptest.NewRecorder()
	signalRoutes(Options{
		SignalToken: tokenHash(t, "sig"), Signals: store, Now: func() time.Time { return now },
	}).ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusInternalServerError {
		t.Fatalf("nil limiter = %d", rec2.Code)
	}
}

func TestPostSignalBodyLengthBoundary(t *testing.T) {
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	h := signalRoutes(Options{
		SignalToken: tokenHash(t, "sig"), Signals: signals.NewStore(),
		Limiter: &signals.Limiter{}, Now: func() time.Time { return now },
	})
	for _, tc := range []struct {
		size     int
		code     int
		codeName string
	}{
		{signals.MaxBody + 1, http.StatusRequestEntityTooLarge, "body_cap"},
		{signals.MaxBody, http.StatusBadRequest, "invalid_signal"},
	} {
		body := bytes.Repeat([]byte("x"), tc.size)
		req := httptest.NewRequest(http.MethodPost, "/v0/signals", bytes.NewReader(body))
		req.RemoteAddr = "127.0.0.1:1"
		req.Header.Set("Authorization", "Bearer sig")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.code {
			t.Fatalf("size %d: status=%d", tc.size, rec.Code)
		}
		assertVigilCode(t, rec.Body.String(), tc.codeName)
	}
}

func TestPostSignalBadRemoteAddr(t *testing.T) {
	now := time.Now().UTC()
	h := signalRoutes(Options{
		SignalToken: tokenHash(t, "sig"), Signals: signals.NewStore(),
		Limiter: &signals.Limiter{}, Now: func() time.Time { return now },
	})
	req := httptest.NewRequest(http.MethodPost, "/v0/signals", strings.NewReader(`{"apiVersion":"vigil.signal/v0","source":"d","kind":"k","severity":"info"}`))
	req.RemoteAddr = "not-host-port"
	req.Header.Set("Authorization", "Bearer sig")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
	assertVigilCode(t, rec.Body.String(), "invalid_signal")
}

func TestPruneSignalsExitsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	now := func() time.Time { return time.Now().UTC() }
	store := signals.NewStore()
	done := make(chan struct{})
	go func() {
		pruneSignals(ctx, store, now)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("prune did not exit")
	}
}

func TestPruneSignalsNilStore(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pruneSignals(ctx, nil, time.Now)
}

func TestPostSignalRateLimited(t *testing.T) {
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	store := signals.NewStore()
	lim := &signals.Limiter{}
	read := tokenHash(t, "read")
	sig := tokenHash(t, "sig")
	h := signalRoutes(Options{
		ReadToken: read, SignalToken: sig, Signals: store, Limiter: lim, Now: func() time.Time { return now },
	})
	body := []byte(`{"apiVersion":"vigil.signal/v0","source":"d","kind":"k","severity":"info"}`)
	for i := 0; i < 11; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v0/signals", bytes.NewReader(body))
		req.RemoteAddr = "192.0.2.7:1234"
		req.Header.Set("Authorization", "Bearer sig")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if i < 10 && rec.Code != http.StatusCreated {
			t.Fatalf("request %d: %d", i, rec.Code)
		}
		if i == 10 {
			if rec.Code != http.StatusTooManyRequests {
				t.Fatalf("want 429, got %d", rec.Code)
			}
			assertVigilCode(t, rec.Body.String(), "rate_limited")
		}
		if _, err := io.ReadAll(rec.Body); err != nil {
			t.Fatal(err)
		}
	}
}
