// Package console: retained helpers from the removed copied project-scoped
// console (the amq-squad 0.1.0 split). The old Bubble Tea console (model.go,
// view.go, rows.go, filter.go, run.go, update.go, watcher.go, attach.go,
// labels.go) was deleted as dead code; the symbols below are the pieces the
// live NOC TUI (noc_*.go) still uses.
package console

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-noc/internal/state"
)

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

// Filter narrows what the views surface. It is a PURE predicate over the
// snapshot, applied in every view; an empty Filter matches everything. Triage /
// Session stay exported for callers (e.g. the CLI's --session flag) that want to
// preset a scope; the typed-filter machinery lives in the unexported fields and
// is parsed by parseFilter.
type Filter struct {
	// Triage, when non-empty, limits views to threads/sessions at this tier.
	Triage state.Triage
	// Session, when non-empty, scopes views to a single session name.
	Session string

	// Raw is the typed filter expression exactly as entered (for display).
	Raw string
	// kind is the parsed predicate family.
	kind filterKind
	// arg is the operand for agent:/model:/session: filters.
	arg string
	// unknown is set when Raw was non-empty but not a recognized expression.
	unknown bool
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

// ageLabel renders a duration compactly: "7m", "2h", "3d", "5s".
func ageLabel(d time.Duration) string {
	if d <= 0 {
		return "now"
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// shortID trims a namespace prefix for compact display ("p2p/cto__qa" -> "cto__qa").
func shortID(id string) string {
	if i := strings.IndexByte(id, '/'); i >= 0 && i+1 < len(id) {
		return id[i+1:]
	}
	return id
}
