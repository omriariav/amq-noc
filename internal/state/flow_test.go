package state

import "testing"

// TestFlowGraphEmpty: an edgeless coordination returns nil so a render layer can
// show the "(no inter-agent messages yet)" line.
func TestFlowGraphEmpty(t *testing.T) {
	if got := FlowGraph(Coordination{}); got != nil {
		t.Fatalf("expected nil for edgeless coordination, got %v", got)
	}
}

// TestFlowGraphEnrichesAndSorts: edges pick up the blocked / awaiting-reply
// status of the threads on their link, and the result is sorted blocked-first,
// then awaiting-reply, then by descending volume.
func TestFlowGraphEnrichesAndSorts(t *testing.T) {
	c := Coordination{
		Edges: []Edge{
			{From: "cpo", To: "cto", Count: 2},   // plain, low volume
			{From: "cto", To: "qa", Count: 5},    // awaiting-reply link
			{From: "qa", To: "senior", Count: 1}, // blocked link
			{From: "cpo", To: "senior", Count: 9},
		},
		Threads: []ThreadSummary{
			// Blocked thread between qa and senior.
			{ID: "t/block", Participants: []string{"qa", "senior"}, Status: ThreadBlocked},
			// Awaiting-reply thread between cto and qa.
			{ID: "t/await", Participants: []string{"cto", "qa"}, Status: ThreadAwaitingReply},
			// A resolved thread must NOT mark its edge.
			{ID: "t/done", Participants: []string{"cpo", "senior"}, Status: ThreadResolved},
		},
	}

	edges := FlowGraph(c)
	if len(edges) != 4 {
		t.Fatalf("expected 4 flow edges, got %d", len(edges))
	}

	// Blocked link sorts first.
	if edges[0].From != "qa" || edges[0].To != "senior" {
		t.Fatalf("expected blocked qa->senior first, got %s->%s", edges[0].From, edges[0].To)
	}
	if !edges[0].Blocked || edges[0].Label() != "blocked" {
		t.Fatalf("expected first edge blocked, got blocked=%v label=%q", edges[0].Blocked, edges[0].Label())
	}

	// Awaiting-reply link sorts second.
	if edges[1].From != "cto" || edges[1].To != "qa" {
		t.Fatalf("expected awaiting cto->qa second, got %s->%s", edges[1].From, edges[1].To)
	}
	if !edges[1].Awaiting || edges[1].Label() != "awaiting-reply" {
		t.Fatalf("expected second edge awaiting-reply, got awaiting=%v label=%q", edges[1].Awaiting, edges[1].Label())
	}

	// Plain links follow, sorted by DESCENDING volume: cpo->senior (9) before
	// cpo->cto (2). The resolved thread did not mark cpo->senior.
	if edges[2].From != "cpo" || edges[2].To != "senior" || edges[2].Count != 9 {
		t.Fatalf("expected cpo->senior(9) third, got %s->%s(%d)", edges[2].From, edges[2].To, edges[2].Count)
	}
	if edges[2].Label() != "" {
		t.Fatalf("expected resolved link to have no status label, got %q", edges[2].Label())
	}
	if edges[3].From != "cpo" || edges[3].To != "cto" || edges[3].Count != 2 {
		t.Fatalf("expected cpo->cto(2) last, got %s->%s(%d)", edges[3].From, edges[3].To, edges[3].Count)
	}
}

// TestFlowGraphStaleThreadDoesNotMark: a STALE blocked/awaiting thread is
// age-decayed and must NOT mark its edge as blocked/awaiting (it is no longer
// live attention), matching the rest of the triage model.
func TestFlowGraphStaleThreadDoesNotMark(t *testing.T) {
	c := Coordination{
		Edges: []Edge{{From: "qa", To: "senior", Count: 3}},
		Threads: []ThreadSummary{
			{ID: "t/old", Participants: []string{"qa", "senior"}, Status: ThreadBlocked, Stale: true},
		},
	}
	edges := FlowGraph(c)
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	if edges[0].Blocked || edges[0].Label() != "" {
		t.Fatalf("stale blocked thread must not mark edge, got blocked=%v label=%q", edges[0].Blocked, edges[0].Label())
	}
}

// TestFlowGraphMatchesEitherDirection: a thread's status marks the edge
// regardless of which direction the edge runs (qa->senior matches a thread whose
// participants are {senior, qa}).
func TestFlowGraphMatchesEitherDirection(t *testing.T) {
	c := Coordination{
		Edges: []Edge{{From: "senior", To: "qa", Count: 1}},
		Threads: []ThreadSummary{
			{ID: "t/b", Participants: []string{"qa", "senior"}, Status: ThreadBlocked},
		},
	}
	edges := FlowGraph(c)
	if len(edges) != 1 || !edges[0].Blocked {
		t.Fatalf("expected senior->qa marked blocked from {qa,senior} thread, got %+v", edges)
	}
}
