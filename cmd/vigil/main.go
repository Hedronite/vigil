// Command vigil is the Go ops query surface. V1 added the backend commands:
// prom targets, prom query, grafana health and sources. V2 added simplex
// summary, which reads an authority's document. The listener arrives in V3.
package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"

	"github.com/Hedronite/vigil/internal/exitcode"
)

// Set at link time with -ldflags "-X main.version=... -X main.commit=...".
var (
	version = "v0-dev"
	commit  = ""
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := runRecovered(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// runRecovered turns a panic into exit 6 so a crash never reads as a verdict.
func runRecovered(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return runRecoveredWith(func() int { return run(ctx, args, stdout, stderr, liveHandlers()) }, stderr)
}

// runRecoveredWith runs f and, if it panics, prints one vigil.error/v0 line
// with the internal code. The panic value is dropped: it could carry an
// address.
func runRecoveredWith(f func() int, stderr io.Writer) (code int) {
	defer func() {
		if r := recover(); r != nil {
			writeError(stderr, exitcode.New(exitcode.Internal, "internal"))
			code = exitcode.Internal
		}
	}()
	return f()
}

func versionLine() string {
	c := commit
	if c == "" {
		c = vcsRevision()
	}
	if c == "" {
		c = "unknown"
	}
	return "vigil " + version + " (commit " + c + ", " + runtime.Version() + ")"
}

func vcsRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			if len(s.Value) > 12 {
				return s.Value[:12]
			}
			return s.Value
		}
	}
	return ""
}
