package console

import (
	"sort"
	"strings"

	"github.com/omriariav/amq-noc/internal/state"
)

// row is one navigable line in a view. Every view projects the (filtered)
// snapshot into an ordered []row; j/k/arrows move the cursor over them, enter
// drills, space peeks, etc. The ID is STABLE across snapshot rebuilds (a session
// name, a thread id, an agent handle) so the cursor survives a refresh; group
// labels the attention bucket a row belongs to so a vanished selection can fall
// back to the nearest SAME-GROUP row.
type row struct {
	// ID is the stable identity (session name / thread id / agent handle).
	ID string
	// group is the attention bucket (e.g. "needs-you", "running", "stopped"),
	// used for nearest-same-group fallback when a selection disappears.
	group string
	// kind distinguishes what the row points at, so the overlay/drill logic knows
	// whether the selection is a session, a thread, or an agent.
	kind rowKind
}

// rowKind is what a navigable row points at.
type rowKind int

const (
	rowSession rowKind = iota
	rowThread
	rowAgent
)

// triageRank orders triage tiers by attention (lower = more urgent). It drives
// the attention-first grouping the brief mandates (needs-you > blocked > gated
// > at-risk > clear).
func triageRank(t state.Triage) int {
	switch t {
	case state.TriageNeedsYou:
		return 0
	case state.TriageBlocked:
		return 1
	case state.TriageGated:
		return 2
	case state.TriageAtRisk:
		return 3
	default:
		return 4
	}
}

// sessionTier derives the single most-urgent triage tier for a session from its
// rollup, so the board can sort/group sessions attention-first.
func sessionTier(s state.Session) state.Triage {
	switch {
	case s.Rollup.NeedsYou > 0:
		return state.TriageNeedsYou
	case s.Rollup.Blocked > 0:
		return state.TriageBlocked
	case s.Rollup.Gated > 0:
		return state.TriageGated
	case s.Rollup.AtRisk > 0:
		return state.TriageAtRisk
	default:
		return state.TriageClear
	}
}

// livenessRank ranks a session by the liveliness of its agents so attention-first
// ordering puts running/degraded sessions above fully-stopped ones when triage
// ties. Lower = should appear higher.
func livenessRank(s state.Session) int {
	hasAlive := false
	hasDegraded := false
	for _, a := range s.Agents {
		switch a.Liveness {
		case state.LivenessAlive:
			hasAlive = true
		case state.LivenessWakeLive, state.LivenessDeadMailboxLive, state.LivenessStale:
			hasDegraded = true
		}
	}
	switch {
	case hasAlive:
		return 0
	case hasDegraded:
		return 1
	default:
		return 2 // all dead/missing -> stopped, sinks below
	}
}

// boardGroup labels a session's attention bucket for the board: needs-you /
// degraded sessions float above plain running, which float above stopped.
func boardGroup(s state.Session) string {
	tier := sessionTier(s)
	if tier != state.TriageClear {
		return string(tier)
	}
	switch livenessRank(s) {
	case 0:
		return "running"
	case 1:
		return "degraded"
	default:
		return "stopped"
	}
}

// boardSessions returns the filtered sessions sorted attention-first: by most
// urgent triage tier, then by liveness (running/degraded above stopped), then by
// name for stability. The snapshot's own session slice is NOT mutated.
func boardSessions(snap state.Snapshot, f Filter) []state.Session {
	out := make([]state.Session, 0, len(snap.Sessions))
	for _, s := range snap.Sessions {
		if f.matchSession(s) {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ti, tj := triageRank(sessionTier(out[i])), triageRank(sessionTier(out[j]))
		if ti != tj {
			return ti < tj
		}
		li, lj := livenessRank(out[i]), livenessRank(out[j])
		if li != lj {
			return li < lj
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// boardRows projects the board's sessions into navigable rows.
func boardRows(snap state.Snapshot, f Filter) []row {
	sessions := boardSessions(snap, f)
	rows := make([]row, 0, len(sessions))
	for _, s := range sessions {
		rows = append(rows, row{ID: sessionKey(s), group: boardGroup(s), kind: rowSession})
	}
	return rows
}

// sortThreads returns the filtered threads of a session sorted urgency-first: by
// triage tier, then blocked/awaiting status weight, then most-recent event, then
// id. This is the COLLAPSED-THREAD BUS order.
func sortThreads(s state.Session, f Filter) []state.ThreadSummary {
	out := make([]state.ThreadSummary, 0, len(s.Coordination.Threads))
	for _, t := range s.Coordination.Threads {
		if f.matchThread(t) {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := triageRank(out[i].Triage), triageRank(out[j].Triage)
		if ri != rj {
			return ri < rj
		}
		si, sj := statusRank(out[i].Status), statusRank(out[j].Status)
		if si != sj {
			return si < sj
		}
		if !out[i].LastEventAt.Equal(out[j].LastEventAt) {
			return out[i].LastEventAt.After(out[j].LastEventAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// sortThreadsNewest returns the filtered threads of a session sorted for the NOC
// live-detail pane: newest activity first, then attention labels for ties. The
// shared collapsed-thread bus keeps urgency-first ordering in sortThreads.
func sortThreadsNewest(s state.Session, f Filter) []state.ThreadSummary {
	out := make([]state.ThreadSummary, 0, len(s.Coordination.Threads))
	for _, t := range s.Coordination.Threads {
		if f.matchThread(t) {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].LastEventAt.Equal(out[j].LastEventAt) {
			return out[i].LastEventAt.After(out[j].LastEventAt)
		}
		ri, rj := triageRank(out[i].Triage), triageRank(out[j].Triage)
		if ri != rj {
			return ri < rj
		}
		si, sj := statusRank(out[i].Status), statusRank(out[j].Status)
		if si != sj {
			return si < sj
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// statusRank orders thread statuses by attention within a triage tier.
func statusRank(s state.ThreadStatus) int {
	switch s {
	case state.ThreadBlocked:
		return 0
	case state.ThreadAwaitingReply:
		return 1
	case state.ThreadOpen:
		return 2
	default:
		return 3
	}
}

// detailAgents returns the filtered agents of a session grouped triage/state:
// alive/dead-mailbox-live first, then stale, then dead/missing; ties by handle.
func detailAgents(s state.Session, f Filter) []state.Agent {
	out := make([]state.Agent, 0, len(s.Agents))
	for _, a := range s.Agents {
		if f.matchAgent(a) {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := agentRank(out[i].Liveness), agentRank(out[j].Liveness)
		if ri != rj {
			return ri < rj
		}
		return out[i].Handle < out[j].Handle
	})
	return out
}

// agentRank orders agents by run-state attention (alive first).
func agentRank(l state.Liveness) int {
	switch l {
	case state.LivenessAlive:
		return 0
	case state.LivenessWakeLive, state.LivenessDeadMailboxLive:
		return 1
	case state.LivenessStale:
		return 2
	case state.LivenessDead:
		return 3
	default:
		return 4
	}
}

// detailRows projects a session's agents (top) then its thread bus (middle) into
// one navigable row list, so j/k moves through agents and then threads. Agents
// sort first because the brief puts the roster at the top of the detail view.
func detailRows(s state.Session, f Filter) []row {
	var rows []row
	for _, a := range detailAgents(s, f) {
		rows = append(rows, row{ID: "agent:" + a.Handle, group: string(a.Liveness), kind: rowAgent})
	}
	for _, t := range sortThreads(s, f) {
		rows = append(rows, row{ID: "thread:" + t.ID, group: string(t.Triage), kind: rowThread})
	}
	return rows
}

// rowsFor returns the navigable rows for the model's current route, applying the
// active filter. It is the single source the keymap and the renderer agree on,
// so the cursor index and the rendered highlight never drift.
func (m Model) rowsFor() []row {
	switch m.route {
	case routeBoard:
		return boardRows(m.snapshot, m.filter)
	case routeSession:
		s, ok := m.currentSession()
		if !ok {
			return nil
		}
		return detailRows(s, m.filter)
	default:
		return nil
	}
}

// currentSession finds the drilled-into session by its STABLE name in the live
// snapshot, so a refresh that replaced the snapshot still resolves the same
// session. Returns ok=false when the session has vanished.
func (m Model) currentSession() (state.Session, bool) {
	for _, s := range m.snapshot.Sessions {
		if sessionKey(s) == m.session {
			return s, true
		}
	}
	return state.Session{}, false
}

// sessionKey is the stable identity of a session row (its name, or a sentinel
// for the rootless layout so an empty name still has a navigable key).
func sessionKey(s state.Session) string {
	if strings.TrimSpace(s.Name) == "" {
		return "(root)"
	}
	return s.Name
}

// resolveFocus recomputes the focus index from the stable selectedID against the
// CURRENT row set. This is the selection-stability core: after any snapshot
// replacement or filter change, the cursor stays on the same logical row by ID;
// if that row has VANISHED, it falls back to the nearest row in the SAME GROUP
// (then clamps into range), never resetting to the top and never going out of
// bounds. It returns the new (id, index); an empty row set yields ("", 0).
func resolveFocus(rows []row, wantID string, prevIndex int) (string, int) {
	if len(rows) == 0 {
		return "", 0
	}
	// Exact match by stable id: the row still exists, keep it.
	for i, r := range rows {
		if r.ID == wantID {
			return r.ID, i
		}
	}
	// The selection vanished. Find the group it belonged to (from the prior
	// index position if we can infer it) and snap to the nearest same-group row.
	wantGroup := groupForID(rows, wantID)
	if wantGroup == "" && prevIndex >= 0 && prevIndex < len(rows) {
		wantGroup = rows[prevIndex].group
	}
	if wantGroup != "" {
		// Prefer the same-group row nearest the previous index.
		best := -1
		bestDist := 1 << 30
		for i, r := range rows {
			if r.group != wantGroup {
				continue
			}
			d := prevIndex - i
			if d < 0 {
				d = -d
			}
			if d < bestDist {
				bestDist = d
				best = i
			}
		}
		if best >= 0 {
			return rows[best].ID, best
		}
	}
	// No same-group row left: clamp the prior index into range.
	idx := prevIndex
	if idx < 0 {
		idx = 0
	}
	if idx >= len(rows) {
		idx = len(rows) - 1
	}
	return rows[idx].ID, idx
}

// groupForID returns the group of the row with id, or "" if absent. (Used only
// when the id is still present, which resolveFocus already handles, so it is a
// best-effort helper for clarity.)
func groupForID(rows []row, id string) string {
	for _, r := range rows {
		if r.ID == id {
			return r.group
		}
	}
	return ""
}

// reselect re-derives focus/selectedID for the current route+filter after a
// snapshot replace or a navigation change. It is the ONE place selection
// stability is enforced; every mutation of route/filter/snapshot funnels here.
func (m Model) reselect() Model {
	rows := m.rowsFor()
	id, idx := resolveFocus(rows, m.selectedID, m.focus)
	m.selectedID = id
	m.focus = idx
	return m
}

// moveFocus shifts the cursor by delta within the current row set, clamping at
// the ends, and updates selectedID to the new row's stable id. It NEVER wraps and
// NEVER scrolls the viewport off the data.
func (m Model) moveFocus(delta int) Model {
	rows := m.rowsFor()
	if len(rows) == 0 {
		m.selectedID = ""
		m.focus = 0
		return m
	}
	idx := m.focus + delta
	if idx < 0 {
		idx = 0
	}
	if idx >= len(rows) {
		idx = len(rows) - 1
	}
	m.focus = idx
	m.selectedID = rows[idx].ID
	return m
}

// selectedRow returns the currently-focused row, ok=false when none.
func (m Model) selectedRow() (row, bool) {
	rows := m.rowsFor()
	if m.focus < 0 || m.focus >= len(rows) {
		return row{}, false
	}
	return rows[m.focus], true
}
