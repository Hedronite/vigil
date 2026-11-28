package main

import (
	"context"
	"testing"
	"time"

	"github.com/Hedronite/vigil/internal/config"
	"github.com/Hedronite/vigil/internal/reasons"
	"github.com/Hedronite/vigil/internal/server"
)

func TestReadAuthorityServePushWithoutTestimony(t *testing.T) {
	auth, err := readAuthorityServe(
		context.Background(),
		&config.Freshness{StaleAfter: "120s"},
		config.Authority{ID: "simplex", Via: "push"},
		server.NewState(),
		server.NewTestimonyStore(),
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
		nil,
		generated,
	)
	if err != nil {
		t.Fatal(err)
	}
	if auth.DetailCode != reasons.SourceUnavailable {
		t.Fatalf("non-simplex authority = %+v", auth)
	}
}

func TestPushAuthorityServeReadsTestimonyStore(t *testing.T) {
	store := server.NewTestimonyStore()
	st := server.NewState()
	body := []byte(vendoredDoc(t, "green"))
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	doc, code, _ := server.DecodeTestimonyBody(body)
	if code != "" || doc == nil {
		t.Fatal("decode green")
	}
	if _, err := store.Accept("simplex", body, doc, st, now); err != nil {
		t.Fatal(err)
	}
	auth, err := pushAuthorityServe(
		context.Background(),
		&config.Freshness{StaleAfter: "120s"},
		config.Authority{ID: "simplex", Via: "push"},
		st, store, now,
	)
	if err != nil || auth.DetailCode != "" || auth.Document == nil {
		t.Fatalf("auth = %+v err %v", auth, err)
	}
}

func TestPushAuthorityServeNilTestimonyUnavailable(t *testing.T) {
	auth, err := pushAuthorityServe(
		context.Background(),
		&config.Freshness{StaleAfter: "120s"},
		config.Authority{ID: "simplex", Via: "push"},
		server.NewState(), nil, generated,
	)
	if err != nil || auth.DetailCode != reasons.SourceUnavailable {
		t.Fatalf("auth = %+v", auth)
	}
}

func TestPushAuthorityAugurPushStaysUnavailable(t *testing.T) {
	auth, err := pushAuthorityServe(
		context.Background(),
		&config.Freshness{StaleAfter: "120s"},
		config.Authority{ID: "augur", Via: "push"},
		server.NewState(), server.NewTestimonyStore(), generated,
	)
	if err != nil || auth.DetailCode != reasons.SourceUnavailable {
		t.Fatalf("auth = %+v", auth)
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
	doc, err := serveSummary(context.Background(), cfg, server.NewState(), server.NewTestimonyStore(), now.Add(-time.Minute), now)
	if err != nil || doc == nil || doc.Declaration == nil {
		t.Fatalf("summary = %v err %v", doc, err)
	}
	if g := declarationGrace(cfg); g != 120*time.Second {
		t.Fatalf("grace = %v, want 120s", g)
	}
}
