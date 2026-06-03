package console

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-noc/internal/act"
	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/state"
)

// newPaletteModel builds a ready *NOCModel over a TWO-project / multi-session
// snapshot so the palette has several agents to fuzzy-filter across teams. The
// "beta" project's session carries a cto (codex, ALIVE) and a qa (claude,
// STOPPED); the "alpha" project carries an unrelated dev so the fuzzy match has
// to discriminate. Every seam is neutralized: no test touches a real bus/tmux.
func newPaletteModel(t *testing.T) *NOCModel {
	t.Helper()
	betaSess := state.Session{
		Name: "beta",
		Root: "/fake/proj/beta/.agent-mail",
		Agents: []state.Agent{
			{Handle: "cto", Role: "cto", Engine: "codex", Liveness: state.LivenessAlive},
			{Handle: "qa", Role: "qa", Engine: "claude", Liveness: state.LivenessDead},
		},
	}
	betaPS := noc.ProjectSnapshot{
		Project: "beta",
		Dir:     "/fake/proj/beta",
		Snap:    state.Snapshot{Sessions: []state.Session{betaSess}},
	}
	alphaSess := state.Session{
		Name: "alpha",
		Root: "/fake/proj/alpha/.agent-mail",
		Agents: []state.Agent{
			{Handle: "dev", Role: "dev", Engine: "codex", Liveness: state.LivenessAlive},
		},
	}
	alphaPS := noc.ProjectSnapshot{
		Project: "alpha",
		Dir:     "/fake/proj/alpha",
		Snap:    state.Snapshot{Sessions: []state.Session{alphaSess}},
	}
	ms := noc.MultiSnapshot{
		Roots:    []string{"/fake/proj"},
		Projects: []noc.ProjectSnapshot{betaPS, alphaPS},
	}

	m := newNOCModel(NOCRebuildConfig{Roots: []string{"/fake/proj"}})
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	m.panes = func() ([]noc.TmuxPane, error) { return nil, nil }
	m.switchTo = func(noc.TmuxTarget) error { return nil }
	m.pidTree = func(int) []int { return nil }

	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m2 := mm.(*NOCModel)
	mm, _ = m2.Update(nocSnapshotMsg{ms: ms})
	return mm.(*NOCModel)
}

// TestPalette_OpenAndClose proves 'p' opens the palette overlay and esc closes
// it, both through the PUBLIC Update (the level the live surface runs at).
func TestPalette_OpenAndClose(t *testing.T) {
	m := newPaletteModel(t)
	if m.palette != nil {
		t.Fatal("palette should start closed")
	}
	m, _ = nocPress(m, "p")
	if m.palette == nil {
		t.Fatal("p should open the command palette")
	}
	// The overlay renders its header (View dispatches to the palette overlay).
	if !strings.Contains(m.View(), "COMMAND PALETTE") {
		t.Errorf("open palette should render the palette overlay, got:\n%s", m.View())
	}
	m, _ = nocPress(m, "esc")
	if m.palette != nil {
		t.Error("esc should close the palette")
	}
}

func TestPalette_HelpDocumentsCreationActions(t *testing.T) {
	m := newPaletteModel(t)
	m, _ = nocPress(m, "?")
	out := m.View()
	for _, want := range []string{
		"projects, actions, teams, and agents",
		"project/action/status",
		"project/action/amq-env",
		"project/action/amq-who",
		"project/action/doctor",
		"project/action/history",
		"project/action/resume-plan",
		"project/action/team-rules",
		"project/action/new-team",
		"project/action/roles",
		"project/action/team-profiles",
		"project/action/sync-pointers",
		"new-profile",
		"new-session",
		"amq-ops",
		"amq-env",
		"amq-who",
		"amq-cleanup",
		"presence",
		"thread-context-any",
		"thread-context",
		"brief",
		"brief-seed",
		"fork-plan",
		"context",
		"read-needs-you",
		"read needs-you",
		"approve",
		"reply",
		"deny",
		"inbox",
		"dlq",
		"dlq-read",
		"read DLQ",
		"dlq-retry",
		"retry DLQ",
		"dlq-purge",
		"purge DLQ",
		"retry all DLQ",
		"receipts-wait",
		"wait receipts",
		"message-wait",
		"wait message",
		"drain",
		"resume agent",
		"project status",
		"session status",
		"threads",
		"thread context by id",
		"brief",
		"seed brief",
		"stop session",
		"resume session",
		"restart session",
		"archive session",
		"remove session",
		"amq cleanup",
		"amq env",
		"amq who",
		"preview-gated T/N editors",
		"create team",
		"doctor",
		"history",
		"resume plan",
		"fork plan",
		"role market",
		"team rules",
		"team profiles",
		"sync pointers",
		"start session",
		"session=issue-96",
		"rejects existing names",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("help should mention %q, got:\n%s", want, out)
		}
	}
}

// TestPalette_FuzzyFilterNarrows proves typing a fuzzy (subsequence) query
// narrows the candidate list to the matching agent. "betacto" is a subsequence
// of "beta/beta/cto" — it must keep the beta cto agent and drop the alpha dev.
func TestPalette_FuzzyFilterNarrows(t *testing.T) {
	m := newPaletteModel(t)
	m, _ = nocPress(m, "p")

	for _, ch := range "betacto" {
		m, _ = nocPress(m, string(ch))
	}
	res := m.palette.filtered()
	if len(res) == 0 {
		t.Fatalf("fuzzy %q should match at least the beta cto agent, got nothing", m.palette.query)
	}
	// Every survivor must be a fuzzy match; the beta cto agent must be among them
	// and the alpha dev must be gone.
	sawBetaCTO := false
	for _, it := range res {
		if !fuzzySubsequence(strings.ToLower(it.label), strings.ToLower(m.palette.query)) {
			t.Errorf("filtered result %q is not a fuzzy match for %q", it.label, m.palette.query)
		}
		if strings.Contains(it.label, "alpha") {
			t.Errorf("fuzzy %q must not keep the alpha dev row %q", m.palette.query, it.label)
		}
		if it.kind == palAgent && it.label == "beta/beta/cto" {
			sawBetaCTO = true
		}
	}
	if !sawBetaCTO {
		var labels []string
		for _, it := range res {
			labels = append(labels, it.label)
		}
		t.Errorf("fuzzy %q should narrow to the beta/beta/cto agent, got %v", m.palette.query, labels)
	}
}

func TestPalette_PastedQueryNarrows(t *testing.T) {
	m := newControlModel(t)
	addCandidateProject(m, "delta", "/fake/proj/delta")
	m, _ = nocPress(m, "p")
	m, _ = nocPress(m, "team")
	if m.palette == nil {
		t.Fatal("palette should remain open")
	}
	if m.palette.query != "team" {
		t.Fatalf("palette query = %q, want team", m.palette.query)
	}
	res := m.palette.filtered()
	if len(res) == 0 {
		t.Fatalf("pasted query should match create-team row")
	}
	found := false
	for _, it := range res {
		if it.label == "delta/action/new-team" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("pasted query should keep create-team row, got %+v", res)
	}
	if !strings.Contains(m.View(), "find: team") {
		t.Fatalf("palette view should render pasted query:\n%s", m.View())
	}
}

// TestPalette_EnterRunningAgentJumps proves enter on a RUNNING agent calls the
// switchTo seam with the NAME-FIRST-resolved target (the pane whose title token
// is amq:<session>:<role>), exactly like the tree's gated jump. The palette must
// close after selecting.
func TestPalette_EnterRunningAgentJumps(t *testing.T) {
	m := newPaletteModel(t)

	var gotTarget noc.TmuxTarget
	called := false
	m.switchTo = func(tt noc.TmuxTarget) error { called = true; gotTarget = tt; return nil }
	// The launcher stamps the deterministic title amq:beta:cto on the cto pane;
	// the name-first resolver must pick THAT pane even though another codex pane
	// shares the cwd. The decoy comes first so a cwd+engine-only resolver would
	// mis-pick it.
	m.panes = func() ([]noc.TmuxPane, error) {
		return []noc.TmuxPane{
			{Session: "decoy", Window: "9", Pane: "9", Command: "codex", CWD: "/fake/proj/beta", Title: "amq:beta:other"},
			{Session: "beta", Window: "0", Pane: "1", Command: "codex", CWD: "/fake/proj/beta", Title: "amq:beta:cto"},
		}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "betacto" {
		m, _ = nocPress(m, string(ch))
	}
	// The cto agent is the top match; select it.
	m, _ = nocPress(m, "enter")

	if !called {
		t.Fatal("enter on a running agent should call the switch seam (the gated jump)")
	}
	if gotTarget.Session != "beta" || gotTarget.Window != "0" || gotTarget.Pane != "1" {
		t.Errorf("jump should resolve name-first to amq:beta:cto's pane (beta:0.1), got %+v", gotTarget)
	}
	if m.palette != nil {
		t.Error("selecting should close the palette")
	}
}

// TestPalette_EnterStoppedAgentSuggestsUpNoSwitch proves selecting a STOPPED
// agent does NOT jump to a live pane: with no tmux window for the squad it sets
// the suggest-up note and calls the switch seam zero times.
func TestPalette_EnterStoppedAgentSuggestsUpNoSwitch(t *testing.T) {
	m := newPaletteModel(t)
	switched := false
	m.switchTo = func(noc.TmuxTarget) error { switched = true; return nil }
	// No tmux windows at all: the squad is not running.
	m.panes = func() ([]noc.TmuxPane, error) { return nil, nil }

	m, _ = nocPress(m, "p")
	// "betaqa" fuzzy-matches the STOPPED qa agent (beta/beta/qa).
	for _, ch := range "betaqa" {
		m, _ = nocPress(m, string(ch))
	}
	// Confirm the top match is the stopped qa agent before selecting.
	sel, ok := m.palette.selected()
	if !ok || sel.label != "beta/beta/qa" {
		t.Fatalf("expected the stopped beta/beta/qa agent selected, got %+v ok=%v", sel, ok)
	}
	if sel.running {
		t.Fatal("fixture qa agent must be stopped for this test")
	}

	m, _ = nocPress(m, "enter")
	if switched {
		t.Error("selecting a stopped agent with no live window must NOT call the switch seam")
	}
	if !strings.Contains(m.jumpNote, "team not running") ||
		!strings.Contains(m.jumpNote, "amq-squad new session --project /fake/proj/beta <name>") {
		t.Errorf("stopped-agent select should set the suggest-up note, got %q", m.jumpNote)
	}
	if m.palette != nil {
		t.Error("selecting should close the palette")
	}
}

// TestPalette_TeamRowFocusesExistingWindow proves selecting a TEAM row focuses an
// EXISTING tmux window for the squad (the focus path) rather than jumping to a
// single agent pane.
func TestPalette_TeamRowFocusesExistingWindow(t *testing.T) {
	m := newPaletteModel(t)
	var gotTarget noc.TmuxTarget
	called := false
	m.switchTo = func(tt noc.TmuxTarget) error { called = true; gotTarget = tt; return nil }
	m.panes = func() ([]noc.TmuxPane, error) {
		return []noc.TmuxPane{{Session: "beta", Window: "0", Pane: "1", Command: "codex", CWD: "/fake/proj/beta"}}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "betabetateam" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palTeam {
		t.Fatalf("expected the beta team row selected, got %+v ok=%v", sel, ok)
	}
	m, _ = nocPress(m, "enter")
	if !called {
		t.Fatal("selecting a team row with a live window should focus it via the switch seam")
	}
	if gotTarget.Session != "beta" {
		t.Errorf("team focus targeted the wrong session: %+v", gotTarget)
	}
}

func TestPalette_IncludesProjectAndCreationActions(t *testing.T) {
	m := newControlModel(t)
	addCandidateProject(m, "delta", "/fake/proj/delta")
	addConfiguredEmptyProject(m, "empty-team", "/fake/proj/empty-team")

	labels := map[string]bool{}
	for _, it := range buildPaletteItems(m.ms) {
		labels[it.label] = true
	}
	for _, want := range []string{
		"beta/project",
		"beta/action/status",
		"beta/action/amq-env",
		"beta/action/amq-who",
		"beta/action/doctor",
		"beta/action/history",
		"beta/action/resume-plan",
		"beta/action/roles",
		"beta/action/team-rules",
		"beta/action/team-profiles",
		"beta/action/sync-pointers",
		"beta/action/new-session",
		"beta/action/new-profile",
		"beta/beta/action/status",
		"beta/beta/action/threads",
		"beta/beta/action/thread-context-any",
		"beta/beta/action/brief",
		"beta/beta/action/brief-seed",
		"beta/beta/action/fork-plan",
		"beta/beta/action/stop",
		"beta/beta/action/resume",
		"beta/beta/action/restart",
		"beta/beta/action/thread-context",
		"beta/beta/action/read-needs-you",
		"beta/beta/action/reply",
		"beta/beta/action/approve",
		"beta/beta/action/deny",
		"beta/beta/action/broadcast",
		"beta/beta/action/amq-ops",
		"beta/beta/action/amq-cleanup",
		"beta/beta/action/presence",
		"beta/beta/action/archive",
		"beta/beta/action/remove",
		"beta/beta/qa/action/thread-context",
		"beta/beta/qa/action/read-needs-you",
		"beta/beta/qa/action/reply",
		"beta/beta/qa/action/approve",
		"beta/beta/qa/action/deny",
		"beta/beta/qa/action/inbox",
		"beta/beta/qa/action/drain",
		"beta/beta/qa/action/dlq",
		"beta/beta/qa/action/dlq-read",
		"beta/beta/qa/action/dlq-retry",
		"beta/beta/qa/action/dlq-purge",
		"beta/beta/qa/action/dlq-retry-all",
		"beta/beta/qa/action/receipts-wait",
		"beta/beta/qa/action/message",
		"beta/beta/qa/action/message-wait",
		"beta/beta/qa/action/agent-resume",
		"delta/project",
		"delta/action/doctor",
		"delta/action/roles",
		"delta/action/new-team",
		"empty-team/project",
		"empty-team/action/status",
		"empty-team/action/doctor",
		"empty-team/action/history",
		"empty-team/action/resume-plan",
		"empty-team/action/roles",
		"empty-team/action/team-rules",
		"empty-team/action/team-profiles",
		"empty-team/action/sync-pointers",
		"empty-team/action/new-session",
		"empty-team/action/new-profile",
	} {
		if !labels[want] {
			t.Errorf("palette missing %q", want)
		}
	}
}

func TestPalette_ActionAliasesFindCreationRows(t *testing.T) {
	m := newControlModel(t)
	addCandidateProject(m, "delta", "/fake/proj/delta")
	addConfiguredEmptyProject(m, "empty-team", "/fake/proj/empty-team")
	items := buildPaletteItems(m.ms)

	cases := []struct {
		query string
		label string
		tag   string
	}{
		{query: "beta doctor health", label: "beta/action/doctor", tag: "doctor"},
		{query: "beta history", label: "beta/action/history", tag: "history"},
		{query: "beta resume plan", label: "beta/action/resume-plan", tag: "resume plan"},
		{query: "beta amq env", label: "beta/action/amq-env", tag: "AMQ env"},
		{query: "beta amq who", label: "beta/action/amq-who", tag: "AMQ who"},
		{query: "beta project status", label: "beta/action/status", tag: "project status"},
		{query: "beta session status", label: "beta/beta/action/status", tag: "session status"},
		{query: "empty project status", label: "empty-team/action/status", tag: "project status"},
		{query: "beta team rules", label: "beta/action/team-rules", tag: "team rules"},
		{query: "beta threads", label: "beta/beta/action/threads", tag: "threads"},
		{query: "beta thread id", label: "beta/beta/action/thread-context-any", tag: "thread context by id"},
		{query: "beta brief", label: "beta/beta/action/brief", tag: "brief"},
		{query: "beta seed brief", label: "beta/beta/action/brief-seed", tag: "seed brief"},
		{query: "beta fork plan", label: "beta/beta/action/fork-plan", tag: "fork plan"},
		{query: "beta stop session", label: "beta/beta/action/stop", tag: "stop session"},
		{query: "beta resume session", label: "beta/beta/action/resume", tag: "resume session"},
		{query: "beta restart session", label: "beta/beta/action/restart", tag: "restart session"},
		{query: "delta create team", label: "delta/action/new-team", tag: "create team"},
		{query: "empty start workstream", label: "empty-team/action/new-session", tag: "start session"},
		{query: "beta create profile", label: "beta/action/new-profile", tag: "create profile"},
		{query: "delta role market", label: "delta/action/roles", tag: "role market"},
		{query: "beta team profiles", label: "beta/action/team-profiles", tag: "team profiles"},
		{query: "beta qa thread context", label: "beta/beta/qa/action/thread-context", tag: "thread context"},
		{query: "beta qa read needs you", label: "beta/beta/qa/action/read-needs-you", tag: "read needs-you"},
		{query: "beta qa approve", label: "beta/beta/qa/action/approve", tag: "approve"},
		{query: "beta qa reply", label: "beta/beta/qa/action/reply", tag: "reply"},
		{query: "beta qa deny", label: "beta/beta/qa/action/deny", tag: "deny"},
		{query: "beta broadcast", label: "beta/beta/action/broadcast", tag: "broadcast"},
		{query: "beta amq ops", label: "beta/beta/action/amq-ops", tag: "AMQ ops"},
		{query: "beta amq cleanup", label: "beta/beta/action/amq-cleanup", tag: "AMQ cleanup"},
		{query: "beta presence", label: "beta/beta/action/presence", tag: "presence"},
		{query: "beta qa inbox", label: "beta/beta/qa/action/inbox", tag: "inbox"},
		{query: "beta qa drain", label: "beta/beta/qa/action/drain", tag: "drain"},
		{query: "beta qa dlq", label: "beta/beta/qa/action/dlq", tag: "DLQ"},
		{query: "beta qa read dlq", label: "beta/beta/qa/action/dlq-read", tag: "read DLQ"},
		{query: "beta qa retry dlq", label: "beta/beta/qa/action/dlq-retry", tag: "retry DLQ"},
		{query: "beta qa purge dlq", label: "beta/beta/qa/action/dlq-purge", tag: "purge DLQ"},
		{query: "beta qa retry all dlq", label: "beta/beta/qa/action/dlq-retry-all", tag: "retry all DLQ"},
		{query: "beta qa wait receipts", label: "beta/beta/qa/action/receipts-wait", tag: "wait receipts"},
		{query: "beta qa message", label: "beta/beta/qa/action/message", tag: "message"},
		{query: "beta qa wait message", label: "beta/beta/qa/action/message-wait", tag: "wait message"},
		{query: "beta qa resume agent", label: "beta/beta/qa/action/agent-resume", tag: "resume agent"},
		{query: "beta archive session", label: "beta/beta/action/archive", tag: "archive session"},
		{query: "beta remove session", label: "beta/beta/action/remove", tag: "remove session"},
	}
	for _, tc := range cases {
		p := paletteState{query: tc.query, items: items}
		got, ok := p.selected()
		if !ok {
			t.Fatalf("query %q should find %q", tc.query, tc.label)
		}
		if got.label != tc.label {
			t.Fatalf("query %q selected %q, want %q", tc.query, got.label, tc.label)
		}
		if tag := paletteActionLabel(got); tag != tc.tag {
			t.Fatalf("query %q tag = %q, want %q", tc.query, tag, tc.tag)
		}
	}
}

func TestPalette_DoctorActionShowsResultWithoutMutation(t *testing.T) {
	m := newControlModel(t)
	var got []projectDoctorOp
	m.projectDoctor = func(op projectDoctorOp) (projectDoctorResult, error) {
		got = append(got, op)
		return projectDoctorResult{ProjectDir: op.ProjectDir, Output: "STATUS  CHECK\nok      amq version\nfail    pointer sync\n\ndoctor: 1 check(s) failed\n"}, nil
	}
	mutated := false
	m.sendOp = func(act.OpMessage) error { mutated = true; return nil }
	m.lifecycle = func(lifecycleOp) error { mutated = true; return nil }
	m.newSession = func(newSessionOp) error { mutated = true; return nil }
	m.newTeam = func(newTeamOp) error { mutated = true; return nil }
	m.drainAgent = func(drainAgentOp) (drainAgentResult, error) {
		mutated = true
		return drainAgentResult{}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "beta doctor health" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palDoctor {
		t.Fatalf("expected doctor action selected, got %+v ok=%v", sel, ok)
	}
	m, cmd := nocPress(m, "enter")

	if m.palette != nil {
		t.Fatal("selecting doctor action should close the palette")
	}
	if m.input != nil || m.pending != nil {
		t.Fatalf("doctor action must not open input/confirm, input=%+v pending=%+v", m.input, m.pending)
	}
	if cmd != nil {
		t.Fatal("read-only palette doctor should not request a rebuild")
	}
	if mutated {
		t.Fatal("palette doctor must not call mutating seams")
	}
	if len(got) != 1 {
		t.Fatalf("palette doctor should call the doctor seam once, got %d", len(got))
	}
	if got[0].ProjectDir != "/fake/proj/beta" {
		t.Fatalf("doctor project mismatch: %+v", got[0])
	}
	if m.projectDoctorResult == nil {
		t.Fatal("palette doctor should open a result overlay")
	}
	out := m.View()
	for _, want := range []string{
		"PROJECT DOCTOR",
		"amq-squad doctor --project /fake/proj/beta --all-profiles",
		"fail    pointer sync",
		"doctor: 1 check(s) failed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor overlay missing %q:\n%s", want, out)
		}
	}
	m, _ = nocPress(m, "enter")
	if m.projectDoctorResult != nil {
		t.Fatal("enter should close the doctor overlay")
	}
}

func TestPalette_HistoryActionShowsResultWithoutMutation(t *testing.T) {
	m := newControlModel(t)
	var got []projectHistoryOp
	m.projectHistory = func(op projectHistoryOp) (projectHistoryResult, error) {
		got = append(got, op)
		return projectHistoryResult{ProjectDir: op.ProjectDir, Output: "ROLE\tHANDLE\tBINARY\tSESSION\nqa\tqa\tclaude\tbeta\n"}, nil
	}
	mutated := false
	m.sendOp = func(act.OpMessage) error { mutated = true; return nil }
	m.lifecycle = func(lifecycleOp) error { mutated = true; return nil }
	m.newSession = func(newSessionOp) error { mutated = true; return nil }
	m.newTeam = func(newTeamOp) error { mutated = true; return nil }
	m.drainAgent = func(drainAgentOp) (drainAgentResult, error) {
		mutated = true
		return drainAgentResult{}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "beta history" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palHistory {
		t.Fatalf("expected history action selected, got %+v ok=%v", sel, ok)
	}
	m, cmd := nocPress(m, "enter")

	if m.palette != nil {
		t.Fatal("selecting history action should close the palette")
	}
	if m.input != nil || m.pending != nil {
		t.Fatalf("history action must not open input/confirm, input=%+v pending=%+v", m.input, m.pending)
	}
	if cmd != nil {
		t.Fatal("read-only palette history should not request a rebuild")
	}
	if mutated {
		t.Fatal("palette history must not call mutating seams")
	}
	if len(got) != 1 {
		t.Fatalf("palette history should call the history seam once, got %d", len(got))
	}
	if got[0].ProjectDir != "/fake/proj/beta" {
		t.Fatalf("history project mismatch: %+v", got[0])
	}
	if m.projectHistoryResult == nil {
		t.Fatal("palette history should open a result overlay")
	}
	out := m.View()
	for _, want := range []string{
		"HISTORY",
		"amq-squad history --project /fake/proj/beta",
		"qa\tqa\tclaude\tbeta",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("history overlay missing %q:\n%s", want, out)
		}
	}
	m, _ = nocPress(m, "enter")
	if m.projectHistoryResult != nil {
		t.Fatal("enter should close the history overlay")
	}
}

func TestPalette_ResumePlanActionShowsResultWithoutMutation(t *testing.T) {
	m := newControlModel(t)
	var got []projectResumePlanOp
	m.projectResumePlan = func(op projectResumePlanOp) (projectResumePlanResult, error) {
		got = append(got, op)
		return projectResumePlanResult{ProjectDir: op.ProjectDir, Profile: op.Profile, Output: "# amq-squad resume\nROLE\tACTION\tWAKE\tNOTE\nqa\trestore\tstale\tlaunch record\n"}, nil
	}
	mutated := false
	m.sendOp = func(act.OpMessage) error { mutated = true; return nil }
	m.lifecycle = func(lifecycleOp) error { mutated = true; return nil }
	m.newSession = func(newSessionOp) error { mutated = true; return nil }
	m.newTeam = func(newTeamOp) error { mutated = true; return nil }
	m.drainAgent = func(drainAgentOp) (drainAgentResult, error) {
		mutated = true
		return drainAgentResult{}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "beta resume plan" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palResumePlan {
		t.Fatalf("expected resume-plan action selected, got %+v ok=%v", sel, ok)
	}
	m, cmd := nocPress(m, "enter")

	if m.palette != nil {
		t.Fatal("selecting resume-plan action should close the palette")
	}
	if m.input != nil || m.pending != nil {
		t.Fatalf("resume-plan action must not open input/confirm, input=%+v pending=%+v", m.input, m.pending)
	}
	if cmd != nil {
		t.Fatal("read-only palette resume-plan should not request a rebuild")
	}
	if mutated {
		t.Fatal("palette resume-plan must not call mutating seams")
	}
	if len(got) != 1 {
		t.Fatalf("palette resume-plan should call the seam once, got %d", len(got))
	}
	if got[0].ProjectDir != "/fake/proj/beta" || got[0].Profile != "" {
		t.Fatalf("resume-plan op mismatch: %+v", got[0])
	}
	if m.projectResumePlanResult == nil {
		t.Fatal("palette resume-plan should open a result overlay")
	}
	out := m.View()
	for _, want := range []string{
		"RESUME PLAN",
		"amq-squad resume --project /fake/proj/beta",
		"# amq-squad resume",
		"qa\trestore\tstale\tlaunch record",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("resume-plan overlay missing %q:\n%s", want, out)
		}
	}
	m, _ = nocPress(m, "enter")
	if m.projectResumePlanResult != nil {
		t.Fatal("enter should close the resume-plan overlay")
	}
}

func TestPalette_ForkPlanActionShowsResultWithoutMutation(t *testing.T) {
	m := newControlModel(t)
	var got []forkPlanOp
	m.forkPlan = func(op forkPlanOp) (forkPlanResult, error) {
		got = append(got, op)
		return forkPlanResult{
			ProjectDir:  op.ProjectDir,
			Profile:     op.Profile,
			FromSession: op.FromSession,
			ToSession:   op.ToSession,
			Output:      "# amq-squad fork\nROLE\tACTION\tWAKE\tNOTE\nqa\tlaunch fresh\t-\tbranched\n",
		}, nil
	}
	mutated := false
	m.sendOp = func(act.OpMessage) error { mutated = true; return nil }
	m.lifecycle = func(lifecycleOp) error { mutated = true; return nil }
	m.newSession = func(newSessionOp) error { mutated = true; return nil }
	m.newTeam = func(newTeamOp) error { mutated = true; return nil }
	m.drainAgent = func(drainAgentOp) (drainAgentResult, error) {
		mutated = true
		return drainAgentResult{}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "beta fork plan" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palForkPlan {
		t.Fatalf("expected fork-plan action selected, got %+v ok=%v", sel, ok)
	}
	m, cmd := nocPress(m, "enter")
	if m.palette != nil {
		t.Fatal("selecting fork-plan action should close the palette")
	}
	if cmd != nil {
		t.Fatal("opening fork-plan input should not request a rebuild")
	}
	if m.input == nil || m.input.kind != ctlForkPlan {
		t.Fatalf("fork-plan should open target-session input, got %+v", m.input)
	}
	for _, ch := range "beta-next" {
		m, _ = nocPress(m, string(ch))
	}
	m, cmd = nocPress(m, "enter")

	if m.input != nil || m.pending != nil {
		t.Fatalf("fork-plan action must not open confirm, input=%+v pending=%+v", m.input, m.pending)
	}
	if cmd != nil {
		t.Fatal("read-only fork-plan should not request a rebuild")
	}
	if mutated {
		t.Fatal("palette fork-plan must not call mutating seams")
	}
	if len(got) != 1 {
		t.Fatalf("palette fork-plan should call the seam once, got %d", len(got))
	}
	if got[0].ProjectDir != "/fake/proj/beta" || got[0].Profile != "" || got[0].FromSession != "beta" || got[0].ToSession != "beta-next" {
		t.Fatalf("fork-plan op mismatch: %+v", got[0])
	}
	if m.forkPlanResult == nil {
		t.Fatal("palette fork-plan should open a result overlay")
	}
	out := m.View()
	for _, want := range []string{
		"FORK PLAN",
		"amq-squad fork --project /fake/proj/beta --from beta --as beta-next",
		"# amq-squad fork",
		"qa\tlaunch fresh\t-\tbranched",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("fork-plan overlay missing %q:\n%s", want, out)
		}
	}
	m, _ = nocPress(m, "enter")
	if m.forkPlanResult != nil {
		t.Fatal("enter should close the fork-plan overlay")
	}
}

func TestPalette_StatusActionShowsResultWithoutMutation(t *testing.T) {
	m := newControlModel(t)
	var got []statusOp
	m.status = func(op statusOp) (statusResult, error) {
		got = append(got, op)
		return statusResult{ProjectDir: op.ProjectDir, Session: op.Session, Profile: op.Profile, Output: "ROLE  HANDLE  STATUS\nqa    qa      live\n"}, nil
	}
	mutated := false
	m.sendOp = func(act.OpMessage) error { mutated = true; return nil }
	m.lifecycle = func(lifecycleOp) error { mutated = true; return nil }
	m.newSession = func(newSessionOp) error { mutated = true; return nil }
	m.newTeam = func(newTeamOp) error { mutated = true; return nil }
	m.drainAgent = func(drainAgentOp) (drainAgentResult, error) {
		mutated = true
		return drainAgentResult{}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "beta session status" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palStatus {
		t.Fatalf("expected status action selected, got %+v ok=%v", sel, ok)
	}
	m, cmd := nocPress(m, "enter")

	if m.palette != nil {
		t.Fatal("selecting status action should close the palette")
	}
	if m.input != nil || m.pending != nil {
		t.Fatalf("status action must not open input/confirm, input=%+v pending=%+v", m.input, m.pending)
	}
	if cmd != nil {
		t.Fatal("read-only palette status should not request a rebuild")
	}
	if mutated {
		t.Fatal("palette status must not call mutating seams")
	}
	if len(got) != 1 {
		t.Fatalf("palette status should call the status seam once, got %d", len(got))
	}
	if got[0].ProjectDir != "/fake/proj/beta" || got[0].Session != "beta" {
		t.Fatalf("status op mismatch: %+v", got[0])
	}
	if m.statusResult == nil {
		t.Fatal("palette status should open a result overlay")
	}
	out := m.View()
	for _, want := range []string{
		"STATUS",
		"amq-squad status --project /fake/proj/beta --session beta",
		"ROLE  HANDLE  STATUS",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("status overlay missing %q:\n%s", want, out)
		}
	}
	m, _ = nocPress(m, "enter")
	if m.statusResult != nil {
		t.Fatal("enter should close the status overlay")
	}
}

func TestPalette_BriefActionShowsResultWithoutMutation(t *testing.T) {
	m := newControlModel(t)
	var got []briefOp
	m.brief = func(op briefOp) (briefResult, error) {
		got = append(got, op)
		return briefResult{
			ProjectDir: op.ProjectDir,
			Session:    op.Session,
			Path:       op.ProjectDir + "/.amq-squad/briefs/" + op.Session + ".md",
			Kind:       "real",
			Exists:     true,
			Content:    "# beta\n\nShip it\n",
		}, nil
	}
	mutated := false
	m.sendOp = func(act.OpMessage) error { mutated = true; return nil }
	m.lifecycle = func(lifecycleOp) error { mutated = true; return nil }
	m.newSession = func(newSessionOp) error { mutated = true; return nil }
	m.newTeam = func(newTeamOp) error { mutated = true; return nil }
	m.drainAgent = func(drainAgentOp) (drainAgentResult, error) {
		mutated = true
		return drainAgentResult{}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "beta brief" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palBrief {
		t.Fatalf("expected brief action selected, got %+v ok=%v", sel, ok)
	}
	m, cmd := nocPress(m, "enter")

	if m.palette != nil {
		t.Fatal("selecting brief action should close the palette")
	}
	if m.input != nil || m.pending != nil {
		t.Fatalf("brief action must not open input/confirm, input=%+v pending=%+v", m.input, m.pending)
	}
	if cmd != nil {
		t.Fatal("read-only palette brief should not request a rebuild")
	}
	if mutated {
		t.Fatal("palette brief must not call mutating seams")
	}
	if len(got) != 1 {
		t.Fatalf("palette brief should call the brief seam once, got %d", len(got))
	}
	if got[0].ProjectDir != "/fake/proj/beta" || got[0].Session != "beta" {
		t.Fatalf("brief op mismatch: %+v", got[0])
	}
	if m.briefResult == nil {
		t.Fatal("palette brief should open a result overlay")
	}
	out := m.View()
	for _, want := range []string{
		"BRIEF",
		"amq-squad brief --project /fake/proj/beta --session beta",
		"kind: real",
		"# beta",
		"Ship it",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("brief overlay missing %q:\n%s", want, out)
		}
	}
	m, _ = nocPress(m, "enter")
	if m.briefResult != nil {
		t.Fatal("enter should close the brief overlay")
	}
}

func TestPalette_BriefSeedActionOpensPreviewGatedFlow(t *testing.T) {
	m := newControlModel(t)
	var got []briefSeedOp
	m.briefSeed = func(op briefSeedOp) error {
		got = append(got, op)
		return nil
	}
	mutated := false
	m.sendOp = func(act.OpMessage) error { mutated = true; return nil }
	m.lifecycle = func(lifecycleOp) error { mutated = true; return nil }
	m.newSession = func(newSessionOp) error { mutated = true; return nil }
	m.newTeam = func(newTeamOp) error { mutated = true; return nil }
	m.drainAgent = func(drainAgentOp) (drainAgentResult, error) {
		mutated = true
		return drainAgentResult{}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "beta seed brief" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palBriefSeed {
		t.Fatalf("expected brief-seed action selected, got %+v ok=%v", sel, ok)
	}
	m, cmd := nocPress(m, "enter")
	if m.palette != nil {
		t.Fatal("selecting brief-seed action should close the palette")
	}
	if cmd != nil {
		t.Fatal("opening brief-seed input should not request a rebuild")
	}
	if m.input == nil || m.input.kind != ctlBriefSeed {
		t.Fatalf("brief-seed should open seed input, got %+v", m.input)
	}
	for _, ch := range "issue:31 force=true" {
		m, _ = nocPress(m, string(ch))
	}
	m, cmd = nocPress(m, "enter")
	if m.input != nil || m.pending == nil {
		t.Fatalf("brief-seed should move from input to pending confirm, input=%+v pending=%+v", m.input, m.pending)
	}
	if cmd != nil {
		t.Fatal("opening brief-seed confirm should not request a rebuild")
	}
	if m.pending.kind != ctlBriefSeed || m.pending.brief == nil {
		t.Fatalf("brief-seed pending mismatch: %+v", m.pending)
	}
	for _, want := range []string{
		"BRIEF SEED",
		"amq-squad brief seed --project /fake/proj/beta --session beta --seed-from issue:31 --force",
	} {
		if !strings.Contains(m.View(), want) {
			t.Fatalf("brief-seed confirm missing %q:\n%s", want, m.View())
		}
	}
	m, cmd = nocPress(m, "y")
	if cmd == nil {
		t.Fatal("confirmed brief-seed should request a rebuild")
	}
	if mutated {
		t.Fatal("brief-seed should not call unrelated mutating seams")
	}
	if len(got) != 1 {
		t.Fatalf("brief-seed seam calls = %d, want 1", len(got))
	}
	if got[0].ProjectDir != "/fake/proj/beta" || got[0].Session != "beta" || got[0].SeedFrom != "issue:31" || !got[0].Force {
		t.Fatalf("brief-seed op mismatch: %+v", got[0])
	}
}

func TestPalette_LifecycleActionsOpenPreviewGatedFlow(t *testing.T) {
	for _, tc := range []struct {
		query       string
		action      paletteAction
		kind        controlKind
		verb        lifecycleVerb
		previewPart string
	}{
		{
			query:       "beta stop session",
			action:      palStop,
			kind:        ctlStop,
			verb:        lifecycleStop,
			previewPart: "amq-squad stop --project /fake/proj/beta --all --session beta",
		},
		{
			query:       "beta resume session",
			action:      palResume,
			kind:        ctlResume,
			verb:        lifecycleResume,
			previewPart: "amq-squad resume --project /fake/proj/beta --exec --target new-session --terminal-session amq-squad-beta --session beta",
		},
		{
			query:       "beta restart session",
			action:      palRestart,
			kind:        ctlRestart,
			verb:        lifecycleRestart,
			previewPart: " && amq-squad resume --project /fake/proj/beta --exec --target new-session --terminal-session amq-squad-beta --session beta",
		},
	} {
		t.Run(tc.query, func(t *testing.T) {
			m := newControlModel(t)
			var ops []lifecycleOp
			m.lifecycle = func(op lifecycleOp) error {
				ops = append(ops, op)
				return nil
			}

			m, _ = nocPress(m, "p")
			for _, ch := range tc.query {
				m, _ = nocPress(m, string(ch))
			}
			sel, ok := m.palette.selected()
			if !ok || sel.kind != palAction || sel.action != tc.action {
				t.Fatalf("expected lifecycle action selected, got %+v ok=%v", sel, ok)
			}
			m, _ = nocPress(m, "enter")
			if m.palette != nil {
				t.Fatal("selecting lifecycle action should close the palette")
			}
			if len(ops) != 0 {
				t.Fatal("selecting lifecycle must not call the seam before confirm")
			}
			if m.pending == nil || m.pending.kind != tc.kind || m.pending.life == nil {
				t.Fatalf("lifecycle action should open a confirm overlay, pending=%+v", m.pending)
			}
			if !strings.Contains(m.pending.preview, tc.previewPart) {
				t.Fatalf("lifecycle preview missing %q: %q", tc.previewPart, m.pending.preview)
			}
			m, _ = nocPress(m, "y")
			if len(ops) != 1 {
				t.Fatalf("confirming should call the lifecycle seam once, got %d", len(ops))
			}
			if ops[0].ProjectDir != "/fake/proj/beta" || ops[0].Session != "beta" || ops[0].Verb != tc.verb {
				t.Fatalf("lifecycle op mismatch: %+v", ops[0])
			}
		})
	}
}

func TestPalette_NeedsYouActionsOpenExpectedFlows(t *testing.T) {
	t.Run("read needs-you opens confirmed read flow", func(t *testing.T) {
		m := newControlModel(t)
		var ops []readNeedsYouOp
		m.readNeedsYou = func(op readNeedsYouOp) (readNeedsYouResult, error) {
			ops = append(ops, op)
			return readNeedsYouResult{MessageID: op.MessageID, Thread: op.Thread, Subject: op.Subject, Body: "Please approve"}, nil
		}

		m, _ = nocPress(m, "p")
		for _, ch := range "beta qa read needs you" {
			m, _ = nocPress(m, string(ch))
		}
		sel, ok := m.palette.selected()
		if !ok || sel.kind != palAction || sel.action != palReadNeedsYou {
			t.Fatalf("expected read-needs-you action selected, got %+v ok=%v", sel, ok)
		}
		m, _ = nocPress(m, "enter")
		if len(ops) != 0 {
			t.Fatal("selecting read-needs-you must not call the seam before confirm")
		}
		if m.pending == nil || m.pending.kind != ctlRead || m.pending.read == nil {
			t.Fatalf("read-needs-you action should open a confirm overlay, pending=%+v", m.pending)
		}
		m, _ = nocPress(m, "y")
		if len(ops) != 1 {
			t.Fatalf("confirming should call read seam once, got %d", len(ops))
		}
		if ops[0].Root != ctlRoot || ops[0].MessageID != "msg-ship" || ops[0].Thread != "decision/ship" {
			t.Fatalf("read op mismatch: %+v", ops[0])
		}
	})

	t.Run("approve opens confirmed AMQ send flow", func(t *testing.T) {
		m := newControlModel(t)
		var sent []act.OpMessage
		m.sendOp = func(op act.OpMessage) error {
			sent = append(sent, op)
			return nil
		}

		m, _ = nocPress(m, "p")
		for _, ch := range "beta qa approve" {
			m, _ = nocPress(m, string(ch))
		}
		sel, ok := m.palette.selected()
		if !ok || sel.kind != palAction || sel.action != palApprove {
			t.Fatalf("expected approve action selected, got %+v ok=%v", sel, ok)
		}
		m, _ = nocPress(m, "enter")
		if len(sent) != 0 {
			t.Fatal("selecting approve must not call send before confirm")
		}
		if m.pending == nil || m.pending.kind != ctlApprove {
			t.Fatalf("approve action should open a confirm overlay, pending=%+v", m.pending)
		}
		m, _ = nocPress(m, "y")
		if len(sent) != 1 {
			t.Fatalf("confirming should send once, got %d", len(sent))
		}
		if sent[0].Root != ctlRoot || sent[0].Thread != "decision/ship" || sent[0].To != "qa" {
			t.Fatalf("approve send mismatch: %+v", sent[0])
		}
	})

	t.Run("reply captures body before confirmed AMQ send", func(t *testing.T) {
		m := newControlModel(t)
		var sent []act.OpMessage
		m.sendOp = func(op act.OpMessage) error {
			sent = append(sent, op)
			return nil
		}

		m, _ = nocPress(m, "p")
		for _, ch := range "beta qa reply" {
			m, _ = nocPress(m, string(ch))
		}
		sel, ok := m.palette.selected()
		if !ok || sel.kind != palAction || sel.action != palReply {
			t.Fatalf("expected reply action selected, got %+v ok=%v", sel, ok)
		}
		m, _ = nocPress(m, "enter")
		if m.input == nil || m.input.kind != ctlReply {
			t.Fatalf("reply action should open body input, input=%+v", m.input)
		}
		m = typeControlText(t, m, "Approved with notes")
		m, _ = nocPress(m, "enter")
		if len(sent) != 0 {
			t.Fatal("entering reply body should preview, not send")
		}
		if m.pending == nil || m.pending.kind != ctlReply {
			t.Fatalf("reply should open confirm overlay, pending=%+v", m.pending)
		}
		m, _ = nocPress(m, "y")
		if len(sent) != 1 || sent[0].Body != "Approved with notes" || sent[0].Thread != "decision/ship" {
			t.Fatalf("reply send mismatch: %+v", sent)
		}
	})

	t.Run("deny captures reason before confirmed AMQ send", func(t *testing.T) {
		m := newControlModel(t)
		var sent []act.OpMessage
		m.sendOp = func(op act.OpMessage) error {
			sent = append(sent, op)
			return nil
		}

		m, _ = nocPress(m, "p")
		for _, ch := range "beta qa deny" {
			m, _ = nocPress(m, string(ch))
		}
		sel, ok := m.palette.selected()
		if !ok || sel.kind != palAction || sel.action != palDeny {
			t.Fatalf("expected deny action selected, got %+v ok=%v", sel, ok)
		}
		m, _ = nocPress(m, "enter")
		if m.input == nil || m.input.kind != ctlDeny {
			t.Fatalf("deny action should open reason input, input=%+v", m.input)
		}
		m = typeControlText(t, m, "Need more validation")
		m, _ = nocPress(m, "enter")
		if len(sent) != 0 {
			t.Fatal("entering deny reason should preview, not send")
		}
		if m.pending == nil || m.pending.kind != ctlDeny {
			t.Fatalf("deny should open confirm overlay, pending=%+v", m.pending)
		}
		m, _ = nocPress(m, "y")
		if len(sent) != 1 || sent[0].Thread != "decision/ship" || !strings.Contains(sent[0].Body, "Need more validation") {
			t.Fatalf("deny send mismatch: %+v", sent)
		}
	})
}

func TestPalette_BroadcastActionOpensPreviewGatedFlow(t *testing.T) {
	m := newControlModel(t)
	var sent []act.OpMessage
	m.sendOp = func(op act.OpMessage) error {
		sent = append(sent, op)
		return nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "beta broadcast" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palBroadcast {
		t.Fatalf("expected broadcast action selected, got %+v ok=%v", sel, ok)
	}
	m, _ = nocPress(m, "enter")
	if m.palette != nil {
		t.Fatal("selecting broadcast action should close the palette")
	}
	if len(sent) != 0 {
		t.Fatal("selecting broadcast must not call send before confirm")
	}
	if m.input == nil || m.input.kind != ctlBroadcast || m.input.stage != 0 {
		t.Fatalf("broadcast action should open subject input, input=%+v", m.input)
	}
	m = typeControlText(t, m, "Update")
	m, _ = nocPress(m, "enter")
	if m.input == nil || m.input.kind != ctlBroadcast || m.input.stage != 1 {
		t.Fatalf("broadcast subject should advance to body input, input=%+v", m.input)
	}
	m = typeControlText(t, m, "Please report status")
	m, _ = nocPress(m, "enter")
	if len(sent) != 0 {
		t.Fatal("entering broadcast body should preview, not send")
	}
	if m.pending == nil || m.pending.kind != ctlBroadcast {
		t.Fatalf("broadcast should open confirm overlay, pending=%+v", m.pending)
	}
	m, _ = nocPress(m, "y")
	if len(sent) != 1 {
		t.Fatalf("confirming should send broadcast once, got %d", len(sent))
	}
	if sent[0].Root != ctlRoot || sent[0].To != "dev,qa" || sent[0].Subject != "Update" || sent[0].Body != "Please report status" {
		t.Fatalf("broadcast send mismatch: %+v", sent[0])
	}
}

func TestPalette_AMQOpsActionShowsResultWithoutMutation(t *testing.T) {
	m := newControlModel(t)
	var got []amqOpsOp
	m.amqOps = func(op amqOpsOp) (amqOpsResult, error) {
		got = append(got, op)
		return amqOpsResult{Root: op.Root, Output: "Ops:\n  qa: 2 unread, presence fresh\n"}, nil
	}
	mutated := false
	m.sendOp = func(act.OpMessage) error { mutated = true; return nil }
	m.lifecycle = func(lifecycleOp) error { mutated = true; return nil }
	m.newSession = func(newSessionOp) error { mutated = true; return nil }
	m.newTeam = func(newTeamOp) error { mutated = true; return nil }
	m.drainAgent = func(drainAgentOp) (drainAgentResult, error) {
		mutated = true
		return drainAgentResult{}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "beta amq ops" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palAMQOps {
		t.Fatalf("expected amq ops action selected, got %+v ok=%v", sel, ok)
	}
	m, cmd := nocPress(m, "enter")

	if m.palette != nil {
		t.Fatal("selecting AMQ ops action should close the palette")
	}
	if m.input != nil || m.pending != nil {
		t.Fatalf("AMQ ops action must not open input/confirm, input=%+v pending=%+v", m.input, m.pending)
	}
	if cmd != nil {
		t.Fatal("read-only palette AMQ ops should not request a rebuild")
	}
	if mutated {
		t.Fatal("palette AMQ ops must not call mutating seams")
	}
	if len(got) != 1 {
		t.Fatalf("palette AMQ ops should call the AMQ ops seam once, got %d", len(got))
	}
	if got[0].Root != ctlRoot {
		t.Fatalf("AMQ ops root mismatch: %+v", got[0])
	}
	if m.amqOpsResult == nil {
		t.Fatal("palette AMQ ops should open a result overlay")
	}
	out := m.View()
	for _, want := range []string{"AMQ OPS", "qa: 2 unread", "env AM_ROOT=/fake/root/.agent-mail amq doctor --ops"} {
		if !strings.Contains(out, want) {
			t.Fatalf("AMQ ops overlay missing %q:\n%s", want, out)
		}
	}
}

func TestPalette_AMQWhoActionShowsResultWithoutMutation(t *testing.T) {
	m := newControlModel(t)
	var got []amqWhoOp
	m.amqWho = func(op amqWhoOp) (amqWhoResult, error) {
		got = append(got, op)
		return amqWhoResult{Root: op.Root, Output: "  beta\n    qa  active\n    dev  stale\n"}, nil
	}
	mutated := false
	m.sendOp = func(act.OpMessage) error { mutated = true; return nil }
	m.lifecycle = func(lifecycleOp) error { mutated = true; return nil }
	m.newSession = func(newSessionOp) error { mutated = true; return nil }
	m.newTeam = func(newTeamOp) error { mutated = true; return nil }
	m.drainAgent = func(drainAgentOp) (drainAgentResult, error) {
		mutated = true
		return drainAgentResult{}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "beta amq who" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palAMQWho {
		t.Fatalf("expected AMQ who action selected, got %+v ok=%v", sel, ok)
	}
	m, cmd := nocPress(m, "enter")

	if m.palette != nil {
		t.Fatal("selecting AMQ who action should close the palette")
	}
	if m.input != nil || m.pending != nil {
		t.Fatalf("AMQ who action must not open input/confirm, input=%+v pending=%+v", m.input, m.pending)
	}
	if cmd != nil {
		t.Fatal("read-only palette AMQ who should not request a rebuild")
	}
	if mutated {
		t.Fatal("palette AMQ who must not call mutating seams")
	}
	if len(got) != 1 {
		t.Fatalf("palette AMQ who should call the AMQ who seam once, got %d", len(got))
	}
	wantRoot := "/fake/proj/beta/.agent-mail"
	if got[0].Root != wantRoot {
		t.Fatalf("AMQ who root mismatch: %+v, want %s", got[0], wantRoot)
	}
	if m.amqWhoResult == nil {
		t.Fatal("palette AMQ who should open a result overlay")
	}
	out := m.View()
	for _, want := range []string{"AMQ WHO", "qa  active", "amq who --root /fake/proj/beta/.agent-mail"} {
		if !strings.Contains(out, want) {
			t.Fatalf("AMQ who overlay missing %q:\n%s", want, out)
		}
	}
}

func TestPalette_AMQEnvActionShowsResultWithoutMutation(t *testing.T) {
	m := newControlModel(t)
	var got []amqEnvOp
	m.amqEnv = func(op amqEnvOp) (amqEnvResult, error) {
		got = append(got, op)
		return amqEnvResult{Root: op.Root, Output: `{"root":"/fake/proj/beta/.agent-mail","project":"beta","peers":{"pm":"/pm/.agent-mail"}}` + "\n"}, nil
	}
	mutated := false
	m.sendOp = func(act.OpMessage) error { mutated = true; return nil }
	m.lifecycle = func(lifecycleOp) error { mutated = true; return nil }
	m.newSession = func(newSessionOp) error { mutated = true; return nil }
	m.newTeam = func(newTeamOp) error { mutated = true; return nil }
	m.drainAgent = func(drainAgentOp) (drainAgentResult, error) {
		mutated = true
		return drainAgentResult{}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "beta amq env" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palAMQEnv {
		t.Fatalf("expected AMQ env action selected, got %+v ok=%v", sel, ok)
	}
	m, cmd := nocPress(m, "enter")

	if m.palette != nil {
		t.Fatal("selecting AMQ env action should close the palette")
	}
	if m.input != nil || m.pending != nil {
		t.Fatalf("AMQ env action must not open input/confirm, input=%+v pending=%+v", m.input, m.pending)
	}
	if cmd != nil {
		t.Fatal("read-only palette AMQ env should not request a rebuild")
	}
	if mutated {
		t.Fatal("palette AMQ env must not call mutating seams")
	}
	if len(got) != 1 {
		t.Fatalf("palette AMQ env should call the AMQ env seam once, got %d", len(got))
	}
	wantRoot := "/fake/proj/beta/.agent-mail"
	if got[0].Root != wantRoot {
		t.Fatalf("AMQ env root mismatch: %+v, want %s", got[0], wantRoot)
	}
	if m.amqEnvResult == nil {
		t.Fatal("palette AMQ env should open a result overlay")
	}
	out := m.View()
	for _, want := range []string{"AMQ ENV", `"project":"beta"`, "amq env --root /fake/proj/beta/.agent-mail --json"} {
		if !strings.Contains(out, want) {
			t.Fatalf("AMQ env overlay missing %q:\n%s", want, out)
		}
	}
}

func TestPalette_PresenceActionShowsResultWithoutMutation(t *testing.T) {
	m := newControlModel(t)
	var got []presenceOp
	m.presence = func(op presenceOp) (presenceResult, error) {
		got = append(got, op)
		return presenceResult{Root: op.Root, Output: "qa  active  2026-06-01T09:00:00Z\n"}, nil
	}
	mutated := false
	m.sendOp = func(act.OpMessage) error { mutated = true; return nil }
	m.lifecycle = func(lifecycleOp) error { mutated = true; return nil }
	m.newSession = func(newSessionOp) error { mutated = true; return nil }
	m.newTeam = func(newTeamOp) error { mutated = true; return nil }
	m.drainAgent = func(drainAgentOp) (drainAgentResult, error) {
		mutated = true
		return drainAgentResult{}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "beta presence" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palPresence {
		t.Fatalf("expected presence action selected, got %+v ok=%v", sel, ok)
	}
	m, cmd := nocPress(m, "enter")

	if m.palette != nil {
		t.Fatal("selecting presence action should close the palette")
	}
	if m.input != nil || m.pending != nil {
		t.Fatalf("presence action must not open input/confirm, input=%+v pending=%+v", m.input, m.pending)
	}
	if cmd != nil {
		t.Fatal("read-only palette presence should not request a rebuild")
	}
	if mutated {
		t.Fatal("palette presence must not call mutating seams")
	}
	if len(got) != 1 {
		t.Fatalf("palette presence should call the presence seam once, got %d", len(got))
	}
	if got[0].Root != ctlRoot {
		t.Fatalf("presence root mismatch: %+v", got[0])
	}
	if m.presenceResult == nil {
		t.Fatal("palette presence should open a result overlay")
	}
	out := m.View()
	for _, want := range []string{"PRESENCE", "qa  active", "amq presence list --root /fake/root/.agent-mail"} {
		if !strings.Contains(out, want) {
			t.Fatalf("presence overlay missing %q:\n%s", want, out)
		}
	}
}

func TestPalette_AMQCleanupActionConfirmsAndShowsResult(t *testing.T) {
	m := newControlModel(t)
	var ops []amqCleanupOp
	m.amqCleanup = func(op amqCleanupOp) (amqCleanupResult, error) {
		ops = append(ops, op)
		return amqCleanupResult{Root: op.Root, TmpOlderThan: op.TmpOlderThan, Output: "Removed 2 tmp file(s).\n"}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "beta amq cleanup" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palAMQCleanup {
		t.Fatalf("expected AMQ cleanup action selected, got %+v ok=%v", sel, ok)
	}
	m, cmd := nocPress(m, "enter")
	if m.palette != nil {
		t.Fatal("selecting AMQ cleanup action should close the palette")
	}
	if cmd != nil {
		t.Fatal("opening AMQ cleanup input should not request a rebuild")
	}
	if len(ops) != 0 {
		t.Fatal("selecting AMQ cleanup must not call the seam before confirm")
	}
	if m.input == nil || m.input.kind != ctlAMQCleanup || m.input.stage != 1 {
		t.Fatalf("AMQ cleanup action should open an age editor, input=%+v", m.input)
	}

	m = typeControlText(t, m, "0s")
	m, _ = nocPress(m, "enter")
	if m.input == nil || m.pending != nil {
		t.Fatalf("invalid cleanup age should keep the editor open without preview, input=%+v pending=%+v", m.input, m.pending)
	}
	if !strings.Contains(m.actNote, "positive duration") {
		t.Fatalf("invalid cleanup age should explain the duration rule, note=%q", m.actNote)
	}

	m.input.body = ""
	m = typeControlText(t, m, "36h")
	m, _ = nocPress(m, "enter")
	if m.pending == nil || m.pending.kind != ctlAMQCleanup || m.pending.amqClean == nil {
		t.Fatalf("AMQ cleanup action should open a confirm overlay, pending=%+v", m.pending)
	}
	if !strings.Contains(m.pending.preview, "amq cleanup --root /fake/root/.agent-mail --tmp-older-than 36h --yes") {
		t.Fatalf("AMQ cleanup preview mismatch: %q", m.pending.preview)
	}
	m, cmd = nocPress(m, "y")
	if cmd == nil {
		t.Fatal("confirmed AMQ cleanup should request a rebuild")
	}
	if len(ops) != 1 {
		t.Fatalf("confirming should call the AMQ cleanup seam once, got %d", len(ops))
	}
	if ops[0].Root != ctlRoot || ops[0].TmpOlderThan != "36h" {
		t.Fatalf("AMQ cleanup op mismatch: %+v", ops[0])
	}
	if m.amqCleanupResult == nil {
		t.Fatal("confirmed AMQ cleanup should open a result overlay")
	}
	out := m.View()
	for _, want := range []string{"AMQ CLEANUP", "Removed 2 tmp file(s).", "amq cleanup --root /fake/root/.agent-mail --tmp-older-than 36h --yes"} {
		if !strings.Contains(out, want) {
			t.Fatalf("AMQ cleanup overlay missing %q:\n%s", want, out)
		}
	}
}

func TestPalette_ThreadContextActionShowsResultWithoutMutation(t *testing.T) {
	m := newControlModel(t)
	var got []threadContextOp
	m.threadContext = func(op threadContextOp) (threadContextResult, error) {
		got = append(got, op)
		return threadContextResult{Thread: op.Thread, Subject: op.Subject, Output: "2026-06-01T09:00:00Z  qa  Ship it?\nPlease approve\n---\n"}, nil
	}
	mutated := false
	m.sendOp = func(act.OpMessage) error { mutated = true; return nil }
	m.lifecycle = func(lifecycleOp) error { mutated = true; return nil }
	m.newSession = func(newSessionOp) error { mutated = true; return nil }
	m.newTeam = func(newTeamOp) error { mutated = true; return nil }
	m.drainAgent = func(drainAgentOp) (drainAgentResult, error) {
		mutated = true
		return drainAgentResult{}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "beta qa thread context" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palThreadContext {
		t.Fatalf("expected thread-context action selected, got %+v ok=%v", sel, ok)
	}
	m, cmd := nocPress(m, "enter")

	if m.palette != nil {
		t.Fatal("selecting thread-context action should close the palette")
	}
	if m.input != nil || m.pending != nil {
		t.Fatalf("thread-context action must not open input/confirm, input=%+v pending=%+v", m.input, m.pending)
	}
	if cmd != nil {
		t.Fatal("read-only palette thread context should not request a rebuild")
	}
	if mutated {
		t.Fatal("palette thread context must not call mutating seams")
	}
	if len(got) != 1 {
		t.Fatalf("palette thread context should call the seam once, got %d", len(got))
	}
	if got[0].Root != ctlRoot || got[0].Thread != "decision/ship" {
		t.Fatalf("thread context op mismatch: %+v", got[0])
	}
	if m.threadContextResult == nil {
		t.Fatal("palette thread context should open a result overlay")
	}
	out := m.View()
	for _, want := range []string{"THREAD CONTEXT", "Please approve", "amq thread --root /fake/root/.agent-mail --id decision/ship --include-body --limit 20"} {
		if !strings.Contains(out, want) {
			t.Fatalf("thread context overlay missing %q:\n%s", want, out)
		}
	}
}

func TestPalette_ThreadsActionShowsResultWithoutMutation(t *testing.T) {
	m := newControlModel(t)
	var got []threadsOp
	m.threads = func(op threadsOp) (threadsResult, error) {
		got = append(got, op)
		return threadsResult{ProjectDir: op.ProjectDir, Session: op.Session, Output: "TRIAGE  STATUS  THREAD\nneeds-you  awaiting-reply  decision/ship\n"}, nil
	}
	mutated := false
	m.sendOp = func(act.OpMessage) error { mutated = true; return nil }
	m.lifecycle = func(lifecycleOp) error { mutated = true; return nil }
	m.newSession = func(newSessionOp) error { mutated = true; return nil }
	m.newTeam = func(newTeamOp) error { mutated = true; return nil }
	m.drainAgent = func(drainAgentOp) (drainAgentResult, error) {
		mutated = true
		return drainAgentResult{}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "beta threads" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palThreads {
		t.Fatalf("expected threads action selected, got %+v ok=%v", sel, ok)
	}
	m, cmd := nocPress(m, "enter")

	if m.palette != nil {
		t.Fatal("selecting threads action should close the palette")
	}
	if m.input != nil || m.pending != nil {
		t.Fatalf("threads action must not open input/confirm, input=%+v pending=%+v", m.input, m.pending)
	}
	if cmd != nil {
		t.Fatal("read-only palette threads should not request a rebuild")
	}
	if mutated {
		t.Fatal("palette threads must not call mutating seams")
	}
	if len(got) != 1 {
		t.Fatalf("palette threads should call the seam once, got %d", len(got))
	}
	if got[0].ProjectDir != "/fake/proj/beta" || got[0].Session != "beta" {
		t.Fatalf("threads op mismatch: %+v", got[0])
	}
	if m.threadsResult == nil {
		t.Fatal("palette threads should open a result overlay")
	}
	out := m.View()
	for _, want := range []string{"THREADS", "decision/ship", "amq-squad threads --project /fake/proj/beta --session beta --limit 20"} {
		if !strings.Contains(out, want) {
			t.Fatalf("threads overlay missing %q:\n%s", want, out)
		}
	}
}

func TestPalette_ThreadContextAnyPromptsForThreadIDAndReadsWithoutMutation(t *testing.T) {
	m := newControlModel(t)
	var got []threadContextOp
	m.threadContext = func(op threadContextOp) (threadContextResult, error) {
		got = append(got, op)
		return threadContextResult{Thread: op.Thread, Output: "2026-06-01T09:00:00Z  dev  Status\nLooks good\n---\n"}, nil
	}
	mutated := false
	m.sendOp = func(act.OpMessage) error { mutated = true; return nil }
	m.lifecycle = func(lifecycleOp) error { mutated = true; return nil }
	m.newSession = func(newSessionOp) error { mutated = true; return nil }
	m.newTeam = func(newTeamOp) error { mutated = true; return nil }
	m.drainAgent = func(drainAgentOp) (drainAgentResult, error) {
		mutated = true
		return drainAgentResult{}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "beta thread id" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palThreadContextAny {
		t.Fatalf("expected thread-context-any action selected, got %+v ok=%v", sel, ok)
	}
	m, cmd := nocPress(m, "enter")
	if cmd != nil {
		t.Fatal("opening thread-context-any input should not request a rebuild")
	}
	if m.palette != nil {
		t.Fatal("selecting thread-context-any should close the palette")
	}
	if m.input == nil || m.input.kind != ctlThreadContextAny {
		t.Fatalf("thread-context-any should open a thread id input, got %+v", m.input)
	}
	if len(got) != 0 {
		t.Fatalf("thread-context-any should not call seam before enter, got %d", len(got))
	}
	for _, ch := range "decision/ship" {
		m, _ = nocPress(m, string(ch))
	}
	m, cmd = nocPress(m, "enter")
	if cmd != nil {
		t.Fatal("read-only thread-context-any should not request a rebuild")
	}
	if m.input != nil || m.pending != nil {
		t.Fatalf("thread-context-any should not open confirm, input=%+v pending=%+v", m.input, m.pending)
	}
	if mutated {
		t.Fatal("thread-context-any must not call mutating seams")
	}
	if len(got) != 1 {
		t.Fatalf("thread-context-any should call thread seam once, got %d", len(got))
	}
	if got[0].Root != ctlRoot || got[0].Thread != "decision/ship" {
		t.Fatalf("thread-context-any op mismatch: %+v", got[0])
	}
	out := m.View()
	for _, want := range []string{"THREAD CONTEXT", "Looks good", "amq thread --root /fake/root/.agent-mail --id decision/ship --include-body --limit 20"} {
		if !strings.Contains(out, want) {
			t.Fatalf("thread-context-any overlay missing %q:\n%s", want, out)
		}
	}
}

func TestPalette_InboxActionShowsResultWithoutMutation(t *testing.T) {
	m := newControlModel(t)
	var got []inboxAgentOp
	m.inboxAgent = func(op inboxAgentOp) (inboxAgentResult, error) {
		got = append(got, op)
		return inboxAgentResult{Handle: op.Handle, Output: "2026-06-01T09:00:00Z  normal  dev  msg-1  Review needed\n"}, nil
	}
	mutated := false
	m.sendOp = func(act.OpMessage) error { mutated = true; return nil }
	m.lifecycle = func(lifecycleOp) error { mutated = true; return nil }
	m.newSession = func(newSessionOp) error { mutated = true; return nil }
	m.newTeam = func(newTeamOp) error { mutated = true; return nil }
	m.drainAgent = func(drainAgentOp) (drainAgentResult, error) {
		mutated = true
		return drainAgentResult{}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "beta qa inbox" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palInbox {
		t.Fatalf("expected inbox action selected, got %+v ok=%v", sel, ok)
	}
	m, cmd := nocPress(m, "enter")

	if m.palette != nil {
		t.Fatal("selecting inbox action should close the palette")
	}
	if m.input != nil || m.pending != nil {
		t.Fatalf("inbox action must not open input/confirm, input=%+v pending=%+v", m.input, m.pending)
	}
	if cmd != nil {
		t.Fatal("read-only palette inbox should not request a rebuild")
	}
	if mutated {
		t.Fatal("palette inbox must not call mutating seams")
	}
	if len(got) != 1 {
		t.Fatalf("palette inbox should call the inbox seam once, got %d", len(got))
	}
	if got[0].Root != ctlRoot || got[0].Handle != "qa" {
		t.Fatalf("inbox op mismatch: %+v", got[0])
	}
	if m.inboxResult == nil {
		t.Fatal("palette inbox should open a result overlay")
	}
	out := m.View()
	for _, want := range []string{"INBOX", "Review needed", "amq list --root /fake/root/.agent-mail --me qa --new"} {
		if !strings.Contains(out, want) {
			t.Fatalf("inbox overlay missing %q:\n%s", want, out)
		}
	}
}

func TestPalette_DLQActionShowsResultWithoutMutation(t *testing.T) {
	m := newControlModel(t)
	var got []dlqAgentOp
	m.dlqAgent = func(op dlqAgentOp) (dlqAgentResult, error) {
		got = append(got, op)
		return dlqAgentResult{Handle: op.Handle, Output: "dlq-1  corrupt_header  msg-1  new\n"}, nil
	}
	mutated := false
	m.sendOp = func(act.OpMessage) error { mutated = true; return nil }
	m.lifecycle = func(lifecycleOp) error { mutated = true; return nil }
	m.newSession = func(newSessionOp) error { mutated = true; return nil }
	m.newTeam = func(newTeamOp) error { mutated = true; return nil }
	m.drainAgent = func(drainAgentOp) (drainAgentResult, error) {
		mutated = true
		return drainAgentResult{}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "beta qa dlq" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palDLQ {
		t.Fatalf("expected DLQ action selected, got %+v ok=%v", sel, ok)
	}
	m, cmd := nocPress(m, "enter")

	if m.palette != nil {
		t.Fatal("selecting DLQ action should close the palette")
	}
	if m.input != nil || m.pending != nil {
		t.Fatalf("DLQ action must not open input/confirm, input=%+v pending=%+v", m.input, m.pending)
	}
	if cmd != nil {
		t.Fatal("read-only palette DLQ should not request a rebuild")
	}
	if mutated {
		t.Fatal("palette DLQ must not call mutating seams")
	}
	if len(got) != 1 {
		t.Fatalf("palette DLQ should call the seam once, got %d", len(got))
	}
	if got[0].Root != ctlRoot || got[0].Handle != "qa" {
		t.Fatalf("DLQ op mismatch: %+v", got[0])
	}
	if m.dlqResult == nil {
		t.Fatal("palette DLQ should open a result overlay")
	}
	out := m.View()
	for _, want := range []string{"DLQ", "corrupt_header", "amq dlq list --root /fake/root/.agent-mail --me qa"} {
		if !strings.Contains(out, want) {
			t.Fatalf("DLQ overlay missing %q:\n%s", want, out)
		}
	}
}

func TestPalette_DLQReadActionOpensPreviewGatedFlow(t *testing.T) {
	m := newControlModel(t)
	var ops []dlqReadOp
	m.dlqRead = func(op dlqReadOp) (dlqReadResult, error) {
		ops = append(ops, op)
		return dlqReadResult{
			Handle: op.Handle,
			ID:     op.ID,
			Output: "DLQ ID: dlq_123\nFailure Reason: corrupt_header\n",
		}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "beta qa read dlq" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palDLQRead {
		t.Fatalf("expected DLQ read action selected, got %+v ok=%v", sel, ok)
	}
	m, _ = nocPress(m, "enter")
	if m.palette != nil {
		t.Fatal("selecting DLQ read action should close the palette")
	}
	if len(ops) != 0 {
		t.Fatal("selecting DLQ read must not call the seam before confirm")
	}
	if m.input == nil || m.input.kind != ctlDLQRead || m.input.stage != 1 {
		t.Fatalf("DLQ read action should open an ID editor, input=%+v", m.input)
	}
	m = typeControlText(t, m, "../x")
	m, _ = nocPress(m, "enter")
	if m.input == nil || m.pending != nil {
		t.Fatalf("invalid DLQ ID should keep the editor open without preview, input=%+v pending=%+v", m.input, m.pending)
	}
	if !strings.Contains(m.actNote, "not a path") {
		t.Fatalf("invalid DLQ ID should explain the path rejection, note=%q", m.actNote)
	}
	m.input.body = ""
	m = typeControlText(t, m, "dlq_123")
	m, _ = nocPress(m, "enter")
	if m.input != nil {
		t.Fatal("valid DLQ ID should close the editor")
	}
	if m.pending == nil || m.pending.kind != ctlDLQRead || m.pending.dlqRead == nil {
		t.Fatalf("DLQ read action should open a confirm overlay, pending=%+v", m.pending)
	}
	if !strings.Contains(m.pending.preview, "amq dlq read --root /fake/root/.agent-mail --me qa --id dlq_123") {
		t.Fatalf("DLQ read preview mismatch: %q", m.pending.preview)
	}
	m, cmd := nocPress(m, "y")
	if cmd == nil {
		t.Fatal("confirmed DLQ read should request a rebuild")
	}
	if len(ops) != 1 {
		t.Fatalf("confirming should call the DLQ read seam once, got %d", len(ops))
	}
	if ops[0].Root != ctlRoot || ops[0].Handle != "qa" || ops[0].ID != "dlq_123" {
		t.Fatalf("DLQ read op mismatch: %+v", ops[0])
	}
	if m.dlqReadResult == nil {
		t.Fatal("DLQ read should open a result overlay")
	}
	out := m.View()
	for _, want := range []string{"DLQ READ RESULT", "Failure Reason: corrupt_header", "amq dlq read --root /fake/root/.agent-mail --me qa --id dlq_123"} {
		if !strings.Contains(out, want) {
			t.Fatalf("DLQ read overlay missing %q:\n%s", want, out)
		}
	}
	m, _ = nocPress(m, "enter")
	if m.dlqReadResult != nil {
		t.Fatal("enter should close the DLQ read result overlay")
	}
}

func TestPalette_DLQRetryActionOpensPreviewGatedFlow(t *testing.T) {
	m := newControlModel(t)
	var ops []dlqRetryOp
	m.dlqRetry = func(op dlqRetryOp) (dlqRetryResult, error) {
		ops = append(ops, op)
		return dlqRetryResult{
			Handle: op.Handle,
			ID:     op.ID,
			Output: "Retried 1 message(s).\n",
		}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "beta qa retry dlq" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palDLQRetry {
		t.Fatalf("expected DLQ retry action selected, got %+v ok=%v", sel, ok)
	}
	m, _ = nocPress(m, "enter")
	if m.palette != nil {
		t.Fatal("selecting DLQ retry action should close the palette")
	}
	if len(ops) != 0 {
		t.Fatal("selecting DLQ retry must not call the seam before confirm")
	}
	if m.input == nil || m.input.kind != ctlDLQRetry || m.input.stage != 1 {
		t.Fatalf("DLQ retry action should open an ID editor, input=%+v", m.input)
	}
	m = typeControlText(t, m, "dlq_123")
	m, _ = nocPress(m, "enter")
	if m.pending == nil || m.pending.kind != ctlDLQRetry || m.pending.dlqRetry == nil {
		t.Fatalf("DLQ retry action should open a confirm overlay, pending=%+v", m.pending)
	}
	if !strings.Contains(m.pending.preview, "amq dlq retry --root /fake/root/.agent-mail --me qa --id dlq_123") {
		t.Fatalf("DLQ retry preview mismatch: %q", m.pending.preview)
	}
	m, cmd := nocPress(m, "y")
	if cmd == nil {
		t.Fatal("confirmed DLQ retry should request a rebuild")
	}
	if len(ops) != 1 {
		t.Fatalf("confirming should call the DLQ retry seam once, got %d", len(ops))
	}
	if ops[0].Root != ctlRoot || ops[0].Handle != "qa" || ops[0].ID != "dlq_123" {
		t.Fatalf("DLQ retry op mismatch: %+v", ops[0])
	}
	if m.dlqRetryResult == nil {
		t.Fatal("DLQ retry should open a result overlay")
	}
	out := m.View()
	for _, want := range []string{"DLQ RETRY RESULT", "Retried 1 message(s).", "amq dlq retry --root /fake/root/.agent-mail --me qa --id dlq_123"} {
		if !strings.Contains(out, want) {
			t.Fatalf("DLQ retry overlay missing %q:\n%s", want, out)
		}
	}
	m, _ = nocPress(m, "enter")
	if m.dlqRetryResult != nil {
		t.Fatal("enter should close the DLQ retry result overlay")
	}
}

func TestPalette_DLQPurgeActionOpensPreviewGatedFlow(t *testing.T) {
	m := newControlModel(t)
	var ops []dlqPurgeOp
	m.dlqPurge = func(op dlqPurgeOp) (dlqPurgeResult, error) {
		ops = append(ops, op)
		return dlqPurgeResult{
			Handle:    op.Handle,
			OlderThan: op.OlderThan,
			Output:    "Purged 2 message(s).\n",
		}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "beta qa purge dlq" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palDLQPurge {
		t.Fatalf("expected DLQ purge action selected, got %+v ok=%v", sel, ok)
	}
	m, _ = nocPress(m, "enter")
	if m.palette != nil {
		t.Fatal("selecting DLQ purge action should close the palette")
	}
	if len(ops) != 0 {
		t.Fatal("selecting DLQ purge must not call the seam before confirm")
	}
	if m.input == nil || m.input.kind != ctlDLQPurge || m.input.stage != 1 {
		t.Fatalf("DLQ purge action should open an age editor, input=%+v", m.input)
	}
	m = typeControlText(t, m, "0s")
	m, _ = nocPress(m, "enter")
	if m.input == nil || m.pending != nil {
		t.Fatalf("invalid purge age should keep the editor open without preview, input=%+v pending=%+v", m.input, m.pending)
	}
	if !strings.Contains(m.actNote, "positive duration") {
		t.Fatalf("invalid purge age should explain the duration rejection, note=%q", m.actNote)
	}
	m.input.body = ""
	m = typeControlText(t, m, "168h")
	m, _ = nocPress(m, "enter")
	if m.pending == nil || m.pending.kind != ctlDLQPurge || m.pending.dlqPurge == nil {
		t.Fatalf("DLQ purge action should open a confirm overlay, pending=%+v", m.pending)
	}
	if !strings.Contains(m.pending.preview, "amq dlq purge --root /fake/root/.agent-mail --me qa --older-than 168h --yes") {
		t.Fatalf("DLQ purge preview mismatch: %q", m.pending.preview)
	}
	m, cmd := nocPress(m, "y")
	if cmd == nil {
		t.Fatal("confirmed DLQ purge should request a rebuild")
	}
	if len(ops) != 1 {
		t.Fatalf("confirming should call the DLQ purge seam once, got %d", len(ops))
	}
	if ops[0].Root != ctlRoot || ops[0].Handle != "qa" || ops[0].OlderThan != "168h" {
		t.Fatalf("DLQ purge op mismatch: %+v", ops[0])
	}
	if m.dlqPurgeResult == nil {
		t.Fatal("DLQ purge should open a result overlay")
	}
	out := m.View()
	for _, want := range []string{"DLQ PURGE RESULT", "Purged 2 message(s).", "amq dlq purge --root /fake/root/.agent-mail --me qa --older-than 168h --yes"} {
		if !strings.Contains(out, want) {
			t.Fatalf("DLQ purge overlay missing %q:\n%s", want, out)
		}
	}
	m, _ = nocPress(m, "enter")
	if m.dlqPurgeResult != nil {
		t.Fatal("enter should close the DLQ purge result overlay")
	}
}

func TestPalette_DLQRetryAllActionOpensPreviewGatedFlow(t *testing.T) {
	m := newControlModel(t)
	var ops []dlqRetryAllOp
	m.dlqRetryAll = func(op dlqRetryAllOp) (dlqRetryAllResult, error) {
		ops = append(ops, op)
		return dlqRetryAllResult{Handle: op.Handle, Output: "Retried 2 message(s).\n"}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "beta qa retry all dlq" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palDLQRetryAll {
		t.Fatalf("expected DLQ retry-all action selected, got %+v ok=%v", sel, ok)
	}
	m, _ = nocPress(m, "enter")
	if m.palette != nil {
		t.Fatal("selecting DLQ retry-all action should close the palette")
	}
	if len(ops) != 0 {
		t.Fatal("selecting DLQ retry-all must not call the seam before confirm")
	}
	if m.pending == nil || m.pending.kind != ctlDLQRetryAll || m.pending.dlqAll == nil {
		t.Fatalf("DLQ retry-all action should open a confirm overlay, pending=%+v", m.pending)
	}
	if !strings.Contains(m.pending.preview, "amq dlq retry --root /fake/root/.agent-mail --me qa --all") {
		t.Fatalf("DLQ retry-all preview mismatch: %q", m.pending.preview)
	}
	m, cmd := nocPress(m, "y")
	if cmd == nil {
		t.Fatal("confirmed DLQ retry-all should request a rebuild")
	}
	if len(ops) != 1 {
		t.Fatalf("confirming should call the DLQ retry-all seam once, got %d", len(ops))
	}
	if ops[0].Root != ctlRoot || ops[0].Handle != "qa" {
		t.Fatalf("DLQ retry-all op mismatch: %+v", ops[0])
	}
	if m.dlqRetryAllResult == nil {
		t.Fatal("DLQ retry-all should open a result overlay")
	}
	out := m.View()
	for _, want := range []string{"DLQ RETRY ALL RESULT", "Retried 2 message(s).", "amq dlq retry --root /fake/root/.agent-mail --me qa --all"} {
		if !strings.Contains(out, want) {
			t.Fatalf("DLQ retry-all overlay missing %q:\n%s", want, out)
		}
	}
}

func TestPalette_ReceiptsActionShowsResultWithoutMutation(t *testing.T) {
	m := newControlModel(t)
	var got []receiptsAgentOp
	m.receiptsAgent = func(op receiptsAgentOp) (receiptsAgentResult, error) {
		got = append(got, op)
		return receiptsAgentResult{Handle: op.Handle, Output: "msg-1  drained  dev  2026-06-01T09:00:00Z\n"}, nil
	}
	mutated := false
	m.sendOp = func(act.OpMessage) error { mutated = true; return nil }
	m.lifecycle = func(lifecycleOp) error { mutated = true; return nil }
	m.newSession = func(newSessionOp) error { mutated = true; return nil }
	m.newTeam = func(newTeamOp) error { mutated = true; return nil }
	m.drainAgent = func(drainAgentOp) (drainAgentResult, error) {
		mutated = true
		return drainAgentResult{}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "beta qa receipts" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palReceipts {
		t.Fatalf("expected receipts action selected, got %+v ok=%v", sel, ok)
	}
	m, cmd := nocPress(m, "enter")

	if m.palette != nil {
		t.Fatal("selecting receipts action should close the palette")
	}
	if m.input != nil || m.pending != nil {
		t.Fatalf("receipts action must not open input/confirm, input=%+v pending=%+v", m.input, m.pending)
	}
	if cmd != nil {
		t.Fatal("read-only palette receipts should not request a rebuild")
	}
	if mutated {
		t.Fatal("palette receipts must not call mutating seams")
	}
	if len(got) != 1 {
		t.Fatalf("palette receipts should call the seam once, got %d", len(got))
	}
	if got[0].Root != ctlRoot || got[0].Handle != "qa" {
		t.Fatalf("receipts op mismatch: %+v", got[0])
	}
	if m.receiptsResult == nil {
		t.Fatal("palette receipts should open a result overlay")
	}
	out := m.View()
	for _, want := range []string{"RECEIPTS", "drained", "amq receipts list --root /fake/root/.agent-mail --me qa"} {
		if !strings.Contains(out, want) {
			t.Fatalf("receipts overlay missing %q:\n%s", want, out)
		}
	}
}

func TestPalette_ReceiptsWaitActionRunsReadOnlyAfterInput(t *testing.T) {
	m := newControlModel(t)
	var ops []receiptsWaitOp
	m.receiptsWait = func(op receiptsWaitOp) (receiptsWaitResult, error) {
		ops = append(ops, op)
		return receiptsWaitResult{
			Handle:  op.Handle,
			MsgID:   op.MsgID,
			Stage:   op.Stage,
			Timeout: op.Timeout,
			Output:  "Receipt: drained msg_123 by qa\n",
		}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "beta qa wait receipts" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palReceiptsWait {
		t.Fatalf("expected receipts-wait action selected, got %+v ok=%v", sel, ok)
	}
	m, _ = nocPress(m, "enter")
	if m.palette != nil {
		t.Fatal("selecting receipts-wait action should close the palette")
	}
	if len(ops) != 0 {
		t.Fatal("selecting receipts-wait must not call the seam before input")
	}
	if m.input == nil || m.input.kind != ctlReceiptsWait || m.input.stage != 1 {
		t.Fatalf("receipts-wait action should open an input editor, input=%+v", m.input)
	}
	m = typeControlText(t, m, "msg_123 stage=sent timeout=60s")
	m, _ = nocPress(m, "enter")
	if m.input == nil || m.pending != nil {
		t.Fatalf("invalid receipt stage should keep the editor open without preview, input=%+v pending=%+v", m.input, m.pending)
	}
	if !strings.Contains(m.actNote, "drained or dlq") {
		t.Fatalf("invalid receipt stage should explain the stage choices, note=%q", m.actNote)
	}
	m.input.body = ""
	m = typeControlText(t, m, "msg_123 stage=drained timeout=60s")
	m, cmd := nocPress(m, "enter")
	if cmd != nil {
		t.Fatal("read-only receipts-wait should not request a rebuild")
	}
	if m.input != nil || m.pending != nil {
		t.Fatalf("read-only receipts-wait should not leave input or open confirm, input=%+v pending=%+v", m.input, m.pending)
	}
	if len(ops) != 1 {
		t.Fatalf("valid receipts-wait input should call the seam once, got %d", len(ops))
	}
	if ops[0].Root != ctlRoot || ops[0].Handle != "qa" || ops[0].MsgID != "msg_123" || ops[0].Stage != "drained" || ops[0].Timeout != "60s" {
		t.Fatalf("receipts-wait op mismatch: %+v", ops[0])
	}
	if m.receiptsWaitResult == nil {
		t.Fatal("receipts-wait should open a result overlay")
	}
	out := m.View()
	for _, want := range []string{"RECEIPTS WAIT RESULT", "Receipt: drained msg_123 by qa", "amq receipts wait --root /fake/root/.agent-mail --me qa --msg-id msg_123 --stage drained --timeout 60s"} {
		if !strings.Contains(out, want) {
			t.Fatalf("receipts-wait overlay missing %q:\n%s", want, out)
		}
	}
	m, _ = nocPress(m, "enter")
	if m.receiptsWaitResult != nil {
		t.Fatal("enter should close the receipts-wait result overlay")
	}
}

func TestPalette_MessageActionOpensConfirmWithoutMutation(t *testing.T) {
	m := newControlModel(t)
	var sent []act.OpMessage
	m.sendOp = func(op act.OpMessage) error {
		sent = append(sent, op)
		return nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "beta qa message" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palMessage {
		t.Fatalf("expected message action selected, got %+v ok=%v", sel, ok)
	}
	m, _ = nocPress(m, "enter")
	if m.palette != nil {
		t.Fatal("selecting message action should close the palette")
	}
	if len(sent) != 0 {
		t.Fatal("selecting message must not call the send seam before confirm")
	}
	if m.input == nil || m.input.kind != ctlMessage || m.input.stage != 1 {
		t.Fatalf("message action should open a body editor, input=%+v", m.input)
	}
	m = typeControlText(t, m, "Please check logs")
	m, _ = nocPress(m, "enter")
	if m.pending == nil || m.pending.kind != ctlMessage {
		t.Fatalf("message action should open a confirm overlay, pending=%+v", m.pending)
	}
	if !strings.Contains(m.pending.preview, "amq send --root /fake/root/.agent-mail --me user --to qa --subject 'Message from operator' --body 'Please check logs' --kind status") {
		t.Fatalf("message preview mismatch: %q", m.pending.preview)
	}
	m, _ = nocPress(m, "y")
	if len(sent) != 1 {
		t.Fatalf("confirming should call the send seam once, got %d", len(sent))
	}
	if sent[0].Root != ctlRoot || sent[0].To != "qa" || sent[0].Body != "Please check logs" {
		t.Fatalf("message op mismatch: %+v", sent[0])
	}
}

func TestPalette_MessageWaitActionOpensPreviewGatedFlow(t *testing.T) {
	m := newControlModel(t)
	var ops []messageWaitOp
	m.messageWait = func(op messageWaitOp) (messageWaitResult, error) {
		ops = append(ops, op)
		return messageWaitResult{
			Handle:  op.Handle,
			Timeout: op.Timeout,
			Output:  "Receipt: drained msg_123 by qa\n",
		}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "beta qa wait message" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palMessageWait {
		t.Fatalf("expected message-wait action selected, got %+v ok=%v", sel, ok)
	}
	m, _ = nocPress(m, "enter")
	if m.palette != nil {
		t.Fatal("selecting message-wait action should close the palette")
	}
	if len(ops) != 0 {
		t.Fatal("selecting message-wait must not call the seam before confirm")
	}
	if m.input == nil || m.input.kind != ctlMessageWait || m.input.stage != 0 {
		t.Fatalf("message-wait action should open a timeout editor, input=%+v", m.input)
	}
	m = typeControlText(t, m, "-1s")
	m, _ = nocPress(m, "enter")
	if m.input == nil || m.pending != nil {
		t.Fatalf("invalid timeout should keep the editor open without preview, input=%+v pending=%+v", m.input, m.pending)
	}
	if !strings.Contains(m.actNote, "non-negative duration") {
		t.Fatalf("invalid timeout should explain the duration rejection, note=%q", m.actNote)
	}
	m.input.subject = ""
	m = typeControlText(t, m, "60s")
	m, _ = nocPress(m, "enter")
	if m.input == nil || m.input.stage != 1 {
		t.Fatalf("valid timeout should advance to body editor, input=%+v", m.input)
	}
	m = typeControlText(t, m, "Please check logs")
	m, _ = nocPress(m, "enter")
	if m.pending == nil || m.pending.kind != ctlMessageWait || m.pending.msgWait == nil {
		t.Fatalf("message-wait action should open a confirm overlay, pending=%+v", m.pending)
	}
	if !strings.Contains(m.pending.preview, "amq send --root /fake/root/.agent-mail --me user --to qa --subject 'Message from operator' --body 'Please check logs' --kind status --wait-for drained --wait-timeout 60s") {
		t.Fatalf("message-wait preview mismatch: %q", m.pending.preview)
	}
	m, cmd := nocPress(m, "y")
	if cmd == nil {
		t.Fatal("confirmed message-wait should request a rebuild")
	}
	if len(ops) != 1 {
		t.Fatalf("confirming should call the message-wait seam once, got %d", len(ops))
	}
	if ops[0].Root != ctlRoot || ops[0].Handle != "qa" || ops[0].Body != "Please check logs" || ops[0].Timeout != "60s" {
		t.Fatalf("message-wait op mismatch: %+v", ops[0])
	}
	if m.messageWaitResult == nil {
		t.Fatal("message-wait should open a result overlay")
	}
	out := m.View()
	for _, want := range []string{"MESSAGE WAIT RESULT", "Receipt: drained msg_123 by qa", "amq send --root /fake/root/.agent-mail --me user --to qa --subject 'Message from operator' --body 'Please check logs' --kind status --wait-for drained --wait-timeout 60s"} {
		if !strings.Contains(out, want) {
			t.Fatalf("message-wait overlay missing %q:\n%s", want, out)
		}
	}
	m, _ = nocPress(m, "enter")
	if m.messageWaitResult != nil {
		t.Fatal("enter should close the message-wait result overlay")
	}
}

func TestPalette_DrainActionOpensConfirmWithoutMutation(t *testing.T) {
	m := newControlModel(t)
	called := false
	m.drainAgent = func(drainAgentOp) (drainAgentResult, error) {
		called = true
		return drainAgentResult{}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "beta qa drain" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palDrain {
		t.Fatalf("expected drain action selected, got %+v ok=%v", sel, ok)
	}
	m, _ = nocPress(m, "enter")

	if m.palette != nil {
		t.Fatal("selecting drain action should close the palette")
	}
	if m.pending == nil || m.pending.kind != ctlDrain || m.pending.drain == nil {
		t.Fatalf("drain action should open a confirm overlay, pending=%+v", m.pending)
	}
	if called {
		t.Fatal("palette drain must not call the drain seam before confirm")
	}
	if !strings.Contains(m.pending.preview, "amq drain --root /fake/root/.agent-mail --me qa --include-body") {
		t.Fatalf("drain preview mismatch: %q", m.pending.preview)
	}
}

func TestPalette_AgentResumeActionOpensPreviewGatedFlow(t *testing.T) {
	m := newControlModel(t)
	var ops []agentResumeOp
	m.agentResume = func(op agentResumeOp) error {
		ops = append(ops, op)
		return nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "beta qa resume agent" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palAgentResume {
		t.Fatalf("expected agent-resume action selected, got %+v ok=%v", sel, ok)
	}
	m, _ = nocPress(m, "enter")
	if m.palette != nil {
		t.Fatal("selecting agent-resume action should close the palette")
	}
	if len(ops) != 0 {
		t.Fatal("selecting agent-resume must not call the seam before confirm")
	}
	if m.pending == nil || m.pending.kind != ctlAgentResume || m.pending.agent == nil {
		t.Fatalf("agent-resume action should open a confirm overlay, pending=%+v", m.pending)
	}
	if !strings.Contains(m.pending.preview, "amq-squad agent resume qa --project /fake/proj/beta --session beta") {
		t.Fatalf("agent-resume preview mismatch: %q", m.pending.preview)
	}
	m, _ = nocPress(m, "y")
	if len(ops) != 1 {
		t.Fatalf("confirming should call the agent-resume seam once, got %d", len(ops))
	}
	if ops[0].ProjectDir != "/fake/proj/beta" || ops[0].Role != "qa" || ops[0].Session != "beta" {
		t.Fatalf("agent-resume op mismatch: %+v", ops[0])
	}
}

func TestPalette_SessionCleanupActionsOpenPreviewGatedFlow(t *testing.T) {
	for _, tc := range []struct {
		query   string
		action  paletteAction
		kind    controlKind
		preview string
		archive bool
	}{
		{
			query:   "beta archive session",
			action:  palArchive,
			kind:    ctlArchive,
			preview: "amq-squad archive --project /fake/proj/beta --yes beta",
			archive: true,
		},
		{
			query:   "beta remove session",
			action:  palRemove,
			kind:    ctlRemove,
			preview: "amq-squad rm --project /fake/proj/beta --yes beta",
		},
	} {
		t.Run(tc.query, func(t *testing.T) {
			m := newControlModel(t)
			var ops []sessionCleanupOp
			m.sessionCleanup = func(op sessionCleanupOp) error {
				ops = append(ops, op)
				return nil
			}

			m, _ = nocPress(m, "p")
			for _, ch := range tc.query {
				m, _ = nocPress(m, string(ch))
			}
			sel, ok := m.palette.selected()
			if !ok || sel.kind != palAction || sel.action != tc.action {
				t.Fatalf("expected cleanup action selected, got %+v ok=%v", sel, ok)
			}
			m, _ = nocPress(m, "enter")
			if m.palette != nil {
				t.Fatal("selecting cleanup action should close the palette")
			}
			if len(ops) != 0 {
				t.Fatal("selecting cleanup must not call the seam before confirm")
			}
			if m.pending == nil || m.pending.kind != tc.kind || m.pending.cleanup == nil {
				t.Fatalf("cleanup action should open a confirm overlay, pending=%+v", m.pending)
			}
			if m.pending.preview != tc.preview {
				t.Fatalf("cleanup preview = %q, want %q", m.pending.preview, tc.preview)
			}
			m, _ = nocPress(m, "y")
			if len(ops) != 1 {
				t.Fatalf("confirming should call the cleanup seam once, got %d", len(ops))
			}
			if ops[0].ProjectDir != "/fake/proj/beta" || ops[0].Session != "beta" || ops[0].Archive != tc.archive {
				t.Fatalf("cleanup op mismatch: %+v", ops[0])
			}
		})
	}
}

func TestPalette_RolesActionShowsMarketWithoutMutation(t *testing.T) {
	m := newControlModel(t)
	addCandidateProject(m, "delta", "/fake/proj/delta")
	var newTeams []newTeamOp
	m.newTeam = func(req newTeamOp) error {
		newTeams = append(newTeams, req)
		return nil
	}
	var newSessions []newSessionOp
	m.newSession = func(req newSessionOp) error {
		newSessions = append(newSessions, req)
		return nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "delta role market" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palRoles {
		t.Fatalf("expected roles action selected, got %+v ok=%v", sel, ok)
	}
	m, _ = nocPress(m, "enter")

	if m.palette != nil {
		t.Fatal("selecting roles action should close the palette")
	}
	if m.input != nil || m.pending != nil {
		t.Fatalf("roles action must not open mutating input/confirm, input=%+v pending=%+v", m.input, m.pending)
	}
	if m.roleMarket == nil {
		t.Fatal("roles action should open the role market overlay")
	}
	if len(newTeams) != 0 || len(newSessions) != 0 {
		t.Fatalf("roles action must not call creation seams, teams=%d sessions=%d", len(newTeams), len(newSessions))
	}
	if !strings.Contains(m.actNote, "amq-squad roles") {
		t.Fatalf("roles action should note the role market command, note=%q", m.actNote)
	}
	out := m.View()
	for _, want := range []string{"ROLE MARKET", "team-home: /fake/proj/delta", "NUM", "cto", "qa", "role=binary"} {
		if !strings.Contains(out, want) {
			t.Fatalf("role market overlay missing %q:\n%s", want, out)
		}
	}
	m, _ = nocPress(m, "enter")
	if m.roleMarket != nil {
		t.Fatal("enter should close the role market overlay")
	}
}

func TestPalette_TeamProfilesActionShowsProfilesWithoutMutation(t *testing.T) {
	m := newControlModel(t)
	addNamedConfiguredProject(m, "many-profiles", "/fake/proj/many-profiles", "review", "release")
	var newTeams []newTeamOp
	m.newTeam = func(req newTeamOp) error {
		newTeams = append(newTeams, req)
		return nil
	}
	var newSessions []newSessionOp
	m.newSession = func(req newSessionOp) error {
		newSessions = append(newSessions, req)
		return nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "many profiles team profiles" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palTeamProfiles {
		t.Fatalf("expected team-profiles action selected, got %+v ok=%v", sel, ok)
	}
	m, _ = nocPress(m, "enter")

	if m.palette != nil {
		t.Fatal("selecting team-profiles action should close the palette")
	}
	if m.input != nil || m.pending != nil {
		t.Fatalf("team-profiles action must not open mutating input/confirm, input=%+v pending=%+v", m.input, m.pending)
	}
	if m.teamProfiles == nil {
		t.Fatal("team-profiles action should open the team profiles overlay")
	}
	if len(newTeams) != 0 || len(newSessions) != 0 {
		t.Fatalf("team-profiles action must not call creation seams, teams=%d sessions=%d", len(newTeams), len(newSessions))
	}
	out := m.View()
	for _, want := range []string{
		"TEAM PROFILES",
		"team-home: /fake/proj/many-profiles",
		"ran: amq-squad team profiles --project /fake/proj/many-profiles",
		"review",
		".amq-squad/teams/review.json",
		"release",
		"press N to start a session",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("team profiles overlay missing %q:\n%s", want, out)
		}
	}
	m, _ = nocPress(m, "enter")
	if m.teamProfiles != nil {
		t.Fatal("enter should close the team profiles overlay")
	}
}

func TestPalette_ReadOnlyProjectActionPreservesVisibleProjectFilter(t *testing.T) {
	m := newControlModel(t)
	addNamedConfiguredProject(m, "many-profiles", "/fake/proj/many-profiles", "review", "release")
	m.filter = "project:many-profiles"
	m.clampCursor()

	m, _ = nocPress(m, "p")
	for _, ch := range "many profiles team profiles" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palTeamProfiles {
		t.Fatalf("expected team-profiles action selected, got %+v ok=%v", sel, ok)
	}
	m, _ = nocPress(m, "enter")

	if m.filter != "project:many-profiles" {
		t.Fatalf("palette action should preserve visible project filter, got %q", m.filter)
	}
	for _, n := range m.nodes() {
		if n.project.Project != "many-profiles" {
			t.Fatalf("filtered tree leaked project %q after palette action", n.project.Project)
		}
	}
}

func TestPalette_TeamRulesActionShowsResultWithoutMutation(t *testing.T) {
	m := newControlModel(t)
	addNamedConfiguredProject(m, "many-profiles", "/fake/proj/many-profiles", "review", "release")
	var calls []teamRulesOp
	m.teamRules = func(op teamRulesOp) (teamRulesResult, error) {
		calls = append(calls, op)
		return teamRulesResult{
			ProjectDir: op.ProjectDir,
			Path:       op.ProjectDir + "/.amq-squad/team-rules.md",
			Content:    "# Team Rules\n\n- stay focused\n",
		}, nil
	}
	var newTeams []newTeamOp
	m.newTeam = func(req newTeamOp) error {
		newTeams = append(newTeams, req)
		return nil
	}
	var newSessions []newSessionOp
	m.newSession = func(req newSessionOp) error {
		newSessions = append(newSessions, req)
		return nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "many rules source truth" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palTeamRules {
		t.Fatalf("expected team-rules action selected, got %+v ok=%v", sel, ok)
	}
	m, _ = nocPress(m, "enter")

	if m.palette != nil {
		t.Fatal("selecting team-rules action should close the palette")
	}
	if m.input != nil || m.pending != nil {
		t.Fatalf("team-rules action must not open mutating input/confirm, input=%+v pending=%+v", m.input, m.pending)
	}
	if m.teamRulesResult == nil {
		t.Fatal("team-rules action should open the team rules overlay")
	}
	if len(calls) != 1 || calls[0].ProjectDir != "/fake/proj/many-profiles" {
		t.Fatalf("team-rules seam calls = %+v", calls)
	}
	if len(newTeams) != 0 || len(newSessions) != 0 {
		t.Fatalf("team-rules action must not call creation seams, teams=%d sessions=%d", len(newTeams), len(newSessions))
	}
	out := m.View()
	for _, want := range []string{
		"TEAM RULES",
		"project: /fake/proj/many-profiles",
		"path: /fake/proj/many-profiles/.amq-squad/team-rules.md",
		"ran: amq-squad team rules show --project /fake/proj/many-profiles",
		"# Team Rules",
		"- stay focused",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("team rules overlay missing %q:\n%s", want, out)
		}
	}
	m, _ = nocPress(m, "enter")
	if m.teamRulesResult != nil {
		t.Fatal("enter should close the team rules overlay")
	}
}

func TestPalette_ProjectRowSelectsEmptyCandidate(t *testing.T) {
	m := newControlModel(t)
	addCandidateProject(m, "delta", "/fake/proj/delta")
	m.hideStale = true

	m, _ = nocPress(m, "p")
	for _, ch := range "deltaproject" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palProject || sel.label != "delta/project" {
		t.Fatalf("expected the delta project row selected, got %+v ok=%v", sel, ok)
	}
	m, _ = nocPress(m, "enter")

	n, ok := m.selectedNode()
	if !ok || n.kind != nodeProject || n.label != "delta" {
		t.Fatalf("project row select should move the tree cursor to delta, got %+v ok=%v", n, ok)
	}
	if m.hideStale {
		t.Fatal("selecting a hidden project should unhide stale/candidate rows")
	}
	if !strings.Contains(m.actNote, "press T") {
		t.Fatalf("candidate project select should guide to T, note=%q", m.actNote)
	}
}

func TestPalette_NewTeamActionOpensPreviewGatedFlow(t *testing.T) {
	m := newControlModel(t)
	addCandidateProject(m, "delta", "/fake/proj/delta")
	var ops []newTeamOp
	m.newTeam = func(op newTeamOp) error { ops = append(ops, op); return nil }

	m, _ = nocPress(m, "p")
	for _, ch := range "deltaactionnewteam" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palNewTeam {
		t.Fatalf("expected the new-team action selected, got %+v ok=%v", sel, ok)
	}
	m, _ = nocPress(m, "enter")
	if m.palette != nil {
		t.Fatal("selecting a palette action should close the palette")
	}
	if len(ops) != 0 {
		t.Fatal("selecting a palette action must not call the new-team seam")
	}
	if m.input == nil || m.input.kind != ctlNewTeam || m.input.stage != 1 {
		t.Fatalf("new-team action should open the roles editor, input=%+v", m.input)
	}

	m = typeControlText(t, m, "cto=codex,qa")
	m, _ = nocPress(m, "enter")
	if len(ops) != 0 {
		t.Fatal("entering roles should open preview, not call the new-team seam")
	}
	if m.pending == nil || !strings.Contains(m.pending.preview, "amq-squad new team --project /fake/proj/delta --roles cto,qa --binary cto=codex") {
		t.Fatalf("new-team action should preview the exact command, pending=%+v", m.pending)
	}
	m, _ = nocPress(m, "y")
	if len(ops) != 1 {
		t.Fatalf("confirming should call the new-team seam once, got %d", len(ops))
	}
	if ops[0].ProjectDir != "/fake/proj/delta" || ops[0].Roles != "cto,qa" || ops[0].Binary != "cto=codex" {
		t.Fatalf("new-team op mismatch: %+v", ops[0])
	}
}

func TestPalette_NewTeamActionAcceptsInitialSession(t *testing.T) {
	m := newControlModel(t)
	addCandidateProject(m, "delta", "/fake/proj/delta")
	var ops []newTeamOp
	m.newTeam = func(op newTeamOp) error { ops = append(ops, op); return nil }

	m, _ = nocPress(m, "p")
	for _, ch := range "deltaactionnewteam" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palNewTeam {
		t.Fatalf("expected the new-team action selected, got %+v ok=%v", sel, ok)
	}
	m, _ = nocPress(m, "enter")
	m = typeControlText(t, m, "cto,qa,session=issue-96")
	m, _ = nocPress(m, "enter")
	if m.pending == nil || !strings.Contains(m.pending.preview, "--roles cto,qa --session issue-96 --sync") {
		t.Fatalf("new-team action should preview the initial session, pending=%+v", m.pending)
	}
	m, _ = nocPress(m, "y")
	if len(ops) != 1 {
		t.Fatalf("confirming should call the new-team seam once, got %d", len(ops))
	}
	if ops[0].ProjectDir != "/fake/proj/delta" || ops[0].Roles != "cto,qa" || ops[0].Session != "issue-96" {
		t.Fatalf("new-team op mismatch: %+v", ops[0])
	}
}

func TestPalette_NewSessionActionOpensPreviewGatedFlow(t *testing.T) {
	m := newControlModel(t)
	addConfiguredEmptyProject(m, "empty-team", "/fake/proj/empty-team")
	var ops []newSessionOp
	m.newSession = func(op newSessionOp) error { ops = append(ops, op); return nil }

	m, _ = nocPress(m, "p")
	for _, ch := range "emptyactionnewsession" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palNewSession {
		t.Fatalf("expected the new-session action selected, got %+v ok=%v", sel, ok)
	}
	m, _ = nocPress(m, "enter")
	if len(ops) != 0 {
		t.Fatal("selecting a palette action must not call the new-session seam")
	}
	if m.input == nil || m.input.kind != ctlNewSession || m.input.stage != 1 {
		t.Fatalf("new-session action should open the session editor, input=%+v", m.input)
	}

	m = typeControlText(t, m, "issue-501")
	m, _ = nocPress(m, "enter")
	if len(ops) != 0 {
		t.Fatal("entering a session should open preview, not call the new-session seam")
	}
	if m.pending == nil || !strings.Contains(m.pending.preview, "amq-squad new session --project /fake/proj/empty-team --target new-session --terminal-session amq-squad-empty-team-issue-501 issue-501") {
		t.Fatalf("new-session action should preview the exact command, pending=%+v", m.pending)
	}
	m, _ = nocPress(m, "y")
	if len(ops) != 1 {
		t.Fatalf("confirming should call the new-session seam once, got %d", len(ops))
	}
	if ops[0].ProjectDir != "/fake/proj/empty-team" || ops[0].Session != "issue-501" {
		t.Fatalf("new-session op mismatch: %+v", ops[0])
	}
}

func TestPalette_NewSessionActionAcceptsSeedFrom(t *testing.T) {
	m := newControlModel(t)
	addConfiguredEmptyProject(m, "empty-team", "/fake/proj/empty-team")
	var ops []newSessionOp
	m.newSession = func(op newSessionOp) error { ops = append(ops, op); return nil }

	m, _ = nocPress(m, "p")
	for _, ch := range "emptyactionnewsession" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palNewSession {
		t.Fatalf("expected the new-session action selected, got %+v ok=%v", sel, ok)
	}
	m, _ = nocPress(m, "enter")
	m = typeControlText(t, m, "issue-502 seed-from=issue:31")
	m, _ = nocPress(m, "enter")
	if m.pending == nil || !strings.Contains(m.pending.preview, "--seed-from issue:31") ||
		!strings.Contains(m.pending.preview, "--terminal-session amq-squad-empty-team-issue-502 issue-502") {
		t.Fatalf("seeded new-session action should preview the exact command, pending=%+v", m.pending)
	}
	m, _ = nocPress(m, "y")
	if len(ops) != 1 {
		t.Fatalf("confirming should call the new-session seam once, got %d", len(ops))
	}
	if ops[0].ProjectDir != "/fake/proj/empty-team" || ops[0].Session != "issue-502" || ops[0].SeedFrom != "issue:31" {
		t.Fatalf("seeded new-session op mismatch: %+v", ops[0])
	}
}

func TestPalette_SyncPointersActionOpensPreviewGatedFlow(t *testing.T) {
	m := newControlModel(t)
	addConfiguredEmptyProject(m, "empty-team", "/fake/proj/empty-team")
	var ops []pointerSyncOp
	m.pointerSync = func(op pointerSyncOp) error {
		ops = append(ops, op)
		return nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "emptyactionsyncpointers" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palSyncPointers {
		t.Fatalf("expected the sync-pointers action selected, got %+v ok=%v", sel, ok)
	}
	m, _ = nocPress(m, "enter")
	if len(ops) != 0 {
		t.Fatal("selecting a palette action must not call the pointer-sync seam")
	}
	if m.input != nil {
		t.Fatalf("single-profile sync-pointers should not ask for input: %+v", m.input)
	}
	if m.pending == nil || !strings.Contains(m.pending.preview, "amq-squad team sync --project /fake/proj/empty-team --apply") {
		t.Fatalf("sync-pointers action should preview the exact command, pending=%+v", m.pending)
	}
	m, _ = nocPress(m, "y")
	if len(ops) != 1 {
		t.Fatalf("confirming should call the pointer-sync seam once, got %d", len(ops))
	}
	if ops[0].ProjectDir != "/fake/proj/empty-team" || ops[0].Profile != "" {
		t.Fatalf("pointer-sync op mismatch: %+v", ops[0])
	}
}

func TestPalette_SyncPointersActionAsksForProfileWhenAmbiguous(t *testing.T) {
	m := newControlModel(t)
	addNamedConfiguredProject(m, "many-profiles", "/fake/proj/many-profiles", "review", "release")
	var ops []pointerSyncOp
	m.pointerSync = func(op pointerSyncOp) error {
		ops = append(ops, op)
		return nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "many profiles sync pointers" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palSyncPointers {
		t.Fatalf("expected the sync-pointers action selected, got %+v ok=%v", sel, ok)
	}
	m, _ = nocPress(m, "enter")
	if m.input == nil || m.input.kind != ctlSyncPointers || m.input.stage != 0 || !strings.Contains(m.input.hint, "review, release") {
		t.Fatalf("multi-profile sync-pointers should ask for profile first, input=%+v", m.input)
	}
	m = typeControlText(t, m, "review")
	m, _ = nocPress(m, "enter")
	if len(ops) != 0 {
		t.Fatal("entering a profile should open preview, not call the pointer-sync seam")
	}
	if m.pending == nil || !strings.Contains(m.pending.preview, "amq-squad team sync --project /fake/proj/many-profiles --profile review --apply") {
		t.Fatalf("sync-pointers action should preview selected profile, pending=%+v", m.pending)
	}
	m, _ = nocPress(m, "y")
	if len(ops) != 1 {
		t.Fatalf("confirming should call the pointer-sync seam once, got %d", len(ops))
	}
	if ops[0].ProjectDir != "/fake/proj/many-profiles" || ops[0].Profile != "review" {
		t.Fatalf("pointer-sync op mismatch: %+v", ops[0])
	}
}

func TestPalette_DeleteTeamActionOpensPreviewGatedFlow(t *testing.T) {
	m := newControlModel(t)
	addNamedConfiguredProject(m, "many-profiles", "/fake/proj/many-profiles", "review", "release")
	var ops []teamDeleteOp
	m.teamDelete = func(op teamDeleteOp) error {
		ops = append(ops, op)
		return nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "many profiles delete team" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palDeleteTeam {
		t.Fatalf("expected the delete-team action selected, got %+v ok=%v", sel, ok)
	}
	m, _ = nocPress(m, "enter")
	if m.input == nil || m.input.kind != ctlDeleteTeam || m.input.stage != 0 || !strings.Contains(m.input.hint, "1 one profile") {
		t.Fatalf("delete-team should ask for a plain deletion choice first, input=%+v", m.input)
	}
	m = typeControlText(t, m, "1")
	m, _ = nocPress(m, "enter")
	if m.input == nil || m.input.kind != ctlDeleteTeam || m.input.stage != 1 || !strings.Contains(m.input.hint, "review, release") {
		t.Fatalf("one-profile delete-team should ask for profile next, input=%+v", m.input)
	}
	m = typeControlText(t, m, "release")
	m, _ = nocPress(m, "enter")
	if len(ops) != 0 {
		t.Fatal("entering a profile should open preview, not call the delete-team seam")
	}
	if m.pending == nil || !strings.Contains(m.pending.preview, "amq-squad team rm --project /fake/proj/many-profiles --profile release --yes") {
		t.Fatalf("delete-team action should preview selected profile, pending=%+v", m.pending)
	}
	m, _ = nocPress(m, "y")
	if len(ops) != 1 {
		t.Fatalf("confirming should call the delete-team seam once, got %d", len(ops))
	}
	if ops[0].ProjectDir != "/fake/proj/many-profiles" || ops[0].Profile != "release" {
		t.Fatalf("delete-team op mismatch: %+v", ops[0])
	}
}

func TestPalette_MultiTokenQueryMatchesActionOutOfOrder(t *testing.T) {
	m := newControlModel(t)
	addNamedConfiguredProject(m, "amq-squad", "/fake/proj/amq-squad", "default")

	m, _ = nocPress(m, "p")
	for _, ch := range "delete team amq-squad" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palDeleteTeam || sel.project != "amq-squad" {
		t.Fatalf("expected amq-squad delete-team action selected, got %+v ok=%v", sel, ok)
	}
}

func TestPalette_NewSessionActionRejectsExistingSession(t *testing.T) {
	m := newControlModel(t)
	called := false
	m.newSession = func(newSessionOp) error { called = true; return nil }

	m, _ = nocPress(m, "p")
	for _, ch := range "beta start session" {
		m, _ = nocPress(m, string(ch))
	}
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAction || sel.action != palNewSession {
		t.Fatalf("expected the beta new-session action selected, got %+v ok=%v", sel, ok)
	}
	m, _ = nocPress(m, "enter")
	m = typeControlText(t, m, "beta")
	m, _ = nocPress(m, "enter")

	if called {
		t.Fatal("existing session must not call the new-session seam")
	}
	if m.pending != nil {
		t.Fatal("existing session must not open a confirm overlay")
	}
	if m.input == nil || !strings.Contains(m.actNote, "session beta already exists") {
		t.Fatalf("existing session should keep editor open with guidance, input=%+v note=%q", m.input, m.actNote)
	}
}

// TestPalette_CursorMovesWithinResults proves down/up move the selection within
// the filtered results and clamp at the bounds.
func TestPalette_CursorMovesWithinResults(t *testing.T) {
	m := newPaletteModel(t)
	m, _ = nocPress(m, "p")
	if m.palette.cursor != 0 {
		t.Fatalf("palette cursor should start at 0, got %d", m.palette.cursor)
	}
	n := len(m.palette.filtered())
	if n < 2 {
		t.Fatalf("fixture must produce >= 2 palette rows, got %d", n)
	}
	m, _ = nocPress(m, "down")
	if m.palette.cursor != 1 {
		t.Errorf("down should move palette cursor to 1, got %d", m.palette.cursor)
	}
	m, _ = nocPress(m, "up")
	if m.palette.cursor != 0 {
		t.Errorf("up should move palette cursor back to 0, got %d", m.palette.cursor)
	}
	// Up at the top clamps.
	m, _ = nocPress(m, "up")
	if m.palette.cursor != 0 {
		t.Errorf("up at the top should clamp to 0, got %d", m.palette.cursor)
	}
}

func TestPalette_DownKeepsCursorVisibleInResultWindow(t *testing.T) {
	m := newLongTreeNOCModel(t, 20, 24)
	m, _ = nocPress(m, "p")
	if m.palette == nil {
		t.Fatal("palette should be open")
	}
	if n := len(m.palette.filtered()); n <= 12 {
		t.Fatalf("test expects more than 12 palette rows, got %d", n)
	}
	targetLabel := m.palette.filtered()[13].label
	if strings.Contains(m.paletteOverlayView(), targetLabel) {
		t.Fatalf("test setup should start above %q", targetLabel)
	}

	for i := 0; i < 13; i++ {
		m, _ = nocPress(m, "down")
	}

	if m.palette.cursor != 13 {
		t.Fatalf("palette cursor = %d, want 13", m.palette.cursor)
	}
	view := m.paletteOverlayView()
	if !strings.Contains(view, targetLabel) {
		t.Fatalf("selected palette row should be visible after scrolling down:\n%s", view)
	}
	if strings.Contains(view, "proj-00/project") {
		t.Fatalf("top palette rows should scroll out of view:\n%s", view)
	}
}

func TestPalette_ProjectSelectionKeepsTreeCursorVisible(t *testing.T) {
	m := newLongTreeNOCModel(t, 20, 12)
	if strings.Contains(m.treeView(), "proj-18") {
		t.Fatal("test setup should start above proj-18")
	}

	m, _ = nocPress(m, "p")
	if m.palette == nil {
		t.Fatal("palette should be open")
	}
	found := false
	for i, it := range m.palette.filtered() {
		if it.kind == palProject && it.label == "proj-18/project" {
			m.palette.cursor = i
			found = true
			break
		}
	}
	if !found {
		t.Fatal("proj-18/project not found in palette")
	}

	m, _ = nocPress(m, "enter")
	n, ok := m.selectedNode()
	if !ok || n.kind != nodeProject || n.label != "proj-18" {
		t.Fatalf("selected node = %+v ok=%v, want proj-18 project", n, ok)
	}
	if m.scroll == 0 {
		t.Fatal("palette project selection should move the tree scroll window")
	}
	view := m.treeView()
	if !strings.Contains(view, "proj-18") {
		t.Fatalf("selected project should be visible after palette selection:\n%s", view)
	}
	if strings.Contains(view, "proj-00") {
		t.Fatalf("top rows should scroll out of view after far selection:\n%s", view)
	}
}

// TestPalette_JumpSelectionDoesNotCallMutatingSeams proves jump/focus palette
// selections never reach mutating seams. Creation-action seam gating is covered
// by the palette action tests above.
func TestPalette_JumpSelectionDoesNotCallMutatingSeams(t *testing.T) {
	m := newPaletteModel(t)
	sent := false
	mutated := false
	m.sendOp = func(act.OpMessage) error { sent = true; return nil }
	m.lifecycle = func(lifecycleOp) error { mutated = true; return nil }
	m.panes = func() ([]noc.TmuxPane, error) {
		return []noc.TmuxPane{{Session: "beta", Window: "0", Pane: "1", Command: "codex", CWD: "/fake/proj/beta", Title: "amq:beta:cto"}}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "betacto" {
		m, _ = nocPress(m, string(ch))
	}
	m, _ = nocPress(m, "enter")
	if sent {
		t.Error("palette selection must NEVER call the send seam")
	}
	if mutated {
		t.Error("palette selection must NEVER call the lifecycle seam")
	}
}

// TestPalette_RunningAgentJumpsToAgentPaneNotTeamFocus independently PINS the
// running-agent JUMP branch of paletteSelect AGAINST the team-FOCUS branch.
//
// The panes are constructed so the two resolvers DIVERGE on purpose:
//   - ResolveTmuxTargetForSession (the agent jump) name-first-matches the cto's
//     own pane "amq:beta:cto" at beta:0.1.
//   - resolveSquadWindow (the team focus) returns the FIRST pane whose title has
//     the "amq:beta:" prefix, which here is a DIFFERENT squad pane "amq:beta:qa"
//     at beta:9.0 (listed first).
//
// A RUNNING-agent select MUST switch to the AGENT pane (beta:0.1). The test
// asserts both that the switch fires AND that it lands on beta:0.1 — and the
// companion guard below proves the focus window is a DIFFERENT pane (beta:9.0),
// so the assertion genuinely discriminates the two branches. If paletteSelect's
// running branch were gated off (collapsing to the focus path), the switch would
// land on beta:9.0 and this test FAILS.
func TestPalette_RunningAgentJumpsToAgentPaneNotTeamFocus(t *testing.T) {
	m := newPaletteModel(t)

	var gotTarget noc.TmuxTarget
	called := false
	m.switchTo = func(tt noc.TmuxTarget) error { called = true; gotTarget = tt; return nil }
	m.panes = func() ([]noc.TmuxPane, error) {
		return []noc.TmuxPane{
			// A DIFFERENT squad pane FIRST: resolveSquadWindow (team focus) returns
			// this one (beta:9.0) because its title carries the amq:beta: prefix.
			{Session: "beta", Window: "9", Pane: "0", Command: "claude", CWD: "/fake/proj/beta", Title: "amq:beta:qa"},
			// The cto's own name-first pane: ResolveTmuxTargetForSession (agent jump)
			// exact-matches amq:beta:cto and returns this one (beta:0.1).
			{Session: "beta", Window: "0", Pane: "1", Command: "codex", CWD: "/fake/proj/beta", Title: "amq:beta:cto"},
		}, nil
	}

	m, _ = nocPress(m, "p")
	for _, ch := range "betacto" {
		m, _ = nocPress(m, string(ch))
	}
	// Confirm the selection really is the RUNNING cto agent (so we exercise the
	// jump branch, not the focus branch).
	sel, ok := m.palette.selected()
	if !ok || sel.kind != palAgent || !sel.running || sel.label != "beta/beta/cto" {
		t.Fatalf("expected the running beta/beta/cto AGENT selected, got %+v ok=%v", sel, ok)
	}
	m, _ = nocPress(m, "enter")

	if !called {
		t.Fatal("running-agent select must call the switch seam (the gated jump)")
	}
	// MUST be the AGENT pane (beta:0.1), NOT the team-focus window (beta:9.0).
	if gotTarget.Session != "beta" || gotTarget.Window != "0" || gotTarget.Pane != "1" {
		t.Fatalf("running-agent select must JUMP to the agent pane beta:0.1, got %+v "+
			"(beta:9.0 is the team-focus window — landing there means the jump branch collapsed to focus)", gotTarget)
	}

	// Companion guard: the team-FOCUS resolver returns the OTHER pane (beta:9.0),
	// so the assertion above is genuinely discriminating, not a coincidence of a
	// single shared pane.
	panes, _ := m.panes()
	focus, found := resolveSquadWindow("beta", "/fake/proj/beta", panes)
	if !found {
		t.Fatalf("expected a squad-focus window to resolve")
	}
	if focus.Window == gotTarget.Window && focus.Pane == gotTarget.Pane {
		t.Fatalf("focus window (%s:%s.%s) must DIFFER from the agent pane (beta:0.1) for this test to discriminate",
			focus.Session, focus.Window, focus.Pane)
	}
}
