package console

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-noc/internal/act"
	"github.com/omriariav/amq-noc/internal/catalog"
	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/state"
)

// --- control-layer test scaffolding --------------------------------------
//
// These tests build a model over a hand-crafted MultiSnapshot so the node
// kinds (project/session/agent) and the needs-you thread are fully under the
// test's control, independent of the shared NOC fixture. Every test drives the
// PUBLIC Update with real tea.KeyMsg values (via nocPress from the sibling test
// file) and asserts on the injected SEAMS — never on a real amq/tmux side
// effect — which is exactly the PR15 safety contract: a decline never calls a
// seam; a confirm calls it once with the exact OpMessage / lifecycle command.

// ctlRoot is the pinned AMQ root the control fixture writes against. It is a
// fake path: no test ever lets a write reach a real bus (the send seam is a
// fake), so the root only has to be a stable, recognizable string in previews.
const ctlRoot = "/fake/root/.agent-mail"

func TestControlCommandPreviewsDelegateToAMQSquad(t *testing.T) {
	session := newSessionOp{ProjectDir: "/repo/app", Session: "issue-2"}
	life := lifecycleOp{Verb: lifecycleStop, ProjectDir: "/repo/app", Session: "issue-2"}

	// Default: confirmed squad-control previews show the installed amq-squad
	// CLI, never the running amq-noc executable.
	if got := session.command(); !strings.HasPrefix(got, "amq-squad new session ") {
		t.Fatalf("new-session preview should delegate to amq-squad, got %q", got)
	}
	if got := life.command(); !strings.HasPrefix(got, "amq-squad stop ") {
		t.Fatalf("lifecycle preview should delegate to amq-squad, got %q", got)
	}

	// The generatedSquadCommandOverride hook still redirects previews (used by
	// tests) to a stub binary.
	old := generatedSquadCommandOverride
	generatedSquadCommandOverride = "/tmp/amq2"
	t.Cleanup(func() { generatedSquadCommandOverride = old })

	if got := session.command(); !strings.HasPrefix(got, "/tmp/amq2 new session ") {
		t.Fatalf("override should redirect new-session preview, got %q", got)
	}
	if got := life.command(); !strings.HasPrefix(got, "/tmp/amq2 stop ") {
		t.Fatalf("override should redirect lifecycle preview, got %q", got)
	}
}

// newControlModel builds a ready *NOCModel over a one-project / one-session
// snapshot whose session carries a needs-you (AttnApprove) thread between the
// operator ("user") and the agent "qa". The agent is alive. The model is sized +
// marked ready so View() renders the live frame and node selection works.
func newControlModel(t *testing.T) *NOCModel {
	t.Helper()
	th := state.ThreadSummary{
		ID:           "decision/ship",
		LatestID:     "msg-ship",
		Participants: []string{"qa", "user"},
		Subject:      "Ship it?",
		Triage:       state.TriageNeedsYou,
		AttnReason:   state.AttnApprove,
	}
	sess := state.Session{
		Name: "beta",
		Root: ctlRoot,
		Agents: []state.Agent{
			{Handle: "qa", Role: "qa", Engine: "claude", Liveness: state.LivenessAlive},
			{Handle: "dev", Role: "dev", Engine: "codex", Liveness: state.LivenessAlive},
		},
		Coordination: state.Coordination{Threads: []state.ThreadSummary{th}},
		Rollup:       state.TriageRollup{NeedsYou: 1},
	}
	ps := noc.ProjectSnapshot{
		Project:        "beta",
		Dir:            "/fake/proj/beta",
		TeamConfigured: true,
		DefaultTeam:    true,
		Profiles:       []string{"default"},
		Snap:           state.Snapshot{Sessions: []state.Session{sess}},
	}
	ms := noc.MultiSnapshot{Roots: []string{"/fake/proj"}, Projects: []noc.ProjectSnapshot{ps}}

	m := newNOCModel(NOCRebuildConfig{Roots: []string{"/fake/proj"}})
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	// Neutralize the real seams: no test may touch a live bus or tmux.
	m.sendOp = func(act.OpMessage) error { return nil }
	m.lifecycle = func(lifecycleOp) error { return nil }
	m.panes = func() ([]noc.TmuxPane, error) { return nil, nil }
	m.switchTo = func(noc.TmuxTarget) error { return nil }

	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m2 := mm.(*NOCModel)
	mm, _ = m2.Update(nocSnapshotMsg{ms: ms})
	return mm.(*NOCModel)
}

// selectKind moves the cursor onto the first node of the given kind (and, for
// agents, the given handle). It fails the test if no such node is visible.
func selectKind(t *testing.T, m *NOCModel, kind nocNodeKind, handle string) {
	t.Helper()
	for i, n := range m.nodes() {
		if n.kind != kind {
			continue
		}
		if kind == nodeAgent && n.agent.Handle != handle {
			continue
		}
		m.cursor = i
		return
	}
	t.Fatalf("no node of kind %d (handle %q) found in %d nodes", kind, handle, len(m.nodes()))
}

func addCandidateProject(m *NOCModel, name, dir string) {
	m.ms.Projects = append(m.ms.Projects, noc.ProjectSnapshot{
		Project:   name,
		Dir:       dir,
		Candidate: true,
	})
}

func addConfiguredEmptyProject(m *NOCModel, name, dir string) {
	m.ms.Projects = append(m.ms.Projects, noc.ProjectSnapshot{
		Project:        name,
		Dir:            dir,
		TeamConfigured: true,
		DefaultTeam:    true,
		Profiles:       []string{"default"},
	})
}

func addNamedConfiguredProject(m *NOCModel, name, dir string, profiles ...string) {
	m.ms.Projects = append(m.ms.Projects, noc.ProjectSnapshot{
		Project:        name,
		Dir:            dir,
		TeamConfigured: true,
		Profiles:       profiles,
	})
}

func addSecondSession(m *NOCModel) {
	if len(m.ms.Projects) == 0 {
		return
	}
	m.ms.Projects[0].Snap.Sessions = append(m.ms.Projects[0].Snap.Sessions, state.Session{
		Name: "alpha",
		Root: "/fake/root/.agent-mail/alpha",
		Agents: []state.Agent{
			{Handle: "cto", Role: "cto", Engine: "codex", Liveness: state.LivenessAlive},
		},
	})
}

func setPrimarySessionProfiles(m *NOCModel, profiles ...string) {
	if len(m.ms.Projects) == 0 || len(m.ms.Projects[0].Snap.Sessions) == 0 {
		return
	}
	for i := range m.ms.Projects[0].Snap.Sessions[0].Agents {
		profile := ""
		if len(profiles) > 0 {
			profile = profiles[i%len(profiles)]
		}
		m.ms.Projects[0].Snap.Sessions[0].Agents[i].TeamProfile = profile
	}
}

func selectProject(t *testing.T, m *NOCModel, label string) {
	t.Helper()
	for i, n := range m.nodes() {
		if n.kind == nodeProject && n.label == label {
			m.cursor = i
			return
		}
	}
	t.Fatalf("no project %q found in %d nodes", label, len(m.nodes()))
}

func typeControlText(t *testing.T, m *NOCModel, text string) *NOCModel {
	t.Helper()
	for _, r := range text {
		m, _ = nocPress(m, string(r))
	}
	return m
}

// theNeedsYouThread returns the fixture's needs-you thread off the current
// selection's session, so a test can build the EXACT expected OpMessage.
func theNeedsYouThread(t *testing.T, m *NOCModel) state.ThreadSummary {
	t.Helper()
	n, ok := m.selectedNode()
	if !ok {
		t.Fatal("no selected node")
	}
	ny := n.session.Coordination.NeedsYouThreads()
	if len(ny) == 0 {
		t.Fatal("selected session has no needs-you thread")
	}
	return ny[0]
}

// --- inbox / read / approve / reply / deny --------------------------------

func TestControl_InboxReadOnlyAction(t *testing.T) {
	t.Run("i lists selected agent inbox without confirm or mutation", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeAgent, "qa")
		var got []inboxAgentOp
		m.inboxAgent = func(op inboxAgentOp) (inboxAgentResult, error) {
			got = append(got, op)
			return inboxAgentResult{Handle: op.Handle, Output: "2026-06-01T09:00:00Z  normal  dev  msg-1  Review needed\n"}, nil
		}

		m, cmd := nocPress(m, "i")
		if len(got) != 1 {
			t.Fatalf("inbox should call the list seam exactly once, got %d", len(got))
		}
		if got[0].Root != ctlRoot || got[0].Handle != "qa" {
			t.Fatalf("inbox op mismatch: %+v", got[0])
		}
		if cmd != nil {
			t.Fatal("read-only inbox listing should not request a rebuild")
		}
		if m.pending != nil || m.input != nil {
			t.Fatalf("inbox listing should not open input/confirm, input=%+v pending=%+v", m.input, m.pending)
		}
		if m.inboxResult == nil {
			t.Fatal("inbox listing should open a result overlay")
		}
		out := m.View()
		for _, want := range []string{
			"INBOX",
			"agent: qa",
			"amq list --root /fake/root/.agent-mail --me qa --new",
			"Review needed",
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("inbox overlay missing %q:\n%s", want, out)
			}
		}
		m, _ = nocPress(m, "enter")
		if m.inboxResult != nil {
			t.Fatal("enter should close the inbox result overlay")
		}
	})

	t.Run("empty inbox output explains there were no unread messages", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeAgent, "qa")
		m.inboxAgent = func(op inboxAgentOp) (inboxAgentResult, error) {
			return inboxAgentResult{Handle: op.Handle}, nil
		}

		m, _ = nocPress(m, "i")
		if m.inboxResult == nil {
			t.Fatal("empty inbox listing should open a result overlay")
		}
		if !strings.Contains(m.View(), "(no unread messages)") {
			t.Fatalf("empty inbox result should explain no unread messages:\n%s", m.View())
		}
	})
}

func TestControl_DLQReadOnlyAction(t *testing.T) {
	t.Run("D lists selected agent DLQ without confirm or mutation", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeAgent, "qa")
		var got []dlqAgentOp
		m.dlqAgent = func(op dlqAgentOp) (dlqAgentResult, error) {
			got = append(got, op)
			return dlqAgentResult{Handle: op.Handle, Output: "dlq-1  corrupt_header  msg-1  new\n"}, nil
		}

		m, cmd := nocPress(m, "D")
		if len(got) != 1 {
			t.Fatalf("DLQ should call the list seam exactly once, got %d", len(got))
		}
		if got[0].Root != ctlRoot || got[0].Handle != "qa" {
			t.Fatalf("DLQ op mismatch: %+v", got[0])
		}
		if cmd != nil {
			t.Fatal("read-only DLQ listing should not request a rebuild")
		}
		if m.pending != nil || m.input != nil {
			t.Fatalf("DLQ listing should not open input/confirm, input=%+v pending=%+v", m.input, m.pending)
		}
		if m.dlqResult == nil {
			t.Fatal("DLQ listing should open a result overlay")
		}
		out := m.View()
		for _, want := range []string{
			"DLQ",
			"agent: qa",
			"amq dlq list --root /fake/root/.agent-mail --me qa",
			"corrupt_header",
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("DLQ overlay missing %q:\n%s", want, out)
			}
		}
		m, _ = nocPress(m, "enter")
		if m.dlqResult != nil {
			t.Fatal("enter should close the DLQ result overlay")
		}
	})

	t.Run("empty DLQ output explains there are no failed messages", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeAgent, "qa")
		m.dlqAgent = func(op dlqAgentOp) (dlqAgentResult, error) {
			return dlqAgentResult{Handle: op.Handle}, nil
		}

		m, _ = nocPress(m, "D")
		if m.dlqResult == nil {
			t.Fatal("empty DLQ listing should open a result overlay")
		}
		if !strings.Contains(m.View(), "(no DLQ messages)") {
			t.Fatalf("empty DLQ result should explain no failed messages:\n%s", m.View())
		}
	})
}

func TestControl_ThreadContextReadOnlyAction(t *testing.T) {
	m := newControlModel(t)
	selectKind(t, m, nodeSession, "")
	var got []threadContextOp
	m.threadContext = func(op threadContextOp) (threadContextResult, error) {
		got = append(got, op)
		return threadContextResult{
			Thread:  op.Thread,
			Subject: op.Subject,
			Output:  "2026-06-01T09:00:00Z  qa  Ship it?\nPlease approve\n---\n",
		}, nil
	}

	m, cmd := nocPress(m, "c")
	if len(got) != 1 {
		t.Fatalf("context should call the thread seam exactly once, got %d", len(got))
	}
	if got[0].Root != ctlRoot || got[0].Thread != "decision/ship" || got[0].Subject != "Ship it?" {
		t.Fatalf("thread context op mismatch: %+v", got[0])
	}
	if cmd != nil {
		t.Fatal("read-only thread context should not request a rebuild")
	}
	if m.pending != nil || m.input != nil {
		t.Fatalf("thread context should not open input/confirm, input=%+v pending=%+v", m.input, m.pending)
	}
	if m.threadContextResult == nil {
		t.Fatal("thread context should open a result overlay")
	}
	out := m.View()
	for _, want := range []string{
		"THREAD CONTEXT",
		"thread: decision/ship",
		"subject: Ship it?",
		"amq thread --root /fake/root/.agent-mail --id decision/ship --include-body --limit 20",
		"Please approve",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("thread context overlay missing %q:\n%s", want, out)
		}
	}
	m, _ = nocPress(m, "enter")
	if m.threadContextResult != nil {
		t.Fatal("enter should close the thread context overlay")
	}
}

func TestControl_ReadNeedsYouConfirmGate(t *testing.T) {
	t.Run("v opens a preview overlay showing amq read and sends nothing", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		called := false
		m.readNeedsYou = func(readNeedsYouOp) (readNeedsYouResult, error) {
			called = true
			return readNeedsYouResult{}, nil
		}

		m, _ = nocPress(m, "v")
		if m.pending == nil || m.pending.kind != ctlRead || m.pending.read == nil {
			t.Fatalf("v on a needs-you session should open the read confirm overlay, got %+v", m.pending)
		}
		for _, want := range []string{"amq read", "--root /fake/root/.agent-mail", "--me user", "--id msg-ship", "--json"} {
			if !strings.Contains(m.pending.preview, want) {
				t.Fatalf("read preview missing %q: %s", want, m.pending.preview)
			}
		}
		if called {
			t.Fatal("opening the read overlay must not call the read seam")
		}
	})

	t.Run("esc declines: read seam NEVER called", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		called := false
		m.readNeedsYou = func(readNeedsYouOp) (readNeedsYouResult, error) {
			called = true
			return readNeedsYouResult{}, nil
		}

		m, _ = nocPress(m, "v")
		m, _ = nocPress(m, "esc")
		if called {
			t.Error("declining read must NOT call the read seam")
		}
		if m.pending != nil {
			t.Error("esc should close the confirm overlay")
		}
	})

	t.Run("y confirms: read seam called once and body overlay opens", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		var got []readNeedsYouOp
		m.readNeedsYou = func(op readNeedsYouOp) (readNeedsYouResult, error) {
			got = append(got, op)
			return readNeedsYouResult{
				MessageID: op.MessageID,
				Thread:    op.Thread,
				Subject:   op.Subject,
				Body:      "Please approve the ship plan.",
			}, nil
		}

		m, _ = nocPress(m, "v")
		m, cmd := nocPress(m, "y")
		if len(got) != 1 {
			t.Fatalf("confirm should call the read seam exactly once, got %d", len(got))
		}
		if got[0].Root != ctlRoot || got[0].MessageID != "msg-ship" || got[0].Thread != "decision/ship" {
			t.Fatalf("read op mismatch: %+v", got[0])
		}
		if cmd == nil {
			t.Fatal("successful read should request an immediate refresh")
		}
		if m.readResult == nil {
			t.Fatal("successful read should open the body result overlay")
		}
		if !strings.Contains(m.View(), "Please approve the ship plan.") {
			t.Fatalf("read result overlay should show the body:\n%s", m.View())
		}
		m, _ = nocPress(m, "enter")
		if m.readResult != nil {
			t.Fatal("enter should close the read result overlay")
		}
	})

	t.Run("missing latest id leaves a guidance note and calls nothing", func(t *testing.T) {
		m := newControlModel(t)
		m.ms.Projects[0].Snap.Sessions[0].Coordination.Threads[0].LatestID = ""
		selectKind(t, m, nodeSession, "")
		called := false
		m.readNeedsYou = func(readNeedsYouOp) (readNeedsYouResult, error) {
			called = true
			return readNeedsYouResult{}, nil
		}

		m, _ = nocPress(m, "v")
		if called {
			t.Fatal("missing latest id must not call the read seam")
		}
		if m.pending != nil {
			t.Fatal("missing latest id must not open a confirm overlay")
		}
		if !strings.Contains(m.actNote, "no latest message id") {
			t.Fatalf("missing latest id should explain the problem, note=%q", m.actNote)
		}
	})
}

func TestControl_DrainConfirmGate(t *testing.T) {
	t.Run("d opens a preview overlay showing amq drain and calls nothing", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeAgent, "qa")
		called := false
		m.drainAgent = func(drainAgentOp) (drainAgentResult, error) {
			called = true
			return drainAgentResult{}, nil
		}

		m, _ = nocPress(m, "d")
		if m.pending == nil || m.pending.kind != ctlDrain || m.pending.drain == nil {
			t.Fatalf("d on an agent should open the drain confirm overlay, got %+v", m.pending)
		}
		for _, want := range []string{"amq drain", "--root /fake/root/.agent-mail", "--me qa", "--include-body"} {
			if !strings.Contains(m.pending.preview, want) {
				t.Fatalf("drain preview missing %q: %s", want, m.pending.preview)
			}
		}
		if called {
			t.Fatal("opening the drain overlay must not call the drain seam")
		}
	})

	t.Run("esc declines: drain seam NEVER called", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeAgent, "qa")
		called := false
		m.drainAgent = func(drainAgentOp) (drainAgentResult, error) {
			called = true
			return drainAgentResult{}, nil
		}

		m, _ = nocPress(m, "d")
		m, _ = nocPress(m, "esc")
		if called {
			t.Error("declining drain must NOT call the drain seam")
		}
		if m.pending != nil {
			t.Error("esc should close the confirm overlay")
		}
	})

	t.Run("y confirms: drain seam called once and output overlay opens", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeAgent, "qa")
		var got []drainAgentOp
		m.drainAgent = func(op drainAgentOp) (drainAgentResult, error) {
			got = append(got, op)
			return drainAgentResult{Handle: op.Handle, Output: "[AMQ] 1 new message(s) for qa:\nbody\n"}, nil
		}

		m, _ = nocPress(m, "d")
		m, cmd := nocPress(m, "y")
		if len(got) != 1 {
			t.Fatalf("confirm should call the drain seam exactly once, got %d", len(got))
		}
		if got[0].Root != ctlRoot || got[0].Handle != "qa" {
			t.Fatalf("drain op mismatch: %+v", got[0])
		}
		if cmd == nil {
			t.Fatal("successful drain should request an immediate refresh")
		}
		if m.drainResult == nil {
			t.Fatal("successful drain should open the output result overlay")
		}
		if !strings.Contains(m.View(), "[AMQ] 1 new message(s) for qa:") {
			t.Fatalf("drain result overlay should show output:\n%s", m.View())
		}
		m, _ = nocPress(m, "enter")
		if m.drainResult != nil {
			t.Fatal("enter should close the drain result overlay")
		}
	})

	t.Run("empty drain output explains there were no messages", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeAgent, "qa")
		m.drainAgent = func(op drainAgentOp) (drainAgentResult, error) {
			return drainAgentResult{Handle: op.Handle}, nil
		}

		m, _ = nocPress(m, "d")
		m, _ = nocPress(m, "y")
		if m.drainResult == nil {
			t.Fatal("successful empty drain should open the output result overlay")
		}
		if !strings.Contains(m.View(), "(no new messages)") {
			t.Fatalf("empty drain result should explain no messages:\n%s", m.View())
		}
	})
}

func TestControl_ApproveConfirmGate(t *testing.T) {
	t.Run("a opens a preview overlay showing act.Preview", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")

		called := false
		m.sendOp = func(act.OpMessage) error { called = true; return nil }

		m, _ = nocPress(m, "a")
		if m.pending == nil {
			t.Fatal("a on a needs-you session should open the confirm overlay")
		}
		want := act.Preview(act.Approve(ctlRoot, "beta", theNeedsYouThread(t, m)))
		if m.pending.preview != want {
			t.Errorf("overlay preview mismatch:\n got %q\nwant %q", m.pending.preview, want)
		}
		if !strings.Contains(m.View(), "amq send") {
			t.Errorf("confirm overlay should render the literal amq send command:\n%s", m.View())
		}
		if !strings.Contains(m.View(), "--kind answer") {
			t.Errorf("approve preview should carry --kind answer:\n%s", m.View())
		}
		if called {
			t.Fatal("merely opening the overlay must NOT call the send seam")
		}
	})

	t.Run("esc declines: send seam NEVER called", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		called := false
		m.sendOp = func(act.OpMessage) error { called = true; return nil }

		m, _ = nocPress(m, "a")
		m, _ = nocPress(m, "esc")
		if called {
			t.Error("declining (esc) must NOT call the send seam")
		}
		if m.pending != nil {
			t.Error("esc should close the confirm overlay")
		}
	})

	t.Run("y confirms: send seam called once with the Approve OpMessage", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		var got []act.OpMessage
		m.sendOp = func(op act.OpMessage) error { got = append(got, op); return nil }

		want := act.Approve(ctlRoot, "beta", theNeedsYouThread(t, m))
		m, _ = nocPress(m, "a")
		m, _ = nocPress(m, "y")
		if len(got) != 1 {
			t.Fatalf("confirm should call the send seam exactly once, got %d", len(got))
		}
		if got[0] != want {
			t.Errorf("confirmed OpMessage mismatch:\n got %+v\nwant %+v", got[0], want)
		}
		if m.pending != nil {
			t.Error("confirm should close the overlay")
		}
	})
}

func TestControl_ConfirmedMutationRequestsImmediateRefresh(t *testing.T) {
	m := newControlModel(t)
	selectKind(t, m, nodeSession, "")
	m.sendOp = func(act.OpMessage) error { return nil }

	m, _ = nocPress(m, "a")
	if m.pending == nil {
		t.Fatal("approve should open a confirm overlay")
	}
	m, cmd := nocPress(m, "y")
	if cmd == nil {
		t.Fatal("successful confirmed mutation should request an immediate refresh")
	}
	if !strings.Contains(m.actNote, "APPROVE sent") {
		t.Fatalf("successful confirm should keep an action note, got %q", m.actNote)
	}
}

func TestControl_FailedMutationDoesNotRequestImmediateRefresh(t *testing.T) {
	m := newControlModel(t)
	selectKind(t, m, nodeSession, "")
	m.sendOp = func(act.OpMessage) error { return errString("bus unavailable") }

	m, _ = nocPress(m, "a")
	if m.pending == nil {
		t.Fatal("approve should open a confirm overlay")
	}
	m, cmd := nocPress(m, "y")
	if cmd != nil {
		t.Fatal("failed mutation should not request an immediate refresh")
	}
	if !strings.Contains(m.actNote, "failed") {
		t.Fatalf("failed confirm should explain the failure, got %q", m.actNote)
	}
}

func TestControl_ReplyConfirmGate(t *testing.T) {
	typeReply := func(m *NOCModel, body string) *NOCModel {
		selectKind(t, m, nodeSession, "")
		m, _ = nocPress(m, "r")
		if m.input == nil || m.input.kind != ctlReply {
			t.Fatalf("r on a needs-you session should open the reply body editor, got %+v", m.input)
		}
		m = typeControlText(t, m, body)
		m, _ = nocPress(m, "enter")
		if m.pending == nil {
			t.Fatal("enter on the reply editor should open the confirm overlay")
		}
		return m
	}

	t.Run("preview answers the needs-you thread and sends nothing", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		called := false
		m.sendOp = func(act.OpMessage) error { called = true; return nil }

		want := act.Preview(act.Reply(ctlRoot, "beta", theNeedsYouThread(t, m), "I need one more check"))
		m = typeReply(m, "I need one more check")
		if m.pending.preview != want {
			t.Errorf("reply preview mismatch:\n got %q\nwant %q", m.pending.preview, want)
		}
		if !strings.Contains(m.View(), "--kind answer") {
			t.Errorf("reply preview should carry --kind answer:\n%s", m.View())
		}
		if called {
			t.Fatal("opening the reply overlay must not call the send seam")
		}
	})

	t.Run("decline: send seam NEVER called", func(t *testing.T) {
		m := newControlModel(t)
		called := false
		m.sendOp = func(act.OpMessage) error { called = true; return nil }
		m = typeReply(m, "I need one more check")
		m, _ = nocPress(m, "esc")
		if called {
			t.Error("declining a reply must NOT call the send seam")
		}
		if m.pending != nil {
			t.Error("declining should close the overlay")
		}
	})

	t.Run("confirm: send seam called once with the Reply OpMessage", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		var got []act.OpMessage
		m.sendOp = func(op act.OpMessage) error { got = append(got, op); return nil }

		want := act.Reply(ctlRoot, "beta", theNeedsYouThread(t, m), "I need one more check")
		m = typeReply(m, "I need one more check")
		m, _ = nocPress(m, "y")
		if len(got) != 1 {
			t.Fatalf("confirmed reply should call the send seam exactly once, got %d", len(got))
		}
		if got[0] != want {
			t.Errorf("confirmed Reply OpMessage mismatch:\n got %+v\nwant %+v", got[0], want)
		}
	})

	t.Run("empty body: stays in editor and sends nothing", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		called := false
		m.sendOp = func(act.OpMessage) error { called = true; return nil }

		m, _ = nocPress(m, "r")
		m, _ = nocPress(m, "enter")
		if called {
			t.Fatal("empty reply body must not call the send seam")
		}
		if m.pending != nil {
			t.Fatal("empty reply body must not open a confirm overlay")
		}
		if m.input == nil || !strings.Contains(m.actNote, "body cannot be empty") {
			t.Fatalf("empty reply body should keep editor open with guidance, input=%+v note=%q", m.input, m.actNote)
		}
	})
}

func TestControl_DenyConfirmGate(t *testing.T) {
	t.Run("x decline: send seam NEVER called", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		called := false
		m.sendOp = func(act.OpMessage) error { called = true; return nil }

		m, _ = nocPress(m, "x")
		if m.input == nil {
			t.Fatal("x should open the deny reason editor")
		}
		for _, ch := range "looks risky" {
			m, _ = nocPress(m, string(ch))
		}
		m, _ = nocPress(m, "enter") // editor -> confirm overlay
		if m.pending == nil {
			t.Fatal("enter on the deny editor should open the confirm overlay")
		}
		if called {
			t.Fatal("building the deny preview must not call the send seam")
		}
		m, _ = nocPress(m, "esc") // decline
		if called {
			t.Error("declining deny must NOT call the send seam")
		}
	})

	t.Run("y confirms: send seam called once with the Deny OpMessage", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		var got []act.OpMessage
		m.sendOp = func(op act.OpMessage) error { got = append(got, op); return nil }

		want := act.Deny(ctlRoot, "beta", theNeedsYouThread(t, m), "nope")
		m, _ = nocPress(m, "x")
		for _, ch := range "nope" {
			m, _ = nocPress(m, string(ch))
		}
		m, _ = nocPress(m, "enter")
		m, _ = nocPress(m, "y")
		if len(got) != 1 {
			t.Fatalf("confirmed deny should call the send seam once, got %d", len(got))
		}
		if got[0] != want {
			t.Errorf("confirmed deny OpMessage mismatch:\n got %+v\nwant %+v", got[0], want)
		}
	})
}

// --- message -------------------------------------------------------------

func TestControl_MessageConfirmGate(t *testing.T) {
	// typeMessage opens the editor on agent qa, types body, and returns the model
	// at the confirm overlay (preview built, nothing sent yet).
	typeMessage := func(m *NOCModel) *NOCModel {
		selectKind(t, m, nodeAgent, "qa")
		m, _ = nocPress(m, "m")
		if m.input == nil {
			t.Fatal("m on an agent should open the message body editor")
		}
		for _, ch := range "hello qa" {
			m, _ = nocPress(m, string(ch))
		}
		m, _ = nocPress(m, "enter")
		if m.pending == nil {
			t.Fatal("enter on the message editor should open the confirm overlay")
		}
		return m
	}

	t.Run("preview addresses qa via amq send; opening sends nothing", func(t *testing.T) {
		m := newControlModel(t)
		sent := false
		m.sendOp = func(act.OpMessage) error { sent = true; return nil }
		m = typeMessage(m)
		if !strings.Contains(m.pending.preview, "amq send") || !strings.Contains(m.pending.preview, "--to qa") {
			t.Errorf("message preview should address qa via amq send: %q", m.pending.preview)
		}
		if sent {
			t.Error("opening the message overlay must not call the send seam")
		}
	})

	t.Run("decline: send seam NEVER called", func(t *testing.T) {
		m := newControlModel(t)
		called := false
		m.sendOp = func(act.OpMessage) error { called = true; return nil }
		m = typeMessage(m)
		m, _ = nocPress(m, "n") // any non-confirm key cancels
		if called {
			t.Error("declining a message must NOT call the send seam")
		}
		if m.pending != nil {
			t.Error("declining should close the overlay")
		}
	})

	t.Run("confirm: send seam called once addressing qa", func(t *testing.T) {
		m := newControlModel(t)
		var got []act.OpMessage
		m.sendOp = func(op act.OpMessage) error { got = append(got, op); return nil }
		m = typeMessage(m)
		m, _ = nocPress(m, "y")
		if len(got) != 1 {
			t.Fatalf("confirmed message should call the send seam once, got %d", len(got))
		}
		if got[0].To != "qa" || got[0].Body != "hello qa" {
			t.Errorf("confirmed message OpMessage mismatch: %+v", got[0])
		}
	})

	t.Run("empty body: stays in editor and sends nothing", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeAgent, "qa")
		called := false
		m.sendOp = func(act.OpMessage) error { called = true; return nil }

		m, _ = nocPress(m, "m")
		m, _ = nocPress(m, "enter")
		if called {
			t.Fatal("empty message body must not call the send seam")
		}
		if m.pending != nil {
			t.Fatal("empty message body must not open a confirm overlay")
		}
		if m.input == nil || !strings.Contains(m.actNote, "body cannot be empty") {
			t.Fatalf("empty message body should keep editor open with guidance, input=%+v note=%q", m.input, m.actNote)
		}
	})
}

// --- broadcast -----------------------------------------------------------

func TestControl_BroadcastConfirmGate(t *testing.T) {
	// typeBroadcast opens the editor on the session, types subject then body, and
	// returns the model at the confirm overlay (preview built, nothing sent yet).
	typeBroadcast := func(m *NOCModel) *NOCModel {
		selectKind(t, m, nodeSession, "")
		m, _ = nocPress(m, "b")
		if m.input == nil {
			t.Fatal("b on a session should open the broadcast subject editor")
		}
		for _, ch := range "standup" {
			m, _ = nocPress(m, string(ch))
		}
		m, _ = nocPress(m, "enter") // subject -> body stage
		if m.input == nil || m.input.stage != 1 {
			t.Fatal("first enter on broadcast should advance subject -> body")
		}
		for _, ch := range "status please" {
			m, _ = nocPress(m, string(ch))
		}
		m, _ = nocPress(m, "enter") // body -> confirm overlay
		if m.pending == nil {
			t.Fatal("second enter on broadcast should open the confirm overlay")
		}
		return m
	}

	t.Run("decline: send seam NEVER called", func(t *testing.T) {
		m := newControlModel(t)
		called := false
		m.sendOp = func(act.OpMessage) error { called = true; return nil }
		m = typeBroadcast(m)
		m, _ = nocPress(m, "esc")
		if called {
			t.Error("declining a broadcast must NOT call the send seam")
		}
		if m.pending != nil {
			t.Error("declining should close the overlay")
		}
	})

	t.Run("confirm: send seam called once with the exact Broadcast OpMessage", func(t *testing.T) {
		m := newControlModel(t)
		var got []act.OpMessage
		m.sendOp = func(op act.OpMessage) error { got = append(got, op); return nil }
		m = typeBroadcast(m)
		m, _ = nocPress(m, "y")
		if len(got) != 1 {
			t.Fatalf("confirmed broadcast should call the send seam once, got %d", len(got))
		}
		want := act.Broadcast(ctlRoot, "beta", []string{"qa", "dev"}, "standup", "status please")
		if got[0] != want {
			t.Errorf("confirmed broadcast OpMessage mismatch:\n got %+v\nwant %+v", got[0], want)
		}
	})

	t.Run("empty subject: stays in subject editor and sends nothing", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		called := false
		m.sendOp = func(act.OpMessage) error { called = true; return nil }

		m, _ = nocPress(m, "b")
		m, _ = nocPress(m, "enter")
		if called {
			t.Fatal("empty broadcast subject must not call the send seam")
		}
		if m.pending != nil {
			t.Fatal("empty broadcast subject must not open a confirm overlay")
		}
		if m.input == nil || m.input.stage != 0 || !strings.Contains(m.actNote, "subject cannot be empty") {
			t.Fatalf("empty broadcast subject should keep subject editor open with guidance, input=%+v note=%q", m.input, m.actNote)
		}
	})

	t.Run("empty body: stays in body editor and sends nothing", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		called := false
		m.sendOp = func(act.OpMessage) error { called = true; return nil }

		m, _ = nocPress(m, "b")
		for _, ch := range "standup" {
			m, _ = nocPress(m, string(ch))
		}
		m, _ = nocPress(m, "enter")
		m, _ = nocPress(m, "enter")
		if called {
			t.Fatal("empty broadcast body must not call the send seam")
		}
		if m.pending != nil {
			t.Fatal("empty broadcast body must not open a confirm overlay")
		}
		if m.input == nil || m.input.stage != 1 || !strings.Contains(m.actNote, "body cannot be empty") {
			t.Fatalf("empty broadcast body should keep body editor open with guidance, input=%+v note=%q", m.input, m.actNote)
		}
	})
}

func TestControl_ProjectWithMultipleSessionsRequiresSessionSelection(t *testing.T) {
	t.Run("broadcast does not guess a session", func(t *testing.T) {
		m := newControlModel(t)
		addSecondSession(m)
		selectKind(t, m, nodeProject, "")
		called := false
		m.sendOp = func(act.OpMessage) error { called = true; return nil }

		m, _ = nocPress(m, "b")
		if called {
			t.Fatal("project-level broadcast with multiple sessions must not call send")
		}
		if m.input != nil || m.pending != nil {
			t.Fatalf("project-level broadcast with multiple sessions must not open input/confirm, input=%+v pending=%+v", m.input, m.pending)
		}
		if !strings.Contains(m.actNote, "multiple sessions") || !strings.Contains(m.actNote, "select one session") {
			t.Fatalf("broadcast should explain the ambiguity, note=%q", m.actNote)
		}
	})

	t.Run("lifecycle does not guess a session", func(t *testing.T) {
		m := newControlModel(t)
		addSecondSession(m)
		selectKind(t, m, nodeProject, "")
		called := false
		m.lifecycle = func(lifecycleOp) error { called = true; return nil }

		m, _ = nocPress(m, "S")
		if called {
			t.Fatal("project-level stop with multiple sessions must not call lifecycle")
		}
		if m.pending != nil {
			t.Fatalf("project-level stop with multiple sessions must not open confirm, pending=%+v", m.pending)
		}
		if !strings.Contains(m.actNote, "multiple sessions") || !strings.Contains(m.actNote, "select one session") {
			t.Fatalf("lifecycle should explain the ambiguity, note=%q", m.actNote)
		}
	})

	t.Run("open does not guess a session", func(t *testing.T) {
		m := newControlModel(t)
		addSecondSession(m)
		selectKind(t, m, nodeProject, "")
		switched := false
		m.switchTo = func(noc.TmuxTarget) error { switched = true; return nil }

		m, _ = nocPress(m, "o")
		if switched {
			t.Fatal("project-level open with multiple sessions must not switch")
		}
		if m.jumpPending != nil {
			t.Fatalf("project-level open with multiple sessions must not open focus confirm, jumpPending=%+v", m.jumpPending)
		}
		if !strings.Contains(m.actNote, "multiple sessions") || !strings.Contains(m.actNote, "select one session") {
			t.Fatalf("open should explain the ambiguity, note=%q", m.actNote)
		}
	})
}

// --- stop / resume / restart --------------------------------------------

func TestLifecycleCommandScopesProjectDir(t *testing.T) {
	op := lifecycleOp{Verb: lifecycleResume, ProjectDir: "/tmp/team home", Session: "issue-1"}
	want := "amq-squad resume --project '/tmp/team home' --exec --target new-session --terminal-session amq-squad-team-home-issue-1 --session issue-1"
	if got := op.command(); got != want {
		t.Fatalf("lifecycle command = %q, want %q", got, want)
	}
}

func TestLifecycleCommandCarriesProfile(t *testing.T) {
	op := lifecycleOp{Verb: lifecycleRestart, ProjectDir: "/tmp/team home", Profile: "review", Session: "issue-1"}
	want := "amq-squad stop --project '/tmp/team home' --all --profile review --session issue-1 && amq-squad resume --project '/tmp/team home' --profile review --exec --target new-session --terminal-session amq-squad-team-home-issue-1 --session issue-1"
	if got := op.command(); got != want {
		t.Fatalf("lifecycle command = %q, want %q", got, want)
	}
}

func TestAdaptLifecycleCarriesProfile(t *testing.T) {
	var got LifecycleRequest
	fn := adaptLifecycle(func(req LifecycleRequest) error {
		got = req
		return nil
	})
	if err := fn(lifecycleOp{Verb: lifecycleResume, ProjectDir: "/tmp/team", Profile: "review", Session: "issue-1"}); err != nil {
		t.Fatalf("adaptLifecycle: %v", err)
	}
	if got.Verb != "resume" || got.ProjectDir != "/tmp/team" || got.Profile != "review" || got.Session != "issue-1" {
		t.Fatalf("LifecycleRequest mismatch: %+v", got)
	}
}

func TestAdaptAgentResumeCarriesScope(t *testing.T) {
	var got AgentResumeRequest
	fn := adaptAgentResume(func(req AgentResumeRequest) error {
		got = req
		return nil
	})
	if err := fn(agentResumeOp{ProjectDir: "/tmp/team", Role: "qa", Session: "issue-1"}); err != nil {
		t.Fatalf("adaptAgentResume: %v", err)
	}
	if got.ProjectDir != "/tmp/team" || got.Role != "qa" || got.Session != "issue-1" {
		t.Fatalf("AgentResumeRequest mismatch: %+v", got)
	}
}

func TestAdaptSessionCleanupCarriesRequest(t *testing.T) {
	var got SessionCleanupRequest
	fn := adaptSessionCleanup(func(req SessionCleanupRequest) error {
		got = req
		return nil
	})
	if err := fn(sessionCleanupOp{ProjectDir: "/tmp/team", Session: "issue-1", Archive: true}); err != nil {
		t.Fatalf("adaptSessionCleanup: %v", err)
	}
	if got.ProjectDir != "/tmp/team" || got.Session != "issue-1" || !got.Archive {
		t.Fatalf("SessionCleanupRequest mismatch: %+v", got)
	}
}

func TestAdaptStatusCarriesRequest(t *testing.T) {
	var got StatusRequest
	fn := adaptStatus(func(req StatusRequest) (StatusResult, error) {
		got = req
		return StatusResult{ProjectDir: req.ProjectDir, Session: req.Session, Profile: req.Profile, Output: "ok\n"}, nil
	})
	res, err := fn(statusOp{ProjectDir: "/tmp/team", Session: "issue-1", Profile: "review"})
	if err != nil {
		t.Fatalf("adaptStatus: %v", err)
	}
	if got.ProjectDir != "/tmp/team" || got.Session != "issue-1" || got.Profile != "review" {
		t.Fatalf("StatusRequest mismatch: %+v", got)
	}
	if res.Output != "ok\n" {
		t.Fatalf("StatusResult mismatch: %+v", res)
	}
}

func TestAdaptNewSessionCarriesProfile(t *testing.T) {
	var got NewSessionRequest
	fn := adaptNewSession(func(req NewSessionRequest) error {
		got = req
		return nil
	})
	if err := fn(newSessionOp{ProjectDir: "/tmp/team", Profile: "review", Session: "issue-1", SeedFrom: "issue:31"}); err != nil {
		t.Fatalf("adaptNewSession: %v", err)
	}
	if got.ProjectDir != "/tmp/team" || got.Profile != "review" || got.Session != "issue-1" || got.SeedFrom != "issue:31" {
		t.Fatalf("NewSessionRequest mismatch: %+v", got)
	}
}

func TestAdaptNewTeamCarriesProfile(t *testing.T) {
	var got NewTeamRequest
	fn := adaptNewTeam(func(req NewTeamRequest) error {
		got = req
		return nil
	})
	if err := fn(newTeamOp{ProjectDir: "/tmp/team", Profile: "review", Roles: "cto,qa", Binary: "qa=codex", Session: "issue-96", Sync: true}); err != nil {
		t.Fatalf("adaptNewTeam: %v", err)
	}
	if got.ProjectDir != "/tmp/team" || got.Profile != "review" || got.Roles != "cto,qa" || got.Binary != "qa=codex" || got.Session != "issue-96" || !got.Sync {
		t.Fatalf("NewTeamRequest mismatch: %+v", got)
	}
}

func TestAdaptTeamDeleteCarriesProfile(t *testing.T) {
	var got TeamDeleteRequest
	fn := adaptTeamDelete(func(req TeamDeleteRequest) error {
		got = req
		return nil
	})
	if err := fn(teamDeleteOp{ProjectDir: "/tmp/team", Profile: "review", AllProfiles: true}); err != nil {
		t.Fatalf("adaptTeamDelete: %v", err)
	}
	if got.ProjectDir != "/tmp/team" || got.Profile != "review" || !got.AllProfiles {
		t.Fatalf("TeamDeleteRequest mismatch: %+v", got)
	}
}

func TestAdaptReadNeedsYouCarriesMessageID(t *testing.T) {
	var got ReadNeedsYouRequest
	fn := adaptReadNeedsYou(func(req ReadNeedsYouRequest) (ReadNeedsYouResult, error) {
		got = req
		return ReadNeedsYouResult{MessageID: req.MessageID, Thread: req.Thread, Subject: req.Subject, Body: "body"}, nil
	})
	res, err := fn(readNeedsYouOp{Root: "/tmp/root", MessageID: "msg-1", Thread: "ask/ship", Subject: "Ship it?"})
	if err != nil {
		t.Fatalf("adaptReadNeedsYou: %v", err)
	}
	if got.Root != "/tmp/root" || got.MessageID != "msg-1" || got.Thread != "ask/ship" || got.Subject != "Ship it?" {
		t.Fatalf("ReadNeedsYouRequest mismatch: %+v", got)
	}
	if res.MessageID != "msg-1" || res.Body != "body" {
		t.Fatalf("read result mismatch: %+v", res)
	}
}

func TestAdaptDrainAgentCarriesHandle(t *testing.T) {
	var got DrainAgentRequest
	fn := adaptDrainAgent(func(req DrainAgentRequest) (DrainAgentResult, error) {
		got = req
		return DrainAgentResult{Handle: req.Handle, Output: "drained"}, nil
	})
	res, err := fn(drainAgentOp{Root: "/tmp/root", Handle: "qa"})
	if err != nil {
		t.Fatalf("adaptDrainAgent: %v", err)
	}
	if got.Root != "/tmp/root" || got.Handle != "qa" {
		t.Fatalf("DrainAgentRequest mismatch: %+v", got)
	}
	if res.Handle != "qa" || res.Output != "drained" {
		t.Fatalf("drain result mismatch: %+v", res)
	}
}

func TestAdaptInboxAgentCarriesHandle(t *testing.T) {
	var got InboxAgentRequest
	fn := adaptInboxAgent(func(req InboxAgentRequest) (InboxAgentResult, error) {
		got = req
		return InboxAgentResult{Handle: req.Handle, Output: "listed"}, nil
	})
	res, err := fn(inboxAgentOp{Root: "/tmp/root", Handle: "qa"})
	if err != nil {
		t.Fatalf("adaptInboxAgent: %v", err)
	}
	if got.Root != "/tmp/root" || got.Handle != "qa" {
		t.Fatalf("InboxAgentRequest mismatch: %+v", got)
	}
	if res.Handle != "qa" || res.Output != "listed" {
		t.Fatalf("inbox result mismatch: %+v", res)
	}
}

func TestAdaptDLQAgentCarriesHandle(t *testing.T) {
	var got DLQAgentRequest
	fn := adaptDLQAgent(func(req DLQAgentRequest) (DLQAgentResult, error) {
		got = req
		return DLQAgentResult{Handle: req.Handle, Output: "dlq"}, nil
	})
	res, err := fn(dlqAgentOp{Root: "/tmp/root", Handle: "qa"})
	if err != nil {
		t.Fatalf("adaptDLQAgent: %v", err)
	}
	if got.Root != "/tmp/root" || got.Handle != "qa" {
		t.Fatalf("DLQAgentRequest mismatch: %+v", got)
	}
	if res.Handle != "qa" || res.Output != "dlq" {
		t.Fatalf("DLQ result mismatch: %+v", res)
	}
}

func TestAdaptDLQReadCarriesID(t *testing.T) {
	var got DLQReadRequest
	fn := adaptDLQRead(func(req DLQReadRequest) (DLQReadResult, error) {
		got = req
		return DLQReadResult{Handle: req.Handle, ID: req.ID, Output: "DLQ body"}, nil
	})
	res, err := fn(dlqReadOp{Root: "/tmp/root", Handle: "qa", ID: "dlq_123"})
	if err != nil {
		t.Fatalf("adaptDLQRead: %v", err)
	}
	if got.Root != "/tmp/root" || got.Handle != "qa" || got.ID != "dlq_123" {
		t.Fatalf("DLQReadRequest mismatch: %+v", got)
	}
	if res.Handle != "qa" || res.ID != "dlq_123" || res.Output != "DLQ body" {
		t.Fatalf("DLQ read result mismatch: %+v", res)
	}
}

func TestAdaptDLQRetryCarriesID(t *testing.T) {
	var got DLQRetryRequest
	fn := adaptDLQRetry(func(req DLQRetryRequest) (DLQRetryResult, error) {
		got = req
		return DLQRetryResult{Handle: req.Handle, ID: req.ID, Output: "Retried 1 message."}, nil
	})
	res, err := fn(dlqRetryOp{Root: "/tmp/root", Handle: "qa", ID: "dlq_123"})
	if err != nil {
		t.Fatalf("adaptDLQRetry: %v", err)
	}
	if got.Root != "/tmp/root" || got.Handle != "qa" || got.ID != "dlq_123" {
		t.Fatalf("DLQRetryRequest mismatch: %+v", got)
	}
	if res.Handle != "qa" || res.ID != "dlq_123" || res.Output != "Retried 1 message." {
		t.Fatalf("DLQ retry result mismatch: %+v", res)
	}
}

func TestAdaptDLQPurgeCarriesOlderThan(t *testing.T) {
	var got DLQPurgeRequest
	fn := adaptDLQPurge(func(req DLQPurgeRequest) (DLQPurgeResult, error) {
		got = req
		return DLQPurgeResult{Handle: req.Handle, OlderThan: req.OlderThan, Output: "Purged 2 message(s)."}, nil
	})
	res, err := fn(dlqPurgeOp{Root: "/tmp/root", Handle: "qa", OlderThan: "168h"})
	if err != nil {
		t.Fatalf("adaptDLQPurge: %v", err)
	}
	if got.Root != "/tmp/root" || got.Handle != "qa" || got.OlderThan != "168h" {
		t.Fatalf("DLQPurgeRequest mismatch: %+v", got)
	}
	if res.Handle != "qa" || res.OlderThan != "168h" || res.Output != "Purged 2 message(s)." {
		t.Fatalf("DLQ purge result mismatch: %+v", res)
	}
}

func TestAdaptDLQRetryAllCarriesHandle(t *testing.T) {
	var got DLQRetryAllRequest
	fn := adaptDLQRetryAll(func(req DLQRetryAllRequest) (DLQRetryAllResult, error) {
		got = req
		return DLQRetryAllResult{Handle: req.Handle, Output: "Retried 2 message(s)."}, nil
	})
	res, err := fn(dlqRetryAllOp{Root: "/tmp/root", Handle: "qa"})
	if err != nil {
		t.Fatalf("adaptDLQRetryAll: %v", err)
	}
	if got.Root != "/tmp/root" || got.Handle != "qa" {
		t.Fatalf("DLQRetryAllRequest mismatch: %+v", got)
	}
	if res.Handle != "qa" || res.Output != "Retried 2 message(s)." {
		t.Fatalf("DLQ retry-all result mismatch: %+v", res)
	}
}

func TestAdaptReceiptsAgentCarriesHandle(t *testing.T) {
	var got ReceiptsAgentRequest
	fn := adaptReceiptsAgent(func(req ReceiptsAgentRequest) (ReceiptsAgentResult, error) {
		got = req
		return ReceiptsAgentResult{Handle: req.Handle, Output: "receipt"}, nil
	})
	res, err := fn(receiptsAgentOp{Root: "/tmp/root", Handle: "qa"})
	if err != nil {
		t.Fatalf("adaptReceiptsAgent: %v", err)
	}
	if got.Root != "/tmp/root" || got.Handle != "qa" {
		t.Fatalf("ReceiptsAgentRequest mismatch: %+v", got)
	}
	if res.Handle != "qa" || res.Output != "receipt" {
		t.Fatalf("receipts result mismatch: %+v", res)
	}
}

func TestAdaptReceiptsWaitCarriesRequest(t *testing.T) {
	var got ReceiptsWaitRequest
	fn := adaptReceiptsWait(func(req ReceiptsWaitRequest) (ReceiptsWaitResult, error) {
		got = req
		return ReceiptsWaitResult{Handle: req.Handle, MsgID: req.MsgID, Stage: req.Stage, Timeout: req.Timeout, Output: "Receipt: drained"}, nil
	})
	res, err := fn(receiptsWaitOp{Root: "/tmp/root", Handle: "qa", MsgID: "msg_123", Stage: "drained", Timeout: "60s"})
	if err != nil {
		t.Fatalf("adaptReceiptsWait: %v", err)
	}
	if got.Root != "/tmp/root" || got.Handle != "qa" || got.MsgID != "msg_123" || got.Stage != "drained" || got.Timeout != "60s" {
		t.Fatalf("ReceiptsWaitRequest mismatch: %+v", got)
	}
	if res.Handle != "qa" || res.MsgID != "msg_123" || res.Stage != "drained" || res.Timeout != "60s" || res.Output != "Receipt: drained" {
		t.Fatalf("receipts wait result mismatch: %+v", res)
	}
}

func TestAdaptMessageWaitCarriesRequest(t *testing.T) {
	var got MessageWaitRequest
	fn := adaptMessageWait(func(req MessageWaitRequest) (MessageWaitResult, error) {
		got = req
		return MessageWaitResult{Handle: req.Handle, Timeout: req.Timeout, Output: "Receipt: drained"}, nil
	})
	res, err := fn(messageWaitOp{Root: "/tmp/root", Handle: "qa", Body: "Please check logs", Timeout: "60s"})
	if err != nil {
		t.Fatalf("adaptMessageWait: %v", err)
	}
	if got.Root != "/tmp/root" || got.Handle != "qa" || got.Body != "Please check logs" || got.Timeout != "60s" {
		t.Fatalf("MessageWaitRequest mismatch: %+v", got)
	}
	if res.Handle != "qa" || res.Timeout != "60s" || res.Output != "Receipt: drained" {
		t.Fatalf("message wait result mismatch: %+v", res)
	}
}

func TestAdaptThreadContextCarriesThread(t *testing.T) {
	var got ThreadContextRequest
	fn := adaptThreadContext(func(req ThreadContextRequest) (ThreadContextResult, error) {
		got = req
		return ThreadContextResult{Thread: req.Thread, Subject: req.Subject, Output: "transcript"}, nil
	})
	res, err := fn(threadContextOp{Root: "/tmp/root", Thread: "ask/ship", Subject: "Ship it?"})
	if err != nil {
		t.Fatalf("adaptThreadContext: %v", err)
	}
	if got.Root != "/tmp/root" || got.Thread != "ask/ship" || got.Subject != "Ship it?" {
		t.Fatalf("ThreadContextRequest mismatch: %+v", got)
	}
	if res.Thread != "ask/ship" || res.Subject != "Ship it?" || res.Output != "transcript" {
		t.Fatalf("thread context result mismatch: %+v", res)
	}
}

func TestAdaptAMQOpsCarriesRoot(t *testing.T) {
	var got AMQOpsRequest
	fn := adaptAMQOps(func(req AMQOpsRequest) (AMQOpsResult, error) {
		got = req
		return AMQOpsResult{Root: req.Root, Output: "ops"}, nil
	})
	res, err := fn(amqOpsOp{Root: "/tmp/root"})
	if err != nil {
		t.Fatalf("adaptAMQOps: %v", err)
	}
	if got.Root != "/tmp/root" {
		t.Fatalf("AMQOpsRequest mismatch: %+v", got)
	}
	if res.Root != "/tmp/root" || res.Output != "ops" {
		t.Fatalf("AMQ ops result mismatch: %+v", res)
	}
}

func TestAdaptAMQWhoCarriesRoot(t *testing.T) {
	var got AMQWhoRequest
	fn := adaptAMQWho(func(req AMQWhoRequest) (AMQWhoResult, error) {
		got = req
		return AMQWhoResult{Root: req.Root, Output: "who"}, nil
	})
	res, err := fn(amqWhoOp{Root: "/tmp/root"})
	if err != nil {
		t.Fatalf("adaptAMQWho: %v", err)
	}
	if got.Root != "/tmp/root" {
		t.Fatalf("AMQWhoRequest mismatch: %+v", got)
	}
	if res.Root != "/tmp/root" || res.Output != "who" {
		t.Fatalf("AMQ who result mismatch: %+v", res)
	}
}

func TestAdaptAMQEnvCarriesRoot(t *testing.T) {
	var got AMQEnvRequest
	fn := adaptAMQEnv(func(req AMQEnvRequest) (AMQEnvResult, error) {
		got = req
		return AMQEnvResult{Root: req.Root, Output: "env"}, nil
	})
	res, err := fn(amqEnvOp{Root: "/tmp/root"})
	if err != nil {
		t.Fatalf("adaptAMQEnv: %v", err)
	}
	if got.Root != "/tmp/root" {
		t.Fatalf("AMQEnvRequest mismatch: %+v", got)
	}
	if res.Root != "/tmp/root" || res.Output != "env" {
		t.Fatalf("AMQ env result mismatch: %+v", res)
	}
}

func TestAdaptAMQCleanupCarriesRootAndAge(t *testing.T) {
	var got AMQCleanupRequest
	fn := adaptAMQCleanup(func(req AMQCleanupRequest) (AMQCleanupResult, error) {
		got = req
		return AMQCleanupResult{Root: req.Root, TmpOlderThan: req.TmpOlderThan, Output: "removed"}, nil
	})
	res, err := fn(amqCleanupOp{Root: "/tmp/root", TmpOlderThan: "36h"})
	if err != nil {
		t.Fatalf("adaptAMQCleanup: %v", err)
	}
	if got.Root != "/tmp/root" || got.TmpOlderThan != "36h" {
		t.Fatalf("AMQCleanupRequest mismatch: %+v", got)
	}
	if res.Root != "/tmp/root" || res.TmpOlderThan != "36h" || res.Output != "removed" {
		t.Fatalf("AMQ cleanup result mismatch: %+v", res)
	}
}

func TestAdaptPresenceCarriesRoot(t *testing.T) {
	var got PresenceRequest
	fn := adaptPresence(func(req PresenceRequest) (PresenceResult, error) {
		got = req
		return PresenceResult{Root: req.Root, Output: "presence"}, nil
	})
	res, err := fn(presenceOp{Root: "/tmp/root"})
	if err != nil {
		t.Fatalf("adaptPresence: %v", err)
	}
	if got.Root != "/tmp/root" {
		t.Fatalf("PresenceRequest mismatch: %+v", got)
	}
	if res.Root != "/tmp/root" || res.Output != "presence" {
		t.Fatalf("presence result mismatch: %+v", res)
	}
}

func TestAdaptProjectDoctorCarriesProjectDir(t *testing.T) {
	var got ProjectDoctorRequest
	fn := adaptProjectDoctor(func(req ProjectDoctorRequest) (ProjectDoctorResult, error) {
		got = req
		return ProjectDoctorResult{ProjectDir: req.ProjectDir, Output: "doctor"}, nil
	})
	res, err := fn(projectDoctorOp{ProjectDir: "/tmp/team"})
	if err != nil {
		t.Fatalf("adaptProjectDoctor: %v", err)
	}
	if got.ProjectDir != "/tmp/team" {
		t.Fatalf("ProjectDoctorRequest mismatch: %+v", got)
	}
	if res.ProjectDir != "/tmp/team" || res.Output != "doctor" {
		t.Fatalf("project doctor result mismatch: %+v", res)
	}
}

func TestAdaptProjectHistoryCarriesProjectDir(t *testing.T) {
	var got ProjectHistoryRequest
	fn := adaptProjectHistory(func(req ProjectHistoryRequest) (ProjectHistoryResult, error) {
		got = req
		return ProjectHistoryResult{ProjectDir: req.ProjectDir, Output: "history"}, nil
	})
	res, err := fn(projectHistoryOp{ProjectDir: "/tmp/team"})
	if err != nil {
		t.Fatalf("adaptProjectHistory: %v", err)
	}
	if got.ProjectDir != "/tmp/team" {
		t.Fatalf("ProjectHistoryRequest mismatch: %+v", got)
	}
	if res.ProjectDir != "/tmp/team" || res.Output != "history" {
		t.Fatalf("project history result mismatch: %+v", res)
	}
}

func TestAdaptTeamRulesCarriesProjectDir(t *testing.T) {
	var got TeamRulesRequest
	fn := adaptTeamRules(func(req TeamRulesRequest) (TeamRulesResult, error) {
		got = req
		return TeamRulesResult{ProjectDir: req.ProjectDir, Path: "/tmp/team/.amq-squad/team-rules.md", Content: "rules"}, nil
	})
	res, err := fn(teamRulesOp{ProjectDir: "/tmp/team"})
	if err != nil {
		t.Fatalf("adaptTeamRules: %v", err)
	}
	if got.ProjectDir != "/tmp/team" {
		t.Fatalf("TeamRulesRequest mismatch: %+v", got)
	}
	if res.ProjectDir != "/tmp/team" || res.Path != "/tmp/team/.amq-squad/team-rules.md" || res.Content != "rules" {
		t.Fatalf("team rules result mismatch: %+v", res)
	}
}

func TestAdaptProjectResumePlanCarriesRequest(t *testing.T) {
	var got ProjectResumePlanRequest
	fn := adaptProjectResumePlan(func(req ProjectResumePlanRequest) (ProjectResumePlanResult, error) {
		got = req
		return ProjectResumePlanResult{ProjectDir: req.ProjectDir, Profile: req.Profile, Output: "plan"}, nil
	})
	res, err := fn(projectResumePlanOp{ProjectDir: "/tmp/team", Profile: "review"})
	if err != nil {
		t.Fatalf("adaptProjectResumePlan: %v", err)
	}
	if got.ProjectDir != "/tmp/team" || got.Profile != "review" {
		t.Fatalf("ProjectResumePlanRequest mismatch: %+v", got)
	}
	if res.ProjectDir != "/tmp/team" || res.Profile != "review" || res.Output != "plan" {
		t.Fatalf("project resume plan result mismatch: %+v", res)
	}
}

func TestAdaptForkPlanCarriesRequest(t *testing.T) {
	var got ForkPlanRequest
	fn := adaptForkPlan(func(req ForkPlanRequest) (ForkPlanResult, error) {
		got = req
		return ForkPlanResult{
			ProjectDir:  req.ProjectDir,
			Profile:     req.Profile,
			FromSession: req.FromSession,
			ToSession:   req.ToSession,
			Output:      "fork",
		}, nil
	})
	res, err := fn(forkPlanOp{ProjectDir: "/tmp/team", Profile: "review", FromSession: "issue-1", ToSession: "issue-2"})
	if err != nil {
		t.Fatalf("adaptForkPlan: %v", err)
	}
	if got.ProjectDir != "/tmp/team" || got.Profile != "review" || got.FromSession != "issue-1" || got.ToSession != "issue-2" {
		t.Fatalf("ForkPlanRequest mismatch: %+v", got)
	}
	if res.ProjectDir != "/tmp/team" || res.Profile != "review" || res.FromSession != "issue-1" || res.ToSession != "issue-2" || res.Output != "fork" {
		t.Fatalf("fork plan result mismatch: %+v", res)
	}
}

func TestAdaptBriefCarriesRequest(t *testing.T) {
	var got BriefRequest
	fn := adaptBrief(func(req BriefRequest) (BriefResult, error) {
		got = req
		return BriefResult{
			ProjectDir: req.ProjectDir,
			Session:    req.Session,
			Path:       "/tmp/team/.amq-squad/briefs/issue-1.md",
			Kind:       "real",
			Exists:     true,
			Content:    "# issue-1\n",
		}, nil
	})
	res, err := fn(briefOp{ProjectDir: "/tmp/team", Session: "issue-1"})
	if err != nil {
		t.Fatalf("adaptBrief: %v", err)
	}
	if got.ProjectDir != "/tmp/team" || got.Session != "issue-1" {
		t.Fatalf("BriefRequest mismatch: %+v", got)
	}
	if res.ProjectDir != "/tmp/team" || res.Session != "issue-1" || res.Kind != "real" || !res.Exists || res.Content != "# issue-1\n" {
		t.Fatalf("brief result mismatch: %+v", res)
	}
}

func TestAdaptBriefSeedCarriesRequest(t *testing.T) {
	var got BriefSeedRequest
	fn := adaptBriefSeed(func(req BriefSeedRequest) error {
		got = req
		return nil
	})
	if err := fn(briefSeedOp{ProjectDir: "/tmp/team", Session: "issue-1", SeedFrom: "issue:31", Force: true}); err != nil {
		t.Fatalf("adaptBriefSeed: %v", err)
	}
	if got.ProjectDir != "/tmp/team" || got.Session != "issue-1" || got.SeedFrom != "issue:31" || !got.Force {
		t.Fatalf("BriefSeedRequest mismatch: %+v", got)
	}
}

func TestParseNOCNewSessionInputSeedFrom(t *testing.T) {
	session, seedFrom, err := parseNOCNewSessionInput("issue-206 seed-from=issue:31")
	if err != nil {
		t.Fatal(err)
	}
	if session != "issue-206" || seedFrom != "issue:31" {
		t.Fatalf("parseNOCNewSessionInput = %q, %q", session, seedFrom)
	}

	session, seedFrom, err = parseNOCNewSessionInput("issue-207 seed=gh:owner/repo#31")
	if err != nil {
		t.Fatal(err)
	}
	if session != "issue-207" || seedFrom != "gh:owner/repo#31" {
		t.Fatalf("parseNOCNewSessionInput seed alias = %q, %q", session, seedFrom)
	}

	if _, _, err := parseNOCNewSessionInput("issue-208 seed-from=issue:not-a-number"); err == nil ||
		!strings.Contains(err.Error(), "issue:<n>") {
		t.Fatalf("invalid seed-from error = %v, want issue:<n> guidance", err)
	}
}

func TestParseNOCTeamSpecSelectionShortcuts(t *testing.T) {
	spec, err := parseNOCTeamSpec("2,9=codex")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Roles != "cto,qa" || spec.Binary != "qa=codex" {
		t.Fatalf("parseNOCTeamSpec numbers = %+v, want roles cto,qa and binary qa=codex", spec)
	}

	spec, err = parseNOCTeamSpec("all")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Roles != strings.Join(catalog.IDs(), ",") {
		t.Fatalf("parseNOCTeamSpec all roles = %q, want catalog IDs", spec.Roles)
	}

	spec, err = parseNOCTeamSpec("cto,qa,session=issue-96")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Roles != "cto,qa" || spec.Session != "issue-96" {
		t.Fatalf("parseNOCTeamSpec session = %+v, want roles cto,qa and session issue-96", spec)
	}

	if _, err := parseNOCTeamSpec("cto,session=Issue.96"); err == nil ||
		!strings.Contains(err.Error(), "session names allow") {
		t.Fatalf("invalid team session error = %v, want session guidance", err)
	}
}

func TestControl_StopConfirmGate(t *testing.T) {
	t.Run("S decline: lifecycle seam not called", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		called := false
		m.lifecycle = func(lifecycleOp) error { called = true; return nil }

		m, _ = nocPress(m, "S")
		if m.pending == nil {
			t.Fatal("S on a session should open the confirm overlay")
		}
		if !strings.Contains(m.pending.preview, "amq-squad stop --project /fake/proj/beta --all --session beta") {
			t.Errorf("stop overlay should preview the exact lifecycle command: %q", m.pending.preview)
		}
		m, _ = nocPress(m, "esc")
		if called {
			t.Error("declining stop must NOT call the lifecycle seam")
		}
	})

	t.Run("S confirm: lifecycle seam called for the right session", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		var ops []lifecycleOp
		m.lifecycle = func(op lifecycleOp) error { ops = append(ops, op); return nil }

		m, _ = nocPress(m, "S")
		m, _ = nocPress(m, "y")
		if len(ops) != 1 {
			t.Fatalf("confirmed stop should call the lifecycle seam once, got %d", len(ops))
		}
		if ops[0].Verb != lifecycleStop || ops[0].Session != "beta" {
			t.Errorf("stop lifecycleOp mismatch: %+v", ops[0])
		}
	})

	t.Run("R confirm: resume lifecycle seam called for the right session", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		var ops []lifecycleOp
		m.lifecycle = func(op lifecycleOp) error { ops = append(ops, op); return nil }

		m, _ = nocPress(m, "R")
		if m.pending == nil ||
			!strings.Contains(m.pending.preview, "amq-squad resume --project /fake/proj/beta --exec --target new-session --terminal-session amq-squad-beta --session beta") {
			t.Fatalf("R should preview the exact resume command, got %+v", m.pending)
		}
		m, _ = nocPress(m, "y")
		if len(ops) != 1 || ops[0].Verb != lifecycleResume || ops[0].Session != "beta" {
			t.Fatalf("confirmed resume should call lifecycle once for beta, got %+v", ops)
		}
	})

	t.Run("R confirm: named-profile session resumes the same profile", func(t *testing.T) {
		m := newControlModel(t)
		setPrimarySessionProfiles(m, "review")
		selectKind(t, m, nodeSession, "")
		var ops []lifecycleOp
		m.lifecycle = func(op lifecycleOp) error { ops = append(ops, op); return nil }

		m, _ = nocPress(m, "R")
		if m.pending == nil ||
			!strings.Contains(m.pending.preview, "amq-squad resume --project /fake/proj/beta --profile review --exec --target new-session") {
			t.Fatalf("R should preview resume with --profile review, got %+v", m.pending)
		}
		m, _ = nocPress(m, "y")
		if len(ops) != 1 || ops[0].Verb != lifecycleResume || ops[0].Profile != "review" || ops[0].Session != "beta" {
			t.Fatalf("confirmed resume should carry review profile for beta, got %+v", ops)
		}
	})

	t.Run("R on mixed-profile session asks which profile to resume", func(t *testing.T) {
		m := newControlModel(t)
		setPrimarySessionProfiles(m, "review", "release")
		selectKind(t, m, nodeSession, "")
		var ops []lifecycleOp
		m.lifecycle = func(op lifecycleOp) error { ops = append(ops, op); return nil }

		m, _ = nocPress(m, "R")
		if m.input == nil || m.input.kind != ctlResume || m.input.stage != 0 || !strings.Contains(m.input.hint, "release, review") {
			t.Fatalf("mixed-profile resume should ask for profile first, input=%+v", m.input)
		}
		m = typeControlText(t, m, "review")
		m, _ = nocPress(m, "enter")
		if m.pending == nil {
			t.Fatal("profile selection should open lifecycle confirm overlay")
		}
		if !strings.Contains(m.pending.preview, "amq-squad resume --project /fake/proj/beta --profile review --exec --target new-session") {
			t.Fatalf("resume preview should carry selected profile, got %q", m.pending.preview)
		}
		m, _ = nocPress(m, "y")
		if len(ops) != 1 {
			t.Fatalf("confirmed resume should call lifecycle once, got %d", len(ops))
		}
		if ops[0].Verb != lifecycleResume || ops[0].Profile != "review" || ops[0].Session != "beta" {
			t.Fatalf("confirmed resume should carry review profile for beta, got %+v", ops[0])
		}
		if len(ops[0].Agents) != 1 || ops[0].Agents[0] != "qa" {
			t.Fatalf("mixed-profile resume should affect only review-profile agents, got %+v", ops[0].Agents)
		}
	})

	t.Run("R rejects unknown profile before confirm", func(t *testing.T) {
		m := newControlModel(t)
		setPrimarySessionProfiles(m, "review", "release")
		selectKind(t, m, nodeSession, "")
		called := false
		m.lifecycle = func(lifecycleOp) error { called = true; return nil }

		m, _ = nocPress(m, "R")
		m = typeControlText(t, m, "banana")
		m, _ = nocPress(m, "enter")
		if called {
			t.Fatal("unknown profile must not call lifecycle")
		}
		if m.pending != nil {
			t.Fatalf("unknown profile must not open confirm, pending=%+v", m.pending)
		}
		if m.input == nil || m.input.stage != 0 || !strings.Contains(m.actNote, "unknown profile banana") {
			t.Fatalf("unknown profile should keep profile editor open with guidance, input=%+v note=%q", m.input, m.actNote)
		}
	})

	t.Run("X confirm: restart previews stop plus resume and calls lifecycle seam", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		var ops []lifecycleOp
		m.lifecycle = func(op lifecycleOp) error { ops = append(ops, op); return nil }

		m, _ = nocPress(m, "X")
		if m.pending == nil ||
			!strings.Contains(m.pending.preview, "amq-squad stop --project /fake/proj/beta --all --session beta && amq-squad resume --project /fake/proj/beta --exec --target new-session --terminal-session amq-squad-beta --session beta") {
			t.Fatalf("X should preview stop plus live resume, got %+v", m.pending)
		}
		m, _ = nocPress(m, "y")
		if len(ops) != 1 || ops[0].Verb != lifecycleRestart || ops[0].Session != "beta" {
			t.Fatalf("confirmed restart should call lifecycle once for beta, got %+v", ops)
		}
	})
}

// --- new session ---------------------------------------------------------

func TestControl_NewSessionConfirmGate(t *testing.T) {
	typeName := func(m *NOCModel, name string) *NOCModel {
		for _, r := range name {
			m, _ = nocPress(m, string(r))
		}
		return m
	}

	t.Run("N decline: new-session seam not called", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		called := false
		m.newSession = func(newSessionOp) error { called = true; return nil }

		m, _ = nocPress(m, "N")
		if m.input == nil || m.input.kind != ctlNewSession {
			t.Fatalf("N should open the new-session input editor, got %+v", m.input)
		}
		m = typeName(m, "issue-200")
		m, _ = nocPress(m, "enter")
		if m.pending == nil {
			t.Fatal("enter after a session name should open the confirm overlay")
		}
		if !strings.Contains(m.pending.preview, "amq-squad new session --project /fake/proj/beta --target new-session --terminal-session amq-squad-beta-issue-200 issue-200") {
			t.Errorf("new session overlay should preview the exact command, got %q", m.pending.preview)
		}
		m, _ = nocPress(m, "esc")
		if called {
			t.Error("declining new session must NOT call the new-session seam")
		}
	})

	t.Run("N confirm: new-session seam called for the selected project", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		var ops []newSessionOp
		m.newSession = func(op newSessionOp) error { ops = append(ops, op); return nil }

		m, _ = nocPress(m, "N")
		m = typeName(m, "issue-201")
		m, _ = nocPress(m, "enter")
		m, _ = nocPress(m, "y")
		if len(ops) != 1 {
			t.Fatalf("confirmed new session should call the seam once, got %d", len(ops))
		}
		if ops[0].ProjectDir != "/fake/proj/beta" || ops[0].Session != "issue-201" {
			t.Fatalf("new session op mismatch: %+v", ops[0])
		}
	})

	t.Run("N confirm: inline seed-from carries brief source", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		var ops []newSessionOp
		m.newSession = func(op newSessionOp) error { ops = append(ops, op); return nil }

		m, _ = nocPress(m, "N")
		m = typeName(m, "issue-206 seed-from=issue:31")
		m, _ = nocPress(m, "enter")
		if m.pending == nil {
			t.Fatal("seeded session input should open the confirm overlay")
		}
		if !strings.Contains(m.pending.preview, "--seed-from issue:31") ||
			!strings.Contains(m.pending.preview, "--terminal-session amq-squad-beta-issue-206 issue-206") {
			t.Fatalf("seeded session preview should include --seed-from and parsed session, got %q", m.pending.preview)
		}
		m, _ = nocPress(m, "y")
		if len(ops) != 1 {
			t.Fatalf("confirmed seeded session should call the seam once, got %d", len(ops))
		}
		if ops[0].ProjectDir != "/fake/proj/beta" || ops[0].Session != "issue-206" || ops[0].SeedFrom != "issue:31" {
			t.Fatalf("seeded new session op mismatch: %+v", ops[0])
		}
	})

	t.Run("N confirm: new-session can start the first session for a configured team", func(t *testing.T) {
		m := newControlModel(t)
		addConfiguredEmptyProject(m, "empty-team", "/fake/proj/empty-team")
		selectProject(t, m, "empty-team")
		var ops []newSessionOp
		m.newSession = func(op newSessionOp) error { ops = append(ops, op); return nil }

		m, _ = nocPress(m, "N")
		m = typeName(m, "issue-202")
		m, _ = nocPress(m, "enter")
		m, _ = nocPress(m, "y")
		if len(ops) != 1 {
			t.Fatalf("confirmed first session should call the seam once, got %d", len(ops))
		}
		if ops[0].ProjectDir != "/fake/proj/empty-team" || ops[0].Session != "issue-202" {
			t.Fatalf("new session op mismatch: %+v", ops[0])
		}
	})

	t.Run("N confirm: single named profile starts with --profile", func(t *testing.T) {
		m := newControlModel(t)
		addNamedConfiguredProject(m, "review-team", "/fake/proj/review-team", "review")
		selectProject(t, m, "review-team")
		var ops []newSessionOp
		m.newSession = func(op newSessionOp) error { ops = append(ops, op); return nil }

		m, _ = nocPress(m, "N")
		m = typeName(m, "issue-203")
		m, _ = nocPress(m, "enter")
		if m.pending == nil {
			t.Fatal("single named profile should open the confirm overlay")
		}
		if !strings.Contains(m.pending.preview, "amq-squad new session --project /fake/proj/review-team --profile review --target new-session --terminal-session amq-squad-review-team-issue-203 issue-203") {
			t.Fatalf("new session preview should carry --profile review, got %q", m.pending.preview)
		}
		m, _ = nocPress(m, "y")
		if len(ops) != 1 {
			t.Fatalf("confirmed named-profile session should call the seam once, got %d", len(ops))
		}
		if ops[0].ProjectDir != "/fake/proj/review-team" || ops[0].Profile != "review" || ops[0].Session != "issue-203" {
			t.Fatalf("new session op mismatch: %+v", ops[0])
		}
	})

	t.Run("N can choose among multiple named profiles", func(t *testing.T) {
		m := newControlModel(t)
		addNamedConfiguredProject(m, "many-profiles", "/fake/proj/many-profiles", "alpha", "review")
		selectProject(t, m, "many-profiles")
		var ops []newSessionOp
		m.newSession = func(op newSessionOp) error { ops = append(ops, op); return nil }

		m, _ = nocPress(m, "N")
		if m.input == nil || m.input.stage != 0 || !strings.Contains(m.input.hint, "alpha, review") {
			t.Fatalf("multiple profiles should ask for profile first, input=%+v", m.input)
		}
		m = typeName(m, "review")
		m, _ = nocPress(m, "enter")
		if m.input == nil || m.input.stage != 1 {
			t.Fatalf("after profile, N should ask for session, input=%+v", m.input)
		}
		m = typeName(m, "issue-204")
		m, _ = nocPress(m, "enter")
		if m.pending == nil {
			t.Fatal("session should open the confirm overlay")
		}
		if !strings.Contains(m.pending.preview, "amq-squad new session --project /fake/proj/many-profiles --profile review --target new-session --terminal-session amq-squad-many-profiles-issue-204 issue-204") {
			t.Fatalf("new session preview should carry selected --profile, got %q", m.pending.preview)
		}
		m, _ = nocPress(m, "y")
		if len(ops) != 1 {
			t.Fatalf("confirmed chosen-profile session should call the seam once, got %d", len(ops))
		}
		if ops[0].ProjectDir != "/fake/proj/many-profiles" || ops[0].Profile != "review" || ops[0].Session != "issue-204" {
			t.Fatalf("new session op mismatch: %+v", ops[0])
		}
	})

	t.Run("N can choose default when named profiles also exist", func(t *testing.T) {
		m := newControlModel(t)
		m.ms.Projects[0].Profiles = []string{"default", "review"}
		selectKind(t, m, nodeProject, "")
		var ops []newSessionOp
		m.newSession = func(op newSessionOp) error { ops = append(ops, op); return nil }

		m, _ = nocPress(m, "N")
		if m.input == nil || m.input.stage != 0 || !strings.Contains(m.input.hint, "default, review") {
			t.Fatalf("default plus named profiles should ask for profile first, input=%+v", m.input)
		}
		m = typeName(m, "default")
		m, _ = nocPress(m, "enter")
		m = typeName(m, "issue-205")
		m, _ = nocPress(m, "enter")
		if m.pending == nil {
			t.Fatal("session should open the confirm overlay")
		}
		if strings.Contains(m.pending.preview, "--profile") {
			t.Fatalf("default profile selection should not emit --profile, got %q", m.pending.preview)
		}
		m, _ = nocPress(m, "y")
		if len(ops) != 1 || ops[0].Profile != "" || ops[0].Session != "issue-205" {
			t.Fatalf("confirmed default-profile session mismatch: %+v", ops)
		}
	})

	t.Run("N rejects unknown profile before session", func(t *testing.T) {
		m := newControlModel(t)
		addNamedConfiguredProject(m, "many-profiles", "/fake/proj/many-profiles", "alpha", "review")
		selectProject(t, m, "many-profiles")
		called := false
		m.newSession = func(newSessionOp) error { called = true; return nil }

		m, _ = nocPress(m, "N")
		m = typeName(m, "banana")
		m, _ = nocPress(m, "enter")
		if called {
			t.Fatal("unknown profile must not call the new-session seam")
		}
		if m.pending != nil {
			t.Fatal("unknown profile must not open confirm")
		}
		if m.input == nil || m.input.stage != 0 || !strings.Contains(m.actNote, "unknown profile banana") {
			t.Fatalf("unknown profile should keep profile editor open with guidance, input=%+v note=%q", m.input, m.actNote)
		}
	})

	t.Run("N rejects invalid session name before confirm", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		called := false
		m.newSession = func(newSessionOp) error { called = true; return nil }

		m, _ = nocPress(m, "N")
		m = typeName(m, "Issue.201")
		m, _ = nocPress(m, "enter")
		if called {
			t.Fatal("invalid session name must not call the new-session seam")
		}
		if m.pending != nil {
			t.Fatal("invalid session name must not open a confirm overlay")
		}
		if m.input == nil || !strings.Contains(m.actNote, "session names allow") {
			t.Fatalf("invalid session should keep editor open with guidance, input=%+v note=%q", m.input, m.actNote)
		}
	})

	t.Run("N rejects invalid seed-from before confirm", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		called := false
		m.newSession = func(newSessionOp) error { called = true; return nil }

		m, _ = nocPress(m, "N")
		m = typeName(m, "issue-207 seed-from=issue:not-a-number")
		m, _ = nocPress(m, "enter")
		if called {
			t.Fatal("invalid seed-from must not call the new-session seam")
		}
		if m.pending != nil {
			t.Fatal("invalid seed-from must not open a confirm overlay")
		}
		if m.input == nil || !strings.Contains(m.actNote, "issue:<n>") {
			t.Fatalf("invalid seed-from should keep editor open with guidance, input=%+v note=%q", m.input, m.actNote)
		}
	})

	t.Run("N rejects existing session before confirm", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeProject, "")
		called := false
		m.newSession = func(newSessionOp) error { called = true; return nil }

		m, _ = nocPress(m, "N")
		m = typeName(m, "beta")
		m, _ = nocPress(m, "enter")
		if called {
			t.Fatal("existing session must not call the new-session seam")
		}
		if m.pending != nil {
			t.Fatal("existing session must not open a confirm overlay")
		}
		if m.input == nil || !strings.Contains(m.actNote, "session beta already exists") ||
			!strings.Contains(m.actNote, "press R") || !strings.Contains(m.actNote, "choose a new name") {
			t.Fatalf("existing session should keep editor open with guidance, input=%+v note=%q", m.input, m.actNote)
		}
	})

	t.Run("N rejects existing AMQ session directory before confirm", func(t *testing.T) {
		m := newControlModel(t)
		addConfiguredEmptyProject(m, "empty-team", "/fake/proj/empty-team")
		m.ms.Projects[len(m.ms.Projects)-1].SessionNames = []string{"reserved"}
		selectProject(t, m, "empty-team")
		called := false
		m.newSession = func(newSessionOp) error { called = true; return nil }

		m, _ = nocPress(m, "N")
		m = typeName(m, "reserved")
		m, _ = nocPress(m, "enter")
		if called {
			t.Fatal("existing AMQ session directory must not call the new-session seam")
		}
		if m.pending != nil {
			t.Fatal("existing AMQ session directory must not open a confirm overlay")
		}
		if m.input == nil || !strings.Contains(m.actNote, "session reserved already exists") {
			t.Fatalf("existing AMQ session directory should keep editor open with guidance, input=%+v note=%q", m.input, m.actNote)
		}
	})

	t.Run("N requires a team profile", func(t *testing.T) {
		m := newControlModel(t)
		addCandidateProject(m, "delta", "/fake/proj/delta")
		selectProject(t, m, "delta")
		called := false
		m.newSession = func(newSessionOp) error { called = true; return nil }

		m, _ = nocPress(m, "N")
		if called {
			t.Fatal("new session without a team profile must not call the seam")
		}
		if m.input != nil || m.pending != nil {
			t.Fatalf("new session without team profile should not open input/confirm, input=%+v pending=%+v", m.input, m.pending)
		}
		if !strings.Contains(m.actNote, "press T") {
			t.Fatalf("new session without team profile should point at T, note=%q", m.actNote)
		}
	})
}

// --- new team ------------------------------------------------------------

func TestControl_NewTeamConfirmGate(t *testing.T) {
	typeRoles := func(m *NOCModel, roles string) *NOCModel {
		for _, r := range roles {
			m, _ = nocPress(m, string(r))
		}
		return m
	}
	typeText := func(m *NOCModel, text string) *NOCModel {
		for _, r := range text {
			m, _ = nocPress(m, string(r))
		}
		return m
	}

	t.Run("T decline: new-team seam not called", func(t *testing.T) {
		m := newControlModel(t)
		addCandidateProject(m, "delta", "/fake/proj/delta")
		selectProject(t, m, "delta")
		called := false
		m.newTeam = func(newTeamOp) error { called = true; return nil }

		m, _ = nocPress(m, "T")
		if m.input == nil || m.input.kind != ctlNewTeam {
			t.Fatalf("T should open the new-team input editor, got %+v", m.input)
		}
		if !strings.Contains(m.input.hint, "2,9") || !strings.Contains(m.input.hint, "all") {
			t.Fatalf("new-team editor should hint role shortcuts, got %q", m.input.hint)
		}
		m = typeRoles(m, "cto,qa")
		m, _ = nocPress(m, "enter")
		if m.pending == nil {
			t.Fatal("enter after roles should open the confirm overlay")
		}
		if !strings.Contains(m.pending.preview, "amq-squad new team --project /fake/proj/delta --roles cto,qa") {
			t.Errorf("new team overlay should preview the exact command, got %q", m.pending.preview)
		}
		if !strings.Contains(m.pending.preview, "--sync") {
			t.Errorf("NOC new team should preview complete setup with --sync, got %q", m.pending.preview)
		}
		m, _ = nocPress(m, "esc")
		if called {
			t.Error("declining new team must NOT call the new-team seam")
		}
	})

	t.Run("T confirm: new-team seam called for the selected project", func(t *testing.T) {
		m := newControlModel(t)
		addCandidateProject(m, "delta", "/fake/proj/delta")
		selectProject(t, m, "delta")
		var ops []newTeamOp
		m.newTeam = func(op newTeamOp) error { ops = append(ops, op); return nil }

		m, _ = nocPress(m, "T")
		m = typeRoles(m, "CTO,qa,cto")
		m, _ = nocPress(m, "enter")
		m, _ = nocPress(m, "y")
		if len(ops) != 1 {
			t.Fatalf("confirmed new team should call the seam once, got %d", len(ops))
		}
		if ops[0].ProjectDir != "/fake/proj/delta" || ops[0].Roles != "cto,qa" || !ops[0].Sync {
			t.Fatalf("new team op mismatch: %+v", ops[0])
		}
	})

	t.Run("T accepts role=binary overrides", func(t *testing.T) {
		m := newControlModel(t)
		addCandidateProject(m, "delta", "/fake/proj/delta")
		selectProject(t, m, "delta")
		var ops []newTeamOp
		m.newTeam = func(op newTeamOp) error { ops = append(ops, op); return nil }

		m, _ = nocPress(m, "T")
		m = typeRoles(m, "cto=claude,qa=codex,cto=codex")
		m, _ = nocPress(m, "enter")
		if m.pending == nil {
			t.Fatal("team spec should open the confirm overlay")
		}
		if !strings.Contains(m.pending.preview, "amq-squad new team --project /fake/proj/delta --roles cto,qa --binary cto=codex,qa=codex") {
			t.Fatalf("new team preview should carry binary overrides, got %q", m.pending.preview)
		}
		m, _ = nocPress(m, "y")
		if len(ops) != 1 {
			t.Fatalf("confirmed new team should call the seam once, got %d", len(ops))
		}
		if ops[0].Roles != "cto,qa" || ops[0].Binary != "cto=codex,qa=codex" {
			t.Fatalf("new team op mismatch: %+v", ops[0])
		}
	})

	t.Run("T accepts initial session for a default team", func(t *testing.T) {
		m := newControlModel(t)
		addCandidateProject(m, "delta", "/fake/proj/delta")
		selectProject(t, m, "delta")
		var ops []newTeamOp
		m.newTeam = func(op newTeamOp) error { ops = append(ops, op); return nil }

		m, _ = nocPress(m, "T")
		m = typeRoles(m, "cto,qa,session=issue-96")
		m, _ = nocPress(m, "enter")
		if m.pending == nil {
			t.Fatal("team spec with session should open the confirm overlay")
		}
		if !strings.Contains(m.pending.preview, "amq-squad new team --project /fake/proj/delta --roles cto,qa --session issue-96 --sync") {
			t.Fatalf("new team preview should carry initial session, got %q", m.pending.preview)
		}
		m, _ = nocPress(m, "y")
		if len(ops) != 1 {
			t.Fatalf("confirmed new team should call the seam once, got %d", len(ops))
		}
		if ops[0].Roles != "cto,qa" || ops[0].Session != "issue-96" {
			t.Fatalf("new team op mismatch: %+v", ops[0])
		}
	})

	t.Run("T carries role=binary overrides for a named profile", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeProject, "")
		var ops []newTeamOp
		m.newTeam = func(op newTeamOp) error { ops = append(ops, op); return nil }

		m, _ = nocPress(m, "T")
		m = typeText(m, "review")
		m, _ = nocPress(m, "enter")
		m = typeRoles(m, "cto=codex,qa=claude")
		m, _ = nocPress(m, "enter")
		if m.pending == nil {
			t.Fatal("named profile team spec should open the confirm overlay")
		}
		if !strings.Contains(m.pending.preview, "amq-squad new profile review --project /fake/proj/beta --roles cto,qa --binary cto=codex,qa=claude") {
			t.Fatalf("named team preview should carry profile and binary overrides, got %q", m.pending.preview)
		}
		m, _ = nocPress(m, "y")
		if len(ops) != 1 {
			t.Fatalf("confirmed named team should call the seam once, got %d", len(ops))
		}
		if ops[0].Profile != "review" || ops[0].Roles != "cto,qa" || ops[0].Binary != "cto=codex,qa=claude" {
			t.Fatalf("new team op mismatch: %+v", ops[0])
		}
	})

	t.Run("T carries initial session for a named profile", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeProject, "")
		var ops []newTeamOp
		m.newTeam = func(op newTeamOp) error { ops = append(ops, op); return nil }

		m, _ = nocPress(m, "T")
		m = typeText(m, "review")
		m, _ = nocPress(m, "enter")
		m = typeRoles(m, "cto,qa,session=issue-97")
		m, _ = nocPress(m, "enter")
		if m.pending == nil {
			t.Fatal("named team spec with session should open the confirm overlay")
		}
		if !strings.Contains(m.pending.preview, "amq-squad new profile review --project /fake/proj/beta --roles cto,qa --session issue-97 --sync") {
			t.Fatalf("named team preview should carry initial session, got %q", m.pending.preview)
		}
		m, _ = nocPress(m, "y")
		if len(ops) != 1 {
			t.Fatalf("confirmed named team should call the seam once, got %d", len(ops))
		}
		if ops[0].Profile != "review" || ops[0].Roles != "cto,qa" || ops[0].Session != "issue-97" {
			t.Fatalf("new team op mismatch: %+v", ops[0])
		}
	})

	t.Run("T rejects invalid initial session before confirm", func(t *testing.T) {
		m := newControlModel(t)
		addCandidateProject(m, "delta", "/fake/proj/delta")
		selectProject(t, m, "delta")
		called := false
		m.newTeam = func(newTeamOp) error { called = true; return nil }

		m, _ = nocPress(m, "T")
		m = typeRoles(m, "cto,session=Issue.96")
		m, _ = nocPress(m, "enter")
		if called {
			t.Fatal("invalid initial session must not call the new-team seam")
		}
		if m.pending != nil {
			t.Fatal("invalid initial session must not open a confirm overlay")
		}
		if m.input == nil || !strings.Contains(m.actNote, "session names allow") {
			t.Fatalf("invalid initial session should keep editor open with guidance, input=%+v note=%q", m.input, m.actNote)
		}
	})

	t.Run("T rejects empty binary override before confirm", func(t *testing.T) {
		m := newControlModel(t)
		addCandidateProject(m, "delta", "/fake/proj/delta")
		selectProject(t, m, "delta")
		called := false
		m.newTeam = func(newTeamOp) error { called = true; return nil }

		m, _ = nocPress(m, "T")
		m = typeRoles(m, "cto=")
		m, _ = nocPress(m, "enter")
		if called {
			t.Fatal("empty binary override must not call the new-team seam")
		}
		if m.pending != nil {
			t.Fatal("empty binary override must not open a confirm overlay")
		}
		if m.input == nil || !strings.Contains(m.actNote, "binary cannot be empty") {
			t.Fatalf("empty binary should keep editor open with guidance, input=%+v note=%q", m.input, m.actNote)
		}
	})

	t.Run("T rejects unknown role before confirm", func(t *testing.T) {
		m := newControlModel(t)
		addCandidateProject(m, "delta", "/fake/proj/delta")
		selectProject(t, m, "delta")
		called := false
		m.newTeam = func(newTeamOp) error { called = true; return nil }

		m, _ = nocPress(m, "T")
		m = typeRoles(m, "cto,banana")
		m, _ = nocPress(m, "enter")
		if called {
			t.Fatal("invalid roles must not call the new-team seam")
		}
		if m.pending != nil {
			t.Fatal("invalid roles must not open a confirm overlay")
		}
		if m.input == nil || !strings.Contains(m.actNote, "banana") {
			t.Fatalf("invalid roles should keep editor open with guidance, input=%+v note=%q", m.input, m.actNote)
		}
	})

	t.Run("T on an existing default team creates a named profile", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeProject, "")
		var ops []newTeamOp
		m.newTeam = func(op newTeamOp) error { ops = append(ops, op); return nil }

		m, _ = nocPress(m, "T")
		if m.input == nil || m.input.stage != 0 {
			t.Fatalf("T on existing default team should ask for profile first, input=%+v", m.input)
		}
		m = typeText(m, "review")
		m, _ = nocPress(m, "enter")
		if m.input == nil || m.input.stage != 1 {
			t.Fatalf("after profile, T should ask for roles, input=%+v", m.input)
		}
		if !strings.Contains(m.input.hint, "2,9") || !strings.Contains(m.input.hint, "all") {
			t.Fatalf("role stage should hint shortcuts, got %q", m.input.hint)
		}
		m = typeRoles(m, "cto,qa")
		m, _ = nocPress(m, "enter")
		if m.pending == nil {
			t.Fatal("roles should open the named-team confirm overlay")
		}
		if !strings.Contains(m.pending.preview, "amq-squad new profile review --project /fake/proj/beta --roles cto,qa") {
			t.Fatalf("named team preview should carry named profile command, got %q", m.pending.preview)
		}
		m, _ = nocPress(m, "y")
		if len(ops) != 1 {
			t.Fatalf("confirmed named team should call the seam once, got %d", len(ops))
		}
		if ops[0].ProjectDir != "/fake/proj/beta" || ops[0].Profile != "review" || ops[0].Roles != "cto,qa" {
			t.Fatalf("new team op mismatch: %+v", ops[0])
		}
	})

	t.Run("T on named-only team can create the default profile", func(t *testing.T) {
		m := newControlModel(t)
		addNamedConfiguredProject(m, "review-team", "/fake/proj/review-team", "review")
		selectProject(t, m, "review-team")
		var ops []newTeamOp
		m.newTeam = func(op newTeamOp) error { ops = append(ops, op); return nil }

		m, _ = nocPress(m, "T")
		if m.input == nil || m.input.stage != 0 || !strings.Contains(m.input.hint, "review") {
			t.Fatalf("T on named-only team should ask for profile first, input=%+v", m.input)
		}
		m = typeText(m, "default")
		m, _ = nocPress(m, "enter")
		if m.input == nil || m.input.stage != 1 {
			t.Fatalf("after default profile, T should ask for roles, input=%+v", m.input)
		}
		m = typeRoles(m, "cto,qa")
		m, _ = nocPress(m, "enter")
		if m.pending == nil {
			t.Fatal("roles should open the default-team confirm overlay")
		}
		if strings.Contains(m.pending.preview, "--profile") {
			t.Fatalf("default profile creation should not emit --profile, got %q", m.pending.preview)
		}
		m, _ = nocPress(m, "y")
		if len(ops) != 1 {
			t.Fatalf("confirmed default team should call the seam once, got %d", len(ops))
		}
		if ops[0].ProjectDir != "/fake/proj/review-team" || ops[0].Profile != "" || ops[0].Roles != "cto,qa" {
			t.Fatalf("new team op mismatch: %+v", ops[0])
		}
	})

	t.Run("T on named-only team rejects duplicate named profile", func(t *testing.T) {
		m := newControlModel(t)
		addNamedConfiguredProject(m, "review-team", "/fake/proj/review-team", "review")
		selectProject(t, m, "review-team")
		called := false
		m.newTeam = func(newTeamOp) error { called = true; return nil }

		m, _ = nocPress(m, "T")
		m = typeText(m, "review")
		m, _ = nocPress(m, "enter")
		if called {
			t.Fatal("duplicate named profile must not call the new-team seam")
		}
		if m.pending != nil {
			t.Fatal("duplicate named profile must not open a confirm overlay")
		}
		if m.input == nil || m.input.stage != 0 || !strings.Contains(m.actNote, "profile review already exists") {
			t.Fatalf("duplicate named profile should keep profile editor open with guidance, input=%+v note=%q", m.input, m.actNote)
		}
	})

	t.Run("T rejects duplicate profile before roles", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeProject, "")
		called := false
		m.newTeam = func(newTeamOp) error { called = true; return nil }

		m, _ = nocPress(m, "T")
		m = typeText(m, "default")
		m, _ = nocPress(m, "enter")
		if called {
			t.Fatal("duplicate profile must not call the new-team seam")
		}
		if m.pending != nil {
			t.Fatal("duplicate profile must not open a confirm overlay")
		}
		if m.input == nil || m.input.stage != 0 || !strings.Contains(m.actNote, "already exists") {
			t.Fatalf("duplicate profile should keep profile editor open with guidance, input=%+v note=%q", m.input, m.actNote)
		}
	})
}

func TestControl_DeleteTeamConfirmGate(t *testing.T) {
	t.Run("delete key on single profile opens preview directly and decline does not call seam", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeProject, "")
		called := false
		m.teamDelete = func(teamDeleteOp) error { called = true; return nil }

		m, cmd := nocPress(m, "delete")
		if cmd != nil {
			t.Fatal("delete key setup should not request a rebuild")
		}
		if m.input != nil {
			t.Fatalf("single-profile delete should not ask for mode, input=%+v", m.input)
		}
		if !strings.Contains(m.pending.preview, "amq-squad team rm --project /fake/proj/beta --yes") {
			t.Fatalf("delete-team preview mismatch: %q", m.pending.preview)
		}
		if len(m.pending.affected) != 1 || !strings.Contains(m.pending.affected[0], ".amq-squad/team.json") {
			t.Fatalf("delete-team affected paths mismatch: %+v", m.pending.affected)
		}
		m, _ = nocPress(m, "esc")
		if called {
			t.Fatal("declining delete-team must not call the seam")
		}
	})

	t.Run("mac delete key on session row opens same team preview", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		called := false
		m.teamDelete = func(teamDeleteOp) error { called = true; return nil }

		m, cmd := nocPress(m, "backspace")
		if cmd != nil {
			t.Fatal("delete key setup should not request a rebuild")
		}
		if m.input != nil {
			t.Fatalf("single-profile session delete should not ask for mode, input=%+v", m.input)
		}
		if m.pending == nil || !strings.Contains(m.pending.preview, "amq-squad team rm --project /fake/proj/beta --yes") {
			t.Fatalf("delete-team preview mismatch: pending=%+v", m.pending)
		}
		if called {
			t.Fatal("opening delete preview must not call the seam")
		}
	})

	t.Run("multi profile can choose one profile with shortcut before preview", func(t *testing.T) {
		m := newControlModel(t)
		project := noc.ProjectSnapshot{
			Project:        "many-profiles",
			Dir:            "/fake/proj/many-profiles",
			TeamConfigured: true,
			DefaultTeam:    true,
			Profiles:       []string{"default", "review", "release"},
		}
		m.ms.Projects = append(m.ms.Projects, project)
		called := false
		m.teamDelete = func(teamDeleteOp) error { called = true; return nil }

		if cmd := m.beginDeleteTeamForProject(project); cmd != nil {
			t.Fatal("delete-team setup should not request a rebuild")
		}
		if m.input == nil || m.input.kind != ctlDeleteTeam || !strings.Contains(m.input.hint, "1 one profile") {
			t.Fatalf("multi-profile delete should ask for a plain choice, input=%+v", m.input)
		}
		m = typeControlText(t, m, "1")
		m, _ = nocPress(m, "enter")
		if m.input == nil || m.input.stage != 1 || !strings.Contains(m.input.hint, "default, review, release") {
			t.Fatalf("one-profile delete should ask which profile, input=%+v", m.input)
		}
		m = typeControlText(t, m, "review")
		m, _ = nocPress(m, "enter")
		if called {
			t.Fatal("typing profile should only open preview, not call the seam")
		}
		if m.pending == nil || !strings.Contains(m.pending.preview, "amq-squad team rm --project /fake/proj/many-profiles --profile review --yes") {
			t.Fatalf("delete-team preview mismatch: pending=%+v", m.pending)
		}
		if len(m.pending.affected) != 1 || !strings.Contains(m.pending.affected[0], ".amq-squad/teams/review.json") {
			t.Fatalf("delete-team affected paths mismatch: %+v", m.pending.affected)
		}
	})

	t.Run("all team config requires project name before preview and confirm calls once", func(t *testing.T) {
		m := newControlModel(t)
		project := noc.ProjectSnapshot{
			Project:        "many-profiles",
			Dir:            "/fake/proj/many-profiles",
			TeamConfigured: true,
			DefaultTeam:    true,
			Profiles:       []string{"default", "review", "release"},
		}
		m.ms.Projects = append(m.ms.Projects, project)
		var ops []teamDeleteOp
		m.teamDelete = func(op teamDeleteOp) error {
			ops = append(ops, op)
			return nil
		}

		cmd := m.beginDeleteTeamForProject(project)
		if cmd != nil {
			t.Fatal("delete-team setup should not request a rebuild")
		}
		if m.input == nil || m.input.kind != ctlDeleteTeam || !strings.Contains(m.input.hint, "2 all team config") {
			t.Fatalf("multi-profile delete-team should ask for a plain choice, input=%+v", m.input)
		}
		m = typeControlText(t, m, "2")
		m, _ = nocPress(m, "enter")
		if m.input == nil || m.input.stage != 1 || !strings.Contains(m.input.hint, "many-profiles") {
			t.Fatalf("all-team-config should ask for project name, input=%+v", m.input)
		}
		m = typeControlText(t, m, "wrong")
		m, _ = nocPress(m, "enter")
		if m.pending != nil || len(ops) != 0 || !strings.Contains(m.actNote, "project name did not match") {
			t.Fatalf("wrong project name should block preview, pending=%+v ops=%d note=%q", m.pending, len(ops), m.actNote)
		}
		m.input.body = ""
		m = typeControlText(t, m, "many-profiles")
		m, _ = nocPress(m, "enter")
		if len(ops) != 0 {
			t.Fatal("typing project name should only open preview, not call the seam")
		}
		if m.pending == nil || !strings.Contains(m.pending.preview, "amq-squad team rm --project /fake/proj/many-profiles --all-profiles --yes") {
			t.Fatalf("delete-team preview mismatch: pending=%+v", m.pending)
		}
		if len(m.pending.affected) != 3 || !strings.Contains(strings.Join(m.pending.affected, "\n"), ".amq-squad/teams/review.json") {
			t.Fatalf("all-team-config affected paths mismatch: %+v", m.pending.affected)
		}
		m, _ = nocPress(m, "y")
		if len(ops) != 1 {
			t.Fatalf("confirming should call the delete-team seam once, got %d", len(ops))
		}
		if ops[0].ProjectDir != "/fake/proj/many-profiles" || !ops[0].AllProfiles {
			t.Fatalf("delete-team op mismatch: %+v", ops[0])
		}
	})
}

// --- focus / open ('o') --------------------------------------------------

func TestControl_FocusRunningCallsSwitch(t *testing.T) {
	m := newControlModel(t)
	selectKind(t, m, nodeSession, "")
	var gotTarget noc.TmuxTarget
	called := false
	m.switchTo = func(tt noc.TmuxTarget) error { called = true; gotTarget = tt; return nil }
	// A live tmux window for session "beta" exists.
	m.panes = func() ([]noc.TmuxPane, error) {
		return []noc.TmuxPane{{Session: "beta", Window: "0", Pane: "1", Command: "claude", CWD: "/fake/proj/beta"}}, nil
	}

	// 'o' is now confirm-gated (QA-2/QA-4b): it opens a READ-ONLY focus confirm
	// overlay (jumpPending, NOT the mutating pending) and does not focus yet.
	m, _ = nocPress(m, "o")
	if m.jumpPending == nil {
		t.Fatal("o should open the read-only focus confirm overlay")
	}
	if m.pending != nil {
		t.Error("focus is read-only: it must never open the MUTATING confirm overlay")
	}
	if called {
		t.Fatal("o must NOT call the switch seam before confirm")
	}
	// y confirms: the switch seam fires with the right session.
	m, _ = nocPress(m, "y")
	if !called {
		t.Fatal("y on the focus confirm should call the switch seam")
	}
	if gotTarget.Session != "beta" {
		t.Errorf("focus targeted the wrong session: %+v", gotTarget)
	}
}

func TestControl_FocusStoppedSuggestsUpCallsNothing(t *testing.T) {
	m := newControlModel(t)
	selectKind(t, m, nodeSession, "")
	switched := false
	lifecycleCalled := false
	sent := false
	m.switchTo = func(noc.TmuxTarget) error { switched = true; return nil }
	m.lifecycle = func(lifecycleOp) error { lifecycleCalled = true; return nil }
	m.sendOp = func(act.OpMessage) error { sent = true; return nil }
	// No tmux windows: the squad is not running.
	m.panes = func() ([]noc.TmuxPane, error) { return nil, nil }

	// 'o' opens the read-only focus confirm overlay; confirming with y then finds
	// no running window and sets the suggest-up note (still NOTHING is called).
	m, _ = nocPress(m, "o")
	if m.pending != nil {
		t.Error("focus is read-only: it must never open the MUTATING confirm overlay")
	}
	if m.jumpPending == nil {
		t.Fatal("o should open the read-only focus confirm overlay")
	}
	m, _ = nocPress(m, "y")
	if switched || lifecycleCalled || sent {
		t.Error("o on a stopped squad must call NOTHING (no switch, no lifecycle, no send)")
	}
	if !strings.Contains(m.actNote, "team not running") ||
		!strings.Contains(m.actNote, "amq-squad new session --project /fake/proj/beta <name>") {
		t.Errorf("o on a stopped squad should set the suggest-up note, got %q", m.actNote)
	}
}

// --- wrong-node no-ops ---------------------------------------------------

func TestControl_WrongNodeNoOps(t *testing.T) {
	// 'S' (stop) on an AGENT row is a no-op note, no lifecycle call.
	t.Run("S on an agent row is a no-op", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeAgent, "qa")
		called := false
		m.lifecycle = func(lifecycleOp) error { called = true; return nil }
		m, _ = nocPress(m, "S")
		if called {
			t.Error("S on an agent row must NOT call the lifecycle seam")
		}
		if m.pending != nil {
			t.Error("S on an agent row must NOT open a confirm overlay")
		}
		if m.actNote == "" {
			t.Error("S on the wrong node should leave a guidance note")
		}
	})

	// 'a' (approve) on a PROJECT with no needs-you is a no-op note.
	t.Run("a on a project with no needs-you thread is a no-op", func(t *testing.T) {
		m := newControlModel(t)
		// Drop the needs-you thread so nothing needs the operator.
		m.ms.Projects[0].Snap.Sessions[0].Coordination = state.Coordination{}
		m.ms.Projects[0].Snap.Sessions[0].Rollup = state.TriageRollup{}
		selectKind(t, m, nodeProject, "")
		sent := false
		m.sendOp = func(act.OpMessage) error { sent = true; return nil }
		m, _ = nocPress(m, "a")
		if sent {
			t.Error("a on a project must NOT call the send seam")
		}
		if m.pending != nil {
			t.Error("a on a project with no needs-you must NOT open a confirm overlay")
		}
		if m.actNote == "" {
			t.Error("a on the wrong node should leave a guidance note")
		}
	})

	// 'r' (reply) on a PROJECT with no needs-you is a no-op note.
	t.Run("r on a project with no needs-you thread is a no-op", func(t *testing.T) {
		m := newControlModel(t)
		m.ms.Projects[0].Snap.Sessions[0].Coordination = state.Coordination{}
		m.ms.Projects[0].Snap.Sessions[0].Rollup = state.TriageRollup{}
		selectKind(t, m, nodeProject, "")
		sent := false
		m.sendOp = func(act.OpMessage) error { sent = true; return nil }
		m, _ = nocPress(m, "r")
		if sent {
			t.Error("r on a project must NOT call the send seam")
		}
		if m.input != nil || m.pending != nil {
			t.Error("r on a project with no needs-you must NOT open an editor or confirm overlay")
		}
		if m.actNote == "" {
			t.Error("r on the wrong node should leave a guidance note")
		}
	})

	// 'v' (read) on a PROJECT with no needs-you is a no-op note.
	t.Run("v on a project with no needs-you thread is a no-op", func(t *testing.T) {
		m := newControlModel(t)
		m.ms.Projects[0].Snap.Sessions[0].Coordination = state.Coordination{}
		m.ms.Projects[0].Snap.Sessions[0].Rollup = state.TriageRollup{}
		selectKind(t, m, nodeProject, "")
		read := false
		m.readNeedsYou = func(readNeedsYouOp) (readNeedsYouResult, error) {
			read = true
			return readNeedsYouResult{}, nil
		}
		m, _ = nocPress(m, "v")
		if read {
			t.Error("v on a project must NOT call the read seam")
		}
		if m.input != nil || m.pending != nil {
			t.Error("v on a project with no needs-you must NOT open an editor or confirm overlay")
		}
		if m.actNote == "" {
			t.Error("v on the wrong node should leave a guidance note")
		}
	})

	// 'i' (inbox) on a SESSION row (not an agent) is a no-op note.
	t.Run("i on a session row is a no-op", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		listed := false
		m.inboxAgent = func(inboxAgentOp) (inboxAgentResult, error) {
			listed = true
			return inboxAgentResult{}, nil
		}
		m, _ = nocPress(m, "i")
		if listed || m.input != nil || m.pending != nil || m.inboxResult != nil {
			t.Error("i on a session row must be a no-op (no editor, no overlay, no list)")
		}
		if m.actNote == "" {
			t.Error("i on the wrong node should leave a guidance note")
		}
	})

	// 'm' (message) on a SESSION row (not an agent) is a no-op note.
	t.Run("m on a session row is a no-op", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		sent := false
		m.sendOp = func(act.OpMessage) error { sent = true; return nil }
		m, _ = nocPress(m, "m")
		if sent || m.input != nil || m.pending != nil {
			t.Error("m on a session row must be a no-op (no editor, no overlay, no send)")
		}
		if m.actNote == "" {
			t.Error("m on the wrong node should leave a guidance note")
		}
	})

	// 'd' (drain) on a SESSION row (not an agent) is a no-op note.
	t.Run("d on a session row is a no-op", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeSession, "")
		drained := false
		m.drainAgent = func(drainAgentOp) (drainAgentResult, error) {
			drained = true
			return drainAgentResult{}, nil
		}
		m, _ = nocPress(m, "d")
		if drained || m.input != nil || m.pending != nil {
			t.Error("d on a session row must be a no-op (no editor, no overlay, no drain)")
		}
		if m.actNote == "" {
			t.Error("d on the wrong node should leave a guidance note")
		}
	})
}

// --- read-only default ---------------------------------------------------

func TestControl_ReadOnlyDefaultNoMutatingSeam(t *testing.T) {
	// Plain nav / peek / filter keys must never reach a mutating seam.
	m := newControlModel(t)
	sent := false
	lifecycleCalled := false
	newSessionCalled := false
	newTeamCalled := false
	teamDeleteCalled := false
	readCalled := false
	drainCalled := false
	inboxCalled := false
	dlqCalled := false
	contextCalled := false
	m.sendOp = func(act.OpMessage) error { sent = true; return nil }
	m.lifecycle = func(lifecycleOp) error { lifecycleCalled = true; return nil }
	m.newSession = func(newSessionOp) error { newSessionCalled = true; return nil }
	m.newTeam = func(newTeamOp) error { newTeamCalled = true; return nil }
	m.teamDelete = func(teamDeleteOp) error { teamDeleteCalled = true; return nil }
	m.readNeedsYou = func(readNeedsYouOp) (readNeedsYouResult, error) {
		readCalled = true
		return readNeedsYouResult{}, nil
	}
	m.drainAgent = func(drainAgentOp) (drainAgentResult, error) {
		drainCalled = true
		return drainAgentResult{}, nil
	}
	m.inboxAgent = func(inboxAgentOp) (inboxAgentResult, error) {
		inboxCalled = true
		return inboxAgentResult{}, nil
	}
	m.dlqAgent = func(dlqAgentOp) (dlqAgentResult, error) {
		dlqCalled = true
		return dlqAgentResult{}, nil
	}
	m.threadContext = func(threadContextOp) (threadContextResult, error) {
		contextCalled = true
		return threadContextResult{}, nil
	}

	for _, k := range []string{"j", "k", "down", "up", "right", "left", "enter", "h", "t", "g", "/"} {
		m, _ = nocPress(m, k)
	}
	if sent {
		t.Error("nav/peek/filter keys must NOT call the send seam")
	}
	if lifecycleCalled {
		t.Error("nav/peek/filter keys must NOT call the lifecycle seam")
	}
	if newSessionCalled {
		t.Error("nav/peek/filter keys must NOT call the new-session seam")
	}
	if newTeamCalled {
		t.Error("nav/peek/filter keys must NOT call the new-team seam")
	}
	if teamDeleteCalled {
		t.Error("nav/peek/filter keys must NOT call the delete-team seam")
	}
	if readCalled {
		t.Error("nav/peek/filter keys must NOT call the read seam")
	}
	if drainCalled {
		t.Error("nav/peek/filter keys must NOT call the drain seam")
	}
	if inboxCalled {
		t.Error("nav/peek/filter keys must NOT call the inbox seam")
	}
	if dlqCalled {
		t.Error("nav/peek/filter keys must NOT call the DLQ seam")
	}
	if contextCalled {
		t.Error("nav/peek/filter keys must NOT call the thread context seam")
	}
	if m.pending != nil {
		t.Error("nav/peek/filter keys must NOT open a confirm overlay")
	}
}
