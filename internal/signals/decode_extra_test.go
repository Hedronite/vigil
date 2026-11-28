package signals

import "testing"

func TestValidateInboundRefuses(t *testing.T) {
	got, err := validateInbound(inbound{
		APIVersion: "vigil.signal/v0", Source: "a", Kind: "b", Severity: "info",
	})
	if err != nil {
		t.Fatalf("empty note: %v", err)
	}
	if got.Note != "" {
		t.Fatal("note should be empty")
	}
	cases := []inbound{
		{APIVersion: "vigil.signal/v1", Source: "a", Kind: "b", Severity: "info"},
		{APIVersion: "vigil.signal/v0", Source: "A", Kind: "b", Severity: "info"},
		{APIVersion: "vigil.signal/v0", Source: "a", Kind: "b", Severity: "GREEN"},
		{APIVersion: "vigil.signal/v0", Source: "a", Kind: "b", Severity: "info", Note: "\x7f"},
		{APIVersion: "vigil.signal/v0", Source: "a", Kind: "b", Severity: "info", Note: "\xff"},
	}
	for i, in := range cases {
		if _, err := validateInbound(in); err == nil {
			t.Fatalf("case %d: want refuse", i)
		}
	}
}
