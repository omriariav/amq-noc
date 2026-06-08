package noc

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-noc/internal/launch"
	"github.com/omriariav/amq-noc/internal/state"
)

// writeAgentLaunch writes a launch.json into
// <projectDir>/.agent-mail/<session>/agents/<handle>/ so Collect (via
// state.BuildWithThresholds) discovers it. Returns the agent dir.
func writeAgentLaunch(t *testing.T, projectDir, session, handle string, rec launch.Record) string {
	t.Helper()
	agentDir := filepath.Join(projectDir, AgentMailDirName, session, "agents", handle)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir agent dir: %v", err)
	}
	rec.Session = session
	rec.Handle = handle
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatalf("write launch: %v", err)
	}
	return agentDir
}

// writePresence writes a presence.json with the given status + last_seen so the
// liveness classifier can read a fresh mailbox heartbeat.
func writePresence(t *testing.T, agentDir, handle, status string, lastSeen time.Time) {
	t.Helper()
	body := `{"handle":"` + handle + `","status":"` + status + `","last_seen":"` + lastSeen.UTC().Format(time.RFC3339Nano) + `"}`
	if err := os.WriteFile(filepath.Join(agentDir, "presence.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write presence: %v", err)
	}
}

func TestCollect_MergesRollsUpAndSortsAttentionFirst(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	// --- Project "running": one alive agent, nothing outstanding (tier 2). ---
	running := filepath.Join(root, "running")
	aliveDir := writeAgentLaunch(t, running, "main", "codex", launch.Record{
		Binary: "codex", AgentPID: 4001, Root: filepath.Join(running, AgentMailDirName, "main"),
	})
	writePresence(t, aliveDir, "codex", "active", now.Add(-10*time.Second))

	// --- Project "needsyou": a question addressed to the operator (tier 0). ---
	needs := filepath.Join(root, "needsyou")
	writeOperatorTeamProfile(t, needs, 3, true, "user")
	qDir := writeAgentLaunch(t, needs, "main", "claude", launch.Record{
		Binary: "claude", AgentPID: 5001, Root: filepath.Join(needs, AgentMailDirName, "main"),
	})
	writeQuestionToOperator(t, qDir, "claude", now)

	// --- Project "stopped": a dead agent, nothing outstanding (tier 3). ---
	stopped := filepath.Join(root, "stopped")
	deadDir := writeAgentLaunch(t, stopped, "main", "codex", launch.Record{
		Binary: "codex", AgentPID: 6001, Root: filepath.Join(stopped, AgentMailDirName, "main"),
	})
	writePresence(t, deadDir, "codex", "offline", now.Add(-48*time.Hour))

	// Probe: 4001 alive (running proj), 5001 alive, 6001 dead.
	probe := state.Probe{
		PIDAlive: func(pid int) bool { return pid == 4001 || pid == 5001 },
		ProcessMatch: func(pid int, predicate func(args string) bool) bool {
			switch pid {
			case 4001, 6001:
				return predicate("codex")
			case 5001:
				return predicate("claude")
			}
			return false
		},
		Now: func() time.Time { return now },
	}

	ms := Collect([]string{root}, DefaultDepth, probe, state.Thresholds{})

	if len(ms.Projects) != 3 {
		t.Fatalf("expected 3 projects, got %d: %+v", len(ms.Projects), ms.Projects)
	}

	// Attention-first ordering: needsyou (tier0) < running (tier2) < stopped (tier3).
	order := []string{ms.Projects[0].Project, ms.Projects[1].Project, ms.Projects[2].Project}
	want := []string{"needsyou", "running", "stopped"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("attention-first order mismatch\n got: %v\nwant: %v", order, want)
		}
	}

	// Global rollup sums per-project rollups: exactly one needs-you overall.
	if ms.Rollup.NeedsYou != 1 {
		t.Fatalf("expected global NeedsYou=1, got %d (rollup=%+v)", ms.Rollup.NeedsYou, ms.Rollup)
	}

	// ObservedAt comes from the probe clock.
	if !ms.ObservedAt.Equal(now) {
		t.Fatalf("ObservedAt = %v, want %v", ms.ObservedAt, now)
	}

	// Each project's Dir is absolute and Project is its basename.
	for _, p := range ms.Projects {
		if !filepath.IsAbs(p.Dir) {
			t.Fatalf("project Dir not absolute: %q", p.Dir)
		}
		if p.Project != filepath.Base(p.Dir) {
			t.Fatalf("project label %q != basename of %q", p.Project, p.Dir)
		}
		if p.Warning != "" {
			t.Fatalf("unexpected warning on %q: %s", p.Project, p.Warning)
		}
	}
}

func TestCollect_NeverFatalOnEmptyRoot(t *testing.T) {
	root := t.TempDir() // no .agent-mail anywhere
	ms := Collect([]string{root}, DefaultDepth, deterministicProbe(time.Unix(0, 0)), state.Thresholds{})
	if len(ms.Projects) != 0 {
		t.Fatalf("expected 0 projects for empty root, got %d", len(ms.Projects))
	}
	if ms.Rollup.NeedsYou != 0 || ms.Rollup.AtRisk != 0 || ms.Rollup.Blocked != 0 {
		t.Fatalf("expected zero rollup, got %+v", ms.Rollup)
	}
}

func TestCollect_IncludesConfiguredTeamWithoutSessions(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "configured")
	writeOperatorTeamProfile(t, project, 3, true, "user")

	ms := Collect([]string{root}, DefaultDepth, deterministicProbe(time.Unix(0, 0)), state.Thresholds{})
	if len(ms.Projects) != 1 {
		t.Fatalf("expected configured team project, got %d: %+v", len(ms.Projects), ms.Projects)
	}
	ps := ms.Projects[0]
	if !ps.TeamConfigured || !ps.DefaultTeam || ps.Candidate {
		t.Fatalf("configured team flags mismatch: %+v", ps)
	}
	if len(ps.Profiles) != 1 || ps.Profiles[0] != "default" {
		t.Fatalf("configured team profiles mismatch: %+v", ps.Profiles)
	}
	if !ps.Operator.Enabled || ps.Operator.Handle != "user" || ps.Operator.Runnable || !ps.Capabilities.OperatorGates {
		t.Fatalf("operator metadata mismatch: op=%+v caps=%+v", ps.Operator, ps.Capabilities)
	}
	if len(ps.Snap.Sessions) != 0 || ps.Warning != "" {
		t.Fatalf("configured team without sessions should be quiet, got sessions=%d warning=%q", len(ps.Snap.Sessions), ps.Warning)
	}
}

func TestCollect_RuntimeActionsCapability(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "runtime-actions")
	squadDir := filepath.Join(project, SquadDirName)
	if err := os.MkdirAll(squadDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"schema":3,"operator":{"enabled":true,"handle":"user","runnable":false},"capabilities":{"operator_gates":true,"runtime_actions":true}}`
	if err := os.WriteFile(filepath.Join(squadDir, "team.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	ms := Collect([]string{root}, DefaultDepth, deterministicProbe(time.Unix(0, 0)), state.Thresholds{})
	if len(ms.Projects) != 1 {
		t.Fatalf("expected configured team project, got %d: %+v", len(ms.Projects), ms.Projects)
	}
	ps := ms.Projects[0]
	if !ps.Capabilities.OperatorGates || !ps.Capabilities.RuntimeActions {
		t.Fatalf("capability metadata mismatch: %+v", ps.Capabilities)
	}
}

func TestCollect_RecordsExistingAMQSessionDirs(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "configured")
	for _, dir := range []string{
		filepath.Join(project, AgentMailDirName, "zeta"),
		filepath.Join(project, AgentMailDirName, "alpha"),
		filepath.Join(project, AgentMailDirName, ".archive"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(project, SquadDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, SquadDirName, "team.json"), []byte(`{"schema":2}`), 0o600); err != nil {
		t.Fatal(err)
	}

	ms := Collect([]string{root}, DefaultDepth, deterministicProbe(time.Unix(0, 0)), state.Thresholds{})
	if len(ms.Projects) != 1 {
		t.Fatalf("expected configured project, got %d: %+v", len(ms.Projects), ms.Projects)
	}
	got := ms.Projects[0].SessionNames
	want := []string{"alpha", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("SessionNames = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SessionNames = %+v, want %+v", got, want)
		}
	}
	if len(ms.Projects[0].Snap.Sessions) != 0 {
		t.Fatalf("empty AMQ session dirs should not fabricate agent sessions: %+v", ms.Projects[0].Snap.Sessions)
	}
}

func TestCollect_IncludesNamedProfileWithoutSessions(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "configured")
	teamsDir := filepath.Join(project, SquadDirName, "teams")
	if err := os.MkdirAll(teamsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamsDir, "review.json"), []byte(`{"schema":2}`), 0o600); err != nil {
		t.Fatal(err)
	}

	ms := Collect([]string{root}, DefaultDepth, deterministicProbe(time.Unix(0, 0)), state.Thresholds{})
	if len(ms.Projects) != 1 {
		t.Fatalf("expected named team project, got %d: %+v", len(ms.Projects), ms.Projects)
	}
	ps := ms.Projects[0]
	if !ps.TeamConfigured || ps.DefaultTeam || ps.Candidate {
		t.Fatalf("named team flags mismatch: %+v", ps)
	}
	if len(ps.Profiles) != 1 || ps.Profiles[0] != "review" {
		t.Fatalf("named team profiles mismatch: %+v", ps.Profiles)
	}
	if len(ps.Snap.Sessions) != 0 || ps.Warning != "" {
		t.Fatalf("named team without sessions should be quiet, got sessions=%d warning=%q", len(ps.Snap.Sessions), ps.Warning)
	}
}

func TestCollect_IncludesGitCandidateProject(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "repo/.git")

	ms := Collect([]string{root}, DefaultDepth, deterministicProbe(time.Unix(0, 0)), state.Thresholds{})
	if len(ms.Projects) != 1 {
		t.Fatalf("expected candidate project, got %d: %+v", len(ms.Projects), ms.Projects)
	}
	ps := ms.Projects[0]
	if ps.Project != "repo" || !ps.Candidate || ps.TeamConfigured || ps.DefaultTeam || ps.SessionStore {
		t.Fatalf("candidate flags mismatch: %+v", ps)
	}
}

func TestCollect_ZeroProbeFillsClock(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "p/.agent-mail/main/agents")
	before := time.Now()
	ms := Collect([]string{root}, DefaultDepth, state.Probe{}, state.Thresholds{})
	after := time.Now()
	if ms.ObservedAt.Before(before) || ms.ObservedAt.After(after) {
		t.Fatalf("ObservedAt %v not within [%v,%v]", ms.ObservedAt, before, after)
	}
}

func TestCollect_LegacyTeamProfileKeepsDefaultOperatorGate(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	project := filepath.Join(root, "legacy")
	writeOperatorTeamProfile(t, project, 2, false, "")
	agentDir := writeAgentLaunch(t, project, "main", "cto", launch.Record{
		Binary: "codex", AgentPID: 1001, Root: filepath.Join(project, AgentMailDirName, "main"),
	})
	writeQuestionToOperator(t, agentDir, "cto", now)

	ms := Collect([]string{root}, DefaultDepth, state.Probe{
		PIDAlive:     func(pid int) bool { return pid == 1001 },
		ProcessMatch: func(pid int, predicate func(args string) bool) bool { return predicate("codex") },
		Now:          func() time.Time { return now },
	}, state.Thresholds{})

	if len(ms.Projects) != 1 {
		t.Fatalf("projects = %d, want 1", len(ms.Projects))
	}
	ps := ms.Projects[0]
	if !ps.Capabilities.OperatorGates || !ps.Operator.Enabled || ps.Operator.Handle != "user" {
		t.Fatalf("schema-2 amq-squad profile should preserve legacy operator gate: op=%+v caps=%+v", ps.Operator, ps.Capabilities)
	}
	if ps.Snap.Rollup.NeedsYou != 1 || ms.Rollup.NeedsYou != 1 {
		t.Fatalf("legacy operator gate should surface needs-you: project=%+v global=%+v", ps.Snap.Rollup, ms.Rollup)
	}
}

func TestCollect_LegacyRunnableUserMemberDisablesImplicitOperatorGate(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	project := filepath.Join(root, "legacy-user")
	squadDir := filepath.Join(project, SquadDirName)
	if err := os.MkdirAll(squadDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"schema":2,"members":[{"role":"operator-like","handle":"user","binary":"codex"}]}`
	if err := os.WriteFile(filepath.Join(squadDir, "team.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	agentDir := writeAgentLaunch(t, project, "main", "user", launch.Record{
		Binary: "codex", AgentPID: 1001, Root: filepath.Join(project, AgentMailDirName, "main"),
	})
	writeQuestionToOperator(t, agentDir, "cto", now)

	ms := Collect([]string{root}, DefaultDepth, state.Probe{
		PIDAlive:     func(pid int) bool { return pid == 1001 },
		ProcessMatch: func(pid int, predicate func(args string) bool) bool { return predicate("codex") },
		Now:          func() time.Time { return now },
	}, state.Thresholds{})

	if len(ms.Projects) != 1 {
		t.Fatalf("projects = %d, want 1", len(ms.Projects))
	}
	ps := ms.Projects[0]
	if ps.Capabilities.OperatorGates || ps.Operator.Enabled {
		t.Fatalf("legacy runnable user member must disable implicit operator gate: op=%+v caps=%+v", ps.Operator, ps.Capabilities)
	}
	if ps.Snap.Rollup.NeedsYou != 0 || ms.Rollup.NeedsYou != 0 {
		t.Fatalf("runnable user member should not create false needs-you: project=%+v global=%+v", ps.Snap.Rollup, ms.Rollup)
	}
}

func TestCollect_DoesNotTreatUserMailboxAsNeedsYouWhenOperatorOptedOut(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	project := filepath.Join(root, "optout")
	writeOperatorTeamProfile(t, project, 3, false, "")
	agentDir := writeAgentLaunch(t, project, "main", "cto", launch.Record{
		Binary: "codex", AgentPID: 1001, Root: filepath.Join(project, AgentMailDirName, "main"),
	})
	writeQuestionToOperator(t, agentDir, "cto", now)

	ms := Collect([]string{root}, DefaultDepth, state.Probe{
		PIDAlive:     func(pid int) bool { return pid == 1001 },
		ProcessMatch: func(pid int, predicate func(args string) bool) bool { return predicate("codex") },
		Now:          func() time.Time { return now },
	}, state.Thresholds{})

	if len(ms.Projects) != 1 {
		t.Fatalf("projects = %d, want 1", len(ms.Projects))
	}
	ps := ms.Projects[0]
	if ps.Capabilities.OperatorGates || ps.Operator.Enabled {
		t.Fatalf("schema-3 opt-out must not advertise operator gates: op=%+v caps=%+v", ps.Operator, ps.Capabilities)
	}
	if ps.Snap.Rollup.NeedsYou != 0 || ms.Rollup.NeedsYou != 0 {
		t.Fatalf("opt-out project should not surface needs-you: project=%+v global=%+v", ps.Snap.Rollup, ms.Rollup)
	}
}

func TestCollect_Schema3CustomOperatorHandleNeedsYou(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	project := filepath.Join(root, "custom")
	writeOperatorTeamProfile(t, project, 3, true, "operator")
	agentDir := writeAgentLaunch(t, project, "main", "cto", launch.Record{
		Binary: "codex", AgentPID: 1001, Root: filepath.Join(project, AgentMailDirName, "main"),
	})
	writeQuestionToHandle(t, agentDir, "cto", "operator", now)

	ms := Collect([]string{root}, DefaultDepth, state.Probe{
		PIDAlive:     func(pid int) bool { return pid == 1001 },
		ProcessMatch: func(pid int, predicate func(args string) bool) bool { return predicate("codex") },
		Now:          func() time.Time { return now },
	}, state.Thresholds{})

	if len(ms.Projects) != 1 {
		t.Fatalf("projects = %d, want 1", len(ms.Projects))
	}
	ps := ms.Projects[0]
	if !ps.Capabilities.OperatorGates || !ps.Operator.Enabled || ps.Operator.Handle != "operator" {
		t.Fatalf("custom operator metadata mismatch: op=%+v caps=%+v", ps.Operator, ps.Capabilities)
	}
	if ps.Snap.Rollup.NeedsYou != 1 || ms.Rollup.NeedsYou != 1 {
		t.Fatalf("custom operator gate should surface needs-you: project=%+v global=%+v", ps.Snap.Rollup, ms.Rollup)
	}
}

// writeQuestionToOperator drops a maildir message of kind=question addressed to
// the operator ("user") into a DISCOVERED agent's inbox/new, so the
// coordination model (which only scans discovered agents' mailboxes) sees it and
// flags the thread needs-you. The triage NeedsYou tier keys off the message
// being addressed-to and unread-by "user"; the physical inbox it sits in only
// needs to belong to a discovered agent so coordinateSession scans it.
func writeQuestionToOperator(t *testing.T, agentDir, from string, created time.Time) {
	t.Helper()
	writeQuestionToHandle(t, agentDir, from, "user", created)
}

func writeQuestionToHandle(t *testing.T, agentDir, from, to string, created time.Time) {
	t.Helper()
	inbox := filepath.Join(agentDir, "inbox", "new")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatalf("mkdir inbox: %v", err)
	}
	msg := "---json\n" +
		`{"schema":1,"id":"q1","thread":"decision/ship","from":"` + from + `","to":["` + to + `"],` +
		`"kind":"question","subject":"need a decision",` +
		`"created":"` + created.UTC().Format(time.RFC3339Nano) + `"}` + "\n" +
		"---\n" +
		"Should we ship?\n"
	if err := os.WriteFile(filepath.Join(inbox, "q1.md"), []byte(msg), 0o600); err != nil {
		t.Fatalf("write msg: %v", err)
	}
}

func writeOperatorTeamProfile(t *testing.T, projectDir string, schema int, enabled bool, handle string) {
	t.Helper()
	squadDir := filepath.Join(projectDir, SquadDirName)
	if err := os.MkdirAll(squadDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"schema":` + string(rune('0'+schema))
	if schema >= 3 {
		body += `,"operator":{"enabled":` + boolJSON(enabled)
		if handle != "" {
			body += `,"handle":"` + handle + `"`
		}
		body += `,"runnable":false},"capabilities":{"operator_gates":` + boolJSON(enabled) + `}`
	}
	body += `}`
	if err := os.WriteFile(filepath.Join(squadDir, "team.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func boolJSON(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func deterministicProbe(now time.Time) state.Probe {
	return state.Probe{
		PIDAlive:     func(int) bool { return false },
		ProcessMatch: func(int, func(string) bool) bool { return false },
		Now:          func() time.Time { return now },
	}
}
