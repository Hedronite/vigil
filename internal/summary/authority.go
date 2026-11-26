package summary

import (
	"errors"
	"slices"
	"time"

	"github.com/Hedronite/vigil/internal/exitcode"
	"github.com/Hedronite/vigil/internal/reasons"
	"github.com/Hedronite/vigil/internal/simplexdoc"
)

// Authority is one configured authority and what reading it gave.
type Authority struct {
	// ID is the config id, which is also the source kind.
	ID string
	// StaleAfter is the config's age bound for this authority.
	StaleAfter time.Duration
	// Document is what was read, or nil when nothing usable was.
	Document *AuthorityDocument
	// DetailCode says why Document is nil. Empty means source_unavailable.
	DetailCode string
}

// AuthorityDocument is what any authority's document says about itself.
type AuthorityDocument struct {
	Severity string
	// Reasons are the authority's own verdict-level codes, passed through.
	Reasons     []string
	GeneratedAt time.Time
	// ReceivedAt is when Vigil first saw the document.
	ReceivedAt  time.Time
	ValidFor    *int
	Declaration *string
	// Vantage and Hosts are read from Simplex only.
	Vantage *simplexdoc.Vantage
	Hosts   []simplexdoc.Host
}

// FromSimplex turns the reader's answer into the Simplex authority. Any error
// means no document; a caller whose context ended should not get this far.
func FromSimplex(seen *simplexdoc.Observation, err error, staleAfter time.Duration) Authority {
	a := Authority{ID: simplexdoc.Source, StaleAfter: staleAfter}
	var refused *simplexdoc.UnavailableError
	switch {
	case errors.As(err, &refused):
		a.DetailCode = refused.DetailCode
	case err != nil || seen == nil || seen.Document == nil:
		a.DetailCode = reasons.SourceUnavailable
	default:
		d := seen.Document
		a.Document = &AuthorityDocument{
			Severity:    d.Overall,
			GeneratedAt: d.GeneratedAt,
			ReceivedAt:  seen.ReceivedAt,
			ValidFor:    d.ValidFor,
			Declaration: d.Declaration,
			Vantage:     &d.Vantage,
			Hosts:       d.Hosts,
		}
	}
	return a
}

// usable says whether a document can carry a verdict. The readers refuse
// anything else already; this keeps a made-up colour out if one did not.
func (d *AuthorityDocument) usable() bool {
	return d != nil && slices.Contains(exitcode.Severities, d.Severity) && (d.ValidFor == nil || *d.ValidFor >= 1)
}

// judge gives an authority its verdict and its source row.
func judge(a Authority, skewBudget time.Duration, now time.Time) (Verdict, Source) {
	if !a.Document.usable() {
		return missing(a)
	}
	d := a.Document
	// A sighting cannot be later than now, whatever the caller was told.
	received := d.ReceivedAt
	if received.After(now) {
		received = now
	}
	observed, sighted, severity := stamp(d.GeneratedAt), stamp(received), d.Severity

	verdict := Verdict{
		Source: a.ID, State: reasons.StateOK, Severity: &severity, Reasons: append([]string{}, d.Reasons...),
		ObservedAt: &observed, ReceivedAt: &sighted, ValidFor: d.ValidFor, Declaration: d.Declaration,
	}
	row := Source{
		ID: a.ID, Kind: a.ID, State: reasons.StateOK, ObservedAt: &observed, LastOK: &observed,
		ReceivedAt: &sighted, ValidFor: d.ValidFor, Declaration: d.Declaration,
	}
	if !fresh(d, received, a.StaleAfter, skewBudget, now) {
		code := reasons.SourceStale
		verdict.State, row.State, row.DetailCode = reasons.StateStale, reasons.StateStale, &code
		verdict.Reasons = append(verdict.Reasons, reasons.SourceStale)
	}
	if received.Sub(d.GeneratedAt).Abs() > skewBudget {
		verdict.Reasons = append(verdict.Reasons, reasons.ClockSkew)
	}
	return verdict, row
}

// missing is an authority with no verdict. Its row says why.
func missing(a Authority) (Verdict, Source) {
	code := a.DetailCode
	if code == "" {
		code = reasons.SourceUnavailable
	}
	verdict := Verdict{Source: a.ID, State: reasons.StateUnavailable, Reasons: []string{reasons.AuthorityUnavailable}}
	row := Source{ID: a.ID, Kind: a.ID, State: reasons.StateUnavailable, DetailCode: &code}
	return verdict, row
}

// fresh holds a document to both age bounds. The first runs from Vigil's own
// sighting, so a producer clock that runs ahead buys no life. The second runs
// from generated_at, with the skew budget, so a stuck producer clock is caught.
func fresh(d *AuthorityDocument, received time.Time, staleAfter, skewBudget time.Duration, now time.Time) bool {
	life := lifetime(d.ValidFor, staleAfter)
	return now.Sub(received) <= life && now.Sub(d.GeneratedAt) <= life+skewBudget
}

// lifetime is the smaller of the producer's valid_for and stale_after. The
// comparison is in whole seconds so that a huge valid_for cannot overflow.
func lifetime(validFor *int, staleAfter time.Duration) time.Duration {
	if validFor != nil && time.Duration(*validFor) <= staleAfter/time.Second {
		return time.Duration(*validFor) * time.Second
	}
	return staleAfter
}
