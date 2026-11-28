package signals

import (
	"strings"
	"testing"
)

func TestNoteByteLimits(t *testing.T) {
	if !NoteOK(strings.Repeat("a", 280)) {
		t.Fatal("280 ASCII should be accepted")
	}
	if NoteOK(strings.Repeat("a", 281)) {
		t.Fatal("281 ASCII should be refused")
	}
	twoByte := strings.Repeat("\u00e9", 140)
	if len(twoByte) != 280 {
		t.Fatalf("fixture len = %d, want 280 bytes", len(twoByte))
	}
	if !NoteOK(twoByte) {
		t.Fatal("140 two-byte runes (280 bytes) should be accepted")
	}
	tooMany := strings.Repeat("\u00e9", 141)
	if len(tooMany) != 282 {
		t.Fatalf("fixture len = %d, want 282 bytes", len(tooMany))
	}
	if NoteOK(tooMany) {
		t.Fatal("141 two-byte runes should be refused")
	}
}

func TestNoteRefusals(t *testing.T) {
	bad := []string{
		"a\u0000b", "a\u007fb", "a\u0080b", "a\u2028b", "a\u2029b",
	}
	for _, note := range bad {
		if NoteOK(note) {
			t.Fatalf("note %q should be refused", note)
		}
	}
}