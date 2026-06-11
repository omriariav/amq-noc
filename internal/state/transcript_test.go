package state

import (
	"path/filepath"
	"testing"
	"time"
)

// ConversationTranscript is conversation mode's data source (amq-noc#27):
// participant-filtered (operator<->lead wherever the messages live, p2p and
// gate threads alike), full bodies, oldest first, deduplicated, capped to the
// newest limit.

func transcriptRoot(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func agentDirFor(root, handle string) string {
	return filepath.Join(root, "agents", handle)
}

func TestConversationTranscriptParticipantFiltered(t *testing.T) {
	root := transcriptRoot(t)
	base := time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC)

	// Operator -> lead directive (lands in the lead's inbox).
	seedMessage(t, agentDirFor(root, "copilot"), "new", msgSpec{
		id: "m1", from: "user", to: []string{"copilot"}, thread: "p2p/copilot__user",
		subject: "DIRECTIVE: ship the sweep", kind: "todo", body: "Ship the sweep per spec.",
		createdAt: base,
	})
	// Lead ack (lands in the operator's mailbox).
	seedMessage(t, agentDirFor(root, "user"), "cur", msgSpec{
		id: "m2", from: "copilot", to: []string{"user"}, thread: "p2p/copilot__user",
		subject: "ack: sweep underway", kind: "status", body: "On it; analyst dispatched.",
		createdAt: base.Add(5 * time.Minute),
	})
	// Lead-raised gate ask (different thread, still operator<->lead).
	seedMessage(t, agentDirFor(root, "user"), "new", msgSpec{
		id: "m3", from: "copilot", to: []string{"user"}, thread: "gate/dm-coverage",
		subject: "APPROVAL: hourly DM sweep?", kind: "question", body: "Option 1 or 2?",
		createdAt: base.Add(10 * time.Minute),
	})
	// Child -> lead traffic: NOT part of the operator conversation.
	seedMessage(t, agentDirFor(root, "copilot"), "cur", msgSpec{
		id: "m4", from: "analyst", to: []string{"copilot"}, thread: "p2p/analyst__copilot",
		subject: "status: done", kind: "status", body: "Sweep ran.",
		createdAt: base.Add(15 * time.Minute),
	})
	// Operator -> child traffic: also excluded (lead-only conversation).
	seedMessage(t, agentDirFor(root, "analyst"), "new", msgSpec{
		id: "m5", from: "user", to: []string{"analyst"}, thread: "p2p/analyst__user",
		subject: "direct poke", body: "ping",
		createdAt: base.Add(20 * time.Minute),
	})

	got := ConversationTranscript(root, "copilot", "user", 0)
	if len(got) != 3 {
		t.Fatalf("transcript = %d messages, want 3 (m1,m2,m3)", len(got))
	}
	wantOrder := []string{"m1", "m2", "m3"}
	for i, id := range wantOrder {
		if got[i].ID != id {
			t.Fatalf("order[%d] = %s, want %s", i, got[i].ID, id)
		}
	}
	if got[0].Body != "Ship the sweep per spec." {
		t.Fatalf("transcript must carry full bodies, got %q", got[0].Body)
	}
	if got[2].Thread != "gate/dm-coverage" {
		t.Fatalf("gate asks belong to the conversation, got thread %q", got[2].Thread)
	}
}

func TestConversationTranscriptDeduplicatesCopies(t *testing.T) {
	root := transcriptRoot(t)
	base := time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC)
	// The same message id present in both mailboxes (sender copy + recipient
	// copy) must appear once.
	for _, h := range []string{"copilot", "user"} {
		seedMessage(t, agentDirFor(root, h), "cur", msgSpec{
			id: "dup", from: "user", to: []string{"copilot"}, thread: "p2p/copilot__user",
			subject: "DIRECTIVE: x", body: "x", createdAt: base,
		})
	}
	got := ConversationTranscript(root, "copilot", "user", 0)
	if len(got) != 1 {
		t.Fatalf("transcript = %d messages, want 1 after dedup", len(got))
	}
}

func TestConversationTranscriptCapsToNewest(t *testing.T) {
	root := transcriptRoot(t)
	base := time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 6; i++ {
		seedMessage(t, agentDirFor(root, "copilot"), "cur", msgSpec{
			id: "m" + itoa(i), from: "user", to: []string{"copilot"}, thread: "p2p/copilot__user",
			subject: "s", body: "b", createdAt: base.Add(time.Duration(i) * time.Minute),
		})
	}
	got := ConversationTranscript(root, "copilot", "user", 2)
	if len(got) != 2 {
		t.Fatalf("transcript = %d, want capped 2", len(got))
	}
	if got[0].ID != "m4" || got[1].ID != "m5" {
		t.Fatalf("cap must keep the NEWEST messages, got %s,%s", got[0].ID, got[1].ID)
	}
}

func TestConversationTranscriptEmptyInputs(t *testing.T) {
	if got := ConversationTranscript("", "a", "b", 0); got != nil {
		t.Fatal("empty root must return nil")
	}
	if got := ConversationTranscript(transcriptRoot(t), "a", "a", 0); got != nil {
		t.Fatal("same-handle conversation must return nil")
	}
}
