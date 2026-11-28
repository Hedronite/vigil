package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Hedronite/vigil/internal/config"
	"github.com/Hedronite/vigil/internal/exitcode"
	"github.com/Hedronite/vigil/internal/server"
)

func TestLoadServeTokenHashesRejectsMissingSignal(t *testing.T) {
	dir := t.TempDir()
	readPath := filepath.Join(dir, "read.token")
	if err := os.WriteFile(readPath, []byte("r"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIGIL_READ_TOKEN_FILE", readPath)
	cfg := &config.Config{Tokens: &config.Tokens{
		ReadFile: "env:VIGIL_READ_TOKEN_FILE", SignalFile: "",
	}}
	_, _, err := loadServeTokenHashes(cfg)
	if ee, ok := exitcode.As(err); !ok || ee.Code != "missing_signal_token" {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadServeTokenHashesRejectsUnusableSignal(t *testing.T) {
	dir := t.TempDir()
	readPath := filepath.Join(dir, "read.token")
	sigPath := filepath.Join(dir, "signal.token")
	if err := os.WriteFile(readPath, []byte("r"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sigPath, []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIGIL_READ_TOKEN_FILE", readPath)
	t.Setenv("VIGIL_SIGNAL_TOKEN_FILE", sigPath)
	cfg := &config.Config{Tokens: &config.Tokens{
		ReadFile: "env:VIGIL_READ_TOKEN_FILE", SignalFile: "env:VIGIL_SIGNAL_TOKEN_FILE",
	}}
	_, sig, err := loadServeTokenHashes(cfg)
	if err == nil {
		t.Fatalf("unexpected sig %x", sig)
	}
	if ee, ok := exitcode.As(err); !ok || ee.Code != "token_unusable" {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadServeTokenHashesRequiresNonemptySignalFile(t *testing.T) {
	dir := t.TempDir()
	readPath := filepath.Join(dir, "read.token")
	if err := os.WriteFile(readPath, []byte("r"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIGIL_READ_TOKEN_FILE", readPath)
	cfg := &config.Config{Tokens: &config.Tokens{
		ReadFile: "env:VIGIL_READ_TOKEN_FILE", SignalFile: "",
	}}
	read, sig, err := loadServeTokenHashes(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	var zero [32]byte
	if read == zero || sig != zero {
		t.Fatalf("read=%x sig=%x", read, sig)
	}
	if ee, ok := exitcode.As(err); !ok || ee.Code != "missing_signal_token" {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadTestimonyTokenHashSkipsWhenNoPush(t *testing.T) {
	cfg := &config.Config{Tokens: &config.Tokens{TestimonyFile: ""}}
	h, err := loadTestimonyTokenHash(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var zero [32]byte
	if h != zero {
		t.Fatal("expected zero hash without push")
	}
}

func TestLoadTestimonyTokenHashRejectsMissingWhenPush(t *testing.T) {
	cfg := &config.Config{
		Authorities: []config.Authority{{ID: "simplex", Via: "push"}},
	}
	_, err := loadTestimonyTokenHash(cfg)
	if ee, ok := exitcode.As(err); !ok || ee.Code != "missing_testimony_token" {
		t.Fatalf("nil tokens err = %v", err)
	}
	cfg = &config.Config{
		Authorities: []config.Authority{{ID: "simplex", Via: "push"}},
		Tokens:      &config.Tokens{TestimonyFile: ""},
	}
	_, err = loadTestimonyTokenHash(cfg)
	if ee, ok := exitcode.As(err); !ok || ee.Code != "missing_testimony_token" {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadTestimonyTokenHashLoadsWhenPush(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "testimony.token")
	if err := os.WriteFile(path, []byte("witness"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIGIL_TESTIMONY_TOKEN_FILE", path)
	cfg := &config.Config{
		Authorities: []config.Authority{{ID: "simplex", Via: "push"}},
		Tokens:      &config.Tokens{TestimonyFile: "env:VIGIL_TESTIMONY_TOKEN_FILE"},
	}
	h, err := loadTestimonyTokenHash(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want, err := server.LoadTestimonyTokenHash("env:VIGIL_TESTIMONY_TOKEN_FILE")
	if err != nil || h != want {
		t.Fatalf("hash mismatch")
	}
}

func TestLoadServeTokenHashesLoadsGoodTokens(t *testing.T) {
	dir := t.TempDir()
	readPath := filepath.Join(dir, "read.token")
	sigPath := filepath.Join(dir, "signal.token")
	if err := os.WriteFile(readPath, []byte("read-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sigPath, []byte("sig-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIGIL_READ_TOKEN_FILE", readPath)
	t.Setenv("VIGIL_SIGNAL_TOKEN_FILE", sigPath)
	cfg := &config.Config{Tokens: &config.Tokens{
		ReadFile: "env:VIGIL_READ_TOKEN_FILE", SignalFile: "env:VIGIL_SIGNAL_TOKEN_FILE",
	}}
	_, sig, err := loadServeTokenHashes(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want, err := server.LoadSignalTokenHash("env:VIGIL_SIGNAL_TOKEN_FILE")
	if err != nil {
		t.Fatal(err)
	}
	if sig != want {
		t.Fatalf("sig mismatch")
	}
	var zero [32]byte
	if sig == zero {
		t.Fatal("zero hash")
	}
}
