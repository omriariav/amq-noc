package noc

import (
	"errors"
	"reflect"
	"testing"
)

const sampleStatusJSON = `{"schema_version":1,"kind":"status","data":{
  "capabilities":{"operator_gates":true},
  "records":[
    {"role":"cto","handle":"cto","tmux":{"session":"main","window_name":"squad","pane_id":"%1","pane_alive":true},
     "actions":[
       {"kind":"focus","command":"amq-squad focus --session s --role cto","available":true},
       {"kind":"send","command":"amq-squad send --session s --role cto --body-file -","available":true},
       {"kind":"resume","command":"amq-squad resume --session s --role cto","available":true},
       {"kind":"status","command":"amq-squad status --session s --role cto","available":true}
     ]},
    {"role":"qa","handle":"qa","tmux":null,
     "actions":[
       {"kind":"focus","command":"amq-squad focus --session s --role qa","available":false},
       {"kind":"resume","command":"amq-squad resume --session s --role qa","available":true}
     ]}
  ]}}`

func TestFetchRuntimeStatusParsesContract(t *testing.T) {
	var gotArgs []string
	run := func(dir string, args ...string) ([]byte, error) {
		gotArgs = args
		return []byte(sampleStatusJSON), nil
	}
	rs := FetchRuntimeStatus(run, "/repo", "review", "issue-96")

	wantArgs := []string{"status", "--project", "/repo", "--session", "issue-96", "--json", "--profile", "review"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args = %v, want %v", gotArgs, wantArgs)
	}
	// v1.5.0 ships actions without advertising the capability flag.
	if rs.Advertised {
		t.Error("Advertised should be false when capabilities omit runtime_actions")
	}
	if !rs.HasActions() {
		t.Fatal("HasActions should be true when records carry actions")
	}
	if len(rs.Members) != 2 {
		t.Fatalf("want 2 members, got %d", len(rs.Members))
	}
	cto, ok := rs.MemberByRole("CTO") // case-insensitive
	if !ok || cto.PaneID != "%1" || !cto.PaneAlive || len(cto.Actions) != 4 {
		t.Fatalf("cto member parsed wrong: %+v ok=%v", cto, ok)
	}
	if cto.Session != "main" || cto.WindowName != "squad" {
		t.Errorf("cto tmux session/window not parsed: %+v", cto)
	}
	if !cto.Actions[0].Available || cto.Actions[0].Kind != "focus" {
		t.Errorf("cto focus action wrong: %+v", cto.Actions[0])
	}
	qa, _ := rs.MemberByRole("qa")
	if qa.PaneAlive {
		t.Error("qa pane_alive should be false (tmux:null)")
	}
	if qa.Actions[0].Available {
		t.Error("qa focus should be unavailable (dead pane)")
	}
}

func TestFetchRuntimeStatusAdvertisedFlag(t *testing.T) {
	run := func(string, ...string) ([]byte, error) {
		return []byte(`{"kind":"status","data":{"capabilities":{"runtime_actions":true},"records":[]}}`), nil
	}
	if rs := FetchRuntimeStatus(run, "/r", "", "s"); !rs.Advertised {
		t.Error("Advertised should be true when capabilities.runtime_actions is set")
	}
}

func TestFetchRuntimeStatusDefaultProfileOmitsFlag(t *testing.T) {
	var gotArgs []string
	run := func(dir string, args ...string) ([]byte, error) {
		gotArgs = args
		return []byte(`{"kind":"status","data":{"records":[]}}`), nil
	}
	for _, p := range []string{"", "default"} {
		FetchRuntimeStatus(run, "/r", p, "s")
		for _, a := range gotArgs {
			if a == "--profile" {
				t.Errorf("profile %q must not pass --profile, got %v", p, gotArgs)
			}
		}
	}
}

func TestFetchRuntimeStatusDegradesGracefully(t *testing.T) {
	cases := map[string]SquadRunner{
		"exec error":     func(string, ...string) ([]byte, error) { return nil, errors.New("not found") },
		"malformed json": func(string, ...string) ([]byte, error) { return []byte("{not json"), nil },
		"wrong kind":     func(string, ...string) ([]byte, error) { return []byte(`{"kind":"sessions","data":{}}`), nil },
		"empty output":   func(string, ...string) ([]byte, error) { return nil, nil },
		// pre-1.5 amq-squad: status with members but no actions/capability.
		"no runtime fields": func(string, ...string) ([]byte, error) {
			return []byte(`{"kind":"status","data":{"capabilities":{"operator_gates":true},"records":[{"role":"cto","handle":"cto"}]}}`), nil
		},
	}
	for name, run := range cases {
		rs := FetchRuntimeStatus(run, "/r", "", "s")
		if rs.HasActions() || rs.Advertised || len(rs.Members) != 0 {
			t.Errorf("%s: expected zero RuntimeStatus, got %+v", name, rs)
		}
	}
	// missing required scope -> no call, zero value.
	called := false
	run := func(string, ...string) ([]byte, error) { called = true; return nil, nil }
	FetchRuntimeStatus(run, "", "", "s")
	FetchRuntimeStatus(run, "/r", "", "")
	FetchRuntimeStatus(nil, "/r", "", "s")
	if called {
		t.Error("must not shell amq-squad without dir+session")
	}
}

func TestRuntimeMemberJumpTarget(t *testing.T) {
	m := RuntimeMember{Role: "cto", Handle: "cto", Session: "main", WindowName: "squad", PaneID: "%7", PaneAlive: true}
	tt, ok := m.JumpTarget("issue-96", "cto")
	if !ok {
		t.Fatal("a live member should yield a jump target")
	}
	if tt.PaneID != "%7" || tt.Session != "main" || tt.WindowName != "squad" {
		t.Errorf("target fields wrong: %+v", tt)
	}
	// The iTerm2 focus token is reconstructed as amq:<workstream>:<role>, matching
	// what amq-squad stamps — so cross-session focus works without scraping.
	if tt.Title != "amq:issue-96:cto" {
		t.Errorf("title token = %q, want amq:issue-96:cto", tt.Title)
	}
	if _, ok := (RuntimeMember{PaneID: "%7", PaneAlive: false}).JumpTarget("s", "r"); ok {
		t.Error("a dead pane must not yield a target")
	}
	if _, ok := (RuntimeMember{PaneAlive: true}).JumpTarget("s", "r"); ok {
		t.Error("an empty pane id must not yield a target")
	}
}

func TestTargetSpecPrefersPaneID(t *testing.T) {
	if got := targetSpec(TmuxTarget{Session: "main", Window: "0", Pane: "1", PaneID: "%9"}); got != "%9" {
		t.Errorf("pane id must win as the -t target, got %q", got)
	}
	if got := targetSpec(TmuxTarget{Session: "main", Window: "0", Pane: "1"}); got != "main:0.1" {
		t.Errorf("without a pane id, fall back to session:window.pane, got %q", got)
	}
}
