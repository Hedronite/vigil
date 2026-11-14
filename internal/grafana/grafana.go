// Package grafana answers the Grafana health command and supplies the health
// row the sources command needs.
//
// Health means the backend answered its own health endpoint with a JSON
// database status of ok. A login page, a redirect, another status or any other
// body is not health, however green it looks: the check exists to catch a
// Grafana that is up enough to serve HTML and not up enough to serve data.
package grafana

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Hedronite/vigil/internal/config"
	"github.com/Hedronite/vigil/internal/exitcode"
	"github.com/Hedronite/vigil/internal/httpx"
	"github.com/Hedronite/vigil/internal/reasons"
)

// Version is the document this package produces.
const Version = "vigil.health/v0"

// Codes owned by the command layer until internal/reasons carries them.
const (
	codeNoBackend = "no_backend"
	codeAmbiguous = "ambiguous_backend"
	codeNoAddress = "no_address"
)

// HealthDoc is vigil.health/v0, printed only when the backend is healthy.
type HealthDoc struct {
	APIVersion string  `json:"apiVersion"`
	Backend    string  `json:"backend"`
	State      string  `json:"state"`
	DetailCode *string `json:"detail_code"`
	CheckedAt  string  `json:"checked_at"`
}

// Health checks the named Grafana backend. A healthy backend returns the
// document; anything else returns an *exitcode.Error whose code says why, so
// the command prints one error line and no document.
func Health(ctx context.Context, cfg *config.Config, ov *config.Overlay, now time.Time) (*HealthDoc, error) {
	backend, err := grafanaBackend(cfg)
	if err != nil {
		return nil, err
	}
	state, detail, err := Check(ctx, cfg, ov, backend)
	if err != nil {
		return nil, err
	}
	if state != reasons.StateOK {
		return nil, exitcode.New(exitcode.Unavailable, detail)
	}
	doc := &HealthDoc{
		APIVersion: Version,
		Backend:    backend.ID,
		State:      state,
		CheckedAt:  stamp(now),
	}
	return doc, nil
}

// Check performs the health probe and reports the row state and detail code.
// A healthy answer is ok with no detail; every failure is unavailable with the
// code the sources row prints.
func Check(ctx context.Context, cfg *config.Config, ov *config.Overlay, backend config.Backend) (string, string, error) {
	address, ok := ov.Address(backend.ID)
	if !ok {
		return reasons.StateUnavailable, codeNoAddress, nil
	}
	client, err := httpx.New(httpx.Config{
		Base:    address,
		MaxBody: httpx.GrafanaMaxBody,
		Timeout: 2 * time.Second,
	})
	if err != nil {
		return reasons.StateUnavailable, codeNoAddress, nil
	}
	resp, err := client.Get(ctx, httpx.Request{
		Path:    "/api/health",
		Timeout: 2 * time.Second,
		MaxBody: httpx.GrafanaMaxBody,
	})
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
	var payload struct {
		Database string `json:"database"`
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		// HTML, or any body that is not the health JSON, is not health.
		return reasons.StateUnavailable, reasons.NotHealth, nil
	}
	if payload.Database != "ok" {
		return reasons.StateUnavailable, reasons.NotHealth, nil
	}
	return reasons.StateOK, "", nil
}

// grafanaBackend picks the single Grafana backend the config declares.
func grafanaBackend(cfg *config.Config) (config.Backend, error) {
	var found []config.Backend
	for _, b := range cfg.Backends {
		if b.Kind == "grafana" {
			found = append(found, b)
		}
	}
	switch len(found) {
	case 0:
		return config.Backend{}, exitcode.Newf(exitcode.Usage, codeNoBackend, "no grafana backend is configured")
	case 1:
		return found[0], nil
	default:
		return config.Backend{}, exitcode.Newf(exitcode.Usage, codeAmbiguous, "more than one grafana backend is configured")
	}
}

func stamp(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.UTC().Format("2006-01-02T15:04:05Z")
}
