package signals

import (
	"net"
	"net/http"
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
	oldest := l.stalestPeer()
	l.slots[oldest].addr = netip.Addr{}
	return oldest
}

func (l *Limiter) stalestPeer() int {
	oldest := 0
	for i := 1; i < maxPeers; i++ {
		if l.slots[i].sec < l.slots[oldest].sec {
			oldest = i
		}
	}
	return oldest
}

// RemoteAddr parses the client address from an HTTP request.
func RemoteAddr(r *http.Request) (netip.Addr, error) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}, err
	}
	return netip.ParseAddr(host)
}
