package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Hedronite/vigil/internal/exitcode"
)

// Invocation is what a command handler receives. Handlers write the document
// to Stdout and return nil, or return an error and write nothing: the command
// layer discards Stdout on any failure, so a failed command prints no stdout.
// A summary's verdict is not a failure: see verdictExit.
type Invocation struct {
	// Name is the query name for prom query; empty for the other commands.
	Name string
	// Host is the --host slug for prom query; empty when absent.
	Host string
	// ConfigPath and OverlayPath are the --config and --overlay values.
	ConfigPath  string
	OverlayPath string
	// JSON asks for the document alone instead of the human table.
	JSON bool
	// Now is the pinned clock from --now; the zero time means use the real one.
	Now time.Time
	// Stdout collects the output; it is flushed only if the handler succeeds.
	Stdout io.Writer
}

// Handler runs one command. A returned *exitcode.Error picks the exit code and
// the machine code; any other error is an internal failure.
type Handler func(ctx context.Context, inv Invocation) error

// Handlers is the table of command bodies. The command layer owns parsing,
// validation and error output; each body owns one backend or document.
type Handlers struct {
	PromTargets    Handler
	PromQuery      Handler
	GrafanaHealth  Handler
	Sources        Handler
	Validate       Handler
	SimplexSummary Handler
}

// notImplemented is the body of a command whose backend has not landed yet.
func notImplemented(context.Context, Invocation) error {
	return exitcode.Newf(exitcode.Internal, "not_implemented", "this command is not wired yet")
}

func defaultHandlers() Handlers {
	return Handlers{
		PromTargets:    notImplemented,
		PromQuery:      notImplemented,
		GrafanaHealth:  notImplemented,
		Sources:        notImplemented,
		Validate:       notImplemented,
		SimplexSummary: notImplemented,
	}
}

// Shapes of the values commands accept. Both match the config schema, so a
// value that cannot be a configured name never gets past the parser.
var (
	queryNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)
	hostSlugPattern  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
)

// run builds the command tree for one invocation, runs it and returns the
// exit code. Every failure prints exactly one vigil.error/v0 line on stderr
// and nothing on stdout. A verdict code comes only with its document.
func run(ctx context.Context, args []string, stdout, stderr io.Writer, h Handlers) int {
	var out bytes.Buffer
	root := newRoot(&out, h)
	root.SetArgs(args)
	root.SetOut(stdout) // help text only: --help is a success
	root.SetErr(io.Discard)
	root.SilenceErrors = true
	root.SilenceUsage = true

	err := root.ExecuteContext(ctx)
	var verdict verdictExit // zero is GREEN, which is also what no error means
	if err == nil || errors.As(err, &verdict) {
		return flush(stdout, stderr, out.Bytes(), int(verdict))
	}
	ee, ok := exitcode.As(err)
	if !ok {
		ee = classifyUsage(err)
	}
	writeError(stderr, ee)
	return ee.Exit
}

// flush writes the held output and returns code. A document that did not reach
// stdout is not a verdict.
func flush(stdout, stderr io.Writer, out []byte, code int) int {
	if _, err := stdout.Write(out); err != nil {
		writeError(stderr, exitcode.New(exitcode.Internal, "internal"))
		return exitcode.Internal
	}
	return code
}

// handlerError keeps what a handler may answer with: a verdict or a coded
// failure. Text from any other error could carry an address.
func handlerError(err error) error {
	var verdict verdictExit
	if err == nil || errors.As(err, &verdict) {
		return err
	}
	if coded, ok := exitcode.As(err); ok {
		return coded
	}
	return exitcode.New(exitcode.Internal, "internal")
}

// newRoot builds the tree. out collects handler output.
func newRoot(out *bytes.Buffer, h Handlers) *cobra.Command {
	var (
		config, overlay, now string
		asJSON               bool
		allowNonLoopback     bool
	)
	inv := func(name, host string) (Invocation, error) {
		i := Invocation{
			Name: name, Host: host, ConfigPath: config, OverlayPath: overlay,
			JSON: asJSON, Stdout: out,
		}
		for _, p := range []string{config, overlay} {
			if p != "" && !plainPath(p) {
				return i, usage("a path is required here, not a URL")
			}
		}
		if now != "" {
			t, err := time.Parse(time.RFC3339, now)
			if err != nil {
				return i, usage("--now must be an RFC 3339 time")
			}
			i.Now = t
		}
		return i, nil
	}

	root := &cobra.Command{
		Use:   "vigil",
		Short: "Vigil: one query surface over the mesh's named backends",
		Long: "Vigil reads named backends (Prometheus, Grafana) and authority documents\n" +
			"through names in its config. No command takes a URL, a selector or PromQL.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error { return usage("a command is required") },
		// Completion is not part of the surface.
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	f := root.PersistentFlags()
	f.StringVar(&config, "config", "", "path to the vigil.config/v0 file")
	f.StringVar(&overlay, "overlay", "", "path to the vigil.overlay/v0 file (backend addresses)")
	f.BoolVar(&asJSON, "json", false, "print the document alone on stdout")
	f.StringVar(&now, "now", "", "pin the clock to an RFC 3339 time (tests and deterministic renders)")

	run := func(handler Handler, name func(args []string) string, host *string) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, args []string) error {
			n, hv := "", ""
			if name != nil {
				n = name(args)
			}
			if host != nil {
				hv = *host
			}
			i, err := inv(n, hv)
			if err != nil {
				return err
			}
			return handlerError(handler(cmd.Context(), i))
		}
	}

	version := &cobra.Command{
		Use:   "version",
		Short: "Print the version, the commit and the Go runtime",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			_, _ = fmt.Fprintln(out, versionLine())
			return nil
		},
	}

	prom := group("prom", "Query the named Prometheus backend")
	targets := &cobra.Command{
		Use:   "targets",
		Short: "List scrape targets: host slug, health, last scrape",
		Args:  cobra.NoArgs,
		RunE:  run(h.PromTargets, nil, nil),
	}
	var host string
	query := &cobra.Command{
		Use:   "query <name>",
		Short: "Run one named query from the config",
		Long: "Run one named query from the config. The name is the only handle:\n" +
			"there is no free-form PromQL. --host fills the {{.host}} placeholder.",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usage("a query name is required")
			}
			if !queryNamePattern.MatchString(args[0]) {
				return usage("the query name is not a configured name")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if host != "" && !hostSlugPattern.MatchString(host) {
				return usage("--host must be a host slug")
			}
			return run(h.PromQuery, func(a []string) string { return a[0] }, &host)(cmd, args)
		},
	}
	query.Flags().StringVar(&host, "host", "", "host slug for the {{.host}} placeholder")
	prom.AddCommand(targets, query)

	grafana := group("grafana", "Check the named Grafana backend")
	health := &cobra.Command{
		Use:   "health",
		Short: "Call the Grafana health endpoint (a source, never a verdict)",
		Args:  cobra.NoArgs,
		RunE:  run(h.GrafanaHealth, nil, nil),
	}
	grafana.AddCommand(health)

	sources := &cobra.Command{
		Use:   "sources",
		Short: "List the configured backends and their reachability",
		Args:  cobra.NoArgs,
		RunE:  run(h.Sources, nil, nil),
	}

	validate := &cobra.Command{
		Use:   "validate",
		Short: "Validate config and optional overlay without probing",
		Args:  cobra.NoArgs,
		RunE:  run(h.Validate, nil, nil),
	}

	root.AddCommand(version, prom, grafana, sources, validate, simplexCommand(run(h.SimplexSummary, nil, nil)))
	addServeCommand(root, inv, &allowNonLoopback)
	return root
}

// group is a command that only holds subcommands.
func group(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE:  func(*cobra.Command, []string) error { return usage("a subcommand is required") },
	}
}

// usage is a usage error with fixed message text.
func usage(msg string) *exitcode.Error { return exitcode.Newf(exitcode.Usage, "usage", msg) }

// plainPath reports whether s looks like a file path and not a URL: no scheme
// separator and no characters a URL needs.
func plainPath(s string) bool {
	return !strings.Contains(s, "://") && !strings.ContainsAny(s, "?#\x00\r\n")
}

// classifyUsage turns a cobra parse error into a usage error. The message is
// chosen from fixed text, never copied from the error: cobra echoes the
// argument, and an argument can be a URL.
func classifyUsage(err error) *exitcode.Error {
	s := err.Error()
	switch {
	case strings.HasPrefix(s, "unknown command"):
		return usage("unknown command; see vigil --help")
	case strings.Contains(s, "unknown flag"), strings.Contains(s, "unknown shorthand flag"):
		return usage("unknown flag; see vigil --help")
	case strings.Contains(s, "flag needs an argument"):
		return usage("a flag is missing its value")
	case strings.Contains(s, "invalid argument"):
		return usage("a flag value is invalid")
	case strings.HasPrefix(s, "accepts"), strings.HasPrefix(s, "requires"):
		return usage("unexpected arguments; no command takes a URL, selector or PromQL")
	}
	return usage("usage error; see vigil --help")
}

// errorDoc is vigil.error/v0.
type errorDoc struct {
	APIVersion string    `json:"apiVersion"`
	Error      errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// writeError prints exactly one vigil.error/v0 line. A non-Vigil error never
// reaches here with its own text: callers pass an exitcode.Error.
func writeError(w io.Writer, e *exitcode.Error) {
	doc := errorDoc{APIVersion: "vigil.error/v0", Error: errorBody{Code: e.Code, Message: e.Message}}
	b, err := json.Marshal(doc)
	if err != nil {
		b = []byte(`{"apiVersion":"vigil.error/v0","error":{"code":"internal"}}`)
	}
	_, _ = w.Write(append(b, '\n'))
}
