package console

import (
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

// seedOperatorMsg drops a needs-you message (addressed to "user") with a custom
// kind + subject into a discovered agent's inbox/new, so the coordination model
// classifies the thread needs-you and assigns its AttnReason from the subject.
func seedOperatorMsg(t *testing.T, agentDir, from, thread, kind, subject string, created time.Time) {
	t.Helper()
	inbox := filepath.Join(agentDir, "inbox", "new")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatalf("mkdir inbox: %v", err)
	}
	id := strings.ReplaceAll(thread, "/", "_")
	msg := "---json\n" +
		fmt.Sprintf(`{"schema":1,"id":%q,"thread":%q,"from":%q,"to":["user"],"kind":%q,"subject":%q,"created":%q}`,
			id, thread, from, kind, subject, created.UTC().Format(time.RFC3339Nano)) + "\n" +
		"---\n" +
		"body\n"
	if err := os.WriteFile(filepath.Join(inbox, id+".md"), []byte(msg), 0o600); err != nil {
		t.Fatalf("write msg: %v", err)
	}
}

// seedBlockedThread drops an agent<->agent message that DECLARES a block (via a
// block marker in the body) into a discovered agent's inbox/new, raising the
// thread's triage to blocked. Used to build LIVE blocked counts for the headline
// reconciliation fixture.
func seedBlockedThread(t *testing.T, agentDir, from, to, thread string, created time.Time) {
	t.Helper()
	inbox := filepath.Join(agentDir, "inbox", "new")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatalf("mkdir inbox: %v", err)
	}
	id := strings.ReplaceAll(thread, "/", "_")
	msg := "---json\n" +
		fmt.Sprintf(`{"schema":1,"id":%q,"thread":%q,"from":%q,"to":[%q],"kind":"status","subject":"status","created":%q}`,
			id, thread, from, to, created.UTC().Format(time.RFC3339Nano)) + "\n" +
		"---\n" +
		"I am blocked on the schema review.\n"
	if err := os.WriteFile(filepath.Join(inbox, id+".md"), []byte(msg), 0o600); err != nil {
		t.Fatalf("write msg: %v", err)
	}
}

// renderNeedsYouFixture builds a two-project workspace and returns the --once
// digest. One project ("ship") has BOTH an approve-reason and a goal-reached
// needs-you thread addressed to the operator; the other ("idle") is calm.
func renderNeedsYouFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	ship := filepath.Join(root, "ship")
	nocSeedTeamProfile(t, ship)
	sDir := nocSeedAgent(t, ship, "main", "qa", launch.Record{Binary: "claude", AgentPID: 7001})
	nocSeedPresence(t, sDir, "qa", "active", nocTestNow.Add(-10*time.Second))
	// A goal-reached ask and an approve ask in the same session.
	seedOperatorMsg(t, sDir, "qa", "ask/done", "question", "shipped, ready to close?", nocTestNow.Add(-5*time.Minute))
	seedOperatorMsg(t, sDir, "dev", "ask/approve", "question", "ok to proceed with deploy?", nocTestNow.Add(-2*time.Minute))

	idle := filepath.Join(root, "idle")
	nocSeedTeamProfile(t, idle)
	iDir := nocSeedAgent(t, idle, "main", "cto", launch.Record{Binary: "codex", AgentPID: 7002})
	nocSeedPresence(t, iDir, "cto", "active", nocTestNow.Add(-10*time.Second))

	probe := state.Probe{
		PIDAlive: func(pid int) bool { return pid == 7001 || pid == 7002 },
		ProcessMatch: func(pid int, predicate func(args string) bool) bool {
			switch pid {
			case 7001:
				return predicate("claude")
			case 7002:
				return predicate("codex")
			}
			return false
		},
		Now: func() time.Time { return nocTestNow },
	}
	return renderNOCOnce(t, root, probe, ColorNone)
}

// TestNOCNeedsYou_RenderAndSort proves PR13c part C render: the --once digest
// renders a NEEDS YOU block listing operator-addressed items with TEXT labels
// (APPROVE / GOAL-REACHED) that survive NO_COLOR, APPROVE sorted ABOVE
// GOAL-REACHED, and GOAL-REACHED kept inside NEEDS YOU (not a bare green check).
func TestNOCNeedsYou_RenderAndSort(t *testing.T) {
	out := renderNeedsYouFixture(t)

	if !strings.Contains(out, "NEEDS YOU") {
		t.Fatalf("digest should render a NEEDS YOU block:\n%s", out)
	}
	ai := strings.Index(out, "APPROVE")
	gi := strings.Index(out, "GOAL-REACHED")
	if ai < 0 {
		t.Errorf("NEEDS YOU should carry an APPROVE text label:\n%s", out)
	}
	if gi < 0 {
		t.Errorf("NEEDS YOU should carry a GOAL-REACHED text label:\n%s", out)
	}
	// APPROVE sorts ABOVE GOAL-REACHED.
	if ai >= 0 && gi >= 0 && !(ai < gi) {
		t.Errorf("APPROVE must sort above GOAL-REACHED (approve@%d goal@%d):\n%s", ai, gi, out)
	}
	// GOAL-REACHED lives inside NEEDS YOU (below its header, above NEEDS ATTENTION).
	nyi := strings.Index(out, "NEEDS YOU")
	nai := strings.Index(out, "NEEDS ATTENTION")
	if nyi < 0 || nai < 0 || !(nyi < gi && gi < nai) {
		t.Errorf("GOAL-REACHED must sit inside NEEDS YOU, between its header and NEEDS ATTENTION (ny@%d goal@%d na@%d):\n%s", nyi, gi, nai, out)
	}
	// The review accent must not collapse goal-reached into a healthy phrase.
	if strings.Contains(out, "team done · review and close") == false {
		t.Errorf("GOAL-REACHED row should read 'team done · review and close':\n%s", out)
	}
	// NO_COLOR: no escape codes leak.
	if strings.Contains(out, "\x1b[") {
		t.Errorf("NO_COLOR digest must not contain ANSI escapes:\n%q", out)
	}
}

// TestNOCNeedsYou_TreeInlineReason proves PR13c part C interactive render: in
// the tree, a needs-you SESSION row shows its reason inline (the top reason of
// its needs-you threads), with a TEXT label that survives NO_COLOR.
func TestNOCNeedsYou_TreeInlineReason(t *testing.T) {
	root := t.TempDir()
	ship := filepath.Join(root, "ship")
	nocSeedTeamProfile(t, ship)
	sDir := nocSeedAgent(t, ship, "main", "qa", launch.Record{Binary: "claude", AgentPID: 7301})
	nocSeedPresence(t, sDir, "qa", "active", nocTestNow.Add(-10*time.Second))
	seedOperatorMsg(t, sDir, "dev", "ask/approve", "question", "ok to proceed with deploy?", nocTestNow.Add(-2*time.Minute))

	probe := state.Probe{
		PIDAlive:     func(pid int) bool { return pid == 7301 },
		ProcessMatch: func(pid int, predicate func(args string) bool) bool { return predicate("claude") },
		Now:          func() time.Time { return nocTestNow },
	}
	rebuild := NOCRebuildConfig{Roots: []string{root}, Depth: noc.DefaultDepth, Probe: probe}
	ms := noc.Collect(rebuild.Roots, rebuild.Depth, rebuild.Probe, rebuild.Thresholds)
	m := newNOCModel(rebuild)
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	m.ms = ms
	m.ready = true
	m.fullTree = true
	m.width = 120
	m.height = 40

	tree := m.treeView()
	if !strings.Contains(tree, "APPROVE") {
		t.Errorf("a needs-you session row should show its reason (APPROVE) inline in the tree:\n%s", tree)
	}
}

// TestNOCNeedsYou_OmittedWhenNothingNeedsYou proves the truth-over-summaries
// rule: with no operator-addressed thread, the NEEDS YOU block is NOT rendered
// (never fabricated on a calm board).
func TestNOCNeedsYou_OmittedWhenNothingNeedsYou(t *testing.T) {
	root, probe := seedNOCFixtureCalm(t)
	out := renderNOCOnce(t, root, probe, ColorNone)
	if strings.Contains(out, "NEEDS YOU") {
		t.Errorf("a board with nothing addressed to the operator must omit NEEDS YOU:\n%s", out)
	}
}

// seedNOCFixtureCalm builds a single running project with NO operator-addressed
// thread, so the board is calm (no needs-you).
func seedNOCFixtureCalm(t *testing.T) (string, state.Probe) {
	t.Helper()
	root := t.TempDir()
	p := filepath.Join(root, "calm")
	dir := nocSeedAgent(t, p, "main", "cto", launch.Record{Binary: "codex", AgentPID: 8001})
	nocSeedPresence(t, dir, "cto", "active", nocTestNow.Add(-10*time.Second))
	probe := state.Probe{
		PIDAlive:     func(pid int) bool { return pid == 8001 },
		ProcessMatch: func(pid int, predicate func(args string) bool) bool { return predicate("codex") },
		Now:          func() time.Time { return nocTestNow },
	}
	return root, probe
}

// TestNOCHeadline_ReconcilesWithVisibleProjectStates proves the pulse counts the
// SAME visible project-row states the tree renders. Raw blocked/gated/at-risk
// thread buckets collapse into the primary "waiting" status.
func TestNOCHeadline_ReconcilesWithVisibleProjectStates(t *testing.T) {
	root := t.TempDir()

	// Two running projects, each with a live blocked agent<->agent thread.
	alpha := filepath.Join(root, "alpha")
	aDir := nocSeedAgent(t, alpha, "main", "qa", launch.Record{Binary: "claude", AgentPID: 9001})
	nocSeedPresence(t, aDir, "qa", "active", nocTestNow.Add(-10*time.Second))
	seedBlockedThread(t, aDir, "qa", "cto", "p2p/alpha-block", nocTestNow.Add(-20*time.Minute))

	beta := filepath.Join(root, "beta")
	bDir := nocSeedAgent(t, beta, "main", "dev", launch.Record{Binary: "codex", AgentPID: 9002})
	nocSeedPresence(t, bDir, "dev", "active", nocTestNow.Add(-10*time.Second))
	seedBlockedThread(t, bDir, "dev", "cto", "p2p/beta-block", nocTestNow.Add(-25*time.Minute))

	probe := state.Probe{
		PIDAlive: func(pid int) bool { return pid == 9001 || pid == 9002 },
		ProcessMatch: func(pid int, predicate func(args string) bool) bool {
			switch pid {
			case 9001:
				return predicate("claude")
			case 9002:
				return predicate("codex")
			}
			return false
		},
		Now: func() time.Time { return nocTestNow },
	}

	rebuild := NOCRebuildConfig{Roots: []string{root}, Depth: noc.DefaultDepth, Probe: probe}
	ms := noc.Collect(rebuild.Roots, rebuild.Depth, rebuild.Probe, rebuild.Thresholds)
	m := newNOCModel(rebuild)
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	m.ms = ms
	m.ready = true

	scope := m.scopedProjects()
	tally := childTallyProjects(scope)
	if tally.Blocked != 2 {
		t.Fatalf("fixture should produce 2 visible blocked projects, got tally=%+v", tally)
	}

	// And the rendered pulse line must show the reconciled count under the
	// simplified visible model: declared blocks stay blocked.
	pulse := m.pulseLine()
	want := fmt.Sprintf("%d blocked", tally.Blocked)
	if !strings.Contains(pulse, want) {
		t.Errorf("pulse line should show reconciled %q:\n%s", want, pulse)
	}
}

// TestNOCHeadline_ReconcilesUnderHideStale proves the reconciliation holds when
// a stale-only squad carrying blocked threads is HIDDEN: the pulse drops the
// hidden squad from BOTH the total and the rendered rows.
func TestNOCHeadline_ReconcilesUnderHideStale(t *testing.T) {
	root := t.TempDir()

	// A running project with a live blocked thread.
	live := filepath.Join(root, "live")
	lDir := nocSeedAgent(t, live, "main", "qa", launch.Record{Binary: "claude", AgentPID: 9101})
	nocSeedPresence(t, lDir, "qa", "active", nocTestNow.Add(-10*time.Second))
	seedBlockedThread(t, lDir, "qa", "cto", "p2p/live-block", nocTestNow.Add(-20*time.Minute))

	// A STOPPED, stale-only project that ALSO carries a (stale) blocked thread:
	// its agent is long dead and its block is far past the stale window, so it is
	// stale-only and hidden under hideStale.
	stale := filepath.Join(root, "stale")
	stDir := nocSeedAgent(t, stale, "main", "dev", launch.Record{Binary: "codex", AgentPID: 9102})
	nocSeedPresence(t, stDir, "dev", "offline", nocTestNow.Add(-30*24*time.Hour))
	seedBlockedThread(t, stDir, "dev", "cto", "p2p/stale-block", nocTestNow.Add(-30*24*time.Hour))

	probe := state.Probe{
		PIDAlive: func(pid int) bool { return pid == 9101 },
		ProcessMatch: func(pid int, predicate func(args string) bool) bool {
			switch pid {
			case 9101:
				return predicate("claude")
			case 9102:
				return predicate("codex")
			}
			return false
		},
		Now: func() time.Time { return nocTestNow },
	}

	rebuild := NOCRebuildConfig{Roots: []string{root}, Depth: noc.DefaultDepth, Probe: probe}
	ms := noc.Collect(rebuild.Roots, rebuild.Depth, rebuild.Probe, rebuild.Thresholds)
	m := newNOCModel(rebuild)
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	m.ms = ms
	m.ready = true
	m.hideStale = true

	scope := m.scopedProjects()
	tally := childTallyProjects(scope)
	if tally.Blocked != 1 {
		t.Fatalf("under hide-stale, pulse should count only the visible blocked project, got tally=%+v", tally)
	}
	pulse := m.pulseLine()
	if !strings.Contains(pulse, "1 blocked") {
		t.Errorf("pulse should count only the 1 visible blocked project:\n%s", pulse)
	}
	if strings.Contains(pulse, "1 stale") {
		t.Errorf("hidden stale squad should not appear in the pulse:\n%s", pulse)
	}
}
