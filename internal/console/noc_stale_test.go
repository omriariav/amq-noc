package console

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-noc/internal/launch"
	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/state"
)

// nocSeedReviewRequest drops an agent->agent review_request (NOT to the operator)
// into a discovered agent's inbox/new, created at `created`. When that age is past
// ReviewAge but within the stale window it collapses to a LIVE at-risk thread.
func nocSeedReviewRequest(t *testing.T, agentDir, from, to string, created time.Time) {
	t.Helper()
	inbox := filepath.Join(agentDir, "inbox", "new")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatalf("mkdir inbox: %v", err)
	}
	msg := "---json\n" +
		`{"schema":1,"id":"rr1","thread":"review/api","from":"` + from + `","to":["` + to + `"],` +
		`"kind":"review_request","subject":"review the api change",` +
		`"created":"` + created.UTC().Format(time.RFC3339Nano) + `"}` + "\n" +
		"---\n" +
		"Please review.\n"
	if err := os.WriteFile(filepath.Join(inbox, "rr1.md"), []byte(msg), 0o600); err != nil {
		t.Fatalf("write review request: %v", err)
	}
}

// nocSeedBlockedThread drops an agent->agent status whose body declares a block
// ("blocked on"), created at `created`. Aged past the stale window it collapses
// to a STALE blocked thread (age-decayed, not live attention).
func nocSeedBlockedThread(t *testing.T, agentDir, from, to string, created time.Time) {
	t.Helper()
	inbox := filepath.Join(agentDir, "inbox", "new")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatalf("mkdir inbox: %v", err)
	}
	msg := "---json\n" +
		`{"schema":1,"id":"bl1","thread":"build/pipeline","from":"` + from + `","to":["` + to + `"],` +
		`"kind":"status","subject":"pipeline stuck",` +
		`"created":"` + created.UTC().Format(time.RFC3339Nano) + `"}` + "\n" +
		"---\n" +
		"I am blocked on the upstream migration.\n"
	if err := os.WriteFile(filepath.Join(inbox, "bl1.md"), []byte(msg), 0o600); err != nil {
		t.Fatalf("write blocked thread: %v", err)
	}
}

// seedStaleFixture builds two squads under one root:
//
//	live   - RUNNING (alive cto + alive dev), with a LIVE at-risk review thread
//	         (aged past ReviewAge but within the stale window). Active "just now".
//	old    - STOPPED (dead dev), with an OLD blocked thread last touched well past
//	         the stale window (30 days ago). Its block is STALE, age-decayed noise.
//
// This is the brief's real-board shape: the running at-risk squad must outrank
// the stopped-stale one.
func seedStaleFixture(t *testing.T) (root string, probe state.Probe, now time.Time) {
	t.Helper()
	root = t.TempDir()
	now = time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	// live: two alive agents; a review_request aged 2h (> 45m ReviewAge, < 72h).
	live := filepath.Join(root, "live")
	ctoDir := nocSeedAgent(t, live, "vault", "cto", launch.Record{Binary: "codex", AgentPID: 7001})
	nocSeedPresence(t, ctoDir, "cto", "active", now.Add(-5*time.Second))
	devDir := nocSeedAgent(t, live, "vault", "dev", launch.Record{Binary: "claude", AgentPID: 7002})
	nocSeedPresence(t, devDir, "dev", "active", now.Add(-5*time.Second))
	nocSeedReviewRequest(t, devDir, "cto", "dev", now.Add(-2*time.Hour))

	// old: a dead agent; a blocked thread last touched 30 days ago (stale).
	old := filepath.Join(root, "old")
	oldDir := nocSeedAgent(t, old, "main", "dev", launch.Record{Binary: "codex", AgentPID: 8001})
	nocSeedPresence(t, oldDir, "dev", "offline", now.Add(-30*24*time.Hour))
	nocSeedBlockedThread(t, oldDir, "qa", "dev", now.Add(-30*24*time.Hour))

	probe = state.Probe{
		PIDAlive: func(pid int) bool { return pid == 7001 || pid == 7002 },
		ProcessMatch: func(pid int, predicate func(args string) bool) bool {
			switch pid {
			case 7001, 8001:
				return predicate("codex")
			case 7002:
				return predicate("claude")
			}
			return false
		},
		Now: func() time.Time { return now },
	}
	return root, probe, now
}

func collectStale(t *testing.T, root string, probe state.Probe) noc.MultiSnapshot {
	t.Helper()
	return noc.Collect([]string{root}, noc.DefaultDepth, probe, state.Thresholds{})
}

// TestNOC_RunningAtRiskOutranksStoppedStale is the core PR13b ranking assertion:
// the RUNNING at-risk squad sorts ABOVE the STOPPED squad whose only blocked
// thread is past the stale window.
func TestNOC_RunningAtRiskOutranksStoppedStale(t *testing.T) {
	root, probe, _ := seedStaleFixture(t)
	ms := collectStale(t, root, probe)

	if len(ms.Projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(ms.Projects))
	}
	if ms.Projects[0].Project != "live" {
		t.Fatalf("running at-risk squad must rank first, got order: %s, %s",
			ms.Projects[0].Project, ms.Projects[1].Project)
	}
	if ms.Projects[1].Project != "old" {
		t.Fatalf("stopped-stale squad must rank last, got %s", ms.Projects[1].Project)
	}
}

// TestNOC_HeadlineSeparatesLiveFromStale asserts the headline reports live vs
// stale counts separately, and that the stale block is NOT counted as live.
func TestNOC_HeadlineSeparatesLiveFromStale(t *testing.T) {
	root, probe, _ := seedStaleFixture(t)
	ms := collectStale(t, root, probe)

	// The rollup: exactly one LIVE at-risk (live squad), zero live blocked, and
	// exactly one STALE blocked (old squad). The stale block must not leak into
	// the live AtRisk/Blocked counts.
	if ms.Rollup.AtRisk != 1 {
		t.Errorf("expected 1 LIVE at-risk, got %d (rollup=%+v)", ms.Rollup.AtRisk, ms.Rollup)
	}
	if ms.Rollup.Blocked != 0 {
		t.Errorf("expected 0 LIVE blocked (the only block is stale), got %d", ms.Rollup.Blocked)
	}
	if ms.Rollup.BlockedStale != 1 {
		t.Errorf("expected 1 STALE blocked, got %d (rollup=%+v)", ms.Rollup.BlockedStale, ms.Rollup)
	}
	if ms.LiveProjects != 1 {
		t.Errorf("expected 1 live project, got %d", ms.LiveProjects)
	}

	// Render the headline and assert the live/stale split is textually present.
	m := newNOCModel(NOCRebuildConfig{Roots: ms.Roots})
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	m.ms = ms
	m.ready = true
	pulse := m.pulseLine()

	if !strings.Contains(pulse, "1 running") {
		t.Errorf("headline should lead with '1 running':\n%s", pulse)
	}
	if strings.Contains(pulse, "at-risk(live)") {
		t.Errorf("headline should not expose at-risk(live) as primary:\n%s", pulse)
	}
	// S4b: the header counts OPERATIONAL agents in a wait state, not raw threads.
	// Both live participants own the aged at-risk review, so the live squad shows
	// "2 waiting"; the stale block (stopped squad) must not leak into waiting.
	if !strings.Contains(pulse, "2 waiting") {
		t.Errorf("headline should count the 2 operational agents waiting on the aged at-risk:\n%s", pulse)
	}
	if strings.Contains(pulse, "blocked") {
		t.Errorf("simplified header should not show a primary 'blocked' segment:\n%s", pulse)
	}
	if !strings.Contains(pulse, "1 stale") {
		t.Errorf("headline should show aggregate stale count:\n%s", pulse)
	}
}

// TestNOC_OnceRendersRollupsAndNeedsAttention asserts the --once default leads
// with a needs-attention section + project rollups (not the full firehose), and
// that --tree expands fully.
func TestNOC_OnceRendersRollupsAndNeedsAttention(t *testing.T) {
	root, probe, _ := seedStaleFixture(t)
	ms := collectStale(t, root, probe)

	m := newNOCModel(NOCRebuildConfig{Roots: ms.Roots})
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	m.ms = ms
	m.ready = true
	m.refreshGuidance()

	// Default (rollup digest): both section headers present, both squads listed.
	digest := m.staticView()
	if !strings.Contains(digest, "NEEDS ATTENTION") {
		t.Errorf("--once default should render a NEEDS ATTENTION section:\n%s", digest)
	}
	if !strings.Contains(digest, "PROJECTS") {
		t.Errorf("--once default should render a PROJECTS rollup section:\n%s", digest)
	}
	if !strings.Contains(digest, "live") || !strings.Contains(digest, "old") {
		t.Errorf("rollup digest should list both squads:\n%s", digest)
	}
	// The running at-risk squad heads the needs-attention section; the stale
	// squad collapses to the simplified stale surface, never live attention.
	if !strings.Contains(digest, "running 2/2 agents alive") {
		t.Errorf("rollup should show the unambiguous liveness phrase 'running 2/2 agents alive':\n%s", digest)
	}
	if !strings.Contains(digest, "1 stale") {
		t.Errorf("stale squad should render as simplified stale:\n%s", digest)
	}
	if strings.Contains(digest, "blocked stale") {
		t.Errorf("digest should not expose old blocked-stale detail labels:\n%s", digest)
	}
	// The digest is NOT the firehose: it must not render per-agent rows by handle.
	if strings.Contains(digest, "engine") {
		t.Errorf("rollup digest should not render the full agent detail firehose:\n%s", digest)
	}

	// --tree: the full expansion renders the tree (agent handles appear).
	m.fullTree = true
	full := m.staticView()
	if !strings.Contains(full, "cto") || !strings.Contains(full, "dev") {
		t.Errorf("--tree should fully expand to agent rows (cto/dev):\n%s", full)
	}
}

// TestNOC_NoNameSessionRendersRoot asserts a session with no name renders a
// never-blank placeholder ("(root)" for the base-root layout), never an empty
// cell.
func TestNOC_NoNameSessionRendersRoot(t *testing.T) {
	// A base-root (rootless) session: its Root is the .agent-mail container and
	// its Name is empty.
	sess := state.Session{
		Name: "",
		Root: filepath.Join("/tmp/proj", noc.AgentMailDirName),
	}
	if got := sessionLabel(sess); got != "(root)" {
		t.Errorf("base-root session label = %q, want %q", got, "(root)")
	}

	// A named-but-empty session that is NOT at the base root falls back to the
	// generic placeholder.
	sess2 := state.Session{Name: "", Root: "/tmp/proj/.agent-mail/sub"}
	if got := sessionLabel(sess2); got != "(default-session)" {
		t.Errorf("empty-name session label = %q, want %q", got, "(default-session)")
	}

	// A named session renders its name verbatim.
	sess3 := state.Session{Name: "vault", Root: "/tmp/proj/.agent-mail/vault"}
	if got := sessionLabel(sess3); got != "vault" {
		t.Errorf("named session label = %q, want %q", got, "vault")
	}
}

// TestNOC_HideStaleTogglesStaleSquads asserts the 'h' key toggles hiding
// stopped/archived (stale) squads in the interactive tree.
func TestNOC_HideStaleTogglesStaleSquads(t *testing.T) {
	root, probe, _ := seedStaleFixture(t)
	ms := collectStale(t, root, probe)

	rebuild := NOCRebuildConfig{Roots: ms.Roots, Depth: noc.DefaultDepth, Probe: probe}
	m := newNOCModel(rebuild)
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m2 := mm.(*NOCModel)
	mm, _ = m2.Update(nocSnapshotMsg{ms: ms})
	mp := mm.(*NOCModel)

	// Before: the stale "old" squad has a project node in the tree.
	hasOld := func(mm *NOCModel) bool {
		for _, n := range mm.nodes() {
			if n.kind == nodeProject && n.label == "old" {
				return true
			}
		}
		return false
	}
	if !hasOld(mp) {
		t.Fatalf("expected stale squad 'old' visible before toggle; nodes=%d", len(mp.nodes()))
	}

	// Press 'h': stale squads hide; "old" disappears, "live" remains.
	mp, _ = nocPress(mp, "h")
	if !mp.hideStale {
		t.Fatal("'h' should set hideStale")
	}
	if hasOld(mp) {
		t.Errorf("after 'h', the stopped-stale squad 'old' should be hidden:\n%v", mp.nodes())
	}
	hasLive := false
	for _, n := range mp.nodes() {
		if n.kind == nodeProject && n.label == "live" {
			hasLive = true
		}
	}
	if !hasLive {
		t.Error("the running squad 'live' must remain visible when hiding stale")
	}

	// Press 'h' again: stale squads return.
	mp, _ = nocPress(mp, "h")
	if mp.hideStale {
		t.Fatal("a second 'h' should clear hideStale")
	}
	if !hasOld(mp) {
		t.Error("a second 'h' should restore the stale squad 'old'")
	}
}
