package main

import (
	"context"
	"errors"
	"time"

	"github.com/Hedronite/vigil/internal/exitcode"
	"github.com/Hedronite/vigil/internal/server"
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
		return tokenStartup(err)
	}
	hash, err := server.LoadReadTokenHash(tokenPath)
	if err != nil {
		return tokenStartup(err)
	}
	if err := server.CheckBind(cfg.Listen, allowNonLoopback); err != nil {
		return exitcode.Newf(exitcode.Startup, "non_loopback_listen", err.Error())
	}
	st := server.NewState()
	started := time.Now().UTC()
	build := func(callCtx context.Context, at time.Time) (*summary.Document, error) {
		t := at.UTC()
		if !inv.Now.IsZero() {
			t = inv.Now.UTC()
		}
		return serveSummary(callCtx, cfg, st, started, t)
	}
	if err := server.Run(ctx, server.Options{
		Listen: cfg.Listen, ReadToken: hash, AllowNonLoopback: allowNonLoopback, Build: build,
	}); err != nil {
		return exitcode.Newf(exitcode.Startup, "bind_failed", "the listener could not bind at "+cfg.Listen)
	}
	return nil
}

func tokenStartup(err error) error {
	switch {
	case errors.Is(err, server.ErrTokenMissing):
		return exitcode.Newf(exitcode.Startup, "missing_read_token", "the read token file is not configured")
	case errors.Is(err, server.ErrTokenMode), errors.Is(err, server.ErrTokenEmpty):
		return exitcode.Newf(exitcode.Startup, "token_unusable", "the read token file cannot be used")
	default:
		return exitcode.Newf(exitcode.Startup, "token_unusable", "the read token file cannot be used")
	}
}
