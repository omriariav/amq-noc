package state

import (
	"testing"
	"time"
)

// Anchors the shared primary blocked-vs-waiting gate extracted for #20 (the
// JSON snapshot and the TUI previously carried byte-identical private copies).

func hardStopThread(at time.Time, participants ...string) ThreadSummary {
	return ThreadSummary{
		Triage:       TriageBlocked,
		Subject:      "BLOCKER: broken environment",
		LatestBody:   "NO-GO: cannot proceed",
		LastEventAt:  at,
		Participants: participants,
	}
}

func waitingThread(at time.Time, participants ...string) ThreadSummary {
	return ThreadSummary{
		Triage:       TriageAtRisk,
		Subject:      "awaiting QA",
		LastEventAt:  at,
		Participants: participants,
	}
}

func TestSessionHasHardStopCurrent(t *testing.T) {
	base := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	sess := Session{Coordination: Coordination{Threads: []ThreadSummary{
		hardStopThread(base, "cto", "fullstack"),
	}}}
	if !SessionHasHardStop(sess) {
		t.Fatal("a current hard-stop thread with no newer wait must report a hard stop")
	}
}

func TestSessionHasHardStopSupersededByOverlappingWait(t *testing.T) {
	base := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	sess := Session{Coordination: Coordination{Threads: []ThreadSummary{
		hardStopThread(base, "cto", "fullstack"),
		waitingThread(base.Add(time.Hour), "fullstack", "qa"),
	}}}
	if SessionHasHardStop(sess) {
		t.Fatal("a newer waiting thread sharing a participant must supersede the hard stop on primary surfaces")
	}
}

func TestSessionHasHardStopNotSupersededByDisjointWait(t *testing.T) {
	base := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	sess := Session{Coordination: Coordination{Threads: []ThreadSummary{
		hardStopThread(base, "cto", "fullstack"),
		waitingThread(base.Add(time.Hour), "qa", "scribe"),
	}}}
	if !SessionHasHardStop(sess) {
		t.Fatal("a newer wait with no shared participant must not supersede the hard stop")
	}
}
