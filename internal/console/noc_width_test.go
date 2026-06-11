package console

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-noc/internal/state"
)

// #17 narrow-terminal validation: at 80 columns no rendered line of the main
// board or the conversation frame may exceed the terminal width (overflow
// lines wrap mid-glyph and shred the layout).

func assertLinesFit(t *testing.T, view string, width int, surface string) {
	t.Helper()
	for _, line := range strings.Split(view, "\n") {
		if w := visibleWidth(line); w > width {
			t.Fatalf("%s: line %d cols wide exceeds %d:\n%q", surface, w, width, line)
		}
	}
}

func TestBoardFitsNarrowTerminal(t *testing.T) {
	m := conversationTestModel(t, state.LivenessStale, nil)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = mm.(*NOCModel)
	assertLinesFit(t, m.View(), 80, "board at 80 cols")
}

func TestConversationFitsNarrowTerminal(t *testing.T) {
	base := time.Date(2026, 6, 11, 16, 0, 0, 0, time.UTC)
	long := strings.Repeat("a long body line that must wrap not overflow ", 6)
	msgs := append(conversationMsgs(base), state.Message{
		ID: "l1", From: "copilot", To: []string{"user"}, Thread: "p2p/copilot__user",
		Subject: strings.Repeat("very long subject ", 10), Kind: state.KindStatus,
		Body: long + "\n" + long, Created: base.Add(20 * time.Minute),
	})
	m := conversationTestModel(t, state.LivenessAlive, msgs)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = mm.(*NOCModel)
	selectKind(t, m, nodeSession, "")
	m, _ = nocPress(m, "m")
	assertLinesFit(t, m.View(), 80, "conversation at 80 cols")
}

// The lead-down signal lives in the FULL-WIDTH header pulse: narrow left-pane
// rows truncate before a session tail could show it.
func TestLeadDownCountInHeaderPulse(t *testing.T) {
	m := conversationTestModel(t, state.LivenessStale, nil)
	if view := m.View(); !strings.Contains(view, "1 lead-down") {
		t.Fatalf("header pulse should count lead-down squads:\n%s", view)
	}
	healthy := conversationTestModel(t, state.LivenessAlive, nil)
	if view := healthy.View(); strings.Contains(view, "lead-down") {
		t.Fatalf("healthy lead must keep the pulse quiet:\n%s", view)
	}
}
