package team

import (
	"strings"
	"testing"
)

func orchestratedTeam(lead string, members ...Member) Team {
	return Team{Orchestrated: lead != "", Lead: lead, Members: members}
}

func TestValidateOrchestrationContract(t *testing.T) {
	cto := Member{Role: "cto"}
	qa := Member{Role: "qa"}

	if err := Validate(orchestratedTeam("cto", cto, qa)); err != nil {
		t.Fatalf("valid orchestrated team rejected: %v", err)
	}
	if err := Validate(Team{Orchestrated: true, Members: []Member{cto}}); err == nil {
		t.Fatal("orchestrated without lead must be rejected")
	}
	if err := Validate(Team{Lead: "cto", Members: []Member{cto}}); err == nil {
		t.Fatal("lead without orchestrated must be rejected (no half-state)")
	}
	if err := Validate(orchestratedTeam("ghost", cto, qa)); err == nil ||
		!strings.Contains(err.Error(), "does not name a team member") {
		t.Fatalf("lead naming a non-member must be rejected, got %v", err)
	}
}

func TestLeadHandleResolvesMemberOverride(t *testing.T) {
	tm := orchestratedTeam("copilot",
		Member{Role: "copilot", Handle: "pilot"},
		Member{Role: "qa"},
	)
	if got := tm.LeadHandle(); got != "pilot" {
		t.Fatalf("LeadHandle = %q, want member handle override pilot", got)
	}
	tm.Members[0].Handle = ""
	if got := tm.LeadHandle(); got != "copilot" {
		t.Fatalf("LeadHandle = %q, want role fallback copilot", got)
	}
	if got := (Team{}).LeadHandle(); got != "" {
		t.Fatalf("LeadHandle on a flat team = %q, want empty", got)
	}
}

func TestOrchestrationRoundTripsThroughWrite(t *testing.T) {
	dir := t.TempDir()
	in := orchestratedTeam("cto", Member{Role: "cto"}, Member{Role: "qa"})
	if err := Write(dir, in); err != nil {
		t.Fatal(err)
	}
	out, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Orchestrated || out.Lead != "cto" {
		t.Fatalf("round trip lost orchestration: orchestrated=%v lead=%q", out.Orchestrated, out.Lead)
	}
}
