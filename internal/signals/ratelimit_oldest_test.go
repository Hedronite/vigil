package signals

import (
	"net/netip"
	"testing"
)

func TestOldestPeerPicksLowestSec(t *testing.T) {
	var slots [maxPeers]peerSlot
	slots[0] = peerSlot{addr: netip.MustParseAddr("192.0.2.1"), sec: 5}
	slots[1] = peerSlot{addr: netip.MustParseAddr("192.0.2.2"), sec: 2}
	if got := oldestPeer(slots); got != 1 {
		t.Fatalf("oldest = %d, want 1", got)
	}
}