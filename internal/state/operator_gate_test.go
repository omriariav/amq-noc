package state

import (
	"testing"
	"time"
)

// S5 operator-gate contract.
//
// When an agent holds for a human-only decision (manual RC, approval, commit), it
// emits a STRUCTURAL ask addressed to the operator (to: user). The NOC surfaces
// that as needs-you DETERMINISTICALLY - from the recipient, never from prose over a
// p2p status. Three invariants:
//  1. A structural operator gate (to: user, action subject) is needs-you, owned by
//     the asker that is holding.
//  2. A bare p2p hold ACK between agents - even one whose prose mentions manual RC
//     or the user - is NOT needs-you.
//  3. The gate clears once the operator responds (the latest message is no longer
//     addressed to the operator), so a stale gate does not linger.

func TestOperatorGate_StructuralAskIsNeedsYou(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	msgs := []Message{
		{ID: "g1", From: "fullstack", To: []string{"user"}, Thread: "gate/manual-rc",
			Subject: "APPROVAL: manual RC test + commit decision", Kind: KindQuestion,
			Created: now.Add(-1 * time.Minute), Owner: "user", State: MailboxNew,
			Body: "Holding the slice uncommitted. Need your decision: test the RC, then approve commit."},
	}
	agents := []Agent{opAgent("cto"), opAgent("fullstack")}

	coord := buildCoordination(collapseInput{messages: msgs, agents: agents}, now, Thresholds{})
	th := findThread(t, coord, "gate/manual-rc")
	if th.Triage != TriageNeedsYou {
		t.Fatalf("gate triage = %q, want needs-you", th.Triage)
	}
	if th.NeedsYouOwner != "fullstack" {
		t.Fatalf("needs-you owner = %q, want fullstack (the asker holding the gate)", th.NeedsYouOwner)
	}

	headline, _ := attachAttention(agents, coord.Threads)
	if headline.State != TriageNeedsYou {
		t.Fatalf("session headline = %q, want needs-you", headline.State)
	}
	got := map[string]Triage{}
	for _, ag := range agents {
		got[ag.Handle] = ag.Attention.State
	}
	if got["fullstack"] != TriageNeedsYou {
		t.Fatalf("fullstack attention = %q, want needs-you (owns the gate)", got["fullstack"])
	}
	if got["cto"] != TriageClear {
		t.Fatalf("cto attention = %q, want clear (not the asker)", got["cto"])
	}
}

func TestOperatorGate_P2PHoldAloneIsNotNeedsYou(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	// A hold ACK addressed agent->agent that MENTIONS manual RC / the user in prose
	// must NOT become needs-you. Deterministic: needs-you comes from the recipient
	// (to: user), never from prose over a p2p status.
	msgs := []Message{
		{ID: "p1", From: "fullstack", To: []string{"cto"}, Thread: "p2p/cto__fullstack",
			Subject: "ACK: holding uncommitted for user manual RC and commit decision", Kind: KindStatus,
			Created: now.Add(-1 * time.Minute), Owner: "cto", State: MailboxNew,
			Body: "Holding for the user's manual RC test and explicit commit decision."},
	}
	agents := []Agent{opAgent("cto"), opAgent("fullstack")}

	coord := buildCoordination(collapseInput{messages: msgs, agents: agents}, now, Thresholds{})
	th := findThread(t, coord, "p2p/cto__fullstack")
	if th.Triage == TriageNeedsYou {
		t.Fatalf("p2p hold triage = %q, want NOT needs-you (a p2p status is not an operator gate)", th.Triage)
	}

	headline, unowned := attachAttention(agents, coord.Threads)
	if headline.State == TriageNeedsYou || unowned.State == TriageNeedsYou {
		t.Fatalf("needs-you must not fire from a p2p hold: headline=%q unowned=%q", headline.State, unowned.State)
	}
}

func TestOperatorGate_ClearsAfterOperatorResponse(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	// Gate ask (fullstack->user) followed by the operator's reply (user->fullstack)
	// on the same thread. The latest message is no longer addressed to the operator,
	// so the gate clears - no lingering needs-you.
	msgs := []Message{
		{ID: "g1", From: "fullstack", To: []string{"user"}, Thread: "gate/manual-rc",
			Subject: "APPROVAL: manual RC test + commit decision", Kind: KindQuestion,
			Created: now.Add(-5 * time.Minute), Owner: "user", State: MailboxCur,
			Body: "Need your decision: approve commit or request changes."},
		{ID: "g2", From: "user", To: []string{"fullstack"}, Thread: "gate/manual-rc",
			Subject: "Re: APPROVAL: approved", Kind: KindAnswer,
			Created: now.Add(-1 * time.Minute), Owner: "fullstack", State: MailboxNew,
			Body: "Approved. Go ahead and commit."},
	}
	agents := []Agent{opAgent("cto"), opAgent("fullstack")}

	coord := buildCoordination(collapseInput{messages: msgs, agents: agents}, now, Thresholds{})
	th := findThread(t, coord, "gate/manual-rc")
	if th.Triage == TriageNeedsYou {
		t.Fatalf("gate triage = %q, want NOT needs-you after the operator responds", th.Triage)
	}

	headline, _ := attachAttention(agents, coord.Threads)
	if headline.State == TriageNeedsYou {
		t.Fatalf("session headline = %q, want not needs-you after operator reply", headline.State)
	}
}

func TestReviewApprovalClearsPriorBlockedThread(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	msgs := []Message{
		{ID: "b1", From: "fullstack", To: []string{"cto"}, Thread: "p2p/cto__fullstack",
			Subject: "Review: blocker found", Kind: KindReviewResponse,
			Created: now.Add(-10 * time.Minute), Owner: "cto", State: MailboxCur,
			Body: "BLOCKER: command rejects valid operator gates."},
		{ID: "b2", From: "fullstack", To: []string{"cto"}, Thread: "p2p/cto__fullstack",
			Subject: "APPROVED: operator gate fix is GREEN", Kind: KindReviewResponse,
			Created: now.Add(-1 * time.Minute), Owner: "cto", State: MailboxNew,
			Body: "Verified independently. Approved."},
	}
	agents := []Agent{opAgent("cto"), opAgent("fullstack")}

	coord := buildCoordination(collapseInput{messages: msgs, agents: agents}, now, Thresholds{})
	th := findThread(t, coord, "p2p/cto__fullstack")
	if th.Status != ThreadResolved || th.Triage != TriageClear {
		t.Fatalf("approved review should clear prior block, got status=%q triage=%q subject=%q", th.Status, th.Triage, th.Subject)
	}
}

func TestReviewNonApprovalDoesNotClearPriorBlockedThread(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		subject string
		body    string
	}{
		{name: "not-approved", subject: "Not approved", body: "Needs rework before release."},
		{name: "unresolved", subject: "Review remains unresolved", body: "The original issue remains unresolved."},
		{name: "not-green", subject: "Not green", body: "Validation is not green yet."},
		{name: "greenfield", subject: "Greenfield note", body: "This is a greenfield follow-up, not a release approval."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msgs := []Message{
				{ID: "b1", From: "fullstack", To: []string{"cto"}, Thread: "p2p/cto__fullstack",
					Subject: "Review: blocker found", Kind: KindReviewResponse,
					Created: now.Add(-10 * time.Minute), Owner: "cto", State: MailboxCur,
					Body: "BLOCKER: command rejects valid operator gates."},
				{ID: "b2", From: "fullstack", To: []string{"cto"}, Thread: "p2p/cto__fullstack",
					Subject: tc.subject, Kind: KindReviewResponse,
					Created: now.Add(-1 * time.Minute), Owner: "cto", State: MailboxNew,
					Body: tc.body},
			}
			agents := []Agent{opAgent("cto"), opAgent("fullstack")}

			coord := buildCoordination(collapseInput{messages: msgs, agents: agents}, now, Thresholds{})
			th := findThread(t, coord, "p2p/cto__fullstack")
			if th.Status != ThreadBlocked || th.Triage != TriageBlocked {
				t.Fatalf("non-approval should not clear prior block, got status=%q triage=%q subject=%q", th.Status, th.Triage, th.Subject)
			}
		})
	}
}
