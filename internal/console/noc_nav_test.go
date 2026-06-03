package console

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-noc/internal/noc"
)

// These are the REGRESSION tests for the interactive-navigation-dead bug:
// `amq2 noc` rendered, but arrows / j / k never moved the cursor on the live
// surface. The pre-existing unit tests missed it because they poked the
// movement helpers (moveCursor) directly; the real defect lived in the seam
// between Bubble Tea's event loop and the model — the program was driven as a
// VALUE while the helpers were pointer-receiver, so a keypress mutated a copy
// the loop discarded. The fix made Update / Init / View pointer-receiver and
// drives the program as *NOCModel.
//
// Every test here drives a REAL tea.KeyMsg (or nocSnapshotMsg) through the
// PUBLIC Update and asserts the cursor in the model Update RETURNS — the one
// Bubble Tea renders next. That is the only level at which the bug is visible.

// requireAgentRows fails the test unless the seeded tree has at least n visible
// rows, so the cursor has somewhere to move (a guard the bug also implicated:
// nav looks dead when there are no rows to move through).
func requireRows(t *testing.T, m *NOCModel, n int) {
	t.Helper()
	if got := len(m.nodes()); got < n {
		t.Fatalf("fixture must seed >= %d visible rows, got %d", n, got)
	}
}

// TestNOCNav_DownArrowMovesCursor drives the bare arrow-key path (KeyDown /
// KeyUp) through Update and asserts the RETURNED model's cursor advances, then
// retreats. With the value-receiver-copy bug a KeyDown returned a model whose
// cursor was still 0, so the live surface never moved.
func TestNOCNav_DownArrowMovesCursor(t *testing.T) {
	m := newSeededNOCModel(t)
	requireRows(t, m, 3)
	if m.cursor != 0 {
		t.Fatalf("initial cursor = %d, want 0", m.cursor)
	}

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = m2.(*NOCModel)
	if m.cursor != 1 {
		t.Fatalf("after KeyDown, returned cursor = %d, want 1", m.cursor)
	}

	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = m2.(*NOCModel)
	if m.cursor != 2 {
		t.Fatalf("after second KeyDown, returned cursor = %d, want 2", m.cursor)
	}

	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = m2.(*NOCModel)
	if m.cursor != 1 {
		t.Fatalf("after KeyUp, returned cursor = %d, want 1", m.cursor)
	}
}

// TestNOCNav_JKRunesMoveCursor drives the j / k rune path (KeyRunes) — the same
// keymap entry, but exercised as runes rather than named keys — and asserts the
// returned cursor moves down then up.
func TestNOCNav_JKRunesMoveCursor(t *testing.T) {
	m := newSeededNOCModel(t)
	requireRows(t, m, 3)

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = m2.(*NOCModel)
	if m.cursor != 1 {
		t.Fatalf("after 'j', returned cursor = %d, want 1", m.cursor)
	}

	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = m2.(*NOCModel)
	if m.cursor != 2 {
		t.Fatalf("after second 'j', returned cursor = %d, want 2", m.cursor)
	}

	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	m = m2.(*NOCModel)
	if m.cursor != 1 {
		t.Fatalf("after 'k', returned cursor = %d, want 1", m.cursor)
	}
}

// TestNOCNav_CursorClampsAtEnds proves the returned cursor never escapes the
// visible-row bounds: 'k'/up at the top stays at 0, and 'j'/down past the last
// row stays on the last row.
func TestNOCNav_CursorClampsAtEnds(t *testing.T) {
	m := newSeededNOCModel(t)
	rows := len(m.nodes())
	requireRows(t, m, 3)

	// Up at the very top clamps to 0 (never negative).
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = m2.(*NOCModel)
	if m.cursor != 0 {
		t.Fatalf("KeyUp at top must clamp to 0, got %d", m.cursor)
	}
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	m = m2.(*NOCModel)
	if m.cursor != 0 {
		t.Fatalf("'k' at top must clamp to 0, got %d", m.cursor)
	}

	// Drive down well past the end; it must clamp to the last row, not overflow.
	for i := 0; i < rows+5; i++ {
		m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = m2.(*NOCModel)
	}
	if m.cursor != rows-1 {
		t.Fatalf("KeyDown past the end must clamp to last row %d, got %d", rows-1, m.cursor)
	}
	// One more 'j' stays put.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = m2.(*NOCModel)
	if m.cursor != rows-1 {
		t.Fatalf("'j' at the last row must stay at %d, got %d", rows-1, m.cursor)
	}
}

// TestNOCNav_SnapshotRefreshPreservesCursor moves the cursor, then delivers a
// nocSnapshotMsg (the exact message the 2s refresh tick produces) and asserts
// the RETURNED model still points at the same row — the refresh clamps /
// re-locates the selection, it does NOT reset the cursor to 0. A refresh that
// reset the cursor every poll would also make navigation look dead.
func TestNOCNav_SnapshotRefreshPreservesCursor(t *testing.T) {
	m := newSeededNOCModel(t)
	requireRows(t, m, 3)

	// Move down twice so we are on a non-zero, identifiable row.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = m2.(*NOCModel)
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = m2.(*NOCModel)
	if m.cursor != 2 {
		t.Fatalf("setup: cursor should be 2 before refresh, got %d", m.cursor)
	}
	want, ok := m.selectedNode()
	if !ok {
		t.Fatal("setup: expected a selected node before refresh")
	}

	// Re-collect an identical snapshot and feed it as the refresh tick would.
	root := m.rebuild.Roots[0]
	ms2 := noc.Collect([]string{root}, noc.DefaultDepth, m.rebuild.Probe, m.rebuild.Thresholds)
	m2, _ = m.Update(nocSnapshotMsg{ms: ms2})
	m = m2.(*NOCModel)

	if m.cursor == 0 {
		t.Fatalf("refresh reset the cursor to 0 — selection must be preserved across a snapshot")
	}
	got, ok := m.selectedNode()
	if !ok {
		t.Fatal("no selection after refresh")
	}
	if got.id != want.id {
		t.Fatalf("refresh moved the selection: before %q, after %q", want.id, got.id)
	}
}

// TestNOCNav_KeypressThenRefreshCycle simulates the live loop's steady state: a
// keypress moves the cursor, then the periodic tick fires a snapshot, repeated.
// The cursor must keep advancing — it must not be silently rewound by each
// refresh (the compound symptom the operator saw: keys appear to do nothing
// because every 2s tick yanked the cursor back).
func TestNOCNav_KeypressThenRefreshCycle(t *testing.T) {
	m := newSeededNOCModel(t)
	requireRows(t, m, 3)
	root := m.rebuild.Roots[0]

	for step := 1; step <= 2; step++ {
		m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = m2.(*NOCModel)
		if m.cursor != step {
			t.Fatalf("step %d: cursor should be %d after KeyDown, got %d", step, step, m.cursor)
		}
		// Refresh tick lands.
		ms := noc.Collect([]string{root}, noc.DefaultDepth, m.rebuild.Probe, m.rebuild.Thresholds)
		m2, _ = m.Update(nocSnapshotMsg{ms: ms})
		m = m2.(*NOCModel)
		if m.cursor != step {
			t.Fatalf("step %d: refresh rewound the cursor to %d, want %d", step, m.cursor, step)
		}
	}
}

// TestNOCNav_ViewMarksSelectedRow proves View actually READS m.cursor and marks
// the selected row, so a cursor move is visible to the operator. It renders the
// full-tree board (so the left tree pane is present) and checks that the
// selection marker moves to a different line after a KeyDown.
func TestNOCNav_ViewMarksSelectedRow(t *testing.T) {
	m := newSeededNOCModel(t)
	requireRows(t, m, 3)
	m.fullTree = true // render the expandable tree, where the selection bar lives

	tree0 := m.treeView()
	selLine0 := selectedLineIndex(tree0)
	if selLine0 < 0 {
		t.Fatalf("treeView must mark a selected row at cursor 0:\n%s", tree0)
	}

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = m2.(*NOCModel)
	tree1 := m.treeView()
	selLine1 := selectedLineIndex(tree1)
	if selLine1 < 0 {
		t.Fatalf("treeView must still mark a selected row after KeyDown:\n%s", tree1)
	}
	if selLine1 == selLine0 {
		t.Fatalf("KeyDown must move the rendered selection marker: stayed on line %d", selLine0)
	}
}

// selectedLineIndex returns the index of the first rendered tree line carrying
// the selection marker glyph, or -1 if none. The marker is nocGlyphSelect; in
// ColorNone its ascii form leads the selected row (see renderNode).
func selectedLineIndex(tree string) int {
	marker := nocGlyphSelect.glyph(ColorNone)
	for i, line := range splitLines(tree) {
		if len(line) >= len(marker) && line[:len(marker)] == marker {
			return i
		}
	}
	return -1
}

// selectedLineIndexInView returns the index of the first FULL-VIEW line carrying
// the selection marker. Unlike selectedLineIndex (which scans treeView, where the
// marker leads the line) the live View composes a two-pane layout, so the marker
// leads the LEFT column of a row — i.e. the line STARTS with it. Returns -1 when
// no rendered row is marked (which is exactly what the old staticView digest did:
// it never reads m.cursor, so View carried no selection marker at all).
func selectedLineIndexInView(view string) int {
	marker := nocGlyphSelect.glyph(ColorNone)
	for i, line := range splitLines(view) {
		if strings.HasPrefix(line, marker) {
			return i
		}
	}
	return -1
}

// TestNOCNav_LiveViewRendersMovingSelection is the REGRESSION test that closes
// the test-vs-live gap which let the "live nav does nothing" bug ship: the older
// nav tests poked treeView() (an interactive sub-view) or m.cursor directly, but
// the LIVE program's View() returned staticView() — the cursor-LESS rollup digest
// — so a real keypress changed model state that the rendered frame NEVER reflected.
//
// This drives a real tea.KeyMsg{KeyDown} through the PUBLIC Update on the LIVE
// model (the pointer the event loop renders next) and asserts on View() itself:
//  1. the rendered View string CHANGES after the keypress, and
//  2. the selection marker sits on a LATER rendered row than before.
//
// It FAILS against the old View()->staticView(): the digest carries no selection
// marker (selectedLineIndexInView == -1) and does not change on a cursor move, so
// both assertions trip. (Verified by temporarily restoring View->staticView.)
func TestNOCNav_LiveViewRendersMovingSelection(t *testing.T) {
	m := newSeededNOCModel(t) // colorMode ColorNone, width 120 / height 40 via WindowSizeMsg
	requireRows(t, m, 3)

	view0 := m.View()
	sel0 := selectedLineIndexInView(view0)
	if sel0 < 0 {
		t.Fatalf("live View() must mark the selected row at cursor 0 (the digest never did):\n%s", view0)
	}

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = m2.(*NOCModel)

	view1 := m.View()
	if view1 == view0 {
		t.Fatalf("KeyDown must change the rendered live View() — the live program renders this string, and an unchanged frame is exactly the dead-nav bug")
	}
	sel1 := selectedLineIndexInView(view1)
	if sel1 < 0 {
		t.Fatalf("live View() must still mark a selected row after KeyDown:\n%s", view1)
	}
	if sel1 <= sel0 {
		t.Fatalf("KeyDown must move the rendered selection marker to a LATER row: before line %d, after line %d", sel0, sel1)
	}
}

// TestNOCNav_LiveViewTimelineToggle proves 't' produces a VISIBLE change in the
// LIVE View() (not just in treeView/detailView called directly): toggling the
// timeline must alter the rendered frame. Against the old staticView digest 't'
// flipped m.showTimeline, which the digest never reads, so View() was identical.
func TestNOCNav_LiveViewTimelineToggle(t *testing.T) {
	m := newSeededNOCModel(t)
	requireRows(t, m, 3)

	// Land the cursor on a session node so the detail pane is sessionDetail, which
	// is the pane that honors m.showTimeline.
	target := -1
	for i, n := range m.nodes() {
		if n.kind == nodeSession {
			target = i
			break
		}
	}
	if target < 0 {
		t.Fatal("fixture must seed at least one session node")
	}
	m.cursor = target

	before := m.View()
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	m = m2.(*NOCModel)
	after := m.View()
	if after == before {
		t.Fatalf("'t' must change the live View() (toggle the timeline in the detail pane); an unchanged frame means the live render ignores m.showTimeline")
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
