package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-noc/internal/launch"
)

// fixedNow returns a deterministic clock anchored at a known instant so
// presence-freshness math is stable and the sandbox never touches a real
// wall-clock. All seeded last_seen values are expressed relative to this.
var testNow = time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)

// fakeProbe builds a Probe whose PID liveness and process-match decisions come
// from explicit maps, and whose clock is fixed. It also records every PIDAlive
// call so a test can assert the probe (not a real subprocess) was the only
// liveness source. Mirrors the internal/cli downFakeProbe pattern.
func fakeProbe(alive, match map[int]bool, calls *[]int) Probe {
	return Probe{
		PIDAlive: func(pid int) bool {
			if calls != nil {
				*calls = append(*calls, pid)
			}
			return alive[pid]
		},
		ProcessMatch: func(pid int, _ func(args string) bool) bool {
			return match[pid]
		},
		Now: func() time.Time { return testNow },
	}
}

// seedAgent writes a launch.json under the AMQ session/agents/<handle> layout
// and returns the agent dir. Mirrors internal/cli.seedAgentRecord.
func seedAgent(t *testing.T, base, session, handle string, rec launch.Record) string {
	t.Helper()
	agentDir := filepath.Join(base, session, "agents", handle)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	return agentDir
}

func seedPresence(t *testing.T, agentDir, handle, status string, lastSeen time.Time) {
	t.Helper()
	pres := presenceFile{Schema: 1, Handle: handle, Status: status, LastSeen: lastSeen}
	writeJSON(t, presencePath(agentDir), pres)
}

func seedWakeLock(t *testing.T, agentDir string, pid int, root string) {
	t.Helper()
	lock := wakeLockFile{PID: pid, Root: root, Started: testNow.Add(-time.Hour)}
	writeJSON(t, wakeLockPath(agentDir), lock)
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// findAgent locates an agent by handle across all sessions in a snapshot.
func findAgent(t *testing.T, snap Snapshot, handle string) Agent {
	t.Helper()
	for _, s := range snap.Sessions {
		for _, a := range s.Agents {
			if a.Handle == handle {
				return a
			}
		}
	}
	t.Fatalf("agent %q not found in snapshot %+v", handle, snap)
	return Agent{}
}

func TestBuildDiscoversSessionsAndAgents(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()

	seedAgent(t, base, "issue-96", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Role: "cto", Session: "issue-96", AgentPID: 1111, TeamProfile: "review",
	})
	seedAgent(t, base, "issue-96", "fullstack", launch.Record{
		Binary: "claude", Handle: "fullstack", Role: "fullstack", Session: "issue-96", AgentPID: 2222,
	})
	seedAgent(t, base, "drive-fix", "qa", launch.Record{
		Binary: "claude", Handle: "qa", Role: "qa", Session: "drive-fix", AgentPID: 3333,
	})

	probe := fakeProbe(
		map[int]bool{1111: true, 2222: true, 3333: true},
		map[int]bool{1111: true, 2222: true, 3333: true},
		nil,
	)
	snap, err := Build(proj, base, probe)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if len(snap.Sessions) != 2 {
		t.Fatalf("want 2 sessions, got %d: %+v", len(snap.Sessions), snap.Sessions)
	}
	// Sessions are sorted by name: drive-fix before issue-96.
	if snap.Sessions[0].Name != "drive-fix" || snap.Sessions[1].Name != "issue-96" {
		t.Fatalf("session order wrong: %q, %q", snap.Sessions[0].Name, snap.Sessions[1].Name)
	}
	if got := len(snap.Sessions[1].Agents); got != 2 {
		t.Fatalf("issue-96 want 2 agents, got %d", got)
	}
	if got := findAgent(t, snap, "cto").TeamProfile; got != "review" {
		t.Fatalf("TeamProfile = %q, want review", got)
	}
	// Agents sorted by role: cto before fullstack.
	if snap.Sessions[1].Agents[0].Handle != "cto" || snap.Sessions[1].Agents[1].Handle != "fullstack" {
		t.Fatalf("agent order wrong: %+v", snap.Sessions[1].Agents)
	}
	if snap.BaseRoot != base {
		t.Fatalf("BaseRoot = %q, want %q", snap.BaseRoot, base)
	}
	cto := findAgent(t, snap, "cto")
	if cto.Engine != "codex" {
		t.Fatalf("cto.Engine = %q, want codex", cto.Engine)
	}
	if cto.AgentDir == "" {
		t.Fatal("cto.AgentDir empty")
	}
}

func TestBuildNormalizesRelativeLaunchRoot(t *testing.T) {
	proj := t.TempDir()
	base := filepath.Join(proj, ".agent-mail")
	seedAgent(t, base, "live-1", "cto", launch.Record{
		CWD:     proj,
		Binary:  "sleep",
		Handle:  "cto",
		Session: "live-1",
		Root:    ".agent-mail/live-1",
	})

	snap, err := Build(proj, base, fakeProbe(nil, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(snap.Sessions))
	}
	want := filepath.Join(proj, ".agent-mail", "live-1")
	if got := snap.Sessions[0].Root; got != want {
		t.Fatalf("session root = %q, want %q", got, want)
	}
}

func TestClassifyAlive(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	seedAgent(t, base, "s", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1234,
	})
	probe := fakeProbe(map[int]bool{1234: true}, map[int]bool{1234: true}, nil)

	snap, err := Build(proj, base, probe)
	if err != nil {
		t.Fatal(err)
	}
	a := findAgent(t, snap, "cto")
	if a.Liveness != LivenessAlive {
		t.Fatalf("Liveness = %q, want alive", a.Liveness)
	}
}

func TestClassifyStaleWhenRecordedPIDDeadAndNoPresence(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	seedAgent(t, base, "s", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1234,
	})
	// PID dead, no presence file at all.
	probe := fakeProbe(map[int]bool{1234: false}, map[int]bool{1234: false}, nil)

	snap, err := Build(proj, base, probe)
	if err != nil {
		t.Fatal(err)
	}
	a := findAgent(t, snap, "cto")
	// A recorded-but-dead agent PID with no fresh mailbox is a stale leftover.
	if a.Liveness != LivenessStale {
		t.Fatalf("Liveness = %q, want stale (dead recorded PID, no fresh mailbox)", a.Liveness)
	}
}

func TestClassifyStalePresenceLies(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	agentDir := seedAgent(t, base, "s", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1234,
	})
	// presence.status="active" but last_seen is 26 days old — the classic lie.
	seedPresence(t, agentDir, "cto", "active", testNow.Add(-26*24*time.Hour))
	probe := fakeProbe(map[int]bool{1234: false}, map[int]bool{1234: false}, nil)

	snap, err := Build(proj, base, probe)
	if err != nil {
		t.Fatal(err)
	}
	a := findAgent(t, snap, "cto")
	if a.Liveness == LivenessAlive {
		t.Fatalf("Liveness = alive, but a 26-day-dead 'active' presence must not read alive")
	}
	if a.Liveness != LivenessStale {
		t.Fatalf("Liveness = %q, want stale", a.Liveness)
	}
	if a.Presence != "active" {
		t.Fatalf("raw Presence should still be reported as %q, got %q", "active", a.Presence)
	}
}

func TestClassifyDeadMailboxLive(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	agentDir := seedAgent(t, base, "s", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1234,
	})
	// The agent PID is DEAD, but the mailbox presence was touched just now and
	// reads "active". This is the dead-process / live-mailbox case.
	seedPresence(t, agentDir, "cto", "active", testNow.Add(-10*time.Second))
	probe := fakeProbe(map[int]bool{1234: false}, map[int]bool{1234: false}, nil)

	snap, err := Build(proj, base, probe)
	if err != nil {
		t.Fatal(err)
	}
	a := findAgent(t, snap, "cto")
	if a.Liveness != LivenessDeadMailboxLive {
		t.Fatalf("Liveness = %q, want dead-mailbox-live", a.Liveness)
	}
	if a.Liveness == LivenessStale {
		t.Fatal("dead-mailbox-live must not collapse into stale")
	}
}

func TestClassifyDeadMailboxLiveNonActiveStatus(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	agentDir := seedAgent(t, base, "s", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1234,
	})
	// Mailbox touched recently but status is "offline": still a live mailbox
	// behind a dead process — the inverse lie (running-looking writes, status
	// says offline). Must surface as dead-mailbox-live, not dead.
	seedPresence(t, agentDir, "cto", "offline", testNow.Add(-5*time.Second))
	probe := fakeProbe(map[int]bool{1234: false}, map[int]bool{1234: false}, nil)

	snap, err := Build(proj, base, probe)
	if err != nil {
		t.Fatal(err)
	}
	a := findAgent(t, snap, "cto")
	if a.Liveness != LivenessDeadMailboxLive {
		t.Fatalf("Liveness = %q, want dead-mailbox-live (fresh mailbox, dead pid)", a.Liveness)
	}
}

func TestClassifyAlivePresenceOnlyNoPID(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	// No agent PID recorded (e.g. a codex seat). Fresh active presence should
	// read alive, mirroring status.go's presence-live-without-verified-pid path.
	agentDir := seedAgent(t, base, "s", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Session: "s",
	})
	seedPresence(t, agentDir, "cto", "active", testNow.Add(-3*time.Second))
	probe := fakeProbe(nil, nil, nil)

	snap, err := Build(proj, base, probe)
	if err != nil {
		t.Fatal(err)
	}
	a := findAgent(t, snap, "cto")
	if a.Liveness != LivenessAlive {
		t.Fatalf("Liveness = %q, want alive (fresh active presence, no recorded pid)", a.Liveness)
	}
}

func TestClassifyWakeLiveFromVerifiedWake(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	agentDir := seedAgent(t, base, "s", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Session: "s", Root: filepath.Join(base, "s"),
	})
	seedWakeLock(t, agentDir, 9001, filepath.Join(base, "s"))

	snap, err := Build(proj, base, fakeProbe(map[int]bool{9001: true}, map[int]bool{9001: true}, nil))
	if err != nil {
		t.Fatal(err)
	}
	a := findAgent(t, snap, "cto")
	if a.Liveness != LivenessWakeLive {
		t.Fatalf("Liveness = %q, want wake-live", a.Liveness)
	}
	if a.WakeHealth != WakeHealth("pid:9001") {
		t.Fatalf("WakeHealth = %q, want pid:9001", a.WakeHealth)
	}
}

func TestWakeProcessMatcherAcceptsSymlinkedAbsoluteRoot(t *testing.T) {
	realBase := t.TempDir()
	linkBase := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(realBase, linkBase); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	expected := filepath.Join(realBase, ".agent-mail", "issue-96")
	actual := filepath.Join(linkBase, ".agent-mail", "issue-96")
	if err := os.MkdirAll(expected, 0o755); err != nil {
		t.Fatal(err)
	}
	args := "amq wake --me cto --root " + actual
	if !wakeProcessMatcher("cto", expected)(args) {
		t.Fatalf("wake matcher should accept symlink-equivalent root: args=%q expected=%q", args, expected)
	}
}

func TestWakeHealthLabels(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()

	// alive agent with a live wake at a different PID -> pid:<wakepid>
	dirA := seedAgent(t, base, "s", "live", launch.Record{
		Binary: "codex", Handle: "live", Session: "s", AgentPID: 1000, Root: filepath.Join(base, "s"),
	})
	seedWakeLock(t, dirA, 5000, filepath.Join(base, "s"))

	// alive agent with NO wake lock -> missing
	seedAgent(t, base, "s", "nowake", launch.Record{
		Binary: "codex", Handle: "nowake", Session: "s", AgentPID: 1001,
	})

	// alive agent with a wake lock whose PID is dead -> stale
	dirC := seedAgent(t, base, "s", "deadwake", launch.Record{
		Binary: "codex", Handle: "deadwake", Session: "s", AgentPID: 1002, Root: filepath.Join(base, "s"),
	})
	seedWakeLock(t, dirC, 6000, filepath.Join(base, "s"))

	// dead agent -> wake health not investigated (none)
	seedAgent(t, base, "s", "dead", launch.Record{
		Binary: "codex", Handle: "dead", Session: "s", AgentPID: 1003,
	})

	probe := fakeProbe(
		map[int]bool{1000: true, 1001: true, 1002: true, 1003: false, 5000: true, 6000: false},
		map[int]bool{1000: true, 1001: true, 1002: true, 5000: true},
		nil,
	)
	snap, err := Build(proj, base, probe)
	if err != nil {
		t.Fatal(err)
	}

	if got := findAgent(t, snap, "live").WakeHealth; got != WakeHealth("pid:5000") {
		t.Fatalf("live WakeHealth = %q, want pid:5000", got)
	}
	if got := findAgent(t, snap, "nowake").WakeHealth; got != WakeHealthMissing {
		t.Fatalf("nowake WakeHealth = %q, want missing", got)
	}
	if got := findAgent(t, snap, "deadwake").WakeHealth; got != WakeHealthStale {
		t.Fatalf("deadwake WakeHealth = %q, want stale", got)
	}
	if got := findAgent(t, snap, "dead").WakeHealth; got != WakeHealthNone {
		t.Fatalf("dead WakeHealth = %q, want none", got)
	}
	// Wake PID is the wake pid, NOT the agent pid.
	if wp := findAgent(t, snap, "live").WakePID; wp != 5000 {
		t.Fatalf("live WakePID = %d, want 5000 (wake pid, not agent pid)", wp)
	}
}

// TestBuildNeverExecsAMQ asserts the snapshot path performs no `amq` (or any)
// subprocess: liveness flows ONLY through the injected probe. We prove this two
// ways: (1) the probe's PIDAlive is the recorded liveness source, and (2) PATH
// is emptied so any accidental exec.Command("amq"/"ps") would fail loudly — yet
// Build still classifies correctly.
func TestBuildNeverExecsAMQ(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	agentDir := seedAgent(t, base, "s", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1234, Root: filepath.Join(base, "s"),
	})
	seedPresence(t, agentDir, "cto", "active", testNow.Add(-1*time.Second))
	seedWakeLock(t, agentDir, 7777, filepath.Join(base, "s"))

	// Break PATH so any exec of amq/ps would error. The fake probe must be the
	// only liveness source.
	t.Setenv("PATH", "")

	var calls []int
	probe := fakeProbe(
		map[int]bool{1234: true, 7777: true},
		map[int]bool{1234: true, 7777: true},
		&calls,
	)
	snap, err := Build(proj, base, probe)
	if err != nil {
		t.Fatalf("Build with empty PATH must still work via the injected probe: %v", err)
	}
	a := findAgent(t, snap, "cto")
	if a.Liveness != LivenessAlive {
		t.Fatalf("Liveness = %q, want alive", a.Liveness)
	}
	if len(calls) == 0 {
		t.Fatal("probe.PIDAlive was never called; liveness came from somewhere else")
	}
	// The agent PID must have been probed.
	found := false
	for _, c := range calls {
		if c == 1234 {
			found = true
		}
	}
	if !found {
		t.Fatalf("agent PID 1234 was never probed; calls=%v", calls)
	}
}

func TestProbeDefaultsFilledWhenNil(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	seedAgent(t, base, "s", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Session: "s",
	})
	// Pass only a Now seam; PIDAlive/ProcessMatch must be defaulted, not panic.
	probe := Probe{Now: func() time.Time { return testNow }}
	if _, err := Build(proj, base, probe); err != nil {
		t.Fatalf("Build with partial probe should not error: %v", err)
	}
}

func TestClassifyAliveWhenPIDAliveAndPresenceFreshEvenIfProcessMatchFails(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	agentDir := seedAgent(t, base, "s", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1234,
	})
	seedPresence(t, agentDir, "cto", "active", testNow.Add(-10*time.Second))

	snap, err := Build(proj, base, fakeProbe(map[int]bool{1234: true}, map[int]bool{1234: false}, nil))
	if err != nil {
		t.Fatal(err)
	}
	a := findAgent(t, snap, "cto")
	if a.Liveness != LivenessAlive {
		t.Fatalf("Liveness = %q, want alive (PID exists and fresh active presence proves the agent is live)", a.Liveness)
	}
}

func TestClassifyMissingWhenNoSignals(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	// launch.json exists (so the entry is discovered) but carries no PID, and
	// there is no presence file and no wake lock: no live signals at all.
	seedAgent(t, base, "s", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Session: "s",
	})
	snap, err := Build(proj, base, fakeProbe(nil, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	a := findAgent(t, snap, "cto")
	if a.Liveness != LivenessMissing {
		t.Fatalf("Liveness = %q, want missing (no pid, no presence, no wake)", a.Liveness)
	}
}

func TestClassifyDeadWhenOnlyStalePresenceNoPID(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	// No recorded agent PID, no wake. Only an old presence file. There is a
	// disk signal (presence) but no live-leaning signal, so it collapses to
	// dead rather than stale.
	agentDir := seedAgent(t, base, "s", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Session: "s",
	})
	seedPresence(t, agentDir, "cto", "active", testNow.Add(-26*24*time.Hour))
	snap, err := Build(proj, base, fakeProbe(nil, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	a := findAgent(t, snap, "cto")
	if a.Liveness != LivenessDead {
		t.Fatalf("Liveness = %q, want dead (only an old presence, no live-leaning signal)", a.Liveness)
	}
}

func TestEmptyBaseRootYieldsNoSessions(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	snap, err := Build(proj, base, fakeProbe(nil, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Sessions) != 0 {
		t.Fatalf("want 0 sessions for empty base root, got %d", len(snap.Sessions))
	}
}
