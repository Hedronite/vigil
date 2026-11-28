package server

import (
	"testing"

	"github.com/Hedronite/vigil/internal/config"
)

func TestNeedsTestimonyTokenPushSimplexOnly(t *testing.T) {
	push := &config.Config{Authorities: []config.Authority{{ID: "simplex", Via: "push"}}}
	pushOpts := Options{Config: push}
	if !pushOpts.needsTestimonyToken() {
		t.Fatal("push simplex must need testimony token")
	}
	file := &config.Config{Authorities: []config.Authority{{ID: "simplex", Via: "file", PathRef: "env:X"}}}
	fileOpts := Options{Config: file}
	if fileOpts.needsTestimonyToken() {
		t.Fatal("file simplex must not need testimony token")
	}
	empty := Options{Config: &config.Config{}}
	if empty.needsTestimonyToken() {
		t.Fatal("no authorities")
	}
}
