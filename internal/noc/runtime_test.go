package noc

import (
	"errors"
	"reflect"
	"testing"
)

const sampleStatusJSON = `{"schema_version":1,"kind":"status","data":{
  "capabilities":{"operator_gates":true},
  "actions":[
    {"kind":"resume_current_window","label":"resume in current window","scope":"session","command":"amq-squad resume --session s --exec --target current-window","mutates":true,"needs_confirmation":true,"available":true},
    {"kind":"resume_new_session","label":"resume in new tmux session","scope":"session","command":"amq-squad resume --session s --exec --target new-session","mutates":true,"needs_confirmation":true,"available":true},
    {"kind":"stop","label":"stop the session","scope":"session","command":"amq-squad stop --session s --all","mutates":true,"needs_confirmation":true,"available":false,"reason":"already stopped"}
  ],
  "records":[
    {"role":"cto","handle":"cto","tmux":{"session":"main","window_id":"@3","window_name":"squad","pane_id":"%1","pane_alive":true},
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
	if len(rs.SessionActions) != 3 {
		t.Fatalf("want 3 session actions, got %d", len(rs.SessionActions))
	}
	if rs.SessionActions[0].Kind != "resume_current_window" ||
		rs.SessionActions[0].Label != "resume in current window" ||
		rs.SessionActions[0].Scope != "session" ||
		!rs.SessionActions[0].Mutates ||
		!rs.SessionActions[0].NeedsConfirmation ||
		!rs.SessionActions[0].Available {
		t.Fatalf("session action parsed wrong: %+v", rs.SessionActions[0])
	}
	if rs.SessionActions[2].Available || rs.SessionActions[2].Reason != "already stopped" {
		t.Fatalf("unavailable session action metadata parsed wrong: %+v", rs.SessionActions[2])
	}
	if len(rs.Members) != 2 {
		t.Fatalf("want 2 members, got %d", len(rs.Members))
	}
	cto, ok := rs.MemberByRole("CTO") // case-insensitive
	if !ok || cto.PaneID != "%1" || !cto.PaneAlive || len(cto.Actions) != 4 {
		t.Fatalf("cto member parsed wrong: %+v ok=%v", cto, ok)
	}
	if cto.Session != "main" || cto.WindowID != "@3" || cto.WindowName != "squad" {
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
	m := RuntimeMember{Role: "cto", Handle: "cto", Session: "main", WindowID: "@3", WindowName: "squad", PaneID: "%7", PaneAlive: true}
	tt, ok := m.JumpTarget("issue-96", "cto")
	if !ok {
		t.Fatal("a live member should yield a jump target")
	}
	if tt.PaneID != "%7" || tt.WindowID != "@3" || tt.Session != "main" || tt.WindowName != "squad" {
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

func TestWindowAndPaneTargets(t *testing.T) {
	// Contract target (ids, no indices): window id for select-window, pane id for
	// select-pane — each on its documented tmux target type.
	ct := TmuxTarget{Session: "main", WindowID: "@5", PaneID: "%9"}
	if got := windowTarget(ct); got != "@5" {
		t.Errorf("windowTarget should prefer the window id, got %q", got)
	}
	if got := paneTarget(ct); got != "%9" {
		t.Errorf("paneTarget should prefer the pane id, got %q", got)
	}
	// A pane id alone is a valid window target (tmux resolves it to its window).
	if got := windowTarget(TmuxTarget{PaneID: "%9"}); got != "%9" {
		t.Errorf("windowTarget should fall back to the pane id, got %q", got)
	}
	// Scraping path (indices) keeps the session:window.pane spec for both.
	it := TmuxTarget{Session: "main", Window: "1", Pane: "2"}
	if got := windowTarget(it); got != "main:1.2" {
		t.Errorf("windowTarget index spec = %q", got)
	}
	if got := paneTarget(it); got != "main:1.2" {
		t.Errorf("paneTarget index spec = %q", got)
	}
	// targetSpec stays the index/session form so switch-client (SuggestJump) and
	// the cross-session osascript fallback never embed a pane id where a session
	// or window is expected.
	if got := targetSpec(TmuxTarget{Session: "main", PaneID: "%9"}); got != "main" {
		t.Errorf("targetSpec must not embed the pane id, got %q", got)
	}
}
