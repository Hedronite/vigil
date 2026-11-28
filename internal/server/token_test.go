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
