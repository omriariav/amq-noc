package console

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-noc/internal/noc"
)

// TestNOCFocusGuard_EnterOnParentRowExpandsNoConfirm proves the PARENT-row
// exception: enter on a project/session row expands/drills WITHOUT opening the
// confirm overlay and without calling the focus seam.
func TestNOCFocusGuard_EnterOnParentRowExpandsNoConfirm(t *testing.T) {
	m := newSeededNOCModel(t)
	m.fullTree = false
	called := false
	m.switchTo = func(noc.TmuxTarget) error { called = true; return nil }
	m.panes = func() ([]noc.TmuxPane, error) { return nil, nil }
	m.pidTree = func(int) []int { return nil }

	top, ok := m.selectedNode()
	if !ok || top.kind != nodeProject {
		t.Fatalf("expected a project at cursor 0, got %+v ok=%v", top, ok)
	}
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
