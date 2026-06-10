package cli

import (
	"testing"

	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/state"
)

// The orchestration JSON fields are additive: orchestrated sessions carry
// orchestrated/lead/lead_handle (+ lead_down when the driver is gone) and the
// lead agent carries is_lead; flat sessions keep emitting none of them.
func TestNOCSessionEnvelopeCarriesOrchestration(t *testing.T) {
	sess := state.Session{
		Name:         "pm-copilot",
		Orchestrated: true,
		LeadRole:     "copilot",
		LeadHandle:   "copilot",
		Agents: []state.Agent{
			{Handle: "copilot", Role: "copilot", IsLead: true, Liveness: state.LivenessStale},
			{Handle: "qa", Role: "qa", Liveness: state.LivenessAlive},
		},
	}
	env := nocSessionEnvelope(noc.ProjectSnapshot{Dir: "/tmp/p"}, sess)

	if !env.Orchestrated || env.Lead != "copilot" || env.LeadHandle != "copilot" {
		t.Fatalf("session envelope missing orchestration: %+v", env)
	}
	if !env.LeadDown {
		t.Fatal("stale lead with a live child must surface lead_down")
	}
	leadSeen := false
	for _, ag := range env.Agents {
		if ag.Handle == "copilot" {
			leadSeen = true
			if !ag.IsLead {
				t.Fatal("lead agent must carry is_lead")
			}
		} else if ag.IsLead {
			t.Fatalf("non-lead agent %s carries is_lead", ag.Handle)
		}
	}
	if !leadSeen {
		t.Fatal("lead agent missing from envelope")
	}
}

func TestNOCSessionEnvelopeFlatSessionStaysClean(t *testing.T) {
	sess := state.Session{
		Name:   "flat",
		Agents: []state.Agent{{Handle: "solo", Liveness: state.LivenessAlive}},
	}
	env := nocSessionEnvelope(noc.ProjectSnapshot{Dir: "/tmp/p"}, sess)
	if env.Orchestrated || env.Lead != "" || env.LeadHandle != "" || env.LeadDown {
		t.Fatalf("flat session envelope must omit orchestration fields: %+v", env)
	}
}
