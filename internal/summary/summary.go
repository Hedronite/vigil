// Package summary combines what the authorities published into one
// vigil.summary/v0 document.
//
// It is pure: the clock is an argument and nothing is read, so the same input
// at the same instant gives the same document. It takes each authority's colour
// as published and applies only the caps of docs/semantics.md. It never derives
// a colour from a host, a check, a backend or an informational source.
package summary

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/Hedronite/vigil/internal/exitcode"
	"github.com/Hedronite/vigil/internal/reasons"
	"github.com/Hedronite/vigil/internal/simplexdoc"
)

// Version is the document this package produces.
const Version = "vigil.summary/v0"

// Input is everything a summary is built from. Durations come from the config.
type Input struct {
	// Authorities are the configured ones, in config order. The document needs
	// at least one.
	Authorities []Authority
	// Sources are the rows that are not authorities: backends and informational
	// sources. A scry or cubit row in state ok is also a witness for drift.
	Sources []Source
	// SkewBudget is how far generated_at and received_at may differ.
	SkewBudget time.Duration
	// Declaration is Vigil's own, the reference for drift, or nil.
	Declaration *string
	// VigilDeclChangedAt is when that declaration last changed, or nil when the
	// caller has no memory of it, which opens no grace window.
	VigilDeclChangedAt *time.Time
	// DeclarationGrace is how long after a change drift is only listed.
	DeclarationGrace time.Duration
	// Augur, Cubit and Scry are the data blocks, carried as given. Nothing in
	// them is read.
	Augur, Cubit, Scry json.RawMessage
}

// Document is vigil.summary/v0.
type Document struct {
	APIVersion       string              `json:"apiVersion"`
	GeneratedAt      string              `json:"generated_at"`
	Overall          *string             `json:"overall"`
	Verdicts         []Verdict           `json:"verdicts"`
	Vantage          *simplexdoc.Vantage `json:"vantage,omitempty"`
	Hosts            []simplexdoc.Host   `json:"hosts"`
	Sources          []Source            `json:"sources"`
	Augur            json.RawMessage     `json:"augur,omitempty"`
	Cubit            json.RawMessage     `json:"cubit,omitempty"`
	Scry             json.RawMessage     `json:"scry,omitempty"`
	Declaration      *string             `json:"declaration"`
	DeclarationDrift *Drift              `json:"declaration_drift"`
}

// Verdict is one authority's entry. Severity is what it published, or nil when
// it could not be read.
type Verdict struct {
	Source      string   `json:"source"`
	State       string   `json:"state"`
	Severity    *string  `json:"severity"`
	Reasons     []string `json:"reasons"`
	ObservedAt  *string  `json:"observed_at"`
	ReceivedAt  *string  `json:"received_at,omitempty"`
	ValidFor    *int     `json:"valid_for,omitempty"`
	Declaration *string  `json:"declaration,omitempty"`
}

// Source is one row of sources[], where a source's status lives.
type Source struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"`
	State       string  `json:"state"`
	ObservedAt  *string `json:"observed_at"`
	LastOK      *string `json:"last_ok"`
	DetailCode  *string `json:"detail_code,omitempty"`
	Vantage     string  `json:"vantage,omitempty"`
	ReceivedAt  *string `json:"received_at,omitempty"`
	ValidFor    *int    `json:"valid_for,omitempty"`
	Declaration *string `json:"declaration,omitempty"`
}

// Combine builds the document as of now.
func Combine(in Input, now time.Time) *Document {
	doc := &Document{
		APIVersion:  Version,
		GeneratedAt: stamp(now),
		Verdicts:    make([]Verdict, 0, len(in.Authorities)),
		Hosts:       []simplexdoc.Host{},
		Sources:     make([]Source, 0, len(in.Authorities)+len(in.Sources)),
		Augur:       in.Augur,
		Cubit:       in.Cubit,
		Scry:        in.Scry,
		Declaration: in.Declaration,
	}
	for _, a := range in.Authorities {
		verdict, row := judge(a, in.SkewBudget, now)
		doc.Verdicts = append(doc.Verdicts, verdict)
		doc.Sources = append(doc.Sources, row)
		doc.place(a, verdict)
	}
	for _, row := range in.Sources {
		doc.Sources = append(doc.Sources, held(row))
	}
	drift, colouring := compare(in, now, witnesses(doc.Verdicts, doc.Sources))
	doc.DeclarationDrift = drift
	for i := range doc.Verdicts {
		if slices.Contains(colouring, doc.Verdicts[i].Source) {
			doc.Verdicts[i].Reasons = append(doc.Verdicts[i].Reasons, reasons.DeclarationDrift)
		}
	}
	doc.Overall = overall(doc.Verdicts, len(colouring) > 0)
	return doc
}

// place carries the vantage and the hosts, which come from Simplex alone and
// only when its document was read.
func (d *Document) place(a Authority, verdict Verdict) {
	if a.ID != simplexdoc.Source || verdict.State == reasons.StateUnavailable {
		return
	}
	if a.Document.Vantage != nil {
		vantage := *a.Document.Vantage
		d.Vantage = &vantage
	}
	for _, h := range a.Document.Hosts {
		h.Reasons = append([]string{}, h.Reasons...)
		d.Hosts = append(d.Hosts, h)
	}
}

// held holds a row handed in to the schema's rule: an ok row names no detail
// code and an unavailable one always does.
func held(row Source) Source {
	switch row.State {
	case reasons.StateOK:
		row.DetailCode = nil
	case reasons.StateUnavailable:
		if row.DetailCode == nil || *row.DetailCode == "" {
			code := reasons.SourceUnavailable
			row.DetailCode = &code
		}
	}
	return row
}

var severityRank = map[string]int{
	exitcode.SeverityGreen:  0,
	exitcode.SeverityYellow: 1,
	exitcode.SeverityRed:    2,
}

// overall is the worst readable verdict after the caps: a stale one counts as
// at least YELLOW, and so does the whole while an authority is missing or has
// drifted past the grace window. With nothing readable there is no colour.
func overall(verdicts []Verdict, drifted bool) *string {
	floor := severityRank[exitcode.SeverityYellow]
	worst, capped := -1, drifted
	for _, v := range verdicts {
		if v.Severity == nil {
			capped = true
			continue
		}
		rank := severityRank[*v.Severity]
		if v.State == reasons.StateStale {
			rank = max(rank, floor)
		}
		worst = max(worst, rank)
	}
	if worst < 0 {
		return nil
	}
	if capped {
		worst = max(worst, floor)
	}
	colour := exitcode.Severities[worst]
	return &colour
}

// ExitCode is the verdict code of a document: 0, 1 or 2 for its colour and 4
// when there is none. A value that is not a severity is an internal error, never
// a verdict.
func ExitCode(doc *Document) int {
	if doc.Overall == nil {
		return exitcode.Unavailable
	}
	code, known := exitcode.FromSeverity(*doc.Overall)
	if !known {
		return exitcode.Internal
	}
	return code
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
