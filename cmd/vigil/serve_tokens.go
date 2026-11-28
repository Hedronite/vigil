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
	hash, err := server.LoadReadTokenHash(path)
	if err != nil {
		return [32]byte{}, readTokenStartup(err)
	}
	return hash, nil
}

func loadServeTokenHashes(cfg *config.Config) ([32]byte, [32]byte, error) {
	read, err := loadReadTokenHash(cfg)
	if err != nil {
		return [32]byte{}, [32]byte{}, err
	}
	if cfg.Tokens == nil {
		return read, [32]byte{}, exitcode.Newf(exitcode.Startup, "missing_signal_token", "the signal token file is not configured")
	}
	if cfg.Tokens.SignalFile == "" {
		return read, [32]byte{}, exitcode.Newf(exitcode.Startup, "missing_signal_token", "the signal token file is not configured")
	}
	sig, err := server.LoadSignalTokenHash(cfg.Tokens.SignalFile)
	if err != nil {
		return read, [32]byte{}, signalTokenStartup(err)
	}
	var zero [32]byte
	if sig == zero {
		return read, [32]byte{}, exitcode.Newf(exitcode.Startup, "token_unusable", "the signal token file cannot be used")
	}
	return read, sig, nil
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
