package summary_test

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/Hedronite/vigil/internal/summary"
)

// TestFreshness: both age checks, the tighter of the two lifetimes, and the
// informational skew finding. stale_after is 120s and the skew budget 10s.
func TestFreshness(t *testing.T) {
	cases := []struct {
		name      string
		generated int // seconds before now
		received  int
		validFor  *int
		state     string
		skewed    bool
	}{
		{name: "new", state: "ok"},
		{name: "received exactly at stale_after", generated: 120, received: 120, state: "ok"},
		{name: "received one past stale_after", generated: 121, received: 121, state: "stale"},
		{name: "valid_for tightens", generated: 61, received: 61, validFor: ptr(60), state: "stale"},
		{name: "at a tighter valid_for", generated: 60, received: 60, validFor: ptr(60), state: "ok"},
		{name: "valid_for cannot extend", generated: 121, received: 121, validFor: ptr(600), state: "stale"},
		{name: "huge valid_for cannot extend", generated: 121, received: 121, validFor: ptr(math.MaxInt), state: "stale"},
		{name: "huge valid_for does not overflow", validFor: ptr(math.MaxInt), state: "ok"},
		{name: "valid_for equal to stale_after", generated: 120, received: 120, validFor: ptr(120), state: "ok"},
		// A stuck producer clock: the file is new, the time in it is old.
		{name: "generated within the budget", generated: 130, received: 0, state: "ok", skewed: true},
		{name: "generated past the budget", generated: 131, received: 0, state: "stale", skewed: true},
		{name: "generated past a tighter budget", generated: 71, received: 0, validFor: ptr(60), state: "stale", skewed: true},
		// A producer clock that runs ahead buys no extra life.
		{name: "future generated, new file", generated: -3600, received: 0, state: "ok", skewed: true},
		{name: "future generated, old file", generated: -3600, received: 121, state: "stale", skewed: true},
		// A sighting stamped after now is held to now.
		{name: "future received", generated: 0, received: -3600, state: "ok"},
		{name: "future received, old document", generated: 131, received: -3600, state: "stale", skewed: true},
		{name: "skew at the budget", generated: 20, received: 10, state: "ok"},
		{name: "skew one past the budget", generated: 21, received: 10, state: "ok", skewed: true},
		{name: "skew the other way", generated: 10, received: 21, state: "ok", skewed: true},
		{name: "valid_for zero is not a document", validFor: ptr(0), state: "unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := document(green)
			d.GeneratedAt, d.ReceivedAt, d.ValidFor = ago(tc.generated), ago(tc.received), tc.validFor
			doc := combine(t, input(authority("simplex", d)))
			v, source := doc.Verdicts[0], doc.Sources[0]
			if v.State != tc.state || source.State != tc.state {
				t.Fatalf("state %s, source %s, want %s", v.State, source.State, tc.state)
			}
			if got := slices.Contains(v.Reasons, "clock_skew"); got != tc.skewed {
				t.Errorf("clock_skew = %v, want %v (%v)", got, tc.skewed, v.Reasons)
			}
			if got := slices.Contains(v.Reasons, "source_stale"); got != (tc.state == "stale") {
				t.Errorf("source_stale = %v for %s", got, tc.state)
			}
			wantOverall := map[string]string{"ok": green, "stale": yellow, "unavailable": "null"}[tc.state]
			if overall(doc) != wantOverall {
				t.Errorf("overall %s, want %s: skew alone colours nothing", overall(doc), wantOverall)
			}
			switch tc.state {
			case "ok":
				if source.DetailCode != nil {
					t.Errorf("an ok row names %s", *source.DetailCode)
				}
			case "stale":
				if source.DetailCode == nil || *source.DetailCode != "source_stale" {
					t.Errorf("a stale row names %v", source.DetailCode)
				}
			}
		})
	}
}

// TestHeaderPassThrough: the document carries the producer's own values, the
// sighting held to now, and nothing for what was not there.
func TestHeaderPassThrough(t *testing.T) {
	d := document(yellow)
	d.GeneratedAt, d.ReceivedAt = ago(5), clock.Add(time.Hour)
	d.ValidFor, d.Declaration, d.Reasons = ptr(600), ptr(digestA), []string{"watchdog_only"}
	doc := combine(t, input(authority("augur", d)))
	v, source := doc.Verdicts[0], doc.Sources[0]
	for name, got := range map[string]*string{"verdict": v.ReceivedAt, "source": source.ReceivedAt} {
		if got == nil || *got != "2026-11-13T18:00:00Z" {
			t.Errorf("%s received_at = %v, want now", name, got)
		}
	}
	if *v.ValidFor != 600 || *source.ValidFor != 600 || *v.Declaration != digestA || *source.Declaration != digestA {
		t.Errorf("header = %+v / %+v", v, source)
	}
	if *v.ObservedAt != "2026-11-13T17:59:55Z" || *source.LastOK != *v.ObservedAt || source.Kind != "augur" {
		t.Errorf("times = %+v / %+v", v, source)
	}
	if !slices.Equal(v.Reasons, []string{"watchdog_only"}) {
		t.Errorf("an authority's own reasons = %v", v.Reasons)
	}
	bare := combine(t, input(ok("simplex", green))).Verdicts[0]
	if bare.ValidFor != nil || bare.Declaration != nil || bare.ReceivedAt == nil {
		t.Errorf("a document without a header gave %+v", bare)
	}
}

// TestInputIsUntouched: combining twice from one input gives one document, and
// nothing handed in is changed.
func TestInputIsUntouched(t *testing.T) {
	codes := make([]string, 1, 8)
	codes[0] = "watchdog_only"
	d := document(green)
	d.GeneratedAt, d.ReceivedAt, d.Reasons = ago(900), ago(600), codes
	d.Hosts = append(d.Hosts, host("lab", green, "held"))
	in := input(authority("simplex", d))
	in.Sources = []summary.Source{{ID: "grafana", Kind: "grafana", State: "ok", DetailCode: ptr("timeout")}}

	first := combine(t, in)
	second := combine(t, in)
	if len(first.Verdicts[0].Reasons) != 3 || !slices.Equal(first.Verdicts[0].Reasons, second.Verdicts[0].Reasons) {
		t.Errorf("reasons %v then %v", first.Verdicts[0].Reasons, second.Verdicts[0].Reasons)
	}
	if spare := codes[:3]; spare[1] != "" || spare[2] != "" {
		t.Errorf("the caller's reasons were written to: %q", spare)
	}
	if in.Sources[0].DetailCode == nil {
		t.Error("the caller's row was changed")
	}
	first.Hosts[0].Reasons[0] = "changed"
	if d.Hosts[0].Reasons[0] != "held" {
		t.Error("the document shares a host's reasons with the input")
	}
}

// TestSourceRows: rows that are not authorities are carried in order, held to
// the schema's rule for a detail code.
func TestSourceRows(t *testing.T) {
	in := input(ok("simplex", green))
	in.Sources = []summary.Source{
		{ID: "prom", Kind: "prom", State: "ok", DetailCode: ptr("timeout"), Vantage: "lab"},
		{ID: "grafana", Kind: "grafana", State: "unavailable"},
		{ID: "edge", Kind: "prom", State: "unavailable", DetailCode: ptr("")},
		{ID: "core", Kind: "prom", State: "unavailable", DetailCode: ptr("redirect_refused")},
		{ID: "scry", Kind: "scry", State: "stale", DetailCode: ptr("source_stale")},
	}
	doc := combine(t, in)
	got := []string{}
	for _, s := range doc.Sources {
		code := "-"
		if s.DetailCode != nil {
			code = *s.DetailCode
		}
		got = append(got, s.ID+":"+s.State+":"+code)
	}
	want := []string{
		"simplex:ok:-", "prom:ok:-", "grafana:unavailable:source_unavailable",
		"edge:unavailable:source_unavailable", "core:unavailable:redirect_refused", "scry:stale:source_stale",
	}
	if !slices.Equal(got, want) {
		t.Errorf("rows\n%v\nwant\n%v", got, want)
	}
}

// TestUnusableDocuments: an authority whose input is not a usable document is
// unavailable, never a verdict with a made-up colour.
func TestUnusableDocuments(t *testing.T) {
	odd := document("BLUE")
	empty := summary.Authority{ID: "augur"}
	doc := combine(t, input(authority("simplex", odd), empty))
	for i, v := range doc.Verdicts {
		if v.State != "unavailable" || *doc.Sources[i].DetailCode != "source_unavailable" {
			t.Errorf("%s = %s %v", v.Source, v.State, doc.Sources[i].DetailCode)
		}
	}
	if doc.Overall != nil {
		t.Errorf("overall %s", *doc.Overall)
	}
}
