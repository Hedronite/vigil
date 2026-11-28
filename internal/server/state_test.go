package server

import (
	"testing"
	"time"
)

func TestReceivedAtTracksIdentity(t *testing.T) {
	st := NewState()
	seen := time.Date(2026, 11, 13, 18, 0, 30, 0, time.UTC)
	body := []byte(`{"apiVersion":"simplex.probe/v0"}`)
	gen := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	first := st.ReceivedAt("simplex", gen, body, seen)
	second := st.ReceivedAt("simplex", gen, body, seen.Add(time.Minute))
	if !first.Equal(second) {
		t.Fatalf("same identity: %v vs %v", first, second)
	}
	newBody := append(body, ' ')
	third := st.ReceivedAt("simplex", gen, newBody, seen.Add(2*time.Minute))
	if third.Equal(first) {
		t.Fatal("new body digest should open a new sighting")
	}
}
