package main

import (
	"context"
	"errors"
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
	if cfg.Tokens == nil || cfg.Tokens.ReadFile == "" {
		return exitcode.Newf(exitcode.Startup, "missing_read_token", "the read token file is not configured")
	}
	tokenPath, err := server.ReadTokenPath(cfg.Tokens.ReadFile)
	if err != nil {
		return readTokenStartup(err)
	}
	readHash, err := server.LoadReadTokenHash(tokenPath)
	if err != nil {
		return readTokenStartup(err)
	}
	if cfg.Tokens == nil || cfg.Tokens.SignalFile == "" {
		return exitcode.Newf(exitcode.Startup, "missing_signal_token", "the signal token file is not configured")
	}
	sigPath, err := server.SignalTokenPath(cfg.Tokens.SignalFile)
	if err != nil {
		return signalTokenStartup(err)
	}
	sigHash, err := server.LoadReadTokenHash(sigPath)
	if err != nil {
		return signalTokenStartup(err)
	}
	if err := server.CheckBind(cfg.Listen, allowNonLoopback); err != nil {
		return exitcode.Newf(exitcode.Startup, "non_loopback_listen", err.Error())
	}
	st := server.NewState()
	started := time.Now().UTC()
	clock := func() time.Time {
		if !inv.Now.IsZero() {
			return inv.Now.UTC()
		}
		return time.Now().UTC()
	}
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

func readTokenStartup(err error) error {
	if errors.Is(err, server.ErrTokenMissing) {
		return exitcode.Newf(exitcode.Startup, "missing_read_token", "the read token file is not configured")
	}
	return exitcode.Newf(exitcode.Startup, "token_unusable", "the read token file cannot be used")
}

func signalTokenStartup(err error) error {
	if errors.Is(err, server.ErrTokenMissing) {
		return exitcode.Newf(exitcode.Startup, "missing_signal_token", "the signal token file is not configured")
	}
	return exitcode.Newf(exitcode.Startup, "token_unusable", "the signal token file cannot be used")
}
