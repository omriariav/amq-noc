package state

import (
	"testing"
	"time"

	"github.com/omriariav/amq-noc/internal/launch"
)

// TestHeartbeatQuietSkipsLiveAgent is the QA-9 regression: an agent that is
// computed-ALIVE (its PID verifies live) must NOT make its threads at-risk just
// because its presence.json heartbeat FILE is stale. Before the fix, a live
// squad whose agents had a ~hours-old presence (they don't rewrite presence on
// every turn) flipped EVERY awaiting-reply thread to at-risk, inflating the
// count (observed: 63 at-risk on a 4-agent squad).
//
// Fixture: cto + senior-dev are both PID-alive (probe) but their presence is far
// older than the 90s Heartbeat threshold, and the thread between them is a FRESH
// review_request (under ReviewAge) so the ONLY thing that could mark it at-risk
// is heartbeatQuiet. With the fix it must be Clear.
func TestHeartbeatQuietSkipsLiveAgent(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	ctoDir := seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})
	sdDir := seedAgent(t, base, "s", "senior-dev", launch.Record{Binary: "codex", Handle: "senior-dev", Session: "s", AgentPID: 2})

	// Stale presence (2h old) for both — well past the 90s Heartbeat threshold —
	// yet the probe marks both PIDs alive, so computed Liveness is alive.
	stale := coordNow.Add(-2 * time.Hour)
	seedPresence(t, ctoDir, "cto", "offline", stale)
	seedPresence(t, sdDir, "senior-dev", "offline", stale)

	// A FRESH (5m, under the 45m ReviewAge) awaiting-reply review_request, so the
	// aging-review at-risk path does NOT fire; heartbeatQuiet is the only lever.
	seedMessage(t, ctoDir, "new", msgSpec{
		id: "fresh", from: "senior-dev", to: []string{"cto"},
		thread: "p2p/cto__senior-dev", subject: "review PR", kind: "review_request",
		createdAt: coordNow.Add(-5 * time.Minute),
	})

	snap, err := Build(proj, base, coordProbe()) // coordProbe forces PIDs alive
	if err != nil {
		t.Fatal(err)
	}

	// Precondition: both agents must be computed-alive despite the stale presence.
	if cto := findAgent(t, snap, "cto"); cto.Liveness != LivenessAlive {
		t.Fatalf("precondition: cto liveness = %q, want alive", cto.Liveness)
	}

	th := findThread(t, snap.Sessions[0].Coordination, "p2p/cto__senior-dev")
	if th.Triage == TriageAtRisk {
		t.Fatalf("a fresh thread among ALIVE agents must NOT be at-risk from stale "+
			"presence; got %q (heartbeatQuiet must skip live agents)", th.Triage)
	}
}

// TestHeartbeatQuietFiresForNonAliveParticipant guards the other side: a NOT-
// alive participant (dead PID) with stale presence on an awaiting-reply thread
// SHOULD still be at-risk via heartbeatQuiet — the fix only exempts alive agents,
// it must not silence genuinely-quiet dead ones.
func TestHeartbeatQuietFiresForNonAliveParticipant(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	ctoDir := seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})
	sdDir := seedAgent(t, base, "s", "senior-dev", launch.Record{Binary: "codex", Handle: "senior-dev", Session: "s", AgentPID: 2})

	// senior-dev presence stale; its PID will be marked DEAD by the probe below,
	// so it is NOT alive and heartbeatQuiet should fire for it.
	seedPresence(t, sdDir, "senior-dev", "offline", coordNow.Add(-2*time.Hour))

	probe := Probe{
		PIDAlive:     func(p int) bool { return p != 2 }, // senior-dev (pid 2) dead
		ProcessMatch: func(int, func(string) bool) bool { return true },
		Now:          func() time.Time { return coordNow },
	}

	seedMessage(t, ctoDir, "new", msgSpec{
		id: "fresh", from: "senior-dev", to: []string{"cto"},
		thread: "p2p/cto__senior-dev", subject: "review PR", kind: "review_request",
		createdAt: coordNow.Add(-5 * time.Minute),
	})

	snap, err := Build(proj, base, probe)
	if err != nil {
		t.Fatal(err)
	}
	if sd := findAgent(t, snap, "senior-dev"); sd.Liveness == LivenessAlive {
		t.Fatalf("precondition: senior-dev must NOT be alive, got %q", sd.Liveness)
	}
	th := findThread(t, snap.Sessions[0].Coordination, "p2p/cto__senior-dev")
	if th.Triage != TriageAtRisk {
		t.Fatalf("a quiet NOT-alive participant on an awaiting-reply thread should be "+
			"at-risk; got %q", th.Triage)
	}
}
