package console

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-noc/internal/state"
)

// fixedClock is the deterministic render-layer clock for the static-board tests
// (same anchor as the view/run fixtures), so age suffixes are byte-stable.
func fixedClock() time.Time { return viewNow }

// fixtureSnapshot builds an immutable multi-session snapshot without touching
// the filesystem, so the Update / render tests stay pure and deterministic.
func fixtureSnapshot() state.Snapshot {
	s1 := state.Session{
		Name:   "issue-96",
		Root:   "/base/issue-96",
		Agents: []state.Agent{{Handle: "cto", Engine: "codex", Liveness: state.LivenessAlive}},
		Rollup: state.TriageRollup{NeedsYou: 1, AtRisk: 0, Blocked: 0},
	}
	s2 := state.Session{
		Name:   "drive-fix",
		Root:   "/base/drive-fix",
		Agents: []state.Agent{{Handle: "qa", Engine: "claude", Liveness: state.LivenessStale}},
		Rollup: state.TriageRollup{NeedsYou: 0, AtRisk: 2, Blocked: 1},
	}
	var roll state.TriageRollup
	roll.Add(s1.Rollup)
	roll.Add(s2.Rollup)
	return state.Snapshot{BaseRoot: "/base", Sessions: []state.Session{s2, s1}, Rollup: roll}
}

// TestUpdateReplacesSnapshotOnMsg proves a snapshotMsg swaps the held snapshot
// wholesale for the fresh immutable value (the core update-flow contract).
func TestUpdateReplacesSnapshotOnMsg(t *testing.T) {
	m := newModel(rebuildConfig{}, state.Snapshot{}, "")
	if len(m.Snapshot().Sessions) != 0 {
		t.Fatalf("seed snapshot should be empty, got %d sessions", len(m.Snapshot().Sessions))
	}

	fresh := fixtureSnapshot()
	next, _ := m.Update(snapshotMsg{snapshot: fresh})
	got := next.(Model)
	if len(got.Snapshot().Sessions) != 2 {
		t.Fatalf("snapshot not replaced: want 2 sessions, got %d", len(got.Snapshot().Sessions))
	}
	if got.Snapshot().Rollup.NeedsYou != 1 || got.Snapshot().Rollup.AtRisk != 2 || got.Snapshot().Rollup.Blocked != 1 {
		t.Errorf("rollup not carried through: %+v", got.Snapshot().Rollup)
	}
	if got.Err() != nil {
		t.Errorf("a clean snapshotMsg should clear err, got %v", got.Err())
	}
}

// TestUpdateBuildErrKeepsPriorSnapshot proves a build error is surfaced as a
// sticky error WITHOUT blanking the previously-held snapshot.
func TestUpdateBuildErrKeepsPriorSnapshot(t *testing.T) {
	m := newModel(rebuildConfig{}, fixtureSnapshot(), "")
	next, _ := m.Update(snapshotMsg{buildErr: errors.New("scan failed")})
	got := next.(Model)
	if len(got.Snapshot().Sessions) != 2 {
		t.Errorf("build error must not blank the prior snapshot, got %d sessions", len(got.Snapshot().Sessions))
	}
	if got.Err() == nil || !strings.Contains(got.Err().Error(), "scan failed") {
		t.Errorf("build error should be surfaced, got %v", got.Err())
	}
}

// TestUpdateQuitOnQ proves 'q' and ctrl+c set quitting and return tea.Quit.
// NOTE: esc is DELIBERATELY NOT a quit key — the reviewer's hard requirement is
// that esc is back/close (board: no-op), never quit; see TestEscIsBackNotQuit.
func TestUpdateQuitOnQ(t *testing.T) {
	for _, key := range []string{"q", "ctrl+c"} {
		m := newModel(rebuildConfig{}, fixtureSnapshot(), "")
		next, cmd := m.Update(tea.KeyMsg{Type: keyTypeFor(key), Runes: runesFor(key)})
		got := next.(Model)
		if !got.IsQuitting() {
			t.Errorf("key %q should set quitting", key)
		}
		if cmd == nil {
			t.Fatalf("key %q should return a quit command", key)
		}
		// The returned cmd should be tea.Quit (produces a QuitMsg).
		if msg := cmd(); msg == nil {
			t.Errorf("key %q quit cmd produced nil msg", key)
		}
	}
}

// keyTypeFor / runesFor map a small set of key strings to tea.KeyMsg shapes so
// the test can drive handleKey via its String() switch.
func keyTypeFor(s string) tea.KeyType {
	switch s {
	case "ctrl+c":
		return tea.KeyCtrlC
	case "esc":
		return tea.KeyEsc
	default:
		return tea.KeyRunes
	}
}

func runesFor(s string) []rune {
	if len(s) == 1 {
		return []rune(s)
	}
	return nil
}

// TestUpdateWatchErrTriggersRebuild proves a watchErrMsg never crashes: it
// records the error and returns a (rebuild) command rather than nil.
func TestUpdateWatchErrTriggersRebuild(t *testing.T) {
	m := newModel(rebuildConfig{BaseRoot: "/base"}, fixtureSnapshot(), "")
	next, cmd := m.Update(watchErrMsg{err: errors.New("overflow")})
	got := next.(Model)
	if got.Err() == nil {
		t.Error("watch error should be recorded on the model")
	}
	if cmd == nil {
		t.Error("watch error should trigger a rebuild command (resync), got nil")
	}
}

// TestViewRendersRollupHeadline proves the rendered frame leads with the
// snapshot-wide triage rollup line.
func TestViewRendersRollupHeadline(t *testing.T) {
	m := newModel(rebuildConfig{}, fixtureSnapshot(), "")
	out := m.View()
	for _, want := range []string{"mission control", "needs-you", "at-risk", "blocked"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q:\n%s", want, out)
		}
	}
}

// TestNoTeamScreenExplains proves the degraded NoTeam model renders the notice
// rather than an empty frame, and never depends on a snapshot.
func TestNoTeamScreenExplains(t *testing.T) {
	notice := "amq-squad: no team configured for profile \"default\"."
	m := newModel(rebuildConfig{}, state.Snapshot{}, notice)
	if !m.noTeam {
		t.Fatal("a non-empty notice should put the model in the NoTeam state")
	}
	if m.Init() != nil {
		t.Error("NoTeam model should not issue rebuild/tick commands")
	}
	out := m.View()
	if !strings.Contains(out, notice) {
		t.Errorf("NoTeam view should explain the notice:\n%s", out)
	}
}

// --- Watcher debounce / resync unit tests (clock-injected, no OS watcher). ---

// TestDebouncerCoalescesBurst proves a burst of requests inside the window
// produces exactly one Ready, and only after the window elapses from the FIRST
// request.
func TestDebouncerCoalescesBurst(t *testing.T) {
	t0 := time.Unix(0, 0)
	d := newDebouncer(100 * time.Millisecond)

	d.Request(t0, watchDecision{Rebuild: true})
	if !d.Pending() {
		t.Fatal("first request should make the debouncer pending")
	}
	// More requests within the window are coalesced.
	d.Request(t0.Add(20*time.Millisecond), watchDecision{Rebuild: true})
	d.Request(t0.Add(40*time.Millisecond), watchDecision{Rebuild: true})

	if d.Ready(t0.Add(50 * time.Millisecond)) {
		t.Error("debouncer should NOT be ready before the window elapses")
	}
	if !d.Ready(t0.Add(100 * time.Millisecond)) {
		t.Error("debouncer should be ready once the window elapses from the first request")
	}
	d.Reset()
	if d.Pending() {
		t.Error("Reset should clear the pending burst")
	}
}

// TestDebouncerResyncSticky proves any resync request in the burst makes the
// fired rebuild a full resync.
func TestDebouncerResyncSticky(t *testing.T) {
	t0 := time.Unix(0, 0)
	d := newDebouncer(50 * time.Millisecond)
	d.Request(t0, watchDecision{Rebuild: true, Resync: false})
	d.Request(t0.Add(10*time.Millisecond), watchDecision{Rebuild: true, Resync: true})
	d.Request(t0.Add(20*time.Millisecond), watchDecision{Rebuild: true, Resync: false})
	if !d.WantsResync() {
		t.Error("a resync anywhere in the burst should make the rebuild a full resync")
	}
}

// TestDebouncerIgnoresEmpty proves a no-op decision does not start a burst.
func TestDebouncerIgnoresEmpty(t *testing.T) {
	d := newDebouncer(50 * time.Millisecond)
	d.Request(time.Unix(0, 0), watchDecision{})
	if d.Pending() {
		t.Error("an empty decision should not start a debounce burst")
	}
}

// TestClassifyWatchErrorAlwaysResyncs proves every watch error maps to a
// resync (the never-crash contract), and nil maps to no-op.
func TestClassifyWatchErrorAlwaysResyncs(t *testing.T) {
	if dec := classifyWatchError(nil); dec.Rebuild || dec.Resync {
		t.Errorf("nil error should be a no-op, got %+v", dec)
	}
	dec := classifyWatchError(errors.New("queue overflow"))
	if !dec.Rebuild || !dec.Resync {
		t.Errorf("any watch error must force a resync, got %+v", dec)
	}
}

// TestWatchTargetsAreDirsNotLeaves proves the watch set is the session/agent
// PARENT dirs plus the base root — not every maildir leaf — and is deduped.
func TestWatchTargetsAreDirsNotLeaves(t *testing.T) {
	sessions := []sessionDirs{
		{Root: "/base/issue-96", AgentDirs: []string{"/base/issue-96/agents/cto", "/base/issue-96/agents/qa"}},
		{Root: "/base/issue-96", AgentDirs: []string{"/base/issue-96/agents/cto"}}, // dup root
	}
	got := watchTargets("/base", sessions)
	want := map[string]bool{
		"/base":                     true,
		"/base/issue-96":            true,
		"/base/issue-96/agents":     true,
		"/base/issue-96/agents/cto": true,
		"/base/issue-96/agents/qa":  true,
	}
	if len(got) != len(want) {
		t.Fatalf("watch targets = %v, want %d unique entries", got, len(want))
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected watch target %q", g)
		}
	}
	// The base root must always be present so brand-new sessions are noticed.
	seenBase := false
	for _, g := range got {
		if g == "/base" {
			seenBase = true
		}
	}
	if !seenBase {
		t.Error("watch targets must always include the base root")
	}
}

// TestStaticBoardRendersRollupAndSessions proves the static board (the --once
// surface) includes the triage rollup line and one block per session with its
// agents — over the multi-session fixture.
func TestStaticBoardRendersRollupAndSessions(t *testing.T) {
	board := StaticBoard(fixtureSnapshot(), fixedClock)
	for _, want := range []string{
		"mission control",
		"2 sessions",
		// The headline separates concepts with " · " and labels the triage
		// numbers as the THREAD counts they are, pluralizing each noun on its
		// own count (so "1 needs-you thread" / "1 blocked thread").
		"1 needs-you thread · 1 blocked thread · 0 gated threads · 2 at-risk threads",
		"issue-96",
		"drive-fix",
		"cto",
		"qa",
		"alive",
		// drive-fix is at-risk (not stopped), so its stale agent reads
		// "stale-heartbeat" rather than the bare/alarming "stale".
		"stale-heartbeat",
		// agents are a SEPARATE labeled line from the thread triage counts.
		"agents:",
	} {
		if !strings.Contains(board, want) {
			t.Errorf("static board missing %q:\n%s", want, board)
		}
	}
}

// TestStaticBoardEmptyDiscovery proves an empty snapshot renders guidance, not a
// blank board or a crash.
func TestStaticBoardEmptyDiscovery(t *testing.T) {
	board := StaticBoard(state.Snapshot{BaseRoot: "/base"}, fixedClock)
	if !strings.Contains(board, "no sessions found") {
		t.Errorf("empty board should show guidance:\n%s", board)
	}
}
