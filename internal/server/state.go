package server

import (
	"crypto/sha256"
	"sync"
	"time"
)

// State holds in-memory serve sighting times for file authorities.
type State struct {
	mu        sync.Mutex
	sightings map[sightKey]time.Time
}

type sightKey struct {
	authority string
	generated time.Time
	body      [32]byte
}

// NewState returns an empty serve state.
func NewState() *State {
	return &State{sightings: map[sightKey]time.Time{}}
}

// ReceivedAt stores and returns the first sighting time of a document identity.
func (s *State) ReceivedAt(authority string, generated time.Time, body []byte, sighted time.Time) time.Time {
	sum := sha256.Sum256(body)
	key := sightKey{authority: authority, generated: generated.UTC(), body: sum}
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.sightings[key]; ok {
		return t.UTC()
	}
	t := sighted.UTC()
	s.sightings[key] = t
	return t
}
