package console

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-noc/internal/state"
)

func TestFooterHideStaleTokenReflectsCurrentDirection(t *testing.T) {
	m := newNOCModel(NOCRebuildConfig{})
	m.hideStale = true
	footer := m.footerView()
	if !strings.Contains(footer, "h show-stale") {
		t.Fatalf("hidden-stale footer should advertise show-stale:\n%s", footer)
	}
	if strings.Contains(footer, "h hide-stale") {
		t.Fatalf("hidden-stale footer must not contradict itself with hide-stale:\n%s", footer)
	}

	m.hideStale = false
	footer = m.footerView()
	if !strings.Contains(footer, "h hide-stale") {
		t.Fatalf("visible-stale footer should advertise hide-stale:\n%s", footer)
	}
}

// #4.2: the footer control legend shows only the mutating actions ACTUALLY
// available on the current selection (its begin* guard would proceed), not merely
// row-kind applicable. So availability is stateful, not just kind-based.
func TestContextFooter_AvailabilityIsStateful(t *testing.T) {
	// needs-you agent (qa in the beta fixture) -> approve/reply/deny are live.
	t.Run("needs-you agent shows approve/reply/deny", func(t *testing.T) {
		m := seededKeymapModel(t)
		selectKind(t, m, nodeAgent, "qa")
		legend := m.controlFooterLegendForSelection(false)
		for _, w := range []string{"a approve", "r reply", "x deny"} {
			if !strings.Contains(legend, w) {
				t.Errorf("needs-you agent should show %q: %q", w, legend)
			}
		}
	})

	// ordinary running agent (cto in the alpha fixture, no needs-you) -> a/r/x hidden.
	t.Run("running agent without needs-you hides approve/reply/deny", func(t *testing.T) {
		m := seededKeymapModel(t)
		selectKind(t, m, nodeAgent, "cto")
		legend := m.controlFooterLegendForSelection(false)
		for _, a := range []string{"a approve", "r reply", "x deny"} {
			if strings.Contains(legend, a) {
				t.Errorf("running agent without needs-you must not show %q: %q", a, legend)
			}
		}
	})

	// candidate / no-team project -> create-team (T) is the valid action; Del and
	// new-session are not (no configured team, no launchable profile).
	t.Run("candidate project shows new-team not delete or new-session", func(t *testing.T) {
		m := seededKeymapModel(t)
		addCandidateProject(m, "delta", "/fake/proj/delta")
		selectProject(t, m, "delta")
		legend := m.controlFooterLegendForSelection(false)
		if !strings.Contains(legend, "T new-team") {
			t.Errorf("candidate project should show 'T new-team': %q", legend)
		}
		for _, a := range []string{"Del delete", "N new-session"} {
			if strings.Contains(legend, a) {
				t.Errorf("candidate project must not show %q (no team/profile to act on): %q", a, legend)
			}
		}
	})

	t.Run("plain AMQ session shows delete", func(t *testing.T) {
		m := seededKeymapModel(t)
		m.ms.Projects[0].TeamConfigured = false
		m.ms.Projects[0].DefaultTeam = false
		m.ms.Projects[0].Profiles = nil
		if !selectFirstKind(m, nodeSession) {
			t.Fatal("fixture has no session row")
		}
		legend := m.controlFooterLegendForSelection(false)
		if !strings.Contains(legend, "Del delete") {
			t.Fatalf("plain AMQ session should show Del delete for session cleanup: %q", legend)
		}
	})

	t.Run("root AMQ session hides delete", func(t *testing.T) {
		m := seededKeymapModel(t)
		m.ms.Projects[0].TeamConfigured = false
		m.ms.Projects[0].DefaultTeam = false
		m.ms.Projects[0].Profiles = nil
		m.ms.Projects[0].Snap.Sessions[0].Name = ""
		m.ms.Projects[0].Snap.Sessions[0].Root = "/fake/proj/beta/.agent-mail"
		if !selectFirstKind(m, nodeSession) {
			t.Fatal("fixture has no session row")
		}
		legend := m.controlFooterLegendForSelection(false)
		if strings.Contains(legend, "Del delete") {
			t.Fatalf("root AMQ session must not show Del delete: %q", legend)
		}
	})
}

// #4.2: pressing a control key on a row where it does not apply must show a short
// reason (actNote), never be a silent no-op. The footer Scopes must agree with the
// begin* guards: an action the footer hides for a kind must explain itself if
// pressed anyway.
func TestContextFooter_InvalidActionShowsReasonNotSilent(t *testing.T) {
	cases := []struct {
		name string
		kind nocNodeKind
		key  string
	}{
		{"stop-on-agent", nodeAgent, "S"},
		{"delete-on-agent", nodeAgent, "delete"},
		{"drain-on-project", nodeProject, "d"},
		{"message-on-project", nodeProject, "m"},
		{"message-on-session", nodeSession, "m"},
		{"broadcast-on-agent", nodeAgent, "b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := seededKeymapModel(t)
			if !selectFirstKind(m, c.kind) {
				t.Skipf("fixture has no %s row", c.name)
			}
			m.actNote = ""
			_, handled := m.handleControlKey(c.key)
			if !handled {
				t.Fatalf("control key %q should be recognized by the router", c.key)
			}
			if strings.TrimSpace(m.actNote) == "" {
				t.Errorf("pressing %q on a %s row must show a reason in actNote, not be silent", c.key, c.name)
			}
		})
	}
}

// #4.4: the left tree shows operational shape only - one terminal line per row and
// NO thread subjects (those belong in the right detail pane).
func TestTreeIA_RowIsOneLineWithoutThreadSubject(t *testing.T) {
	m := seededKeymapModel(t)
	const secret = "SECRET-THREAD-SUBJECT-DO-NOT-LEAK"
	node := nocNode{
		kind:  nodeSession,
		label: "issue-96",
		state: nocNeedsYou,
		session: state.Session{
			Name:      "issue-96",
			Attention: state.Attention{State: state.TriageNeedsYou, Reason: state.AttnApprove},
			Agents:    []state.Agent{{Handle: "qa", Liveness: state.LivenessAlive, Attention: state.Attention{State: state.TriageNeedsYou}}},
			Coordination: state.Coordination{Threads: []state.ThreadSummary{{
				ID: "ask/x", Subject: secret, Triage: state.TriageNeedsYou,
				AttnReason: state.AttnApprove, NeedsYouOwner: "qa",
			}}},
		},
	}
	row := m.renderNode(node, false)
	if strings.Contains(row, "\n") {
		t.Errorf("tree row must be a single line, got:\n%q", row)
	}
	if strings.Contains(row, secret) {
		t.Errorf("tree row leaks the thread subject (belongs in the detail pane): %q", row)
	}
}

func selectFirstKind(m *NOCModel, kind nocNodeKind) bool {
	for i, n := range m.nodes() {
		if n.kind == kind {
			m.cursor = i
			return true
		}
	}
	return false
}
