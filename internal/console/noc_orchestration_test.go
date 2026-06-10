package console

import (
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/state"
)

func orchestratedTestSession() state.Session {
	base := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	return state.Session{
		Name:         "pm-copilot",
		Orchestrated: true,
		LeadRole:     "copilot",
		LeadHandle:   "copilot",
		Agents: []state.Agent{
			{Handle: "copilot", Role: "copilot", IsLead: true, Liveness: state.LivenessAlive},
			{Handle: "qa", Role: "qa", Liveness: state.LivenessAlive},
			{Handle: "scribe", Role: "scribe", Liveness: state.LivenessAlive},
		},
		Coordination: state.Coordination{Threads: []state.ThreadSummary{
			{
				ID:           "p2p/copilot__qa",
				Participants: []string{"copilot", "qa"},
				Subject:      "diff ready on branch x",
				Kind:         state.Kind("review_request"),
				LastFrom:     "qa",
				LastEventAt:  base.Add(30 * time.Minute),
			},
			{
				ID:           "p2p/copilot__qa",
				Participants: []string{"copilot", "qa"},
				Subject:      "older exchange",
				LastEventAt:  base,
			},
			{
				ID:           "p2p/copilot__scribe",
				Participants: []string{"copilot", "scribe"},
				Subject:      "docs updated",
				Kind:         state.Kind("status"),
				LastFrom:     "copilot",
				LastEventAt:  base.Add(10 * time.Minute),
			},
			{
				ID:           "decision/storage",
				Participants: []string{"copilot", "qa"},
				Subject:      "not a p2p thread",
				LastEventAt:  base.Add(2 * time.Hour),
			},
		}},
	}
}

func TestLeadExchangesNewestPerChild(t *testing.T) {
	got := leadExchanges(orchestratedTestSession())
	if len(got) != 2 {
		t.Fatalf("exchanges = %d, want 2 (one per child, p2p only)", len(got))
	}
	if got[0].child != "qa" || got[0].thread.Subject != "diff ready on branch x" {
		t.Fatalf("first exchange = %s/%q, want qa newest", got[0].child, got[0].thread.Subject)
	}
	if got[1].child != "scribe" {
		t.Fatalf("second exchange child = %s, want scribe", got[1].child)
	}
}

func TestLeadExchangesEmptyForFlatSession(t *testing.T) {
	sess := orchestratedTestSession()
	sess.Orchestrated = false
	sess.LeadHandle = ""
	if got := leadExchanges(sess); got != nil {
		t.Fatalf("flat session exchanges = %v, want none", got)
	}
}

func TestSessionDetailRendersOrchestration(t *testing.T) {
	m := newNOCModel(NOCRebuildConfig{})
	sess := orchestratedTestSession()
	ps := noc.ProjectSnapshot{
		Project:           "os-omri-pm",
		Dir:               "/tmp/os-omri-pm",
		SessionBriefGoals: map[string]string{"pm-copilot": "Always-on PM OS co-pilot squad."},
	}
	m.ms = noc.MultiSnapshot{
		Projects:   []noc.ProjectSnapshot{ps},
		ObservedAt: time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC),
	}
	view := m.sessionDetail(nocNode{kind: nodeSession, label: sessionLabel(sess), project: ps, session: sess})

	for _, want := range []string{
		"lead: copilot",
		"goal: Always-on PM OS co-pilot squad.",
		"lead exchanges: newest per child",
		"qa: review_request diff ready on branch x",
		"copilot (lead)",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("session detail missing %q in:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "lead replied last") {
		t.Fatalf("scribe exchange should be marked lead-replied, got:\n%s", view)
	}
}

func TestSessionDetailRendersLeadDownWarning(t *testing.T) {
	m := newNOCModel(NOCRebuildConfig{})
	sess := orchestratedTestSession()
	sess.Agents[0].Liveness = state.LivenessStale
	m.ms = noc.MultiSnapshot{ObservedAt: time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)}
	view := m.sessionDetail(nocNode{kind: nodeSession, label: sessionLabel(sess), project: noc.ProjectSnapshot{}, session: sess})
	if !strings.Contains(view, "lead is DOWN; children continue") {
		t.Fatalf("expected lead-down warning, got:\n%s", view)
	}
}

func TestTreeLabelsCarryOrchestrationBadges(t *testing.T) {
	sess := orchestratedTestSession()
	if got := sessionLabel(sess); got != "pm-copilot (orchestrated)" {
		t.Fatalf("sessionLabel = %q", got)
	}
	if got := agentLabel(sess.Agents[0]); got != "copilot (lead)" {
		t.Fatalf("lead agentLabel = %q", got)
	}
	if got := agentLabel(sess.Agents[1]); got != "qa" {
		t.Fatalf("child agentLabel = %q", got)
	}
}

func TestSortedAgentsLeadFirstWithinTier(t *testing.T) {
	sess := orchestratedTestSession()
	got := sortedAgentsForSession(sess)
	if !got[0].IsLead {
		t.Fatalf("lead should sort first within its tier, got %s", got[0].Handle)
	}
	// Attention still outranks the lead: give qa a needs-you tier.
	sess.Agents[1].Attention = state.Attention{State: state.TriageNeedsYou}
	got = sortedAgentsForSession(sess)
	if got[0].Handle != "qa" {
		t.Fatalf("needs-you child must outrank the lead, got %s first", got[0].Handle)
	}
}

func TestNOCFilterOrchestratedAndLead(t *testing.T) {
	sess := orchestratedTestSession()
	flat := state.Session{Name: "flat", Agents: []state.Agent{{Handle: "solo"}}}

	if !SessionMatchesNOCFilter(sess, "orchestrated") {
		t.Fatal("orchestrated session must match the orchestrated filter")
	}
	if SessionMatchesNOCFilter(flat, "orchestrated") {
		t.Fatal("flat session must not match the orchestrated filter")
	}
	if !SessionMatchesNOCFilter(sess, "lead:copilot") {
		t.Fatal("lead:copilot must match the session lead")
	}
	if SessionMatchesNOCFilter(sess, "lead:ghost") {
		t.Fatal("lead:ghost must not match")
	}

	ps := noc.ProjectSnapshot{Project: "p", Snap: state.Snapshot{Sessions: []state.Session{sess}}}
	if !ProjectMatchesNOCFilter(ps, "orchestrated") {
		t.Fatal("project with an orchestrated session must match")
	}
	for _, ag := range sess.Agents {
		if !AgentMatchesNOCProjectFilter(ps, sess, ag, "orchestrated") {
			t.Fatalf("agent %s under an orchestrated session must pass the filter", ag.Handle)
		}
	}
}
