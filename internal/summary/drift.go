package summary

import (
	"maps"
	"slices"
	"time"

	"github.com/Hedronite/vigil/internal/reasons"
)

// Drift is declaration_drift: which declaration each fresh witness was produced
// under, compared with Vigil's own.
type Drift struct {
	Reference     *string  `json:"reference"`
	InGrace       bool     `json:"in_grace"`
	Drifted       bool     `json:"drifted"`
	Undeclared    []string `json:"undeclared"`
	ByDeclaration []Group  `json:"by_declaration"`
}

// Group is the witnesses on one digest.
type Group struct {
	Declaration string   `json:"declaration"`
	Subjects    []string `json:"subjects"`
}

// witness is one fresh document's say about the declaration. Only an
// authority's can colour overall.
type witness struct {
	id          string
	declaration *string
	authority   bool
}

// witnesses are the documents that speak for what is deployed now: the fresh
// authorities, and the informational sources whose row is ok. A backend row is
// not a document, and a stale or unread one says nothing about now.
func witnesses(verdicts []Verdict, rows []Source) []witness {
	var out []witness
	for _, v := range verdicts {
		if v.State == reasons.StateOK {
			out = append(out, witness{id: v.Source, declaration: v.Declaration, authority: true})
		}
	}
	for _, row := range rows {
		if row.State == reasons.StateOK && (row.Kind == "scry" || row.Kind == "cubit") {
			out = append(out, witness{id: row.ID, declaration: row.Declaration})
		}
	}
	return out
}

// compare builds the drift block and names the authorities whose drift counts
// against overall: those that differ from the reference once the grace window
// is closed.
func compare(in Input, now time.Time, seen []witness) (*Drift, []string) {
	drift := &Drift{Reference: in.Declaration, InGrace: inGrace(in, now)}
	groups := map[string][]string{}
	var undeclared, colouring []string
	for _, w := range seen {
		if w.declaration == nil {
			undeclared = append(undeclared, w.id)
			continue
		}
		groups[*w.declaration] = append(groups[*w.declaration], w.id)
		if in.Declaration == nil || *w.declaration == *in.Declaration {
			continue
		}
		drift.Drifted = true
		if w.authority && !drift.InGrace {
			colouring = append(colouring, w.id)
		}
	}
	drift.Undeclared = sorted(undeclared)
	drift.ByDeclaration = make([]Group, 0, len(groups))
	for _, digest := range slices.Sorted(maps.Keys(groups)) {
		drift.ByDeclaration = append(drift.ByDeclaration, Group{Declaration: digest, Subjects: sorted(groups[digest])})
	}
	return drift, colouring
}

// inGrace says whether the window after a change of Vigil's declaration is
// still open. A change that is unknown, or stamped after now, opens none.
func inGrace(in Input, now time.Time) bool {
	if in.VigilDeclChangedAt == nil {
		return false
	}
	since := now.Sub(*in.VigilDeclChangedAt)
	return since >= 0 && since <= in.DeclarationGrace
}

// sorted returns the ids in order, each once, as a list that is never null.
func sorted(ids []string) []string {
	out := append([]string{}, ids...)
	slices.Sort(out)
	return slices.Compact(out)
}
