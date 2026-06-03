package console

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/state"
)

// alertSnapshot builds a one-project / one-session MultiSnapshot whose session
// "beta" carries the given needs-you count. It is the controlled delta the alert
// tests feed: snapshot A (needsYou=0) then snapshot B (needsYou=1) to drive a
// 0→N transition.
func alertSnapshot(needsYou int) noc.MultiSnapshot {
	sess := state.Session{
		Name:   "beta",
		Root:   "/fake/proj/beta/.agent-mail",
		Agents: []state.Agent{{Handle: "cto", Role: "cto", Engine: "codex", Liveness: state.LivenessAlive}},
		Rollup: state.TriageRollup{NeedsYou: needsYou},
	}
	ps := noc.ProjectSnapshot{
		Project: "beta",
		Dir:     "/fake/proj/beta",
		Snap:    state.Snapshot{Sessions: []state.Session{sess}},
	}
	return noc.MultiSnapshot{Roots: []string{"/fake/proj"}, Projects: []noc.ProjectSnapshot{ps}}
}

// newAlertModel builds a ready *NOCModel with a counting bell seam and a clean
// prior-needs-you map, primed with snapshot A (needsYou=0) so the FIRST graded
// transition is the test's snapshot B. Returns the model and a pointer to the
// bell counter.
func newAlertModel(t *testing.T) (*NOCModel, *int) {
	t.Helper()
	m := newNOCModel(NOCRebuildConfig{Roots: []string{"/fake/proj"}})
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	m.panes = func() ([]noc.TmuxPane, error) { return nil, nil }
	m.switchTo = func(noc.TmuxTarget) error { return nil }

	bells := 0
	m.bell = func() { bells++ }

	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m2 := mm.(*NOCModel)
	// Snapshot A: needs-you = 0. This primes priorNeedsYou["beta"]=0 WITHOUT a
	// transition (prior was absent==0, now 0).
	mm, _ = m2.Update(nocSnapshotMsg{ms: alertSnapshot(0)})
	m3 := mm.(*NOCModel)
	if bells != 0 {
		t.Fatalf("snapshot A (needsYou=0) must not ring the bell, rang %d", bells)
	}
	return m3, &bells
}

// TestAlerts_BellFiresOnceOnTransition proves a session going 0→1 needs-you
// between snapshots rings the bell EXACTLY once and sets the banner.
func TestAlerts_BellFiresOnceOnTransition(t *testing.T) {
	m, bells := newAlertModel(t)

	// Snapshot B: needs-you = 1. The 0→1 transition must alert.
	mm, _ := m.Update(nocSnapshotMsg{ms: alertSnapshot(1)})
	m = mm.(*NOCModel)

	if *bells != 1 {
		t.Fatalf("a 0->1 needs-you transition should ring the bell once, rang %d", *bells)
	}
	if !strings.Contains(m.alertBanner, "beta") || !strings.Contains(m.alertBanner, "needs you") {
		t.Errorf("transition should set the needs-you banner, got %q", m.alertBanner)
	}
}

// TestAlerts_NoReFireWhileStaying proves the bell does NOT ring again while the
// session STAYS in needs-you across subsequent refreshes (transition-only, no
// spam).
func TestAlerts_NoReFireWhileStaying(t *testing.T) {
	m, bells := newAlertModel(t)

	// B: 0->1 fires once.
	mm, _ := m.Update(nocSnapshotMsg{ms: alertSnapshot(1)})
	m = mm.(*NOCModel)
	if *bells != 1 {
		t.Fatalf("first transition should ring once, rang %d", *bells)
	}

	// Feed B again (still needs-you = 1): NO new bell — it stayed needs-you.
	mm, _ = m.Update(nocSnapshotMsg{ms: alertSnapshot(1)})
	m = mm.(*NOCModel)
	if *bells != 1 {
		t.Fatalf("staying in needs-you must NOT re-ring the bell (transition-only), rang %d", *bells)
	}

	// And once more, still 1.
	mm, _ = m.Update(nocSnapshotMsg{ms: alertSnapshot(1)})
	m = mm.(*NOCModel)
	if *bells != 1 {
		t.Fatalf("a third identical needs-you snapshot must still not re-ring, rang %d", *bells)
	}
}

// TestAlerts_ReFiresAfterDropAndRise proves the alert may fire AGAIN when the
// session drops back to 0 and rises to needs-you once more (a NEW 0→N
// transition).
func TestAlerts_ReFiresAfterDropAndRise(t *testing.T) {
	m, bells := newAlertModel(t)

	mm, _ := m.Update(nocSnapshotMsg{ms: alertSnapshot(1)}) // 0->1 fires
	m = mm.(*NOCModel)
	mm, _ = m.Update(nocSnapshotMsg{ms: alertSnapshot(0)}) // 1->0, no fire (resolved)
	m = mm.(*NOCModel)
	if *bells != 1 {
		t.Fatalf("dropping back to 0 must not ring, rang %d", *bells)
	}
	mm, _ = m.Update(nocSnapshotMsg{ms: alertSnapshot(1)}) // 0->1 again: fires
	m = mm.(*NOCModel)
	if *bells != 2 {
		t.Fatalf("a fresh 0->1 transition should ring again, total rang %d (want 2)", *bells)
	}
}

// TestAlerts_MuteSuppressesViaKey proves the interactive 'A' mute suppresses BOTH
// the bell and the banner on a 0→N transition.
func TestAlerts_MuteSuppressesViaKey(t *testing.T) {
	m, bells := newAlertModel(t)

	// Mute via the 'A' key.
	m, _ = nocPress(m, "A")
	if !m.alertsMuted {
		t.Fatal("A should mute alerts")
	}

	mm, _ := m.Update(nocSnapshotMsg{ms: alertSnapshot(1)})
	m = mm.(*NOCModel)
	if *bells != 0 {
		t.Errorf("muted: a transition must NOT ring the bell, rang %d", *bells)
	}
	if strings.TrimSpace(m.alertBanner) != "" {
		t.Errorf("muted: a transition must NOT set the banner, got %q", m.alertBanner)
	}

	// Unmute and the next transition fires again (drop to 0 first, then rise).
	m, _ = nocPress(m, "A")
	if m.alertsMuted {
		t.Fatal("A should unmute alerts")
	}
	mm, _ = m.Update(nocSnapshotMsg{ms: alertSnapshot(0)})
	m = mm.(*NOCModel)
	mm, _ = m.Update(nocSnapshotMsg{ms: alertSnapshot(1)})
	m = mm.(*NOCModel)
	if *bells != 1 {
		t.Errorf("unmuted: a fresh transition should ring, rang %d", *bells)
	}
}

// TestAlerts_NoBellFlagStartsMuted proves --no-bell (modeled as alertsMuted set
// at startup) suppresses the alert without any key press.
func TestAlerts_NoBellFlagStartsMuted(t *testing.T) {
	m, bells := newAlertModel(t)
	// Simulate the --no-bell startup mute (RunNOC sets m.alertsMuted = cfg.NoBell).
	m.alertsMuted = true

	mm, _ := m.Update(nocSnapshotMsg{ms: alertSnapshot(1)})
	m = mm.(*NOCModel)
	if *bells != 0 {
		t.Errorf("--no-bell should suppress the bell, rang %d", *bells)
	}
	if strings.TrimSpace(m.alertBanner) != "" {
		t.Errorf("--no-bell should suppress the banner, got %q", m.alertBanner)
	}
}

// TestAlerts_NilBellDegradesGracefully proves a nil bell seam (banner-only) does
// not panic and still sets the banner.
func TestAlerts_NilBellDegradesGracefully(t *testing.T) {
	m, _ := newAlertModel(t)
	m.bell = nil // banner-only degrade

	mm, _ := m.Update(nocSnapshotMsg{ms: alertSnapshot(1)})
	m = mm.(*NOCModel)
	if !strings.Contains(m.alertBanner, "needs you") {
		t.Errorf("nil bell should still set the banner, got %q", m.alertBanner)
	}
}

// TestAlerts_BannerClearedOnKeypress proves the banner is acknowledged (cleared)
// by any keypress, mirroring jumpNote/actNote, so it does not linger forever.
func TestAlerts_BannerClearedOnKeypress(t *testing.T) {
	m, _ := newAlertModel(t)
	mm, _ := m.Update(nocSnapshotMsg{ms: alertSnapshot(1)})
	m = mm.(*NOCModel)
	if strings.TrimSpace(m.alertBanner) == "" {
		t.Fatal("transition should set a banner to clear")
	}
	m, _ = nocPress(m, "j") // any nav key acknowledges
	if strings.TrimSpace(m.alertBanner) != "" {
		t.Errorf("a keypress should clear the alert banner, got %q", m.alertBanner)
	}
}
