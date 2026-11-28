package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/Hedronite/vigil/internal/signals"
)

func signalRoutes(opts Options) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			withAuth(opts.ReadToken, getSignals(opts))(w, r)
		case http.MethodPost:
			withAuth(opts.SignalToken, postSignal(opts))(w, r)
		default:
			writeAPI(w, http.StatusMethodNotAllowed, "method_not_allowed")
		}
	})
}

func getSignals(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if opts.Signals == nil {
			writeAPI(w, http.StatusInternalServerError, "internal")
			return
		}
		now := opts.now()
		docs := opts.Signals.Live(now)
		b, err := signals.MarshalList(docs)
		if err != nil {
			writeAPI(w, http.StatusInternalServerError, "internal")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		writeBody(w, b)
	}
}

func postSignal(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.Signals == nil || opts.Limiter == nil {
			writeAPI(w, http.StatusInternalServerError, "internal")
			return
		}
		addr, err := signals.RemoteAddr(r)
		if err != nil {
			writeAPI(w, http.StatusBadRequest, "invalid_signal")
			return
		}
		now := opts.now()
		if !opts.Limiter.Allow(addr, now.Unix()) {
			writeAPI(w, http.StatusTooManyRequests, "rate_limited")
			return
		}
		lim := http.MaxBytesReader(w, r.Body, signals.MaxBody+1)
		body, err := io.ReadAll(lim)
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				writeAPI(w, http.StatusRequestEntityTooLarge, "body_cap")
				return
			}
			writeAPI(w, http.StatusBadRequest, "invalid_signal")
			return
		}
		if len(body) > signals.MaxBody {
			writeAPI(w, http.StatusRequestEntityTooLarge, "body_cap")
			return
		}
		fields, err := signals.DecodePost(body)
		if err != nil {
			writeAPI(w, http.StatusBadRequest, "invalid_signal")
			return
		}
		doc, err := opts.Signals.Accept(fields, now)
		if err != nil {
			writeAPI(w, http.StatusInternalServerError, "internal")
			return
		}
		b, err := json.Marshal(doc)
		if err != nil {
			writeAPI(w, http.StatusInternalServerError, "internal")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		writeBody(w, append(b, '\n'))
	}
}

func pruneSignals(ctx context.Context, store *signals.Store, now func() time.Time) {
	if store == nil {
		return
	}
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			store.Prune(now())
		}
	}
}