package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Hedronite/vigil/internal/simplexdoc"
)

const (
	readTokenEnv      = "VIGIL_READ_TOKEN_FILE"
	signalTokenEnv    = "VIGIL_SIGNAL_TOKEN_FILE"
	testimonyTokenEnv = "VIGIL_TESTIMONY_TOKEN_FILE"
)

var (
	// ErrTokenMissing means no read token path was configured.
	ErrTokenMissing = errors.New("read token path is not configured")
	// ErrTokenMode means the token file is readable by others.
	ErrTokenMode = errors.New("token file permissions are too loose")
	// ErrTokenEmpty means the token file has no secret.
	ErrTokenEmpty = errors.New("token file is empty")
)

// SignalTokenPath resolves the signal-token file path from config and environment.
func SignalTokenPath(locator string) (string, error) {
	if path := strings.TrimSpace(os.Getenv(signalTokenEnv)); path != "" {
		return path, nil
	}
	if locator == "" {
		return "", ErrTokenMissing
	}
	return simplexdoc.ResolvePath(locator)
}

// ReadTokenPath resolves the read-token file path from config and environment.
func ReadTokenPath(locator string) (string, error) {
	if path := strings.TrimSpace(os.Getenv(readTokenEnv)); path != "" {
		return path, nil
	}
	if locator == "" {
		return "", ErrTokenMissing
	}
	return simplexdoc.ResolvePath(locator)
}

// TestimonyTokenPath resolves the testimony-token file path from config and environment.
func TestimonyTokenPath(locator string) (string, error) {
	if path := strings.TrimSpace(os.Getenv(testimonyTokenEnv)); path != "" {
		return path, nil
	}
	if locator == "" {
		return "", ErrTokenMissing
	}
	return simplexdoc.ResolvePath(locator)
}

// LoadTestimonyTokenHash resolves the testimony token locator and loads its hash.
func LoadTestimonyTokenHash(locator string) ([32]byte, error) {
	path, err := TestimonyTokenPath(locator)
	if err != nil {
		return [32]byte{}, err
	}
	return LoadReadTokenHash(path)
}

// LoadSignalTokenHash resolves the signal token locator and loads its hash.
func LoadSignalTokenHash(locator string) ([32]byte, error) {
	path, err := SignalTokenPath(locator)
	if err != nil {
		return [32]byte{}, err
	}
	return LoadReadTokenHash(path)
}

// LoadReadTokenHash reads the token file and returns the SHA-256 of its content.
func LoadReadTokenHash(path string) ([32]byte, error) {
	var zero [32]byte
	info, err := os.Stat(path)
	if err != nil {
		return zero, fmt.Errorf("read token: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return zero, ErrTokenMode
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return zero, fmt.Errorf("read token: %w", err)
	}
	raw = bytesTrimSpace(raw)
	if len(raw) == 0 {
		return zero, ErrTokenEmpty
	}
	return sha256.Sum256(raw), nil
}

func bytesTrimSpace(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}

func tokenAuthorized(presented []byte, want [32]byte) bool {
	if len(presented) == 0 {
		return false
	}
	got := sha256.Sum256(presented)
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}

func bearerToken(h string) []byte {
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return nil
	}
	return []byte(strings.TrimSpace(h[len(prefix):]))
}
