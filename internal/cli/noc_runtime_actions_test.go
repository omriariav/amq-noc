package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/state"
)

// squadEnvFixture builds a one-session, two-agent team-configured envelope whose
// sessions/agents already carry the fallback action templates, so a fold can be
// observed replacing/extending them.
func squadEnvFixture(t *testing.T) nocSnapshotEnvelopeData {
	t.Helper()
	ps := noc.ProjectSnapshot{
		Project:        "amq-noc",
		Dir:            "/repo",
		TeamConfigured: true,
		Snap: state.Snapshot{Sessions: []state.Session{{
			Name: "s",
			Root: "/repo/.agent-mail/s",
			Agents: []state.Agent{
				{Handle: "cto", Role: "cto", Liveness: state.LivenessAlive},
				{Handle: "fullstack", Role: "fullstack", Liveness: state.LivenessAlive},
			},
		}}},
	}
	return nocSnapshotEnvelope(noc.MultiSnapshot{Projects: []noc.ProjectSnapshot{ps}}, "", false)
}

// availableRuntimeStatus mirrors the v1.5 status-detail contract with a LIVE pane:
// focus/send are available, plus session-scope resume/status.
func availableRuntimeStatus() noc.RuntimeStatus {
	member := func(role string) noc.RuntimeMember {
		return noc.RuntimeMember{Role: role, Handle: role, PaneID: "%1", PaneAlive: true, Actions: []noc.RuntimeAction{
			{Kind: "focus", Command: "amq-squad focus --project /repo --session s --role " + role, Available: true},
			{Kind: "send", Command: "amq-squad send --project /repo --session s --role " + role + " --body-file -", Available: true},
			{Kind: "resume", Command: "amq-squad resume --project /repo --session s --exec", Available: true},
			{Kind: "status", Command: "amq-squad status --project /repo --session s --json", Available: true},
		}}
	}
	return noc.RuntimeStatus{Members: []noc.RuntimeMember{member("cto"), member("fullstack")}}
}

func availableRuntimeStatusWithSessionCatalog() noc.RuntimeStatus {
	rs := availableRuntimeStatus()
	rs.SessionActions = []noc.RuntimeAction{
		{
			Kind: "status", Label: "show session status", Scope: "session",
			Command:   "amq-squad status --project /repo --session s --json",
			Available: true,
		},
		{
			Kind: "resume_preview", Label: "preview resume plan", Scope: "session",
			Command:   "amq-squad resume --project /repo --session s --json",
			Available: true,
		},
		{
			Kind: "resume_current_window", Label: "resume in current window", Scope: "session",
			Command: "amq-squad resume --project /repo --session s --exec --target current-window",
			Mutates: true, NeedsConfirmation: true, Available: true,
		},
		{
			Kind: "resume_new_session", Label: "resume in new tmux session", Scope: "session",
			Command: "amq-squad resume --project /repo --session s --exec --target new-session",
			Mutates: true, NeedsConfirmation: true, Available: true,
		},
		{
			Kind: "stop", Label: "stop the session", Scope: "session",
			Command: "amq-squad stop --project /repo --session s --all",
			Mutates: true, NeedsConfirmation: true, Available: true,
		},
	}
	return rs
}

func findNOCAction(actions []nocActionJSONData, scope, name string) (nocActionJSONData, bool) {
	for _, a := range actions {
		if a.Scope == scope && a.Name == name {
			return a, true
		}
	}
	return nocActionJSONData{}, false
}

func sessionRow(t *testing.T, env nocSnapshotEnvelopeData) nocSessionJSONData {
	t.Helper()
	if len(env.Projects) != 1 || len(env.Projects[0].Sessions) != 1 {
		t.Fatalf("fixture shape changed: %d projects", len(env.Projects))
	}
	return env.Projects[0].Sessions[0]
}

func agentRow(t *testing.T, sess nocSessionJSONData, handle string) nocAgentJSONData {
	t.Helper()
	for _, ag := range sess.Agents {
		if ag.Handle == handle {
			return ag
		}
	}
	t.Fatalf("agent %q not found", handle)
	return nocAgentJSONData{}
}

func TestApplyRuntimeActionsPrefersPublishedAndAddsFocusSend(t *testing.T) {
	env := squadEnvFixture(t)
	fallbackStatus, _ := findNOCAction(sessionRow(t, env).Actions, "session", "status")

	var calls int
	fetch := func(dir, profile, session string) noc.RuntimeStatus {
		calls++
		if dir != "/repo" || session != "s" || profile != "" {
			t.Errorf("unexpected scope: dir=%q profile=%q session=%q", dir, profile, session)
		}
		return availableRuntimeStatus()
	}
	applyRuntimeActions(&env, fetch)

	if calls != 1 {
		t.Fatalf("expected exactly one runtime fetch, got %d", calls)
	}

	sess := sessionRow(t, env)
	// Published session status/resume REPLACE the fallback commands (same id).
	gotStatus, ok := findNOCAction(sess.Actions, "session", "status")
	if !ok || gotStatus.Command != "amq-squad status --project /repo --session s --json" {
		t.Fatalf("session status not replaced by published: %+v", gotStatus)
	}
	if gotStatus.Command == fallbackStatus.Command {
		t.Fatal("session status command should differ from the fallback after the fold")
	}
	if gotStatus.ID != fallbackStatus.ID {
		t.Errorf("status id must stay stable for selectors: got %q want %q", gotStatus.ID, fallbackStatus.ID)
	}
	if gotStatus.Mutates {
		t.Error("published status must stay read-only")
	}
	gotResume, ok := findNOCAction(sess.Actions, "session", "resume")
	if !ok || gotResume.Command != "amq-squad resume --project /repo --session s --exec" || !gotResume.Mutates {
		t.Fatalf("session resume not replaced/mutating: %+v", gotResume)
	}
	// Exactly one status and one resume after replacement (no duplicate rows).
	if n := countActions(sess.Actions, "session", "status"); n != 1 {
		t.Errorf("expected 1 session status action, got %d", n)
	}
	if n := countActions(sess.Actions, "session", "resume"); n != 1 {
		t.Errorf("expected 1 session resume action, got %d", n)
	}

	// Agent focus/send are ADDED (no fallback for those), per matching role.
	cto := agentRow(t, sess, "cto")
	focus, ok := findNOCAction(cto.Actions, "agent", "focus")
	if !ok || focus.Command != "amq-squad focus --project /repo --session s --role cto" || focus.Mutates {
		t.Fatalf("cto focus wrong: %+v ok=%v", focus, ok)
	}
	send, ok := findNOCAction(cto.Actions, "agent", "send")
	if !ok || !send.Mutates || !send.RequiresConfirmation || !send.Template {
		t.Fatalf("cto send must be mutating+confirm+template: %+v ok=%v", send, ok)
	}
	if !nocActionReadsStdinBody(send.Command) {
		t.Errorf("cto send should be a stdin-body command: %q", send.Command)
	}
	fs := agentRow(t, sess, "fullstack")
	if _, ok := findNOCAction(fs.Actions, "agent", "focus"); !ok {
		t.Error("fullstack should also gain a focus action keyed to its own role")
	}

	// No raw tmux command construction: every folded command delegates to amq-squad.
	for _, a := range append(append([]nocActionJSONData{}, sess.Actions...), cto.Actions...) {
		if a.Scope == "session" && (a.Name == "status" || a.Name == "resume") ||
			a.Scope == "agent" && (a.Name == "focus" || a.Name == "send") {
			if !strings.HasPrefix(a.Command, "amq-squad ") {
				t.Errorf("folded action %q must be an amq-squad command, got %q", a.Name, a.Command)
			}
		}
	}

	// Flat action list and counts recomputed to include the folded actions.
	if _, ok := findNOCAction(env.Actions, "agent", "focus"); !ok {
		t.Error("flat env.Actions should include the folded focus action")
	}
	if env.ActionCount != len(env.Actions) {
		t.Errorf("ActionCount=%d out of sync with %d flat actions", env.ActionCount, len(env.Actions))
	}
	if env.MutatingActionCount != nocMutatingActionCount(env.Actions) {
		t.Error("MutatingActionCount not recomputed after fold")
	}
}

func TestApplyRuntimeActionsPrefersTopLevelSessionCatalog(t *testing.T) {
	env := squadEnvFixture(t)
	before := sessionRow(t, env)
	fallbackStatus, _ := findNOCAction(before.Actions, "session", "status")
	fallbackStop, _ := findNOCAction(before.Actions, "session", "stop")
	if _, ok := findNOCAction(before.Actions, "session", "resume"); !ok {
		t.Fatal("fixture should include fallback generic resume")
	}

	applyRuntimeActions(&env, func(string, string, string) noc.RuntimeStatus {
		return availableRuntimeStatusWithSessionCatalog()
	})

	sess := sessionRow(t, env)
	if _, ok := findNOCAction(sess.Actions, "session", "resume"); ok {
		t.Fatal("top-level resume variants should supersede the generic fallback resume")
	}
	status, ok := findNOCAction(sess.Actions, "session", "status")
	if !ok || status.Command != "amq-squad status --project /repo --session s --json" {
		t.Fatalf("published status missing: %+v ok=%v", status, ok)
	}
	if status.ID != fallbackStatus.ID {
		t.Errorf("status selector id should stay stable: got %q want %q", status.ID, fallbackStatus.ID)
	}
	if status.Command == fallbackStatus.Command {
		t.Fatal("status command should be replaced by the published catalog command")
	}
	stop, ok := findNOCAction(sess.Actions, "session", "stop")
	if !ok || stop.Command != "amq-squad stop --project /repo --session s --all" || !stop.Mutates || !stop.RequiresConfirmation {
		t.Fatalf("published stop missing/wrong: %+v ok=%v", stop, ok)
	}
	if stop.ID != fallbackStop.ID {
		t.Errorf("stop selector id should stay stable: got %q want %q", stop.ID, fallbackStop.ID)
	}

	for _, name := range []string{"resume_preview", "resume_current_window", "resume_new_session"} {
		action, ok := findNOCAction(sess.Actions, "session", name)
		if !ok {
			t.Fatalf("missing published session action %q", name)
		}
		if !strings.HasPrefix(action.Command, "amq-squad ") {
			t.Fatalf("published action %q must delegate to amq-squad, got %q", name, action.Command)
		}
		if name == "resume_preview" && action.Mutates {
			t.Fatal("resume_preview must remain read-only")
		}
		if name != "resume_preview" && (!action.Mutates || !action.RequiresConfirmation) {
			t.Fatalf("%s must be mutating+confirm-gated: %+v", name, action)
		}
	}

	// Agent focus/send still come from member actions even when session catalog is
	// present.
	if _, ok := findNOCAction(agentRow(t, sess, "cto").Actions, "agent", "focus"); !ok {
		t.Fatal("top-level session catalog must not suppress agent focus/send")
	}
}

func countActions(actions []nocActionJSONData, scope, name string) int {
	n := 0
	for _, a := range actions {
		if a.Scope == scope && a.Name == name {
			n++
		}
	}
	return n
}

func TestApplyRuntimeActionsOnlyAvailable(t *testing.T) {
	env := squadEnvFixture(t)
	fallbackStatus, _ := findNOCAction(sessionRow(t, env).Actions, "session", "status")

	// Dead pane: focus/send unavailable, resume available, status unavailable.
	fetch := func(string, string, string) noc.RuntimeStatus {
		return noc.RuntimeStatus{Members: []noc.RuntimeMember{{Role: "cto", Handle: "cto", Actions: []noc.RuntimeAction{
			{Kind: "focus", Command: "amq-squad focus --project /repo --session s --role cto", Available: false},
			{Kind: "send", Command: "amq-squad send --project /repo --session s --role cto --body-file -", Available: false},
			{Kind: "resume", Command: "amq-squad resume --project /repo --session s --exec", Available: true},
			{Kind: "status", Command: "amq-squad status --project /repo --session s --json", Available: false},
		}}}}
	}
	applyRuntimeActions(&env, fetch)

	sess := sessionRow(t, env)
	// resume is available -> replaced; status is unavailable -> fallback retained.
	if got, _ := findNOCAction(sess.Actions, "session", "resume"); got.Command != "amq-squad resume --project /repo --session s --exec" {
		t.Errorf("available resume should be folded, got %q", got.Command)
	}
	if got, _ := findNOCAction(sess.Actions, "session", "status"); got.Command != fallbackStatus.Command {
		t.Errorf("unavailable status must keep the fallback command, got %q", got.Command)
	}
	// Unavailable focus/send never surface.
	if _, ok := findNOCAction(agentRow(t, sess, "cto").Actions, "agent", "focus"); ok {
		t.Error("unavailable focus must not be folded in")
	}
	if _, ok := findNOCAction(agentRow(t, sess, "cto").Actions, "agent", "send"); ok {
		t.Error("unavailable send must not be folded in")
	}
}

func TestApplyRuntimeActionsUnavailableCatalogSuppressesFallback(t *testing.T) {
	env := squadEnvFixture(t)
	applyRuntimeActions(&env, func(string, string, string) noc.RuntimeStatus {
		return noc.RuntimeStatus{SessionActions: []noc.RuntimeAction{
			{
				Kind: "stop", Label: "stop the session", Scope: "session",
				Command: "amq-squad stop --project /repo --session s --all",
				Mutates: true, NeedsConfirmation: true, Available: false, Reason: "not running",
			},
		}}
	})

	if _, ok := findNOCAction(sessionRow(t, env).Actions, "session", "stop"); ok {
		t.Fatal("published-but-unavailable stop should suppress the generated fallback stop")
	}
	if _, ok := findNOCAction(sessionRow(t, env).Actions, "session", "restart"); ok {
		t.Fatal("published stop should suppress generated restart so it cannot bypass availability")
	}
}

func TestApplyRuntimeActionsDegradesGracefully(t *testing.T) {
	// nil fetch (default for unit-constructed nocExecution) leaves the model pure.
	env := squadEnvFixture(t)
	before, _ := findNOCAction(sessionRow(t, env).Actions, "session", "status")
	applyRuntimeActions(&env, nil)
	if after, _ := findNOCAction(sessionRow(t, env).Actions, "session", "status"); after.Command != before.Command {
		t.Error("nil fetch must not alter actions")
	}

	// Older/unsupported amq-squad: zero RuntimeStatus -> fallback untouched.
	env2 := squadEnvFixture(t)
	applyRuntimeActions(&env2, func(string, string, string) noc.RuntimeStatus { return noc.RuntimeStatus{} })
	if after, _ := findNOCAction(sessionRow(t, env2).Actions, "session", "status"); after.Command != before.Command {
		t.Error("empty runtime status must leave the fallback status command unchanged")
	}
	if _, ok := findNOCAction(agentRow(t, sessionRow(t, env2), "cto").Actions, "agent", "focus"); ok {
		t.Error("no runtime contract must not add focus/send")
	}
}

func TestApplyRuntimeActionsSkipsNonSquadProjects(t *testing.T) {
	// A plain AMQ root (not team-configured) must never shell amq-squad.
	ps := noc.ProjectSnapshot{
		Project: "mailbox",
		Dir:     "/repo",
		Snap: state.Snapshot{Sessions: []state.Session{{
			Name:   "s",
			Agents: []state.Agent{{Handle: "cto", Role: "cto"}},
		}}},
	}
	env := nocSnapshotEnvelope(noc.MultiSnapshot{Projects: []noc.ProjectSnapshot{ps}}, "", false)
	called := false
	applyRuntimeActions(&env, func(string, string, string) noc.RuntimeStatus {
		called = true
		return availableRuntimeStatus()
	})
	if called {
		t.Error("non-team-configured project must not trigger a runtime fetch")
	}
}

func TestRunNOCActionRefusesStdinBodySend(t *testing.T) {
	send := nocAction("agent", "a|action|send", "send",
		"amq-squad send --project /repo --session s --role cto --body-file -",
		"deliver a prompt", true, true, true)

	// Real run: the runner is NEVER called and a usage error is returned, so a
	// script cannot read exit 0 as a successful prompt delivery.
	ran := false
	out := &strings.Builder{}
	s := nocExecution{Out: out, Yes: true, RunActionCommand: func(string) error { ran = true; return nil }}
	err := runNOCActionCommand(s, send, send.Command, nil)
	if err == nil {
		t.Fatal("a stdin-body send must return an error, not silent success")
	}
	if !strings.Contains(err.Error(), "stdin-body send") {
		t.Errorf("error should explain the stdin-body refusal, got: %v", err)
	}
	if ran {
		t.Fatal("the action runner must not be invoked for a stdin-body send")
	}

	// --dry-run still inspects it (no error, no execution): display path preserved.
	ran = false
	dry := nocExecution{Out: &strings.Builder{}, DryRun: true, RunActionCommand: func(string) error { ran = true; return nil }}
	if err := runNOCActionCommand(dry, send, send.Command, nil); err != nil {
		t.Errorf("--dry-run must not error on a stdin-body send: %v", err)
	}
	if ran {
		t.Error("--dry-run must not execute the command")
	}
}

func TestSessionEnvelopeRuntimeProfile(t *testing.T) {
	cases := []struct {
		name   string
		agents []nocAgentJSONData
		want   string
	}{
		{"named profile passes through", []nocAgentJSONData{{TeamProfile: "review"}, {TeamProfile: "review"}}, "review"},
		{"default profile omitted", []nocAgentJSONData{{TeamProfile: "default"}}, ""},
		{"empty treated as default", []nocAgentJSONData{{TeamProfile: ""}}, ""},
		{"mixed profiles omitted", []nocAgentJSONData{{TeamProfile: "review"}, {TeamProfile: "build"}}, ""},
		{"no agents", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionEnvelopeRuntimeProfile(nocSessionJSONData{Agents: tc.agents}); got != tc.want {
				t.Errorf("profile = %q, want %q", got, tc.want)
			}
		})
	}
}
