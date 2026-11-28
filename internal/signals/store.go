package signals

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

// Document is a stored vigil.signal/v0.
type Document struct {
	APIVersion string `json:"apiVersion"`
	ID         string `json:"id"`
	Source     string `json:"source"`
	Kind       string `json:"kind"`
	Severity   string `json:"severity"`
	CreatedAt  string `json:"created_at"`
	TTL        int    `json:"ttl"`
	Note       string `json:"note,omitempty"`
}

type slot struct {
	doc     Document
	expires time.Time
	live    bool
}

// Store is a fixed 256-entry ring, never persisted.
type Store struct {
	mu    sync.RWMutex
	ring  [Capacity]slot
	next  int
	clock func() time.Time
}

// NewStore returns an empty ring using clock for timestamps.
func NewStore(clock func() time.Time) *Store {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Store{clock: clock}
}

// Accept stores a new signal and returns the assigned document.
func (s *Store) Accept(fields PostFields, now time.Time) (Document, error) {
	id, err := newID()
	if err != nil {
		return Document{}, err
	}
	ttl := ClampTTL(fields.TTL)
	ttlSec := int(ttl / time.Second)
	doc := Document{
		APIVersion: "vigil.signal/v0",
		ID:         id,
		Source:     fields.Source,
		Kind:       fields.Kind,
		Severity:   fields.Severity,
		CreatedAt:  formatTime(now),
		TTL:        ttlSec,
	}
	if fields.Note != "" {
		doc.Note = fields.Note
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ring[s.next] = slot{doc: doc, expires: now.Add(ttl), live: true}
	s.next = (s.next + 1) % Capacity
	return doc, nil
}

// Live returns unexpired signals in arbitrary stable ring order.
func (s *Store) Live(now time.Time) []Document {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropExpired(now)
	out := make([]Document, 0, Capacity)
	for i := range s.ring {
		if s.ring[i].live {
			out = append(out, s.ring[i].doc)
		}
	}
	return out
}

// LenLive returns how many unexpired entries are stored.
func (s *Store) LenLive(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropExpired(now)
	n := 0
	for i := range s.ring {
		if s.ring[i].live {
			n++
		}
	}
	return n
}

// Prune drops expired entries.
func (s *Store) Prune(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropExpired(now)
}

func (s *Store) dropExpired(now time.Time) {
	for i := range s.ring {
		if s.ring[i].live && !now.Before(s.ring[i].expires) {
			s.ring[i].live = false
		}
	}
}

func newID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func formatTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05Z")
}

// MarshalList JSON-encodes a signal list with a trailing newline.
func MarshalList(docs []Document) ([]byte, error) {
	b, err := json.Marshal(docs)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
