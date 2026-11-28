package signals

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDecodeFixtures(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "signal")
	for _, name := range []string{"valid/minimal.json"} {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodePost(b); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for _, name := range []string{
		"invalid/unknown-field.json", "invalid/severity-verdict.json",
		"invalid/note-control.json",
	} {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodePost(b); err == nil {
			t.Fatalf("%s should be refused", name)
		}
	}
}

func TestDecodeRejectsAssignedFields(t *testing.T) {
	body := []byte(`{"apiVersion":"vigil.signal/v0","source":"x","kind":"y","severity":"info","id":"01234567890123456789012345678901"}`)
	if _, err := DecodePost(body); err == nil {
		t.Fatal("id in request should be refused")
	}
}