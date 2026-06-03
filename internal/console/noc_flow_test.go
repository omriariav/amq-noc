package console

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/state"
)

// flowTestSession builds a session whose coordination carries an inter-agent
// edge list plus a blocked thread, so the flow graph has rows to render with a
// status marker.
func flowTestSession() state.Session {
	return state.Session{
		Name: "alpha",
		Coordination: state.Coordination{
			Edges: []state.Edge{
				{From: "cpo", To: "cto", Count: 4},
				{From: "qa", To: "senior", Count: 1},
			},
			Threads: []state.ThreadSummary{
				{ID: "alpha/block", Participants: []string{"qa", "senior"}, Status: state.ThreadBlocked},
			},
		},
	}
}

// flowTestModel returns a model in full color mode + a session node carrying the
// edge list, so sessionDetail renders the flow sub-panel when showFlow is on.
func flowTestModel() (NOCModel, nocNode) {
	m := newNOCModel(NOCRebuildConfig{})
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	sess := flowTestSession()
	n := nocNode{
		kind:    nodeSession,
		label:   sess.Name,
		session: sess,
		project: noc.ProjectSnapshot{Project: "proj"},
	}
	return m, n
}

// TestSessionDetailFlowToggle: with the flow toggle on, the session detail pane
// shows a flow-graph section with "from → to  Nmsgs" rows and a blocked marker.
func TestSessionDetailFlowToggle(t *testing.T) {
	m, n := flowTestModel()

	// Off by default: no flow section.
	if got := m.sessionDetail(n); strings.Contains(got, "flow graph") {
		t.Fatalf("flow graph should be hidden by default, got:\n%s", got)
	}

	m.showFlow = true
	got := m.sessionDetail(n)
	if !strings.Contains(got, "flow graph") {
		t.Fatalf("expected 'flow graph' section when toggled, got:\n%s", got)
	}
	if !strings.Contains(got, "qa → senior") {
		t.Fatalf("expected 'qa → senior' edge row, got:\n%s", got)
	}
	if !strings.Contains(got, "cpo → cto") || !strings.Contains(got, "4 msgs") {
		t.Fatalf("expected 'cpo → cto  4 msgs' edge row, got:\n%s", got)
	}
	// The blocked link is marked as TEXT, not color alone.
	if !strings.Contains(got, "[blocked]") {
		t.Fatalf("expected [blocked] marker on qa → senior, got:\n%s", got)
	}
	// Blocked link sorts before the higher-volume plain link.
	if strings.Index(got, "qa → senior") > strings.Index(got, "cpo → cto") {
		t.Fatalf("expected blocked qa → senior before cpo → cto, got:\n%s", got)
	}
}

// TestSessionDetailFlowEmpty: a session with no edges renders the explicit
// no-messages line under the flow-graph section.
func TestSessionDetailFlowEmpty(t *testing.T) {
	m := newNOCModel(NOCRebuildConfig{})
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	m.showFlow = true
	n := nocNode{kind: nodeSession, label: "empty", session: state.Session{Name: "empty"}, project: noc.ProjectSnapshot{Project: "proj"}}

	got := m.sessionDetail(n)
	if !strings.Contains(got, "flow graph") {
		t.Fatalf("expected 'flow graph' section, got:\n%s", got)
	}
	if !strings.Contains(got, "(no inter-agent messages yet)") {
		t.Fatalf("expected empty-flow line, got:\n%s", got)
	}
}

// TestSessionDetailFlowAndTimelineCoexist: the flow and timeline toggles are
// independent — both sub-panels render at once, flow leading the timeline.
func TestSessionDetailFlowAndTimelineCoexist(t *testing.T) {
	m, n := flowTestModel()
	c := n.session.Coordination
	c.Timeline = []state.TimelineEvent{{Summary: "qa blocked on senior"}}
	n.session.Coordination = c

	m.showFlow = true
	m.showTimeline = true
	got := m.sessionDetail(n)
	if !strings.Contains(got, "flow graph") || !strings.Contains(got, "recent") {
		t.Fatalf("expected both flow-graph and recent (timeline) sections, got:\n%s", got)
	}
	if strings.Index(got, "flow graph") > strings.Index(got, "recent") {
		t.Fatalf("expected flow before timeline, got:\n%s", got)
	}
}

// TestProjectDetailFlowToggle: a project node also renders the flow graph (its
// first session's edges) when the toggle is on.
func TestProjectDetailFlowToggle(t *testing.T) {
	m := newNOCModel(NOCRebuildConfig{})
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	m.showFlow = true
	ps := noc.ProjectSnapshot{
		Project: "proj",
		Dir:     "/proj",
		Snap:    state.Snapshot{Sessions: []state.Session{flowTestSession()}},
	}
	n := nocNode{kind: nodeProject, label: "proj", project: ps}

	got := m.projectDetail(n)
	if !strings.Contains(got, "flow graph") {
		t.Fatalf("expected flow-graph section on project node, got:\n%s", got)
	}
	if !strings.Contains(got, "qa → senior") || !strings.Contains(got, "[blocked]") {
		t.Fatalf("expected the blocked edge row on the project flow graph, got:\n%s", got)
	}
}

func TestProjectDetailShowsAMQBaseRoot(t *testing.T) {
	m := newNOCModel(NOCRebuildConfig{})
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	ps := noc.ProjectSnapshot{
		Project:      "proj",
		Dir:          "/proj",
		SessionStore: true,
		Snap: state.Snapshot{
			BaseRoot: "/proj/.agent-mail",
			Sessions: []state.Session{{
				Name: "alpha",
			}},
		},
	}

	got := m.projectDetail(nocNode{kind: nodeProject, label: "proj", project: ps})
	for _, want := range []string{"amq base root", "/proj/.agent-mail"} {
		if !strings.Contains(got, want) {
			t.Fatalf("project detail missing %q:\n%s", want, got)
		}
	}
}

// TestFlowGraphAsciiArrow: in the ascii color mode the directed edge renders as
// "->" (not "→"), and the status marker stays TEXT.
func TestFlowGraphAsciiArrow(t *testing.T) {
	m := newNOCModel(NOCRebuildConfig{})
	m.colorMode = ColorAscii
	m.th = newNOCTheme(ColorAscii)
	m.showFlow = true
	n := nocNode{kind: nodeSession, label: "alpha", session: flowTestSession(), project: noc.ProjectSnapshot{Project: "proj"}}

	got := m.sessionDetail(n)
	if !strings.Contains(got, "qa -> senior") {
		t.Fatalf("expected ascii arrow 'qa -> senior', got:\n%s", got)
	}
	if strings.Contains(got, "→") {
		t.Fatalf("expected no unicode arrow in ascii mode, got:\n%s", got)
	}
	if !strings.Contains(got, "[blocked]") {
		t.Fatalf("expected [blocked] text marker in ascii mode, got:\n%s", got)
	}
}

// TestFlowKeyTogglesModel: pressing 'f' through the PUBLIC Update flips showFlow
// on, then off — the Update-level toggle, sibling to the 't' timeline toggle.
func TestFlowKeyTogglesModel(t *testing.T) {
	m := newSeededNOCModel(t)
	if m.showFlow {
		t.Fatalf("showFlow should start false")
	}
	m, _ = nocPress(m, "f")
	if !m.showFlow {
		t.Fatalf("expected 'f' to enable the flow graph")
	}
	m, _ = nocPress(m, "f")
	if m.showFlow {
		t.Fatalf("expected second 'f' to disable the flow graph")
	}
}
