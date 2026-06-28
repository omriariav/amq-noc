package console

import (
	"errors"
	"strings"
	"testing"

	"github.com/omriariav/amq-noc/internal/act"
	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/state"
)

// #24 regression set: the directive flow is the human's core write action as
// the orchestrator's client. Channel selection is deterministic (operational
// lead -> pane send; down lead -> durable AMQ inbox message), the busy-guard
// is surfaced and never forced, and a directive never clears gates.

func directiveTestSession(leadLiveness state.Liveness) state.Session {
	sess := orchestratedTestSession()
	sess.Root = "/tmp/agent-mail/pm-copilot"
	sess.Agents[0].Liveness = leadLiveness
	return sess
}

func TestDirectiveScopeGuards(t *testing.T) {
	m := &NOCModel{}
	flat := state.Session{Name: "flat", Agents: []state.Agent{{Handle: "solo"}}}
	m.beginDirectiveForSession(noc.ProjectSnapshot{Dir: "/tmp/p"}, flat)
	if m.input != nil {
		t.Fatal("flat session must not open the directive compose")
	}
	if !strings.Contains(m.actNote, "no lead handle") {
		t.Fatalf("note = %q", m.actNote)
	}
}

func TestDirectivePaneChannelWhenLeadOperational(t *testing.T) {
	m := &NOCModel{}
	sess := directiveTestSession(state.LivenessAlive)
	m.beginDirectiveForSession(noc.ProjectSnapshot{Dir: "/tmp/p"}, sess)
	if m.input == nil || m.input.kind != ctlDirective {
		t.Fatalf("expected directive compose, note=%q", m.actNote)
	}
	if !strings.Contains(m.input.hint, "pane") {
		t.Fatalf("hint = %q, want pane channel", m.input.hint)
	}
	pa := m.input.build("", "Ship the next slice.\nReport when done.", "")
	if pa.sendPrompt == nil {
		t.Fatalf("expected pane sendPrompt op, got %+v", pa)
	}
	if pa.sendPrompt.Role != "copilot" {
		t.Fatalf("send role = %q, want copilot", pa.sendPrompt.Role)
	}
	if !strings.Contains(pa.preview, "amq-squad send") || !strings.Contains(pa.preview, "--role copilot") {
		t.Fatalf("preview = %q", pa.preview)
	}
	if strings.Contains(pa.preview, "--force") {
		t.Fatal("the directive flow must never force past the busy-guard")
	}
	if len(pa.affected) != 1 || pa.affected[0] != "copilot" {
		t.Fatalf("affected = %v", pa.affected)
	}
}

func TestDirectivePaneChannelKeepsNamedProfile(t *testing.T) {
	m := &NOCModel{}
	sess := directiveTestSession(state.LivenessAlive)
	sess.TeamProfile = "review"
	sess.Root = "/tmp/p/.agent-mail/review/pm-copilot"
	m.beginDirectiveForSession(noc.ProjectSnapshot{Dir: "/tmp/p"}, sess)
	if m.input == nil {
		t.Fatalf("expected directive compose, note=%q", m.actNote)
	}
	pa := m.input.build("", "Ship the next slice.", "")
	if pa.sendPrompt == nil {
		t.Fatalf("expected pane sendPrompt op, got %+v", pa)
	}
	if pa.sendPrompt.Profile != "review" {
		t.Fatalf("send profile = %q, want review", pa.sendPrompt.Profile)
	}
	if !strings.Contains(pa.preview, "--profile review") || !strings.Contains(pa.preview, "--session pm-copilot") {
		t.Fatalf("named-profile directive preview lost namespace: %q", pa.preview)
	}
}

func TestDirectiveAMQFallbackWhenLeadDown(t *testing.T) {
	m := &NOCModel{}
	sess := directiveTestSession(state.LivenessStale)
	m.beginDirectiveForSession(noc.ProjectSnapshot{Dir: "/tmp/p"}, sess)
	if m.input == nil {
		t.Fatalf("expected directive compose, note=%q", m.actNote)
	}
	if !strings.Contains(m.input.hint, "inbox") {
		t.Fatalf("hint = %q, want AMQ inbox channel", m.input.hint)
	}
	pa := m.input.build("", "Resume the workstream per the brief.", "")
	if pa.sendPrompt != nil {
		t.Fatal("down lead must not get a pane delivery")
	}
	op := pa.op
	if op.To != "copilot" || op.Kind != "todo" {
		t.Fatalf("op = %+v, want to=copilot kind=todo", op)
	}
	if op.Thread != "p2p/copilot__user" {
		t.Fatalf("thread = %q, want sorted p2p convention", op.Thread)
	}
	if !strings.HasPrefix(op.Subject, "DIRECTIVE: Resume the workstream") {
		t.Fatalf("subject = %q", op.Subject)
	}
	if op.Root != sess.Root {
		t.Fatalf("root = %q, want pinned session root", op.Root)
	}
	if pa.preview == "" || !strings.Contains(pa.preview, "amq") {
		t.Fatalf("preview = %q", pa.preview)
	}
}

func TestDirectiveThreadSorted(t *testing.T) {
	if got := directiveThread("copilot", "user"); got != "p2p/copilot__user" {
		t.Fatalf("thread = %q", got)
	}
	if got := directiveThread("zeta", "user"); got != "p2p/user__zeta" {
		t.Fatalf("thread = %q, want handles sorted", got)
	}
}

func TestDirectiveSubjectFirstLineCapped(t *testing.T) {
	if got := directiveSubject("short ask\nrest of body"); got != "DIRECTIVE: short ask" {
		t.Fatalf("subject = %q", got)
	}
	long := strings.Repeat("x", 200)
	got := directiveSubject(long)
	if len(got) > len("DIRECTIVE: ")+72 {
		t.Fatalf("subject not capped: %d chars", len(got))
	}
}

func TestDirectivePaneConfirmBusyRefusalSurfaced(t *testing.T) {
	m := newControlModel(t)
	m.sendPrompt = func(sendPromptOp) error {
		return errors.New("amq-squad send: pane is busy (mid-turn); pass --force to interrupt")
	}
	sess := directiveTestSession(state.LivenessAlive)
	m.beginDirectiveForSession(noc.ProjectSnapshot{Dir: "/tmp/p"}, sess)
	m.pending = ptrPending(m.input.build("", "go", ""))
	m.input = nil
	m, _ = nocPress(m, "y")
	if !strings.Contains(m.actNote, "direct lead") || !strings.Contains(m.actNote, "busy") {
		t.Fatalf("busy note = %q, want directive-labeled busy refusal", m.actNote)
	}
}

func TestDirectiveFallbackConfirmSendsViaAMQSeam(t *testing.T) {
	m := newControlModel(t)
	var got act.OpMessage
	called := false
	m.sendOp = func(op act.OpMessage) error { called = true; got = op; return nil }
	sess := directiveTestSession(state.LivenessDead)
	m.beginDirectiveForSession(noc.ProjectSnapshot{Dir: "/tmp/p"}, sess)
	m.pending = ptrPending(m.input.build("", "Pick up issue 30.", ""))
	m.input = nil
	m, _ = nocPress(m, "y")
	if !called {
		t.Fatal("confirming the AMQ fallback must call the send seam")
	}
	if got.To != "copilot" || got.Kind != "todo" {
		t.Fatalf("sent op = %+v", got)
	}
	if !strings.Contains(m.actNote, "does not clear gates") {
		t.Fatalf("result note = %q, want non-clearing label", m.actNote)
	}
}
