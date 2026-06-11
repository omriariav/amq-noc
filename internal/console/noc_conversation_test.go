package console

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-noc/internal/act"
	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/state"
)

// #27 regression set: conversation mode. m on an orchestrated session or its
// lead row opens the dialogue; the transcript is participant-filtered with
// full bodies; sends go through the inline staged confirm (enter stages,
// enter sends, esc steps back) onto the existing seams.

func conversationTestModel(t *testing.T, leadLiveness state.Liveness, msgs []state.Message) *NOCModel {
	t.Helper()
	sess := state.Session{
		Name:         "pm-copilot",
		Root:         "/fake/agent-mail/pm-copilot",
		Orchestrated: true,
		LeadRole:     "copilot",
		LeadHandle:   "copilot",
		Agents: []state.Agent{
			{Handle: "copilot", Role: "copilot", IsLead: true, Liveness: leadLiveness},
			{Handle: "qa", Role: "qa", Liveness: state.LivenessAlive},
		},
	}
	ps := noc.ProjectSnapshot{
		Project:        "os-omri-pm",
		Dir:            "/fake/os-omri-pm",
		TeamConfigured: true,
		DefaultTeam:    true,
		Profiles:       []string{"default"},
		Snap:           state.Snapshot{Sessions: []state.Session{sess}},
	}
	ms := noc.MultiSnapshot{
		Roots:      []string{"/fake"},
		Projects:   []noc.ProjectSnapshot{ps},
		ObservedAt: time.Date(2026, 6, 11, 17, 0, 0, 0, time.UTC),
	}
	m := newNOCModel(NOCRebuildConfig{Roots: []string{"/fake"}})
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	m.fullTree = true
	m.sendOp = func(act.OpMessage) error { return nil }
	m.panes = func() ([]noc.TmuxPane, error) { return nil, nil }
	m.switchTo = func(noc.TmuxTarget) error { return nil }
	m.transcript = func(root, a, b string, limit int) []state.Message { return msgs }
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m2 := mm.(*NOCModel)
	mm, _ = m2.Update(nocSnapshotMsg{ms: ms})
	return mm.(*NOCModel)
}

func conversationMsgs(base time.Time) []state.Message {
	return []state.Message{
		{
			ID: "d1", From: "user", To: []string{"copilot"}, Thread: "p2p/copilot__user",
			Subject: "DIRECTIVE: ship the sweep", Kind: state.KindTodo,
			Body: "Ship the sweep per spec.", Created: base,
		},
		{
			ID: "a1", From: "copilot", To: []string{"user"}, Thread: "p2p/copilot__user",
			Subject: "ack: sweep underway", Kind: state.KindStatus,
			Body: "On it; analyst dispatched.", Created: base.Add(5 * time.Minute),
		},
	}
}

func typeInto(m *NOCModel, text string) *NOCModel {
	for _, ch := range text {
		m, _ = nocPress(m, string(ch))
	}
	return m
}

func TestMOpensConversationOnOrchestratedRows(t *testing.T) {
	base := time.Date(2026, 6, 11, 16, 0, 0, 0, time.UTC)
	for _, kind := range []nocNodeKind{nodeSession, nodeAgent} {
		m := conversationTestModel(t, state.LivenessAlive, conversationMsgs(base))
		handle := ""
		if kind == nodeAgent {
			handle = "copilot"
		}
		selectKind(t, m, kind, handle)
		m, _ = nocPress(m, "m")
		if m.conversation == nil {
			t.Fatalf("m on %d row should open conversation, note=%q", kind, m.actNote)
		}
		if len(m.conversation.msgs) != 2 {
			t.Fatalf("transcript = %d msgs, want 2", len(m.conversation.msgs))
		}
	}
}

func TestMOnWorkerKeepsComposer(t *testing.T) {
	m := conversationTestModel(t, state.LivenessAlive, nil)
	selectKind(t, m, nodeAgent, "qa")
	m, _ = nocPress(m, "m")
	if m.conversation != nil {
		t.Fatal("worker rows must keep the one-shot composer")
	}
	if m.input == nil || m.input.kind != ctlMessage {
		t.Fatalf("expected message editor, input=%+v note=%q", m.input, m.actNote)
	}
}

func TestConversationFrameRendersTranscript(t *testing.T) {
	base := time.Date(2026, 6, 11, 16, 0, 0, 0, time.UTC)
	m := conversationTestModel(t, state.LivenessAlive, conversationMsgs(base))
	selectKind(t, m, nodeSession, "")
	m, _ = nocPress(m, "m")
	view := m.View()
	for _, want := range []string{
		"CONVERSATION  copilot (lead)",
		"Ship the sweep per spec.",
		"On it; analyst dispatched.",
		"[p2p]",
		"user -> copilot",
		"copilot -> user",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("conversation frame missing %q:\n%s", want, view)
		}
	}
}

func TestConversationStagedDirectiveViaPane(t *testing.T) {
	m := conversationTestModel(t, state.LivenessAlive, nil)
	var sentPane []sendPromptOp
	m.sendPrompt = func(op sendPromptOp) error { sentPane = append(sentPane, op); return nil }
	selectKind(t, m, nodeSession, "")
	m, _ = nocPress(m, "m")
	m = typeInto(m, "run the retro")
	m, _ = nocPress(m, "enter")
	c := m.conversation.composer
	if !c.staged || c.kind != "todo" || c.channel != "pane" {
		t.Fatalf("staged = %+v, want todo via pane for an operational lead", c)
	}
	if len(sentPane) != 0 {
		t.Fatal("staging must not send")
	}
	m, _ = nocPress(m, "enter")
	if len(sentPane) != 1 || sentPane[0].Role != "copilot" || sentPane[0].Body != "run the retro" {
		t.Fatalf("sent = %+v", sentPane)
	}
	if m.conversation.composer.body != "" || m.conversation.composer.staged {
		t.Fatal("composer must reset after send")
	}
	if !strings.Contains(m.conversation.note, "pane") {
		t.Fatalf("note = %q, want pane channel named", m.conversation.note)
	}
}

func TestConversationDirectiveFallsBackToAMQWhenLeadDown(t *testing.T) {
	m := conversationTestModel(t, state.LivenessStale, nil)
	var sent []act.OpMessage
	m.sendOp = func(op act.OpMessage) error { sent = append(sent, op); return nil }
	selectKind(t, m, nodeSession, "")
	m, _ = nocPress(m, "m")
	m = typeInto(m, "pick up issue 30")
	m, _ = nocPress(m, "enter")
	if got := m.conversation.composer.channel; got != "amq" {
		t.Fatalf("channel = %q, want amq for a down lead", got)
	}
	m, _ = nocPress(m, "enter")
	if len(sent) != 1 {
		t.Fatalf("sent = %d ops, want 1", len(sent))
	}
	op := sent[0]
	if op.To != "copilot" || op.Kind != "todo" || op.Thread != "p2p/copilot__user" {
		t.Fatalf("op = %+v", op)
	}
	if !strings.HasPrefix(op.Subject, "DIRECTIVE: ") {
		t.Fatalf("subject = %q", op.Subject)
	}
}

func TestConversationAnswersOpenGate(t *testing.T) {
	base := time.Date(2026, 6, 11, 16, 0, 0, 0, time.UTC)
	msgs := append(conversationMsgs(base), state.Message{
		ID: "g1", From: "copilot", To: []string{"user"}, Thread: "gate/dm-coverage",
		Subject: "APPROVAL: hourly DM sweep?", Kind: state.KindQuestion,
		Body: "Option 1 or 2?", Created: base.Add(10 * time.Minute),
	})
	m := conversationTestModel(t, state.LivenessAlive, msgs)
	var sent []act.OpMessage
	m.sendOp = func(op act.OpMessage) error { sent = append(sent, op); return nil }
	selectKind(t, m, nodeSession, "")
	m, _ = nocPress(m, "m")
	if !strings.Contains(m.View(), "open gate: APPROVAL: hourly DM sweep?") {
		t.Fatalf("open gate banner missing:\n%s", m.View())
	}
	m = typeInto(m, "Option 1, hourly.")
	m, _ = nocPress(m, "enter")
	c := m.conversation.composer
	if !c.gateAnswer || c.kind != "answer" || c.thread != "gate/dm-coverage" {
		t.Fatalf("staged = %+v, want gate answer on gate/dm-coverage", c)
	}
	m, _ = nocPress(m, "enter")
	if len(sent) != 1 || sent[0].Kind != "answer" || sent[0].Thread != "gate/dm-coverage" {
		t.Fatalf("sent = %+v", sent)
	}
	if !strings.HasPrefix(sent[0].Subject, "ANSWER: ") {
		t.Fatalf("subject = %q", sent[0].Subject)
	}
	if sent[0].Me != "user" || sent[0].Root != "/fake/agent-mail/pm-copilot" {
		t.Fatalf("identity/root = %q/%q", sent[0].Me, sent[0].Root)
	}
}

func TestConversationGateClosedByOperatorReplyTargetsDirective(t *testing.T) {
	base := time.Date(2026, 6, 11, 16, 0, 0, 0, time.UTC)
	msgs := []state.Message{
		{ID: "g1", From: "copilot", To: []string{"user"}, Thread: "gate/x",
			Subject: "APPROVAL: x?", Created: base},
		{ID: "g2", From: "user", To: []string{"copilot"}, Thread: "gate/x",
			Subject: "APPROVED: x", Kind: state.KindAnswer, Created: base.Add(time.Minute)},
	}
	m := conversationTestModel(t, state.LivenessStale, msgs)
	selectKind(t, m, nodeSession, "")
	m, _ = nocPress(m, "m")
	m = typeInto(m, "next task")
	m, _ = nocPress(m, "enter")
	c := m.conversation.composer
	if c.gateAnswer || c.kind != "todo" {
		t.Fatalf("answered gate must not be re-targeted, staged = %+v", c)
	}
}

func TestConversationEscStepsBackThenExits(t *testing.T) {
	m := conversationTestModel(t, state.LivenessAlive, nil)
	selectKind(t, m, nodeSession, "")
	m, _ = nocPress(m, "m")
	m = typeInto(m, "hello")
	m, _ = nocPress(m, "enter") // staged
	if !m.conversation.composer.staged {
		t.Fatal("expected staged")
	}
	m, _ = nocPress(m, "esc") // staged -> editing
	if m.conversation.composer.staged || m.conversation.composer.body != "hello" {
		t.Fatalf("esc from staged must return to editing with body intact, got %+v", m.conversation.composer)
	}
	m, _ = nocPress(m, "esc") // editing with text -> cleared
	if m.conversation == nil || m.conversation.composer.body != "" {
		t.Fatal("esc with a draft must clear the draft, not exit")
	}
	m, _ = nocPress(m, "esc") // empty -> exit
	if m.conversation != nil {
		t.Fatal("esc on an empty composer must exit conversation mode")
	}
}

func TestConversationBusyRefusalKeepsDraft(t *testing.T) {
	m := conversationTestModel(t, state.LivenessAlive, nil)
	m.sendPrompt = func(sendPromptOp) error {
		return errors.New("amq-squad send: pane is busy (mid-turn)")
	}
	selectKind(t, m, nodeSession, "")
	m, _ = nocPress(m, "m")
	m = typeInto(m, "go")
	m, _ = nocPress(m, "enter")
	m, _ = nocPress(m, "enter")
	cv := m.conversation
	if !strings.Contains(cv.note, "busy") {
		t.Fatalf("note = %q, want busy refusal surfaced", cv.note)
	}
	if cv.composer.body != "go" || cv.composer.staged {
		t.Fatalf("busy refusal must keep the draft editable, got %+v", cv.composer)
	}
}

func TestConversationRefreshesOnSnapshot(t *testing.T) {
	calls := 0
	m := conversationTestModel(t, state.LivenessAlive, nil)
	m.transcript = func(root, a, b string, limit int) []state.Message { calls++; return nil }
	selectKind(t, m, nodeSession, "")
	m, _ = nocPress(m, "m")
	before := calls
	mm, _ := m.Update(nocSnapshotMsg{ms: m.ms})
	m = mm.(*NOCModel)
	if calls <= before {
		t.Fatal("a fresh snapshot must re-project the open conversation")
	}
}
