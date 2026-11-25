package config_test

import (
	"errors"
	"testing"

	"github.com/Hedronite/vigil/internal/config"
	"github.com/Hedronite/vigil/internal/exitcode"
)

// TestFreshnessDefaults: every freshness key a config leaves out, the whole
// block included, comes back with its documented default.
func TestFreshnessDefaults(t *testing.T) {
	cases := []struct {
		name  string
		block string
		stale string
		skew  string
		grace config.Seconds
	}{
		{name: "no block", stale: "120s", skew: "10s", grace: 240},
		{name: "empty block", block: "freshness: {}\n", stale: "120s", skew: "10s", grace: 240},
		{name: "stale only", block: "freshness:\n  stale_after: 90s\n", stale: "90s", skew: "10s", grace: 240},
		{name: "skew only", block: "freshness:\n  skew_budget: 3s\n", stale: "120s", skew: "3s", grace: 240},
		{name: "grace only", block: "freshness:\n  declaration_grace_s: 600\n", stale: "120s", skew: "10s", grace: 600},
		{name: "no grace is kept", block: "freshness:\n  declaration_grace_s: 0\n", stale: "120s", skew: "10s", grace: 0},
		{name: "upper bounds", block: "freshness:\n  skew_budget: 300s\n  declaration_grace_s: 86400\n", stale: "120s", skew: "300s", grace: 86400},
		{name: "lower skew bound", block: "freshness:\n  skew_budget: 1s\n", stale: "120s", skew: "1s", grace: 240},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := config.Load(writeFile(t, t.TempDir(), "c.yaml", minimal+tc.block))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			f := c.Freshness
			if f == nil || f.DeclarationGraceS == nil {
				t.Fatalf("freshness = %+v, want every default applied", f)
			}
			if f.StaleAfter != tc.stale || f.SkewBudget != tc.skew || *f.DeclarationGraceS != tc.grace {
				t.Errorf("freshness = %s/%s/%d, want %s/%s/%d",
					f.StaleAfter, f.SkewBudget, *f.DeclarationGraceS, tc.stale, tc.skew, tc.grace)
			}
		})
	}
}

// TestFreshnessRefusals: a value outside the bounds is a usage error that names
// the key. A fraction is refused, never rounded down.
func TestFreshnessRefusals(t *testing.T) {
	cases := []struct {
		line  string
		field string
	}{
		{line: "stale_after: 2m", field: "freshness.stale_after"},
		{line: "skew_budget: 0s", field: "freshness.skew_budget"},
		{line: "skew_budget: 301s", field: "freshness.skew_budget"},
		{line: "skew_budget: 1000s", field: "freshness.skew_budget"},
		{line: "skew_budget: 010s", field: "freshness.skew_budget"},
		{line: "skew_budget: 1.5s", field: "freshness.skew_budget"},
		{line: "skew_budget: 1m", field: "freshness.skew_budget"},
		{line: "skew_budget: 10", field: "freshness.skew_budget"},
		{line: "declaration_grace_s: -1", field: "freshness.declaration_grace_s"},
		{line: "declaration_grace_s: 86401", field: "freshness.declaration_grace_s"},
		// The decoder refuses these before the loader can name the key.
		{line: "declaration_grace_s: 1.5"},
		{line: "declaration_grace_s: 240.0"},
		{line: "declaration_grace_s: \"240\""},
		{line: "declaration_grace_s: 240s"},
		{line: "declaration_grace_s: true"},
		{line: "declaration_grace_s: [240]"},
		{line: "declaration_grace_s: 99999999999999999999"},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			_, err := config.Load(writeFile(t, t.TempDir(), "c.yaml", minimal+"freshness:\n  "+tc.line+"\n"))
			var cfgErr *config.Error
			if !errors.As(err, &cfgErr) {
				t.Fatalf("error = %v, want a *config.Error", err)
			}
			if cfgErr.Code != exitcode.Usage || cfgErr.Field != tc.field {
				t.Errorf("code %d field %q, want %d %q (%v)", cfgErr.Code, cfgErr.Field, exitcode.Usage, tc.field, err)
			}
		})
	}
}
