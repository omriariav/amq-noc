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
	m.fullTree = true

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
	m.fullTree = false
	// Cursor 0 is the most-urgent project (beta, needs-you), collapsed by
	// default in the live NOC, so the first right/enter expands it.
	top, ok := m.selectedNode()
	if !ok || top.kind != nodeProject {
		t.Fatalf("expected a project at cursor 0, got %+v ok=%v", top, ok)
	}
	before := len(m.nodes())

	// Expand it: descendants appear.
	m, _ = nocPress(m, "right")
	if m.tree.isCollapsed(top.id) {
		t.Errorf("right should expand the project node %q", top.id)
	}
	expanded := len(m.nodes())
	if expanded <= before {
		t.Errorf("expand should increase visible nodes: before=%d after=%d", before, expanded)
	}

	// Collapse it again: descendants disappear.
	m, _ = nocPress(m, "left")
	if !m.tree.isCollapsed(top.id) {
		t.Errorf("left should collapse the project node %q", top.id)
	}
	if got := len(m.nodes()); got != before {
		t.Errorf("collapse should restore node count: before=%d after=%d", before, got)
	}
}

func TestNOCUpdate_ProjectsCollapsedByDefault(t *testing.T) {
	root, probe := seedNOCFixture(t)
	rebuild := NOCRebuildConfig{Roots: []string{root}, Depth: noc.DefaultDepth, Probe: probe}
	ms := noc.Collect(rebuild.Roots, rebuild.Depth, rebuild.Probe, rebuild.Thresholds)

	m := newNOCModel(rebuild)
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	m.ms = ms
	m.ready = true

	ns := m.nodes()
	if len(ns) != 3 {
		t.Fatalf("default live tree should show only collapsed projects, got %d nodes: %+v", len(ns), ns)
	}
	for _, n := range ns {
		if n.kind != nodeProject {
			t.Fatalf("default live tree should contain only project rows, got %+v", ns)
		}
		if n.expanded {
			t.Fatalf("project %q should be collapsed by default", n.label)
		}
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
	m.fullTree = false
	// First right expands the default-collapsed project; second right drills to
	// the first child (next node deeper).
	parent, _ := m.selectedNode()
	m, _ = nocPress(m, "right")
	m, _ = nocPress(m, "right")
	child, ok := m.selectedNode()
	if !ok {
		t.Fatal("no node after drill")
	}
	if child.depth <= parent.depth {
		t.Errorf("right on expanded parent should drill deeper: parent depth=%d child depth=%d", parent.depth, child.depth)
	}
}

// TestNOCUpdate_EnterJumpGuard proves enter on a project row expands/drills and
// never calls the injected switcher (enter is navigation/expand only).
func TestNOCUpdate_EnterJumpGuard(t *testing.T) {
	t.Run("project row expands, never jumps", func(t *testing.T) {
		m := newSeededNOCModel(t)
		m.fullTree = false
		switched := false
		m.switchTo = func(noc.TmuxTarget) error { switched = true; return nil }
		m.panes = func() ([]noc.TmuxPane, error) { return nil, nil }
		m.pidTree = func(int) []int { return nil }

		// Cursor 0 is a project node (beta, the most-urgent squad).
		top, ok := m.selectedNode()
		if !ok || top.kind != nodeProject {
			t.Fatalf("expected a project at cursor 0, got %+v ok=%v", top, ok)
		}
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
