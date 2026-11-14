// Package promclient answers the two Prometheus commands: the scrape targets a
// named backend reports, and one named query from the config.
//
// Everything it publishes is reduced to what the contract allows: a host slug,
// a job, a health word, a timestamp and a sample. The target decoder never looks
// at scrapeUrl, lastError or discoveredLabels, so an address in those fields
// cannot reach the output even by accident, and every returned label passes the
// overlay's mapping before it is printed.
package promclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Hedronite/vigil/internal/config"
	"github.com/Hedronite/vigil/internal/exitcode"
	"github.com/Hedronite/vigil/internal/httpx"
	"github.com/Hedronite/vigil/internal/reasons"
)

// API versions of the documents this package returns.
const (
	TargetsVersion = "vigil.targets/v0"
	QueryVersion   = "vigil.query/v0"
)

// Codes owned by the command layer: the backend-selection failures are Vigil's
// own words, while the host rules come from internal/reasons.
const (
	codeNoBackend   = "no_backend"
	codeAmbiguous   = "ambiguous_backend"
	codeNoAddress   = "no_address"
	codeUnknownHost = "unknown_host"
)

// unmapped and redacted are the two words the document uses when a label is
// not publishable.
const (
	unmapped = "unmapped"
	redacted = "redacted"
)

// PublicSlugs are the six slugs the product publishes. A --host is accepted for
// one of these even when the overlay does not map it.
var PublicSlugs = []string{"castle", "tower", "citadel", "apiary", "lathe", "box"}

var (
	slugPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	jobPattern  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	// samplePattern is the query document's number shape, so a value that is
	// really an address or a name is never printed as a measurement.
	samplePattern = regexp.MustCompile(`^[+-]?([0-9]+(\.[0-9]+)?([eE][+-]?[0-9]+)?|Inf)$|^NaN$`)
	// hostPlaceholder is the only template the config admits.
	hostPlaceholder = "{{.host}}"
)

// TargetsDoc is vigil.targets/v0.
type TargetsDoc struct {
	APIVersion  string   `json:"apiVersion"`
	Backend     string   `json:"backend"`
	Vantage     string   `json:"vantage"`
	GeneratedAt string   `json:"generated_at"`
	Targets     []Target `json:"targets"`
}

// Target is one scrape target, reduced to publishable fields.
type Target struct {
	Host       string  `json:"host"`
	Job        string  `json:"job"`
	Health     string  `json:"health"`
	LastScrape *string `json:"last_scrape"`
}

// QueryDoc is vigil.query/v0.
type QueryDoc struct {
	APIVersion  string   `json:"apiVersion"`
	Backend     string   `json:"backend"`
	Vantage     string   `json:"vantage"`
	Name        string   `json:"name"`
	Host        string   `json:"host,omitempty"`
	EvaluatedAt string   `json:"evaluated_at"`
	Series      []Series `json:"series"`
}

// Series is one answered series, reduced to publishable fields.
type Series struct {
	Host  string `json:"host"`
	Job   string `json:"job,omitempty"`
	Value string `json:"value"`
}

// Targets asks the named Prometheus backend for its active targets.
func Targets(ctx context.Context, cfg *config.Config, ov *config.Overlay, now time.Time) (*TargetsDoc, error) {
	backend, err := promBackend(cfg)
	if err != nil {
		return nil, err
	}
	client, err := newClient(cfg, ov, backend)
	if err != nil {
		return nil, err
	}
	resp, err := client.Get(ctx, httpx.Request{
		Path:  "/api/v1/targets",
		Query: url.Values{"state": {"active"}},
	})
	if err != nil {
		return nil, httpxError(err)
	}
	if err := resp.Err(); err != nil {
		return nil, httpxError(err)
	}

	var payload targetsPayload
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		return nil, exitcode.New(exitcode.Unavailable, reasons.HTTPStatus)
	}
	if payload.Status != "success" {
		return nil, apiError(payload.ErrorType)
	}

	doc := &TargetsDoc{
		APIVersion:  TargetsVersion,
		Backend:     backend.ID,
		Vantage:     vantage(backend),
		GeneratedAt: stamp(now),
		Targets:     make([]Target, 0, len(payload.Data.ActiveTargets)),
	}
	for _, t := range payload.Data.ActiveTargets {
		doc.Targets = append(doc.Targets, Target{
			Host:       mapInstance(ov, t.Labels["instance"]),
			Job:        mapJob(t.Labels["job"]),
			Health:     healthWord(t.Health),
			LastScrape: optionalStamp(t.LastScrape),
		})
	}
	sort.SliceStable(doc.Targets, func(i, j int) bool {
		a, b := doc.Targets[i], doc.Targets[j]
		if a.Host != b.Host {
			return a.Host < b.Host
		}
		if a.Job != b.Job {
			return a.Job < b.Job
		}
		return a.Health < b.Health
	})
	return doc, nil
}

// Query runs one named query. The host is required exactly when the expression
// contains the placeholder: a missing or unexpected one is a usage error, and
// the expression is never fanned out over every host.
func Query(ctx context.Context, cfg *config.Config, ov *config.Overlay, name, host string, now time.Time) (*QueryDoc, error) {
	backend, err := promBackend(cfg)
	if err != nil {
		return nil, err
	}
	q, ok := cfg.Query(name)
	if !ok {
		return nil, exitcode.Newf(exitcode.Usage, "unknown_query", "no query with that name is configured")
	}
	needsHost := strings.Contains(q.Expr, hostPlaceholder)
	switch {
	case needsHost && host == "":
		return nil, exitcode.Newf(exitcode.Usage, reasons.MissingHost, "this query needs a host")
	case !needsHost && host != "":
		return nil, exitcode.Newf(exitcode.Usage, reasons.UnexpectedHost, "this query takes no host")
	}
	if host != "" && !knownHost(ov, host) {
		return nil, exitcode.Newf(exitcode.Usage, codeUnknownHost, "the host is not a known slug")
	}
	expr := q.Expr
	if needsHost {
		// A literal replacement of the one admitted placeholder. The config
		// loader already rejected any other brace sequence, and the host was
		// checked to be a slug, so nothing here can inject PromQL.
		expr = strings.ReplaceAll(expr, hostPlaceholder, host)
	}

	client, err := newClient(cfg, ov, backend)
	if err != nil {
		return nil, err
	}
	timeout, err := parseTimeout(q.Timeout)
	if err != nil {
		return nil, err
	}
	params := url.Values{
		"query":   {expr},
		"timeout": {q.Timeout},
		// One more than the cap, so exceeding it is visible rather than silent.
		"limit": {fmt.Sprint(q.MaxSeries + 1)},
	}
	if !now.IsZero() {
		params.Set("time", now.UTC().Format(time.RFC3339))
	}
	resp, err := client.Get(ctx, httpx.Request{
		Path:    "/api/v1/query",
		Query:   params,
		Timeout: timeout,
	})
	if err != nil {
		return nil, httpxError(err)
	}
	if err := resp.Err(); err != nil {
		return nil, httpxError(err)
	}

	var payload queryPayload
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		return nil, exitcode.New(exitcode.Unavailable, reasons.HTTPStatus)
	}
	if payload.Status != "success" {
		return nil, apiError(payload.ErrorType)
	}
	// A warning means the answer may be partial. It is never rendered.
	if len(payload.Warnings) > 0 {
		return nil, exitcode.New(exitcode.Unavailable, reasons.QueryWarnings)
	}
	if len(payload.Data.Result) > q.MaxSeries {
		return nil, exitcode.New(exitcode.Unavailable, reasons.SeriesCap)
	}

	doc := &QueryDoc{
		APIVersion:  QueryVersion,
		Backend:     backend.ID,
		Vantage:     vantage(backend),
		Name:        q.Name,
		Host:        host,
		EvaluatedAt: stamp(now),
		Series:      make([]Series, 0, len(payload.Data.Result)),
	}
	for _, r := range payload.Data.Result {
		value, ok := sample(r.Value)
		if !ok {
			return nil, exitcode.New(exitcode.Unavailable, reasons.HTTPStatus)
		}
		mapped := mapInstance(ov, r.Metric["instance"])
		if mapped == unmapped && host != "" {
			mapped = host
		}
		item := Series{Host: mapped, Value: value}
		if job := jobWord(r.Metric["job"]); job != "" {
			item.Job = job
		}
		doc.Series = append(doc.Series, item)
	}
	sort.SliceStable(doc.Series, func(i, j int) bool {
		a, b := doc.Series[i], doc.Series[j]
		if a.Host != b.Host {
			return a.Host < b.Host
		}
		if a.Job != b.Job {
			return a.Job < b.Job
		}
		return a.Value < b.Value
	})
	return doc, nil
}

// promBackend picks the single Prometheus backend the config declares. Two
// would make the answer ambiguous, and the command takes no backend argument.
func promBackend(cfg *config.Config) (config.Backend, error) {
	var found []config.Backend
	for _, b := range cfg.Backends {
		if b.Kind == "prom" {
			found = append(found, b)
		}
	}
	switch len(found) {
	case 0:
		return config.Backend{}, exitcode.Newf(exitcode.Usage, codeNoBackend, "no prometheus backend is configured")
	case 1:
		return found[0], nil
	default:
		return config.Backend{}, exitcode.Newf(exitcode.Usage, codeAmbiguous, "more than one prometheus backend is configured")
	}
}

func newClient(cfg *config.Config, ov *config.Overlay, backend config.Backend) (*httpx.Client, error) {
	address, ok := ov.Address(backend.ID)
	if !ok {
		return nil, exitcode.Newf(exitcode.Usage, codeNoAddress, "the backend has no address in the overlay")
	}
	client, err := httpx.ForBackend(backend.ID, httpx.Config{Base: address, MaxBody: httpx.PromMaxBody, Timeout: httpx.DefaultTimeout})
	if err != nil {
		return nil, exitcode.Newf(exitcode.Usage, codeNoAddress, "the backend address is not usable")
	}
	return client, nil
}

// vantage is the label for the Prometheus vantage, falling back to the backend
// id when the config gives none.
func vantage(b config.Backend) string {
	if b.Vantage != "" {
		return b.Vantage
	}
	return b.ID
}

// mapInstance turns a target instance label into a public slug: the overlay's
// map first, then a value that is already a known slug (an overlay host slug or
// one of the public six), otherwise unmapped. A slug-shaped label that nobody
// declares is still a name, and a name is not published.
func mapInstance(ov *config.Overlay, instance string) string {
	if instance == "" {
		return unmapped
	}
	if slug, ok := ov.SlugFor(instance); ok {
		return slug
	}
	if knownHost(ov, instance) {
		return instance
	}
	return unmapped
}

// mapJob keeps a publishable job label and redacts anything else.
func mapJob(job string) string {
	if jobPattern.MatchString(job) {
		return job
	}
	return redacted
}

// jobWord keeps an optional job label, or returns "" when there is nothing to
// publish: the query document omits the field rather than inventing a word.
func jobWord(job string) string {
	if jobPattern.MatchString(job) {
		return job
	}
	return ""
}

// healthWord narrows Prometheus health to the three words the document allows.
func healthWord(h string) string {
	switch h {
	case "up", "down", "unknown":
		return h
	default:
		return "unknown"
	}
}

// knownHost accepts a slug the overlay maps or one of the public slugs.
func knownHost(ov *config.Overlay, host string) bool {
	if !slugPattern.MatchString(host) {
		return false
	}
	for _, slug := range PublicSlugs {
		if slug == host {
			return true
		}
	}
	for _, h := range ov.Hosts {
		if h.Slug == host {
			return true
		}
	}
	return false
}

// sample reads a Prometheus sample, which is [timestamp, "value"], and checks
// the value has the document's number shape.
func sample(raw []json.RawMessage) (string, bool) {
	if len(raw) != 2 {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw[1], &value); err != nil {
		return "", false
	}
	if !samplePattern.MatchString(value) {
		return "", false
	}
	return value, true
}

// apiError maps a Prometheus error status to an exit and a code. A rejected
// expression is the operator's error; anything else means the backend could
// not answer.
func apiError(errorType string) error {
	switch errorType {
	case "bad_data":
		return exitcode.New(exitcode.Usage, reasons.QueryRejected)
	case "timeout", "canceled":
		return exitcode.New(exitcode.Unavailable, reasons.Timeout)
	default:
		return exitcode.New(exitcode.Unavailable, reasons.QueryRejected)
	}
}

// httpxError keeps the client's detail code and nothing else.
func httpxError(err error) error {
	if code := httpx.Code(err); code != "" {
		return exitcode.New(exitcode.Unavailable, code)
	}
	return exitcode.New(exitcode.Unavailable, reasons.Unreachable)
}

// stamp renders a time the way the documents pin it: UTC, second precision.
func stamp(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.UTC().Format("2006-01-02T15:04:05Z")
}

// optionalStamp renders a time that may be absent.
func optionalStamp(t *time.Time) *string {
	if t == nil || t.IsZero() {
		return nil
	}
	s := t.UTC().Format("2006-01-02T15:04:05Z")
	return &s
}

// parseTimeout reads a configured per-query timeout, which the loader already
// checked against the 1s..10s shape.
func parseTimeout(v string) (time.Duration, error) {
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, exitcode.Newf(exitcode.Usage, "bad_timeout", "the query timeout is not usable")
	}
	return d, nil
}

// targetsPayload is the part of /api/v1/targets the document needs. Addresses,
// last errors and discovered labels are deliberately not decoded.
type targetsPayload struct {
	Status    string `json:"status"`
	ErrorType string `json:"errorType"`
	Data      struct {
		ActiveTargets []struct {
			Labels     map[string]string `json:"labels"`
			Health     string            `json:"health"`
			LastScrape *time.Time        `json:"lastScrape"`
		} `json:"activeTargets"`
	} `json:"data"`
}

// queryPayload is the part of /api/v1/query the document needs.
type queryPayload struct {
	Status    string   `json:"status"`
	Warnings  []string `json:"warnings"`
	ErrorType string   `json:"errorType"`
	Data      struct {
		Result []struct {
			Metric map[string]string `json:"metric"`
			Value  []json.RawMessage `json:"value"`
		} `json:"result"`
	} `json:"data"`
}
