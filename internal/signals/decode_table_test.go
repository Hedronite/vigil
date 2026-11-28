package signals

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDecodeInvalidPostFixtures(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "signal", "invalid")
	names := []string{
		"unknown-field.json", "severity-verdict.json", "note-control.json",
		"note-line-separator.json", "note-nel.json", "note-paragraph-separator.json",
		"note-too-long.json",
	}
	for _, name := range names {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodePost(b); err == nil {
			t.Fatalf("%s: want refuse", name)
		}
	}
}