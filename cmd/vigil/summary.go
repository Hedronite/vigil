package main

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/Hedronite/vigil/internal/config"
	"github.com/Hedronite/vigil/internal/exitcode"
	"github.com/Hedronite/vigil/internal/reasons"
	"github.com/Hedronite/vigil/internal/render"
	"github.com/Hedronite/vigil/internal/simplexdoc"
	"github.com/Hedronite/vigil/internal/summary"
)

// verdictExit is how a handler whose document was built asks for its code: the
// output is kept and nothing goes to stderr. It is not a failure, and only
// verdictOf makes one.
type verdictExit int

func (v verdictExit) Error() string { return "verdict exit " + strconv.Itoa(int(v)) }

// verdictOf maps a summary to the handler's answer: its colour as 0 to 2, or 4
// when it has none.
func verdictOf(doc *summary.Document) error {
	switch code := summary.ExitCode(doc); code {
	case exitcode.Green:
		return nil
	case exitcode.Yellow, exitcode.Red, exitcode.Unavailable:
		return verdictExit(code)
	}
	return exitcode.New(exitcode.Internal, "internal")
}

// simplexCommand is the simplex group and its summary.
func simplexCommand(runSummary func(*cobra.Command, []string) error) *cobra.Command {
	simplex := group("simplex", "Combine what Simplex published")
	simplex.AddCommand(&cobra.Command{
		Use:   "summary",
		Short: "Print vigil.summary/v0 from the Simplex document",
		Long: "Print vigil.summary/v0 for the configured authorities. Only the Simplex\n" +
			"document is read yet; any other authority is unavailable. It reads files\n" +
			"only. The exit code is the verdict: 0 GREEN, 1 YELLOW, 2 RED, and 4 when\n" +
			"no authority could be read.",
		Args: cobra.NoArgs,
		RunE: runSummary,
	})
	return simplex
}

// simplexSummary reads one file and prints what it adds up to. No backend is
// contacted.
func simplexSummary(ctx context.Context, inv Invocation) error {
	cfg, err := loadSummaryConfig(inv)
	if err != nil {
		return err
	}
	doc, err := buildSummary(ctx, cfg, clock(inv))
	if err != nil {
		return err
	}
	if err := printSummary(inv, doc); err != nil {
		return err
	}
	return verdictOf(doc)
}

// loadSummaryConfig loads the config, checks an overlay if one is given, and
// requires a Simplex authority.
func loadSummaryConfig(inv Invocation) (*config.Config, error) {
	if inv.ConfigPath == "" {
		return nil, exitcode.Newf(exitcode.Usage, "missing_config", "--config is required")
	}
	cfg, err := config.Load(inv.ConfigPath)
	if err != nil {
		return nil, configFailure("config", err)
	}
	if inv.OverlayPath != "" {
		if _, err := loadOverlay(inv.OverlayPath, cfg); err != nil {
			return nil, err
		}
	}
	if !slices.ContainsFunc(cfg.Authorities, func(a config.Authority) bool { return a.ID == simplexdoc.Source }) {
		return nil, exitcode.Newf(exitcode.Usage, "missing_authority", "the config has no simplex authority")
	}
	return cfg, nil
}

// buildSummary combines every configured authority, in config order.
//
// A one-shot run remembers nothing, so it passes no time at which the
// declaration changed and drift has no grace window.
func buildSummary(ctx context.Context, cfg *config.Config, now time.Time) (*summary.Document, error) {
	skew, err := duration(cfg.Freshness.SkewBudget)
	if err != nil {
		return nil, err
	}
	in := summary.Input{
		SkewBudget:       skew,
		Declaration:      cfg.Declaration,
		DeclarationGrace: time.Duration(*cfg.Freshness.DeclarationGraceS) * time.Second,
	}
	for _, configured := range cfg.Authorities {
		authority, err := readAuthority(ctx, cfg.Freshness, configured, now)
		if err != nil {
			return nil, err
		}
		in.Authorities = append(in.Authorities, authority)
	}
	return summary.Combine(in, now), nil
}

// readAuthority is what one configured authority contributes. Only Simplex has
// a reader; any other is unavailable, never left out, so that configuring it
// cannot read as health.
//
// Why a document was refused stays here: the summary carries the detail code,
// and the cause, which can name a path, is never printed.
func readAuthority(ctx context.Context, f *config.Freshness, configured config.Authority, now time.Time) (summary.Authority, error) {
	if configured.ID != simplexdoc.Source {
		return summary.Authority{ID: configured.ID, DetailCode: reasons.SourceUnavailable}, nil
	}
	age := f.StaleAfter
	if configured.StaleAfter != "" {
		age = configured.StaleAfter
	}
	staleAfter, err := duration(age)
	if err != nil {
		return summary.Authority{}, err
	}
	seen, readErr := simplexdoc.Read(ctx, configured.PathRef, now)
	var refused *simplexdoc.UnavailableError
	if readErr != nil && !errors.As(readErr, &refused) {
		return summary.Authority{}, exitcode.Newf(exitcode.Unavailable, "interrupted", "the command was interrupted before a verdict")
	}
	return summary.FromSimplex(seen, readErr, staleAfter), nil
}

// duration parses a bound whose shape the loader has checked and whose default
// it has filled.
func duration(value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, exitcode.New(exitcode.Internal, "internal")
	}
	return d, nil
}

func printSummary(inv Invocation, doc *summary.Document) error {
	if inv.JSON {
		return render.JSON(inv.Stdout, doc)
	}
	return render.Summary(inv.Stdout, doc)
}
