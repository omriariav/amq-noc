package state

import "strings"

// Attention is the derived, thread-evidence-based headline attention state for
// an agent or a session/team. It answers "what here needs attention?" at the
// team and agent level; the underlying threads are the evidence that explains
// it. Attention is DISTINCT from process Liveness: a dead/missing agent is a
// liveness concern, not an attention tier, so it stays clear here and its
// unresolved thread evidence rolls up to the session as unowned instead.
//
// State is the max-severity Triage tier (needs-you > blocked > gated > at-risk >
// clear) over the relevant CURRENT (non-historical, non-stale) thread evidence.
// Reason carries the needs-you reason (approve/goal-reached/generic) when
// State == TriageNeedsYou, and is AttnNone otherwise.
type Attention struct {
	State  Triage
	Reason AttnReason
}

// triageSeverity ranks triage tiers for max-severity comparison; lower is more
// severe. Mirrors the documented order on Triage: NeedsYou > Blocked > Gated >
// AtRisk > Clear.
func triageSeverity(t Triage) int {
	switch t {
	case TriageNeedsYou:
		return 0
	case TriageBlocked:
		return 1
	case TriageGated:
		return 2
	case TriageAtRisk:
		return 3
	default:
		return 4
	}
}

// mergeAttention returns the more-severe of a and b. On a needs-you tie it keeps
// the most urgent reason (approve > goal-reached > generic).
func mergeAttention(a, b Attention) Attention {
	switch {
	case triageSeverity(b.State) < triageSeverity(a.State):
		return b
	case triageSeverity(a.State) < triageSeverity(b.State):
		return a
	}
	if a.State == TriageNeedsYou && b.Reason.Rank() < a.Reason.Rank() {
		return b
	}
	return a
}

// threadAttention is the attention a thread contributes as CURRENT evidence, or
// clear when it carries none (clear triage, or age-decayed via Historical/Stale
// so it must never promote live state).
func threadAttention(th ThreadSummary) Attention {
	if th.Historical || th.Stale || th.Triage == TriageClear {
		return Attention{State: TriageClear}
	}
	reason := AttnNone
	if th.Triage == TriageNeedsYou {
		reason = th.AttnReason
	}
	return Attention{State: th.Triage, Reason: reason}
}

// agentOwnsThread reports whether an agent owns a thread's attention. A needs-you
// ask is owned only by its recorded NeedsYouOwner (the asker/declarer waiting on
// the human, derived from the actual sender in collapse), never a sorted
// participant. A blocked/gated/at-risk thread is owned by any participating
// agent.
func agentOwnsThread(handle string, th ThreadSummary) bool {
	if th.Triage == TriageNeedsYou {
		return th.NeedsYouOwner != "" && strings.EqualFold(th.NeedsYouOwner, handle)
	}
	return threadHasParticipant(th, handle)
}

// computeAgentAttention derives an agent's attention from the current threads it
// owns. Only OPERATIONAL agents accrue attention; a non-operational agent stays
// clear (its concern is liveness, a separate axis) and its evidence is treated
// as unowned at the session level instead.
func computeAgentAttention(ag Agent, threads []ThreadSummary) Attention {
	att := Attention{State: TriageClear}
	if !agentOperational(ag) {
		return att
	}
	for _, th := range threads {
		ta := threadAttention(th)
		if ta.State == TriageClear {
			continue
		}
		if agentOwnsThread(ag.Handle, th) {
			att = mergeAttention(att, ta)
		}
	}
	return att
}

// attachAttention sets each agent's derived Attention in place and returns the
// session headline (max severity over the operational agents' attention plus the
// unowned evidence) and the unowned attention (current evidence not attributable
// to any operational agent: a dead/missing participant, or an operator-only
// thread). Historical/stale evidence contributes nothing, so it never promotes a
// team to live attention.
func attachAttention(agents []Agent, threads []ThreadSummary) (headline, unowned Attention) {
	headline = Attention{State: TriageClear}
	unowned = Attention{State: TriageClear}
	for i := range agents {
		agents[i].Attention = computeAgentAttention(agents[i], threads)
		headline = mergeAttention(headline, agents[i].Attention)
	}
	for _, th := range threads {
		ta := threadAttention(th)
		if ta.State == TriageClear {
			continue
		}
		if threadOwnedByOperational(th, agents) {
			continue
		}
		unowned = mergeAttention(unowned, ta)
	}
	// A session's HEADLINE work status comes from its OPERATIONAL agents' owned
	// attention. Unowned evidence is orphaned (no operational owner): an unowned
	// NEEDS-YOU is still a real human action item and drives the headline, but an
	// unowned non-human wait (block/gate/at-risk) requires an operational waiter,
	// so it stays detail-only (UnownedAttention) and never makes a stopped session
	// "waiting".
	if unowned.State == TriageNeedsYou {
		headline = mergeAttention(headline, unowned)
	}
	return headline, unowned
}

// threadOwnedByOperational reports whether any operational agent owns the
// thread's attention. Used to decide whether evidence rolls up as unowned.
func threadOwnedByOperational(th ThreadSummary, agents []Agent) bool {
	for _, ag := range agents {
		if agentOperational(ag) && agentOwnsThread(ag.Handle, th) {
			return true
		}
	}
	return false
}

// agentOperational reports whether an agent is live enough to own attention
// (alive or wake-live). dead-mailbox-live is deliberately NOT operational: its
// process is gone, so it accrues no work attention and its unresolved evidence
// rolls up to the session as unowned rather than making the agent/team "waiting".
// Mirrors the console operational gate so the data layer and renderer agree.
func agentOperational(a Agent) bool {
	switch a.Liveness {
	case LivenessAlive, LivenessWakeLive:
		return true
	default:
		return false
	}
}

// threadHasParticipant reports whether handle is a participant of th (case
// insensitive).
func threadHasParticipant(th ThreadSummary, handle string) bool {
	for _, p := range th.Participants {
		if strings.EqualFold(p, handle) {
			return true
		}
	}
	return false
}

// SessionLeadDown reports an orchestrated session whose lead agent is not
// operational while at least one other agent still is: the squad continues
// but its driver is gone. Deterministic liveness only, never prose inference;
// false when the session is not orchestrated or carries no lead row at all
// (an unlaunched lead is a roster question, not a runtime regression).
func SessionLeadDown(sess Session) bool {
	if !sess.Orchestrated || strings.TrimSpace(sess.LeadHandle) == "" {
		return false
	}
	leadFound := false
	leadOperational := false
	otherOperational := false
	for _, ag := range sess.Agents {
		if ag.IsLead || strings.EqualFold(ag.Handle, sess.LeadHandle) {
			leadFound = true
			if agentOperational(ag) {
				leadOperational = true
			}
			continue
		}
		if agentOperational(ag) {
			otherOperational = true
		}
	}
	return leadFound && !leadOperational && otherOperational
}
