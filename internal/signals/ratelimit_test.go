package signals

import (
	"net/netip"
	"testing"
)

func TestLimiterLoopbackExempt(t *testing.T) {
	var l Limiter
	addr := netip.MustParseAddr("127.0.0.1")
	for i := 0; i < 20; i++ {
		if !l.Allow(addr, 1) {
			t.Fatal("loopback should be exempt")
		}
	}
}

func TestLimiterEleventhRequestInSecond(t *testing.T) {
	var l Limiter
	addr := netip.MustParseAddr("192.0.2.9")
	const sec int64 = 77
	for i := 0; i < limitPerSec; i++ {
		if !l.Allow(addr, sec) {
			t.Fatalf("request %d should pass", i+1)
		}
	}
	if l.Allow(addr, sec) {
		t.Fatal("11th request in the same second must be refused")
	}
}

func TestLimiterCapsRemote(t *testing.T) {
	var l Limiter
	addr := netip.MustParseAddr("192.0.2.1")
	for i := 0; i < limitPerSec; i++ {
		if !l.Allow(addr, 42) {
			t.Fatalf("request %d should pass", i)
		}
	}
	if l.Allow(addr, 42) {
		t.Fatal("11th request in the same second should fail")
	}
	if !l.Allow(addr, 43) {
		t.Fatal("new second should reset")
	}
}
