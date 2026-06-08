package console

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-noc/internal/noc"
)

// dividerColumnVisible returns the VISIBLE column (ANSI-stripped) of the first
// " │ " / " | " divider on a composed live-frame line, or -1 if the line has no
// divider. It walks the same way visibleWidth counts so a row carrying color
// codes still reports the true on-screen column of the divider.
func dividerColumnVisible(line string) int {
	col := 0
	inEsc := false
	for _, r := range line {
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == '\x1b' {
			inEsc = true
			continue
		}
		if r == '│' || r == '|' {
			return col
		}
		col++
	}
	return -1
}

// TestTruncateVisibleEllipsis_ClampsAndEllipsizes is the unit-level guard for
// the helper the left-pane clamp relies on: a too-wide row is shortened to the
// budget and ends in the ellipsis, a fitting row is returned untouched, and the
// ascii mode uses "..." instead of "…". Non-vacuous: the long input is provably
// wider than the budget before truncation.
func TestTruncateVisibleEllipsis_ClampsAndEllipsizes(t *testing.T) {
	const w = 20
	long := "▸ ○ stopped pm-context (12 blocked stale, 6 at-risk stale)  Main promotion complete: v3.12.0-alpha.1"
	if visibleWidth(long) <= w {
		t.Fatalf("test setup vacuous: long row already fits (%d <= %d)", visibleWidth(long), w)
	}

	got := truncateVisibleEllipsis(long, w, false)
	if visibleWidth(got) > w {
		t.Fatalf("clamped width %d exceeds budget %d: %q", visibleWidth(got), w, got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("clamped row must end with ellipsis, got %q", got)
	}
	// Leading glyphs/state survive — only the trailing brief is cut.
	if !strings.HasPrefix(got, "▸ ○ stopped") {
		t.Fatalf("leading glyph/state must survive truncation, got %q", got)
	}

	// Fits: unchanged.
	short := "▸ ● short"
	if out := truncateVisibleEllipsis(short, w, false); out != short {
		t.Fatalf("fitting row must pass through unchanged, got %q", out)
	}

	// ASCII mode uses "..." and never emits unicode ellipsis.
	gotA := truncateVisibleEllipsis(long, w, true)
	if visibleWidth(gotA) > w {
		t.Fatalf("ascii clamped width %d exceeds budget %d: %q", visibleWidth(gotA), w, gotA)
	}
	if !strings.HasSuffix(gotA, "...") {
		t.Fatalf("ascii clamped row must end with ..., got %q", gotA)
	}
	if strings.Contains(gotA, "…") {
		t.Fatalf("ascii mode must not emit unicode ellipsis, got %q", gotA)
	}
}

// TestLiveView_LeftRowsClampedToDivider is the regression test for the
// operator-reported overrun: in the live two-pane frame the │ divider must land
// at the SAME visible column on every body row, sit strictly right of the left
// pane (no left row reaches or crosses it), and no left segment may exceed the
// left pane width.
func TestLiveView_LeftRowsClampedToDivider(t *testing.T) {
	root, probe := seedNOCFixture(t)
	rebuild := NOCRebuildConfig{Roots: []string{root}, Depth: noc.DefaultDepth, Probe: probe}
	ms := noc.Collect(rebuild.Roots, rebuild.Depth, rebuild.Probe, rebuild.Thresholds)

	m := newNOCModel(rebuild)
	m.colorMode = ColorNone // ANSI-free output makes column math exact.
	m.th = newNOCTheme(ColorNone)
	m.ms = ms
	m.ready = true
	m.refreshGuidance()
	// A real terminal size so liveView() takes the two-pane (not stacked) path.
	m.width = 160
	m.height = 40

	frame := m.liveView()
	lines := strings.Split(frame, "\n")

	leftW := m.leftWidth()
	if leftW <= 0 {
		t.Fatalf("expected a positive left width at width=160, got %d", leftW)
	}

	// Collect the visible divider column of every body row that has one.
	var cols []int
	for _, ln := range lines {
		if c := dividerColumnVisible(ln); c >= 0 {
			cols = append(cols, c)
		}
	}
	if len(cols) < 2 {
		t.Fatalf("expected at least 2 composed two-pane rows with a divider, got %d:\n%s", len(cols), frame)
	}

	// (a) Every divider bar sits at exactly the same visible column — the
	// alignment guarantee the overrun bug broke (a long left row shoved the bar
	// right on its row only).
	want := cols[0]
	for i, c := range cols {
		if c != want {
			t.Fatalf("divider misaligned on row %d: got col %d want %d\n%s", i, c, want, frame)
		}
	}

	// (b) The bar sits at the gutter (just RIGHT of the left pane), never inside
	// it: with the " │ " gutter the bar is one space past the padded left column,
	// so its column is strictly greater than leftW. No left row crosses the bar.
	if want <= leftW {
		t.Fatalf("divider column %d is not right of left pane width %d (a row overran)\n%s", want, leftW, frame)
	}

	// (c) No left segment (visible text before the bar) exceeds the left width.
	for i, ln := range lines {
		idx := strings.IndexAny(ln, "│|")
		if idx < 0 {
			continue
		}
		leftCells := visibleWidth(strings.TrimRight(ln[:idx], " "))
		if leftCells > leftW {
			t.Fatalf("left segment on row %d is %d cells, exceeds left width %d (overran the divider):\n%q",
				i, leftCells, leftW, ln)
		}
	}
}

func TestLiveView_RightPaneGetsMajorityOnWideTerminal(t *testing.T) {
	m := newNOCModel(NOCRebuildConfig{})
	m.width = 200
	leftW := m.leftWidth()
	rightW := m.width - leftW - 3
	if rightW <= leftW {
		t.Fatalf("wide terminal should allocate more columns to detail pane: left=%d right=%d", leftW, rightW)
	}
	if rightW < 125 {
		t.Fatalf("wide terminal detail pane too narrow: left=%d right=%d", leftW, rightW)
	}
}
