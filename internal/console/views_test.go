package console

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-noc/internal/state"
)

// viewNow anchors the freshness ages in the view fixtures.
var viewNow = time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)

// richFixture builds a multi-session snapshot with threads, edges, a timeline,
// unread recipients, and a needs-you/at-risk/blocked spread so the view + keymap
// tests have real coordination data to drive (no filesystem).
func richFixture() state.Snapshot {
	// Session A: issue-96 — a needs-you thread to the operator, plus an
	// agent<->agent at-risk thread; cto alive, qa stale.
	tA1 := state.ThreadSummary{
		ID:           "p2p/cto__user",
		Participants: []string{"cto", "user"},
		Subject:      "sign-off",
		Kind:         state.KindReviewRequest,
		Status:       state.ThreadAwaitingReply,
		LastEventAt:  viewNow.Add(-7 * time.Minute),
		MessageCount: 3,
		UnreadBy:     []string{"user"},
		Triage:       state.TriageNeedsYou,
		Freshness:    state.Freshness{Source: state.SourceEmbedded, Age: 7 * time.Minute},
	}
	tA2 := state.ThreadSummary{
		ID:           "p2p/cto__qa",
		Participants: []string{"cto", "qa"},
		Subject:      "review api",
		Kind:         state.KindQuestion,
		Status:       state.ThreadAwaitingReply,
		LastEventAt:  viewNow.Add(-50 * time.Minute),
		MessageCount: 2,
		UnreadBy:     []string{"qa"},
		Triage:       state.TriageAtRisk,
		Freshness:    state.Freshness{Source: state.SourceEmbedded, Age: 50 * time.Minute, Stale: true},
	}
	sA := state.Session{
		Name: "issue-96",
		Root: "/base/issue-96",
		Agents: []state.Agent{
			{Handle: "cto", Engine: "codex", Liveness: state.LivenessAlive, LastSeen: viewNow.Add(-30 * time.Second)},
			{Handle: "qa", Engine: "claude", Liveness: state.LivenessStale},
		},
		Coordination: state.Coordination{
			Threads: []state.ThreadSummary{tA1, tA2},
			Edges: []state.Edge{
				{From: "cto", To: "qa", Count: 2},
				{From: "qa", To: "cto", Count: 1},
			},
			Timeline: []state.TimelineEvent{
				{At: tA2.LastEventAt, Kind: state.KindQuestion, Summary: "cto -> qa: asked a question", Source: "p2p/cto__qa"},
				{At: tA1.LastEventAt, Kind: state.KindReviewRequest, Summary: "cto -> user: awaiting review", Source: "p2p/cto__user"},
			},
		},
		Rollup: state.TriageRollup{NeedsYou: 1, AtRisk: 1},
	}

	// Session B: drive-fix — a blocked thread, all agents dead (stopped session).
	tB1 := state.ThreadSummary{
		ID:           "p2p/dev__qa",
		Participants: []string{"dev", "qa"},
		Subject:      "deploy",
		Kind:         state.KindReviewResponse,
		Status:       state.ThreadBlocked,
		LastEventAt:  viewNow.Add(-20 * time.Minute),
		MessageCount: 5,
		Triage:       state.TriageBlocked,
		Freshness:    state.Freshness{Source: state.SourceMtime, Age: 20 * time.Minute},
	}
	sB := state.Session{
		Name: "drive-fix",
		Root: "/base/drive-fix",
		Agents: []state.Agent{
			{Handle: "dev", Engine: "claude", Liveness: state.LivenessDead},
			{Handle: "qa", Engine: "codex", Liveness: state.LivenessDead},
		},
		Coordination: state.Coordination{
			Threads:  []state.ThreadSummary{tB1},
			Timeline: []state.TimelineEvent{{At: tB1.LastEventAt, Summary: "dev blocked on qa", Source: "p2p/dev__qa"}},
		},
		Rollup: state.TriageRollup{Blocked: 1},
	}

	var roll state.TriageRollup
	roll.Add(sA.Rollup)
	roll.Add(sB.Rollup)
	// Order deliberately NOT attention-first on disk, so the board's sort is tested.
	return state.Snapshot{BaseRoot: "/base", Sessions: []state.Session{sB, sA}, Rollup: roll}
}

// keyMsg builds a tea.KeyMsg for a single-rune or named key.
func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "space", " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// press drives a sequence of keys through Update and returns the final Model.
func press(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	var model tea.Model = m
	for _, k := range keys {
		model, _ = model.Update(keyMsg(k))
	}
	return model.(Model)
}

// boardModel returns a board model seeded with the rich fixture, with an initial
// selection resolved (as Init/snapshotMsg would do).
func boardModel() Model {
	m := newModel(rebuildConfig{BaseRoot: "/base"}, richFixture(), "")
	m = m.reselect()
	return m
}

// --- Key routing -------------------------------------------------------------

// TestEnterDrillsBoardToDetail proves enter on a board session drills into the
// detail route for that session (and ONLY drills — never peeks/attaches).
func TestEnterDrillsBoardToDetail(t *testing.T) {
	m := boardModel()
	if m.Route() != routeBoard {
		t.Fatalf("should start on the board, got route %d", m.Route())
	}
	// The attention-first sort puts issue-96 (needs-you) first.
	if m.Selected() != "issue-96" {
		t.Fatalf("first selection should be the needs-you session issue-96, got %q", m.Selected())
	}
	m = press(t, m, "enter")
	if m.Route() != routeSession {
		t.Errorf("enter on a session should drill to the detail route, got %d", m.Route())
	}
	if m.SessionName() != "issue-96" {
		t.Errorf("drill should target issue-96, got %q", m.SessionName())
	}
	if m.Overlay() != overlayNone {
		t.Errorf("enter must NOT open an overlay (no peek/attach doubling), got overlay %d", m.Overlay())
	}
}

// TestSpacePeeksReadOnly proves space opens the read-only peek overlay and the
// overlay text states replies are unavailable in read-only mode.
func TestSpacePeeksReadOnly(t *testing.T) {
	m := boardModel()
	m = press(t, m, "space")
	if m.Overlay() != overlayPeek {
		t.Fatalf("space should open the peek overlay, got %d", m.Overlay())
	}
	out := m.View()
	if !strings.Contains(out, "read-only") {
		t.Errorf("peek must be marked read-only:\n%s", out)
	}
	if !strings.Contains(out, "response unavailable in read-only mode") {
		t.Errorf("peek must say responses are unavailable in read-only mode:\n%s", out)
	}
}

// TestEnterNeverPeeksOrAttaches proves enter is strictly drill: it never sets the
// peek or attach overlay.
func TestEnterNeverPeeksOrAttaches(t *testing.T) {
	m := boardModel()
	m = press(t, m, "enter")
	if m.Overlay() == overlayPeek || m.Overlay() == overlayAttach {
		t.Errorf("enter must never peek or attach, got overlay %d", m.Overlay())
	}
}

// TestLEntersLogMode proves `l` from the detail view routes to the log/tail mode.
func TestLEntersLogMode(t *testing.T) {
	m := boardModel()
	m = press(t, m, "enter") // drill to detail
	m = press(t, m, "l")
	if m.Route() != routeThread {
		t.Errorf("l should enter the log/tail route, got %d", m.Route())
	}
	out := m.View()
	if !strings.Contains(strings.ToLower(out), "logs") {
		t.Errorf("log view should label itself as logs:\n%s", out)
	}
}

// TestTTogglesTimeline proves `t` toggles the detail timeline pane on and off.
func TestTTogglesTimeline(t *testing.T) {
	m := boardModel()
	m = press(t, m, "enter") // detail
	if m.TimelineOn() {
		t.Fatal("timeline should start off")
	}
	m = press(t, m, "t")
	if !m.TimelineOn() {
		t.Error("t should toggle the timeline pane on")
	}
	if !strings.Contains(m.View(), "timeline") {
		t.Errorf("timeline pane should render its heading:\n%s", m.View())
	}
	m = press(t, m, "t")
	if m.TimelineOn() {
		t.Error("t should toggle the timeline pane back off")
	}
}

// TestEscIsBackNotQuit proves esc steps detail -> board (and is a no-op on the
// board), and NEVER quits. The reviewer's hard requirement.
func TestEscIsBackNotQuit(t *testing.T) {
	m := boardModel()
	m = press(t, m, "enter") // drill to detail
	if m.Route() != routeSession {
		t.Fatalf("precondition: should be in detail, got %d", m.Route())
	}
	m = press(t, m, "esc")
	if m.Route() != routeBoard {
		t.Errorf("esc from detail should return to the board, got %d", m.Route())
	}
	if m.IsQuitting() {
		t.Error("esc must NOT quit")
	}
	// esc on the board is a no-op (q quits) and still must not quit.
	m = press(t, m, "esc")
	if m.IsQuitting() {
		t.Error("esc on the board must not quit")
	}
}

// TestEscClosesOverlay proves esc closes an open overlay and returns to the
// underlying route (without quitting).
func TestEscClosesOverlay(t *testing.T) {
	m := boardModel()
	m = press(t, m, "space") // open peek
	if m.Overlay() != overlayPeek {
		t.Fatalf("precondition: peek overlay open, got %d", m.Overlay())
	}
	m = press(t, m, "esc")
	if m.Overlay() != overlayNone {
		t.Errorf("esc should close the overlay, got %d", m.Overlay())
	}
	if m.Route() != routeBoard || m.IsQuitting() {
		t.Errorf("esc closing an overlay must not change route or quit (route=%d quit=%v)", m.Route(), m.IsQuitting())
	}
}

// TestQQuits proves q sets quitting and returns tea.Quit.
func TestQQuits(t *testing.T) {
	m := boardModel()
	next, cmd := m.Update(keyMsg("q"))
	if !next.(Model).IsQuitting() {
		t.Error("q should set quitting")
	}
	if cmd == nil || cmd() == nil {
		t.Error("q should return the quit command")
	}
}

// --- Attach is inert and surfaces the suggested command ---------------------

// TestAttachIsInertAndShowsCommand proves `a` does NOT mutate route/snapshot,
// opens the attach overlay, and surfaces a suggested tmux/amq jump command.
func TestAttachIsInertAndShowsCommand(t *testing.T) {
	m := boardModel()
	before := m.Snapshot()
	m2 := press(t, m, "a")

	if m2.Overlay() != overlayAttach {
		t.Fatalf("a should open the attach overlay, got %d", m2.Overlay())
	}
	if m2.Route() != routeBoard {
		t.Errorf("a must not change the route, got %d", m2.Route())
	}
	if len(m2.Snapshot().Sessions) != len(before.Sessions) {
		t.Error("a must not mutate the snapshot")
	}
	hint := m2.AttachHint()
	if hint == "" {
		t.Fatal("a should compute a suggested attach command")
	}
	if !strings.Contains(hint, "amq-squad attach") || !strings.Contains(hint, "tmux") {
		t.Errorf("attach hint should suggest amq-squad attach and a tmux fallback, got: %s", hint)
	}
	out := m2.View()
	if !strings.Contains(out, "nothing was attached") {
		t.Errorf("attach overlay must state nothing was attached:\n%s", out)
	}
	// The selected board row is the needs-you session; its name should appear.
	if !strings.Contains(hint, "issue-96") {
		t.Errorf("attach hint should reference the selected session issue-96, got: %s", hint)
	}
}

// TestAttachAgentHintNamesAgent proves attach on a selected agent suggests a
// per-agent jump command naming the handle.
func TestAttachAgentHintNamesAgent(t *testing.T) {
	m := boardModel()
	m = press(t, m, "enter") // detail of issue-96; first row is the cto agent
	sel, ok := m.selectedRow()
	if !ok || sel.kind != rowAgent {
		t.Fatalf("first detail row should be an agent, got %+v ok=%v", sel, ok)
	}
	m = press(t, m, "a")
	hint := m.AttachHint()
	if !strings.Contains(hint, "--agent") {
		t.Errorf("agent attach hint should include --agent, got: %s", hint)
	}
}

// --- Filter predicates -------------------------------------------------------

// TestFilterPredicatesSelectRightSessions drives each filter and asserts which
// sessions survive on the board.
func TestFilterPredicatesSelectRightSessions(t *testing.T) {
	snap := richFixture()
	cases := []struct {
		expr string
		want []string // expected session names, attention-first order
	}{
		{"needs-you", []string{"issue-96"}},
		{"at-risk", []string{"issue-96"}},
		{"blocked", []string{"drive-fix"}},
		{"unread", []string{"issue-96"}}, // only issue-96 has unread recipients
		{"agent:dev", []string{"drive-fix"}},
		{"model:codex", []string{"issue-96", "drive-fix"}}, // both have a codex agent
		{"session:issue", []string{"issue-96"}},
	}
	for _, c := range cases {
		f := parseFilter(c.expr)
		got := boardSessions(snap, f)
		var names []string
		for _, s := range got {
			names = append(names, s.Name)
		}
		if !equalStrings(names, c.want) {
			t.Errorf("filter %q sessions = %v, want %v", c.expr, names, c.want)
		}
	}
}

// TestFilterThreadPredicates proves the thread-level predicates narrow the bus.
func TestFilterThreadPredicates(t *testing.T) {
	snap := richFixture()
	var issue96 state.Session
	for _, s := range snap.Sessions {
		if s.Name == "issue-96" {
			issue96 = s
		}
	}
	if got := sortThreads(issue96, parseFilter("needs-you")); len(got) != 1 || got[0].Triage != state.TriageNeedsYou {
		t.Errorf("needs-you should select the single needs-you thread, got %+v", got)
	}
	if got := sortThreads(issue96, parseFilter("at-risk")); len(got) != 1 || got[0].Triage != state.TriageAtRisk {
		t.Errorf("at-risk should select the single at-risk thread, got %+v", got)
	}
	if got := sortThreads(issue96, parseFilter("unread")); len(got) != 2 {
		t.Errorf("both issue-96 threads have unread recipients, want 2, got %d", len(got))
	}
	if got := sortThreads(issue96, parseFilter("agent:qa")); len(got) != 1 || !participates(got[0], "qa") {
		t.Errorf("agent:qa should select the qa thread, got %+v", got)
	}
}

// TestFilterEntryViaSlash proves the `/` entry flow: typed runes build the query,
// enter applies it, and the board narrows accordingly.
func TestFilterEntryViaSlash(t *testing.T) {
	m := boardModel()
	m = press(t, m, "/")
	if !m.Filtering() {
		t.Fatal("/ should open the filter input")
	}
	m = press(t, m, "b", "l", "o", "c", "k", "e", "d")
	if m.FilterInput() != "blocked" {
		t.Fatalf("typed filter should accumulate, got %q", m.FilterInput())
	}
	m = press(t, m, "enter")
	if m.Filtering() {
		t.Error("enter should close the filter input")
	}
	if !m.ActiveFilter().active() || m.ActiveFilter().kind != filterBlocked {
		t.Errorf("applied filter should be 'blocked', got %+v", m.ActiveFilter())
	}
	// Only the blocked session survives; selection re-resolves onto it.
	if rows := m.rowsFor(); len(rows) != 1 || rows[0].ID != "drive-fix" {
		t.Errorf("blocked filter should leave only drive-fix, got %+v", rows)
	}
}

// TestFilterBackspaceEdits proves backspace deletes from the in-progress query.
func TestFilterBackspaceEdits(t *testing.T) {
	m := boardModel()
	m = press(t, m, "/")
	m = press(t, m, "b", "l", "x")
	if m.FilterInput() != "blx" {
		t.Fatalf("typed input should be blx, got %q", m.FilterInput())
	}
	m, _ = applyKey(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.FilterInput() != "bl" {
		t.Errorf("backspace should delete the last rune, got %q", m.FilterInput())
	}
}

// applyKey is a tiny helper to send a raw tea.KeyMsg.
func applyKey(m Model, k tea.KeyMsg) (Model, tea.Cmd) {
	next, cmd := m.Update(k)
	return next.(Model), cmd
}

// TestAgentFilterViaSlash proves an agent:<handle> filter typed via / (with its
// colon) parses and narrows correctly.
func TestAgentFilterViaSlash(t *testing.T) {
	m := boardModel()
	m = press(t, m, "/")
	m = press(t, m, "a", "g", "e", "n", "t", ":", "d", "e", "v")
	m = press(t, m, "enter")
	if m.ActiveFilter().kind != filterAgent || m.ActiveFilter().arg != "dev" {
		t.Fatalf("agent:dev should parse to filterAgent/dev, got %+v", m.ActiveFilter())
	}
	if rows := m.rowsFor(); len(rows) != 1 || rows[0].ID != "drive-fix" {
		t.Errorf("agent:dev should leave only drive-fix, got %+v", rows)
	}
}

// TestFilterEscCancels proves esc in filter-input mode cancels without applying.
func TestFilterEscCancels(t *testing.T) {
	m := boardModel()
	m = press(t, m, "/")
	m = press(t, m, "b", "l")
	m = press(t, m, "esc")
	if m.Filtering() {
		t.Error("esc should close the filter input")
	}
	if m.ActiveFilter().active() {
		t.Errorf("esc should NOT apply the in-progress filter, got %+v", m.ActiveFilter())
	}
}

// --- Selection stability across snapshot replacement ------------------------

// TestSelectionSurvivesSnapshotReplace proves the cursor stays on the same
// logical row (by stable id) after a snapshot is replaced, and never resets to
// the top.
func TestSelectionSurvivesSnapshotReplace(t *testing.T) {
	m := boardModel()
	m = press(t, m, "down") // move off the first row
	want := m.Selected()
	if want == "" {
		t.Fatal("expected a non-empty selection after moving down")
	}
	// Replace with a fresh snapshot whose session slice is in a DIFFERENT order.
	fresh := richFixture() // same ids, sB,sA order on disk
	next, _ := m.Update(snapshotMsg{snapshot: fresh})
	got := next.(Model)
	if got.Selected() != want {
		t.Errorf("selection should survive a snapshot replace by stable id: want %q, got %q", want, got.Selected())
	}
}

// TestSelectionFallsBackToNearestSameGroup proves that when the selected row
// VANISHES, the cursor moves to the nearest same-group row rather than resetting.
func TestSelectionFallsBackToNearestSameGroup(t *testing.T) {
	// Three running (clear) sessions so a removed one falls back within the group.
	mk := func(name string) state.Session {
		return state.Session{
			Name:   name,
			Agents: []state.Agent{{Handle: "a", Engine: "codex", Liveness: state.LivenessAlive}},
		}
	}
	snap := state.Snapshot{BaseRoot: "/base", Sessions: []state.Session{mk("alpha"), mk("bravo"), mk("charlie")}}
	m := newModel(rebuildConfig{BaseRoot: "/base"}, snap, "")
	m = m.reselect()
	m = press(t, m, "down") // select bravo (middle, running group)
	if m.Selected() != "bravo" {
		t.Fatalf("precondition: should select bravo, got %q", m.Selected())
	}
	// Remove bravo; alpha and charlie remain in the same running group.
	snap2 := state.Snapshot{BaseRoot: "/base", Sessions: []state.Session{mk("alpha"), mk("charlie")}}
	next, _ := m.Update(snapshotMsg{snapshot: snap2})
	got := next.(Model)
	if got.Selected() == "" {
		t.Fatal("a vanished selection must not blank the cursor")
	}
	if got.Selected() != "alpha" && got.Selected() != "charlie" {
		t.Errorf("vanished selection should fall back within the same group, got %q", got.Selected())
	}
}

// --- Board summary renders the rollup counts --------------------------------

// TestBoardSummaryRendersRollupCounts proves the board's summary line shows the
// triage rollup counts in the "<n> needs you · <n> at risk · <n> blocked" shape.
func TestBoardSummaryRendersRollupCounts(t *testing.T) {
	m := boardModel()
	out := m.renderBoard()
	for _, want := range []string{"needs you", "at risk", "blocked"} {
		if !strings.Contains(out, want) {
			t.Errorf("board summary missing %q:\n%s", want, out)
		}
	}
	// The fixture has 1 needs-you, 1 at-risk, 1 blocked.
	if !strings.Contains(out, "1") {
		t.Errorf("board summary should show the counts:\n%s", out)
	}
	// Attention-first: the NEEDS YOU group heading appears before BLOCKED (a
	// blocked session sorts into the BLOCKED group even with dead agents, since
	// triage outranks liveness).
	ny := strings.Index(out, "NEEDS YOU")
	blk := strings.Index(out, "BLOCKED")
	if ny < 0 || blk < 0 || ny > blk {
		t.Errorf("board should group attention-first (NEEDS YOU before BLOCKED):\n%s", out)
	}
}

// TestBusRowRendersUrgencyShape proves a collapsed-thread bus row reads like
// "qa <-> cto  blocked · subject  N msgs · 7m".
func TestBusRowRendersUrgencyShape(t *testing.T) {
	m := boardModel()
	m = press(t, m, "enter") // detail of issue-96
	out := m.renderDetail()
	if !strings.Contains(out, "<->") {
		t.Errorf("bus row should render the peer pair with <->:\n%s", out)
	}
	if !strings.Contains(out, "msgs") {
		t.Errorf("bus row should render the message count:\n%s", out)
	}
	// The at-risk thread aged 50m should surface an age label.
	if !strings.Contains(out, "50m") {
		t.Errorf("bus row should render the age label:\n%s", out)
	}
}

// TestDetailRendersEdgesAndAgents proves the detail view shows the agent roster
// and the edge list.
func TestDetailRendersEdgesAndAgents(t *testing.T) {
	m := boardModel()
	m = press(t, m, "enter")
	out := m.renderDetail()
	for _, want := range []string{"agents", "cto", "qa", "threads", "edges", "->"} {
		if !strings.Contains(out, want) {
			t.Errorf("detail view missing %q:\n%s", want, out)
		}
	}
}

// TestHelpOverlay proves `?` opens the help overlay listing the keymap.
func TestHelpOverlay(t *testing.T) {
	m := boardModel()
	m = press(t, m, "?")
	if m.Overlay() != overlayHelp {
		t.Fatalf("? should open the help overlay, got %d", m.Overlay())
	}
	out := m.View()
	for _, want := range []string{"peek", "drill", "attach", "timeline", "filter", "quit", "READ-ONLY"} {
		if !strings.Contains(out, want) {
			t.Errorf("help should document %q:\n%s", want, out)
		}
	}
}

// TestRefreshNowIssuesRebuild proves `g` issues a (read-only) resync command and
// does not quit or change route.
func TestRefreshNowIssuesRebuild(t *testing.T) {
	m := boardModel()
	next, cmd := m.Update(keyMsg("g"))
	if cmd == nil {
		t.Fatal("g should issue a rebuild/resync command")
	}
	got := next.(Model)
	if got.IsQuitting() || got.Route() != routeBoard {
		t.Errorf("g must not quit or change route (quit=%v route=%d)", got.IsQuitting(), got.Route())
	}
	// The command, when run, produces a snapshotMsg (a re-READ of disk).
	if _, ok := cmd().(snapshotMsg); !ok {
		t.Error("g's command should produce a snapshotMsg (a disk re-read)")
	}
}

// --- Slice-C DX: interactive views (state-aware labels, headline, attach) ----

// viewClock is the deterministic render clock for the interactive DX tests.
func viewClock() time.Time { return viewNow }

// clockedModel builds a board model over snap with the injected viewClock, so
// the interactive views age agent signals deterministically.
func clockedModel(snap state.Snapshot) Model {
	m := newModel(rebuildConfig{BaseRoot: "/base", Probe: state.Probe{Now: viewClock}}, snap, "")
	return m.reselect()
}

// stoppedVsRunningFixture builds two sessions: "wrapped" rolled up to STOPPED
// (clear triage, all agents dead/missing) and "wedged" still running with a
// blocked thread and stale/dead agents that must read as problems (with age).
func stoppedVsRunningFixture() state.Snapshot {
	stopped := state.Session{
		Name: "wrapped",
		Agents: []state.Agent{
			{Handle: "cto", Engine: "codex", Liveness: state.LivenessDead, LastSeen: viewNow.Add(-3 * 24 * time.Hour)},
			{Handle: "qa", Engine: "claude", Liveness: state.LivenessMissing},
		},
	}
	running := state.Session{
		Name: "wedged",
		Agents: []state.Agent{
			{Handle: "cto", Engine: "codex", Liveness: state.LivenessAlive, LastSeen: viewNow.Add(-20 * time.Second)},
			{Handle: "qa", Engine: "claude", Liveness: state.LivenessStale, LastSeen: viewNow.Add(-3 * 24 * time.Hour)},
			{Handle: "dev", Engine: "claude", Liveness: state.LivenessDead, LastSeen: viewNow.Add(-2 * time.Hour)},
		},
		Coordination: state.Coordination{Threads: []state.ThreadSummary{{
			ID: "p2p/qa__cto", Participants: []string{"qa", "cto"}, Subject: "deploy",
			Triage: state.TriageBlocked, Status: state.ThreadBlocked, MessageCount: 2,
			LastEventAt: viewNow.Add(-10 * time.Minute),
			Freshness:   state.Freshness{Source: state.SourceEmbedded, Age: 10 * time.Minute},
		}}},
		Rollup: state.TriageRollup{Blocked: 1},
	}
	var roll state.TriageRollup
	roll.Add(running.Rollup)
	return state.Snapshot{BaseRoot: "/base", Sessions: []state.Session{stopped, running}, Rollup: roll}
}

// TestInteractiveStoppedSessionLabelsAgentsStopped proves the detail view of a
// STOPPED session renders agents as "stopped" — not "process-dead"/"stale".
func TestInteractiveStoppedSessionLabelsAgentsStopped(t *testing.T) {
	m := clockedModel(stoppedVsRunningFixture())
	// Drill into the stopped session by stable id.
	m.selectedID = "wrapped"
	m = m.reselect()
	m = press(t, m, "enter")
	if m.SessionName() != "wrapped" {
		t.Fatalf("precondition: should be in the wrapped detail, got %q", m.SessionName())
	}
	out := m.renderDetail()
	if !strings.Contains(out, "stopped") {
		t.Errorf("a stopped session's agents should read 'stopped':\n%s", out)
	}
	if strings.Contains(out, "process-dead") || strings.Contains(out, "stale-heartbeat") {
		t.Errorf("a stopped session must not read 'process-dead'/'stale-heartbeat':\n%s", out)
	}
}

// TestInteractiveRunningSessionStateAwareLabelsWithAge proves the detail view of
// a still-running (blocked) session reads stale/dead agents with the clearer
// vocabulary AND an age suffix from LastSeen against the injected clock.
func TestInteractiveRunningSessionStateAwareLabelsWithAge(t *testing.T) {
	m := clockedModel(stoppedVsRunningFixture())
	m.selectedID = "wedged"
	m = m.reselect()
	m = press(t, m, "enter")
	out := m.renderDetail()
	if !strings.Contains(out, "stale-heartbeat (3d)") {
		t.Errorf("running session's stale agent should read 'stale-heartbeat (3d)':\n%s", out)
	}
	if !strings.Contains(out, "process-dead (2h)") {
		t.Errorf("running session's dead agent should read 'process-dead (2h)':\n%s", out)
	}
	if !strings.Contains(out, "alive") {
		t.Errorf("an alive agent should still read 'alive':\n%s", out)
	}
	if strings.Contains(out, "stopped") {
		t.Errorf("a running (blocked) session must NOT label agents 'stopped':\n%s", out)
	}
}

// TestAgentStateLabelDeadMailboxLiveKeepsDistinctLabel is the regression guard
// for the dead-process / live-mailbox zombie-heartbeat signal: in a LIVE (not
// stopped) session that agent must keep its own "dead-mailbox-live" label and
// MUST NOT collapse into the alarming "process-dead" or the teardown "stopped".
// This protects the labels.go branch that returns the liveness verbatim.
func TestAgentStateLabelDeadMailboxLiveKeepsDistinctLabel(t *testing.T) {
	a := state.Agent{
		Handle:   "cto",
		Engine:   "codex",
		Liveness: state.LivenessDeadMailboxLive,
		LastSeen: viewNow.Add(-2 * time.Hour),
	}
	// stopped=false: the session is still live, so the distinct label stands.
	got := agentStateLabel(a, false, viewClock)
	if got != "dead-mailbox-live" {
		t.Fatalf("dead-mailbox-live agent in a live session should read 'dead-mailbox-live', got %q", got)
	}
	if strings.Contains(got, "stopped") || strings.Contains(got, "process-dead") {
		t.Errorf("dead-mailbox-live label must not collapse into 'stopped'/'process-dead': %q", got)
	}
}

// TestInteractiveRunningSessionKeepsDeadMailboxLiveLabel is the end-to-end
// companion: a still-running session whose agent is dead-mailbox-live must
// render that distinct label in the detail view, not "stopped"/"process-dead".
func TestInteractiveRunningSessionKeepsDeadMailboxLiveLabel(t *testing.T) {
	running := state.Session{
		Name: "wedged",
		Agents: []state.Agent{
			{Handle: "cto", Engine: "codex", Liveness: state.LivenessAlive, LastSeen: viewNow.Add(-20 * time.Second)},
			{Handle: "qa", Engine: "claude", Liveness: state.LivenessDeadMailboxLive, LastSeen: viewNow.Add(-2 * time.Hour)},
		},
		// A blocked thread keeps the session OUT of the stopped bucket, so the
		// stopped short-circuit can't mask the distinct label.
		Coordination: state.Coordination{Threads: []state.ThreadSummary{{
			ID: "p2p/qa__cto", Participants: []string{"qa", "cto"}, Subject: "deploy",
			Triage: state.TriageBlocked, Status: state.ThreadBlocked, MessageCount: 2,
			LastEventAt: viewNow.Add(-10 * time.Minute),
			Freshness:   state.Freshness{Source: state.SourceEmbedded, Age: 10 * time.Minute},
		}}},
		Rollup: state.TriageRollup{Blocked: 1},
	}
	snap := state.Snapshot{BaseRoot: "/base", Sessions: []state.Session{running}, Rollup: state.TriageRollup{Blocked: 1}}
	m := clockedModel(snap)
	m.selectedID = "wedged"
	m = m.reselect()
	m = press(t, m, "enter")
	out := m.renderDetail()
	if !strings.Contains(out, "dead-mailbox-live") {
		t.Errorf("running session's dead-mailbox-live agent should keep its distinct label:\n%s", out)
	}
	if strings.Contains(out, "stopped") {
		t.Errorf("a running (blocked) session must NOT label its dead-mailbox-live agent 'stopped':\n%s", out)
	}
	if strings.Contains(out, "process-dead") {
		t.Errorf("dead-mailbox-live must NOT collapse into 'process-dead':\n%s", out)
	}
}

// TestInteractiveHeadlineLabelsBlockedThreads proves the interactive board
// headline labels the triage numbers as THREAD counts with " · " separators.
func TestInteractiveHeadlineLabelsBlockedThreads(t *testing.T) {
	snap := state.Snapshot{
		BaseRoot: "/base",
		Sessions: []state.Session{{Name: "a"}, {Name: "b"}},
		Rollup:   state.TriageRollup{NeedsYou: 2, AtRisk: 1, Blocked: 3},
	}
	m := clockedModel(snap)
	head := m.renderHeader()
	if !strings.Contains(head, "3 blocked threads") {
		t.Errorf("interactive headline should say 'blocked threads':\n%s", head)
	}
	if !strings.Contains(head, " · ") {
		t.Errorf("interactive headline should separate concepts with ' · ':\n%s", head)
	}
}

// TestAttachOverlayStartsWithReadOnlyNotice proves the inert attach overlay text
// STARTS with "Read-only mode: not attaching" before showing the command.
func TestAttachOverlayStartsWithReadOnlyNotice(t *testing.T) {
	m := boardModel()
	m = press(t, m, "a")
	overlay := m.renderAttach()
	if !strings.HasPrefix(strings.TrimSpace(overlay), "Read-only mode: not attaching") {
		t.Errorf("attach overlay must START with 'Read-only mode: not attaching':\n%s", overlay)
	}
	// The suggested command still follows.
	if !strings.Contains(overlay, "amq-squad attach") {
		t.Errorf("attach overlay should still show the suggested command:\n%s", overlay)
	}
}

// TestFooterAttachNotBarePlainAttach proves the footer labels `a` as a command
// to copy, never the bare "attach" (which would imply it attaches).
func TestFooterAttachNotBarePlainAttach(t *testing.T) {
	m := boardModel()
	footer := m.keyHints()
	if !strings.Contains(footer, "a copy attach cmd") {
		t.Errorf("footer should label `a` as 'copy attach cmd':\n%s", footer)
	}
	if strings.Contains(footer, "a attach ") || strings.HasSuffix(footer, "a attach") {
		t.Errorf("footer must NOT call `a` a bare 'attach':\n%s", footer)
	}
}

// equalStrings compares two string slices (nil == empty).
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
