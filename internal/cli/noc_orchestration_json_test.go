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
		TeamProfile:  "review",
		Root:         "/tmp/p/.agent-mail/review/pm-copilot",
		Orchestrated: true,
		LeadRole:     "copilot",
		LeadHandle:   "copilot",
		Agents: []state.Agent{
			{Handle: "copilot", Role: "copilot", IsLead: true, Liveness: state.LivenessStale, LaunchRecordPath: "/tmp/p/.agent-mail/review/pm-copilot/agents/copilot/extensions/io.github.omriariav.amq-squad/launch.json"},
			{Handle: "qa", Role: "qa", Liveness: state.LivenessAlive, TeamProfile: "review"},
		},
	}
	env := nocSessionEnvelope(noc.ProjectSnapshot{Dir: "/tmp/p"}, sess)

	if !env.Orchestrated || env.Lead != "copilot" || env.LeadHandle != "copilot" {
		t.Fatalf("session envelope missing orchestration: %+v", env)
	}
	if env.ID != "session|/tmp/p|review/pm-copilot" || env.Profile != "review" {
		t.Fatalf("session envelope must carry profile-safe id/profile: %+v", env)
	}
	if env.Namespace.ID != "review/pm-copilot" || env.Namespace.Display != "review/pm-copilot" ||
		env.Namespace.AMQRoot != "/tmp/p/.agent-mail/review/pm-copilot" ||
		env.Namespace.Paths.Brief != "/tmp/p/.amq-squad/briefs/review/pm-copilot.md" ||
		env.Namespace.Paths.Tasks != "/tmp/p/.amq-squad/tasks/review/pm-copilot" {
		t.Fatalf("session envelope namespace mismatch: %+v", env.Namespace)
	}
	if env.GoalBinding.Mode != "amq_task_brief" || env.GoalBinding.NativeGoal || env.GoalBinding.BriefPath == "" || env.GoalBinding.TasksPath == "" {
		t.Fatalf("session envelope fallback goal binding mismatch: %+v", env.GoalBinding)
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
			if ag.ID != "agent|/tmp/p|review/pm-copilot|copilot" || ag.TeamProfile != "review" {
				t.Fatalf("lead agent must carry profile-safe id/profile: %+v", ag)
			}
			if ag.Namespace.ID != "review/pm-copilot" || ag.Namespace.Paths.LaunchRecord != ag.LaunchRecord || ag.LaunchRecord == "" {
				t.Fatalf("lead agent namespace/launch record mismatch: %+v", ag)
			}
		} else if ag.IsLead {
			t.Fatalf("non-lead agent %s carries is_lead", ag.Handle)
		}
	}
	if !leadSeen {
		t.Fatal("lead agent missing from envelope")
	}
	foundProfileScopedAction := false
	for _, action := range env.Actions {
		if action.Name == "resume" && action.ID == "session|/tmp/p|review/pm-copilot|action|resume" {
			if action.NamespaceID != "review/pm-copilot" {
				t.Fatalf("resume action namespace_id = %q", action.NamespaceID)
			}
			foundProfileScopedAction = true
		}
	}
	if !foundProfileScopedAction {
		t.Fatalf("session actions must use profile-safe IDs: %+v", env.Actions)
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
