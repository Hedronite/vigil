package signals

import (
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

func TestParseHostAddrInvalid(t *testing.T) {
	if _, err := ParseHostAddr("not-an-addr"); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestParseHostAddrOK(t *testing.T) {
	addr, err := ParseHostAddr("192.0.2.1:1234")
	if err != nil {
		t.Fatal(err)
	}
	if addr.String() != "192.0.2.1" {
		t.Fatalf("addr = %s", addr)
	}
}
