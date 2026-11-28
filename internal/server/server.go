// Package server is the only non-test package that may construct an HTTP listener.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/Hedronite/vigil/internal/config"
	"github.com/Hedronite/vigil/internal/signals"
	"github.com/Hedronite/vigil/internal/simplexdoc"
	"github.com/Hedronite/vigil/internal/summary"
)

const (
	headerTimeout   = 5 * time.Second
	readTimeout     = 15 * time.Second
	writeTimeout    = 15 * time.Second
	idleTimeout     = 60 * time.Second
	shutdownTimeout = 5 * time.Second
	maxHeader       = 8 << 10
)

// DocumentBuilder builds the current vigil.summary/v0 document.
type DocumentBuilder func(context.Context, time.Time) (*summary.Document, error)

// Options configures the listener.
type Options struct {
	Listen           string
	ReadToken        [32]byte
	SignalToken      [32]byte
	AllowNonLoopback bool
	Build            DocumentBuilder
	Signals          *signals.Store
	Limiter          *signals.Limiter
	Now              func() time.Time
	Listener         net.Listener  // tests only
	PruneEvery       time.Duration // tests only; zero uses signalPruneInterval
	TestimonyToken   [32]byte
	Testimony        *TestimonyStore
	Config           *config.Config
	State            *State
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now().UTC()
	}
	return time.Now().UTC()
}

func validateRunOptions(opts Options) error {
	var zeroToken [32]byte
	if opts.SignalToken == zeroToken {
		return fmt.Errorf("signal token hash is missing")
	}
	if opts.needsTestimonyToken() && opts.TestimonyToken == zeroToken {
		return fmt.Errorf("testimony token hash is missing")
	}
	return CheckBind(opts.Listen, opts.AllowNonLoopback)
}

func openListener(opts Options) (net.Listener, error) {
	if opts.Listener != nil {
		return opts.Listener, nil
	}
	ln, err := net.Listen("tcp", opts.Listen)
	if err != nil {
		return nil, fmt.Errorf("bind %s: %w", opts.Listen, err)
	}
	return ln, nil
}

// Run listens until ctx ends, then shuts down. A bind failure returns an error.
func Run(ctx context.Context, opts Options) error {
	if err := validateRunOptions(opts); err != nil {
		return err
	}
	ln, err := openListener(opts)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           routes(opts),
		ReadHeaderTimeout: headerTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeader,
	}
	if opts.Signals != nil {
		every := opts.PruneEvery
		if every == 0 {
			every = signalPruneInterval
		}
		go pruneSignalsEvery(ctx, opts.Signals, opts.now, every)
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ln)
	}()
	select {
	case <-ctx.Done():
		stop, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		return srv.Shutdown(stop)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func routes(opts Options) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/healthz", allowGET(http.HandlerFunc(health)))
	auth := func(fn http.HandlerFunc) http.Handler {
		return allowGET(http.HandlerFunc(withAuth(opts.ReadToken, fn)))
	}
	mux.Handle("/v0/summary", auth(summaryJSON(opts.Build)))
	mux.Handle("/v0/hosts", auth(hostsJSON(opts.Build)))
	mux.Handle("/v0/hosts/{id}", auth(hostByID(opts.Build)))
	mux.Handle("/v0/sources", auth(sourcesJSON(opts.Build)))
	mux.Handle("/v0/signals", signalRoutes(opts))
	mux.Handle("/v0/testimony/{source}", testimonyRoutes(opts))
	return recoverHTTP(notFound(mux))
}

func (o Options) needsTestimonyToken() bool {
	return o.Config != nil && pushAuthorityConfigured(o.Config, simplexdoc.Source)
}

func allowGET(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAPI(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func notFound(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := mux.Handler(r)
		if pattern == "" {
			writeAPI(w, http.StatusNotFound, "not_found")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	writeBody(w, []byte(healthBody))
}

func withAuth(want [32]byte, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !tokenAuthorized(bearerToken(r.Header.Get("Authorization")), want) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			writeBody(w, []byte(UnauthorizedBody))
			return
		}
		next(w, r)
	}
}

func summaryJSON(build DocumentBuilder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		doc, err := build(r.Context(), time.Now().UTC())
		if err != nil {
			writeAPI(w, http.StatusInternalServerError, "internal")
			return
		}
		writeJSON(w, doc)
	}
}

func hostsJSON(build DocumentBuilder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		doc, err := build(r.Context(), time.Now().UTC())
		if err != nil {
			writeAPI(w, http.StatusInternalServerError, "internal")
			return
		}
		writeJSON(w, doc.Hosts)
	}
}

func hostByID(build DocumentBuilder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		doc, err := build(r.Context(), time.Now().UTC())
		if err != nil {
			writeAPI(w, http.StatusInternalServerError, "internal")
			return
		}
		for _, h := range doc.Hosts {
			if h.ID == id {
				writeJSON(w, h)
				return
			}
		}
		writeAPI(w, http.StatusNotFound, "not_found")
	}
}

func sourcesJSON(build DocumentBuilder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		doc, err := build(r.Context(), time.Now().UTC())
		if err != nil {
			writeAPI(w, http.StatusInternalServerError, "internal")
			return
		}
		writeJSON(w, doc.Sources)
	}
}

func writeJSON(w http.ResponseWriter, doc any) {
	b, err := json.Marshal(doc)
	if err != nil {
		writeAPI(w, http.StatusInternalServerError, "internal")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeBody(w, append(b, '\n'))
}

func recoverHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				writeAPI(w, http.StatusInternalServerError, "internal")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
