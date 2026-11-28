package main

import (
	"errors"

	"github.com/Hedronite/vigil/internal/config"
	"github.com/Hedronite/vigil/internal/exitcode"
	"github.com/Hedronite/vigil/internal/server"
)

func loadReadTokenHash(cfg *config.Config) ([32]byte, error) {
	if cfg.Tokens == nil || cfg.Tokens.ReadFile == "" {
		return [32]byte{}, exitcode.Newf(exitcode.Startup, "missing_read_token", "the read token file is not configured")
	}
	path, err := server.ReadTokenPath(cfg.Tokens.ReadFile)
	if err != nil {
		return [32]byte{}, readTokenStartup(err)
	}
	return server.LoadReadTokenHash(path)
}

func loadSignalTokenHash(cfg *config.Config) ([32]byte, error) {
	if cfg.Tokens == nil || cfg.Tokens.SignalFile == "" {
		return [32]byte{}, exitcode.Newf(exitcode.Startup, "missing_signal_token", "the signal token file is not configured")
	}
	path, err := server.SignalTokenPath(cfg.Tokens.SignalFile)
	if err != nil {
		return [32]byte{}, signalTokenStartup(err)
	}
	return server.LoadReadTokenHash(path)
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
