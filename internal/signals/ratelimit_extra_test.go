package signals

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestLimiterPeerEviction(t *testing.T) {
	var l Limiter
	for i := 0; i < maxPeers+2; i++ {
		addr := netip.AddrFrom4([4]byte{192, 0, 2, byte(i)})
		if !l.Allow(addr, 100) {
			t.Fatalf("peer %d blocked unexpectedly", i)
		}
	}
}

func TestRemoteAddrInvalid(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "not-an-addr"
	if _, err := RemoteAddr(req); err == nil {
		t.Fatal("expected parse error")
	}
}