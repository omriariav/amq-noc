package console

import (
	"strings"
	"testing"
)

// These tests cover the v0.5.0 SEND PROMPT action (the 'p' key): delivering a
// typed prompt to one agent's tmux pane via `amq-squad send`, piping the body to
// the child's STDIN. They mirror the existing message/lifecycle control tests:
// build a model over the hand-crafted snapshot, drive the PUBLIC Update with real
// keys, and assert on the injected sendPrompt SEAM — never a real amq-squad. The
// confirm contract holds here too: opening/declining the overlay never calls the
// seam; a confirm calls it exactly once with the exact op (body included).

// TestSendPromptCommandPreview pins the EXACT argv the confirm overlay shows. The
// body is piped via stdin (--body-file -), so it must NEVER appear in the
// command; --profile is present only for a non-default/non-empty profile.
func TestSendPromptCommandPreview(t *testing.T) {
	t.Run("default profile is omitted and body is not in the command", func(t *testing.T) {
		op := sendPromptOp{
			ProjectDir: "/repo/app",
			Profile:    "default",
			Session:    "issue-7",
			Role:       "qa",
			Body:       "please rerun the smoke suite",
		}
		want := "amq-squad send --project /repo/app --session issue-7 --role qa --body-file -"
		if got := op.command(); got != want {
			t.Fatalf("send-prompt preview mismatch:\n got %q\nwant %q", got, want)
		}
		if strings.Contains(op.command(), "please rerun") {
			t.Fatalf("body must be piped via stdin, never shown in the command: %q", op.command())
		}
	})

	t.Run("empty profile is omitted", func(t *testing.T) {
		op := sendPromptOp{ProjectDir: "/repo/app", Session: "issue-7", Role: "qa", Body: "hi"}
		if strings.Contains(op.command(), "--profile") {
			t.Fatalf("empty profile should omit --profile: %q", op.command())
		}
	})

	t.Run("non-default profile is included", func(t *testing.T) {
		op := sendPromptOp{ProjectDir: "/repo/app", Profile: "review", Session: "issue-7", Role: "qa", Body: "hi"}
		want := "amq-squad send --project /repo/app --profile review --session issue-7 --role qa --body-file -"
		if got := op.command(); got != want {
			t.Fatalf("non-default profile preview mismatch:\n got %q\nwant %q", got, want)
		}
	})

	t.Run("override redirects the binary token", func(t *testing.T) {
		old := generatedSquadCommandOverride
		generatedSquadCommandOverride = "/tmp/amq2"
		t.Cleanup(func() { generatedSquadCommandOverride = old })
		op := sendPromptOp{ProjectDir: "/repo/app", Role: "qa", Body: "hi"}
		if !strings.HasPrefix(op.command(), "/tmp/amq2 send ") {
			t.Fatalf("override should redirect send-prompt preview, got %q", op.command())
		}
	})
}

// TestSendPrompt_NonAgentRowIsGuarded mirrors beginMessage's node-kind guard: on
// any non-agent row, 'p' sets the "select an agent first" note and opens no
// editor.
func TestSendPrompt_NonAgentRowIsGuarded(t *testing.T) {
	m := newControlModel(t)
	selectKind(t, m, nodeSession, "")
	called := false
	m.sendPrompt = func(sendPromptOp) error { called = true; return nil }

	m, _ = nocPress(m, "p")
	if m.input != nil {
		t.Fatalf("p on a session row should NOT open the body editor")
	}
	if !strings.Contains(m.actNote, "select an agent first") {
		t.Fatalf("p on a session row should explain itself, got note %q", m.actNote)
	}
	if called {
		t.Fatal("guarded send-prompt must NOT call the seam")
	}
}

// TestSendPrompt_ConfirmGate walks the full editor -> confirm -> run flow on an
// agent row, asserting the seam is reached exactly once with the typed body and
// never on open/decline.
func TestSendPrompt_ConfirmGate(t *testing.T) {
	t.Run("p opens the body editor; opening calls no seam", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeAgent, "qa")
		called := false
		m.sendPrompt = func(sendPromptOp) error { called = true; return nil }

		m, _ = nocPress(m, "p")
		if m.input == nil || m.input.kind != ctlSendPrompt {
			t.Fatalf("p on an agent row should open the send-prompt body editor, got %+v", m.input)
		}
		if called {
			t.Fatal("merely opening the editor must NOT call the send-prompt seam")
		}
	})

	t.Run("typed body builds a preview overlay; building calls no seam", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeAgent, "qa")
		called := false
		m.sendPrompt = func(sendPromptOp) error { called = true; return nil }

		m, _ = nocPress(m, "p")
		m = typeControlText(t, m, "rerun smoke")
		m, _ = nocPress(m, "enter")
		if m.pending == nil || m.pending.kind != ctlSendPrompt || m.pending.sendPrompt == nil {
			t.Fatalf("entering a body should open the send-prompt confirm overlay, got %+v", m.pending)
		}
		want := "amq-squad send --project /fake/proj/beta --session beta --role qa --body-file -"
		if m.pending.preview != want {
			t.Fatalf("send-prompt preview mismatch:\n got %q\nwant %q", m.pending.preview, want)
		}
		if m.pending.sendPrompt.Body != "rerun smoke" {
			t.Fatalf("pending op should carry the typed body, got %q", m.pending.sendPrompt.Body)
		}
		// The recipient handle is shown as the affected scope, not the body.
		if len(m.pending.affected) != 1 || m.pending.affected[0] != "qa" {
			t.Fatalf("send-prompt should affect exactly the selected agent, got %v", m.pending.affected)
		}
		if !strings.Contains(m.View(), want) {
			t.Fatalf("confirm overlay should render the exact send-prompt command:\n%s", m.View())
		}
		if called {
			t.Fatal("building the overlay must NOT call the send-prompt seam")
		}
	})

	t.Run("esc declines: seam NEVER called", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeAgent, "qa")
		called := false
		m.sendPrompt = func(sendPromptOp) error { called = true; return nil }

		m, _ = nocPress(m, "p")
		m = typeControlText(t, m, "rerun smoke")
		m, _ = nocPress(m, "enter")
		m, _ = nocPress(m, "esc")
		if called {
			t.Error("declining (esc) must NOT call the send-prompt seam")
		}
		if m.pending != nil {
			t.Error("esc should close the confirm overlay")
		}
	})

	t.Run("y confirms: seam called once with the body piped via the op", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeAgent, "qa")
		var got []sendPromptOp
		m.sendPrompt = func(op sendPromptOp) error { got = append(got, op); return nil }

		m, _ = nocPress(m, "p")
		m = typeControlText(t, m, "rerun smoke")
		m, _ = nocPress(m, "enter")
		m, cmd := nocPress(m, "y")
		if len(got) != 1 {
			t.Fatalf("confirm should call the send-prompt seam exactly once, got %d", len(got))
		}
		want := sendPromptOp{
			ProjectDir: "/fake/proj/beta",
			Profile:    "default",
			Session:    "beta",
			Role:       "qa",
			Body:       "rerun smoke",
		}
		if got[0] != want {
			t.Fatalf("confirmed send-prompt op mismatch:\n got %+v\nwant %+v", got[0], want)
		}
		if m.pending != nil {
			t.Error("confirm should close the overlay")
		}
		if !strings.Contains(m.actNote, "SEND PROMPT sent") {
			t.Fatalf("successful send-prompt should note success, got %q", m.actNote)
		}
		if !strings.Contains(m.actNote, "pane-only") || !strings.Contains(m.actNote, "does not clear needs-you") {
			t.Fatalf("successful send-prompt should not look like a gate-clearing AMQ reply, got %q", m.actNote)
		}
		if cmd == nil {
			t.Fatal("a successful confirmed send-prompt should request an immediate refresh")
		}
	})
}

// TestSendPrompt_EmptyBodyRejected ensures the validateBody guard refuses an
// empty/whitespace body: no overlay, the editor stays open, and the seam is
// never reached.
func TestSendPrompt_EmptyBodyRejected(t *testing.T) {
	m := newControlModel(t)
	selectKind(t, m, nodeAgent, "qa")
	called := false
	m.sendPrompt = func(sendPromptOp) error { called = true; return nil }

	m, _ = nocPress(m, "p")
	m = typeControlText(t, m, "   ")
	m, _ = nocPress(m, "enter")
	if m.pending != nil {
		t.Fatal("an empty body must NOT build a confirm overlay")
	}
	if m.input == nil {
		t.Fatal("an empty body should keep the editor open")
	}
	if !strings.Contains(m.actNote, "body cannot be empty") {
		t.Fatalf("empty body should explain itself, got note %q", m.actNote)
	}
	if called {
		t.Fatal("a rejected empty body must NOT call the seam")
	}
}

// TestSendPrompt_BusyErrorSurfacesActionableNote is the key safety behavior:
// amq-squad refuses a prompt while the agent is mid-turn (exit non-zero with a
// "busy" message). The NOC must turn that into an actionable "retry when idle"
// note rather than a raw subprocess error.
func TestSendPrompt_BusyErrorSurfacesActionableNote(t *testing.T) {
	t.Run("busy error becomes an actionable retry note", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeAgent, "qa")
		m.sendPrompt = func(sendPromptOp) error {
			return errString("exit status 1: agent is busy (mid-turn); use --force to override")
		}

		m, _ = nocPress(m, "p")
		m = typeControlText(t, m, "rerun smoke")
		m, _ = nocPress(m, "enter")
		m, _ = nocPress(m, "y")
		if !strings.Contains(m.actNote, "agent busy") || !strings.Contains(m.actNote, "retry when idle") {
			t.Fatalf("busy refusal should surface an actionable note, got %q", m.actNote)
		}
		// The raw subprocess detail must not leak into the operator note.
		if strings.Contains(m.actNote, "exit status 1") {
			t.Fatalf("busy note should be actionable, not the raw error: %q", m.actNote)
		}
	})

	t.Run("non-busy error surfaces the raw failure", func(t *testing.T) {
		m := newControlModel(t)
		selectKind(t, m, nodeAgent, "qa")
		m.sendPrompt = func(sendPromptOp) error { return errString("no such session") }

		m, _ = nocPress(m, "p")
		m = typeControlText(t, m, "rerun smoke")
		m, _ = nocPress(m, "enter")
		m, _ = nocPress(m, "y")
		if !strings.Contains(m.actNote, "send prompt failed") || !strings.Contains(m.actNote, "no such session") {
			t.Fatalf("a non-busy error should surface the failure, got %q", m.actNote)
		}
	})
}

// TestSendPrompt_MixedProfileResolvesToEmpty ensures a session whose agents run
// under DIFFERENT profiles (the "PROFILE" placeholder) maps to "" so the preview
// omits --profile and lets amq-squad resolve the agent's own profile.
func TestSendPrompt_MixedProfileResolvesToEmpty(t *testing.T) {
	m := newControlModel(t)
	// Give the two beta agents different launch profiles so sessionCommandProfile
	// returns the "PROFILE" mixed-profile placeholder.
	m.ms.Projects[0].Snap.Sessions[0].Agents[0].TeamProfile = "alpha"
	m.ms.Projects[0].Snap.Sessions[0].Agents[1].TeamProfile = "beta"
	mm, _ := m.Update(nocSnapshotMsg{ms: m.ms})
	m = mm.(*NOCModel)
	selectKind(t, m, nodeAgent, "qa")
	m.sendPrompt = func(sendPromptOp) error { return nil }

	m, _ = nocPress(m, "p")
	m = typeControlText(t, m, "rerun smoke")
	m, _ = nocPress(m, "enter")
	if m.pending == nil || m.pending.sendPrompt == nil {
		t.Fatalf("mixed-profile agent should still open a send-prompt overlay, got %+v", m.pending)
	}
	if m.pending.sendPrompt.Profile != "" {
		t.Fatalf("mixed-profile (PROFILE) should resolve to empty, got %q", m.pending.sendPrompt.Profile)
	}
	if strings.Contains(m.pending.preview, "--profile") {
		t.Fatalf("mixed-profile preview must omit --profile: %q", m.pending.preview)
	}
}
