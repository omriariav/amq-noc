package state

import (
	"testing"
	"time"
)

func opAgent(handle string) Agent   { return Agent{Handle: handle, Liveness: LivenessAlive} }
func deadAgent(handle string) Agent { return Agent{Handle: handle, Liveness: LivenessDead} }

// One operational agent out of several needing the operator promotes the whole
// session to needs-you, while only that agent carries the needs-you tier.
func TestAttention_OneAgentNeedsYouPromotesSession(t *testing.T) {
	agents := []Agent{opAgent("a"), opAgent("b"), opAgent("c"), opAgent("d")}
	threads := []ThreadSummary{
		{ID: "t1", Participants: []string{"b", "user"}, Triage: TriageNeedsYou, AttnReason: AttnApprove, NeedsYouOwner: "b"},
	}

	headline, unowned := attachAttention(agents, threads)

	if headline.State != TriageNeedsYou {
		t.Fatalf("session headline = %q, want needs-you", headline.State)
	}
	if headline.Reason != AttnApprove {
		t.Fatalf("session reason = %q, want approve", headline.Reason)
	}
	if unowned.State != TriageClear {
		t.Fatalf("unowned = %q, want clear (live asker owns it)", unowned.State)
	}
	for _, ag := range agents {
		want := TriageClear
		if ag.Handle == "b" {
			want = TriageNeedsYou
		}
		if ag.Attention.State != want {
			t.Errorf("agent %s attention = %q, want %q", ag.Handle, ag.Attention.State, want)
		}
	}
}

// Regression: needs-you ownership comes from the actual asker (the ask sender),
// not the lexicographically first participant. A cto/fullstack/user thread where
// fullstack asks must attribute needs-you to fullstack, not cto.
func TestNeedsYouOwnerIsAskerNotFirstParticipant(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	msgs := []Message{
		{ID: "m1", From: "cto", To: []string{"fullstack"}, Thread: "ask/ship", Kind: KindStatus, Created: now.Add(-2 * time.Minute)},
		{ID: "m2", From: "fullstack", To: []string{"user"}, Thread: "ask/ship", Subject: "APPROVAL: ship it?", Kind: KindQuestion, Created: now.Add(-1 * time.Minute), Owner: "user", State: MailboxNew},
	}
	agents := []Agent{{Handle: "cto", Liveness: LivenessAlive}, {Handle: "fullstack", Liveness: LivenessAlive}}

	coord := buildCoordination(collapseInput{messages: msgs, agents: agents}, now, Thresholds{})
	if len(coord.Threads) != 1 {
		t.Fatalf("threads = %d, want 1", len(coord.Threads))
	}
	th := coord.Threads[0]
	if th.Triage != TriageNeedsYou {
		t.Fatalf("triage = %q, want needs-you", th.Triage)
	}
	if len(th.Participants) == 0 || th.Participants[0] != "cto" {
		t.Fatalf("precondition: want cto sorted first, got participants %v", th.Participants)
	}
	if th.NeedsYouOwner != "fullstack" {
		t.Fatalf("needs-you owner = %q, want fullstack (the asker, not the first participant)", th.NeedsYouOwner)
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
		t.Fatalf("fullstack attention = %q, want needs-you", got["fullstack"])
	}
	if got["cto"] != TriageClear {
		t.Fatalf("cto attention = %q, want clear (not the asker)", got["cto"])
	}
}

// Severity precedence: needs-you > blocked > gated > at-risk > clear.
func TestAgentAttention_SeverityPrecedence(t *testing.T) {
	ag := opAgent("b")

	// blocked beats gated and at-risk
	got := computeAgentAttention(ag, []ThreadSummary{
		{ID: "g", Participants: []string{"b", "c"}, Triage: TriageGated},
		{ID: "r", Participants: []string{"b", "c"}, Triage: TriageAtRisk},
		{ID: "x", Participants: []string{"b", "c"}, Triage: TriageBlocked},
	}).State
	if got != TriageBlocked {
		t.Fatalf("blocked should beat gated/at-risk, got %q", got)
	}

	// needs-you (agent is the asker) beats blocked
	got = computeAgentAttention(ag, []ThreadSummary{
		{ID: "x", Participants: []string{"b", "c"}, Triage: TriageBlocked},
		{ID: "ny", Participants: []string{"b", "user"}, Triage: TriageNeedsYou, NeedsYouOwner: "b"},
	}).State
	if got != TriageNeedsYou {
		t.Fatalf("needs-you should beat blocked, got %q", got)
	}

	// gated beats at-risk
	got = computeAgentAttention(ag, []ThreadSummary{
		{ID: "r", Participants: []string{"b", "c"}, Triage: TriageAtRisk},
		{ID: "g", Participants: []string{"b", "c"}, Triage: TriageGated},
	}).State
	if got != TriageGated {
		t.Fatalf("gated should beat at-risk, got %q", got)
	}
}

// Historical or stale evidence must never promote live attention.
func TestAttention_HistoricalAndStaleDoNotPromote(t *testing.T) {
	agents := []Agent{opAgent("b")}
	threads := []ThreadSummary{
		{ID: "hist", Participants: []string{"b", "user"}, Triage: TriageNeedsYou, Historical: true, NeedsYouOwner: "b"},
		{ID: "stale", Participants: []string{"b", "c"}, Triage: TriageBlocked, Stale: true},
	}

	headline, unowned := attachAttention(agents, threads)

	if headline.State != TriageClear {
		t.Fatalf("headline = %q, want clear (historical/stale must not promote)", headline.State)
	}
	if unowned.State != TriageClear {
		t.Fatalf("unowned = %q, want clear", unowned.State)
	}
	if agents[0].Attention.State != TriageClear {
		t.Fatalf("agent attention = %q, want clear", agents[0].Attention.State)
	}
}

// Evidence that cannot be attributed to a live agent (dead/missing asker or
// participant, or an operator-only thread) rolls up as session-level unowned
// evidence and still promotes the session headline, without pinning any live
// agent.
func TestSessionAttention_UnownedEvidence(t *testing.T) {
	t.Run("dead asker", func(t *testing.T) {
		agents := []Agent{opAgent("a"), deadAgent("b")}
		threads := []ThreadSummary{
			{ID: "ny", Participants: []string{"b", "user"}, Triage: TriageNeedsYou, AttnReason: AttnGeneric, NeedsYouOwner: "b"},
		}
		headline, unowned := attachAttention(agents, threads)
		if unowned.State != TriageNeedsYou {
			t.Fatalf("unowned = %q, want needs-you (asker is dead)", unowned.State)
		}
		if headline.State != TriageNeedsYou {
			t.Fatalf("headline = %q, want needs-you (rolls up unowned)", headline.State)
		}
		for _, ag := range agents {
			if ag.Attention.State != TriageClear {
				t.Errorf("agent %s = %q, want clear (no live owner)", ag.Handle, ag.Attention.State)
			}
		}
	})

	t.Run("missing participant blocked", func(t *testing.T) {
		agents := []Agent{{Handle: "z", Liveness: LivenessMissing}}
		threads := []ThreadSummary{{ID: "blk", Participants: []string{"z", "c"}, Triage: TriageBlocked}}
		headline, unowned := attachAttention(agents, threads)
		if headline.State != TriageBlocked || unowned.State != TriageBlocked {
			t.Fatalf("missing-participant blocked: headline=%q unowned=%q, want blocked/blocked", headline.State, unowned.State)
		}
	})

	t.Run("operator-only thread", func(t *testing.T) {
		agents := []Agent{opAgent("a")}
		threads := []ThreadSummary{{ID: "op", Participants: []string{"user"}, Triage: TriageNeedsYou}}
		headline, unowned := attachAttention(agents, threads)
		if headline.State != TriageNeedsYou || unowned.State != TriageNeedsYou {
			t.Fatalf("operator-only needs-you: headline=%q unowned=%q, want needs-you", headline.State, unowned.State)
		}
		if agents[0].Attention.State != TriageClear {
			t.Fatalf("agent a = %q, want clear (not the asker)", agents[0].Attention.State)
		}
	})
}
