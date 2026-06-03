package noc

import (
	"errors"
	"testing"

	"github.com/omriariav/amq-noc/internal/state"
)

func TestResolveTmuxTarget_MatchesByCWDAndEngine(t *testing.T) {
	projectDir := t.TempDir()
	panes := []TmuxPane{
		// Wrong CWD: must be ignored even though command matches.
		{Session: "other", Window: "0", Pane: "0", PID: 100, Command: "codex", CWD: "/elsewhere"},
		// Wrong engine in the right CWD: must be ignored.
		{Session: "shell", Window: "0", Pane: "0", PID: 200, Command: "zsh", CWD: projectDir},
		// The match: right CWD + right engine.
		{Session: "work", Window: "1", Pane: "2", PID: 300, Command: "codex", CWD: projectDir},
	}
	agent := state.Agent{Handle: "codex", Engine: "codex", AgentPID: 999}

	got, ok := ResolveTmuxTarget(agent, projectDir, panes, nil)
	if !ok {
		t.Fatal("expected a match by cwd+engine, got ok=false")
	}
	want := TmuxTarget{Session: "work", Window: "1", Pane: "2"}
	if got != want {
		t.Fatalf("target = %+v, want %+v", got, want)
	}
}

func TestResolveTmuxTarget_PrefersPIDSubtreeMatch(t *testing.T) {
	projectDir := t.TempDir()
	// Two candidate panes, both right CWD + engine. Only the second pane's
	// process subtree contains the agent PID, so it must win regardless of order.
	panes := []TmuxPane{
		{Session: "a", Window: "0", Pane: "0", PID: 1000, Command: "codex", CWD: projectDir},
		{Session: "b", Window: "3", Pane: "1", PID: 2000, Command: "codex", CWD: projectDir},
	}
	agent := state.Agent{Handle: "codex", Engine: "codex", AgentPID: 2500}

	// pidTree: pane 2000 -> 2400 -> 2500 (the agent). Pane 1000 is barren.
	pidTree := func(pid int) []int {
		switch pid {
		case 2000:
			return []int{2400}
		case 2400:
			return []int{2500}
		default:
			return nil
		}
	}

	got, ok := ResolveTmuxTarget(agent, projectDir, panes, pidTree)
	if !ok {
		t.Fatal("expected a match, got ok=false")
	}
	want := TmuxTarget{Session: "b", Window: "3", Pane: "1"}
	if got != want {
		t.Fatalf("pid-subtree match should win: target = %+v, want %+v", got, want)
	}
}

func TestResolveTmuxTarget_ToleratesStaleAgentPID(t *testing.T) {
	projectDir := t.TempDir()
	// Resurrect rotated the PID: launch.json recorded 70241 but the live pane is
	// pid 68476 and its subtree no longer contains 70241. The cwd+engine signal
	// must still resolve the pane (ok=true) despite the stale PID.
	panes := []TmuxPane{
		{Session: "squad", Window: "0", Pane: "0", PID: 68476, Command: "claude", CWD: projectDir},
	}
	agent := state.Agent{Handle: "claude", Engine: "claude", AgentPID: 70241}

	// pidTree for the live pane never contains the stale recorded PID.
	pidTree := func(pid int) []int {
		if pid == 68476 {
			return []int{68999} // some other live child, not 70241
		}
		return nil
	}

	got, ok := ResolveTmuxTarget(agent, projectDir, panes, pidTree)
	if !ok {
		t.Fatal("stale AgentPID must not prevent a cwd+engine match")
	}
	want := TmuxTarget{Session: "squad", Window: "0", Pane: "0"}
	if got != want {
		t.Fatalf("target = %+v, want %+v", got, want)
	}
}

func TestResolveTmuxTarget_NoMatchReturnsFalse(t *testing.T) {
	projectDir := t.TempDir()
	panes := []TmuxPane{
		{Session: "x", Window: "0", Pane: "0", PID: 1, Command: "vim", CWD: projectDir},
		{Session: "y", Window: "0", Pane: "0", PID: 2, Command: "codex", CWD: "/somewhere/else"},
	}
	agent := state.Agent{Handle: "codex", Engine: "codex", AgentPID: 5}

	if _, ok := ResolveTmuxTarget(agent, projectDir, panes, nil); ok {
		t.Fatal("expected ok=false when no pane matches cwd+engine")
	}
}

func TestResolveTmuxTargetForSession_SessionNameBonus(t *testing.T) {
	projectDir := t.TempDir()
	// Two equally-valid cwd+engine panes; the amq session name breaks the tie.
	panes := []TmuxPane{
		{Session: "random", Window: "0", Pane: "0", PID: 10, Command: "codex", CWD: projectDir},
		{Session: "issue-96", Window: "4", Pane: "1", PID: 20, Command: "codex", CWD: projectDir},
	}
	agent := state.Agent{Handle: "cto", Engine: "codex"}

	got, ok := ResolveTmuxTargetForSession(agent, "issue-96", projectDir, panes, nil)
	if !ok {
		t.Fatal("expected a match")
	}
	want := TmuxTarget{Session: "issue-96", Window: "4", Pane: "1"}
	if got != want {
		t.Fatalf("session-name bonus should win: target = %+v, want %+v", got, want)
	}
}

// Core bug fix: two panes share the same cwd (projectDir) AND engine (codex).
// Under cwd+engine scoring alone both score identically and resolution collapses
// onto whichever codex pane is first, so cpo and cto would jump to the SAME pane.
// The name-first title pass must send cpo -> amq:beta:cpo and cto -> amq:beta:cto
// — DIFFERENT panes. This test is non-vacuous: delete the name-first pass in
// ResolveTmuxTargetForSession and both agents resolve to pane 0 (the first codex).
func TestResolveTmuxTargetForSession_TitleDisambiguatesSameCwdEngine(t *testing.T) {
	projectDir := t.TempDir()
	panes := []TmuxPane{
		{Session: "beta", Window: "0", Pane: "0", PID: 100, Command: "codex", CWD: projectDir, Title: "amq:beta:cpo"},
		{Session: "beta", Window: "0", Pane: "1", PID: 200, Command: "codex", CWD: projectDir, Title: "amq:beta:cto"},
	}

	cpoAgent := state.Agent{Role: "cpo", Handle: "cpo", Engine: "codex"}
	gotCPO, ok := ResolveTmuxTargetForSession(cpoAgent, "beta", projectDir, panes, nil)
	if !ok {
		t.Fatal("cpo: expected a title match")
	}
	// The resolved target now carries the pane title token so the cross-session
	// iTerm2 -CC focus can match the native window without re-walking the panes.
	wantCPO := TmuxTarget{Session: "beta", Window: "0", Pane: "0", Title: "amq:beta:cpo"}
	if gotCPO != wantCPO {
		t.Fatalf("cpo resolved to %+v, want %+v (amq:beta:cpo pane)", gotCPO, wantCPO)
	}

	ctoAgent := state.Agent{Role: "cto", Handle: "cto", Engine: "codex"}
	gotCTO, ok := ResolveTmuxTargetForSession(ctoAgent, "beta", projectDir, panes, nil)
	if !ok {
		t.Fatal("cto: expected a title match")
	}
	wantCTO := TmuxTarget{Session: "beta", Window: "0", Pane: "1", Title: "amq:beta:cto"}
	if gotCTO != wantCTO {
		t.Fatalf("cto resolved to %+v, want %+v (amq:beta:cto pane)", gotCTO, wantCTO)
	}

	if gotCPO == gotCTO {
		t.Fatalf("cpo and cto resolved to the SAME pane %+v; name-first disambiguation failed", gotCPO)
	}
}

// Back-compat: panes carrying NO title (launched before titling existed) must
// still resolve via the unchanged cwd+engine fallback.
func TestResolveTmuxTargetForSession_FallsBackWhenNoTitles(t *testing.T) {
	projectDir := t.TempDir()
	panes := []TmuxPane{
		{Session: "beta", Window: "0", Pane: "0", PID: 100, Command: "zsh", CWD: "/elsewhere"},
		{Session: "beta", Window: "1", Pane: "2", PID: 200, Command: "codex", CWD: projectDir}, // no Title
	}
	agent := state.Agent{Role: "cpo", Handle: "cpo", Engine: "codex"}

	got, ok := ResolveTmuxTargetForSession(agent, "beta", projectDir, panes, nil)
	if !ok {
		t.Fatal("expected cwd+engine fallback match when no titles present")
	}
	want := TmuxTarget{Session: "beta", Window: "1", Pane: "2"}
	if got != want {
		t.Fatalf("fallback resolved to %+v, want %+v", got, want)
	}
}

func TestParsePanes_ParsesPaneTitle(t *testing.T) {
	// 7-field rows: one with a title token, one with an empty title (trailing tab).
	out := "" +
		"beta\t0\t0\t100\tcodex\t/repo\tamq:beta:cpo\n" +
		"beta\t0\t1\t200\tcodex\t/repo\t\n"
	panes := parsePanes(out)
	if len(panes) != 2 {
		t.Fatalf("expected 2 panes, got %d: %+v", len(panes), panes)
	}
	if panes[0].Title != "amq:beta:cpo" {
		t.Fatalf("pane[0].Title = %q, want amq:beta:cpo", panes[0].Title)
	}
	if panes[1].Title != "" {
		t.Fatalf("pane[1].Title = %q, want empty", panes[1].Title)
	}
	if panes[1].Command != "codex" || panes[1].CWD != "/repo" {
		t.Fatalf("pane[1] command/cwd mangled by empty title: %+v", panes[1])
	}
}

func TestSuggestJump_FormatsTarget(t *testing.T) {
	got := SuggestJump(TmuxTarget{Session: "squad", Window: "1", Pane: "2"})
	want := "tmux switch-client -t squad:1.2"
	if got != want {
		t.Fatalf("SuggestJump = %q, want %q", got, want)
	}
}

// TestSwitchTo_InsideTmuxSameSessionSelectsWindow pins the public SwitchTo entry
// point on the same-session branch: with the current tmux session injected to
// equal the target's, SwitchTo emits `tmux select-window` (NOT switch-client),
// which under iTerm2 -CC raises the right native tab with no window explosion.
// (Replaces the old TestSwitchTo_InsideTmuxRunsSwitchClient: switch-client is the
// exact behavior that exploded the -CC layout, so it is no longer emitted.)
func TestSwitchTo_InsideTmuxSameSessionSelectsWindow(t *testing.T) {
	var gotName string
	var gotArgs []string
	restoreExec := swapSwitchExec(func(name string, args ...string) error {
		// Record the FIRST call (select-window); the trailing select-pane is best
		// effort and may also fire.
		if gotName == "" {
			gotName = name
			gotArgs = args
		}
		return nil
	})
	defer restoreExec()
	restoreEnv := swapTmuxEnv(func() string { return "/tmp/tmux-1000/default,1234,0" })
	defer restoreEnv()
	restoreCur := swapCurrentTmuxSession(func() string { return "squad" }) // SAME session
	defer restoreCur()

	if err := SwitchTo(TmuxTarget{Session: "squad", Window: "1", Pane: "2"}); err != nil {
		t.Fatalf("SwitchTo same-session: %v", err)
	}
	if gotName != "tmux" {
		t.Fatalf("exec name = %q, want tmux", gotName)
	}
	wantArgs := []string{"select-window", "-t", "squad:1.2"}
	if len(gotArgs) != len(wantArgs) {
		t.Fatalf("args = %v, want %v", gotArgs, wantArgs)
	}
	for i := range wantArgs {
		if gotArgs[i] != wantArgs[i] {
			t.Fatalf("args = %v, want %v", gotArgs, wantArgs)
		}
	}
}

func TestSwitchTo_OutsideTmuxReturnsTypedError(t *testing.T) {
	var calls [][]string
	restoreExec := swapSwitchExec(func(name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		return nil
	})
	defer restoreExec()
	restoreEnv := swapTmuxEnv(func() string { return "" }) // not in tmux
	defer restoreEnv()

	target := TmuxTarget{Session: "squad", Window: "1", Pane: "2"}
	err := SwitchTo(target)
	if err == nil {
		t.Fatal("expected a typed NotInTmuxError when outside tmux")
	}
	var nit *NotInTmuxError
	if !errors.As(err, &nit) {
		t.Fatalf("error = %T (%v), want *NotInTmuxError", err, err)
	}
	if nit.Command != SuggestJump(target) {
		t.Fatalf("NotInTmuxError.Command = %q, want %q", nit.Command, SuggestJump(target))
	}
	// Outside tmux we still attempt a best-effort select-window so an iTerm2 -CC
	// window raises.
	if len(calls) != 1 || calls[0][1] != "select-window" {
		t.Fatalf("expected a single select-window call, got %v", calls)
	}
}

func TestParsePanes_SkipsMalformedRows(t *testing.T) {
	out := "" +
		"sess\t0\t1\t1234\tcodex\t/work\n" +
		"too\tfew\tfields\n" + // skipped
		"\n" + // blank, skipped
		"sess2\t2\t3\tnotanint\tclaude\t/work2\r\n" // CRLF + non-int pid -> pid 0
	panes := parsePanes(out)
	if len(panes) != 2 {
		t.Fatalf("expected 2 parsed panes, got %d: %+v", len(panes), panes)
	}
	if panes[0] != (TmuxPane{Session: "sess", Window: "0", Pane: "1", PID: 1234, Command: "codex", CWD: "/work"}) {
		t.Fatalf("pane[0] = %+v", panes[0])
	}
	if panes[1].PID != 0 || panes[1].CWD != "/work2" || panes[1].Command != "claude" {
		t.Fatalf("pane[1] = %+v (CRLF + bad pid handling)", panes[1])
	}
}

// swapSwitchExec replaces the package-level switchExec seam for a test and
// returns a restore func.
func swapSwitchExec(fn execRunner) func() {
	prev := switchExec
	switchExec = fn
	return func() { switchExec = prev }
}

// swapTmuxEnv replaces the package-level tmuxEnv seam for a test and returns a
// restore func.
func swapTmuxEnv(fn func() string) func() {
	prev := tmuxEnv
	tmuxEnv = fn
	return func() { tmuxEnv = prev }
}
