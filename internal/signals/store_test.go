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
	atExpiry := now.Add(60 * time.Second)
	if st.LenLive(atExpiry) != 0 {
		t.Fatalf("signal at expiry should be dropped")
	}
	later := now.Add(61 * time.Second)
	if st.LenLive(later) != 0 {
		t.Fatalf("expired signal still live")
	}
}

func TestLenLiveCounts(t *testing.T) {
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	st := NewStore(func() time.Time { return now })
	if st.LenLive(now) != 0 {
		t.Fatal("empty store")
	}
	for i := 0; i < 3; i++ {
		if _, err := st.Accept(PostFields{Source: "a", Kind: "b", Severity: "info"}, now); err != nil {
			t.Fatal(err)
		}
	}
	if st.LenLive(now) != 3 {
		t.Fatalf("live = %d, want 3", st.LenLive(now))
	}
}

func TestAcceptTTLSeconds(t *testing.T) {
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	st := NewStore(func() time.Time { return now })
	doc, err := st.Accept(PostFields{Source: "a", Kind: "b", Severity: "info", TTL: 120}, now)
	if err != nil || doc.TTL != 120 {
		t.Fatalf("ttl = %d err %v", doc.TTL, err)
	}
}
