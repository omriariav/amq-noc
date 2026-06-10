package state

import "testing"

func leadDownSession(leadLiveness, childLiveness Liveness) Session {
	return Session{
		Orchestrated: true,
		LeadRole:     "copilot",
		LeadHandle:   "copilot",
		Agents: []Agent{
			{Handle: "copilot", IsLead: true, Liveness: leadLiveness},
			{Handle: "qa", Liveness: childLiveness},
		},
	}
}

func TestSessionLeadDown(t *testing.T) {
	if !SessionLeadDown(leadDownSession(LivenessStale, LivenessAlive)) {
		t.Fatal("stale lead with a live child must report lead-down")
	}
	if SessionLeadDown(leadDownSession(LivenessAlive, LivenessAlive)) {
		t.Fatal("live lead must not report lead-down")
	}
	if SessionLeadDown(leadDownSession(LivenessStale, LivenessStale)) {
		t.Fatal("whole squad down is a liveness story, not lead-down")
	}

	flat := leadDownSession(LivenessStale, LivenessAlive)
	flat.Orchestrated = false
	if SessionLeadDown(flat) {
		t.Fatal("non-orchestrated session must never report lead-down")
	}

	noLeadRow := Session{
		Orchestrated: true,
		LeadRole:     "copilot",
		LeadHandle:   "copilot",
		Agents:       []Agent{{Handle: "qa", Liveness: LivenessAlive}},
	}
	if SessionLeadDown(noLeadRow) {
		t.Fatal("an unlaunched lead (no row) is a roster question, not lead-down")
	}
}
