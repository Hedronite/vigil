package main

import (
	"context"
	"testing"
	"time"

	"github.com/Hedronite/vigil/internal/config"
	"github.com/Hedronite/vigil/internal/reasons"
	"github.com/Hedronite/vigil/internal/server"
)

func TestReadAuthorityServePushWithoutPath(t *testing.T) {
	auth, err := readAuthorityServe(
		context.Background(),
		&config.Freshness{},
		config.Authority{ID: "simplex", Via: "push"},
		server.NewState(),
		generated,
	)
	if err != nil {
		t.Fatal(err)
	}
	if auth.DetailCode != reasons.SourceUnavailable {
		t.Fatalf("push authority = %+v", auth)
	}
}

func TestReadAuthorityServeNonSimplexUnavailable(t *testing.T) {
	auth, err := readAuthorityServe(
		context.Background(),
		&config.Freshness{},
		config.Authority{ID: "augur", Via: "file", PathRef: "env:MISSING"},
		server.NewState(),
		generated,
	)
	if err != nil {
		t.Fatal(err)
	}
	if auth.DetailCode != reasons.SourceUnavailable {
		t.Fatalf("non-simplex authority = %+v", auth)
	}
}

func TestServeSummaryBuildsWithDeclarationGrace(t *testing.T) {
	publish(t, vendoredDoc(t, "green"), generated)
	path := writeText(t, "config.yaml", `apiVersion: vigil.config/v0
declaration: `+digestA+`
authorities:
  - id: simplex
    path_ref: env:VIGIL_TEST_SIMPLEX
freshness:
  declaration_grace_s: 120
`)
	cfg, err := loadSummaryConfig(Invocation{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 13, 18, 0, 30, 0, time.UTC)
	doc, err := serveSummary(context.Background(), cfg, server.NewState(), now.Add(-time.Minute), now)
	if err != nil || doc == nil || doc.Declaration == nil {
		t.Fatalf("summary = %v err %v", doc, err)
	}
	if g := declarationGrace(cfg); g != 120*time.Second {
		t.Fatalf("grace = %v, want 120s", g)
	}
}
