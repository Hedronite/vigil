package schematest_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// recordedDigest is every schema_sha256 line of a SOURCE file.
var recordedDigest = regexp.MustCompile(`(?m)^schema_sha256:(.*)$`)

// TestVendoredSimplexSchemaDigest: the vendored schema has the digest its
// SOURCE records, so an edit to the copy fails here without a network.
// scripts/check-vendor.sh compares the same files with upstream.
func TestVendoredSimplexSchemaDigest(t *testing.T) {
	root := repoRoot(t)
	source, err := os.ReadFile(filepath.Join(root, "testdata", "simplex", "SOURCE"))
	if err != nil {
		t.Fatalf("read SOURCE: %v", err)
	}
	recorded := recordedDigest.FindAllSubmatch(source, -1)
	if len(recorded) != 1 {
		t.Fatalf("SOURCE records schema_sha256 %d times, want once", len(recorded))
	}
	schema, err := os.ReadFile(filepath.Join(root, "schema", "simplex.probe.v0.schema.json"))
	if err != nil {
		t.Fatalf("read the vendored schema: %v", err)
	}
	sum := sha256.Sum256(schema)
	if got, want := hex.EncodeToString(sum[:]), strings.TrimSpace(string(recorded[0][1])); got != want {
		t.Fatalf("the vendored schema has sha256 %s, SOURCE records %q", got, want)
	}
}
