package server

import (
	"context"
	"net"
	"sync"
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
	panicked := make(chan any, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go func() {
		defer func() { panicked <- recover() }()
		pruneSignalsEvery(ctx, nil, func() time.Time { return time.Now().UTC() }, 15*time.Millisecond)
	}()
	<-ctx.Done()
	time.Sleep(30 * time.Millisecond)
	if p := <-panicked; p != nil {
		t.Fatalf("panic: %v", p)
	}
}

func TestPruneSignalsDropsExpiredOnTick(t *testing.T) {
	frozen := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	var nowMu sync.Mutex
	cur := frozen
	nowFn := func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return cur
	}

	store := signals.NewStore()
	if _, err := store.Accept(signals.PostFields{
		Source: "a", Kind: "b", Severity: "info", TTL: 60,
	}, frozen); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		pruneSignalsEvery(ctx, store, nowFn, 15*time.Millisecond)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	nowMu.Lock()
	cur = frozen.Add(61 * time.Second)
	nowMu.Unlock()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if store.LiveSlotCount() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("signal still live after prune ticks")
}

func TestRunWithoutSignalsStoreStillBinds(t *testing.T) {
	addr := loopbackAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, Options{
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
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("listener did not open")
}

func TestRunPrunesExpiredWhenSignalsPresent(t *testing.T) {
	frozen := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	var nowMu sync.Mutex
	cur := frozen
	nowFn := func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return cur
	}

	store := signals.NewStore()
	if _, err := store.Accept(signals.PostFields{
		Source: "a", Kind: "b", Severity: "info", TTL: 60,
	}, frozen); err != nil {
		t.Fatal(err)
	}

	addr := loopbackAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, Options{
			Listen: addr, ReadToken: tokenHash(t, "r"), SignalToken: tokenHash(t, "s"),
			Build: staticDoc(t), Signals: store, Limiter: &signals.Limiter{},
			Now: nowFn, PruneEvery: 15 * time.Millisecond,
		})
	}()
	dialDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(dialDeadline) {
		c, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			if closeErr := c.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	nowMu.Lock()
	cur = frozen.Add(61 * time.Second)
	nowMu.Unlock()
	if store.LiveSlotCount() == 0 {
		t.Fatal("expired before prune tick")
	}
	waitUntil := time.Now().Add(2 * time.Second)
	for time.Now().Before(waitUntil) {
		if store.LiveSlotCount() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Run did not prune expired signal")
}
