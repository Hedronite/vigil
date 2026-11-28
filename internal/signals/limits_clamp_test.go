package signals

import "testing"

func TestBelowMinTTLBoundaries(t *testing.T) {
	if !belowMinTTL(59) {
		t.Fatal("59 is below minimum")
	}
	if belowMinTTL(60) {
		t.Fatal("60 is inclusive minimum, not below")
	}
	if belowMinTTL(61) {
		t.Fatal("61 is above minimum")
	}
}

func TestAboveMaxTTLBoundaries(t *testing.T) {
	if aboveMaxTTL(3599) {
		t.Fatal("3599 is below maximum")
	}
	if aboveMaxTTL(3600) {
		t.Fatal("3600 is inclusive maximum, not above")
	}
	if !aboveMaxTTL(3601) {
		t.Fatal("3601 exceeds maximum")
	}
}
