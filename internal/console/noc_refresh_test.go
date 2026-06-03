package console

import (
	"testing"
)

// TestRefreshNote_GSetsItSilentTickDoesNot is the QA-5 regression: pressing 'g'
// (manual refresh) must set a visible "refreshed" note so the operator sees the
// refresh happened, while the silent 2s auto-tick (nocTickMsg) must NOT set it
// (otherwise the note would flash every 2s on its own). The note also clears on
// the next keypress, like jumpNote/actNote/alertBanner.
func TestRefreshNote_GSetsItSilentTickDoesNot(t *testing.T) {
	m := newSeededNOCModel(t)

	// Precondition: no refresh note at rest.
	if m.refreshNote != "" {
		t.Fatalf("refreshNote should start empty, got %q", m.refreshNote)
	}

	// 'g' sets the visible refresh note.
	m, _ = nocPress(m, "g")
	if m.refreshNote == "" {
		t.Fatal("pressing g must set a visible refresh note")
	}

	// The next keypress clears it (acknowledged), like the other transient notes.
	m, _ = nocPress(m, "j")
	if m.refreshNote != "" {
		t.Fatalf("a subsequent keypress must clear the refresh note, got %q", m.refreshNote)
	}

	// A silent auto-tick must NOT set the refresh note (no 2s self-flash).
	mm, _ := m.Update(nocTickMsg{})
	m2 := mm.(*NOCModel)
	if m2.refreshNote != "" {
		t.Fatalf("a silent nocTickMsg must NOT set the refresh note, got %q", m2.refreshNote)
	}
}
