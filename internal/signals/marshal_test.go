package signals

import "testing"

func TestMarshalListRoundTrip(t *testing.T) {
	docs := []Document{{APIVersion: "vigil.signal/v0", ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Source: "d", Kind: "k", Severity: "info", CreatedAt: "2026-01-01T00:00:00Z", TTL: 300}}
	b, err := MarshalList(docs)
	if err != nil || len(b) == 0 {
		t.Fatalf("marshal: %v len=%d", err, len(b))
	}
}
