package simplexdoc

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/Hedronite/vigil/internal/exitcode"
	"github.com/Hedronite/vigil/internal/reasons"
)

const (
	// Version is the contract this package reads.
	Version = "simplex.probe/v0"
	// Source is what every host carries into the summary.
	Source = "simplex"
)

var (
	versionShape  = regexp.MustCompile(`^[a-z][a-z0-9._-]*/v[0-9][a-z0-9]*$`)
	slugPattern   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	stampPattern  = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?Z$`)
	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

	vantageStatuses = []string{"ok", "blind", "unchecked"}
)

// Document is what Vigil takes from simplex.probe/v0. The checks behind the
// verdict are not read: Vigil does not derive a second one.
type Document struct {
	GeneratedAt time.Time
	// Overall is the producer's colour, as published. A blind vantage is a RED
	// verdict like any other.
	Overall string
	Vantage Vantage
	Hosts   []Host
	// Observed, ValidFor and Declaration are the testimony header; each is nil
	// when the document leaves it out, and Declaration also when it is null.
	Observed    *bool
	ValidFor    *int
	Declaration *string
}

// Vantage is where the probe ran from, in the summary's own shape.
type Vantage struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// Host is one host's verdict in the summary's own shape, reasons in the order
// published.
type Host struct {
	ID       string   `json:"id"`
	Severity string   `json:"severity"`
	Reasons  []string `json:"reasons"`
	Source   string   `json:"source"`
}

// wire is the part of the document that is read. Members not named here are
// ignored, so an addition within v0 changes nothing. The header members stay
// raw to tell an absent one from a null one.
type wire struct {
	GeneratedAt string         `json:"generated_at"`
	Overall     string         `json:"overall"`
	Vantage     *Vantage       `json:"vantage"`
	Hosts       []wireHost     `json:"hosts"`
	Observed    jsontext.Value `json:"observed"`
	ValidFor    jsontext.Value `json:"valid_for"`
	Declaration jsontext.Value `json:"declaration"`
}

type wireHost struct {
	ID       string   `json:"id"`
	Severity string   `json:"severity"`
	Reasons  []string `json:"reasons"`
}

// Decode reads one document. The error is always an *UnavailableError.
//
// Member names match exactly and may appear once. A reader that folded case or
// kept the last duplicate would let a member that is not ours replace the
// verdict.
func Decode(body []byte) (*Document, error) {
	if err := checkVersion(body); err != nil {
		return nil, err
	}
	var w wire
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, unavailable(fmt.Errorf("decode: %w", err))
	}
	doc, err := w.document()
	if err != nil {
		return nil, unavailable(err)
	}
	return doc, nil
}

// checkVersion reads the one member that says what the rest means, before
// anything another version may have changed.
func checkVersion(body []byte) error {
	var head struct {
		APIVersion string `json:"apiVersion"`
	}
	if err := json.Unmarshal(body, &head); err != nil {
		return unavailable(fmt.Errorf("decode: %w", err))
	}
	switch {
	case head.APIVersion == Version:
		return nil
	case versionShape.MatchString(head.APIVersion):
		return &UnavailableError{DetailCode: reasons.SchemaMajor, Cause: errors.New("apiVersion is not " + Version)}
	}
	return unavailable(errors.New("apiVersion is missing or malformed"))
}

func (w *wire) document() (*Document, error) {
	generated, err := parseStamp(w.GeneratedAt)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(exitcode.Severities, w.Overall) {
		return nil, errors.New("overall is not a severity")
	}
	vantage, err := checkVantage(w.Vantage)
	if err != nil {
		return nil, err
	}
	hosts, err := checkHosts(w.Hosts)
	if err != nil {
		return nil, err
	}
	doc := &Document{GeneratedAt: generated, Overall: w.Overall, Vantage: vantage, Hosts: hosts}
	if err := doc.readHeader(w); err != nil {
		return nil, err
	}
	if err := doc.checkHeader(); err != nil {
		return nil, err
	}
	return doc, nil
}

// parseStamp wants RFC 3339 in UTC. The pattern refuses the offsets and the
// comma fraction that time.Parse accepts; Parse refuses impossible dates.
func parseStamp(value string) (time.Time, error) {
	if !stampPattern.MatchString(value) {
		return time.Time{}, errors.New("generated_at is not an RFC 3339 UTC time")
	}
	at, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("generated_at: %w", err)
	}
	return at, nil
}

func checkVantage(v *Vantage) (Vantage, error) {
	if v == nil || !slugPattern.MatchString(v.ID) {
		return Vantage{}, errors.New("vantage.id is not a slug")
	}
	if !slices.Contains(vantageStatuses, v.Status) {
		return Vantage{}, errors.New("vantage.status is not a status")
	}
	return *v, nil
}

// checkHosts wants at least one host, each once: a verdict rests on an
// observation, and two entries for one host would disagree about it.
func checkHosts(in []wireHost) ([]Host, error) {
	if len(in) == 0 {
		return nil, errors.New("hosts is empty")
	}
	out := make([]Host, 0, len(in))
	seen := make(map[string]bool, len(in))
	for i, h := range in {
		if err := h.check(); err != nil {
			return nil, fmt.Errorf("hosts[%d]: %w", i, err)
		}
		if seen[h.ID] {
			return nil, fmt.Errorf("hosts[%d]: the host is listed twice", i)
		}
		seen[h.ID] = true
		out = append(out, Host{ID: h.ID, Severity: h.Severity, Reasons: h.Reasons, Source: Source})
	}
	return out, nil
}

// check holds a host to the shapes the summary will require of it. A reason
// code is the producer's own word, checked for shape and never against a list.
func (h wireHost) check() error {
	if !slugPattern.MatchString(h.ID) {
		return errors.New("id is not a slug")
	}
	if !slices.Contains(exitcode.Severities, h.Severity) {
		return errors.New("severity is not a severity")
	}
	if h.Reasons == nil {
		return errors.New("reasons is missing")
	}
	seen := make(map[string]bool, len(h.Reasons))
	for _, code := range h.Reasons {
		if !reasons.Valid(code) || seen[code] {
			return errors.New("reasons holds a malformed or repeated code")
		}
		seen[code] = true
	}
	return nil
}

func (d *Document) readHeader(w *wire) (err error) {
	if d.Observed, err = optional[bool]("observed", w.Observed); err != nil {
		return err
	}
	if d.ValidFor, err = optional[int]("valid_for", w.ValidFor); err != nil {
		return err
	}
	declaration := w.Declaration
	if declaration.Kind() == jsontext.KindNull {
		declaration = nil
	}
	d.Declaration, err = optional[string]("declaration", declaration)
	return err
}

// optional decodes a member that may be left out. Absent is nil; null is not a
// way of leaving it out.
func optional[T any](name string, raw jsontext.Value) (*T, error) {
	if raw == nil {
		return nil, nil
	}
	if raw.Kind() == jsontext.KindNull {
		return nil, fmt.Errorf("%s is null", name)
	}
	value := new(T)
	if err := json.Unmarshal(raw, value); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return value, nil
}

// checkHeader refuses, among the malformed values, a GREEN that says it
// observed nothing: silence is never shown as health.
func (d *Document) checkHeader() error {
	if d.ValidFor != nil && *d.ValidFor < 1 {
		return errors.New("valid_for is less than 1")
	}
	if d.Declaration != nil && !digestPattern.MatchString(*d.Declaration) {
		return errors.New("declaration is not a sha256 digest")
	}
	if d.Observed != nil && !*d.Observed && d.Overall == exitcode.SeverityGreen {
		return errors.New("observed is false and overall is GREEN")
	}
	return nil
}
