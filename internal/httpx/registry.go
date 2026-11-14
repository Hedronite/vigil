package httpx

import (
	"sync"
)

// The two-in-flight bound belongs to a backend, not to a Client value. A
// command that built a fresh Client per call would get a fresh semaphore per
// call, and three concurrent queries would all reach the wire. ForBackend
// closes that gap: one Client per backend id for the life of the process, so
// every caller of that backend shares one semaphore.

var registry = struct {
	mu      sync.Mutex
	clients map[string]*Client
}{clients: map[string]*Client{}}

// ForBackend returns the process-wide Client for the backend id, building it
// from cfg on the first call. Later calls return the same Client, and their
// cfg is ignored: the first configuration wins. Per-call limits (Request.Timeout
// and Request.MaxBody) still apply to each call, so callers with different
// deadlines or caps share a backend without sharing a configuration.
//
// A configuration that fails to build is an error and is not remembered, so a
// later call with a good one succeeds. ForBackend is safe for concurrent use.
func ForBackend(id string, cfg Config) (*Client, error) {
	return forBackend(id, cfg, nil)
}

// forBackend is ForBackend with the test hook for the dialer.
func forBackend(id string, cfg Config, dial dialFunc) (*Client, error) {
	if id == "" {
		return nil, &Error{Code: "invalid_backend"}
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if c, ok := registry.clients[id]; ok {
		return c, nil
	}
	c, err := newClient(cfg, dial)
	if err != nil {
		return nil, err
	}
	registry.clients[id] = c
	return c, nil
}

// resetRegistry forgets every Client. Only tests call it.
func resetRegistry() {
	registry.mu.Lock()
	registry.clients = map[string]*Client{}
	registry.mu.Unlock()
}
