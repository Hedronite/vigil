package signals

import (
	"errors"
	"testing"
	"time"
)

func TestAcceptRandFailure(t *testing.T) {
	old := randomReader
	randomReader = func([]byte) (int, error) { return 0, errors.New("rand fail") }
	defer func() { randomReader = old }()
	st := NewStore()
	if _, err := st.Accept(PostFields{Source: "a", Kind: "b", Severity: "info"}, time.Now()); err == nil {
		t.Fatal("expected rand failure")
	}
}
