package console

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-noc/internal/noc"
)

// newSeededNOCModel builds a model over the three-project fixture, drives a
// window-size + snapshot message into it, and returns the ready model. It
// returns a *NOCModel: the program (and these tests) drive the model as a
// pointer so a key handler's cursor / collapse / filter mutation lands on the
// SAME model the event loop renders next (Update / Init / View are
// pointer-receiver). Driving a value here would mutate a copy and never reflect
// a keypress — the exact live-nav-dead bug these tests guard against.
func newSeededNOCModel(t *testing.T) *NOCModel {
	t.Helper()
	root, probe := seedNOCFixture(t)
	rebuild := NOCRebuildConfig{Roots: []string{root}, Depth: noc.DefaultDepth, Probe: probe}
	ms := noc.Collect(rebuild.Roots, rebuild.Depth, rebuild.Probe, rebuild.Thresholds)

	m := newNOCModel(rebuild)
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)

	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m2 := mm.(*NOCModel)
	mm, _ = m2.Update(nocSnapshotMsg{ms: ms})
	return mm.(*NOCModel)
}

func nocKey(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// nocPress drives one real key message through the PUBLIC Update and returns the
// model Update RETURNS — the one Bubble Tea renders next. It deliberately does
// NOT call moveCursor / handleKey directly: a test that pokes the helpers would
// pass even with the value-receiver-copy bug, where Update mutates a throwaway
// copy. Threading the returned *NOCModel is what makes these tests catch a dead
// arrow / j / k key.
func nocPress(m *NOCModel, s string) (*NOCModel, tea.Cmd) {
	mm, cmd := m.Update(nocKey(s))
	return mm.(*NOCModel), cmd
}

func TestNOCUpdate_MoveCursor(t *testing.T) {
	m := newSeededNOCModel(t)
	if m.cursor != 0 {
		t.Fatalf("initial cursor = %d, want 0", m.cursor)
	}
	m, _ = nocPress(m, "j")
	if m.cursor != 1 {
		t.Errorf("after j, cursor = %d, want 1", m.cursor)
	}
	m, _ = nocPress(m, "k")
	if m.cursor != 0 {
		t.Errorf("after k, cursor = %d, want 0", m.cursor)
	}
	// Up at the top clamps.
	m, _ = nocPress(m, "up")
	if m.cursor != 0 {
		t.Errorf("up at top should clamp to 0, got %d", m.cursor)
	}
}

func TestNOCUpdate_DownKeepsCursorVisibleInTreeWindow(t *testing.T) {
	m := newLongTreeNOCModel(t, 12, 12)
	if got := m.bodyHeight(); got != 4 {
		t.Fatalf("test expects a four-row tree window, got %d", got)
	}
	if strings.Contains(m.treeView(), "proj-08") {
		t.Fatal("test setup should start above proj-08")
	}

	for i := 0; i < 8; i++ {
		m, _ = nocPress(m, "down")
	}

	if m.cursor != 8 {
		t.Fatalf("cursor = %d, want 8", m.cursor)
	}
	if m.scroll != 5 {
		t.Fatalf("scroll = %d, want 5", m.scroll)
	}
	view := m.treeView()
	if !strings.Contains(view, "proj-08") {
		t.Fatalf("selected row should be visible after scrolling down:\n%s", view)
	}
	if strings.Contains(view, "proj-00") {
		t.Fatalf("top rows should scroll out of view:\n%s", view)
	}
	if got := len(strings.Split(view, "\n")); got != 4 {
		t.Fatalf("tree window rendered %d rows, want 4:\n%s", got, view)
	}
}

func TestNOCUpdate_CollapseAndExpand(t *testing.T) {
	m := newSeededNOCModel(t)
	// Cursor 0 is the most-urgent project (beta, needs-you), expanded by default,
	// so its session/agent descendants follow it.
	top, ok := m.selectedNode()
	if !ok || top.kind != nodeProject {
		t.Fatalf("expected a project at cursor 0, got %+v ok=%v", top, ok)
	}
	before := len(m.nodes())

	// Collapse it: its descendants disappear.
	m, _ = nocPress(m, "left")
	if !m.tree.isCollapsed(top.id) {
		t.Errorf("left should collapse the project node %q", top.id)
	}
	if got := len(m.nodes()); got >= before {
		t.Errorf("collapse should reduce visible nodes: before=%d after=%d", before, got)
	}

	// Expand it again: descendants return.
	m, _ = nocPress(m, "right")
	if m.tree.isCollapsed(top.id) {
		t.Errorf("right should re-expand the project node %q", top.id)
	}
	if got := len(m.nodes()); got != before {
		t.Errorf("re-expand should restore node count: before=%d after=%d", before, got)
	}
}

func newLongTreeNOCModel(t *testing.T, count, height int) *NOCModel {
	t.Helper()
	root := "/fake/root"
	projects := make([]noc.ProjectSnapshot, 0, count)
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("proj-%02d", i)
		projects = append(projects, noc.ProjectSnapshot{
			Project:   name,
			Dir:       root + "/" + name,
			Candidate: true,
		})
	}
	ms := noc.MultiSnapshot{Roots: []string{root}, Projects: projects}
	m := newNOCModel(NOCRebuildConfig{Roots: []string{root}, Depth: noc.DefaultDepth})
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: height})
	m2 := mm.(*NOCModel)
	mm, _ = m2.Update(nocSnapshotMsg{ms: ms})
	return mm.(*NOCModel)
}

func TestNOCUpdate_DrillIntoChild(t *testing.T) {
	m := newSeededNOCModel(t)
	// On an expanded parent, right drills to the first child (next node deeper).
	parent, _ := m.selectedNode()
	m, _ = nocPress(m, "right")
	child, ok := m.selectedNode()
	if !ok {
		t.Fatal("no node after drill")
	}
	if child.depth <= parent.depth {
		t.Errorf("right on expanded parent should drill deeper: parent depth=%d child depth=%d", parent.depth, child.depth)
	}
}

func TestNOCUpdate_JumpCallsSwitcherWithResolvedTarget(t *testing.T) {
	m := newSeededNOCModel(t)

	// Find beta's project dir (the alive claude agent lives there).
	var beta noc.ProjectSnapshot
	for _, n := range m.nodes() {
		if n.kind == nodeProject && n.label == "beta" {
			beta = n.project
			break
		}
	}
	if beta.Dir == "" {
		t.Fatal("beta project not found")
	}

	var gotTarget noc.TmuxTarget
	called := false
	m.switchTo = func(tt noc.TmuxTarget) error { called = true; gotTarget = tt; return nil }
	m.panes = func() ([]noc.TmuxPane, error) {
		// A pane in beta's dir running claude resolves the beta/qa agent.
		return []noc.TmuxPane{{
			Session: "beta", Window: "0", Pane: "1", PID: 5001,
			Command: "claude", CWD: beta.Dir,
		}}, nil
	}
	m.pidTree = func(int) []int { return nil }

	// Navigate to beta's qa agent node and press J.
	agentIdx := -1
	for i, n := range m.nodes() {
		if n.kind == nodeAgent && n.agent.Handle == "qa" {
			agentIdx = i
			break
		}
	}
	if agentIdx < 0 {
		t.Fatalf("qa agent node not found; nodes=%d", len(m.nodes()))
	}
	m.cursor = agentIdx
	// J is now confirm-gated (QA-2/QA-4b): it opens the read-only focus confirm
	// overlay; the switcher fires only after y/enter.
	m, _ = nocPress(m, "J")
	if m.jumpPending == nil {
		t.Fatal("J on a running agent should open the focus confirm overlay")
	}
	if called {
		t.Fatal("J must NOT call the switcher before confirm")
	}
	m, _ = nocPress(m, "y")

	if !called {
		t.Fatal("y on the focus confirm should call the injected switcher")
	}
	if gotTarget.Session != "beta" || gotTarget.Window != "0" || gotTarget.Pane != "1" {
		t.Errorf("switcher got wrong target: %+v", gotTarget)
	}
}

func TestNOCUpdate_JumpNoPaneShowsSuggestionNotError(t *testing.T) {
	m := newSeededNOCModel(t)
	called := false
	m.switchTo = func(noc.TmuxTarget) error { called = true; return nil }
	m.panes = func() ([]noc.TmuxPane, error) { return nil, nil } // no panes resolve
	m.pidTree = func(int) []int { return nil }

	agentIdx := -1
	for i, n := range m.nodes() {
		if n.kind == nodeAgent && n.agent.Handle == "qa" {
			agentIdx = i
			break
		}
	}
	if agentIdx < 0 {
		t.Fatal("qa agent node not found")
	}
	m.cursor = agentIdx
	// J opens the confirm overlay; confirm with y, then the unresolved jump sets a
	// guidance note (and the switcher stays uncalled).
	m, _ = nocPress(m, "J")
	if m.jumpPending == nil {
		t.Fatal("J should open the focus confirm overlay")
	}
	m, _ = nocPress(m, "y")

	if called {
		t.Error("switcher must NOT be called when no pane resolves")
	}
	if m.jumpNote == "" {
		t.Error("an unresolved jump should set a guidance jumpNote, not error out")
	}
}

func TestNOCUpdate_JumpNotInTmuxShowsSuggestion(t *testing.T) {
	m := newSeededNOCModel(t)
	var beta noc.ProjectSnapshot
	for _, n := range m.nodes() {
		if n.kind == nodeProject && n.label == "beta" {
			beta = n.project
		}
	}
	m.switchTo = func(tt noc.TmuxTarget) error {
		return &noc.NotInTmuxError{Target: tt, Command: noc.SuggestJump(tt)}
	}
	m.panes = func() ([]noc.TmuxPane, error) {
		return []noc.TmuxPane{{Session: "beta", Window: "0", Pane: "1", PID: 5001, Command: "claude", CWD: beta.Dir}}, nil
	}
	m.pidTree = func(int) []int { return nil }

	for i, n := range m.nodes() {
		if n.kind == nodeAgent && n.agent.Handle == "qa" {
			m.cursor = i
		}
	}
	// J opens the confirm overlay; confirm with y, then the not-in-tmux switch
	// surfaces the suggested command.
	m, _ = nocPress(m, "J")
	if m.jumpPending == nil {
		t.Fatal("J should open the focus confirm overlay")
	}
	m, _ = nocPress(m, "y")
	if m.jumpNote == "" || !strings.Contains(m.jumpNote, "tmux switch-client") {
		t.Errorf("not-in-tmux jump should surface the suggested command, got %q", m.jumpNote)
	}
}

// TestNOCUpdate_EnterJumpGuard proves the jump guard (Codex): enter JUMPS only
// on a RUNNING-AGENT row. On a project row it expands/drills and never calls the
// injected switcher; on a running-agent row it calls the switcher with the right
// target; on a STOPPED agent it does NOT jump and leaves a note.
func TestNOCUpdate_EnterJumpGuard(t *testing.T) {
	t.Run("project row expands, never jumps", func(t *testing.T) {
		m := newSeededNOCModel(t)
		switched := false
		m.switchTo = func(noc.TmuxTarget) error { switched = true; return nil }
		m.panes = func() ([]noc.TmuxPane, error) { return nil, nil }
		m.pidTree = func(int) []int { return nil }

		// Cursor 0 is a project node (beta, the most-urgent squad).
		top, ok := m.selectedNode()
		if !ok || top.kind != nodeProject {
			t.Fatalf("expected a project at cursor 0, got %+v ok=%v", top, ok)
		}
		// Collapse it first so enter has a visible expand effect to assert.
		m.tree.setCollapsed(top.id, true)
		before := len(m.nodes())

		m, _ = nocPress(m, "enter")
		if switched {
			t.Error("enter on a project row must NOT call the switcher (no teleport into tmux)")
		}
		if m.tree.isCollapsed(top.id) {
			t.Error("enter on a collapsed project row should expand it")
		}
		if got := len(m.nodes()); got <= before {
			t.Errorf("enter on a project row should expand (more visible nodes): before=%d after=%d", before, got)
		}
	})

	t.Run("running-agent row jumps to resolved target", func(t *testing.T) {
		m := newSeededNOCModel(t)
		var beta noc.ProjectSnapshot
		for _, n := range m.nodes() {
			if n.kind == nodeProject && n.label == "beta" {
				beta = n.project
				break
			}
		}
		if beta.Dir == "" {
			t.Fatal("beta project not found")
		}
		var gotTarget noc.TmuxTarget
		called := false
		m.switchTo = func(tt noc.TmuxTarget) error { called = true; gotTarget = tt; return nil }
		m.panes = func() ([]noc.TmuxPane, error) {
			return []noc.TmuxPane{{
				Session: "beta", Window: "0", Pane: "1", PID: 5001,
				Command: "claude", CWD: beta.Dir,
			}}, nil
		}
		m.pidTree = func(int) []int { return nil }

		// beta/qa is the alive claude agent (canJump).
		for i, n := range m.nodes() {
			if n.kind == nodeAgent && n.agent.Handle == "qa" {
				if !n.canJump {
					t.Fatal("beta/qa should be a running (canJump) agent")
				}
				m.cursor = i
				break
			}
		}
		// enter on a running-agent row now opens the focus confirm overlay
		// (QA-2/QA-4b); the switcher fires only after y/enter confirms.
		m, _ = nocPress(m, "enter")
		if m.jumpPending == nil {
			t.Fatal("enter on a running-agent row should open the focus confirm overlay")
		}
		if called {
			t.Fatal("enter on a running-agent row must NOT call the switcher before confirm")
		}
		m, _ = nocPress(m, "y")
		if !called {
			t.Fatal("y on the focus confirm must call the switcher")
		}
		if gotTarget.Session != "beta" || gotTarget.Window != "0" || gotTarget.Pane != "1" {
			t.Errorf("switcher got wrong target: %+v", gotTarget)
		}
	})

	t.Run("stopped-agent row does not jump, shows a note", func(t *testing.T) {
		m := newSeededNOCModel(t)
		switched := false
		m.switchTo = func(noc.TmuxTarget) error { switched = true; return nil }
		m.panes = func() ([]noc.TmuxPane, error) {
			return nil, nil
		}
		m.pidTree = func(int) []int { return nil }

		// gamma/dev is the dead agent (stopped, !canJump). Find it (expanding
		// gamma if the tree collapsed it).
		stoppedIdx := -1
		for i, n := range m.nodes() {
			if n.kind == nodeAgent && n.agent.Handle == "dev" {
				stoppedIdx = i
				break
			}
		}
		if stoppedIdx < 0 {
			// gamma may be collapsed by default order; select + expand it.
			for i, n := range m.nodes() {
				if n.kind == nodeProject && n.label == "gamma" {
					m.cursor = i
					m.tree.setCollapsed(n.id, false)
					break
				}
			}
			for i, n := range m.nodes() {
				if n.kind == nodeAgent && n.agent.Handle == "dev" {
					stoppedIdx = i
					break
				}
			}
		}
		if stoppedIdx < 0 {
			t.Fatalf("gamma/dev stopped agent node not found in %d nodes", len(m.nodes()))
		}
		sel := m.nodes()[stoppedIdx]
		if sel.canJump {
			t.Fatal("gamma/dev should be a stopped (!canJump) agent")
		}
		m.cursor = stoppedIdx
		m, _ = nocPress(m, "enter")
		if switched {
			t.Error("enter on a STOPPED agent row must NOT jump")
		}
		if m.jumpNote == "" {
			t.Error("enter on a STOPPED agent row should leave a note explaining nothing is live to jump to")
		}
	})
}

func TestNOCUpdate_FilterRouting(t *testing.T) {
	m := newSeededNOCModel(t)
	// Open the filter editor and type "needs-you".
	m, _ = nocPress(m, "/")
	if !m.filterEditing {
		t.Fatal("/ should open the filter editor")
	}
	for _, ch := range "needs-you" {
		m, _ = nocPress(m, string(ch))
	}
	if m.filter != "needs-you" {
		t.Fatalf("filter text = %q, want needs-you", m.filter)
	}
	m, _ = nocPress(m, "enter")
	if m.filterEditing {
		t.Error("enter should close the filter editor")
	}
	// Only beta (the needs-you project) survives the filter.
	for _, n := range m.nodes() {
		if n.kind == nodeProject && n.label != "beta" {
			t.Errorf("needs-you filter should drop project %q", n.label)
		}
	}
	// esc clears the filter.
	m, _ = nocPress(m, "esc")
	if m.filter != "" {
		t.Errorf("esc should clear the filter, got %q", m.filter)
	}
}

func TestNOCFilterBareProjectKeepsChildRows(t *testing.T) {
	m := newSeededNOCModel(t)
	m.filter = "beta"

	var projects, sessions, agents []string
	for _, n := range m.nodes() {
		switch n.kind {
		case nodeProject:
			projects = append(projects, n.label)
		case nodeSession:
			sessions = append(sessions, n.session.Name)
		case nodeAgent:
			agents = append(agents, n.agent.Handle)
		}
	}
	if strings.Join(projects, ",") != "beta" {
		t.Fatalf("project rows = %v, want beta", projects)
	}
	if len(sessions) != 1 || sessions[0] != "main" {
		t.Fatalf("bare project filter should keep beta session, got %v", sessions)
	}
	if len(agents) != 1 || agents[0] != "qa" {
		t.Fatalf("bare project filter should keep beta agent, got %v", agents)
	}
}

func TestNOCUpdate_QuitKey(t *testing.T) {
	m := newSeededNOCModel(t)
	_, cmd := nocPress(m, "q")
	if cmd == nil {
		t.Fatal("q should return a command (tea.Quit)")
	}
	if msg := cmd(); msg == nil {
		t.Error("q's command should produce a quit message")
	}
}

func TestNOCUpdate_HelpToggle(t *testing.T) {
	m := newSeededNOCModel(t)
	m, _ = nocPress(m, "?")
	if !m.showHelp {
		t.Fatal("? should open help")
	}
	if !strings.Contains(m.View(), "help") {
		t.Errorf("help view should render help text:\n%s", m.View())
	}
	m, _ = nocPress(m, "x") // any key dismisses help
	if m.showHelp {
		t.Error("a key should dismiss help")
	}
}

func TestNOCUpdate_SelectionStableAcrossSnapshotReplacement(t *testing.T) {
	m := newSeededNOCModel(t)
	// Move to a deeper, identifiable node (beta's qa agent).
	for i, n := range m.nodes() {
		if n.kind == nodeAgent && n.agent.Handle == "qa" {
			m.cursor = i
			m.rememberSelection()
		}
	}
	wantID := m.selectedID
	if wantID == "" {
		t.Fatal("expected a remembered selection id")
	}

	// Replace the snapshot with a freshly-collected (identical) one.
	root := m.rebuild.Roots[0]
	ms2 := noc.Collect([]string{root}, noc.DefaultDepth, m.rebuild.Probe, m.rebuild.Thresholds)
	mm, _ := m.Update(nocSnapshotMsg{ms: ms2})
	m = mm.(*NOCModel)

	sel, ok := m.selectedNode()
	if !ok {
		t.Fatal("no selection after snapshot replacement")
	}
	if sel.id != wantID {
		t.Errorf("selection not stable across snapshot: want %q, got %q", wantID, sel.id)
	}
}

func TestNOCUpdate_ReadOnlyNoMutatingKeys(t *testing.T) {
	// READ-ONLY contract: the well-known mutating letters (s/d/m/x/a/D) must be
	// inert — none may change the snapshot, and none may trigger the tmux switch
	// (the only side effect is the explicit jump on J/enter).
	m := newSeededNOCModel(t)
	switched := false
	m.switchTo = func(noc.TmuxTarget) error { switched = true; return nil }
	before := len(m.ms.Projects)
	for _, k := range []string{"s", "d", "m", "x", "a", "D"} {
		m, _ = nocPress(m, k)
	}
	if switched {
		t.Error("no plain letter key may trigger the tmux switch")
	}
	if len(m.ms.Projects) != before {
		t.Error("no key may change the snapshot (read-only contract)")
	}
}
