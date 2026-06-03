package console

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-noc/internal/launch"
	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/state"
)

// nocTestNow is the deterministic clock all NOC render fixtures age against.
var nocTestNow = time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

// nocSeedAgent writes a launch.json under
// <projectDir>/.agent-mail/<session>/agents/<handle>/ so noc.Collect (via
// state.BuildWithThresholds) discovers it. Returns the agent dir.
func nocSeedAgent(t *testing.T, projectDir, session, handle string, rec launch.Record) string {
	t.Helper()
	agentDir := filepath.Join(projectDir, noc.AgentMailDirName, session, "agents", handle)
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

func nocSeedPresence(t *testing.T, agentDir, handle, status string, lastSeen time.Time) {
	t.Helper()
	body := `{"schema":1,"handle":"` + handle + `","status":"` + status +
		`","last_seen":"` + lastSeen.UTC().Format(time.RFC3339Nano) + `"}`
	if err := os.WriteFile(filepath.Join(agentDir, "presence.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write presence: %v", err)
	}
}

func nocSeedTeamProfile(t *testing.T, projectDir string) {
	t.Helper()
	dir := filepath.Join(projectDir, noc.SquadDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir squad dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "team.json"), []byte(`{"schema":2}`), 0o600); err != nil {
		t.Fatalf("write team profile: %v", err)
	}
}

// nocSeedQuestionToOperator drops a needs-you question (addressed to "user") into
// a discovered agent's inbox/new, so the coordination model flags the thread.
func nocSeedQuestionToOperator(t *testing.T, agentDir, from string, created time.Time) {
	t.Helper()
	inbox := filepath.Join(agentDir, "inbox", "new")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatalf("mkdir inbox: %v", err)
	}
	msg := "---json\n" +
		`{"schema":1,"id":"q1","thread":"decision/ship","from":"` + from + `","to":["user"],` +
		`"kind":"question","subject":"ship the migration?",` +
		`"created":"` + created.UTC().Format(time.RFC3339Nano) + `"}` + "\n" +
		"---\n" +
		"Should we ship?\n"
	if err := os.WriteFile(filepath.Join(inbox, "q1.md"), []byte(msg), 0o600); err != nil {
		t.Fatalf("write msg: %v", err)
	}
}

// seedNOCFixture builds a three-project workspace under a temp root:
//
//	alpha  - one alive codex agent (running)
//	beta   - one alive claude agent with a needs-you question to the operator
//	gamma  - one dead codex agent (stopped)
//
// and returns the root plus a deterministic probe.
func seedNOCFixture(t *testing.T) (root string, probe state.Probe) {
	t.Helper()
	root = t.TempDir()

	alpha := filepath.Join(root, "alpha")
	nocSeedTeamProfile(t, alpha)
	aDir := nocSeedAgent(t, alpha, "main", "cto", launch.Record{Binary: "codex", AgentPID: 4001})
	nocSeedPresence(t, aDir, "cto", "active", nocTestNow.Add(-10*time.Second))

	beta := filepath.Join(root, "beta")
	nocSeedTeamProfile(t, beta)
	bDir := nocSeedAgent(t, beta, "main", "qa", launch.Record{Binary: "claude", AgentPID: 5001})
	nocSeedQuestionToOperator(t, bDir, "qa", nocTestNow)

	gamma := filepath.Join(root, "gamma")
	nocSeedTeamProfile(t, gamma)
	gDir := nocSeedAgent(t, gamma, "main", "dev", launch.Record{Binary: "codex", AgentPID: 6001})
	nocSeedPresence(t, gDir, "dev", "offline", nocTestNow.Add(-48*time.Hour))

	probe = state.Probe{
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
		Now: func() time.Time { return nocTestNow },
	}
	return root, probe
}

// renderNOCOnce collects a fixture and returns the static board exactly as the
// --once path would emit it, in the given color mode.
func renderNOCOnce(t *testing.T, root string, probe state.Probe, mode ColorMode) string {
	t.Helper()
	rebuild := NOCRebuildConfig{
		Roots: []string{root},
		Depth: noc.DefaultDepth,
		Probe: probe,
	}
	ms := noc.Collect(rebuild.Roots, rebuild.Depth, rebuild.Probe, rebuild.Thresholds)
	m := newNOCModel(rebuild)
	m.colorMode = mode
	m.th = newNOCTheme(mode)
	m.ms = ms
	m.ready = true
	m.refreshGuidance()
	return m.staticView()
}

func TestNOCOnce_MultiProjectBoard(t *testing.T) {
	root, probe := seedNOCFixture(t)
	out := renderNOCOnce(t, root, probe, ColorNone)

	// Header pulse counts: 3 squads, 2 running (alpha+beta running), 1 needs-you.
	if !strings.Contains(out, "3 squads") {
		t.Errorf("header pulse missing '3 squads':\n%s", out)
	}
	if !strings.Contains(out, "2 running") {
		t.Errorf("header pulse missing '2 running':\n%s", out)
	}
	if !strings.Contains(out, "1 needs-you") {
		t.Errorf("header pulse missing '1 needs-you':\n%s", out)
	}

	// Project grouping: every project label appears.
	for _, p := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(out, p) {
			t.Errorf("project %q missing from board:\n%s", p, out)
		}
	}

	// A needs-you row's TEXT label is present (color-independent).
	if !strings.Contains(out, "needs-you") {
		t.Errorf("expected a 'needs-you' text label in the board:\n%s", out)
	}

	// The --once default leads with the rollup digest sections.
	if !strings.Contains(out, "NEEDS ATTENTION") {
		t.Errorf("--once default should render a NEEDS ATTENTION section:\n%s", out)
	}
	if !strings.Contains(out, "PROJECTS") {
		t.Errorf("--once default should render a PROJECTS rollup section:\n%s", out)
	}
}

func TestNOCHeaderUsesSimplifiedPrimaryStatusModel(t *testing.T) {
	root, probe := seedNOCFixture(t)
	out := renderNOCOnce(t, root, probe, ColorNone)
	for _, want := range []string{"3 squads", "2 running", "1 needs-you", "0 blocked"} {
		if !strings.Contains(out, want) {
			t.Fatalf("header missing %q:\n%s", want, out)
		}
	}
	for _, noisy := range []string{"at-risk(live)", "blocked(live)", "gated(live)"} {
		if strings.Contains(out, noisy) {
			t.Fatalf("header should not expose noisy primary segment %q:\n%s", noisy, out)
		}
	}
}

func TestNOCStoppedAgentNeedsYouRendersAsHistoryNotLive(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "stopped")
	nocSeedTeamProfile(t, proj)
	agentDir := nocSeedAgent(t, proj, "main", "claude", launch.Record{Binary: "claude", AgentPID: 7001})
	nocSeedQuestionToOperator(t, agentDir, "claude", nocTestNow)

	probe := state.Probe{
		PIDAlive:     func(pid int) bool { return false },
		ProcessMatch: func(pid int, predicate func(args string) bool) bool { return predicate("claude") },
		Now:          func() time.Time { return nocTestNow },
	}
	out := renderNOCOnce(t, root, probe, ColorNone)
	for _, want := range []string{"1 squad", "0 running", "0 needs-you", "1 stale", "needs-you history"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stopped needs-you render missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "NEEDS YOU") {
		t.Fatalf("stopped agent ask must not render live NEEDS YOU section:\n%s", out)
	}
	if strings.Contains(out, "needs-you stopped") {
		t.Fatalf("stopped project must not render as live needs-you:\n%s", out)
	}
}

func TestNOCOnce_AttentionSortBetaFirst(t *testing.T) {
	root, probe := seedNOCFixture(t)
	out := renderNOCOnce(t, root, probe, ColorNone)

	ai := strings.Index(out, "alpha")
	bi := strings.Index(out, "beta")
	gi := strings.Index(out, "gamma")
	if bi < 0 || ai < 0 || gi < 0 {
		t.Fatalf("missing a project label: alpha=%d beta=%d gamma=%d\n%s", ai, bi, gi, out)
	}
	// Attention-first: beta (needs-you) before alpha (running) before gamma (stopped).
	if !(bi < ai && ai < gi) {
		t.Errorf("attention sort wrong: want beta<alpha<gamma, got beta=%d alpha=%d gamma=%d\n%s", bi, ai, gi, out)
	}
}

func TestNOCSessionDetailLabelsOpenThreadSortAndNeedsYouReason(t *testing.T) {
	m := newNOCModel(NOCRebuildConfig{})
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	m.width = 120
	now := nocTestNow
	sess := state.Session{
		Name: "main",
		Coordination: state.Coordination{Threads: []state.ThreadSummary{
			{
				ID:           "ask/old",
				Participants: []string{"qa", "user"},
				Subject:      "APPROVAL: run cleanup",
				Triage:       state.TriageNeedsYou,
				AttnReason:   state.AttnApprove,
				Status:       state.ThreadAwaitingReply,
				LastEventAt:  now.Add(-26 * time.Hour),
				Freshness:    state.Freshness{Age: 26 * time.Hour},
			},
			{
				ID:          "status/new",
				Subject:     "fresh status",
				Triage:      state.TriageClear,
				Status:      state.ThreadOpen,
				LastEventAt: now.Add(-30 * time.Second),
				Freshness:   state.Freshness{Age: 30 * time.Second},
			},
		}},
	}
	out := m.sessionDetail(nocNode{
		label:   "main",
		project: noc.ProjectSnapshot{Project: "proj"},
		session: sess,
	})
	for _, want := range []string{
		"now",
		"threads: newest",
		"user approval 1d",
		"fresh status",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("session detail missing %q:\n%s", want, out)
		}
	}
	threadsSection := out[strings.Index(out, "threads: newest"):]
	fresh := strings.Index(threadsSection, "fresh status")
	old := strings.Index(threadsSection, "APPROVAL: run cleanup")
	if fresh < 0 || old < 0 || fresh > old {
		t.Fatalf("session detail should lead with newest activity, got:\n%s", out)
	}
}

func TestNOCSessionDetailCapsThreadHistory(t *testing.T) {
	m := newNOCModel(NOCRebuildConfig{})
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	m.width = 120
	threads := make([]state.ThreadSummary, 0, sessionThreadPreviewLimit+2)
	for i := 0; i < sessionThreadPreviewLimit+2; i++ {
		threads = append(threads, state.ThreadSummary{
			ID:          fmt.Sprintf("status/%02d", i),
			Subject:     fmt.Sprintf("status %02d", i),
			Triage:      state.TriageClear,
			LastEventAt: nocTestNow.Add(-time.Duration(i) * time.Minute),
			Freshness:   state.Freshness{Age: time.Duration(i) * time.Minute},
		})
	}
	out := m.sessionDetail(nocNode{
		label:   "main",
		project: noc.ProjectSnapshot{Project: "proj"},
		session: state.Session{Name: "main", Coordination: state.Coordination{Threads: threads}},
	})
	for _, want := range []string{"threads: newest 8 of 10", "+2 older hidden"} {
		if !strings.Contains(out, want) {
			t.Fatalf("session detail missing capped-history marker %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "status 09") {
		t.Fatalf("session detail should hide older thread rows:\n%s", out)
	}
}

func TestNOCRunningSessionPrimaryStateIgnoresBlockedHistory(t *testing.T) {
	sess := state.Session{
		Name:   "main",
		Rollup: state.TriageRollup{Blocked: 3, Gated: 1, AtRisk: 1},
		Agents: []state.Agent{
			{Handle: "cto", Liveness: state.LivenessAlive},
			{Handle: "qa", Liveness: state.LivenessAlive},
		},
	}
	if got := sessionRollupState(sess); got != nocRunning {
		t.Fatalf("session state = %s, want running", nocStateText(got))
	}
}

func TestNOCAgentNodeStateReflectsCurrentAttention(t *testing.T) {
	sess := state.Session{
		Name: "main",
		Agents: []state.Agent{
			{Handle: "cto", Liveness: state.LivenessAlive},
			{Handle: "qa", Liveness: state.LivenessAlive},
		},
		Coordination: state.Coordination{Threads: []state.ThreadSummary{
			{
				ID:           "release/hold",
				Participants: []string{"qa", "user"},
				Subject:      "release held",
				Triage:       state.TriageNeedsYou,
				AttnReason:   state.AttnGeneric,
				LastEventAt:  nocTestNow.Add(-3 * time.Hour),
				Freshness:    state.Freshness{Age: 3 * time.Hour},
			},
			{
				ID:           "cto/blocked",
				Participants: []string{"cto", "qa"},
				Subject:      "blocked on release",
				Triage:       state.TriageBlocked,
				LastEventAt:  nocTestNow.Add(-3 * time.Hour),
				Freshness:    state.Freshness{Age: 3 * time.Hour},
			},
		}},
	}
	if got := agentNodeState(sess, sess.Agents[0]); got != nocBlocked {
		t.Fatalf("cto state = %s, want blocked", nocStateText(got))
	}
	if got := agentNodeState(sess, sess.Agents[1]); got != nocNeedsYou {
		t.Fatalf("qa state = %s, want needs-you", nocStateText(got))
	}
}

func TestNOCAgentDetailShowsLatestSignalForRunningAgent(t *testing.T) {
	m := newNOCModel(NOCRebuildConfig{})
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	m.width = 120
	sess := state.Session{
		Name: "main",
		Coordination: state.Coordination{Threads: []state.ThreadSummary{
			{
				ID:           "status/old",
				Participants: []string{"cto"},
				Subject:      "older signal",
				Triage:       state.TriageClear,
				LastEventAt:  nocTestNow.Add(-10 * time.Minute),
				Freshness:    state.Freshness{Age: 10 * time.Minute},
			},
			{
				ID:           "status/new",
				Participants: []string{"cto", "qa"},
				Subject:      "latest build finished",
				Triage:       state.TriageClear,
				LastEventAt:  nocTestNow.Add(-30 * time.Second),
				Freshness:    state.Freshness{Age: 30 * time.Second},
			},
		}},
	}
	out := m.agentDetail(nocNode{
		label:   "cto",
		project: noc.ProjectSnapshot{Project: "proj"},
		session: sess,
		agent:   state.Agent{Handle: "cto", Role: "cto", Engine: "codex", Liveness: state.LivenessAlive},
		canJump: true,
	})
	for _, want := range []string{"latest signal", "latest build finished", "30s", "recent threads"} {
		if !strings.Contains(out, want) {
			t.Fatalf("agent detail missing %q:\n%s", want, out)
		}
	}
}

func TestNOCHelpIncludesSymbolLegend(t *testing.T) {
	m := newNOCModel(NOCRebuildConfig{})
	m.colorMode = ColorAscii
	m.th = newNOCTheme(ColorAscii)
	m.width = 120
	out := m.helpView()
	for _, want := range []string{
		"PRIMARY STATE MODEL",
		"team is alive and working",
		"operator action now",
		"SYMBOL LEGEND",
		"needs-you",
		"blocked",
		"gated",
		"at-risk",
		"running",
		"stopped",
		"stale-blocked",
		"waiting",
		"APPROVE",
		"GOAL-REACHED",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("help missing legend entry %q:\n%s", want, out)
		}
	}
}

func TestNOCFilterMatchesAMQIntegrationMetadata(t *testing.T) {
	ps := noc.ProjectSnapshot{
		Project: "api",
		Snap: state.Snapshot{Sessions: []state.Session{{
			Name: "issue-1",
			Coordination: state.Coordination{Threads: []state.ThreadSummary{{
				ID:           "handoff/test",
				Labels:       []string{"handoff", "blocking"},
				Orchestrator: "kanban",
				FromProject:  "web",
			}}},
		}}},
	}
	for _, filter := range []string{"label:handoff", "label:block", "orchestrator:kanban", "web"} {
		if !ProjectMatchesNOCFilter(ps, filter) {
			t.Fatalf("filter %q should match integration metadata", filter)
		}
	}
	if ProjectMatchesNOCFilter(ps, "label:missing") {
		t.Fatal("missing label should not match")
	}
}

func TestNOCOnce_StoppedProjectReadsStopped(t *testing.T) {
	root, probe := seedNOCFixture(t)
	rebuild := NOCRebuildConfig{Roots: []string{root}, Depth: noc.DefaultDepth, Probe: probe}
	ms := noc.Collect(rebuild.Roots, rebuild.Depth, rebuild.Probe, rebuild.Thresholds)
	m := newNOCModel(rebuild)
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	m.ms = ms
	m.ready = true

	// Select gamma's project node so the detail pane renders its stopped agent.
	gammaIdx := -1
	for i, n := range m.nodes() {
		if n.kind == nodeProject && n.label == "gamma" {
			gammaIdx = i
			break
		}
	}
	if gammaIdx < 0 {
		t.Fatalf("gamma project node not found in %d nodes", len(m.nodes()))
	}
	m.cursor = gammaIdx
	detail := m.detailView()
	if !strings.Contains(detail, "stopped") {
		t.Errorf("gamma detail should mark its agent 'stopped':\n%s", detail)
	}
}

func TestNOCOnce_NoColorHasNoEscapeCodes(t *testing.T) {
	root, probe := seedNOCFixture(t)
	out := renderNOCOnce(t, root, probe, ColorNone)
	if strings.Contains(out, "\x1b[") {
		t.Errorf("ColorNone render must not contain ANSI escape codes:\n%q", out)
	}
}

func TestNOCOnce_AsciiFallbackTextLabelsNoEscapes(t *testing.T) {
	root, probe := seedNOCFixture(t)
	out := renderNOCOnce(t, root, probe, ColorAscii)

	if strings.Contains(out, "\x1b[") {
		t.Errorf("ColorAscii render must not contain ANSI escape codes:\n%q", out)
	}
	// State TEXT labels are always present.
	for _, label := range []string{"running", "needs-you", "stopped"} {
		if !strings.Contains(out, label) {
			t.Errorf("ascii render missing text label %q:\n%s", label, out)
		}
	}
	// Ascii markers, not unicode glyphs, on the dumb-terminal fallback.
	if strings.ContainsAny(out, "●◐○⚠✕▾▸►·") {
		t.Errorf("ascii render must not contain unicode glyphs/separators:\n%s", out)
	}
}

func TestNOCOnce_FullColorEmitsEscapes(t *testing.T) {
	root, probe := seedNOCFixture(t)
	out := renderNOCOnce(t, root, probe, ColorFull)
	// The needs-you eye-grab is bold/hot, so full color must emit some ANSI.
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("ColorFull render expected ANSI escape codes, got none:\n%q", out)
	}
}

func TestNOCOnce_NoProjectsGuidance(t *testing.T) {
	root := t.TempDir() // empty: no .agent-mail anywhere
	out := renderNOCOnce(t, root, deterministicNOCProbe(nocTestNow), ColorNone)
	if !strings.Contains(out, "No amq-squad projects found") {
		t.Errorf("empty roots should render guidance, got:\n%s", out)
	}
	if !strings.Contains(out, "amq-squad new team --project <team-home>") ||
		!strings.Contains(out, "amq-squad new session --project <team-home> <name>") {
		t.Errorf("empty roots guidance should point at create verbs, got:\n%s", out)
	}
	if strings.Contains(out, "panic") {
		t.Errorf("guidance must never look like a crash:\n%s", out)
	}
}

func TestNOCOnce_GitCandidateTeamHome(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "candidate")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	out := renderNOCOnce(t, root, deterministicNOCProbe(nocTestNow), ColorNone)
	if !strings.Contains(out, "candidate team-home") {
		t.Fatalf("git candidate should render as a team-home candidate, got:\n%s", out)
	}
}

func TestRunNOC_OnceWritesPlainTextToBuffer(t *testing.T) {
	root, _ := seedNOCFixture(t)
	var buf bytes.Buffer
	err := RunNOC(NOCConfig{
		Roots: []string{root},
		Depth: noc.DefaultDepth,
		Once:  true,
		Out:   &buf,
	})
	if err != nil {
		t.Fatalf("RunNOC --once: %v", err)
	}
	out := buf.String()
	// A bytes.Buffer is not a TTY, so output must be plain text (no escapes).
	if strings.Contains(out, "\x1b[") {
		t.Errorf("--once to a non-TTY writer must be plain text:\n%q", out)
	}
	if !strings.Contains(out, "squad") {
		t.Errorf("--once board missing header pulse:\n%s", out)
	}
}

func deterministicNOCProbe(now time.Time) state.Probe {
	return state.Probe{
		PIDAlive:     func(int) bool { return false },
		ProcessMatch: func(int, func(string) bool) bool { return false },
		Now:          func() time.Time { return now },
	}
}
