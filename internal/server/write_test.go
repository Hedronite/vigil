package server

import (
	"errors"
	"net/http"
	"testing"
)

type errWriter struct {
	hdr http.Header
}

func (e *errWriter) Header() http.Header {
	if e.hdr == nil {
		e.hdr = make(http.Header)
	}
	return e.hdr
}

func (e *errWriter) WriteHeader(int) {}

func (e *errWriter) Write([]byte) (int, error) { return 0, errors.New("write stopped") }

func TestWriteBodyMarksFailure(t *testing.T) {
	w := &errWriter{}
	writeBody(w, []byte("x"))
	if w.Header().Get(writeFailedHeader) != "1" {
		t.Fatalf("header = %q, want write failure marked", w.Header().Get(writeFailedHeader))
	}
}

func TestWriteAPIOnWriteFailure(t *testing.T) {
	w := &errWriter{}
	writeAPI(w, http.StatusNotFound, "not_found")
	if w.Header().Get(writeFailedHeader) != "1" {
		t.Fatalf("writeAPI did not record a write failure")
	}
}
