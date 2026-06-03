package console

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-noc/internal/noc"
)

// betaQAAgentIdx finds the index of beta's running qa agent in the current node
// list, expanding nothing (it is visible by default in the fixture).
func betaQAAgentIdx(t *testing.T, m *NOCModel) int {
	t.Helper()
	for i, n := range m.nodes() {
		if n.kind == nodeAgent && n.agent.Handle == "qa" {
			return i
		}
	}
	t.Fatalf("beta/qa agent node not found in %d nodes", len(m.nodes()))
	return -1
}

// installPaneSeams wires the switchTo/panes/pidTree seams so a jump on beta/qa
// resolves a pane WITHOUT spawning anything; called records whether switchTo
// fired.
func installPaneSeams(m *NOCModel, called *bool) {
	var beta noc.ProjectSnapshot
	for _, n := range m.nodes() {
		if n.kind == nodeProject && n.label == "beta" {
			beta = n.project
			break
		}
	}
	m.switchTo = func(noc.TmuxTarget) error { *called = true; return nil }
	m.panes = func() ([]noc.TmuxPane, error) {
		return []noc.TmuxPane{{
			Session: "beta", Window: "0", Pane: "1", PID: 5001,
			Command: "claude", CWD: beta.Dir,
		}}, nil
	}
	m.pidTree = func(int) []int { return nil }
}

// --- QA-2 / QA-4b: confirm guard on jump (enter on a running agent) ----------

func TestNOCFocusGuard_EnterOnRunningAgentConfirmsBeforeFocus(t *testing.T) {
	m := newSeededNOCModel(t)
	called := false
	installPaneSeams(m, &called)

	m.cursor = betaQAAgentIdx(t, m)
	m, _ = nocPress(m, "enter")

	// enter on a running agent opens the confirm overlay and does NOT focus yet.
	if m.jumpPending == nil {
		t.Fatal("enter on a running agent should open the focus confirm overlay")
	}
	if called {
		t.Fatal("enter must NOT call the focus seam before confirm")
	}
	if !strings.Contains(m.jumpPending.prompt, "Jump to") {
		t.Errorf("confirm prompt = %q, want it to mention Jump to", m.jumpPending.prompt)
	}

	// y confirms: the seam fires exactly once and the overlay closes.
	m, _ = nocPress(m, "y")
	if m.jumpPending != nil {
		t.Error("y should close the focus confirm overlay")
	}
	if !called {
		t.Fatal("y on the focus confirm should call the focus seam once")
	}
}

func TestNOCFocusGuard_EscCancelsJumpSeamUncalled(t *testing.T) {
	m := newSeededNOCModel(t)
	called := false
	installPaneSeams(m, &called)

	m.cursor = betaQAAgentIdx(t, m)
	m, _ = nocPress(m, "enter")
	if m.jumpPending == nil {
		t.Fatal("enter should open the confirm overlay")
	}
	m, _ = nocPress(m, "esc")
	if m.jumpPending != nil {
		t.Error("esc should close the confirm overlay")
	}
	if called {
		t.Error("esc must cancel — the focus seam stays uncalled")
	}
}

func TestNOCFocusGuard_OtherKeyCancelsJump(t *testing.T) {
	m := newSeededNOCModel(t)
	called := false
	installPaneSeams(m, &called)

	m.cursor = betaQAAgentIdx(t, m)
	m, _ = nocPress(m, "J")
	if m.jumpPending == nil {
		t.Fatal("J should open the confirm overlay")
	}
	// Any non-y/enter key cancels.
	m, _ = nocPress(m, "n")
	if m.jumpPending != nil {
		t.Error("a non-confirm key should close the overlay")
	}
	if called {
		t.Error("a non-confirm key must cancel — seam uncalled")
	}
}

func TestNOCFocusGuard_JKeyConfirmsBeforeFocus(t *testing.T) {
	m := newSeededNOCModel(t)
	called := false
	installPaneSeams(m, &called)

	m.cursor = betaQAAgentIdx(t, m)
	m, _ = nocPress(m, "J")
	if m.jumpPending == nil {
		t.Fatal("J should open the focus confirm overlay")
	}
	if called {
		t.Fatal("J must NOT focus before confirm")
	}
	m, _ = nocPress(m, "enter") // enter also confirms
	if !called {
		t.Fatal("enter on the focus confirm should call the focus seam")
	}
}

// TestNOCFocusGuard_EnterOnParentRowExpandsNoConfirm proves the PARENT-row
// exception: enter on a project/session row expands/drills WITHOUT opening the
// confirm overlay and without calling the focus seam.
func TestNOCFocusGuard_EnterOnParentRowExpandsNoConfirm(t *testing.T) {
	m := newSeededNOCModel(t)
	called := false
	m.switchTo = func(noc.TmuxTarget) error { called = true; return nil }
	m.panes = func() ([]noc.TmuxPane, error) { return nil, nil }
	m.pidTree = func(int) []int { return nil }

	top, ok := m.selectedNode()
	if !ok || top.kind != nodeProject {
		t.Fatalf("expected a project at cursor 0, got %+v ok=%v", top, ok)
	}
	m.tree.setCollapsed(top.id, true)
	before := len(m.nodes())

	m, _ = nocPress(m, "enter")
	if m.jumpPending != nil {
		t.Error("enter on a PARENT row must NOT open the focus confirm overlay")
	}
	if called {
		t.Error("enter on a PARENT row must NOT call the focus seam")
	}
	if m.tree.isCollapsed(top.id) {
		t.Error("enter on a collapsed parent should expand it")
	}
	if got := len(m.nodes()); got <= before {
		t.Errorf("enter on a parent row should expand (more nodes): before=%d after=%d", before, got)
	}
}

// --- QA-2 / QA-4b: confirm guard on o (focus-team) ---------------------------

func TestNOCFocusGuard_OpenTeamConfirmsBeforeFocus(t *testing.T) {
	m := newSeededNOCModel(t)
	switched := false
	m.switchTo = func(noc.TmuxTarget) error { switched = true; return nil }
	m.pidTree = func(int) []int { return nil }

	// Select the first SESSION node so 'o' targets a squad, and provide a pane in
	// THAT session's tmux session so resolveSquadWindow finds a window.
	sessIdx := -1
	var sessName string
	for i, n := range m.nodes() {
		if n.kind == nodeSession {
			sessIdx = i
			sessName = n.session.Name
			break
		}
	}
	if sessIdx < 0 {
		t.Fatal("no session node found")
	}
	m.cursor = sessIdx
	m.panes = func() ([]noc.TmuxPane, error) {
		return []noc.TmuxPane{{Session: sessName, Window: "0", Pane: "0", Command: "claude"}}, nil
	}

	m, _ = nocPress(m, "o")
	if m.jumpPending == nil {
		t.Fatal("o should open the focus confirm overlay")
	}
	if switched {
		t.Fatal("o must NOT focus before confirm")
	}
	if !strings.Contains(m.jumpPending.prompt, "Open/focus squad") {
		t.Errorf("confirm prompt = %q, want it to mention Open/focus squad", m.jumpPending.prompt)
	}

	m, _ = nocPress(m, "esc")
	if m.jumpPending != nil {
		t.Error("esc should close the o confirm overlay")
	}
	if switched {
		t.Error("esc on the o confirm must cancel — seam uncalled")
	}
}

func TestNOCFocusGuard_OpenTeamYConfirmsFocus(t *testing.T) {
	m := newSeededNOCModel(t)
	switched := false
	m.switchTo = func(noc.TmuxTarget) error { switched = true; return nil }
	m.pidTree = func(int) []int { return nil }

	var sessName string
	for i, n := range m.nodes() {
		if n.kind == nodeSession {
			m.cursor = i
			sessName = n.session.Name
			break
		}
	}
	m.panes = func() ([]noc.TmuxPane, error) {
		return []noc.TmuxPane{{Session: sessName, Window: "0", Pane: "0", Command: "claude"}}, nil
	}
	m, _ = nocPress(m, "o")
	if m.jumpPending == nil {
		t.Fatal("o should open the confirm overlay")
	}
	m, _ = nocPress(m, "y")
	if !switched {
		t.Fatal("y on the o confirm should call the focus seam")
	}
}

// --- QA-5: refresh feedback --------------------------------------------------

func TestNOCRefresh_GSetsRefreshNote(t *testing.T) {
	m := newSeededNOCModel(t)
	if m.refreshNote != "" {
		t.Fatalf("refreshNote should start empty, got %q", m.refreshNote)
	}
	m, _ = nocPress(m, "g")
	if m.refreshNote == "" {
		t.Fatal("g should set a visible refresh note")
	}
	if !strings.Contains(m.refreshNote, "refreshed") {
		t.Errorf("refresh note = %q, want it to mention refreshed", m.refreshNote)
	}
	// The note must surface in the footer (the operator must SEE g worked).
	if !strings.Contains(m.View(), m.refreshNote) {
		t.Error("refresh note should render in the footer")
	}
	// It clears on the next keypress.
	m, _ = nocPress(m, "j")
	if m.refreshNote != "" {
		t.Errorf("refresh note should clear on the next keypress, got %q", m.refreshNote)
	}
}

func TestNOCRefresh_SilentTickDoesNotSetRefreshNote(t *testing.T) {
	m := newSeededNOCModel(t)
	// A silent tick (the 2s auto-refresh) must NOT flash the refresh note.
	mm, _ := m.Update(nocTickMsg{})
	m = mm.(*NOCModel)
	if m.refreshNote != "" {
		t.Errorf("a silent tick must NOT set the refresh note, got %q", m.refreshNote)
	}
	// A snapshot landing without a preceding g must also not set it.
	root := m.rebuild.Roots[0]
	ms := noc.Collect([]string{root}, noc.DefaultDepth, m.rebuild.Probe, m.rebuild.Thresholds)
	mm, _ = m.Update(nocSnapshotMsg{ms: ms})
	m = mm.(*NOCModel)
	if m.refreshNote != "" {
		t.Errorf("a silent snapshot must NOT set the refresh note, got %q", m.refreshNote)
	}
}
