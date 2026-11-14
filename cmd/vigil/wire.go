package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"text/tabwriter"
	"time"

	"github.com/Hedronite/vigil/internal/config"
	"github.com/Hedronite/vigil/internal/exitcode"
	"github.com/Hedronite/vigil/internal/promclient"
)

// liveHandlers is the table the binary runs. Each backend command is a thin
// closure: load the config and the overlay, call the backend package with the
// pinned clock, print the document. A command whose backend has not landed is
// still the stub.
func liveHandlers() Handlers {
	h := defaultHandlers()
	h.PromTargets = promTargets
	h.PromQuery = promQuery
	return h
}

func promTargets(ctx context.Context, inv Invocation) error {
	cfg, ov, err := loadDocuments(inv)
	if err != nil {
		return err
	}
	doc, err := promclient.Targets(ctx, cfg, ov, clock(inv))
	if err != nil {
		return err
	}
	return printTargets(inv, doc)
}

func promQuery(ctx context.Context, inv Invocation) error {
	cfg, ov, err := loadDocuments(inv)
	if err != nil {
		return err
	}
	doc, err := promclient.Query(ctx, cfg, ov, inv.Name, inv.Host, clock(inv))
	if err != nil {
		return err
	}
	return printQuery(inv, doc)
}

// clock is the pinned time from --now, or the real one.
func clock(inv Invocation) time.Time {
	if !inv.Now.IsZero() {
		return inv.Now.UTC()
	}
	return time.Now().UTC()
}

// loadDocuments reads the config and the overlay the backend commands need and
// checks that they agree. Failures are usage errors (3), except a wildcard
// listen address, which is a startup refusal (5).
func loadDocuments(inv Invocation) (*config.Config, *config.Overlay, error) {
	if inv.ConfigPath == "" {
		return nil, nil, exitcode.Newf(exitcode.Usage, "missing_config", "--config is required")
	}
	if inv.OverlayPath == "" {
		return nil, nil, exitcode.Newf(exitcode.Usage, "missing_overlay", "--overlay is required")
	}
	cfg, err := config.Load(inv.ConfigPath)
	if err != nil {
		return nil, nil, configFailure("config", err)
	}
	ov, err := config.LoadOverlay(inv.OverlayPath)
	if err != nil {
		return nil, nil, configFailure("overlay", err)
	}
	if err := ov.Bind(cfg); err != nil {
		return nil, nil, configFailure("overlay", err)
	}
	return cfg, ov, nil
}

// fieldPattern is the only shape of config field path that is repeated in an
// error message: names and dots, nothing that could be a value.
var fieldPattern = regexp.MustCompile(`^[A-Za-z0-9_.\[\]-]{1,120}$`)

// configFailure turns a loader error into an exit code and fixed text. The
// loader's own message can quote a value from the file (an address, a path),
// so it is dropped; the field path, when it has a safe shape, stays.
func configFailure(kind string, err error) error {
	var ce *config.Error
	if !errors.As(err, &ce) {
		return exitcode.Newf(exitcode.Usage, kind+"_invalid", "the "+kind+" could not be loaded")
	}
	if ce.Code == exitcode.Startup {
		return exitcode.Newf(exitcode.Startup, "wildcard_listen", "a wildcard listen address is refused; bind one address")
	}
	msg := "the " + kind + " is invalid"
	if fieldPattern.MatchString(ce.Field) {
		msg += " at " + ce.Field
	}
	return exitcode.Newf(exitcode.Usage, kind+"_invalid", msg)
}

// Output. The document is the contract; a table is a view of it. These two
// printers are the seam where internal/render takes over.

func printJSON(w io.Writer, doc any) error {
	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

func printTargets(inv Invocation, doc *promclient.TargetsDoc) error {
	if inv.JSON {
		return printJSON(inv.Stdout, doc)
	}
	_, _ = fmt.Fprintf(inv.Stdout, "backend %s  vantage %s\n\n", doc.Backend, doc.Vantage)
	tw := tabwriter.NewWriter(inv.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "HOST\tJOB\tHEALTH\tLAST SCRAPE")
	for _, t := range doc.Targets {
		last := "-"
		if t.LastScrape != nil {
			last = *t.LastScrape
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", t.Host, t.Job, t.Health, last)
	}
	return tw.Flush()
}

func printQuery(inv Invocation, doc *promclient.QueryDoc) error {
	if inv.JSON {
		return printJSON(inv.Stdout, doc)
	}
	_, _ = fmt.Fprintf(inv.Stdout, "backend %s  vantage %s  query %s\n\n", doc.Backend, doc.Vantage, doc.Name)
	tw := tabwriter.NewWriter(inv.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "HOST\tJOB\tVALUE")
	for _, s := range doc.Series {
		job := s.Job
		if job == "" {
			job = "-"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", s.Host, job, s.Value)
	}
	return tw.Flush()
}
