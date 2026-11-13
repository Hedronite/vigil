// Package reasons holds the vocabulary Vigil owns: its own reason codes, the
// pass-through pattern for reasons that authorities publish, and the source
// states.
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
