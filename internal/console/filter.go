package console

import (
	"strings"

	"github.com/omriariav/amq-noc/internal/state"
)

// Filtering is a PURE, read-only narrowing of the snapshot. A Filter never reads
// the filesystem and never mutates the snapshot or the Model: it only decides
// which sessions / threads / agents a view shows. The same predicates are applied
// in EVERY view (board, detail, log, peek) so a typed filter is consistent.
//
// The typed-filter grammar (entered after `/`):
//
//	needs-you        -> only sessions/threads at the needs-you tier
//	gated           -> only gated sessions/threads
//	at-risk          -> only at-risk
//	blocked          -> only blocked
//	unread           -> only threads with an unread recipient (and the sessions
//	                    that contain one)
//	agent:<handle>   -> only sessions containing that agent handle / threads it
//	                    participates in
//	model:<model>    -> only sessions containing an agent on that engine/model
//	session:<name>   -> only the named session (substring, case-insensitive)
//
// An empty/zero Filter matches everything.

// kind enumerates the filter predicate families. It is derived once from the
// typed text so the per-row predicates do not re-parse on every check.
type filterKind int

const (
	filterNone filterKind = iota
	filterNeedsYou
	filterGated
	filterAtRisk
	filterBlocked
	filterUnread
	filterAgent
	filterModel
	filterSession
)

// parseFilter turns a typed filter expression into a Filter. Unknown text leaves
// the Filter at its zero value (match-everything) but records the raw text so the
// view can show "unknown filter" guidance rather than silently dropping it.
func parseFilter(raw string) Filter {
	t := strings.TrimSpace(raw)
	f := Filter{Raw: t}
	if t == "" {
		return f
	}
	lower := strings.ToLower(t)
	switch {
	case lower == "needs-you", lower == "needsyou", lower == "needs", lower == "needs-user", lower == "needsuser", lower == "needs_user":
		f.kind = filterNeedsYou
		f.Triage = state.TriageNeedsYou
	case lower == "gated", lower == "gate":
		f.kind = filterGated
		f.Triage = state.TriageGated
	case lower == "at-risk", lower == "atrisk", lower == "risk":
		f.kind = filterAtRisk
		f.Triage = state.TriageAtRisk
	case lower == "blocked", lower == "block":
		f.kind = filterBlocked
		f.Triage = state.TriageBlocked
	case lower == "unread":
		f.kind = filterUnread
	case strings.HasPrefix(lower, "agent:"):
		f.kind = filterAgent
		f.arg = strings.TrimSpace(t[len("agent:"):])
	case strings.HasPrefix(lower, "model:"):
		f.kind = filterModel
		f.arg = strings.TrimSpace(t[len("model:"):])
	case strings.HasPrefix(lower, "session:"):
		f.kind = filterSession
		f.arg = strings.TrimSpace(t[len("session:"):])
		f.Session = f.arg
	default:
		// Unknown expression: leave kind=filterNone (matches everything) but keep
		// Raw so the view can flag it as unrecognized.
		f.kind = filterNone
		f.unknown = true
	}
	return f
}

// active reports whether the filter narrows anything.
func (f Filter) active() bool { return f.kind != filterNone }

// filterSnapshot returns a copy of the snapshot with only the sessions that pass
// the filter (the static --once board reuses this). The snapshot-wide rollup is
// RECOMPUTED from the surviving sessions so the headline stays internally
// consistent with the body it describes: a filtered headline must not report
// triage counts for sessions it no longer shows. It never mutates the input.
func filterSnapshot(snap state.Snapshot, f Filter) state.Snapshot {
	if !f.active() {
		return snap
	}
	out := snap
	out.Sessions = make([]state.Session, 0, len(snap.Sessions))
	var rollup state.TriageRollup
	for _, s := range snap.Sessions {
		if f.matchSession(s) {
			out.Sessions = append(out.Sessions, s)
			rollup.Add(s.Rollup)
		}
	}
	out.Rollup = rollup
	return out
}

// matchSession reports whether a session passes the filter. A session passes a
// triage filter when its rollup carries at least one thread at that tier; an
// agent/model filter when it has a matching agent; an unread filter when any
// thread has an unread recipient; a session filter by name substring.
func (f Filter) matchSession(s state.Session) bool {
	switch f.kind {
	case filterNone:
		return true
	case filterNeedsYou:
		return s.Rollup.NeedsYou > 0
	case filterGated:
		return s.Rollup.Gated > 0
	case filterAtRisk:
		return s.Rollup.AtRisk > 0
	case filterBlocked:
		return s.Rollup.Blocked > 0
	case filterUnread:
		for _, t := range s.Coordination.Threads {
			if len(t.UnreadBy) > 0 {
				return true
			}
		}
		return false
	case filterAgent:
		for _, a := range s.Agents {
			if strings.EqualFold(a.Handle, f.arg) {
				return true
			}
		}
		return false
	case filterModel:
		for _, a := range s.Agents {
			if strings.Contains(strings.ToLower(a.Engine), strings.ToLower(f.arg)) {
				return true
			}
		}
		return false
	case filterSession:
		return strings.Contains(strings.ToLower(s.Name), strings.ToLower(f.arg))
	default:
		return true
	}
}

// matchThread reports whether a thread passes the filter (used in the detail /
// bus views). Triage filters compare the thread's own tier; unread checks the
// thread's unread recipients; agent filters check participation; model/session
// filters do not constrain an individual thread (they scope at the session level)
// so they pass every thread within an already-matched session.
func (f Filter) matchThread(t state.ThreadSummary) bool {
	switch f.kind {
	case filterNone:
		return true
	case filterNeedsYou:
		return t.Triage == state.TriageNeedsYou
	case filterGated:
		return t.Triage == state.TriageGated
	case filterAtRisk:
		return t.Triage == state.TriageAtRisk
	case filterBlocked:
		return t.Triage == state.TriageBlocked
	case filterUnread:
		return len(t.UnreadBy) > 0
	case filterAgent:
		for _, p := range t.Participants {
			if strings.EqualFold(p, f.arg) {
				return true
			}
		}
		return false
	case filterModel, filterSession:
		// Session-scoped filters do not narrow individual threads.
		return true
	default:
		return true
	}
}

// matchAgent reports whether an agent passes the filter (used in the detail
// agent roster). Triage/unread filters do not constrain an individual agent, so
// they pass; agent/model filters compare handle/engine.
func (f Filter) matchAgent(a state.Agent) bool {
	switch f.kind {
	case filterAgent:
		return strings.EqualFold(a.Handle, f.arg)
	case filterModel:
		return strings.Contains(strings.ToLower(a.Engine), strings.ToLower(f.arg))
	default:
		// needs-you / gated / at-risk / blocked / unread / session / none: not an
		// agent-level predicate, so every agent in a matched session passes.
		return true
	}
}
