package main

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/Hedronite/vigil/internal/config"
	"github.com/Hedronite/vigil/internal/exitcode"
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
		Long: "Print vigil.summary/v0 from the Simplex document the config names.\n" +
			"It reads files only. The exit code is the verdict: 0 GREEN, 1 YELLOW,\n" +
			"2 RED, and 4 when the document could not be read.",
		Args: cobra.NoArgs,
		RunE: runSummary,
	})
	return simplex
}

// simplexSummary reads one file and prints what it adds up to. No backend is
// contacted.
func simplexSummary(ctx context.Context, inv Invocation) error {
	cfg, authority, err := loadSummaryConfig(inv)
	if err != nil {
		return err
	}
	doc, err := buildSummary(ctx, cfg, authority, clock(inv))
	if err != nil {
		return err
	}
	if err := printSummary(inv, doc); err != nil {
		return err
	}
	return verdictOf(doc)
}

// loadSummaryConfig loads the config, checks an overlay if one is given, and finds
// the Simplex authority.
func loadSummaryConfig(inv Invocation) (*config.Config, config.Authority, error) {
	var none config.Authority
	if inv.ConfigPath == "" {
		return nil, none, exitcode.Newf(exitcode.Usage, "missing_config", "--config is required")
	}
	cfg, err := config.Load(inv.ConfigPath)
	if err != nil {
		return nil, none, configFailure("config", err)
	}
	if inv.OverlayPath != "" {
		if _, err := loadOverlay(inv.OverlayPath, cfg); err != nil {
			return nil, none, err
		}
	}
	for _, a := range cfg.Authorities {
		if a.ID == simplexdoc.Source {
			return cfg, a, nil
		}
	}
	return nil, none, exitcode.Newf(exitcode.Usage, "missing_authority", "the config has no simplex authority")
}

// buildSummary reads the authority's document and combines it. Why a document
// was refused stays here: the summary carries the detail code, and the cause,
// which can name a path, is never printed.
//
// A one-shot run remembers nothing, so it passes no time at which the
// declaration changed and drift has no grace window.
func buildSummary(ctx context.Context, cfg *config.Config, authority config.Authority, now time.Time) (*summary.Document, error) {
	staleAfter, skew, err := bounds(cfg.Freshness, authority)
	if err != nil {
		return nil, err
	}
	seen, readErr := simplexdoc.Read(ctx, authority.PathRef, now)
	var refused *simplexdoc.UnavailableError
	if readErr != nil && !errors.As(readErr, &refused) {
		return nil, exitcode.Newf(exitcode.Unavailable, "interrupted", "the command was interrupted before a verdict")
	}
	return summary.Combine(summary.Input{
		Authorities:      []summary.Authority{summary.FromSimplex(seen, readErr, staleAfter)},
		SkewBudget:       skew,
		Declaration:      cfg.Declaration,
		DeclarationGrace: time.Duration(*cfg.Freshness.DeclarationGraceS) * time.Second,
	}, now), nil
}

// bounds is the authority's stale_after, or the global one, and the skew
// budget. The loader has checked both shapes and filled the defaults.
func bounds(f *config.Freshness, authority config.Authority) (staleAfter, skew time.Duration, err error) {
	age := f.StaleAfter
	if authority.StaleAfter != "" {
		age = authority.StaleAfter
	}
	staleAfter, ageErr := time.ParseDuration(age)
	skew, skewErr := time.ParseDuration(f.SkewBudget)
	if ageErr != nil || skewErr != nil {
		return 0, 0, exitcode.New(exitcode.Internal, "internal")
	}
	return staleAfter, skew, nil
}

func printSummary(inv Invocation, doc *summary.Document) error {
	if inv.JSON {
		return render.JSON(inv.Stdout, doc)
	}
	return render.Summary(inv.Stdout, doc)
}
