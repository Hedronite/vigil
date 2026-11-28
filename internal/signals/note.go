package signals

import (
	"unicode/utf8"
)

// NoteOK reports whether note satisfies the byte and character refusals (#2, #6).
func NoteOK(note string) bool {
	if len(note) > MaxNoteBytes {
		return false
	}
	if !utf8.ValidString(note) {
		return false
	}
	for _, r := range note {
		if forbiddenRune(r) {
			return false
		}
	}
	return true
}

func forbiddenRune(r rune) bool {
	if r < 0x20 || r == 0x7f {
		return true
	}
	if r >= 0x80 && r <= 0x9f {
		return true
	}
	return r == '\u2028' || r == '\u2029'
}