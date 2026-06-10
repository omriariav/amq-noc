package console

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-noc/internal/noc"
)

// #22 regression set: the new-session flow must lead with the team's
// configured workstream instead of free-asking for a name, and must make a
// divergent name's consequence explicit before anything is executed.

func workstreamTestProject(configured map[string][]string, sessionNames []string, profiles ...string) noc.ProjectSnapshot {
	if len(profiles) == 0 {
		profiles = []string{"default"}
	}
	return noc.ProjectSnapshot{
		Project:               "p",
		Dir:                   "/tmp/ws-project",
		TeamConfigured:        true,
		DefaultTeam:           true,
		Profiles:              profiles,
		SessionNames:          sessionNames,
		ConfiguredWorkstreams: configured,
	}
}

func TestBeginNewSessionPrefillsConfiguredWorkstream(t *testing.T) {
	m := &NOCModel{}
	ps := workstreamTestProject(map[string][]string{"default": {"pm-copilot"}}, nil)
	m.beginNewSessionForProject(ps)
	if m.input == nil {
		t.Fatal("expected new-session editor to open")
	}
	if m.input.stage != 1 {
		t.Fatalf("stage = %d, want 1 (single profile)", m.input.stage)
	}
	if m.input.body != "pm-copilot" {
		t.Fatalf("body = %q, want prefilled configured workstream pm-copilot", m.input.body)
	}
	if !strings.Contains(m.input.hint, "pm-copilot") {
		t.Fatalf("hint %q should name the configured workstream", m.input.hint)
	}
	if !strings.Contains(m.input.hint, "workstream") {
		t.Fatalf("hint %q should explain the name is the AMQ workstream", m.input.hint)
	}
}

func TestBeginNewSessionExistingConfiguredWorkstreamNotPrefilled(t *testing.T) {
	m := &NOCModel{}
	ps := workstreamTestProject(map[string][]string{"default": {"pm-copilot"}}, []string{"pm-copilot"})
	m.beginNewSessionForProject(ps)
	if m.input == nil {
		t.Fatal("expected new-session editor to open")
	}
	// The validator rejects existing names, so prefilling one would steer the
	// operator straight into an error; the hint points at resume instead.
	if m.input.body != "" {
		t.Fatalf("body = %q, want empty (configured workstream already exists)", m.input.body)
	}
	if !strings.Contains(m.input.hint, "already exists") {
		t.Fatalf("hint %q should say the configured workstream already exists", m.input.hint)
	}
	if !strings.Contains(m.input.hint, "pm-copilot") {
		t.Fatalf("hint %q should name the configured workstream", m.input.hint)
	}
}

func TestBeginNewSessionWorkstreamConflictHint(t *testing.T) {
	m := &NOCModel{}
	ps := workstreamTestProject(map[string][]string{"default": {"ws-a", "ws-b"}}, nil)
	m.beginNewSessionForProject(ps)
	if m.input == nil {
		t.Fatal("expected new-session editor to open")
	}
	if m.input.body != "" {
		t.Fatalf("body = %q, want empty when members disagree", m.input.body)
	}
	if !strings.Contains(m.input.hint, "ws-a") || !strings.Contains(m.input.hint, "ws-b") {
		t.Fatalf("hint %q should list the disagreeing workstreams", m.input.hint)
	}
}

func TestNewSessionDivergenceWarningInConfirm(t *testing.T) {
	m := &NOCModel{}
	ps := workstreamTestProject(map[string][]string{"default": {"pm-copilot"}}, nil)
	m.beginNewSessionForProject(ps)
	if m.input == nil {
		t.Fatal("expected new-session editor to open")
	}

	diverged := m.input.build("", "first-run", "")
	if !strings.Contains(diverged.warning, "pm-copilot") || !strings.Contains(diverged.warning, "first-run") {
		t.Fatalf("warning %q should name both the configured and the typed workstream", diverged.warning)
	}
	if !strings.Contains(diverged.warning, "stub brief") {
		t.Fatalf("warning %q should state the stub-brief consequence", diverged.warning)
	}

	matching := m.input.build("", "pm-copilot", "")
	if matching.warning != "" {
		t.Fatalf("warning = %q, want none when the typed name matches the configured workstream", matching.warning)
	}
}

func TestNewSessionStagePrefillOnProfileChoice(t *testing.T) {
	m := &NOCModel{}
	ps := workstreamTestProject(
		map[string][]string{"beta": {"beta-ws"}},
		nil,
		"default", "beta",
	)
	m.beginNewSessionForProject(ps)
	in := m.input
	if in == nil {
		t.Fatal("expected new-session editor to open")
	}
	if in.stage != 0 {
		t.Fatalf("stage = %d, want 0 (profile choice first)", in.stage)
	}
	in.subject = "beta"
	m.handleInputKey(tea.KeyMsg{Type: tea.KeyEnter})
	if in.stage != 1 {
		t.Fatalf("stage = %d, want 1 after profile choice", in.stage)
	}
	if in.body != "beta-ws" {
		t.Fatalf("body = %q, want beta profile's configured workstream beta-ws", in.body)
	}
	if !strings.Contains(in.hint, "beta-ws") {
		t.Fatalf("hint %q should name the configured workstream", in.hint)
	}
}

func TestNewSessionConfirmOverlayRendersWarning(t *testing.T) {
	m := newNOCModel(NOCRebuildConfig{})
	m.pending = &pendingAction{
		kind:    ctlNewSession,
		preview: "amq-squad resume --project /tmp/ws-project --exec --target new-session --terminal-session t first-run",
		warning: "team is configured for workstream pm-copilot; launching under first-run creates a NEW workstream with a stub brief",
	}
	view := m.confirmOverlayView()
	if !strings.Contains(view, "warning:") {
		t.Fatalf("confirm overlay should render the warning line, got:\n%s", view)
	}
	if !strings.Contains(view, "pm-copilot") {
		t.Fatalf("confirm overlay warning should name the configured workstream, got:\n%s", view)
	}
}

func TestKickRecoverActionsShowResolvedWorkstream(t *testing.T) {
	ps := workstreamTestProject(map[string][]string{"default": {"pm-copilot"}}, nil)
	actions := kickRecoverActions(ps, "", "")
	var up, resume *nocCommandAction
	for i := range actions {
		switch actions[i].Label {
		case "up":
			up = &actions[i]
		case "resume preview":
			resume = &actions[i]
		}
	}
	if up == nil || resume == nil {
		t.Fatalf("expected up + resume preview actions, got %+v", actions)
	}
	for _, action := range []*nocCommandAction{up, resume} {
		if !strings.Contains(action.Description, "workstream: pm-copilot") {
			t.Fatalf("%s description %q should show the resolved workstream", action.Label, action.Description)
		}
		if strings.Contains(action.Command, "pm-copilot") {
			t.Fatalf("%s command %q must stay session-free (resolution is delegated to amq-squad)", action.Label, action.Command)
		}
	}
}

func TestKickRecoverActionsShowWorkstreamDisagreement(t *testing.T) {
	ps := workstreamTestProject(map[string][]string{"default": {"ws-a", "ws-b"}}, nil)
	actions := kickRecoverActions(ps, "", "")
	found := false
	for _, action := range actions {
		if action.Label != "up" {
			continue
		}
		found = true
		if !strings.Contains(action.Description, "ws-a") || !strings.Contains(action.Description, "ws-b") {
			t.Fatalf("up description %q should list the disagreeing workstreams", action.Description)
		}
	}
	if !found {
		t.Fatal("expected an up action")
	}
}
