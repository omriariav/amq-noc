package noc

import (
	"errors"
	"strings"
	"testing"
)

// recordExec returns an execRunner that appends each (name, args...) call to the
// supplied slice, plus a restore-able install helper. ret controls what it
// returns to the caller (nil = success).
func recordExec(calls *[][]string, ret error) execRunner {
	return func(name string, args ...string) error {
		*calls = append(*calls, append([]string{name}, args...))
		return ret
	}
}

// recordOsa is recordExec for the osaRunner seam: it records each call AND drives
// the stdout sentinel (out) + error the cross-session focus path reads. out=="OK"
// simulates a tab raised; out=="MISS" simulates osascript running cleanly but
// matching no tab; a non-nil ret simulates osascript itself failing.
func recordOsa(calls *[][]string, out string, ret error) osaRunner {
	return func(name string, args ...string) (string, error) {
		*calls = append(*calls, append([]string{name}, args...))
		return out, ret
	}
}

// TestSwitchTo_SameSessionSelectsWindowNotSwitchClient proves QA-4a's
// same-session branch: when the target tmux session equals the client's current
// session, SwitchTo emits `tmux select-window` (and select-pane) and NEVER
// `switch-client`: same-session select-window under iTerm2 -CC raises the right
// native tab with no window explosion. Non-vacuous: a switch-client argv would
// fail the assertion below.
func TestSwitchTo_SameSessionSelectsWindowNotSwitchClient(t *testing.T) {
	var tmuxCalls [][]string
	var osaCalls [][]string
	restoreExec := swapSwitchExec(recordExec(&tmuxCalls, nil))
	defer restoreExec()
	restoreOsa := swapOsascriptExec(recordOsa(&osaCalls, focusOK, nil))
	defer restoreOsa()
	restoreEnv := swapTmuxEnv(func() string { return "/tmp/tmux-1000/default,1234,0" })
	defer restoreEnv()

	target := TmuxTarget{Session: "squad", Window: "1", Pane: "2"}
	err := switchToWithSession(target, func() string { return "squad" }) // SAME session
	if err != nil {
		t.Fatalf("same-session SwitchTo: %v", err)
	}

	if len(osaCalls) != 0 {
		t.Fatalf("same-session must NOT call osascript, got %v", osaCalls)
	}
	if len(tmuxCalls) == 0 {
		t.Fatal("same-session should emit a tmux select-window")
	}
	if tmuxCalls[0][0] != "tmux" || tmuxCalls[0][1] != "select-window" {
		t.Fatalf("first tmux call = %v, want select-window", tmuxCalls[0])
	}
	if tmuxCalls[0][3] != "squad:1.2" {
		t.Fatalf("select-window target = %q, want squad:1.2", tmuxCalls[0][3])
	}
	for _, c := range tmuxCalls {
		if c[1] == "switch-client" {
			t.Fatalf("same-session must NEVER emit switch-client, got %v", c)
		}
	}
}

// TestSwitchTo_DifferentSessionActivatesITermNoSwitchClient proves QA-4a's
// cross-session branch: a target in a DIFFERENT tmux session raises the iTerm2
// native window via osascript and NEVER emits switch-client (the bug that
// exploded the -CC layout). Non-vacuous: it asserts an osascript argv carrying
// the pane title token AND that no tmux switch-client is ever emitted.
func TestSwitchTo_DifferentSessionActivatesITermNoSwitchClient(t *testing.T) {
	var tmuxCalls [][]string
	var osaCalls [][]string
	restoreExec := swapSwitchExec(recordExec(&tmuxCalls, nil))
	defer restoreExec()
	restoreOsa := swapOsascriptExec(recordOsa(&osaCalls, focusOK, nil)) // osascript SUCCEEDS (raised a tab)
	defer restoreOsa()
	restoreEnv := swapTmuxEnv(func() string { return "/tmp/tmux-1000/default,1234,0" })
	defer restoreEnv()

	target := TmuxTarget{Session: "beta", Window: "0", Pane: "1", Title: "amq:beta:cto"}
	err := switchToWithSession(target, func() string { return "alpha" }) // DIFFERENT session
	if err != nil {
		t.Fatalf("cross-session SwitchTo (osascript ok): %v", err)
	}

	if len(osaCalls) != 1 {
		t.Fatalf("cross-session should call osascript exactly once, got %v", osaCalls)
	}
	if osaCalls[0][0] != "osascript" {
		t.Fatalf("cross-session exec = %q, want osascript", osaCalls[0][0])
	}
	// The token must appear as the trailing argv (the AppleScript matches it).
	last := osaCalls[0][len(osaCalls[0])-1]
	if last != "amq:beta:cto" {
		t.Fatalf("osascript token argv = %q, want amq:beta:cto", last)
	}
	// On the osascript-success path no tmux command runs at all.
	if len(tmuxCalls) != 0 {
		t.Fatalf("cross-session osascript-success path must NOT run tmux, got %v", tmuxCalls)
	}
	for _, c := range tmuxCalls {
		if c[1] == "switch-client" {
			t.Fatalf("cross-session must NEVER emit switch-client, got %v", c)
		}
	}
}

// TestSwitchTo_DifferentSessionOsascriptFailsFallsBack proves the documented
// graceful degradation: when osascript fails (not macOS / not iTerm2 / no match)
// the cross-session focus falls back to a best-effort `tmux select-window` and
// returns a NotInTmuxError-style note carrying the suggested manual command, and
// STILL never emits switch-client.
func TestSwitchTo_DifferentSessionOsascriptFailsFallsBack(t *testing.T) {
	var tmuxCalls [][]string
	var osaCalls [][]string
	restoreExec := swapSwitchExec(recordExec(&tmuxCalls, nil))
	defer restoreExec()
	restoreOsa := swapOsascriptExec(recordOsa(&osaCalls, "", errors.New("osascript: no iTerm2")))
	defer restoreOsa()
	restoreEnv := swapTmuxEnv(func() string { return "/tmp/tmux-1000/default,1234,0" })
	defer restoreEnv()

	target := TmuxTarget{Session: "beta", Window: "0", Pane: "1", Title: "amq:beta:cto"}
	err := switchToWithSession(target, func() string { return "alpha" })

	var nit *NotInTmuxError
	if !errors.As(err, &nit) {
		t.Fatalf("osascript-fail fallback should return *NotInTmuxError, got %T (%v)", err, err)
	}
	if nit.Command != SuggestJump(target) {
		t.Fatalf("fallback note command = %q, want %q", nit.Command, SuggestJump(target))
	}
	if len(osaCalls) != 1 {
		t.Fatalf("osascript should still have been attempted once, got %v", osaCalls)
	}
	if len(tmuxCalls) != 1 || tmuxCalls[0][1] != "select-window" {
		t.Fatalf("fallback should emit one tmux select-window, got %v", tmuxCalls)
	}
	for _, c := range tmuxCalls {
		if c[1] == "switch-client" {
			t.Fatalf("fallback must NEVER emit switch-client, got %v", c)
		}
	}
}

// TestSwitchTo_DifferentSessionOsascriptMissReturnsFocusMiss is the QA-8
// regression: osascript runs cleanly (iTerm2 IS scriptable, exit 0) but scans
// every tab and matches NO session name. The agent's tmux session is not
// attached to this iTerm2. The OLD code read only the exit code, saw 0, and
// reported a false "jumped". The fix reads the MISS stdout sentinel and returns
// a typed FocusMissError carrying the manual attach command and must NOT fall
// back to a tmux select-window (there is no tab to raise; that would be another
// silent no-op pretending to work).
func TestSwitchTo_DifferentSessionOsascriptMissReturnsFocusMiss(t *testing.T) {
	var tmuxCalls [][]string
	var osaCalls [][]string
	restoreExec := swapSwitchExec(recordExec(&tmuxCalls, nil))
	defer restoreExec()
	restoreOsa := swapOsascriptExec(recordOsa(&osaCalls, focusMiss, nil)) // ran, matched nothing
	defer restoreOsa()
	restoreEnv := swapTmuxEnv(func() string { return "/tmp/tmux-1000/default,1234,0" })
	defer restoreEnv()

	target := TmuxTarget{Session: "beta", Window: "0", Pane: "1", Title: "amq:beta:cto"}
	err := switchToWithSession(target, func() string { return "alpha" }) // DIFFERENT session

	var fm *FocusMissError
	if !errors.As(err, &fm) {
		t.Fatalf("a MISS must return *FocusMissError (the QA-8 honest-failure fix), got %T (%v)", err, err)
	}
	if fm.Command != AttachCommand(target) {
		t.Errorf("FocusMissError.Command = %q, want the attach hint %q", fm.Command, AttachCommand(target))
	}
	if len(osaCalls) != 1 {
		t.Fatalf("osascript should be attempted exactly once, got %v", osaCalls)
	}
	// A miss has no tab to raise: do NOT fall back to a tmux select-window (that
	// would be the same silent no-op the fix exists to kill).
	if len(tmuxCalls) != 0 {
		t.Fatalf("a MISS must NOT run any tmux command (no tab exists to raise), got %v", tmuxCalls)
	}
}

// TestITermActivateArgs_CarriesSentinels proves the AppleScript the cross-session
// path runs actually emits the OK/MISS sentinels the Go side keys on. Without
// them the stdout read is meaningless and the fix is vacuous.
func TestITermActivateArgs_CarriesSentinels(t *testing.T) {
	args := iTermActivateArgs(TmuxTarget{Session: "beta", Window: "0", Pane: "1", Title: "amq:beta:cto"})
	if len(args) < 2 || args[0] != "-e" {
		t.Fatalf("activate argv should start with -e <script>, got %v", args)
	}
	script := args[1]
	if !strings.Contains(script, `return "`+focusOK+`"`) {
		t.Errorf("script must return the OK sentinel on a match; script:\n%s", script)
	}
	if !strings.Contains(script, `return "`+focusMiss+`"`) {
		t.Errorf("script must return the MISS sentinel when nothing matches; script:\n%s", script)
	}
	// The token must still be the trailing argv the script reads as item 1.
	if args[len(args)-1] != "amq:beta:cto" {
		t.Errorf("token argv = %q, want amq:beta:cto", args[len(args)-1])
	}
}

// TestSwitchTo_NeverSwitchClientAcrossSessions is the explicit non-vacuous guard
// the brief demands: a target in another session (osascript ok OR failing,
// current session known OR unknown) must NEVER produce a switch-client argv on
// ANY code path.
func TestSwitchTo_NeverSwitchClientAcrossSessions(t *testing.T) {
	cases := []struct {
		name       string
		curSession string
		osaErr     error
	}{
		{"osa ok, known cur", "alpha", nil},
		{"osa fail, known cur", "alpha", errors.New("no iterm")},
		{"osa ok, unknown cur", "", nil},
		{"osa fail, unknown cur", "", errors.New("no iterm")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var tmuxCalls [][]string
			var osaCalls [][]string
			restoreExec := swapSwitchExec(recordExec(&tmuxCalls, nil))
			defer restoreExec()
			restoreOsa := swapOsascriptExec(recordOsa(&osaCalls, focusOK, tc.osaErr))
			defer restoreOsa()
			restoreEnv := swapTmuxEnv(func() string { return "in-tmux" })
			defer restoreEnv()

			target := TmuxTarget{Session: "beta", Window: "0", Pane: "1", Title: "amq:beta:cto"}
			_ = switchToWithSession(target, func() string { return tc.curSession })

			for _, c := range tmuxCalls {
				if c[1] == "switch-client" {
					t.Fatalf("%s: emitted switch-client across sessions: %v", tc.name, c)
				}
			}
		})
	}
}

// TestSwitchTo_OutsideTmuxStillReturnsTypedError keeps the not-in-tmux contract:
// $TMUX unset → no switch is run, a best-effort select-window is attempted, and a
// typed NotInTmuxError carrying the suggested command is returned. (The
// pre-existing TestSwitchTo_OutsideTmuxReturnsTypedError covers SwitchTo via the
// real env seam; this one pins the injected-session path.)
func TestSwitchTo_OutsideTmuxStillReturnsTypedError(t *testing.T) {
	var tmuxCalls [][]string
	restoreExec := swapSwitchExec(recordExec(&tmuxCalls, nil))
	defer restoreExec()
	restoreEnv := swapTmuxEnv(func() string { return "" }) // not in tmux
	defer restoreEnv()

	target := TmuxTarget{Session: "squad", Window: "1", Pane: "2"}
	err := switchToWithSession(target, func() string { return "" })
	var nit *NotInTmuxError
	if !errors.As(err, &nit) {
		t.Fatalf("outside tmux should return *NotInTmuxError, got %T (%v)", err, err)
	}
	if len(tmuxCalls) != 1 || tmuxCalls[0][1] != "select-window" {
		t.Fatalf("outside tmux should emit one select-window, got %v", tmuxCalls)
	}
}

// TestITermFocusToken_PrefersTitleThenWindowName proves the cross-session match
// token derivation: pane title first, then window name, then the session:window
// spec.
func TestITermFocusToken_PrefersTitleThenWindowName(t *testing.T) {
	if got := iTermFocusToken(TmuxTarget{Session: "beta", Window: "0", Pane: "1", Title: "amq:beta:cto", WindowName: "wn"}); got != "amq:beta:cto" {
		t.Fatalf("title should win: %q", got)
	}
	if got := iTermFocusToken(TmuxTarget{Session: "beta", Window: "0", Pane: "1", WindowName: "mywin"}); got != "mywin" {
		t.Fatalf("window name should be the fallback: %q", got)
	}
	if got := iTermFocusToken(TmuxTarget{Session: "beta", Window: "0", Pane: "1"}); got != "beta:0.1" {
		t.Fatalf("spec should be the last resort: %q", got)
	}
}

// TestParsePanes_ParsesWindowName proves the 8th field (window_name) is parsed
// and carried, and that older 7-field rows still parse (window name empty).
func TestParsePanes_ParsesWindowName(t *testing.T) {
	out := "" +
		"beta\t0\t0\t100\tcodex\t/repo\tamq:beta:cpo\tcpo-win\n" + // 8 fields
		"beta\t0\t1\t200\tcodex\t/repo\tamq:beta:cto\n" // 7 fields (no window name)
	panes := parsePanes(out)
	if len(panes) != 2 {
		t.Fatalf("expected 2 panes, got %d: %+v", len(panes), panes)
	}
	if panes[0].WindowName != "cpo-win" {
		t.Fatalf("pane[0].WindowName = %q, want cpo-win", panes[0].WindowName)
	}
	if panes[1].WindowName != "" {
		t.Fatalf("pane[1].WindowName = %q, want empty (7-field row)", panes[1].WindowName)
	}
	if panes[1].Title != "amq:beta:cto" {
		t.Fatalf("pane[1].Title = %q, want amq:beta:cto", panes[1].Title)
	}
}

// swapOsascriptExec replaces the package-level osascriptExec seam for a test and
// returns a restore func.
func swapOsascriptExec(fn osaRunner) func() {
	prev := osascriptExec
	osascriptExec = fn
	return func() { osascriptExec = prev }
}

// swapCurrentTmuxSession replaces the package-level currentTmuxSession seam for a
// test and returns a restore func.
func swapCurrentTmuxSession(fn func() string) func() {
	prev := currentTmuxSession
	currentTmuxSession = fn
	return func() { currentTmuxSession = prev }
}
