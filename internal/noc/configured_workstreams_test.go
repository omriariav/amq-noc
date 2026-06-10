package noc

import (
	"reflect"
	"testing"

	"github.com/omriariav/amq-noc/internal/team"
)

// readConfiguredWorkstreams feeds the #22 fix: launch flows lead with the
// team's configured workstream instead of free-asking the operator. The
// derivation rules under test: distinct member session hints per profile,
// sorted; the deprecated team-level workstream shim only when no member
// carries a hint; profiles configuring nothing stay absent from the map.
func TestReadConfiguredWorkstreams(t *testing.T) {
	proj := t.TempDir()

	if err := team.Write(proj, team.Team{Members: []team.Member{
		{Role: "cto", Session: "pm-copilot"},
		{Role: "qa", Session: "pm-copilot"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := team.WriteProfile(proj, "split", team.Team{Members: []team.Member{
		{Role: "cto", Session: "ws-b"},
		{Role: "qa", Session: "ws-a"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := team.WriteProfile(proj, "shim", team.Team{
		Workstream: "legacy-ws",
		Members:    []team.Member{{Role: "cto"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := team.WriteProfile(proj, "bare", team.Team{
		Members: []team.Member{{Role: "cto"}},
	}); err != nil {
		t.Fatal(err)
	}

	got := readConfiguredWorkstreams(proj, []string{"default", "split", "shim", "bare", "missing"})

	want := map[string][]string{
		"default": {"pm-copilot"},
		"split":   {"ws-a", "ws-b"},
		"shim":    {"legacy-ws"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("readConfiguredWorkstreams = %#v, want %#v", got, want)
	}
	if _, ok := got["bare"]; ok {
		t.Fatal("a profile with no configured workstream must stay absent")
	}
}

// A member session hint outranks the deprecated team-level shim, matching
// amq-squad's own resolution precedence.
func TestReadConfiguredWorkstreamsMemberHintBeatsShim(t *testing.T) {
	proj := t.TempDir()
	if err := team.Write(proj, team.Team{
		Workstream: "legacy-ws",
		Members:    []team.Member{{Role: "cto", Session: "pm-copilot"}},
	}); err != nil {
		t.Fatal(err)
	}
	got := readConfiguredWorkstreams(proj, []string{"default"})
	want := map[string][]string{"default": {"pm-copilot"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("readConfiguredWorkstreams = %#v, want %#v", got, want)
	}
}
