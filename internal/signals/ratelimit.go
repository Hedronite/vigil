package signals

import (
	"net"
	"net/netip"
	"sync"
)

const (
	limitPerSec = 10
	maxPeers    = 256
)

type peerSlot struct {
	addr  netip.Addr
	sec   int64
	count uint8
}

// Limiter allows 10 requests per second per remote, loopback exempt.
// At most maxPeers remotes are tracked.
type Limiter struct {
	mu    sync.Mutex
	slots [maxPeers]peerSlot
}

// Allow reports whether the remote may post another signal at sec.
func (l *Limiter) Allow(addr netip.Addr, sec int64) bool {
	if addr.IsLoopback() {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	i := l.find(addr)
	if i < 0 {
		i = l.claim()
		l.slots[i].addr = addr
		l.slots[i].sec = sec
		l.slots[i].count = 0
	}
	if l.slots[i].sec != sec {
		l.slots[i].sec = sec
		l.slots[i].count = 0
	}
	if l.slots[i].count >= limitPerSec {
		return false
	}
	l.slots[i].count++
	return true
}

func (l *Limiter) find(addr netip.Addr) int {
	for i := range l.slots {
		if l.slots[i].addr == addr {
			return i
		}
	}
	return -1
}

func (l *Limiter) claim() int {
	for i := range l.slots {
		if !l.slots[i].addr.IsValid() {
			return i
		}
	}
	i := oldestPeer(l.slots)
	l.slots[i].addr = netip.Addr{}
	return i
}

func oldestPeer(slots [maxPeers]peerSlot) int {
	oldest := -1
	for i := range slots {
		if !slots[i].addr.IsValid() {
			continue
		}
		if oldest < 0 || slots[i].sec < slots[oldest].sec {
			oldest = i
		}
	}
	return oldest
}

// ParseHostAddr splits a host:port remote address.
func ParseHostAddr(remote string) (netip.Addr, error) {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return netip.Addr{}, err
	}
	return netip.ParseAddr(host)
}
