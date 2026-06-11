package console

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-noc/internal/noc"
)

// The keymap (noc_keymap.go) is the single source of truth: every key it
// advertises must be handled by the router, and keys the 0.1.0 prune removed
// (jump J/o, mute A, context/DLQ/inbox c/D/i, timeline t) must NOT be
// handled. (p was re-wired in v0.5.0 to send-prompt; v was re-wired in v0.8.0
// to the read-only view-output affordance (#21). Both are now advertised +
// handled, not dead.) These tests are the permanent guard against issue #4.1
// drift.

// CONTROL keys go through handleControlKey, which returns a handled bool.
func TestKeymapContract_ControlKeysAdvertisedEqualHandled(t *testing.T) {
	m := newControlTestModel(t)
	for _, key := range nocKeymapKeys(keyGroupAction) {
		if _, handled := m.handleControlKey(key); !handled {
			t.Errorf("manifest control key %q is advertised but NOT handled by handleControlKey", key)
		}
	}
	// "v" and "o" left this list in 0.8.0: v re-wired as the read-only
	// view-output affordance (#21), o as the read-only pane focus (#25); both
	// are advertised in nocKeyMap.
	for _, dead := range []string{"J", "A", "c", "D", "i", "t"} {
		if _, handled := m.handleControlKey(dead); handled {
			t.Errorf("removed key %q is still handled; re-wire it AND add it to nocKeyMap, or leave it removed", dead)
		}
	}
}

// NAVIGATION + VIEW keys go through handleKey (no handled bool), so the test
// drives the full Update and treats a key as handled when it changes the rendered
// board OR issues a command. Removed keys must be inert no-ops.
func TestKeymapContract_NavViewKeysHandled(t *testing.T) {
	for _, key := range append(nocKeymapKeys(keyGroupNav), nocKeymapKeys(keyGroupView)...) {
		if !keyHasEffect(t, key) {
			t.Errorf("nav/view key %q is advertised but appears NOT handled (no visible change, no command)", key)
		}
	}
	for _, dead := range []string{"J", "A", "c", "D", "i", "t"} {
		if keyHasEffect(t, dead) {
			t.Errorf("removed key %q still has an effect; it must be a no-op (or re-wired and added to nocKeyMap)", dead)
		}
	}
}

// The rendered footer/help legends derive from the manifest, so they must carry
// every manifest footer token and no removed-surface phrasing.
func TestKeymapContract_FooterAndHelpRenderFromManifest(t *testing.T) {
	footer := nocFooterNavLegend(false) + "\n" + nocFooterNavLegend(true) +
		"\n" + controlFooterKeys(false) + "\n" + controlFooterKeys(true)
	combined := footer + "\n" + strings.Join(controlHelpLines(), "\n")

	for _, b := range nocKeyMap {
		if b.Footer == "" {
			continue
		}
		if !strings.Contains(footer, b.Footer) {
			t.Errorf("footer legend missing manifest token %q", b.Footer)
		}
	}
	for _, dead := range []string{"palette", "PALETTE", "timeline", "jump", " J ", "mute"} {
		if strings.Contains(combined, dead) {
			t.Errorf("footer/help still advertises removed surface %q:\n%s", dead, combined)
		}
	}
}

// keyHasEffect presses key on a freshly seeded board and reports whether it
// changed any observable model state (cursor, toggles, filter, tree expansion, or
// the rendered view) or issued a command. A removed key changes nothing.
func keyHasEffect(t *testing.T, key string) bool {
	t.Helper()
	m := seededKeymapModel(t)
	m.cursor = 1 // sit mid-tree so up AND down both move (no boundary no-op)
	before := navSig(m)
	mm, cmd := m.Update(keyMsg(key))
	return cmd != nil || navSig(mm.(*NOCModel)) != before
}

// navSig is a full observable-state signature of the model.
func navSig(m *NOCModel) string {
	return fmt.Sprintf("cur=%d help=%v flow=%v cmd=%v hide=%v editing=%v filter=%q tree=%v|%v\n%s",
		m.cursor, m.showHelp, m.showFlow, m.commandPicker != nil, m.hideStale, m.filterEditing, m.filter,
		m.tree.collapsed, m.tree.expanded, m.staticView())
}

func newControlTestModel(t *testing.T) *NOCModel {
	t.Helper()
	m := newNOCModel(NOCRebuildConfig{})
	m.th = newNOCTheme(ColorNone)
	return &m
}

func seededKeymapModel(t *testing.T) *NOCModel {
	t.Helper()
	root, probe := seedNOCFixture(t)
	rebuild := NOCRebuildConfig{Roots: []string{root}, Depth: noc.DefaultDepth, Probe: probe}
	ms := noc.Collect(rebuild.Roots, rebuild.Depth, rebuild.Probe, rebuild.Thresholds)
	m := newNOCModel(rebuild)
	m.th = newNOCTheme(ColorNone)
	m.colorMode = ColorNone
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	m = *mm.(*NOCModel)
	mm, _ = m.Update(nocSnapshotMsg{ms: ms})
	out := mm.(*NOCModel)
	out.fullTree = true
	return out
}
