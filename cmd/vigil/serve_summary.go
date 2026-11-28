package main

import (
	"context"
	"errors"
	"time"

	"github.com/Hedronite/vigil/internal/config"
	"github.com/Hedronite/vigil/internal/exitcode"
	"github.com/Hedronite/vigil/internal/reasons"
	"github.com/Hedronite/vigil/internal/server"
	"github.com/Hedronite/vigil/internal/simplexdoc"
	"github.com/Hedronite/vigil/internal/summary"
)

// serveSummary loads config and builds vigil.summary/v0 with serve memory.
func serveSummary(ctx context.Context, cfg *config.Config, st *server.State, testimony *server.TestimonyStore, started, now time.Time) (*summary.Document, error) {
	skew, err := duration(cfg.Freshness.SkewBudget)
	if err != nil {
		return nil, err
	}
	in := summary.Input{
		SkewBudget:         skew,
		Declaration:        cfg.Declaration,
		DeclarationGrace:   declarationGrace(cfg),
		VigilDeclChangedAt: &started,
	}
	for _, configured := range cfg.Authorities {
		authority, err := readAuthorityServe(ctx, cfg.Freshness, configured, st, testimony, now)
		if err != nil {
			return nil, err
		}
		in.Authorities = append(in.Authorities, authority)
	}
	return summary.Combine(in, now), nil
}

func readAuthorityServe(ctx context.Context, f *config.Freshness, configured config.Authority, st *server.State, testimony *server.TestimonyStore, now time.Time) (summary.Authority, error) {
	if configured.ViaEffective() == "push" {
		return pushAuthorityServe(ctx, f, configured, st, testimony, now)
	}
	if configured.ID != simplexdoc.Source {
		return readAuthority(ctx, f, configured, now)
	}
	return simplexAuthorityServe(ctx, f, configured, st, now)
}

func pushAuthorityServe(ctx context.Context, f *config.Freshness, configured config.Authority, st *server.State, testimony *server.TestimonyStore, now time.Time) (summary.Authority, error) {
	age := f.StaleAfter
	if configured.StaleAfter != "" {
		age = configured.StaleAfter
	}
	staleAfter, err := duration(age)
	if err != nil {
		return summary.Authority{}, err
	}
	if configured.ID != simplexdoc.Source {
		return summary.Authority{ID: configured.ID, DetailCode: reasons.SourceUnavailable}, nil
	}
	if testimony == nil {
		return summary.Authority{ID: configured.ID, DetailCode: reasons.SourceUnavailable}, nil
	}
	seen, err := testimony.Observation(configured.ID, st, now)
	if err != nil {
		return summary.FromSimplex(nil, err, staleAfter), nil
	}
	return summary.FromSimplex(seen, nil, staleAfter), nil
}

func simplexAuthorityServe(ctx context.Context, f *config.Freshness, configured config.Authority, st *server.State, now time.Time) (summary.Authority, error) {
	age := f.StaleAfter
	if configured.StaleAfter != "" {
		age = configured.StaleAfter
	}
	staleAfter, err := duration(age)
	if err != nil {
		return summary.Authority{}, err
	}
	path, err := simplexdoc.ResolvePath(configured.PathRef)
	if err != nil {
		return summary.FromSimplex(nil, err, staleAfter), nil
	}
	body, modified, err := simplexdoc.ReadFile(ctx, path)
	var refused *simplexdoc.UnavailableError
	if err != nil && !errors.As(err, &refused) {
		return summary.Authority{}, exitcode.Newf(exitcode.Unavailable, "interrupted", "the command was interrupted before a verdict")
	}
	if err != nil {
		return summary.FromSimplex(nil, err, staleAfter), nil
	}
	doc, err := simplexdoc.Decode(body)
	if err != nil {
		return summary.FromSimplex(nil, err, staleAfter), nil
	}
	received := st.ReceivedAt(configured.ID, doc.GeneratedAt, body, simplexdoc.ReceivedAt(modified, now))
	seen := &simplexdoc.Observation{Document: doc, ReceivedAt: received}
	return summary.FromSimplex(seen, nil, staleAfter), nil
}

func declarationGrace(cfg *config.Config) time.Duration {
	return time.Duration(*cfg.Freshness.DeclarationGraceS) * time.Second
}
