package server

import (
	"testing"
	"time"
)

func TestCeilings(t *testing.T) {
	if headerTimeout != 5*time.Second || readTimeout != 15*time.Second ||
		writeTimeout != 15*time.Second || idleTimeout != 60*time.Second ||
		shutdownTimeout != 5*time.Second || maxHeader != 8<<10 {
		t.Fatal("listener timeouts drifted from the documented bounds")
	}
}
