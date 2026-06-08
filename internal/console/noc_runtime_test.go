package console

import (
	"testing"

	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/state"
)

func sampleRuntimeStatus() noc.RuntimeStatus {
	return noc.RuntimeStatus{
		Members: []noc.RuntimeMember{
			{Role: "cto", Handle: "cto", PaneID: "%1", PaneAlive: true, Actions: []noc.RuntimeAction{
				{Kind: "focus", Command: "amq-squad focus --role cto", Available: true},
				{Kind: "send", Command: "amq-squad send --role cto --body-file -", Available: true},
				{Kind: "resume", Command: "amq-squad resume --role cto", Available: true},
				{Kind: "status", Command: "amq-squad status --role cto", Available: true},
			}},
			{Role: "qa", Handle: "qa", PaneAlive: false, Actions: []noc.RuntimeAction{
				{Kind: "focus", Command: "amq-squad focus --role qa", Available: false}, // dead pane -> omitted
				{Kind: "resume", Command: "amq-squad resume --role qa", Available: true},
			}},
		},
	}
}

func TestRuntimeCommandActionsSessionNode(t *testing.T) {
	rs := sampleRuntimeStatus()
	n := nocNode{kind: nodeSession, session: state.Session{Name: "issue-96"}}
	got := runtimeCommandActions(rs, n)
	// cto: 4 available; qa: 1 available (focus filtered out) -> 5 total.
	if len(got) != 5 {
		t.Fatalf("want 5 available actions, got %d: %+v", len(got), got)
	}
	if got[0].Label != "focus cto" || got[0].Command != "amq-squad focus --role cto" {
		t.Errorf("first action wrong: %+v", got[0])
	}
	for _, a := range got {
		if a.Command == "amq-squad focus --role qa" {
			t.Error("qa's unavailable focus must be omitted")
		}
	}
}

func TestRuntimeCommandActionsAgentNode(t *testing.T) {
	rs := sampleRuntimeStatus()
	n := nocNode{kind: nodeAgent, agent: state.Agent{Role: "qa"}}
	got := runtimeCommandActions(rs, n)
	if len(got) != 1 || got[0].Label != "resume qa" {
		t.Fatalf("agent qa should yield only its available resume action, got %+v", got)
	}
	// Empty status -> nothing (graceful).
	if a := runtimeCommandActions(noc.RuntimeStatus{}, n); a != nil {
		t.Errorf("empty status should yield no actions, got %+v", a)
	}
}

func TestHandleRuntimeActionsMergesIntoOpenPicker(t *testing.T) {
	static := []nocCommandAction{
		commandAction("status", "amq-squad status --session s", "static status"),
		commandAction("resume preview", "amq-squad resume --session s", "static resume"),
	}
	m := &NOCModel{commandPicker: &commandPickerOverlay{selectionID: "sess|x", commands: static}}

	runtime := []nocCommandAction{
		commandAction("focus cto", "amq-squad focus --role cto", "d"),
		commandAction("status", "amq-squad status --session s", "dup of static"), // duplicate command
	}
	m.handleRuntimeActions(runtimeActionsMsg{selectionID: "sess|x", actions: runtime})

	cmds := m.commandPicker.commands
	// runtime leads; duplicate command dropped; static resume retained.
	if cmds[0].Command != "amq-squad focus --role cto" {
		t.Errorf("runtime actions should lead, got %+v", cmds[0])
	}
	seen := map[string]int{}
	for _, c := range cmds {
		seen[c.Command]++
	}
	if seen["amq-squad status --session s"] != 1 {
		t.Errorf("duplicate command should appear once, got %d", seen["amq-squad status --session s"])
	}
	if seen["amq-squad resume --session s"] != 1 {
		t.Error("static resume should be retained")
	}
}

func TestHandleRuntimeActionsIgnoresStaleSelection(t *testing.T) {
	static := []nocCommandAction{commandAction("status", "amq-squad status", "s")}
	m := &NOCModel{commandPicker: &commandPickerOverlay{selectionID: "sess|x", commands: static}}
	// A late fetch for a DIFFERENT row must not bleed in.
	m.handleRuntimeActions(runtimeActionsMsg{selectionID: "sess|other", actions: []nocCommandAction{
		commandAction("focus cto", "amq-squad focus --role cto", "d"),
	}})
	if len(m.commandPicker.commands) != 1 {
		t.Fatalf("stale-selection actions must be ignored, got %+v", m.commandPicker.commands)
	}
	// No picker open -> no panic, no-op.
	(&NOCModel{}).handleRuntimeActions(runtimeActionsMsg{selectionID: "x", actions: static})
}

func TestRuntimeFetchScopePlaceholderProfile(t *testing.T) {
	// A session whose agents span profiles resolves the "PROFILE" placeholder;
	// the fetch must NOT pass that literal as --profile.
	n := nocNode{
		kind:    nodeSession,
		project: noc.ProjectSnapshot{Dir: "/repo", Snap: state.Snapshot{Sessions: []state.Session{{Name: "s", Agents: []state.Agent{{TeamProfile: "a"}, {TeamProfile: "b"}}}}}},
		session: state.Session{Name: "s"},
	}
	dir, profile, session := runtimeFetchScope(n)
	if dir != "/repo" || session != "s" || profile != "" {
		t.Fatalf("placeholder profile must map to empty; got dir=%q profile=%q session=%q", dir, profile, session)
	}
}

func TestPerformAgentJumpPrefersRuntimeContract(t *testing.T) {
	var got noc.TmuxTarget
	m := &NOCModel{
		switchTo: func(tt noc.TmuxTarget) error { got = tt; return nil },
		panes: func() ([]noc.TmuxPane, error) {
			t.Fatal("must NOT scrape tmux when the contract has a live pane")
			return nil, nil
		},
		runtimeFetch: func(dir, profile, session string) noc.RuntimeStatus {
			return noc.RuntimeStatus{Members: []noc.RuntimeMember{{
				Role: "cto", Handle: "cto", Session: "main", WindowName: "squad", PaneID: "%99", PaneAlive: true,
				Actions: []noc.RuntimeAction{{Kind: "focus", Command: "amq-squad focus --role cto", Available: true}},
			}}}
		},
	}
	m.performAgentJump(state.Agent{Role: "cto", Handle: "cto"}, "issue-96", "/repo", "", "cto")
	if got.PaneID != "%99" {
		t.Fatalf("jump must target the contract's pane id, got %+v", got)
	}
	if got.Title != "amq:issue-96:cto" || got.Session != "main" {
		t.Errorf("contract target wrong: %+v", got)
	}
}

func TestPerformAgentJumpFallsBackToScraping(t *testing.T) {
	var got noc.TmuxTarget
	scraped := false
	m := &NOCModel{
		switchTo: func(tt noc.TmuxTarget) error { got = tt; return nil },
		panes: func() ([]noc.TmuxPane, error) {
			scraped = true
			return []noc.TmuxPane{{Session: "main", Window: "0", Pane: "1", CWD: "/repo", Command: "codex", Title: "amq:issue-96:cto"}}, nil
		},
		// Older amq-squad / no runtime pane -> zero RuntimeStatus.
		runtimeFetch: func(dir, profile, session string) noc.RuntimeStatus { return noc.RuntimeStatus{} },
	}
	m.performAgentJump(state.Agent{Role: "cto", Handle: "cto", Engine: "codex"}, "issue-96", "/repo", "", "cto")
	if !scraped {
		t.Error("must fall back to scraping when the contract has no live pane")
	}
	if got.Session != "main" || got.PaneID != "" {
		t.Fatalf("scraping fallback should resolve a session:window.pane target, got %+v", got)
	}
}
