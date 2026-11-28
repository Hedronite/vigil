package signals

import (
	"testing"
	"time"
)

func TestClampTTLDefaultAndBounds(t *testing.T) {
	if d := ClampTTL(0); d != defaultTTL {
		t.Fatalf("default = %v, want %v", d, defaultTTL)
	}
	if d := ClampTTL(30); d != minTTL {
		t.Fatalf("low = %v, want %v", d, minTTL)
	}
	if d := ClampTTL(60); d != minTTL {
		t.Fatalf("min = %v", d)
	}
	if d := ClampTTL(59); d != minTTL {
		t.Fatalf("below min = %v", d)
	}
	if d := ClampTTL(3599); d != 3599*time.Second {
		t.Fatalf("3599 = %v", d)
	}
	if d := ClampTTL(3600); d != maxTTL {
		t.Fatalf("max = %v", d)
	}
	if d := ClampTTL(3601); d != maxTTL {
		t.Fatalf("above max = %v", d)
	}
	if d := ClampTTL(300); d != 300*time.Second {
		t.Fatalf("mid = %v", d)
	}
}
