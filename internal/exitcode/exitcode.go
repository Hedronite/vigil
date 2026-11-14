// Package exitcode holds the process exit codes of the vigil binary.
//
// Codes 0 to 2 are verdicts: the document was built and its severity is the
// code. Codes 3 and up are errors: no verdict was produced. No error path may
// ever exit 0, 1 or 2. Only the split "0-2 verdict, 3+ error" is shared across
// the family; each binary prints its own table.
package exitcode

// Process exit codes.
const (
	// Green is a GREEN verdict.
	Green = 0
	// Yellow is a YELLOW verdict. A stale authority never yields less.
	Yellow = 1
	// Red is a RED verdict.
	Red = 2
	// Usage is a usage or configuration error.
	Usage = 3
	// Unavailable means a source or backend could not be read or reached,
	// so no verdict exists.
	Unavailable = 4
	// Startup is an auth, startup or bind failure.
	Startup = 5
	// Internal is an internal failure, including a recovered panic.
	Internal = 6
)

// Severity names as they appear in documents.
const (
	SeverityGreen  = "GREEN"
	SeverityYellow = "YELLOW"
	SeverityRed    = "RED"
)

// Severities lists the three severity names in ascending order.
var Severities = []string{SeverityGreen, SeverityYellow, SeverityRed}

// Entry describes one exit code for tables and drift tests.
type Entry struct {
	Code    int
	Name    string
	Meaning string
}

// Table is the full exit-code table, ordered by code.
var Table = []Entry{
	{Green, "GREEN", "verdict GREEN"},
	{Yellow, "YELLOW", "verdict YELLOW (a stale source is at least this)"},
	{Red, "RED", "verdict RED"},
	{Usage, "usage", "usage or configuration error"},
	{Unavailable, "unavailable", "source unavailable or backend unreachable"},
	{Startup, "startup", "auth, startup or bind failure"},
	{Internal, "internal", "internal error (panic recovered)"},
}

// IsVerdict reports whether code is a verdict code (0 to 2).
func IsVerdict(code int) bool { return code >= Green && code <= Red }

// FromSeverity maps a severity name to its verdict exit code.
func FromSeverity(severity string) (int, bool) {
	switch severity {
	case SeverityGreen:
		return Green, true
	case SeverityYellow:
		return Yellow, true
	case SeverityRed:
		return Red, true
	}
	return 0, false
}
