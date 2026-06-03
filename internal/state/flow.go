package state

import "sort"

// FlowEdge is one directed inter-agent message flow row for the NOC flow graph:
// the existing Edge (from->to message count) ENRICHED with the outstanding
// status of the work on that link, derived purely from the session's already
// collapsed threads. It answers "where is work piling up / who owes whom" at a
// glance: a Blocked or Awaiting edge marks a link that is not just busy but
// stuck.
//
// FlowEdge carries NO new scan or clock work — it is a join of two derived
// fields (Coordination.Edges and Coordination.Threads), so a render layer can
// format it without recomputing anything.
type FlowEdge struct {
	From  string
	To    string
	Count int
	// Blocked is true when at least one LIVE (non-stale) thread whose
	// participants include both From and To is ThreadBlocked. The hardest stuck
	// signal on a link.
	Blocked bool
	// Awaiting is true when at least one LIVE (non-stale) thread whose
	// participants include both From and To is ThreadAwaitingReply (and the link
	// is not already Blocked). Someone owes a reply on this link.
	Awaiting bool
}

// Label returns the short text status marker for an edge — "blocked",
// "awaiting-reply", or "" — so a render layer can show the state as TEXT (not
// only color), keeping the NO_COLOR / ascii fallback legible.
func (e FlowEdge) Label() string {
	switch {
	case e.Blocked:
		return "blocked"
	case e.Awaiting:
		return "awaiting-reply"
	default:
		return ""
	}
}

// rank orders edges for the flow graph: blocked links first (most stuck), then
// awaiting-reply, then by descending message volume, then From/To for a stable
// deterministic order. Lower sorts first.
func (e FlowEdge) rank() int {
	switch {
	case e.Blocked:
		return 0
	case e.Awaiting:
		return 1
	default:
		return 2
	}
}

// FlowGraph enriches a coordination view's directed edge list with the
// outstanding status of each link, derived from the SAME view's threads, and
// returns the rows sorted blocked-first then awaiting-reply then by descending
// volume. It is a pure join over already-derived data: no new computation, no
// disk, no clock. An edgeless coordination returns nil so a caller can render a
// "(no inter-agent messages yet)" line.
//
// A thread's status is attributed to an edge when BOTH the edge's endpoints are
// participants of that thread. A LIVE (non-stale) thread takes precedence; a
// stale thread's status is age-decayed and does NOT mark the edge (it has
// stopped being live attention), matching the rest of the triage model.
func FlowGraph(c Coordination) []FlowEdge {
	if len(c.Edges) == 0 {
		return nil
	}

	// Index live thread statuses by participant pair (unordered) so an edge in
	// either direction can pick up the link's outstanding state.
	blocked := map[flowPair]bool{}
	awaiting := map[flowPair]bool{}
	for _, t := range c.Threads {
		if t.Stale {
			continue
		}
		if t.Status != ThreadBlocked && t.Status != ThreadAwaitingReply {
			continue
		}
		ps := t.Participants
		for i := 0; i < len(ps); i++ {
			for j := i + 1; j < len(ps); j++ {
				k := orderedPair(ps[i], ps[j])
				switch t.Status {
				case ThreadBlocked:
					blocked[k] = true
				case ThreadAwaitingReply:
					awaiting[k] = true
				}
			}
		}
	}

	out := make([]FlowEdge, 0, len(c.Edges))
	for _, e := range c.Edges {
		k := orderedPair(e.From, e.To)
		fe := FlowEdge{From: e.From, To: e.To, Count: e.Count}
		if blocked[k] {
			fe.Blocked = true
		} else if awaiting[k] {
			fe.Awaiting = true
		}
		out = append(out, fe)
	}

	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := out[i].rank(), out[j].rank()
		if ri != rj {
			return ri < rj
		}
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	return out
}

// flowPair is an order-independent key for a link between two handles, so a
// thread's status matches an edge regardless of which direction the edge runs.
type flowPair struct{ a, b string }

// orderedPair returns the two handles as an order-independent flowPair key.
func orderedPair(a, b string) flowPair {
	if a <= b {
		return flowPair{a: a, b: b}
	}
	return flowPair{a: b, b: a}
}
