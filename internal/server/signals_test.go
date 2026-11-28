package server

import (
	"bytes"
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
	store := signals.NewStore(func() time.Time { return now })
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
	if !strings.Contains(rec.Body.String(), "body_cap") {
		t.Fatalf("body %q", rec.Body.String())
	}
}

func TestPostSignalRateLimited(t *testing.T) {
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	store := signals.NewStore(func() time.Time { return now })
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
			if !strings.Contains(rec.Body.String(), "rate_limited") {
				t.Fatalf("body %q", rec.Body.String())
			}
		}
		if _, err := io.ReadAll(rec.Body); err != nil {
			t.Fatal(err)
		}
	}
}
