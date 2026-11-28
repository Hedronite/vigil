package signals

import (
	"testing"
	"time"
)

func TestRingCapacity(t *testing.T) {
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	st := NewStore(func() time.Time { return now })
	for i := 0; i < Capacity+10; i++ {
		_, err := st.Accept(PostFields{
			Source: "deploy", Kind: "k", Severity: "info",
		}, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	if st.LenLive(now) > Capacity {
		t.Fatalf("live = %d, want at most %d", st.LenLive(now), Capacity)
	}
}

func TestAcceptStoresNote(t *testing.T) {
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	st := NewStore(func() time.Time { return now })
	doc, err := st.Accept(PostFields{
		Source: "deploy", Kind: "k", Severity: "info", Note: "hi",
	}, now)
	if err != nil || doc.Note != "hi" {
		t.Fatalf("doc = %+v err %v", doc, err)
	}
}

func TestNewStoreDefaultClock(t *testing.T) {
	st := NewStore(nil)
	_, err := st.Accept(PostFields{Source: "a", Kind: "b", Severity: "info"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
}

func TestExpiryPrune(t *testing.T) {
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	st := NewStore(func() time.Time { return now })
	_, err := st.Accept(PostFields{Source: "a", Kind: "b", Severity: "info", TTL: 60}, now)
	if err != nil {
		t.Fatal(err)
	}
	later := now.Add(61 * time.Second)
	if st.LenLive(later) != 0 {
		t.Fatalf("expired signal still live")
	}
}