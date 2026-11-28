package main

import (
	"context"
	"time"

	"github.com/Hedronite/vigil/internal/exitcode"
	"github.com/Hedronite/vigil/internal/server"
	"github.com/Hedronite/vigil/internal/signals"
	"github.com/Hedronite/vigil/internal/summary"
)

func runServe(ctx context.Context, inv Invocation, allowNonLoopback bool) error {
	cfg, err := loadSummaryConfig(inv)
	if err != nil {
		return err
	}
	if inv.OverlayPath != "" {
		if _, err := loadOverlay(inv.OverlayPath, cfg); err != nil {
			return err
		}
	}
	readHash, err := loadReadTokenHash(cfg)
	if err != nil {
		return err
	}
	sigHash, err := loadSignalTokenHash(cfg)
	if err != nil {
		return err
	}
	if err := server.CheckBind(cfg.Listen, allowNonLoopback); err != nil {
		return exitcode.Newf(exitcode.Startup, "non_loopback_listen", err.Error())
	}
	st := server.NewState()
	started := time.Now().UTC()
	clock := serveClock(inv)
	build := func(callCtx context.Context, at time.Time) (*summary.Document, error) {
		t := at.UTC()
		if !inv.Now.IsZero() {
			t = inv.Now.UTC()
		}
		return serveSummary(callCtx, cfg, st, started, t)
	}
	ring := signals.NewStore(clock)
	if err := server.Run(ctx, server.Options{
		Listen: cfg.Listen, ReadToken: readHash, SignalToken: sigHash,
		AllowNonLoopback: allowNonLoopback, Build: build,
		Signals: ring, Limiter: &signals.Limiter{}, Now: clock,
	}); err != nil {
		return exitcode.Newf(exitcode.Startup, "bind_failed", "the listener could not bind at "+cfg.Listen)
	}
	return nil
}

func serveClock(inv Invocation) func() time.Time {
	return func() time.Time {
		if !inv.Now.IsZero() {
			return inv.Now.UTC()
		}
		return time.Now().UTC()
	}
}
