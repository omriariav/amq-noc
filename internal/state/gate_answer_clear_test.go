package state

import (
	"testing"
	"time"
)

// #29 regression (found in v0.9.0 live dogfood): an operator ANSWER on a gate
// thread must clear needs-you even when the ask's body tripped declaresBlock
// (it merely CONTAINED "no-go" inside its own reply instructions) and the
// answer carries no affirmative clear word ("not yet", DENIED:). Once the
// operator spoke last, nothing on the thread waits for the human; the block
// may remain BLOCKED-tier evidence, owned by the agent's next move.

func dogfoodGateAsk(now time.Time) Message {
	return Message{
		ID: "ask", From: "copilot", To: []string{"user"}, Thread: "gate/dogfood",
		Subject: "APPROVAL: release amq-noc v0.9.0?", Kind: KindQuestion,
		Created: now.Add(-3 * time.Minute), Owner: "user", State: MailboxNew,
		Body: "Decision needed. Reply APPROVED:, or DENIED: / ANSWER: for no-go or a conditional response.",
	}
}

func TestOperatorGate_NegativeAnswerClearsNeedsYou(t *testing.T) {
	now := time.Date(2026, 6, 11, 14, 30, 0, 0, time.UTC)
	msgs := []Message{
		dogfoodGateAsk(now),
		{ID: "ans", From: "user", To: []string{"copilot"}, Thread: "gate/dogfood",
			Subject: "ANSWER: APPROVAL: release amq-noc v0.9.0?", Kind: KindAnswer,
			Created: now.Add(-1 * time.Minute), Owner: "copilot", State: MailboxCur,
			Body: "ANSWER: not yet - stand down, no release action."},
	}
	agents := []Agent{opAgent("copilot")}

	coord := buildCoordination(collapseInput{messages: msgs, agents: agents}, now, Thresholds{})
	th := findThread(t, coord, "gate/dogfood")
	if th.Triage == TriageNeedsYou {
		t.Fatalf("triage = needs-you after the operator answered; a negative answer must still hand the ball back to the agent")
	}

	headline, _ := attachAttention(agents, coord.Threads)
	if headline.State == TriageNeedsYou {
		t.Fatalf("session headline = needs-you after the operator answered")
	}
}

func TestOperatorGate_UnansweredBlockingAskStaysNeedsYou(t *testing.T) {
	now := time.Date(2026, 6, 11, 14, 30, 0, 0, time.UTC)
	msgs := []Message{dogfoodGateAsk(now)}
	agents := []Agent{opAgent("copilot")}

	coord := buildCoordination(collapseInput{messages: msgs, agents: agents}, now, Thresholds{})
	th := findThread(t, coord, "gate/dogfood")
	if th.Triage != TriageNeedsYou {
		t.Fatalf("triage = %q, want needs-you while the blocking ask is unanswered", th.Triage)
	}
	if th.NeedsYouOwner != "copilot" {
		t.Fatalf("owner = %q, want copilot", th.NeedsYouOwner)
	}
}
