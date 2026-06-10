package noc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-noc/internal/state"
	"github.com/omriariav/amq-noc/internal/team"
)

func writeOrchestratedDefaultTeam(t *testing.T, dir string) {
	t.Helper()
	err := team.Write(dir, team.Team{
		Orchestrated: true,
		Lead:         "copilot",
		Members: []team.Member{
			{Role: "copilot"},
			{Role: "qa"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestApplyOrchestrationMetadata(t *testing.T) {
	dir := t.TempDir()
	writeOrchestratedDefaultTeam(t, dir)

	ps := ProjectSnapshot{
		Dir: dir,
		Snap: state.Snapshot{Sessions: []state.Session{{
			Name: "pm-copilot",
			Agents: []state.Agent{
				{Handle: "qa", Role: "qa"},
				{Handle: "copilot", Role: "copilot"},
			},
		}}},
	}
	applyOrchestrationMetadata(dir, &ps)

	sess := ps.Snap.Sessions[0]
	if !sess.Orchestrated || sess.LeadRole != "copilot" || sess.LeadHandle != "copilot" {
		t.Fatalf("session not enriched: %+v", sess)
	}
	for _, ag := range sess.Agents {
		wantLead := ag.Handle == "copilot"
		if ag.IsLead != wantLead {
			t.Fatalf("agent %s IsLead = %v, want %v", ag.Handle, ag.IsLead, wantLead)
		}
	}
}

func TestApplyOrchestrationMetadataSkipsAmbiguousProfiles(t *testing.T) {
	dir := t.TempDir()
	writeOrchestratedDefaultTeam(t, dir)

	ps := ProjectSnapshot{
		Dir: dir,
		Snap: state.Snapshot{Sessions: []state.Session{{
			Name: "mixed",
			Agents: []state.Agent{
				{Handle: "copilot", Role: "copilot", TeamProfile: "default"},
				{Handle: "qa", Role: "qa", TeamProfile: "review"},
			},
		}}},
	}
	applyOrchestrationMetadata(dir, &ps)
	if ps.Snap.Sessions[0].Orchestrated {
		t.Fatal("a session whose agents disagree on team profile must not be enriched by guesswork")
	}
}

func TestApplyOrchestrationMetadataFlatTeamUnchanged(t *testing.T) {
	dir := t.TempDir()
	if err := team.Write(dir, team.Team{Members: []team.Member{{Role: "cto"}}}); err != nil {
		t.Fatal(err)
	}
	ps := ProjectSnapshot{
		Dir: dir,
		Snap: state.Snapshot{Sessions: []state.Session{{
			Name:   "s",
			Agents: []state.Agent{{Handle: "cto", Role: "cto"}},
		}}},
	}
	applyOrchestrationMetadata(dir, &ps)
	sess := ps.Snap.Sessions[0]
	if sess.Orchestrated || sess.LeadHandle != "" || sess.Agents[0].IsLead {
		t.Fatalf("flat team must stay un-enriched: %+v", sess)
	}
}

func TestReadBriefGoal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pm-copilot.md")
	content := "# pm-copilot brief\n\n## Goal\nAlways-on PM OS co-pilot squad.\nThe lead drives specialists on demand.\n\n## Scope\nOther text.\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got := readBriefGoal(path)
	want := "Always-on PM OS co-pilot squad. The lead drives specialists on demand."
	if got != want {
		t.Fatalf("readBriefGoal = %q, want %q", got, want)
	}
	if readBriefGoal(filepath.Join(dir, "missing.md")) != "" {
		t.Fatal("missing brief must read empty")
	}
	noGoal := filepath.Join(dir, "nogoal.md")
	if err := os.WriteFile(noGoal, []byte("# brief\n\njust prose\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if readBriefGoal(noGoal) != "" {
		t.Fatal("brief without a Goal section must read empty")
	}
}

func TestReadSessionBriefGoals(t *testing.T) {
	dir := t.TempDir()
	briefs := filepath.Join(dir, SquadDirName, "briefs")
	if err := os.MkdirAll(briefs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(briefs, "ws.md"), []byte("## Goal\nShip it.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap := state.Snapshot{Sessions: []state.Session{{Name: "ws"}, {Name: "no-brief"}, {Name: ""}}}
	got := readSessionBriefGoals(dir, snap)
	if got["ws"] != "Ship it." {
		t.Fatalf("goals = %#v, want ws => Ship it.", got)
	}
	if _, ok := got["no-brief"]; ok {
		t.Fatal("session without brief must be absent")
	}
}
