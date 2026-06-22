package state

import (
	"testing"
	"time"
)

func TestAttentionQueue_GateAnsweredStatus(t *testing.T) {
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	msgs := []Message{
		{ID: "g1", From: "cto", To: []string{"user"}, Thread: "gate/release",
			Subject: "APPROVAL: release?", Kind: KindQuestion, Created: now.Add(-5 * time.Minute), Owner: "user", State: MailboxCur,
			Body: "Approve release?"},
		{ID: "g2", From: "user", To: []string{"cto"}, Thread: "gate/release",
			Subject: "APPROVED: release", Kind: KindAnswer, Created: now.Add(-1 * time.Minute), Owner: "cto", State: MailboxNew,
			Body: "Approved."},
	}
	coord := buildCoordination(collapseInput{messages: msgs, agents: []Agent{opAgent("cto")}}, now, Thresholds{})
	th := findThread(t, coord, "gate/release")
	if !th.GateAnswered || th.Gate {
		t.Fatalf("gate answered/open flags = answered:%v open:%v", th.GateAnswered, th.Gate)
	}
	for _, item := range coord.AttentionQueue {
		if item.Kind == AttentionNeedsYouGate {
			t.Fatalf("answered gate should not remain needs-you; queue=%+v", coord.AttentionQueue)
		}
	}
}

func TestAttentionQueue_DirectiveAckDetection(t *testing.T) {
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	msgs := []Message{
		{ID: "d1", From: "user", To: []string{"cto"}, Thread: "p2p/cto__user",
			Subject: "DIRECTIVE: resume work", Kind: KindTodo, Created: now.Add(-10 * time.Minute), Owner: "cto", State: MailboxCur,
			Body: "Resume work."},
		{ID: "d2", From: "cto", To: []string{"user"}, Thread: "p2p/cto__user",
			Subject: "ACK: resuming", Kind: KindStatus, Created: now.Add(-1 * time.Minute), Owner: "user", State: MailboxNew,
			Body: "Resuming now."},
	}
	coord := buildCoordination(collapseInput{messages: msgs, agents: []Agent{opAgent("cto")}}, now, Thresholds{})
	th := findThread(t, coord, "p2p/cto__user")
	if !th.Directive || !th.DirectiveAcked {
		t.Fatalf("directive flags = directive:%v acked:%v", th.Directive, th.DirectiveAcked)
	}
	for _, item := range coord.AttentionQueue {
		if item.Kind == AttentionStaleDirective {
			t.Fatalf("acked directive should not be promoted as stale: %+v", coord.AttentionQueue)
		}
	}
}

func TestAttentionQueue_DirectiveGateConflict(t *testing.T) {
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	msgs := []Message{
		{ID: "d1", From: "user", To: []string{"cto"}, Thread: "p2p/cto__user",
			Subject: "DIRECTIVE: ship now", Kind: KindTodo, Created: now.Add(-10 * time.Minute), Owner: "cto", State: MailboxNew,
			Body: "Ship now."},
		{ID: "g1", From: "cto", To: []string{"user"}, Thread: "gate/release",
			Subject: "APPROVAL: release gate", Kind: KindQuestion, Created: now.Add(-5 * time.Minute), Owner: "user", State: MailboxNew,
			Body: "Need release approval."},
	}
	coord := buildCoordination(collapseInput{messages: msgs, agents: []Agent{opAgent("cto")}}, now, Thresholds{})
	var directive AttentionQueueItem
	for _, item := range coord.AttentionQueue {
		if item.Kind == AttentionStaleDirective {
			directive = item
		}
	}
	if directive.Kind == "" {
		t.Fatalf("unacked directive should be promoted: %+v", coord.AttentionQueue)
	}
	if !directive.Conflict || directive.Why != "directive overlaps open operator gate" {
		t.Fatalf("directive conflict not surfaced: %+v", directive)
	}
}
