package console

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-noc/internal/noc"
)

// #19 regression set: `up` is executable from the NOC through the same
// preview + confirm + exec seam as stop/resume, pins no --session (amq-squad
// derives the workstream from the team config, the #22 lesson), and uses the
// detached new-session target so the NOC pane is never hijacked.

func TestUpCommandShape(t *testing.T) {
	op := lifecycleOp{Verb: lifecycleUp, ProjectDir: "/tmp/team home"}
	want := "amq-squad up --project '/tmp/team home' --target new-session --terminal-session amq-squad-team-home"
	if got := op.command(); got != want {
		t.Fatalf("up command = %q, want %q", got, want)
	}
	op.Profile = "review"
	if got := op.command(); !strings.Contains(got, "--profile review") {
		t.Fatalf("up command with profile = %q, want --profile review", got)
	}
	if strings.Contains(op.command(), "--session") {
		t.Fatal("up must not pin --session; amq-squad derives the workstream")
	}
}

func TestBeginUpForProjectPreview(t *testing.T) {
	m := &NOCModel{}
	ps := workstreamTestProject(map[string][]string{"default": {"pm-copilot"}}, nil)
	m.beginUpForProject(ps)
	if m.pending == nil {
		t.Fatalf("expected confirm overlay, note=%q", m.actNote)
	}
	if m.pending.life == nil || m.pending.life.Verb != lifecycleUp {
		t.Fatalf("pending op = %+v, want lifecycle up", m.pending)
	}
	if m.pending.life.Session != "" {
		t.Fatalf("up op pinned session %q; must stay empty", m.pending.life.Session)
	}
	if !strings.Contains(m.pending.preview, "amq-squad up --project") ||
		!strings.Contains(m.pending.preview, "--target new-session") {
		t.Fatalf("preview = %q", m.pending.preview)
	}
	if m.pending.warning != "" {
		t.Fatalf("unique configured workstream must not warn, got %q", m.pending.warning)
	}
}

func TestBeginUpWorkstreamConflictWarning(t *testing.T) {
	m := &NOCModel{}
	ps := workstreamTestProject(map[string][]string{"default": {"ws-a", "ws-b"}}, nil)
	m.beginUpForProject(ps)
	if m.pending == nil {
		t.Fatal("expected confirm overlay")
	}
	if !strings.Contains(m.pending.warning, "ws-a") || !strings.Contains(m.pending.warning, "ws-b") {
		t.Fatalf("conflict warning = %q, want both workstreams named", m.pending.warning)
	}
}

func TestBeginUpMultiProfileStageZero(t *testing.T) {
	m := &NOCModel{}
	ps := workstreamTestProject(nil, nil, "default", "beta")
	m.beginUpForProject(ps)
	if m.input == nil || m.input.stage != 0 {
		t.Fatalf("expected profile-choice stage, input=%+v", m.input)
	}
	pa := m.input.build("beta", "", "")
	if pa.life == nil || pa.life.Verb != lifecycleUp {
		t.Fatalf("built op = %+v, want lifecycle up", pa)
	}
	if !strings.Contains(pa.preview, "--profile beta") {
		t.Fatalf("preview = %q, want --profile beta", pa.preview)
	}
}

func TestBeginUpUnconfiguredProjectNote(t *testing.T) {
	m := &NOCModel{}
	m.beginUpForProject(noc.ProjectSnapshot{Dir: "/tmp/x"})
	if m.pending != nil {
		t.Fatal("unconfigured project must not open a confirm overlay")
	}
	if !strings.Contains(m.actNote, "team profile") {
		t.Fatalf("note = %q, want team-profile guidance", m.actNote)
	}
}

func TestBeginUpFromSessionAndAgentRows(t *testing.T) {
	// Operator feedback: "I want to bring up the team" is pressed where the
	// team is - the session row - not its project parent. U resolves the
	// project from any row, like N and T.
	for _, kind := range []nocNodeKind{nodeProject, nodeSession, nodeAgent} {
		m := newControlModel(t)
		selectKind(t, m, kind, map[nocNodeKind]string{nodeAgent: "qa"}[kind])
		m, _ = nocPress(m, "U")
		if m.pending == nil {
			t.Fatalf("U from row kind %d should open the up confirm, note=%q", kind, m.actNote)
		}
		if m.pending.life == nil || m.pending.life.Verb != lifecycleUp {
			t.Fatalf("U from row kind %d built %+v, want lifecycle up", kind, m.pending)
		}
	}
}

func TestBeginUpExistingWorkstreamWarnsResumeAlternative(t *testing.T) {
	m := &NOCModel{}
	ps := workstreamTestProject(map[string][]string{"default": {"pm-copilot"}}, []string{"pm-copilot"})
	m.beginUpForProject(ps)
	if m.pending == nil {
		t.Fatal("expected confirm overlay")
	}
	if !strings.Contains(m.pending.warning, "already exists") ||
		!strings.Contains(m.pending.warning, "R resume") {
		t.Fatalf("warning = %q, want the up-vs-resume distinction surfaced", m.pending.warning)
	}
}

func TestUpConfirmExecutesLifecycleSeam(t *testing.T) {
	m := newControlModel(t)
	var got lifecycleOp
	called := false
	m.lifecycle = func(op lifecycleOp) error { called = true; got = op; return nil }
	ps := workstreamTestProject(map[string][]string{"default": {"pm-copilot"}}, nil)
	m.beginUpForProject(ps)
	if m.pending == nil {
		t.Fatal("expected confirm overlay")
	}
	m, _ = nocPress(m, "y")
	if !called {
		t.Fatal("confirming up must call the lifecycle seam")
	}
	if got.Verb != lifecycleUp || got.ProjectDir != "/tmp/ws-project" {
		t.Fatalf("lifecycle op = %+v", got)
	}
	if m.pending != nil {
		t.Fatal("confirm must clear the pending overlay")
	}
}

// A confirmed resume/restart/up (re)creates a detached tmux session; the NOC
// cannot attach it safely itself, so the result note carries the exact attach
// command and copies it to the clipboard.
func TestLifecycleSuccessOffersAttachCommand(t *testing.T) {
	cases := []struct {
		verb       lifecycleVerb
		session    string
		wantTarget string
	}{
		{lifecycleResume, "pm-copilot", "amq-squad-ws-project-pm-copilot"},
		{lifecycleRestart, "pm-copilot", "amq-squad-ws-project-pm-copilot"},
		{lifecycleUp, "", "amq-squad-ws-project"},
	}
	for _, tc := range cases {
		m := newControlModel(t)
		m.lifecycle = func(lifecycleOp) error { return nil }
		var copied []string
		m.copyText = func(s string) error { copied = append(copied, s); return nil }
		op := lifecycleOp{Verb: tc.verb, ProjectDir: "/tmp/ws-project", Session: tc.session}
		m.pending = &pendingAction{kind: ctlResume, preview: op.command(), life: &op}
		m, _ = nocPress(m, "y")
		want := "tmux -CC attach -t " + tc.wantTarget
		if !strings.Contains(m.actNote, want) || !strings.Contains(m.actNote, "copied to clipboard") {
			t.Fatalf("verb %s note = %q, want attach command %q copied", tc.verb, m.actNote, want)
		}
		if len(copied) != 1 || copied[0] != want {
			t.Fatalf("verb %s copied = %v, want %q", tc.verb, copied, want)
		}
	}
}

func TestStopSuccessDoesNotOfferAttach(t *testing.T) {
	m := newControlModel(t)
	m.lifecycle = func(lifecycleOp) error { return nil }
	copiedCount := 0
	m.copyText = func(string) error { copiedCount++; return nil }
	op := lifecycleOp{Verb: lifecycleStop, ProjectDir: "/tmp/ws-project", Session: "pm-copilot"}
	m.pending = &pendingAction{kind: ctlStop, preview: op.command(), life: &op}
	m, _ = nocPress(m, "y")
	if strings.Contains(m.actNote, "attach") || copiedCount != 0 {
		t.Fatalf("stop must not offer attach, note=%q copies=%d", m.actNote, copiedCount)
	}
}

func TestUpEscCancelsWithoutExec(t *testing.T) {
	m := newControlModel(t)
	called := false
	m.lifecycle = func(lifecycleOp) error { called = true; return nil }
	m.beginUpForProject(workstreamTestProject(map[string][]string{"default": {"pm-copilot"}}, nil))
	m, _ = nocPress(m, "esc")
	if called {
		t.Fatal("esc must not execute")
	}
	if m.pending != nil {
		t.Fatal("esc must clear the pending overlay")
	}
}
