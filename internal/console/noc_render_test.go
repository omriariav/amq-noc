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
	body := `{"schema":3,"operator":{"enabled":true,"handle":"user","runnable":false},"capabilities":{"operator_gates":true}}`
	if err := os.WriteFile(filepath.Join(dir, "team.json"), []byte(body), 0o600); err != nil {
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

	// Header pulse counts visible squad rows by primary state:
	// beta needs-you, alpha online, gamma stale.
	if !strings.Contains(out, "3 squads") {
		t.Errorf("header pulse missing '3 squads':\n%s", out)
	}
	if !strings.Contains(out, "1 online") {
		t.Errorf("header pulse missing '1 online':\n%s", out)
	}
	if !strings.Contains(out, "1 needs-you") {
		t.Errorf("header pulse missing '1 needs-you':\n%s", out)
	}
	if !strings.Contains(out, "1 stale") {
		t.Errorf("header pulse missing '1 stale':\n%s", out)
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
	for _, want := range []string{"3 squads", "1 online", "1 needs-you", "0 blocked", "0 waiting", "1 stale"} {
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
	for _, want := range []string{"1 squad", "0 online", "0 needs-you", "1 stale"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stopped needs-you render missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "needs-you history") {
		t.Fatalf("stopped needs-you history should collapse to the simplified stale surface:\n%s", out)
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
	// Attention-first: beta (needs-you) before alpha (online) before gamma (stopped).
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

func TestNOCSessionDetailNowUsesNewestWhenNoNeedsYou(t *testing.T) {
	m := newNOCModel(NOCRebuildConfig{})
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	m.width = 120
	now := nocTestNow
	sess := state.Session{
		Name: "main",
		Coordination: state.Coordination{Threads: []state.ThreadSummary{
			{
				ID:          "decision/status-model",
				Subject:     "Review: S1 first-class team/agent attention state (green)",
				Triage:      state.TriageAtRisk,
				Status:      state.ThreadAwaitingReply,
				LastEventAt: now.Add(-24 * time.Hour),
				Freshness:   state.Freshness{Age: 24 * time.Hour},
			},
			{
				ID:          "p2p/cto__fullstack",
				Subject:     "Retro: add sprint learnings to RETRO.md",
				Triage:      state.TriageClear,
				Status:      state.ThreadOpen,
				LastEventAt: now.Add(-41 * time.Second),
				Freshness:   state.Freshness{Age: 41 * time.Second},
			},
		}},
	}
	out := m.sessionDetail(nocNode{
		label:   "main",
		project: noc.ProjectSnapshot{Project: "proj"},
		session: sess,
	})
	nowIdx := strings.Index(out, "now")
	freshIdx := strings.Index(out, "Retro: add sprint learnings to RETRO.md")
	oldIdx := strings.Index(out, "Review: S1 first-class team/agent attention state")
	if nowIdx < 0 || freshIdx < 0 || oldIdx < 0 {
		t.Fatalf("session detail missing expected rows:\n%s", out)
	}
	if freshIdx < nowIdx || oldIdx < nowIdx {
		t.Fatalf("expected both thread rows after now:\n%s", out)
	}
	if oldIdx < freshIdx {
		t.Fatalf("old at-risk evidence must not lead now over newest current thread:\n%s", out)
	}
}

func TestNOCSessionDetailBlockedThreadRowsUseWaitingPrimary(t *testing.T) {
	m := newNOCModel(NOCRebuildConfig{})
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	m.width = 120
	sess := state.Session{
		Name: "pm-comms",
		Agents: []state.Agent{
			{Handle: "qa", Liveness: state.LivenessAlive, Attention: state.Attention{State: state.TriageBlocked}},
		},
		Attention: state.Attention{State: state.TriageBlocked},
		Coordination: state.Coordination{Threads: []state.ThreadSummary{{
			ID:           "p2p/cpo__qa",
			Subject:      "ACK: Slack contract routed",
			Participants: []string{"cpo", "qa"},
			Triage:       state.TriageBlocked,
			LastEventAt:  nocTestNow.Add(-23 * time.Second),
			Freshness:    state.Freshness{Age: 23 * time.Second},
		}}},
	}
	out := m.sessionDetail(nocNode{
		label:   "pm-comms",
		project: noc.ProjectSnapshot{Project: "taboola-pm-os"},
		session: sess,
	})
	if !strings.Contains(out, "blocked ACK: Slack contract routed  blocked 23s") {
		t.Fatalf("blocked thread should render as blocked primary with blocked reason:\n%s", out)
	}
	if strings.Contains(out, "waiting ACK: Slack contract routed") {
		t.Fatalf("declared block must not be collapsed to waiting:\n%s", out)
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
	header := fmt.Sprintf("threads: newest %d of %d", sessionThreadPreviewLimit, sessionThreadPreviewLimit+2)
	for _, want := range []string{header, "+2 older hidden"} {
		if !strings.Contains(out, want) {
			t.Fatalf("session detail missing capped-history marker %q:\n%s", want, out)
		}
	}
	hidden := fmt.Sprintf("status %02d", sessionThreadPreviewLimit) // first thread beyond the cap
	if strings.Contains(out, hidden) {
		t.Fatalf("session detail should hide older thread rows (%s):\n%s", hidden, out)
	}
}

// S4b: a session whose only agents are dead-mailbox-live (fresh presence but
// non-operational) with unowned at-risk evidence shows ONLINE, not waiting -
// waiting requires an operational agent to be the waiter. The evidence is
// retained in the rollup/detail, but stale means no fresh presence.
func TestNOCSessionVisible_AllDmblUnownedAtRiskIsOnlineNotWaiting(t *testing.T) {
	sess := state.Session{
		Name: "issue-96",
		Agents: []state.Agent{
			{Handle: "cto", Liveness: state.LivenessDeadMailboxLive},
			{Handle: "fullstack", Liveness: state.LivenessDeadMailboxLive},
		},
		Attention:        state.Attention{State: state.TriageClear},  // no operational owner
		UnownedAttention: state.Attention{State: state.TriageAtRisk}, // retained as detail
		Rollup:           state.TriageRollup{AtRisk: 1},
	}
	got := visibleState(sessionRollupState(sess))
	if got == nocNeedsYou || got == nocWaiting {
		t.Fatalf("dmbl session with unowned at-risk must not be waiting, got %s", nocStateText(got))
	}
	if got != nocRunning {
		t.Fatalf("visible status = %s, want online", nocStateText(got))
	}
}

func TestNOCSessionVisible_DefensivelyIgnoresNonHumanAttentionWithoutOperationalOwner(t *testing.T) {
	sess := state.Session{
		Name: "issue-96",
		Agents: []state.Agent{
			{Handle: "cto", Liveness: state.LivenessDeadMailboxLive, Attention: state.Attention{State: state.TriageAtRisk}},
			{Handle: "fullstack", Liveness: state.LivenessDeadMailboxLive, Attention: state.Attention{State: state.TriageBlocked}},
		},
		Attention: state.Attention{State: state.TriageAtRisk},
		Rollup:    state.TriageRollup{AtRisk: 1},
	}
	if got := visibleState(sessionRollupState(sess)); got != nocRunning {
		t.Fatalf("fresh-presence-only session with non-human attention must be online, not waiting; got %s", nocStateText(got))
	}
	if got := visibleState(agentNodeState(sess, sess.Agents[0])); got != nocRunning {
		t.Fatalf("dead-mailbox-live agent with at-risk attention must stay online, not waiting; got %s", nocStateText(got))
	}

	ps := noc.ProjectSnapshot{Snap: state.Snapshot{Sessions: []state.Session{sess}}}
	if got := visibleState(projectRollupState(ps)); got != nocRunning {
		t.Fatalf("project with fresh-presence-only non-human attention must be online, not waiting; got %s", nocStateText(got))
	}
}

// S4b companion: one operational (alive) agent that OWNS an aged peer review
// (at-risk) is genuinely waiting.
func TestNOCSessionVisible_OneLiveAgentOwnsAtRiskIsWaiting(t *testing.T) {
	sess := state.Session{
		Name: "issue-96",
		Agents: []state.Agent{
			{Handle: "cto", Liveness: state.LivenessAlive, Attention: state.Attention{State: state.TriageAtRisk}},
		},
		Attention: state.Attention{State: state.TriageAtRisk},
		Rollup:    state.TriageRollup{AtRisk: 1},
	}
	if got := visibleState(sessionRollupState(sess)); got != nocWaiting {
		t.Fatalf("one live agent owning an aged at-risk must be waiting, got %s", nocStateText(got))
	}
}

func TestNOCSessionVisible_NewerClearActivityBeatsOldAtRisk(t *testing.T) {
	old := nocTestNow.Add(-48 * time.Hour)
	fresh := nocTestNow.Add(-time.Minute)
	sess := state.Session{
		Name: "issue-96",
		Agents: []state.Agent{
			{Handle: "cto", Liveness: state.LivenessAlive, Attention: state.Attention{State: state.TriageAtRisk}},
			{Handle: "fullstack", Liveness: state.LivenessAlive, Attention: state.Attention{State: state.TriageAtRisk}},
		},
		Attention: state.Attention{State: state.TriageAtRisk},
		Rollup:    state.TriageRollup{AtRisk: 1, Clear: 1},
		Coordination: state.Coordination{Threads: []state.ThreadSummary{
			{ID: "decision/status-model", Triage: state.TriageAtRisk, LastEventAt: old, Participants: []string{"cto", "fullstack"}},
			{ID: "p2p/cto__fullstack", Triage: state.TriageClear, LastEventAt: fresh, Participants: []string{"cto", "fullstack"}},
		}},
	}
	if got := visibleState(sessionRollupState(sess)); got != nocRunning {
		t.Fatalf("newer clear activity should make the live session online, got %s", nocStateText(got))
	}
	if got := visibleState(agentNodeState(sess, sess.Agents[0])); got != nocRunning {
		t.Fatalf("newer clear activity should make the live agent online, got %s", nocStateText(got))
	}
	ps := noc.ProjectSnapshot{Snap: state.Snapshot{Sessions: []state.Session{sess}}}
	if got := visibleState(projectRollupState(ps)); got != nocRunning {
		t.Fatalf("newer clear activity should make the project online, got %s", nocStateText(got))
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
		t.Fatalf("session state = %s, want online", nocStateText(got))
	}
}

// A needs-you parent row names who the operator is needed by (the agents carrying
// needs-you attention), across a session and a project scope.
func TestNeedsYouParentRowOwner(t *testing.T) {
	sess := state.Session{
		Name: "main",
		Agents: []state.Agent{
			{Handle: "cpo", Attention: state.Attention{State: state.TriageNeedsYou}},
			{Handle: "cto", Attention: state.Attention{State: state.TriageBlocked}},
			{Handle: "qa", Attention: state.Attention{State: state.TriageBlocked}},
		},
	}
	sNode := nocNode{kind: nodeSession, session: sess}
	if owners := needsYouOwners(sNode); len(owners) != 1 || owners[0] != "cpo" {
		t.Fatalf("session needs-you owners = %v, want [cpo]", owners)
	}
	pNode := nocNode{kind: nodeProject, project: noc.ProjectSnapshot{Snap: state.Snapshot{Sessions: []state.Session{sess}}}}
	if owners := needsYouOwners(pNode); len(owners) != 1 || owners[0] != "cpo" {
		t.Fatalf("project needs-you owners = %v, want [cpo]", owners)
	}
}

// S5 follow-up: a structural operator gate (fullstack->user) keeps the parent
// narrative OWNER-LED even when the owner agent is non-operational (here
// dead-mailbox-live). The thread's NeedsYouOwner is the source of truth, so the
// session/project reads "fullstack needs you", not generic "team needs you". The
// agent row itself stays availability-true (online via fresh mailbox presence),
// not pretended waiting.
func TestNeedsYouParentRowOwner_StoppedOwnerFromThread(t *testing.T) {
	sess := state.Session{
		Name: "amq-noc-0-1-0",
		Agents: []state.Agent{
			{Handle: "cto", Liveness: state.LivenessAlive, Attention: state.Attention{State: state.TriageClear}},
			{Handle: "fullstack", Liveness: state.LivenessDeadMailboxLive, Attention: state.Attention{State: state.TriageClear}},
		},
		Coordination: state.Coordination{Threads: []state.ThreadSummary{
			{ID: "gate/manual-rc", Triage: state.TriageNeedsYou, AttnReason: state.AttnApprove,
				NeedsYouOwner: "fullstack", Participants: []string{"fullstack", "user"}},
		}},
	}
	sNode := nocNode{kind: nodeSession, session: sess}
	if owners := needsYouOwners(sNode); len(owners) != 1 || owners[0] != "fullstack" {
		t.Fatalf("session needs-you owners = %v, want [fullstack] (owner-led even when stopped)", owners)
	}
	pNode := nocNode{kind: nodeProject, project: noc.ProjectSnapshot{Snap: state.Snapshot{Sessions: []state.Session{sess}}}}
	if owners := needsYouOwners(pNode); len(owners) != 1 || owners[0] != "fullstack" {
		t.Fatalf("project needs-you owners = %v, want [fullstack]", owners)
	}

	m := NOCModel{th: newNOCTheme(ColorNone)}
	narrative := m.needsYouNarrative(sNode)
	if !strings.Contains(narrative, "fullstack needs you") {
		t.Fatalf("narrative = %q, want 'fullstack needs you'", narrative)
	}
	if strings.Contains(narrative, "team needs you") {
		t.Fatalf("narrative must not fall back to 'team needs you' when the owner is known: %q", narrative)
	}

	// Agent row stays availability-true: a dead-mailbox-live owner renders
	// online, but does not get promoted to a non-human waiting state.
	if got := agentNodeState(sess, sess.Agents[1]); got != nocRunning {
		t.Fatalf("fullstack agent node state = %s, want online (fresh mailbox presence, not pretended-waiting)", nocStateText(got))
	}
}

func TestNOCOnce_NeedsYouProjectDigestDoesNotLeakRawWaiting(t *testing.T) {
	sess := state.Session{
		Name: "amq-noc-0-1-0",
		Agents: []state.Agent{
			{Handle: "cto", Liveness: state.LivenessDeadMailboxLive},
			{Handle: "fullstack", Liveness: state.LivenessDeadMailboxLive},
		},
		Attention:        state.Attention{State: state.TriageNeedsYou, Reason: state.AttnApprove},
		UnownedAttention: state.Attention{State: state.TriageAtRisk},
		Rollup:           state.TriageRollup{NeedsYou: 1, Blocked: 1, AtRisk: 1},
		Coordination: state.Coordination{Threads: []state.ThreadSummary{
			{
				ID: "gate/manual-rc", Subject: "APPROVAL: manual RC", Triage: state.TriageNeedsYou,
				AttnReason: state.AttnApprove, NeedsYouOwner: "fullstack",
				Participants: []string{"fullstack", "user"}, LastEventAt: nocTestNow,
			},
			{ID: "p2p/cto__fullstack", Subject: "review", Triage: state.TriageBlocked, Participants: []string{"cto", "fullstack"}},
			{ID: "decision/status-model", Subject: "old review", Triage: state.TriageAtRisk, Participants: []string{"cto", "fullstack"}},
		}},
	}
	m := newNOCModel(NOCRebuildConfig{})
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	m.ready = true
	m.ms = noc.MultiSnapshot{
		ObservedAt: nocTestNow,
		Projects: []noc.ProjectSnapshot{{
			Project:        "amq-noc",
			Dir:            "/repo/amq-noc",
			TeamConfigured: true,
			DefaultTeam:    true,
			SessionStore:   true,
			Snap: state.Snapshot{
				Rollup:   state.TriageRollup{NeedsYou: 1, Blocked: 1, AtRisk: 1},
				Sessions: []state.Session{sess},
			},
		}},
	}

	out := m.staticView()
	if !strings.Contains(out, "0 waiting") {
		t.Fatalf("header should count visible operational waiting only:\n%s", out)
	}
	if !strings.Contains(out, "fullstack needs you") {
		t.Fatalf("digest project row should stay owner-led:\n%s", out)
	}
	for _, noisy := range []string{"2 waiting", "amq-noc stopped"} {
		if strings.Contains(out, noisy) {
			t.Fatalf("digest leaked raw/stale status %q:\n%s", noisy, out)
		}
	}
}

// The full tree's needs-you parent rows lead with the owner narrative ("qa needs
// you"), not a leading rollup count.
func TestNOCTree_NeedsYouParentRowIsOwnerLed(t *testing.T) {
	root, probe := seedNOCFixture(t)
	rebuild := NOCRebuildConfig{Roots: []string{root}, Depth: noc.DefaultDepth, Probe: probe}
	ms := noc.Collect(rebuild.Roots, rebuild.Depth, rebuild.Probe, rebuild.Thresholds)
	m := newNOCModel(rebuild)
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	m.ms = ms
	m.ready = true
	m.fullTree = true
	m.refreshGuidance()
	full := m.staticView()
	if !strings.Contains(full, "qa needs you") {
		t.Fatalf("needs-you parent row should be owner-led (qa needs you):\n%s", full)
	}
}

func TestNOCTree_ProjectParentTallyCountsChildTeams(t *testing.T) {
	sessions := []state.Session{
		{
			Name:      "waiting",
			Attention: state.Attention{State: state.TriageBlocked},
			Agents:    []state.Agent{{Handle: "dev", Liveness: state.LivenessAlive}},
			Rollup:    state.TriageRollup{Blocked: 9},
		},
		{
			Name:   "old-a",
			Agents: []state.Agent{{Handle: "cto", Liveness: state.LivenessDead}},
			Rollup: state.TriageRollup{BlockedStale: 16},
		},
		{
			Name:   "old-b",
			Agents: []state.Agent{{Handle: "qa", Liveness: state.LivenessDead}},
			Rollup: state.TriageRollup{AtRiskStale: 5, GatedStale: 3},
		},
	}
	ms := noc.MultiSnapshot{Roots: []string{"/repo"}, Projects: []noc.ProjectSnapshot{{
		Project: "taboola-pm-os",
		Dir:     "/repo/taboola-pm-os",
		Snap: state.Snapshot{
			Sessions: sessions,
			Rollup:   state.TriageRollup{Blocked: 9, BlockedStale: 16, AtRiskStale: 5, GatedStale: 3},
		},
	}}}
	m := newNOCModel(NOCRebuildConfig{Roots: []string{"/repo"}})
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	m.ms = ms
	m.ready = true
	out := m.treeView()
	if !strings.Contains(out, "taboola-pm-os (1 blocked, 2 stale)") {
		t.Fatalf("project row should count child teams/sessions by visible status:\n%s", out)
	}
	for _, noisy := range []string{"9 waiting", "16 blocked stale", "5 at-risk stale", "3 gated stale"} {
		if strings.Contains(out, noisy) {
			t.Fatalf("project row should not expose thread/evidence rollup %q:\n%s", noisy, out)
		}
	}
}

// The NEEDS YOU block is owner-led: the agent that needs the operator (from the
// first-class NeedsYouOwner) is the subject, and the thread reads as the
// evidence ("why:"), not as the primary status item.
func TestNOCOnce_NeedsYouRowOwnerLedThreadAsEvidence(t *testing.T) {
	root, probe := seedNOCFixture(t)
	out := renderNOCOnce(t, root, probe, ColorAscii)
	if !strings.Contains(out, "main - qa") {
		t.Fatalf("needs-you row should lead with the owning agent (main - qa):\n%s", out)
	}
	if !strings.Contains(out, "why: ship the migration?") {
		t.Fatalf("needs-you row should frame the thread as evidence (why: ...):\n%s", out)
	}
	if !strings.Contains(out, "qa asks") {
		t.Fatalf("needs-you phrase should name the owning agent from the model (qa asks):\n%s", out)
	}
}

// squadKindTag is the deterministic surface marker from structural snapshot
// fields: configured team metadata => squad, plain session store => amq.
func TestSquadKindTag(t *testing.T) {
	if got := squadKindTag(noc.ProjectSnapshot{TeamConfigured: true, SessionStore: true}); got != "squad" {
		t.Fatalf("configured team = %q, want squad", got)
	}
	if got := squadKindTag(noc.ProjectSnapshot{SessionStore: true}); got != "amq" {
		t.Fatalf("plain session store = %q, want amq", got)
	}
	if got := squadKindTag(noc.ProjectSnapshot{Candidate: true}); got != "" {
		t.Fatalf("candidate = %q, want empty", got)
	}
}

// kickRecoverLines delegates to amq-squad for squad-managed projects and to amq
// for plain AMQ surfaces, scoped to the project/session.
func TestKickRecoverLines(t *testing.T) {
	squad := kickRecoverLines(noc.ProjectSnapshot{Dir: "/repo/app", TeamConfigured: true, SessionStore: true}, "issue-96", "")
	joined := strings.Join(squad, "\n")
	if !strings.Contains(joined, "amq-squad status --project /repo/app --session issue-96") ||
		!strings.Contains(joined, "amq-squad resume --project /repo/app --session issue-96") {
		t.Fatalf("squad session commands wrong:\n%s", joined)
	}
	named := kickRecoverLines(noc.ProjectSnapshot{
		Dir:            "/repo/app",
		TeamConfigured: true,
		SessionStore:   true,
		Profiles:       []string{"testers"},
		Snap: state.Snapshot{Sessions: []state.Session{{
			Name: "issue-96",
			Agents: []state.Agent{
				{Handle: "claude-tester", TeamProfile: "testers"},
				{Handle: "codex-tester", TeamProfile: "testers"},
			},
		}}},
	}, "issue-96", "")
	nj := strings.Join(named, "\n")
	if !strings.Contains(nj, "amq-squad status --project /repo/app --profile testers --session issue-96") ||
		!strings.Contains(nj, "amq-squad resume --project /repo/app --profile testers --session issue-96") {
		t.Fatalf("named-profile squad session commands missing --profile:\n%s", nj)
	}
	for _, want := range []string{
		"amq-squad resume --project /repo/app --profile testers --exec --target current-window --session issue-96",
		"amq-squad resume --project /repo/app --profile testers --exec --target new-session --terminal-session amq-squad-app-issue-96 --session issue-96",
	} {
		if !strings.Contains(nj, want) {
			t.Fatalf("named-profile squad session commands missing %q:\n%s", want, nj)
		}
	}
	projectNamed := strings.Join(kickRecoverLines(noc.ProjectSnapshot{Dir: "/repo/app", TeamConfigured: true, SessionStore: true, Profiles: []string{"testers"}}, "", ""), "\n")
	for _, want := range []string{
		"amq-squad status --project /repo/app --profile testers",
		"amq-squad resume --project /repo/app --profile testers",
		"amq-squad up --project /repo/app --profile testers",
	} {
		if !strings.Contains(projectNamed, want) {
			t.Fatalf("named-profile project commands missing %q:\n%s", want, projectNamed)
		}
	}
	activeProject := strings.Join(kickRecoverLines(noc.ProjectSnapshot{
		Dir:            "/repo/app",
		TeamConfigured: true,
		SessionStore:   true,
		Profiles:       []string{"testers"},
		Snap: state.Snapshot{Sessions: []state.Session{
			{
				Name:   "active-session",
				Agents: []state.Agent{{Handle: "codex-tester", TeamProfile: "testers", Liveness: state.LivenessAlive}},
			},
			{
				Name:   "old-session",
				Agents: []state.Agent{{Handle: "codex-tester", TeamProfile: "testers", Liveness: state.LivenessDead}},
			},
		}},
	}, "", ""), "\n")
	for _, want := range []string{
		"amq-squad status --project /repo/app --profile testers --session active-session",
		"amq-squad resume --project /repo/app --profile testers --session active-session",
		"amq-squad resume --project /repo/app --profile testers --exec --target current-window --session active-session",
		"amq-squad resume --project /repo/app --profile testers --exec --target new-session --terminal-session amq-squad-app-active-session --session active-session",
	} {
		if !strings.Contains(activeProject, want) {
			t.Fatalf("active named-profile project command missing %q:\n%s", want, activeProject)
		}
	}
	if strings.Contains(activeProject, "old-session") {
		t.Fatalf("active project command should not point at stale session:\n%s", activeProject)
	}
	plain := kickRecoverLines(noc.ProjectSnapshot{Dir: "/repo/app", SessionStore: true}, "", "/repo/app/.agent-mail")
	pj := strings.Join(plain, "\n")
	for _, want := range []string{
		"amq who --root /repo/app/.agent-mail",
		"amq list --root /repo/app/.agent-mail",
		"amq read --root /repo/app/.agent-mail",
		"amq thread --root /repo/app/.agent-mail",
		"amq send --root /repo/app/.agent-mail",
	} {
		if !strings.Contains(pj, want) {
			t.Fatalf("plain amq commands missing %q:\n%s", want, pj)
		}
	}
	if strings.Contains(pj, "amq-squad") {
		t.Fatalf("plain amq must not suggest amq-squad:\n%s", pj)
	}
	plainSession := strings.Join(kickRecoverLines(
		noc.ProjectSnapshot{Dir: "/repo/app", SessionStore: true},
		"old-session",
		"/repo/app/.agent-mail/old-session",
	), "\n")
	for _, want := range []string{
		"amq-squad archive --project /repo/app --yes old-session",
		"amq-squad rm --project /repo/app --yes old-session",
		"amq who --root /repo/app/.agent-mail/old-session",
	} {
		if !strings.Contains(plainSession, want) {
			t.Fatalf("plain AMQ session commands missing %q:\n%s", want, plainSession)
		}
	}
}

func TestKickRecoverActionsLabelRuntimeCommands(t *testing.T) {
	actions := kickRecoverActions(noc.ProjectSnapshot{
		Dir:            "/repo/app",
		TeamConfigured: true,
		SessionStore:   true,
		Profiles:       []string{"testers"},
		Snap: state.Snapshot{Sessions: []state.Session{{
			Name:   "issue-96",
			Agents: []state.Agent{{Handle: "codex-tester", TeamProfile: "testers", Liveness: state.LivenessAlive}},
		}}},
	}, "issue-96", "")
	labels := map[string]string{}
	for _, action := range actions {
		labels[action.Label] = action.Command
	}
	for _, want := range []string{"status", "resume preview", "resume here", "open new tmux session"} {
		if labels[want] == "" {
			t.Fatalf("missing runtime action label %q in %+v", want, actions)
		}
	}
	if strings.Contains(labels["resume here"], "tmux") {
		t.Fatalf("NOC command action should call amq-squad, not raw tmux: %s", labels["resume here"])
	}
	if !strings.Contains(labels["resume here"], "amq-squad resume") ||
		!strings.Contains(labels["resume here"], "--target current-window") {
		t.Fatalf("resume here command wrong: %s", labels["resume here"])
	}
}

func TestAgentCommandActionsUseAMQFallbackAndSquadResume(t *testing.T) {
	ps := noc.ProjectSnapshot{
		Dir:            "/repo/app",
		TeamConfigured: true,
		Operator:       noc.OperatorConfig{Enabled: true, Handle: "operator"},
		Capabilities:   noc.Capabilities{OperatorGates: true},
	}
	sess := state.Session{Name: "issue-96", Root: "/repo/app/.agent-mail/issue-96"}
	ag := state.Agent{Handle: "qa", Role: "qa", Engine: "claude"}

	actions := agentCommandActions(ps, sess, ag)
	joined := ""
	for _, action := range actions {
		joined += action.Label + " => " + action.Command + "\n"
	}
	for _, want := range []string{
		"list inbox => amq list --root /repo/app/.agent-mail/issue-96 --me qa --new",
		"drain inbox => amq drain --root /repo/app/.agent-mail/issue-96 --me qa --include-body",
		"send message => amq send --root /repo/app/.agent-mail/issue-96 --me operator --to qa --thread THREAD_ID",
		"resume agent => amq-squad agent resume qa --project /repo/app --session issue-96",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("agent command actions missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "tmux") {
		t.Fatalf("agent fallback actions must not scrape or construct raw tmux commands:\n%s", joined)
	}
}

func TestCommandsSectionWrapsLongCommandsToDetailWidth(t *testing.T) {
	ps := noc.ProjectSnapshot{
		Project:        "app",
		Dir:            "/Users/example/Code/very-long-project-name",
		TeamConfigured: true,
		SessionStore:   true,
		Profiles:       []string{"testers"},
		Snap: state.Snapshot{Sessions: []state.Session{{
			Name:   "fabric-ud-feedback",
			Agents: []state.Agent{{Handle: "codex-tester", TeamProfile: "testers", Liveness: state.LivenessAlive}},
		}}},
	}
	m := newNOCModel(NOCRebuildConfig{})
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	m.width = 92
	out := m.commandsSection(ps, "", "")
	limit := m.commandDisplayWidth()
	if !strings.Contains(out, "resume here:") || !strings.Contains(out, "open new tmux session:") {
		t.Fatalf("commands section should label runtime actions:\n%s", out)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.Contains(trimmed, "amq-squad ") && !strings.HasPrefix(trimmed, "--") {
			continue
		}
		if visibleWidth(line) > limit {
			t.Fatalf("command line overflows detail width %d (%d cols):\n%s\nfull:\n%s", limit, visibleWidth(line), line, out)
		}
	}
}

func TestCommandPickerCopiesExactSelectedCommand(t *testing.T) {
	ps := noc.ProjectSnapshot{
		Project:        "app",
		Dir:            "/repo/app",
		TeamConfigured: true,
		SessionStore:   true,
		Profiles:       []string{"testers"},
		Snap: state.Snapshot{Sessions: []state.Session{{
			Name:   "issue-96",
			Agents: []state.Agent{{Handle: "codex-tester", TeamProfile: "testers", Liveness: state.LivenessAlive}},
		}}},
	}
	m := newNOCModel(NOCRebuildConfig{})
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	m.width = 90
	m.ms = noc.MultiSnapshot{Projects: []noc.ProjectSnapshot{ps}}
	m.ready = true
	var copied string
	m.copyText = func(s string) error {
		copied = s
		return nil
	}

	mm, cmd := m.Update(keyMsg("C"))
	m = *mm.(*NOCModel)
	if cmd != nil {
		t.Fatal("opening command picker should not copy yet")
	}
	if m.commandPicker == nil || len(m.commandPicker.commands) != 4 {
		t.Fatalf("command picker = %+v, want four commands", m.commandPicker)
	}
	overlay := m.commandPickerOverlayView()
	if !strings.Contains(overlay, "1-4") {
		t.Fatalf("command picker overlay should show numbered choices:\n%s", m.commandPickerOverlayView())
	}
	if !strings.Contains(overlay, "resume here:") {
		t.Fatalf("command picker overlay should show action labels:\n%s", overlay)
	}

	mm, cmd = m.Update(keyMsg("3"))
	m = *mm.(*NOCModel)
	if cmd == nil {
		t.Fatal("choosing command should return copy command")
	}
	msg := cmd()
	mm, _ = m.Update(msg)
	m = *mm.(*NOCModel)
	if !strings.Contains(copied, "--exec --target current-window") || !strings.Contains(copied, "--session issue-96") {
		t.Fatalf("copied command = %q, want exact current-window resume command", copied)
	}
	if !strings.Contains(m.actNote, "copied command") {
		t.Fatalf("copy success note missing, actNote=%q", m.actNote)
	}
}

// needsYouCTA pairs deny with its peer choice (approve for approval/decision,
// reply for a plain ask), never as a cryptic global key.
func TestNeedsYouCTA(t *testing.T) {
	m := NOCModel{colorMode: ColorNone, th: newNOCTheme(ColorNone)}
	if got := m.needsYouCTA(state.AttnApprove); !strings.Contains(got, "approve (a)") || !strings.Contains(got, "deny (x)") {
		t.Fatalf("approve CTA = %q, want approve+deny", got)
	}
	if got := m.needsYouCTA(state.AttnGeneric); !strings.Contains(got, "reply (r)") || !strings.Contains(got, "deny (x)") {
		t.Fatalf("generic CTA = %q, want reply+deny", got)
	}
}

// The session detail pane shows, for a needs-you thread, the ask context inline,
// the approve/deny CTA, and the kick/recover commands for a squad-managed project.
func TestNOCSessionDetail_NeedsYouContextCTAAndCommands(t *testing.T) {
	m := newNOCModel(NOCRebuildConfig{})
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	m.width = 120
	sess := state.Session{
		Name: "issue-96",
		Root: "/repo/app/.agent-mail/issue-96",
		Agents: []state.Agent{
			{Handle: "cpo", Liveness: state.LivenessAlive, Attention: state.Attention{State: state.TriageNeedsYou, Reason: state.AttnApprove}},
		},
		Coordination: state.Coordination{Threads: []state.ThreadSummary{{
			ID: "ask/ship", Subject: "APPROVAL: ship?", Participants: []string{"cpo", "user"},
			Triage: state.TriageNeedsYou, AttnReason: state.AttnApprove, NeedsYouOwner: "cpo",
			LatestBody:  "Please approve the prod deploy before the freeze.",
			LastEventAt: nocTestNow.Add(-5 * time.Minute), Freshness: state.Freshness{Age: 5 * time.Minute},
		}}},
	}
	out := m.sessionDetail(nocNode{
		kind: nodeSession, label: "issue-96",
		project: noc.ProjectSnapshot{Project: "app", Dir: "/repo/app", TeamConfigured: true, DefaultTeam: true, SessionStore: true},
		session: sess,
	})
	if !strings.Contains(out, "cpo paused") {
		t.Fatalf("session detail missing inline needs-you context:\n%s", out)
	}
	if !strings.Contains(out, "Please approve the prod deploy before the freeze.") {
		t.Fatalf("session detail must show the full ask body inline:\n%s", out)
	}
	if !strings.Contains(out, "approve (a)") || !strings.Contains(out, "deny (x)") {
		t.Fatalf("session detail missing approve/deny CTA:\n%s", out)
	}
	if !strings.Contains(out, "amq-squad resume --project /repo/app") {
		t.Fatalf("session detail missing squad kick/recover commands:\n%s", out)
	}
}

func TestNOCAgentNodeStateReflectsCurrentAttention(t *testing.T) {
	sess := state.Session{
		Name: "main",
		Agents: []state.Agent{
			{Handle: "cto", Liveness: state.LivenessAlive, Attention: state.Attention{State: state.TriageBlocked}},
			{Handle: "qa", Liveness: state.LivenessAlive, Attention: state.Attention{State: state.TriageNeedsYou, Reason: state.AttnGeneric}},
		},
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

func TestNOCAgentDetailShowsNeedsYouAsk(t *testing.T) {
	m := newNOCModel(NOCRebuildConfig{})
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	m.width = 120
	sess := state.Session{
		Name: "pm-comms",
		Coordination: state.Coordination{Threads: []state.ThreadSummary{
			{
				ID: "ask/newer-generic", Subject: "Question: later generic ask",
				Participants:  []string{"cpo", "user"},
				Triage:        state.TriageNeedsYou,
				AttnReason:    state.AttnGeneric,
				NeedsYouOwner: "cpo",
				LatestBody:    "This generic ask is newer but lower priority.",
				LastEventAt:   nocTestNow.Add(-2 * time.Minute),
				Freshness:     state.Freshness{Age: 2 * time.Minute},
			},
			{
				ID: "approval/comms", Subject: "APPROVAL: commit frozen /pm:comms collector+daemon snapshot",
				Participants:  []string{"cpo", "user"},
				Triage:        state.TriageNeedsYou,
				AttnReason:    state.AttnApprove,
				NeedsYouOwner: "cpo",
				LatestBody:    "Please approve preserving the collector snapshot before daemon work. RAW [keep-exact].",
				LastEventAt:   nocTestNow.Add(-18 * time.Minute),
				Freshness:     state.Freshness{Age: 18 * time.Minute},
			},
		}},
	}
	out := m.agentDetail(nocNode{
		label:   "cpo",
		project: noc.ProjectSnapshot{Project: "taboola-pm-os"},
		session: sess,
		agent:   state.Agent{Handle: "cpo", Role: "cpo", Engine: "codex", Liveness: state.LivenessAlive, Attention: state.Attention{State: state.TriageNeedsYou, Reason: state.AttnApprove}},
	})
	for _, want := range []string{
		"needs you",
		"APPROVAL: commit frozen /pm:comms",
		"collector+daemon snapshot",
		"ask: Please approve preserving the collector snapshot before daemon work. RAW",
		"[keep-exact].",
		"cpo paused",
		"18m",
		"approve (a)",
		"deny (x)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("agent needs-you detail missing %q:\n%s", want, out)
		}
	}
	needsYouBlock := out[strings.Index(out, "needs you"):]
	if strings.Contains(strings.Split(needsYouBlock, "latest signal")[0], "This generic ask is newer") {
		t.Fatalf("agent needs-you detail should lead with approval over newer generic ask:\n%s", out)
	}
}

func TestNOCHelpIncludesSymbolLegend(t *testing.T) {
	m := newNOCModel(NOCRebuildConfig{})
	m.colorMode = ColorAscii
	m.th = newNOCTheme(ColorAscii)
	m.width = 120
	out := m.helpView()
	for _, want := range []string{
		"PRIMARY STATUS MODEL",
		"team/session/agent is live",
		"operator action now",
		"needs-you",
		"online",
		"waiting",
		"stale",
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

func TestNOCFilterMatchesOnlineStateAndRunningAlias(t *testing.T) {
	sess := state.Session{
		Name: "issue-1",
		Agents: []state.Agent{
			{Handle: "cto", Liveness: state.LivenessAlive},
		},
	}
	ps := noc.ProjectSnapshot{
		Project: "api",
		Snap:    state.Snapshot{Sessions: []state.Session{sess}},
	}
	for _, filter := range []string{"online", "running"} {
		if !ProjectMatchesNOCFilter(ps, filter) {
			t.Fatalf("project should match %q", filter)
		}
		if !SessionMatchesNOCProjectFilter(ps, sess, filter) {
			t.Fatalf("session should match %q", filter)
		}
		if !AgentMatchesNOCProjectFilter(ps, sess, sess.Agents[0], filter) {
			t.Fatalf("agent should match %q", filter)
		}
	}
	if ProjectMatchesNOCFilter(ps, "waiting") {
		t.Fatal("online project must not match waiting")
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
	for _, label := range []string{"online", "needs-you", "stopped"} {
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

func TestRunNOC_OnceRendersVersionInHeader(t *testing.T) {
	root, _ := seedNOCFixture(t)
	var buf bytes.Buffer
	err := RunNOC(NOCConfig{
		Version: "v-test",
		Roots:   []string{root},
		Depth:   noc.DefaultDepth,
		Once:    true,
		Out:     &buf,
	})
	if err != nil {
		t.Fatalf("RunNOC --once: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "amq-noc NOC  v-test  command center") {
		t.Fatalf("--once header missing version:\n%s", out)
	}
}

func deterministicNOCProbe(now time.Time) state.Probe {
	return state.Probe{
		PIDAlive:     func(int) bool { return false },
		ProcessMatch: func(int, func(string) bool) bool { return false },
		Now:          func() time.Time { return now },
	}
}
