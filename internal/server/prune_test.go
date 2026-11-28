package server

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hedronite/vigil/internal/signals"
)

func TestPruneSignalsNilStoreExitsCleanly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pruneSignals(ctx, nil, func() time.Time { return time.Now().UTC() })
}

func TestPruneSignalsNilStoreTickNoPanic(t *testing.T) {
	old := signalPruneInterval
	signalPruneInterval = 15 * time.Millisecond
	defer func() { signalPruneInterval = old }()

	panicked := make(chan any, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go func() {
		defer func() { panicked <- recover() }()
		pruneSignals(ctx, nil, func() time.Time { return time.Now().UTC() })
	}()
	<-ctx.Done()
	time.Sleep(30 * time.Millisecond)
	if p := <-panicked; p != nil {
		t.Fatalf("panic: %v", p)
	}
}

func TestPruneSignalsDropsExpiredOnTick(t *testing.T) {
	old := signalPruneInterval
	signalPruneInterval = 15 * time.Millisecond
	defer func() { signalPruneInterval = old }()

	frozen := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	var now atomic.Value
	now.Store(frozen)
	nowFn := func() time.Time { return now.Load().(time.Time) }

	store := signals.NewStore()
	if _, err := store.Accept(signals.PostFields{
		Source: "a", Kind: "b", Severity: "info", TTL: 60,
	}, frozen); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pruneSignals(ctx, store, nowFn)
	now.Store(frozen.Add(61 * time.Second))
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if store.LenLive(nowFn()) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("signal still live after prune ticks")
}

func TestRunStartsPruneWhenSignalsPresent(t *testing.T) {
	old := signalPruneInterval
	signalPruneInterval = 15 * time.Millisecond
	defer func() { signalPruneInterval = old }()

	frozen := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	var now atomic.Value
	now.Store(frozen)
	nowFn := func() time.Time { return now.Load().(time.Time) }

	store := signals.NewStore()
	if _, err := store.Accept(signals.PostFields{
		Source: "a", Kind: "b", Severity: "info", TTL: 60,
	}, frozen); err != nil {
		t.Fatal(err)
	}

	addr := loopbackAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = Run(ctx, Options{
			Listen: addr, ReadToken: tokenHash(t, "r"), SignalToken: tokenHash(t, "s"),
			Build: staticDoc(t), Signals: store, Limiter: &signals.Limiter{}, Now: nowFn,
		})
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			_ = c.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	now.Store(frozen.Add(61 * time.Second))
	waitUntil := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(waitUntil) {
		if store.LenLive(nowFn()) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Run did not prune expired signal")
}
