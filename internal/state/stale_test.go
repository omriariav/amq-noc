package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-noc/internal/launch"
)

// staleProbe is a deterministic probe pinned to `now` with no live PIDs.
func staleProbe(now time.Time) Probe {
	return Probe{
		PIDAlive:     func(int) bool { return false },
		ProcessMatch: func(int, func(string) bool) bool { return false },
		Now:          func() time.Time { return now },
	}
}

// writeStaleAgent writes a launch.json under
// <projectDir>/.agent-mail/<session>/agents/<handle>/.
func writeStaleAgent(t *testing.T, projectDir, session, handle string) string {
	t.Helper()
	agentDir := filepath.Join(projectDir, ".agent-mail", session, "agents", handle)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir agent dir: %v", err)
	}
	if err := launch.Write(agentDir, launch.Record{Binary: "codex", Session: session, Handle: handle}); err != nil {
		t.Fatalf("write launch: %v", err)
	}
	return agentDir
}

// writeBlockMsg drops an agent->agent status declaring a block into an inbox.
func writeBlockMsg(t *testing.T, agentDir, id, from, to string, created time.Time) {
	t.Helper()
	inbox := filepath.Join(agentDir, "inbox", "new")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatalf("mkdir inbox: %v", err)
	}
	msg := "---json\n" +
		`{"schema":1,"id":"` + id + `","thread":"build/` + id + `","from":"` + from + `","to":["` + to + `"],` +
		`"kind":"status","subject":"stuck",` +
		`"created":"` + created.UTC().Format(time.RFC3339Nano) + `"}` + "\n" +
		"---\n" +
		"I am blocked on upstream.\n"
	if err := os.WriteFile(filepath.Join(inbox, id+".md"), []byte(msg), 0o600); err != nil {
		t.Fatalf("write block msg: %v", err)
	}
}

// TestStaleness_BlockedThreadDecaysPastWindow asserts that a blocked thread older
// than StaleAfter is marked Stale and routed to the rollup's STALE bucket, while
// a recent blocked thread stays LIVE.
func TestStaleness_BlockedThreadDecaysPastWindow(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()

	dir := writeStaleAgent(t, root, "main", "dev")
	// recent block: 1h old (live). old block: 30 days old (stale).
	writeBlockMsg(t, dir, "recent", "qa", "dev", now.Add(-1*time.Hour))
	writeBlockMsg(t, dir, "ancient", "qa", "dev", now.Add(-30*24*time.Hour))

	snap, err := BuildWithThresholds(root, filepath.Join(root, ".agent-mail"), staleProbe(now), Thresholds{StaleAfter: 72 * time.Hour})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(snap.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(snap.Sessions))
	}

	var recentStale, ancientStale bool
	var sawRecent, sawAncient bool
	for _, th := range snap.Sessions[0].Coordination.Threads {
		switch th.ID {
		case "build/recent":
			sawRecent = true
			recentStale = th.Stale
		case "build/ancient":
			sawAncient = true
			ancientStale = th.Stale
		}
	}
	if !sawRecent || !sawAncient {
		t.Fatalf("expected both threads; recent=%v ancient=%v", sawRecent, sawAncient)
	}
	if recentStale {
		t.Errorf("recent (1h) blocked thread must NOT be stale")
	}
	if !ancientStale {
		t.Errorf("ancient (30d) blocked thread MUST be stale")
	}

	// Rollup: one LIVE blocked (recent), one STALE blocked (ancient).
	r := snap.Rollup
	if r.Blocked != 1 {
		t.Errorf("expected 1 LIVE blocked, got %d (rollup=%+v)", r.Blocked, r)
	}
	if r.BlockedStale != 1 {
		t.Errorf("expected 1 STALE blocked, got %d (rollup=%+v)", r.BlockedStale, r)
	}
	if r.HasLiveAttention() != true {
		t.Errorf("a live blocked thread should count as live attention")
	}
}

// TestStaleness_NeedsYouUsesHistoryNotStale asserts a needs-you thread is not
// marked stale, even when ancient, but inactive-agent asks count as historical
// rather than live operator action.
func TestStaleness_NeedsYouUsesHistoryNotStale(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	dir := writeStaleAgent(t, root, "main", "dev")

	inbox := filepath.Join(dir, "inbox", "new")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatalf("mkdir inbox: %v", err)
	}
	// A 30-day-old question addressed to the operator remains a needs-you thread,
	// but the inactive agent means it is only history in the rollup.
	msg := "---json\n" +
		`{"schema":1,"id":"q","thread":"decision/ship","from":"dev","to":["user"],` +
		`"kind":"question","subject":"ship?",` +
		`"created":"` + now.Add(-30*24*time.Hour).UTC().Format(time.RFC3339Nano) + `"}` + "\n" +
		"---\nShip?\n"
	if err := os.WriteFile(filepath.Join(inbox, "q.md"), []byte(msg), 0o600); err != nil {
		t.Fatalf("write q: %v", err)
	}

	snap, err := BuildWithThresholds(root, filepath.Join(root, ".agent-mail"), staleProbe(now), Thresholds{StaleAfter: 72 * time.Hour})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for _, th := range snap.Sessions[0].Coordination.Threads {
		if th.Triage == TriageNeedsYou && th.Stale {
			t.Errorf("needs-you history must not be marked stale")
		}
	}
	if snap.Rollup.NeedsYou != 0 || snap.Rollup.NeedsYouHistorical != 1 {
		t.Errorf("rollup = %+v, want one historical needs-you", snap.Rollup)
	}
}

// TestStaleness_DefaultWindowApplied asserts the default stale window (72h) is
// used when Thresholds.StaleAfter is zero.
func TestStaleness_DefaultWindowApplied(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	dir := writeStaleAgent(t, root, "main", "dev")
	writeBlockMsg(t, dir, "ancient", "qa", "dev", now.Add(-10*24*time.Hour)) // 10d > 72h

	snap, err := BuildWithThresholds(root, filepath.Join(root, ".agent-mail"), staleProbe(now), Thresholds{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if snap.Rollup.BlockedStale != 1 {
		t.Errorf("default 72h window should mark a 10-day block stale; rollup=%+v", snap.Rollup)
	}
}
