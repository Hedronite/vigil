// Package sources builds the vigil.sources/v0 document: one row per configured
// backend, checked live.
//
// A row is the only place a backend's state is reported, so the health command
// and the sources command can never disagree: both call the same checks. A row
// carries no last success, because V1 keeps no state between runs; the field is
// null and the docs say so.
package sources

import (
	"context"
	"time"

	"github.com/Hedronite/vigil/internal/config"
	"github.com/Hedronite/vigil/internal/grafana"
	"github.com/Hedronite/vigil/internal/httpx"
	"github.com/Hedronite/vigil/internal/reasons"
)

// Version is the document this package produces.
const Version = "vigil.sources/v0"

// Source is one row of vigil.sources/v0, the summary's own source shape.
type Source struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"`
	State      string  `json:"state"`
	ObservedAt *string `json:"observed_at"`
	LastOK     *string `json:"last_ok"`
	DetailCode *string `json:"detail_code,omitempty"`
	Vantage    string  `json:"vantage,omitempty"`
}

// Doc is vigil.sources/v0.
type Doc struct {
	APIVersion  string   `json:"apiVersion"`
	GeneratedAt string   `json:"generated_at"`
	Sources     []Source `json:"sources"`
}

// Sources checks every configured backend and returns the document. A backend
// being down is a row state, not a command failure: the command exits 0 with
// the row saying unavailable.
func Sources(ctx context.Context, cfg *config.Config, ov *config.Overlay, now time.Time) (*Doc, error) {
	at := stamp(now)
	doc := &Doc{
		APIVersion:  Version,
		GeneratedAt: at,
		Sources:     make([]Source, 0, len(cfg.Backends)),
	}
	for _, b := range cfg.Backends {
		row := Source{ID: b.ID, Kind: b.Kind, LastOK: nil, ObservedAt: &at}
		var state, detail string
		var err error
		switch b.Kind {
		case "prom":
			row.Vantage = vantage(b)
			state, detail, err = promReady(ctx, ov, b)
			if err != nil {
				return nil, err
			}
		case "grafana":
			state, detail, err = grafana.Check(ctx, cfg, ov, b)
			if err != nil {
				return nil, err
			}
		default:
			state, detail = reasons.StateUnavailable, "unknown_kind"
		}
		row.State = state
		if state == reasons.StateOK {
			row.DetailCode = nil
		} else {
			row.DetailCode = ptr(detail)
		}
		doc.Sources = append(doc.Sources, row)
	}
	return doc, nil
}

// promReady asks the backend its own readiness endpoint. A 200 is ready; any
// other answer, a redirect, a timeout or an unreachable host is unavailable
// with the code that says which.
func promReady(ctx context.Context, ov *config.Overlay, backend config.Backend) (string, string, error) {
	address, ok := ov.Address(backend.ID)
	if !ok {
		return reasons.StateUnavailable, "no_address", nil
	}
	client, err := httpx.ForBackend(backend.ID, httpx.Config{
		Base:    address,
		MaxBody: httpx.PromMaxBody,
		Timeout: httpx.DefaultTimeout,
	})
	if err != nil {
		return reasons.StateUnavailable, "no_address", nil
	}
	resp, err := client.Get(ctx, httpx.Request{Path: "/-/ready"})
	if err != nil {
		if code := httpx.Code(err); code != "" {
			return reasons.StateUnavailable, code, nil
		}
		return reasons.StateUnavailable, reasons.Unreachable, nil
	}
	if err := resp.Err(); err != nil {
		if code := httpx.Code(err); code != "" {
			return reasons.StateUnavailable, code, nil
		}
		return reasons.StateUnavailable, reasons.HTTPStatus, nil
	}
	return reasons.StateOK, "", nil
}

func vantage(b config.Backend) string {
	if b.Vantage != "" {
		return b.Vantage
	}
	return b.ID
}

func stamp(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.UTC().Format("2006-01-02T15:04:05Z")
}

func ptr(s string) *string { return &s }
