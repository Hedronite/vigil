package server

import (
	"testing"
)

func FuzzTestimonyBody(f *testing.F) {
	f.Add([]byte(`{"apiVersion":"simplex.probe/v0","generated_at":"2026-11-13T18:00:00Z","overall":"GREEN","vantage":{"id":"x","status":"ok"},"hosts":[]}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		if len(body) > 1<<20 {
			t.Skip()
		}
		_, code, status := DecodeTestimonyBody(body)
		if code != "" && status == 0 {
			t.Fatalf("code without status")
		}
	})
}
