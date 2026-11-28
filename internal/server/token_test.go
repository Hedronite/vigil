package server

import (
	"path/filepath"
	"testing"
)

func TestReadTokenPathEnvOverridesLocator(t *testing.T) {
	_, err := ReadTokenPath("")
	if err != ErrTokenMissing {
		t.Fatalf("empty locator = %v, want ErrTokenMissing", err)
	}
	want := filepath.Join(t.TempDir(), "t")
	t.Setenv(readTokenEnv, want)
	got, err := ReadTokenPath("/ignored/path")
	if err != nil || got != want {
		t.Fatalf("override = %q %v, want %q", got, err, want)
	}
}

func TestLoadReadTokenHashMissing(t *testing.T) {
	_, err := LoadReadTokenHash(filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadSignalTokenHash(t *testing.T) {
	_, err := LoadSignalTokenHash("")
	if err != ErrTokenMissing {
		t.Fatalf("missing = %v", err)
	}
	okPath := tokenFile(t, "secret", 0o600)
	t.Setenv(signalTokenEnv, okPath)
	if _, err := LoadSignalTokenHash("ignored"); err != nil {
		t.Fatalf("load = %v", err)
	}
	badPath := tokenFile(t, "secret", 0o644)
	t.Setenv(signalTokenEnv, badPath)
	_, err = LoadSignalTokenHash("ignored")
	if err != ErrTokenMode {
		t.Fatalf("mode = %v", err)
	}
}

func TestSignalTokenPathEnvAndMissing(t *testing.T) {
	_, err := SignalTokenPath("")
	if err != ErrTokenMissing {
		t.Fatalf("empty = %v, want ErrTokenMissing", err)
	}
	want := filepath.Join(t.TempDir(), "s")
	t.Setenv(signalTokenEnv, want)
	got, err := SignalTokenPath("/ignored")
	if err != nil || got != want {
		t.Fatalf("env = %q %v, want %q", got, err, want)
	}
}
