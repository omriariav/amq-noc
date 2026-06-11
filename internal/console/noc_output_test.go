package console

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-noc/internal/act"
	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/state"
)

// #21 regression set: the message flow gains a kind selector sourced from
// state.KnownKinds, and the v key shows the selected agent's latest output
// (read-only pane tail via the published pane id, else its newest AMQ
// message), labeled with its source.

func TestMessageKindSelectorChoosesKind(t *testing.T) {
	m := newControlModel(t)
	selectKind(t, m, nodeAgent, "qa")
	var got act.OpMessage
	m.sendOp = func(op act.OpMessage) error { got = op; return nil }

	m, _ = nocPress(m, "m")
	for _, ch := range "question" {
		m, _ = nocPress(m, string(ch))
	}
	m, _ = nocPress(m, "enter")
	if m.input == nil || m.input.stage != 1 {
		t.Fatalf("kind choice should advance to body, input=%+v note=%q", m.input, m.actNote)
	}
	for _, ch := range "which store?" {
		m, _ = nocPress(m, string(ch))
	}
	m, _ = nocPress(m, "enter")
	if m.pending == nil {
		t.Fatal("expected confirm overlay")
	}
	if !strings.Contains(m.pending.preview, "--kind question") {
		t.Fatalf("preview = %q, want --kind question", m.pending.preview)
	}
	m, _ = nocPress(m, "y")
	if got.Kind != "question" {
		t.Fatalf("sent kind = %q, want question", got.Kind)
	}
}

func TestMessageKindSelectorRejectsUnknown(t *testing.T) {
	m := newControlModel(t)
	selectKind(t, m, nodeAgent, "qa")
	m, _ = nocPress(m, "m")
	for _, ch := range "bogus" {
		m, _ = nocPress(m, string(ch))
	}
	m, _ = nocPress(m, "enter")
	if m.input == nil || m.input.stage != 0 {
		t.Fatalf("unknown kind must keep the selector open, input=%+v", m.input)
	}
	if !strings.Contains(m.actNote, "unknown kind") || !strings.Contains(m.actNote, "review_request") {
		t.Fatalf("note = %q, want unknown-kind guidance listing valid kinds", m.actNote)
	}
}

func TestKnownKindsStatusFirst(t *testing.T) {
	kinds := state.KnownKinds()
	if len(kinds) != 7 || kinds[0] != state.KindStatus {
		t.Fatalf("KnownKinds = %v, want 7 kinds with status first", kinds)
	}
}

// outputTestModel mirrors newControlModel but with caller-provided threads so
// the AMQ fallback (LastFrom-based) is deterministic.
func outputTestModel(t *testing.T, threads []state.ThreadSummary) *NOCModel {
	t.Helper()
	sess := state.Session{
		Name: "beta",
		Root: "/fake/root",
		Agents: []state.Agent{
			{Handle: "qa", Role: "qa", Engine: "claude", Liveness: state.LivenessAlive},
		},
		Coordination: state.Coordination{Threads: threads},
	}
	ps := noc.ProjectSnapshot{
		Project:        "beta",
		Dir:            "/fake/proj/beta",
		TeamConfigured: true,
		DefaultTeam:    true,
		Profiles:       []string{"default"},
		Snap:           state.Snapshot{Sessions: []state.Session{sess}},
	}
	ms := noc.MultiSnapshot{
		Roots:      []string{"/fake/proj"},
		Projects:   []noc.ProjectSnapshot{ps},
		ObservedAt: time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC),
	}
	m := newNOCModel(NOCRebuildConfig{Roots: []string{"/fake/proj"}})
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	m.fullTree = true
	m.sendOp = func(act.OpMessage) error { return nil }
	m.panes = func() ([]noc.TmuxPane, error) { return nil, nil }
	m.switchTo = func(noc.TmuxTarget) error { return nil }
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m2 := mm.(*NOCModel)
	mm, _ = m2.Update(nocSnapshotMsg{ms: ms})
	return mm.(*NOCModel)
}

func TestViewOutputPaneTail(t *testing.T) {
	m := outputTestModel(t, nil)
	m.runtimeFetch = func(dir, profile, session string) noc.RuntimeStatus {
		return noc.RuntimeStatus{
			Advertised: true,
			Members: []noc.RuntimeMember{
				{Role: "qa", Handle: "qa", PaneID: "%7", PaneAlive: true},
			},
		}
	}
	var capturedPane string
	m.paneCapture = func(paneID string, lines int) ([]string, error) {
		capturedPane = paneID
		return []string{"running tests...", "ok internal/state"}, nil
	}
	selectKind(t, m, nodeAgent, "qa")
	m, _ = nocPress(m, "v")
	if capturedPane != "%7" {
		t.Fatalf("captured pane = %q, want the published %%7", capturedPane)
	}
	if m.agentOutput == nil || !strings.Contains(m.agentOutput.source, "%7") {
		t.Fatalf("agentOutput = %+v, want pane source label", m.agentOutput)
	}
	n, _ := m.selectedNode()
	view := m.agentDetail(n)
	if !strings.Contains(view, "latest output") || !strings.Contains(view, "ok internal/state") {
		t.Fatalf("agent detail missing output preview:\n%s", view)
	}
	if !strings.Contains(view, "read-only") {
		t.Fatalf("output source must be labeled read-only:\n%s", view)
	}
}

func TestViewOutputAMQFallbackWhenNoPane(t *testing.T) {
	base := time.Date(2026, 6, 11, 7, 30, 0, 0, time.UTC)
	m := outputTestModel(t, []state.ThreadSummary{
		{
			ID:           "p2p/copilot__qa",
			Participants: []string{"copilot", "qa"},
			Subject:      "progress",
			LastFrom:     "qa",
			LatestBody:   "tests are green\nmoving to docs",
			LastEventAt:  base,
		},
	})
	m.runtimeFetch = func(dir, profile, session string) noc.RuntimeStatus { return noc.RuntimeStatus{} }
	m.paneCapture = func(string, int) ([]string, error) {
		t.Fatal("no live pane: capture must not run")
		return nil, nil
	}
	selectKind(t, m, nodeAgent, "qa")
	m, _ = nocPress(m, "v")
	if m.agentOutput == nil || !strings.Contains(m.agentOutput.source, "AMQ") {
		t.Fatalf("agentOutput = %+v, want AMQ fallback source", m.agentOutput)
	}
	if len(m.agentOutput.lines) != 2 || m.agentOutput.lines[0] != "tests are green" {
		t.Fatalf("lines = %v", m.agentOutput.lines)
	}
}

func TestViewOutputNothingAvailableNote(t *testing.T) {
	m := outputTestModel(t, nil)
	m.runtimeFetch = func(dir, profile, session string) noc.RuntimeStatus { return noc.RuntimeStatus{} }
	selectKind(t, m, nodeAgent, "qa")
	m, _ = nocPress(m, "v")
	if m.agentOutput != nil {
		t.Fatalf("agentOutput = %+v, want none", m.agentOutput)
	}
	if !strings.Contains(m.actNote, "no live pane") {
		t.Fatalf("note = %q", m.actNote)
	}
}

// #25: the o key re-wires the read-only focus onto the published runtime
// contract: confirm overlay first, the switch targets the published pane id,
// esc cancels with zero effect.
func TestFocusKeyConfirmsThenSwitchesToContractPane(t *testing.T) {
	m := outputTestModel(t, nil)
	m.runtimeFetch = func(dir, profile, session string) noc.RuntimeStatus {
		return noc.RuntimeStatus{
			Advertised: true,
			Members: []noc.RuntimeMember{
				{Role: "qa", Handle: "qa", Session: "term", PaneID: "%9", PaneAlive: true},
			},
		}
	}
	var switched []noc.TmuxTarget
	m.switchTo = func(t noc.TmuxTarget) error { switched = append(switched, t); return nil }

	selectKind(t, m, nodeAgent, "qa")
	m, _ = nocPress(m, "o")
	if m.jumpPending == nil {
		t.Fatalf("o should open the focus confirm overlay, note=%q", m.jumpNote)
	}
	if !strings.Contains(m.jumpPending.prompt, "qa") {
		t.Fatalf("focus prompt = %q, want the agent named", m.jumpPending.prompt)
	}
	if len(switched) != 0 {
		t.Fatal("opening the focus overlay must not switch yet")
	}
	m, _ = nocPress(m, "y")
	if len(switched) != 1 || switched[0].PaneID != "%9" {
		t.Fatalf("switched = %+v, want one switch to the published pane %%9", switched)
	}
}

func TestFocusKeyEscCancelsWithoutSwitch(t *testing.T) {
	m := outputTestModel(t, nil)
	called := false
	m.switchTo = func(noc.TmuxTarget) error { called = true; return nil }
	selectKind(t, m, nodeAgent, "qa")
	m, _ = nocPress(m, "o")
	m, _ = nocPress(m, "esc")
	if called {
		t.Fatal("esc must not switch")
	}
	if m.jumpPending != nil {
		t.Fatal("esc must clear the focus overlay")
	}
}

func TestViewOutputClearsOnOtherAgentSelection(t *testing.T) {
	m := outputTestModel(t, nil)
	m.agentOutput = &agentOutputView{nodeID: "some-other-node", source: "pane %1", lines: []string{"x"}}
	selectKind(t, m, nodeAgent, "qa")
	n, _ := m.selectedNode()
	if got := m.agentOutputSection(n.id); got != "" {
		t.Fatalf("output for another node must not render, got %q", got)
	}
}
