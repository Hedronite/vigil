package signals

import "time"

const (
	// MaxBody is the largest POST body accepted for a signal.
	MaxBody = 4 << 10
	// MaxNoteBytes is the note size limit (#6), measured in bytes.
	MaxNoteBytes = 280
	// Capacity is the fixed ring size.
	Capacity   = 256
	defaultTTL = 5 * time.Minute
	minTTL     = 60 * time.Second
	maxTTL     = time.Hour
)

const (
	minTTLSeconds = 60
	maxTTLSeconds = 3600
)

// ClampTTL returns the stored TTL duration, defaulting and clamping per semantics §8.
func ClampTTL(seconds int) time.Duration {
	if seconds == 0 {
		return defaultTTL
	}
	if seconds < minTTLSeconds {
		return minTTL
	}
	if seconds > maxTTLSeconds {
		return maxTTL
	}
	return time.Duration(seconds) * time.Second
}
