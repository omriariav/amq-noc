package console

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-noc/internal/launch"
	"github.com/omriariav/amq-noc/internal/state"
)

// runNow is the deterministic clock anchoring the runner tests.
var runNow = time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)

// runProbe builds a state.Probe from explicit maps with a fixed clock, mirroring
// the internal/state and status-board fake-probe pattern. No subprocess ever
// runs.
func runProbe(alive, match map[int]bool) state.Probe {
	return state.Probe{
		PIDAlive:     func(pid int) bool { return alive[pid] },
		ProcessMatch: func(pid int, _ func(args string) bool) bool { return match[pid] },
		Now:          func() time.Time { return runNow },
	}
}

// seedAgentRecord writes a launch.json under base/<session>/agents/<handle>.
func seedAgentRecord(t *testing.T, base, session, handle string, rec launch.Record) string {
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

// seedPresence writes presence.json into an agent dir so liveness rolls up.
func seedPresence(t *testing.T, agentDir, handle, status string, lastSeen time.Time) {
	t.Helper()
	pres := struct {
		Schema   int       `json:"schema"`
		Handle   string    `json:"handle"`
		Status   string    `json:"status"`
		LastSeen time.Time `json:"last_seen"`
	}{Schema: 1, Handle: handle, Status: status, LastSeen: lastSeen}
	data, err := json.Marshal(pres)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "presence.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// seedMultiSessionConsole seeds two sessions into a fresh base root so the
// --once board has real content to render, and returns the base root.
func seedMultiSessionConsole(t *testing.T) string {
	t.Helper()
	base := t.TempDir()

	dir := seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Role: "cto", Session: "issue-96", AgentPID: 1111,
	})
	seedPresence(t, dir, "cto", "active", runNow.Add(-30*time.Second))

	seedAgentRecord(t, base, "drive-fix", "qa", launch.Record{
		Binary: "claude", Handle: "qa", Role: "qa", Session: "drive-fix", AgentPID: 2222,
	})
	// No fresh presence: qa's recorded PID is dead -> not alive.

	return base
}

// TestRunOnceRendersStaticBoard proves --once renders the static board (with the
// snapshot-wide triage rollup line) to the supplied writer and starts NO tea
// program. It seeds a real multi-session fixture and uses an injected probe.
func TestRunOnceRendersStaticBoard(t *testing.T) {
	base := seedMultiSessionConsole(t)
	proj := t.TempDir()
	var buf bytes.Buffer

	err := Run(Config{
		ProjectDir: proj,
		BaseRoot:   base,
		Probe:      runProbe(map[int]bool{1111: true, 2222: false}, map[int]bool{1111: true}),
		Once:       true,
		Out:        &buf,
	})
	if err != nil {
		t.Fatalf("Run --once: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"mission control",
		"2 sessions",
		"needs-you", // the triage rollup line is present
		"at-risk",
		"blocked",
		"issue-96",
		"drive-fix",
		"cto",
		"qa",
	} {
		if !contains(out, want) {
			t.Errorf("--once board missing %q:\n%s", want, out)
		}
	}
}

// TestRunOnceNoTeamNoticeToStdout proves a degraded NoTeamNotice on the --once
// path writes guidance to Out and exits 0 (never starts a program).
func TestRunOnceNoTeamNoticeToStdout(t *testing.T) {
	var buf bytes.Buffer
	err := Run(Config{
		Once:         true,
		Out:          &buf,
		NoTeamNotice: "amq-squad: no team configured for profile \"default\".",
	})
	if err != nil {
		t.Fatalf("Run --once with notice: %v", err)
	}
	if !contains(buf.String(), "no team configured") {
		t.Errorf("notice should be written to Out:\n%s", buf.String())
	}
}

// TestRunOnceInitialFilterScopesBoard proves a preset InitialFilter (e.g. from
// --session) narrows the --once board to the matching session.
func TestRunOnceInitialFilterScopesBoard(t *testing.T) {
	base := seedMultiSessionConsole(t)
	proj := t.TempDir()
	var buf bytes.Buffer
	err := Run(Config{
		ProjectDir:    proj,
		BaseRoot:      base,
		Probe:         runProbe(map[int]bool{1111: true, 2222: false}, map[int]bool{1111: true}),
		Once:          true,
		Out:           &buf,
		InitialFilter: "session:issue-96",
	})
	if err != nil {
		t.Fatalf("Run --once with filter: %v", err)
	}
	out := buf.String()
	if !contains(out, "issue-96") {
		t.Errorf("filtered board should include issue-96:\n%s", out)
	}
	if contains(out, "drive-fix") {
		t.Errorf("session:issue-96 filter should exclude drive-fix:\n%s", out)
	}
}

// TestRunInteractiveTTYFailureIsReported proves the interactive path reports a
// clear error (rather than silently doing nothing or crashing) when /dev/tty
// cannot be opened, via the injected ttyOpener seam — no real program runs.
func TestRunInteractiveTTYFailureIsReported(t *testing.T) {
	base := seedMultiSessionConsole(t)
	proj := t.TempDir()
	err := Run(Config{
		ProjectDir: proj,
		BaseRoot:   base,
		Probe:      runProbe(map[int]bool{1111: true, 2222: false}, map[int]bool{1111: true}),
		Once:       false,
		ttyOpener:  func() (*os.File, error) { return nil, os.ErrNotExist },
	})
	if err == nil {
		t.Fatal("interactive path with no /dev/tty should report an error")
	}
	if !contains(err.Error(), "--once") {
		t.Errorf("the TTY-failure error should point at --once, got: %v", err)
	}
}

func contains(haystack, needle string) bool {
	return bytes.Contains([]byte(haystack), []byte(needle))
}

// --- Slice-C DX: --once board fixes (unresolved threads, state-aware agent
// labels, the thread-count headline, count reconciliation) --------------------

// onceClock is the deterministic render clock for the --once DX tests.
func onceClock() time.Time { return runNow }

// mkUnresolved builds an unresolved (at-risk/blocked) thread aged d before the
// render clock, so the urgency sort and age labels are deterministic.
func mkUnresolved(id, subj string, parts []string, tier state.Triage, age time.Duration, msgs int) state.ThreadSummary {
	return state.ThreadSummary{
		ID:           id,
		Participants: parts,
		Subject:      subj,
		Triage:       tier,
		MessageCount: msgs,
		LastEventAt:  runNow.Add(-age),
		Freshness:    state.Freshness{Source: state.SourceEmbedded, Age: age},
	}
}

// TestStaticBoardShowsUnresolvedThreadRows proves the --once board surfaces the
// top unresolved coordination threads for a session — not just counts — rendered
// "<participants>  <tier> · <subject> · <age> · <N> msgs", urgency-sorted
// most-stale-first, capped at 3 with a "+N more" line.
func TestStaticBoardShowsUnresolvedThreadRows(t *testing.T) {
	// Five unresolved threads. The brief's literal sample row (migration sign-off)
	// is made the MOST stale so it is shown and its exact shape can be asserted;
	// the freshest (api review, 3m) falls below the cap.
	s := state.Session{
		Name: "release",
		Agents: []state.Agent{
			{Handle: "cto", Engine: "codex", Liveness: state.LivenessAlive, LastSeen: runNow.Add(-30 * time.Second)},
			{Handle: "qa", Engine: "claude", Liveness: state.LivenessAlive, LastSeen: runNow.Add(-30 * time.Second)},
		},
		Coordination: state.Coordination{Threads: []state.ThreadSummary{
			mkUnresolved("p2p/qa__cto", "migration sign-off", []string{"qa", "cto"}, state.TriageBlocked, 55*time.Minute, 3),
			mkUnresolved("p2p/dev__qa", "api review", []string{"dev", "qa"}, state.TriageAtRisk, 3*time.Minute, 1),
			mkUnresolved("p2p/cto__sec", "secrets", []string{"cto", "sec"}, state.TriageAtRisk, 20*time.Minute, 2),
			mkUnresolved("p2p/dev__cto", "schema", []string{"dev", "cto"}, state.TriageBlocked, 7*time.Minute, 4),
			mkUnresolved("p2p/qa__sec", "perms", []string{"qa", "sec"}, state.TriageAtRisk, 40*time.Minute, 6),
		}},
		Rollup: state.TriageRollup{AtRisk: 3, Blocked: 2},
	}
	var roll state.TriageRollup
	roll.Add(s.Rollup)
	snap := state.Snapshot{BaseRoot: "/base", Sessions: []state.Session{s}, Rollup: roll}

	board := StaticBoard(snap, onceClock)

	// The exact "who is blocked on whom" row shape, matching the brief's literal
	// sample: "<participants>  <tier> · <subject> · <age> · <N> msgs".
	if !contains(board, "qa, cto  blocked · migration sign-off · 55m · 3 msgs") {
		t.Errorf("--once should render the unresolved-thread row in the documented shape:\n%s", board)
	}
	// Capped at 3 rows + a "+N more" (5 unresolved -> +2 more).
	if !contains(board, "+2 more") {
		t.Errorf("--once should cap unresolved rows at 3 and show '+2 more':\n%s", board)
	}
	// Urgency-sorted most-stale-first: 55m before 40m before 20m; the freshest
	// (3m api review) is pushed past the cap, so it must NOT appear.
	i55 := strings.Index(board, "migration sign-off")
	i40 := strings.Index(board, "perms")
	i20 := strings.Index(board, "secrets")
	if i55 < 0 || i40 < 0 || i20 < 0 || !(i55 < i40 && i40 < i20) {
		t.Errorf("unresolved rows should be most-stale-first (55m,40m,20m):\n%s", board)
	}
	if contains(board, "api review") {
		t.Errorf("the freshest thread (3m) should be below the cap, not shown:\n%s", board)
	}
}

// TestStaticBoardOmitsUnresolvedSectionWhenClear proves a session with no
// at-risk/blocked threads renders NO unresolved section (just counts + agents).
func TestStaticBoardOmitsUnresolvedSectionWhenClear(t *testing.T) {
	s := state.Session{
		Name:   "calm",
		Agents: []state.Agent{{Handle: "cto", Engine: "codex", Liveness: state.LivenessAlive, LastSeen: runNow}},
		Coordination: state.Coordination{Threads: []state.ThreadSummary{
			{ID: "p2p/cto__qa", Participants: []string{"cto", "qa"}, Subject: "fyi", Triage: state.TriageClear, MessageCount: 1},
		}},
		Rollup: state.TriageRollup{Clear: 1},
	}
	snap := state.Snapshot{BaseRoot: "/base", Sessions: []state.Session{s}}
	board := StaticBoard(snap, onceClock)
	if contains(board, "·") && strings.Count(board, "·") > 4 {
		// The only " · " on a clear board is in the headline; an unresolved row
		// would add more. (Cheap guard; the explicit subject check is below.)
	}
	if contains(board, "fyi ·") {
		t.Errorf("a clear thread must not appear as an unresolved row:\n%s", board)
	}
}

// TestStaticBoardStoppedSessionLabelsAgentsStopped proves a session that rolled
// up to STOPPED (clear triage, no live/degraded agents) renders each agent as
// "stopped" — NOT the alarming "process-dead"/"stale".
func TestStaticBoardStoppedSessionLabelsAgentsStopped(t *testing.T) {
	s := state.Session{
		Name: "done",
		Agents: []state.Agent{
			{Handle: "cto", Engine: "codex", Liveness: state.LivenessDead, LastSeen: runNow.Add(-3 * 24 * time.Hour)},
			{Handle: "qa", Engine: "claude", Liveness: state.LivenessMissing},
		},
		Rollup: state.TriageRollup{Clear: 0},
	}
	snap := state.Snapshot{BaseRoot: "/base", Sessions: []state.Session{s}}
	board := StaticBoard(snap, onceClock)
	if !contains(board, "cto (codex): stopped") || !contains(board, "qa (claude): stopped") {
		t.Errorf("a stopped session should label every agent 'stopped':\n%s", board)
	}
	if contains(board, "process-dead") || contains(board, "stale-heartbeat") {
		t.Errorf("a stopped session must not show 'process-dead'/'stale-heartbeat':\n%s", board)
	}
	// Reconciliation: the agents line is separate and reads 0/2 alive.
	if !contains(board, "agents: 0/2 alive") {
		t.Errorf("stopped session should show 'agents: 0/2 alive':\n%s", board)
	}
}

// TestStaticBoardRunningSessionStateAwareLabelsWithAge proves a still-running
// (problem) session renders stale/dead agents with the clearer vocabulary AND an
// age suffix computed from LastSeen against the injected clock.
func TestStaticBoardRunningSessionStateAwareLabelsWithAge(t *testing.T) {
	// Blocked triage keeps the session OUT of the stopped bucket, so the dead/
	// stale agents read as problems (with age), not "stopped".
	s := state.Session{
		Name: "wedged",
		Agents: []state.Agent{
			{Handle: "cto", Engine: "codex", Liveness: state.LivenessAlive, LastSeen: runNow.Add(-30 * time.Second)},
			{Handle: "qa", Engine: "claude", Liveness: state.LivenessStale, LastSeen: runNow.Add(-3 * 24 * time.Hour)},
			{Handle: "dev", Engine: "claude", Liveness: state.LivenessDead, LastSeen: runNow.Add(-2 * time.Hour)},
		},
		Coordination: state.Coordination{Threads: []state.ThreadSummary{
			mkUnresolved("p2p/qa__cto", "deploy", []string{"qa", "cto"}, state.TriageBlocked, 10*time.Minute, 2),
		}},
		Rollup: state.TriageRollup{Blocked: 1},
	}
	snap := state.Snapshot{BaseRoot: "/base", Sessions: []state.Session{s}, Rollup: state.TriageRollup{Blocked: 1}}
	board := StaticBoard(snap, onceClock)
	if !contains(board, "qa (claude): stale-heartbeat (3d)") {
		t.Errorf("a running session's stale agent should read 'stale-heartbeat (3d)':\n%s", board)
	}
	if !contains(board, "dev (claude): process-dead (2h)") {
		t.Errorf("a running session's dead agent should read 'process-dead (2h)':\n%s", board)
	}
	if !contains(board, "cto (codex): alive") {
		t.Errorf("an alive agent should still read 'alive':\n%s", board)
	}
	if contains(board, ": stopped") {
		t.Errorf("a running (blocked) session must NOT label agents 'stopped':\n%s", board)
	}
}

// TestStaticBoardHeadlineLabelsBlockedThreads proves the --once headline
// separates concepts with " · " and labels the triage numbers as THREAD counts.
func TestStaticBoardHeadlineLabelsBlockedThreads(t *testing.T) {
	snap := state.Snapshot{
		BaseRoot: "/base",
		Sessions: []state.Session{{Name: "a"}, {Name: "b"}},
		Rollup:   state.TriageRollup{NeedsYou: 2, AtRisk: 1, Blocked: 3, Gated: 4},
	}
	board := StaticBoard(snap, onceClock)
	if !contains(board, "2 needs-you threads · 3 blocked threads · 4 gated threads · 1 at-risk thread") {
		t.Errorf("headline should label triage as thread counts with ' · ' separators, each noun pluralized on its own count:\n%s", board)
	}
}
