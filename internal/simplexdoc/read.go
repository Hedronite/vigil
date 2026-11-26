// Package simplexdoc reads the document Simplex publishes, simplex.probe/v0,
// from the file the config names. Vigil never runs Simplex and never dials a
// host: the file is the whole transport.
//
// A document is either used as published or not at all. The package takes the
// vantage, the colours and the reason codes as written and derives nothing from
// the checks behind them. Anything it cannot read, or cannot trust to be what
// the contract says, is an UnavailableError carrying the detail code a source
// row reports.
package simplexdoc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/Hedronite/vigil/internal/reasons"
)

// MaxBytes is the largest document that is read.
const MaxBytes = 4 << 20

var envName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,63}$`)

// UnavailableError says that the document cannot be used and why, as the
// detail code of a source row: source_unavailable or schema_major. Cause is for
// diagnosis and may name a path, so it is not for printing to a caller.
type UnavailableError struct {
	DetailCode string
	Cause      error
}

func (e *UnavailableError) Error() string {
	return "simplex document " + e.DetailCode + ": " + e.Cause.Error()
}

// Unwrap exposes the cause, so errors.Is can ask whether the file was absent.
func (e *UnavailableError) Unwrap() error { return e.Cause }

func unavailable(cause error) error {
	return &UnavailableError{DetailCode: reasons.SourceUnavailable, Cause: cause}
}

// Observation is one sighting of a usable document.
type Observation struct {
	Document   *Document
	ReceivedAt time.Time
}

// Read resolves pathRef, reads the file and decodes it. now is the caller's
// clock and bounds ReceivedAt. The error is an *UnavailableError unless the
// context ended, which says nothing about the source.
func Read(ctx context.Context, pathRef string, now time.Time) (*Observation, error) {
	path, err := ResolvePath(pathRef)
	if err != nil {
		return nil, err
	}
	body, modified, err := ReadFile(ctx, path)
	if err != nil {
		return nil, err
	}
	doc, err := Decode(body)
	if err != nil {
		return nil, err
	}
	return &Observation{Document: doc, ReceivedAt: ReceivedAt(modified, now)}, nil
}

// ReceivedAt is the sighting time of a read that has no memory of earlier ones:
// the file's modification time, never later than now, in UTC. A file stamped in
// the future therefore gains no extra life.
func ReceivedAt(modified, now time.Time) time.Time {
	if modified.After(now) {
		return now.UTC()
	}
	return modified.UTC()
}

// ResolvePath turns a path_ref into the file to read: an absolute path, or
// env:NAME whose value is one. A relative path, a URL and a "." or ".."
// segment are refused, whether written in the config or taken from the
// environment.
func ResolvePath(pathRef string) (string, error) {
	path := pathRef
	if name, indirect := strings.CutPrefix(pathRef, "env:"); indirect {
		value, err := lookup(name)
		if err != nil {
			return "", unavailable(err)
		}
		path = value
	}
	if !filepath.IsAbs(path) {
		return "", unavailable(errors.New("path_ref does not name an absolute path"))
	}
	segments := strings.Split(path, "/")
	if slices.Contains(segments, ".") || slices.Contains(segments, "..") {
		return "", unavailable(errors.New("path has a dot segment"))
	}
	return path, nil
}

func lookup(name string) (string, error) {
	if !envName.MatchString(name) {
		return "", errors.New("env: wants an upper-case variable name")
	}
	value, set := os.LookupEnv(name)
	if !set {
		return "", fmt.Errorf("variable %s is not set", name)
	}
	return value, nil
}

// ReadFile returns the bytes and modification time of a regular file of at most
// MaxBytes. Both come from the one open file, so a writer that renames a new
// document into place cannot pair one file's bytes with another's time.
//
// The open does not block: a named pipe with no writer would otherwise hang the
// caller before it could be refused.
func ReadFile(ctx context.Context, path string) (body []byte, modified time.Time, err error) {
	if err := ctx.Err(); err != nil {
		return nil, time.Time{}, fmt.Errorf("read simplex document: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, time.Time{}, unavailable(err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil && err == nil {
			body, modified, err = nil, time.Time{}, unavailable(closeErr)
		}
	}()
	info, err := f.Stat()
	if err != nil {
		return nil, time.Time{}, unavailable(err)
	}
	body, err = readRegular(f, info.Mode())
	if err != nil {
		return nil, time.Time{}, unavailable(err)
	}
	return body, info.ModTime(), nil
}

// readRegular reads one byte past the cap, so that an oversize file is told
// apart from one that fills it.
func readRegular(r io.Reader, mode fs.FileMode) ([]byte, error) {
	if !mode.IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", mode.Type())
	}
	body, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if len(body) > MaxBytes {
		return nil, fmt.Errorf("larger than %d bytes", MaxBytes)
	}
	return body, nil
}
