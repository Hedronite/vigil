package signals

import (
	"net/netip"
	"testing"
)

func TestOldestPeerPicksLowestSec(t *testing.T) {
	var slots [maxPeers]peerSlot
	slots[0] = peerSlot{addr: netip.MustParseAddr("192.0.2.1"), sec: 5}
	slots[1] = peerSlot{addr: netip.MustParseAddr("192.0.2.2"), sec: 2}
	slots[2] = peerSlot{addr: netip.MustParseAddr("192.0.2.3"), sec: 2}
	if got := oldestPeer(slots); got != 1 {
		t.Fatalf("oldest = %d, want 1", got)
	}
}

func TestOldestPeerSkipsEmptySlots(t *testing.T) {
	var slots [maxPeers]peerSlot
	slots[3] = peerSlot{addr: netip.MustParseAddr("192.0.2.4"), sec: 9}
	if got := oldestPeer(slots); got != 3 {
		t.Fatalf("oldest = %d, want 3", got)
	}
}

func TestOldestPeerIgnoresInvalidWithLowSec(t *testing.T) {
	var slots [maxPeers]peerSlot
	// slot 0 invalid, sec 0 by default — must not beat a valid peer
	slots[2] = peerSlot{addr: netip.MustParseAddr("192.0.2.3"), sec: 9}
	if got := oldestPeer(slots); got != 2 {
		t.Fatalf("oldest = %d, want 2", got)
	}
}

func TestOldestPeerEqualSecKeepsLowerIndex(t *testing.T) {
	var slots [maxPeers]peerSlot
	slots[1] = peerSlot{addr: netip.MustParseAddr("192.0.2.2"), sec: 7}
	slots[4] = peerSlot{addr: netip.MustParseAddr("192.0.2.5"), sec: 7}
	if got := oldestPeer(slots); got != 1 {
		t.Fatalf("oldest = %d, want 1", got)
	}
}
