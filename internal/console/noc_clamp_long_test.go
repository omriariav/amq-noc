package console

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/state"
)

// longRowSnapshot builds a one-project/one-session MultiSnapshot whose project
// and session names are deliberately far longer than the left pane width, so the
// rendered left tree row provably exceeds leftW and exercises the clamp. This is
// what makes TestLiveView_LongRowClampedToDivider non-vacuous: with the clamp
// removed, this row overruns the divider; with it, the row is ellipsized and the
// bar stays column-aligned.
func longRowSnapshot() noc.MultiSnapshot {
	longProject := "project-" + strings.Repeat("x", 120) + "-end"
	longSession := "session-" + strings.Repeat("y", 120) + "-end"
	sess := state.Session{
		Name:   longSession,
		Root:   "/fake/" + longProject + "/.agent-mail",
		Agents: []state.Agent{{Handle: "cto", Role: "cto", Engine: "codex", Liveness: state.LivenessAlive}},
	}
	ps := noc.ProjectSnapshot{
		Project: longProject,
		Dir:     "/fake/" + longProject,
		Snap:    state.Snapshot{Sessions: []state.Session{sess}},
	}
	return noc.MultiSnapshot{Roots: []string{"/fake"}, Projects: []noc.ProjectSnapshot{ps}}
}

// TestLiveView_LongRowClampedToDivider is the NON-VACUOUS regression guard for
// the operator-reported overrun: a left tree row whose text exceeds the left
// pane width must be clamped (ellipsized) so the │ divider stays at the same
// visible column on every row. Unlike the short-label fixture, this row is
// guaranteed wider than leftW, so disabling the clamp makes the test fail.
func TestLiveView_LongRowClampedToDivider(t *testing.T) {
	m := newNOCModel(NOCRebuildConfig{Roots: []string{"/fake"}})
	m.colorMode = ColorNone // ANSI-free output makes column math exact.
	m.th = newNOCTheme(ColorNone)
	m.ms = longRowSnapshot()
	m.ready = true
	m.refreshGuidance()
	m.width = 160
	m.height = 40

	leftW := m.leftWidth()
	if leftW <= 0 {
		t.Fatalf("expected a positive left width at width=160, got %d", leftW)
	}

	frame := m.liveView()
	lines := strings.Split(frame, "\n")

	// Sanity: the UNCLAMPED project label really is wider than the left pane, so
	// this fixture genuinely exercises the clamp (not a fits-anyway no-op).
	if got := visibleWidth(longRowSnapshot().Projects[0].Project); got <= leftW {
		t.Fatalf("test setup vacuous: long project label width %d <= leftW %d", got, leftW)
	}

	var cols []int
	sawEllipsis := false
	for _, ln := range lines {
		c := dividerColumnVisible(ln)
		if c < 0 {
			continue
		}
		cols = append(cols, c)
		// No left segment may reach or cross the divider. Trim the trailing gutter
		// space (the clamp pads to leftW then the " │ " gutter follows), measuring
		// the same way the alignment test does.
		idx := strings.IndexAny(ln, "│|")
		if seg := strings.TrimRight(ln[:idx], " "); visibleWidth(seg) > leftW {
			t.Fatalf("left segment exceeds left width %d (overran the divider): %q", leftW, seg)
		}
		if strings.Contains(ln[:idx], "…") {
			sawEllipsis = true
		}
	}
	if len(cols) < 1 {
		t.Fatalf("expected at least one composed two-pane row with a divider:\n%s", frame)
	}
	// Every divider bar at the same visible column — the alignment the bug broke.
	want := cols[0]
	for i, c := range cols {
		if c != want {
			t.Fatalf("divider misaligned on row %d: got col %d want %d\n%s", i, c, want, frame)
		}
	}
	// The long row must have been ellipsized (proves the clamp actually fired).
	if !sawEllipsis {
		t.Fatalf("expected the over-long left row to be ellipsized with …, frame:\n%s", frame)
	}
}
