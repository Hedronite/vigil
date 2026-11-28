package signals

import (
	"fmt"
	"strings"
	"testing"
)

func TestDecodePostNote281BytesRefused(t *testing.T) {
	note := strings.Repeat("a", MaxNoteBytes+1)
	body := []byte(fmt.Sprintf(
		`{"apiVersion":"vigil.signal/v0","source":"a","kind":"b","severity":"info","note":%q}`,
		note,
	))
	if len(note) != 281 {
		t.Fatalf("note len = %d", len(note))
	}
	if _, err := DecodePost(body); err == nil {
		t.Fatal("281-byte note should be refused")
	}
}

func TestDecodePostNote280BytesOk(t *testing.T) {
	note := strings.Repeat("a", MaxNoteBytes)
	body := []byte(fmt.Sprintf(
		`{"apiVersion":"vigil.signal/v0","source":"a","kind":"b","severity":"info","note":%q}`,
		note,
	))
	if _, err := DecodePost(body); err != nil {
		t.Fatalf("280 bytes: %v", err)
	}
}
