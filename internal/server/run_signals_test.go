package server

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Hedronite/vigil/internal/signals"
)

func TestStartSignalPruneNil(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	startSignalPrune(ctx, Options{})
}

func TestStartSignalPruneRuns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	now := func() time.Time { return time.Now().UTC() }
	startSignalPrune(ctx, Options{Signals: signals.NewStore(now), Now: now})
	cancel()
	time.Sleep(10 * time.Millisecond)
}

func TestRunWithoutSignals(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Listen: addr, ReadToken: tokenHash(t, "r"), SignalToken: tokenHash(t, "s"),
			Build: staticDoc(t), Limiter: &signals.Limiter{},
		})
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			if closeErr := c.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			cancel()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
}
