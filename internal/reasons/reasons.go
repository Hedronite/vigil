// Package reasons holds the vocabulary Vigil owns: its own reason codes, the
// V1 backend detail codes, the pass-through pattern for reasons that
// authorities publish, and the source states.
//
// Authorities (Simplex, Augur) own their reason strings. Vigil passes them
// through and checks only the shape, so an upstream addition never breaks it.
package reasons

import "regexp"

// Vigil's own reason codes.
const (
	SourceStale          = "source_stale"
	SourceUnavailable    = "source_unavailable"
	SchemaMajor          = "schema_major"
	ClockSkew            = "clock_skew"
	AuthorityUnavailable = "authority_unavailable"
)

// Own lists Vigil's own reason codes.
var Own = []string{
	SourceStale,
	SourceUnavailable,
	SchemaMajor,
	ClockSkew,
	AuthorityUnavailable,
}

// Backend and query detail codes (V1). They say why a backend call failed, in
// sources[].detail_code and in vigil.error/v0. They are never addresses,
// never URLs and never text copied from a backend.
const (
	// Unreachable: the connection failed (refused, reset, no route).
	Unreachable = "unreachable"
	// Timeout: the per-call deadline passed.
	Timeout = "timeout"
	// BodyCap: the response body was larger than the cap for the call.
	BodyCap = "body_cap"
	// RedirectRefused: the backend answered with a redirect, which Vigil
	// never follows.
	RedirectRefused = "redirect_refused"
	// HTTPStatus: the backend answered with a non-2xx status.
	HTTPStatus = "http_status"
	// SeriesCap: a query matched more series than the configured cap.
	SeriesCap = "series_cap"
	// QueryWarnings: the backend reported a warning, so the answer could be
	// partial.
	QueryWarnings = "query_warnings"
	// QueryRejected: the backend rejected a configured expression (bad_data).
	QueryRejected = "query_rejected"
	// NotHealth: the body was not a health document (a login page, say).
	NotHealth = "not_health"
	// NoSeries: a source whose data is expected is absent. An empty query
	// result is not this: it is an answer with no series.
	NoSeries = "no_series"
	// MissingHost: a query that names {{.host}} was run without --host.
	MissingHost = "missing_host"
	// UnexpectedHost: --host was given to a query with no {{.host}}.
	UnexpectedHost = "unexpected_host"
)

// Detail lists the V1 backend detail codes.
var Detail = []string{
	Unreachable,
	Timeout,
	BodyCap,
	RedirectRefused,
	HTTPStatus,
	SeriesCap,
	QueryWarnings,
	QueryRejected,
	NotHealth,
	NoSeries,
	MissingHost,
	UnexpectedHost,
}

// Pattern is the shape every reason string must match, Vigil's own and
// pass-through alike.
const Pattern = `^[a-z][a-z0-9_]{0,47}$`

var pattern = regexp.MustCompile(Pattern)

// Valid reports whether s has the shape of a reason code.
func Valid(s string) bool { return pattern.MatchString(s) }

// Source and verdict states.
const (
	StateOK          = "ok"
	StateStale       = "stale"
	StateUnavailable = "unavailable"
)

// States lists the source states in documents.
var States = []string{StateOK, StateStale, StateUnavailable}
