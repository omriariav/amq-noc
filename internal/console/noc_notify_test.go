package console

import (
	"strings"
	"testing"
)

// #28 regression set: the desktop notifier rides the same 0->N needs-you
// transition list as the bell (so its once-per-transition dedup is inherited
// from detectNeedsYouTransitions), is opt-in, and is INDEPENDENT of the bell
// mute.

func TestDesktopNotifyIndependentOfBellMute(t *testing.T) {
	m := newNOCModel(NOCRebuildConfig{})
	var notes []string
	m.notify = func(title, body string) { notes = append(notes, title+": "+body) }
	bells := 0
	m.bell = func() { bells++ }
	m.alertsMuted = true

	fired := m.fireNeedsYouAlerts([]needsYouAlert{{project: "os-omri-pm", session: "pm-copilot"}})

	if fired {
		t.Fatal("muted alerts must not report the banner/bell as fired")
	}
	if bells != 0 {
		t.Fatal("muted bell must stay silent")
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "os-omri-pm/pm-copilot needs you") {
		t.Fatalf("notify = %v, want one notification despite the bell mute", notes)
	}
}

func TestDesktopNotifyOffByDefault(t *testing.T) {
	m := newNOCModel(NOCRebuildConfig{})
	bells := 0
	m.bell = func() { bells++ }
	// notify is nil by default: the banner/bell path must work unchanged.
	if !m.fireNeedsYouAlerts([]needsYouAlert{{project: "p", session: "s"}}) {
		t.Fatal("unmuted alerts must fire the banner/bell")
	}
	if bells != 1 {
		t.Fatalf("bells = %d, want 1", bells)
	}
}

func TestDesktopNotifyCountsExtraTransitions(t *testing.T) {
	m := newNOCModel(NOCRebuildConfig{})
	var notes []string
	m.notify = func(title, body string) { notes = append(notes, body) }
	m.fireNeedsYouAlerts([]needsYouAlert{
		{project: "p", session: "a"},
		{project: "p", session: "b"},
	})
	if len(notes) != 1 || !strings.Contains(notes[0], "(+1 more)") {
		t.Fatalf("notes = %v, want one notification carrying the extra count", notes)
	}
}

func TestDesktopNotifyNothingWithoutTransitions(t *testing.T) {
	m := newNOCModel(NOCRebuildConfig{})
	called := false
	m.notify = func(string, string) { called = true }
	if m.fireNeedsYouAlerts(nil) {
		t.Fatal("no transitions must not fire")
	}
	if called {
		t.Fatal("no transitions must not notify")
	}
}
