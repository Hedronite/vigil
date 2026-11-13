// Command vigil is the Go ops query surface. At V0 it carries only the
// version stub; the Prometheus, Simplex and listener commands arrive in V1 to
// V3.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"

	"github.com/Hedronite/vigil/internal/exitcode"
)

// Set at link time with -ldflags "-X main.version=... -X main.commit=...".
var (
	version = "v0-dev"
	commit  = ""
)

func main() {
	os.Exit(runRecovered(os.Args[1:], os.Stdout, os.Stderr))
}

// runRecovered turns a panic into exit 6 so a crash never reads as a verdict.
func runRecovered(args []string, stdout, stderr io.Writer) int {
	return runRecoveredWith(func() int { return run(args, stdout, stderr) }, stderr)
}

func runRecoveredWith(f func() int, stderr io.Writer) (code int) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(stderr, "vigil: internal error: %v\n", r)
			code = exitcode.Internal
		}
	}()
	return f()
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "version" {
		fmt.Fprintln(stdout, versionLine())
		return exitcode.Green
	}
	fmt.Fprintln(stderr, "usage: vigil version")
	return exitcode.Usage
}

func versionLine() string {
	c := commit
	if c == "" {
		c = vcsRevision()
	}
	if c == "" {
		c = "unknown"
	}
	return fmt.Sprintf("vigil %s (commit %s, %s)", version, c, runtime.Version())
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
