package console

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-noc/internal/state"
)

// clockOrDefault returns the probe's injected clock, or the real wall-clock when
// the probe has no clock. The render layer needs a "now" to age agent LastSeen
// signals; the snapshot itself does not carry a clock (keeping internal/state
// stdlib-only and side-effect-free), so the console supplies one. In production
// the probe's Now is the same clock state.Build used; in tests it is the fixture
// clock, so the rendered ages are deterministic.
func clockOrDefault(p state.Probe) func() time.Time {
	if p.Now != nil {
		return p.Now
	}
	return time.Now
}

// sessionStopped reports whether a session has rolled up to a STOPPED state:
// nothing outstanding in triage AND no agent that is alive or degraded. For a
// stopped session the agent run-states are EXPECTED (the team was torn down), so
// the labels read "stopped" rather than the alarming "process-dead"/"stale".
//
// It reuses boardGroup's bucketing so the board grouping and the agent labels
// never disagree about what "stopped" means.
func sessionStopped(s state.Session) bool {
	return boardGroup(s) == "stopped"
}

// agentStateLabel renders the session-aware, human-readable run-state vocabulary
// the DX review asked for, replacing the bare alive/stale/dead words:
//
//   - a STOPPED session renders every agent as "stopped" (expected after a
//     teardown — never the alarming "dead"/"stale" which read like a problem),
//   - otherwise the agent Liveness maps to clearer words:
//     alive             -> "alive"
//     wake-live         -> "wake-live"
//     stale (presence)  -> "stale-heartbeat"
//     dead (process)    -> "process-dead"
//     dead-mailbox-live -> its distinct at-risk label (kept verbatim — it is its
//     own zombie-heartbeat signal, not a plain dead/stale),
//     missing           -> "missing".
//
// For the stale/dead states an age is appended when LastSeen is known, e.g.
// "stale-heartbeat (3d)", computed from LastSeen against the injected clock.
func agentStateLabel(a state.Agent, stopped bool, now func() time.Time) string {
	if stopped {
		return "stopped"
	}
	switch a.Liveness {
	case state.LivenessAlive:
		return "alive"
	case state.LivenessWakeLive:
		return string(state.LivenessWakeLive)
	case state.LivenessDeadMailboxLive:
		// The explicit dead-process / live-mailbox case keeps its own distinct
		// at-risk label: something is still writing the mailbox while the process
		// is gone. It must NOT collapse into process-dead/stale.
		return string(state.LivenessDeadMailboxLive)
	case state.LivenessStale:
		return withAge("stale-heartbeat", a.LastSeen, now)
	case state.LivenessDead:
		return withAge("process-dead", a.LastSeen, now)
	case state.LivenessMissing:
		return "missing"
	default:
		return string(a.Liveness)
	}
}

// withAge appends an age suffix ("(3d)") to a state label when the agent's
// LastSeen is known and in the past relative to the injected clock. A zero or
// future LastSeen yields no suffix (we never fabricate an age we cannot trust).
func withAge(label string, lastSeen time.Time, now func() time.Time) string {
	if lastSeen.IsZero() {
		return label
	}
	age := now().Sub(lastSeen)
	if age <= 0 {
		return label
	}
	return fmt.Sprintf("%s (%s)", label, ageLabel(age))
}

// aliveCount returns how many of a session's agents are currently alive, and the
// total agent count — the inputs to the "agents: N/M alive" reconciliation line.
func aliveCount(s state.Session) (alive, total int) {
	total = len(s.Agents)
	for _, a := range s.Agents {
		if a.Liveness == state.LivenessAlive {
			alive++
		}
	}
	return alive, total
}

// agentsAliveLine renders the per-session "agents: N/M alive" line. It is a
// SEPARATE concept from the triage thread counts in the "[... blocked]" rollup:
// agents are processes, the rollup counts are THREADS. Keeping them on their own
// labeled line means a reader never conflates "2 blocked (threads)" with a count
// of stale agents.
func agentsAliveLine(s state.Session) string {
	alive, total := aliveCount(s)
	return fmt.Sprintf("agents: %d/%d alive", alive, total)
}

// unresolvedThreads returns a session's coordination threads that are still
// UNRESOLVED (Triage at-risk, blocked, or gated), urgency-sorted
// most-stale-first by
// LastEventAt (oldest event = most stale = highest urgency), then by id for a
// stable tie-break. This answers "who is blocked on whom" straight from --once.
func unresolvedThreads(s state.Session) []state.ThreadSummary {
	out := make([]state.ThreadSummary, 0, len(s.Coordination.Threads))
	for _, t := range s.Coordination.Threads {
		if t.Triage == state.TriageBlocked || t.Triage == state.TriageGated || t.Triage == state.TriageAtRisk {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		// Most stale first: the oldest LastEventAt is the most urgent.
		if !out[i].LastEventAt.Equal(out[j].LastEventAt) {
			return out[i].LastEventAt.Before(out[j].LastEventAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// threadTier renders the urgency tier word for an unresolved thread row.
func threadTier(t state.ThreadSummary) string {
	switch t.Triage {
	case state.TriageBlocked:
		return "blocked"
	case state.TriageGated:
		return "gated"
	case state.TriageAtRisk:
		return "at-risk"
	default:
		return string(t.Triage)
	}
}

// unresolvedThreadRow renders ONE unresolved-thread line for the --once board:
//
//	<participants>  <tier> · <subject> · <age> · <N> msgs
//
// e.g. "qa, cto  blocked · migration sign-off · 7m · 3 msgs". The age is the
// thread's freshness age (now - last event) computed by the snapshot; the tier
// word and subject are the load-bearing literals.
func unresolvedThreadRow(t state.ThreadSummary, now func() time.Time) string {
	parts := participantsLabel(t.Participants)
	subj := t.Subject
	if subj == "" {
		subj = shortID(t.ID)
	}
	return fmt.Sprintf("  %s  %s · %s · %s · %d %s",
		parts, threadTier(t), subj, threadAge(t, now), t.MessageCount,
		plural(t.MessageCount, "msg", "msgs"))
}

// participantsLabel renders a thread's participants as a comma list ("qa, cto"),
// the left column of an unresolved-thread row.
func participantsLabel(parts []string) string {
	if len(parts) == 0 {
		return "(unknown)"
	}
	return strings.Join(parts, ", ")
}

// threadAge returns the displayable age of a thread. It prefers the snapshot's
// pre-computed Freshness.Age (which is honest about its source); when that is
// zero it falls back to now - LastEventAt against the injected clock.
func threadAge(t state.ThreadSummary, now func() time.Time) string {
	if t.Freshness.Age > 0 {
		return ageLabel(t.Freshness.Age)
	}
	if !t.LastEventAt.IsZero() {
		if d := now().Sub(t.LastEventAt); d > 0 {
			return ageLabel(d)
		}
	}
	return "now"
}

// unresolvedSection renders the per-session unresolved-thread block for the
// --once board: the top maxUnresolvedRows threads (urgency-sorted), each on its
// own row, capped with a "+N more" line when the session has more. Returns ""
// when the session has no unresolved threads (the section is then omitted).
func unresolvedSection(s state.Session, now func() time.Time) string {
	threads := unresolvedThreads(s)
	if len(threads) == 0 {
		return ""
	}
	var b strings.Builder
	shown := threads
	if len(shown) > maxUnresolvedRows {
		shown = shown[:maxUnresolvedRows]
	}
	for _, t := range shown {
		b.WriteString(unresolvedThreadRow(t, now))
		b.WriteString("\n")
	}
	if extra := len(threads) - len(shown); extra > 0 {
		fmt.Fprintf(&b, "  +%d more\n", extra)
	}
	return b.String()
}

// maxUnresolvedRows caps the unresolved-thread rows shown per session on the
// --once board; the rest are summarized as "+N more".
const maxUnresolvedRows = 3
