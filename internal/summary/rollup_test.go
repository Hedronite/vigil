package summary_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/Hedronite/vigil/internal/summary"
)

var rank = map[string]int{green: 0, yellow: 1, red: 2}

// forms is every way an authority can arrive: each colour, fresh or stale, or
// not at all.
func forms(id string) map[string]summary.Authority {
	out := map[string]summary.Authority{"unavailable": gone(id)}
	for _, severity := range []string{green, yellow, red} {
		out["ok "+severity] = ok(id, severity)
		out["stale "+severity] = stale(id, severity)
	}
	return out
}

// TestNoSecondRollup: overall comes from the authorities' own colours and the
// caps, never from anything beneath or beside them.
func TestNoSecondRollup(t *testing.T) {
	t.Run("a single fresh authority is overall", func(t *testing.T) {
		for _, severity := range []string{green, yellow, red} {
			for _, status := range []string{"ok", "blind", "unchecked"} {
				for _, inner := range []string{green, yellow, red} {
					in := input(withPlace(ok("simplex", severity), status, host("lab", inner, "prom_pair_down")))
					doc := combine(t, in)
					if overall(doc) != severity || *doc.Verdicts[0].Severity != severity {
						t.Errorf("%s with a %s host and a %s vantage gave %s", severity, inner, status, overall(doc))
					}
				}
			}
		}
	})
	t.Run("two authorities give the worse", func(t *testing.T) {
		for _, a := range []string{green, yellow, red} {
			for _, b := range []string{green, yellow, red} {
				want := a
				if rank[b] > rank[a] {
					want = b
				}
				if got := overall(combine(t, input(ok("simplex", a), ok("augur", b)))); got != want {
					t.Errorf("%s and %s gave %s", a, b, got)
				}
			}
		}
	})
	t.Run("adding an authority never improves overall", func(t *testing.T) {
		for first, a := range forms("simplex") {
			alone := combine(t, input(a))
			for second, b := range forms("augur") {
				both := combine(t, input(a, b))
				if alone.Overall == nil {
					if second != "unavailable" && (both.Overall == nil || rank[*both.Overall] < rank[yellow]) {
						t.Errorf("%s joined by %s gave %s, want at least YELLOW", first, second, overall(both))
					}
					continue
				}
				if both.Overall == nil || rank[*both.Overall] < rank[*alone.Overall] {
					t.Errorf("%s joined by %s went from %s to %s", first, second, overall(alone), overall(both))
				}
			}
		}
	})
	t.Run("nothing beside the authorities moves it", func(t *testing.T) {
		scryDown := row("scry")
		scryDown.DetailCode = ptr("source_unavailable")
		beside := map[string]func(*summary.Input){
			"grafana down":    func(in *summary.Input) { in.Sources = []summary.Source{row("grafana")} },
			"prometheus down": func(in *summary.Input) { in.Sources = []summary.Source{row("prom")} },
			"scry unobserved": func(in *summary.Input) {
				in.Sources = []summary.Source{scryDown}
				in.Scry = json.RawMessage(`{"observed":false}`)
			},
			"scry says red": func(in *summary.Input) {
				in.Sources = []summary.Source{{ID: "scry", Kind: "scry", State: "ok"}}
				in.Scry = json.RawMessage(`{"observed":true,"severity":"RED","reasons":["node_down"]}`)
			},
			"early augur fields": func(in *summary.Input) {
				in.Augur = json.RawMessage(`{"overall":"RED","severity":"RED","alerts":[{"severity":"critical"}]}`)
			},
			"cubit data": func(in *summary.Input) { in.Cubit = json.RawMessage(`{"full":true}`) },
			"informational drift": func(in *summary.Input) {
				in.Declaration = ptr(digestA)
				in.Sources = []summary.Source{
					{ID: "scry", Kind: "scry", State: "ok", Declaration: ptr(digestB)},
					{ID: "cubit", Kind: "cubit", State: "ok", Declaration: ptr(digestB)},
				}
			},
		}
		for first, a := range forms("simplex") {
			for second, b := range forms("augur") {
				bare := combine(t, input(a, b))
				for name, add := range beside {
					in := input(a, b)
					add(&in)
					doc := combine(t, in)
					if overall(doc) != overall(bare) || !reflect.DeepEqual(doc.Verdicts, bare.Verdicts) {
						t.Errorf("%s, %s: %s moved %s to %s", first, second, name, overall(bare), overall(doc))
					}
				}
			}
		}
	})
	t.Run("only simplex places hosts", func(t *testing.T) {
		other := withPlace(ok("augur", red), "blind", host("core", red))
		doc := combine(t, input(ok("simplex", green), other))
		if doc.Vantage != nil || len(doc.Hosts) != 0 {
			t.Errorf("another authority placed %+v %+v", doc.Vantage, doc.Hosts)
		}
	})
}

// TestStateExitMatrix: what each state does to a verdict, to overall and to
// the exit code.
func TestStateExitMatrix(t *testing.T) {
	blind := withPlace(ok("simplex", red), "blind", host("lab", red, "vantage_blind"))
	blindStale := withPlace(stale("simplex", red), "blind", host("lab", red, "vantage_blind"))
	cases := []struct {
		name    string
		in      []summary.Authority
		overall string
		exit    int
		states  []string
		reasons []string // of the first verdict
	}{
		{"fresh green", []summary.Authority{ok("simplex", green)}, green, 0, []string{"ok"}, []string{}},
		{"fresh yellow", []summary.Authority{ok("simplex", yellow)}, yellow, 1, []string{"ok"}, []string{}},
		{"fresh red", []summary.Authority{ok("simplex", red)}, red, 2, []string{"ok"}, []string{}},
		{"stale green", []summary.Authority{stale("simplex", green)}, yellow, 1, []string{"stale"}, []string{"source_stale"}},
		{"stale yellow", []summary.Authority{stale("simplex", yellow)}, yellow, 1, []string{"stale"}, []string{"source_stale"}},
		{"stale red", []summary.Authority{stale("simplex", red)}, red, 2, []string{"stale"}, []string{"source_stale"}},
		{"unavailable", []summary.Authority{gone("simplex")}, "null", 4, []string{"unavailable"}, []string{"authority_unavailable"}},
		{"both unavailable", []summary.Authority{gone("simplex"), gone("augur")}, "null", 4, []string{"unavailable", "unavailable"}, []string{"authority_unavailable"}},
		{"green beside unavailable", []summary.Authority{ok("simplex", green), gone("augur")}, yellow, 1, []string{"ok", "unavailable"}, []string{}},
		{"red beside unavailable", []summary.Authority{ok("simplex", red), gone("augur")}, red, 2, []string{"ok", "unavailable"}, []string{}},
		{"stale green beside unavailable", []summary.Authority{stale("simplex", green), gone("augur")}, yellow, 1, []string{"stale", "unavailable"}, []string{"source_stale"}},
		{"stale beside red", []summary.Authority{stale("simplex", green), ok("augur", red)}, red, 2, []string{"stale", "ok"}, []string{"source_stale"}},
		{"blind", []summary.Authority{blind}, red, 2, []string{"ok"}, []string{}},
		{"stale blind", []summary.Authority{blindStale}, red, 2, []string{"stale"}, []string{"source_stale"}},
		{"no authorities", nil, "null", 4, []string{}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := summary.Combine(input(tc.in...), clock)
			if len(tc.in) > 0 {
				emit(t, doc)
			}
			if overall(doc) != tc.overall || summary.ExitCode(doc) != tc.exit {
				t.Errorf("overall %s exit %d, want %s %d", overall(doc), summary.ExitCode(doc), tc.overall, tc.exit)
			}
			states := []string{}
			for _, v := range doc.Verdicts {
				states = append(states, v.State)
				if (v.State == "unavailable") != (v.Severity == nil) || (v.State == "unavailable") != (v.ObservedAt == nil) {
					t.Errorf("verdict %+v: severity and observed_at must be null exactly when unavailable", v)
				}
			}
			if !slices.Equal(states, tc.states) {
				t.Errorf("states %v, want %v", states, tc.states)
			}
			if len(doc.Verdicts) > 0 && !slices.Equal(doc.Verdicts[0].Reasons, tc.reasons) {
				t.Errorf("reasons %v, want %v", doc.Verdicts[0].Reasons, tc.reasons)
			}
			if doc.Hosts == nil || doc.Sources == nil || doc.Verdicts == nil {
				t.Error("a list is nil and would be written as null")
			}
		})
	}
}

// TestExitCode: no verdict is 4, and an overall that is not a severity is
// never read as one.
func TestExitCode(t *testing.T) {
	for value, want := range map[string]int{green: 0, yellow: 1, red: 2, "BLUE": 6, "": 6} {
		if got := summary.ExitCode(&summary.Document{Overall: ptr(value)}); got != want {
			t.Errorf("%q exits %d, want %d", value, got, want)
		}
	}
	if got := summary.ExitCode(&summary.Document{}); got != 4 {
		t.Errorf("null exits %d, want 4", got)
	}
}
