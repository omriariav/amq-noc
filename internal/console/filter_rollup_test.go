package console

import (
	"testing"

	"github.com/omriariav/amq-noc/internal/state"
)

// TestFilterSnapshotRecomputesRollup pins the consistency fix: a filtered
// snapshot's headline rollup must reflect ONLY the sessions that survive the
// filter, never the whole-team totals (otherwise a filtered --once headline
// reports counts for sessions it no longer shows). The input is not mutated.
func TestFilterSnapshotRecomputesRollup(t *testing.T) {
	snap := state.Snapshot{
		Sessions: []state.Session{
			{Name: "alpha", Rollup: state.TriageRollup{AtRisk: 1, Blocked: 2}},
			{Name: "beta", Rollup: state.TriageRollup{NeedsYou: 1, Blocked: 1}},
		},
		Rollup: state.TriageRollup{NeedsYou: 1, AtRisk: 1, Blocked: 3},
	}

	out := filterSnapshot(snap, parseFilter("session:alpha"))

	if len(out.Sessions) != 1 || out.Sessions[0].Name != "alpha" {
		t.Fatalf("want only alpha, got %+v", out.Sessions)
	}
	want := state.TriageRollup{AtRisk: 1, Blocked: 2}
	if out.Rollup != want {
		t.Errorf("filtered rollup = %+v, want %+v (headline must reflect only the shown session)", out.Rollup, want)
	}
	if snap.Rollup != (state.TriageRollup{NeedsYou: 1, AtRisk: 1, Blocked: 3}) {
		t.Errorf("filterSnapshot mutated the input rollup: %+v", snap.Rollup)
	}
}
