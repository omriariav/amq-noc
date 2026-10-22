package console

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-noc/internal/launch"
	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/state"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files under testdata/")

// goldenNeedsYouBoard renders the interactive board at 80x24 in pure ASCII for
// one seeded needs-you fixture (session node selected): a live "ship" project with an approve ask
// addressed to the operator, plus a calm "idle" project.
func goldenNeedsYouBoard(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	ship := filepath.Join(root, "ship")
	nocSeedTeamProfile(t, ship)
	sDir := nocSeedAgent(t, ship, "main", "qa", launch.Record{Binary: "claude", AgentPID: 7001})
	nocSeedPresence(t, sDir, "qa", "active", nocTestNow.Add(-10*time.Second))
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

	rebuild := NOCRebuildConfig{Roots: []string{root}, Depth: noc.DefaultDepth, Probe: probe}
	ms := noc.Collect(rebuild.Roots, rebuild.Depth, rebuild.Probe, rebuild.Thresholds)

	base := newNOCModel(rebuild)
	base.colorMode = ColorAscii
	base.th = newNOCTheme(ColorAscii)
	mm, _ := base.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	mm, _ = mm.Update(nocSnapshotMsg{ms: ms})
	m := mm.(*NOCModel)
	m.fullTree = true
	selectKind(t, m, nodeSession, "")

	return normalizeGoldenBoard(m.View())
}

var (
	goldenANSI = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	// The fixture lives in a per-run temp dir and the right pane truncates
	// paths mid-token, so collapse every space-led absolute path to a marker.
	goldenPath = regexp.MustCompile(`(\s)/\S+`)
)

// normalizeGoldenBoard strips stray SGR resets that survive truncation in
// ASCII mode and masks machine-specific temp paths.
func normalizeGoldenBoard(view string) string {
	view = goldenANSI.ReplaceAllString(view, "")
	return goldenPath.ReplaceAllString(view, "${1}<PATH>")
}

func TestGoldenBoardNeedsYou80x24(t *testing.T) {
	got := goldenNeedsYouBoard(t)
	path := filepath.Join("testdata", "board_needs_you_80x24.golden")

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to create it): %v", err)
	}
	if got != string(want) {
		t.Fatalf("board differs from %s (run with -update to regenerate)\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}
