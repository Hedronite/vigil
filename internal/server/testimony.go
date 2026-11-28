package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Hedronite/vigil/internal/config"
	"github.com/Hedronite/vigil/internal/reasons"
	"github.com/Hedronite/vigil/internal/simplexdoc"
)

// ErrStaleTestimony is returned when a post would regress the stored document.
var ErrStaleTestimony = errors.New("stale_testimony")

// Receipt is vigil.receipt/v0 returned on accept.
type Receipt struct {
	APIVersion  string `json:"apiVersion"`
	Source      string `json:"source"`
	GeneratedAt string `json:"generated_at"`
	ReceivedAt  string `json:"received_at"`
}

// TestimonyStore holds the newest pushed testimony per source.
type TestimonyStore struct {
	mu      sync.Mutex
	entries map[string]storedTestimony
}

type storedTestimony struct {
	generated time.Time
	body      []byte
}

// NewTestimonyStore returns an empty push testimony store.
func NewTestimonyStore() *TestimonyStore {
	return &TestimonyStore{entries: map[string]storedTestimony{}}
}

func formatRFC3339(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05Z")
}

func newReceipt(source string, generated, received time.Time) Receipt {
	return Receipt{
		APIVersion:  "vigil.receipt/v0",
		Source:      source,
		GeneratedAt: formatRFC3339(generated),
		ReceivedAt:  formatRFC3339(received),
	}
}

// Accept stores testimony when it is newer than or equal with the same body.
func (s *TestimonyStore) Accept(source string, body []byte, doc *simplexdoc.Document, st *State, now time.Time) (Receipt, error) {
	incoming := doc.GeneratedAt.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.entries[source]
	if !ok {
		received := st.ReceivedAt(source, incoming, body, now)
		s.entries[source] = storedTestimony{generated: incoming, body: bytes.Clone(body)}
		return newReceipt(source, incoming, received), nil
	}
	curGen := cur.generated.UTC()
	switch {
	case incoming.After(curGen):
		received := st.ReceivedAt(source, incoming, body, now)
		s.entries[source] = storedTestimony{generated: incoming, body: bytes.Clone(body)}
		return newReceipt(source, incoming, received), nil
	case incoming.Before(curGen):
		return Receipt{}, ErrStaleTestimony
	default:
		if bytes.Equal(cur.body, body) {
			received := st.ReceivedAt(source, incoming, body, now)
			return newReceipt(source, incoming, received), nil
		}
		return Receipt{}, ErrStaleTestimony
	}
}

// Observation returns the stored simplex observation for summary, or nil when empty.
func (s *TestimonyStore) Observation(source string, st *State, now time.Time) (*simplexdoc.Observation, error) {
	s.mu.Lock()
	ent, ok := s.entries[source]
	s.mu.Unlock()
	if !ok {
		return nil, nil
	}
	doc, err := simplexdoc.Decode(ent.body)
	if err != nil {
		return nil, err
	}
	received := st.ReceivedAt(source, doc.GeneratedAt, ent.body, now)
	return &simplexdoc.Observation{Document: doc, ReceivedAt: received}, nil
}

func pushAuthorityConfigured(cfg *config.Config, source string) bool {
	if cfg == nil {
		return false
	}
	if source != simplexdoc.Source {
		return false
	}
	for _, a := range cfg.Authorities {
		if a.ID == source && a.ViaEffective() == "push" {
			return true
		}
	}
	return false
}

func testimonyRoutes(opts Options) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPI(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		source := r.PathValue("source")
		if !pushAuthorityConfigured(opts.Config, source) {
			writeAPI(w, http.StatusNotFound, "unknown_source")
			return
		}
		if !tokenAuthorized(bearerToken(r.Header.Get("Authorization")), opts.TestimonyToken) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			writeBody(w, []byte(UnauthorizedBody))
			return
		}
		postTestimony(w, r, opts, source)
	})
}

func postTestimony(w http.ResponseWriter, r *http.Request, opts Options, source string) {
	if opts.Testimony == nil || opts.State == nil {
		writeAPI(w, http.StatusInternalServerError, "internal")
		return
	}
	body, ok := readTestimonyBody(w, r)
	if !ok {
		return
	}
	doc, code, status := DecodeTestimonyBody(body)
	if code != "" {
		writeAPI(w, status, code)
		return
	}
	now := opts.now()
	receipt, err := opts.Testimony.Accept(source, body, doc, opts.State, now)
	if errors.Is(err, ErrStaleTestimony) {
		writeAPI(w, http.StatusConflict, "stale_testimony")
		return
	}
	if err != nil {
		writeAPI(w, http.StatusInternalServerError, "internal")
		return
	}
	b, err := json.Marshal(receipt)
	if err != nil {
		writeAPI(w, http.StatusInternalServerError, "internal")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	writeBody(w, append(b, '\n'))
}

func readTestimonyBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	lim := http.MaxBytesReader(w, r.Body, simplexdoc.MaxBytes+1)
	body, err := io.ReadAll(lim)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeAPI(w, http.StatusRequestEntityTooLarge, reasons.BodyCap)
			return nil, false
		}
		writeAPI(w, http.StatusBadRequest, reasons.SourceUnavailable)
		return nil, false
	}
	if len(body) > simplexdoc.MaxBytes {
		writeAPI(w, http.StatusRequestEntityTooLarge, reasons.BodyCap)
		return nil, false
	}
	return body, true
}

// DecodeTestimonyBody decodes a testimony POST body for accept or fuzzing.
func DecodeTestimonyBody(body []byte) (*simplexdoc.Document, string, int) {
	doc, err := simplexdoc.Decode(body)
	if err != nil {
		var refused *simplexdoc.UnavailableError
		if errors.As(err, &refused) {
			return nil, refused.DetailCode, http.StatusBadRequest
		}
		return nil, reasons.SourceUnavailable, http.StatusBadRequest
	}
	return doc, "", 0
}
