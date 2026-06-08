package noc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/omriariav/amq-noc/internal/state"
)

// TmuxPane is one row from `tmux list-panes -a`: a pane plus its running
// command and current working directory. PID is the pane's foreground process
// (#{pane_pid}); the actual agent process is typically a descendant of it.
type TmuxPane struct {
	Session string
	Window  string
	Pane    string
	PID     int
	Command string
	CWD     string
	// Title is the pane's #{pane_title}. The launcher stamps a deterministic
	// token here (amq:<session>:<role>) so the jump can resolve name-first,
	// engine-agnostic and rotation-proof. Empty for panes launched before
	// titling existed (or non-amq panes); those fall back to cwd+engine scoring.
	Title string
	// WindowName is the pane's #{window_name}. It is carried so the cross-session
	// iTerm2 -CC focus can fall back to matching the native window/tab by window
	// name when no pane-title token is present. Optional (older tmux output may
	// omit it; the parser tolerates its absence).
	WindowName string
}

// TmuxTarget identifies a single pane for the jump action. Title carries the
// pane's deterministic token (amq:<session>:<role>) when known so the
// cross-session iTerm2 -CC focus can match the native window/tab by title
// without re-walking the pane list. It is best-effort: an empty Title falls
// back to the tmux window name for the osascript match.
type TmuxTarget struct {
	Session string
	Window  string
	Pane    string
	// PaneID is the tmux pane id (%N). When set it is the authoritative -t
	// target: it is globally unique within the tmux server, so select-window /
	// select-pane / switch-client hit the exact pane regardless of window/pane
	// indices. It is populated when the target comes from amq-squad's runtime
	// contract (the persisted pane id) rather than from scraping pane indices.
	PaneID string
	// WindowID is the tmux window id (@N). When set it is the doc-correct
	// target-window for select-window (the pane id resolves to it too, but the
	// window id is the precise type). Populated from the runtime contract.
	WindowID string
	// Title is the pane title token (amq:<session>:<role>) used to match the
	// iTerm2 native window/tab on the cross-session focus path. Optional.
	Title string
	// WindowName is the tmux window's name (#{window_name}); the cross-session
	// osascript falls back to matching it when Title is empty. Optional.
	WindowName string
}

// PaneLister is the seam for enumerating tmux panes. The default implementation
// shells `tmux list-panes -a` READ-ONLY; tests inject a fake.
type PaneLister func() ([]TmuxPane, error)

// execRunner is the seam for the explicit jump action's subprocess. The default
// runs the real tmux binary; tests inject a recorder.
type execRunner func(name string, args ...string) error

// defaultExecRunner runs a command and discards its output, returning only the
// error. It is the production seam for SwitchTo.
func defaultExecRunner(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

// osaRunner is the seam for the cross-session iTerm2 focus subprocess. Unlike
// execRunner it returns the command's stdout so the caller can read the
// AppleScript's OK/MISS sentinel. The crux of QA-8's fix: osascript exits 0
// whether it raised a tab OR fell off the end finding nothing, so the exit code
// alone cannot tell a real focus from a silent no-match. Production captures
// stdout; tests inject a recorder.
type osaRunner func(name string, args ...string) (string, error)

// defaultOsaRunner runs osascript and returns its trimmed stdout plus error. A
// non-nil error means osascript itself could not run (not macOS / not iTerm2 /
// scripting error); stdout carries the OK/MISS sentinel when it ran.
func defaultOsaRunner(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return strings.TrimSpace(string(out)), err
}

// DefaultPaneLister shells `tmux list-panes -a` with a tab-separated format and
// parses each row into a TmuxPane. It is strictly READ-ONLY. A missing tmux
// binary or no server is reported as an error so callers can degrade.
func DefaultPaneLister() ([]TmuxPane, error) {
	// pane_title + window_name are appended last so an empty title (older/non-amq
	// panes) leaves a trailing tab the parser tolerates; pane_title carries the
	// name-first resolution token and window_name the cross-session focus
	// fallback.
	const format = "#{session_name}\t#{window_index}\t#{pane_index}\t#{pane_pid}\t#{pane_current_command}\t#{pane_current_path}\t#{pane_title}\t#{window_name}"
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", format).Output()
	if err != nil {
		return nil, fmt.Errorf("tmux list-panes: %w", err)
	}
	return parsePanes(string(out)), nil
}

// parsePanes parses the tab-separated `tmux list-panes` output. Malformed rows
// (too few fields) are skipped rather than failing the whole parse.
func parsePanes(out string) []TmuxPane {
	var panes []TmuxPane
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 6 {
			continue
		}
		pid, _ := strconv.Atoi(strings.TrimSpace(fields[3]))
		pane := TmuxPane{
			Session: fields[0],
			Window:  fields[1],
			Pane:    fields[2],
			PID:     pid,
			Command: fields[4],
			CWD:     fields[5],
		}
		// pane_title is the optional 7th field; tolerate panes captured without
		// it (older tmux output, or rows that simply have no title).
		if len(fields) >= 7 {
			pane.Title = fields[6]
		}
		// window_name is the optional 8th field; tolerate its absence too.
		if len(fields) >= 8 {
			pane.WindowName = fields[7]
		}
		panes = append(panes, pane)
	}
	return panes
}

// ResolveTmuxTarget matches a running agent to the tmux pane hosting it.
//
// The core difficulty: resurrect/resume rotates PIDs, so the launch-record
// AgentPID is frequently STALE relative to the live pane (proven on this
// machine: launch.json recorded pid 70241 while the live pane_pid was 68476).
// We therefore do NOT require AgentPID to equal pane.PID. Instead we score
// candidate panes:
//
//	(a) REQUIRED: pane.CWD == projectDir AND the pane command matches the
//	    agent engine (codex/claude). This is the durable, rotation-proof signal.
//	(b) PREFERRED: the pane's process subtree (pidTree(pane.PID)) contains the
//	    agent's recorded AgentPID: strongest confirmation when the PID is still
//	    valid, but optional so a stale PID never disqualifies a pane.
//	(c) PREFERRED: the pane's tmux session name equals the amq session name.
//
// Among panes satisfying (a), the highest-scoring is returned. ok is false when
// no pane satisfies the required signal (a).
//
// pidTree may be nil; when nil the (b) bonus is simply not awarded.
//
// Agent does not carry the amq session name, so the (c) tmux-session-name bonus
// is applied only against the agent Handle (the common 1:1 session==handle
// convention). Callers that know the amq session name should use
// ResolveTmuxTargetForSession for the stronger session match.
func ResolveTmuxTarget(a state.Agent, projectDir string, panes []TmuxPane, pidTree func(pid int) []int) (TmuxTarget, bool) {
	return ResolveTmuxTargetForSession(a, "", projectDir, panes, pidTree)
}

// ResolveTmuxTargetForSession is ResolveTmuxTarget with an explicit amq session
// name so the (c) tmux-session-name bonus can be applied. ResolveTmuxTarget
// delegates to this with an empty session hint.
func ResolveTmuxTargetForSession(a state.Agent, sessionName, projectDir string, panes []TmuxPane, pidTree func(pid int) []int) (TmuxTarget, bool) {
	// Name-first pass (highest confidence, engine-agnostic, rotation-proof): if the
	// launcher stamped a deterministic token on a pane title, resolve by an exact
	// title match. This disambiguates agents that share the same cwd AND engine:
	// the bug where cpo·codex and cto·codex in one repo both match the same panes
	// under the cwd+engine scoring below and resolve to whichever pane comes first.
	if want := expectedPaneToken(sessionName, a); want != "" {
		for _, p := range panes {
			if p.Title == want {
				return targetFromPane(p), true
			}
		}
	}

	// Fallback: cwd+engine+pid scoring for panes launched before titles existed
	// (or non-amq panes). Unchanged from the original resolver.
	wantCWD := cleanDir(projectDir)
	engine := strings.ToLower(strings.TrimSpace(a.Engine))

	bestScore := -1
	var best TmuxPane
	ok := false

	for _, p := range panes {
		if cleanDir(p.CWD) != wantCWD {
			continue
		}
		if !commandMatchesEngine(p.Command, engine) {
			continue
		}

		score := 0
		if a.AgentPID > 0 && pidTree != nil && subtreeContains(pidTree, p.PID, a.AgentPID) {
			score += 100
		}
		if sessionName != "" && p.Session == sessionName {
			score += 50
		}
		if a.Handle != "" && p.Session == a.Handle {
			score += 10
		}

		if score > bestScore {
			bestScore = score
			best = p
			ok = true
		}
	}

	if !ok {
		return TmuxTarget{}, false
	}
	return targetFromPane(best), true
}

// targetFromPane builds a TmuxTarget from a resolved pane, carrying its title
// token + window name so the cross-session iTerm2 -CC focus can match the native
// window without re-walking the pane list.
func targetFromPane(p TmuxPane) TmuxTarget {
	return TmuxTarget{
		Session:    p.Session,
		Window:     p.Window,
		Pane:       p.Pane,
		Title:      p.Title,
		WindowName: p.WindowName,
	}
}

// expectedPaneToken derives the deterministic pane-title token the launcher
// stamps for an agent: "amq:<sessionName>:<role>". It MUST mirror the launcher's
// paneTitleToken (internal/cli/team_launch_tmux.go), which keys on the member
// role. We fall back to the agent Handle only when Role is empty, so the
// name-first pass still has a chance for older/role-less records. Returns ""
// (skip the name-first pass) when there is no session name or no role/handle.
func expectedPaneToken(sessionName string, a state.Agent) string {
	if strings.TrimSpace(sessionName) == "" {
		return ""
	}
	key := strings.TrimSpace(a.Role)
	if key == "" {
		key = strings.TrimSpace(a.Handle)
	}
	if key == "" {
		return ""
	}
	return "amq:" + sessionName + ":" + key
}

// commandMatchesEngine reports whether a pane command corresponds to the agent
// engine. tmux's pane_current_command is usually a bare basename ("codex",
// "claude", "node"), but be tolerant of paths. An empty engine matches nothing.
func commandMatchesEngine(command, engine string) bool {
	if engine == "" {
		return false
	}
	command = strings.ToLower(strings.TrimSpace(command))
	if command == "" {
		return false
	}
	base := command
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	return base == engine || strings.HasPrefix(base, engine)
}

// subtreeContains walks the process tree rooted at root (via pidTree) and
// reports whether want appears anywhere in it, including root itself. It is
// defensive against cycles and missing pidTree.
func subtreeContains(pidTree func(pid int) []int, root, want int) bool {
	if pidTree == nil || root <= 0 || want <= 0 {
		return false
	}
	if root == want {
		return true
	}
	seen := map[int]bool{}
	stack := []int{root}
	for len(stack) > 0 {
		pid := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		if pid == want {
			return true
		}
		for _, child := range pidTree(pid) {
			if child > 0 && !seen[child] {
				stack = append(stack, child)
			}
		}
	}
	return false
}

// cleanDir normalizes a directory path for comparison: absolute + cleaned, with
// any trailing separator removed. Empty stays empty.
func cleanDir(dir string) string {
	if dir == "" {
		return ""
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(dir)
}

// NotInTmuxError is returned by SwitchTo when invoked outside any tmux client
// ($TMUX unset). It carries the suggested command so a UI can show "run this".
type NotInTmuxError struct {
	Target  TmuxTarget
	Command string
}

func (e *NotInTmuxError) Error() string {
	return "not inside tmux; run: " + e.Command
}

// FocusMissError is returned by the cross-session focus path when osascript ran
// cleanly (iTerm2 is scriptable) but found NO native tab whose session name
// matched the token, i.e. the agent's tmux session is not attached to this
// iTerm2, or its tab title no longer carries the token. This is the QA-8 case
// the old code reported as a false "jumped": osascript exits 0 on a no-match, so
// without the sentinel the miss was invisible. Command carries the manual
// `tmux attach`/`switch-client` an operator can run to reach it.
type FocusMissError struct {
	Target  TmuxTarget
	Command string
}

func (e *FocusMissError) Error() string {
	return "could not raise the agent's iTerm2 tab (its tmux session may not be attached here); run: " + e.Command
}

// AttachCommand renders the manual command to reach a target session that is not
// currently a tab in this iTerm2: attach the session in a fresh client. It is
// the actionable companion to a FocusMiss (whereas SuggestJump's switch-client
// assumes the session is already attached somewhere).
func AttachCommand(t TmuxTarget) string {
	if t.Session == "" {
		return SuggestJump(t)
	}
	return "tmux attach -t " + t.Session
}

// switchExec is the injectable subprocess seam for the tmux side of SwitchTo.
// Production points it at the real tmux binary; tests swap it for a recorder. It
// is package-level so tests can override it without changing the SwitchTo
// signature.
var switchExec execRunner = defaultExecRunner

// osascriptExec is the injectable subprocess seam for the iTerm2 native-window
// raise on the CROSS-SESSION focus path (macOS osascript, always present, no
// new dependency, no python). Production runs the real osascript and reads its
// OK/MISS stdout sentinel; tests swap it for a recorder so they assert the EXACT
// argv AND drive the sentinel without spawning anything. When osascript fails
// (or is absent: non-macOS / not iTerm2) SwitchTo degrades to a tmux
// select-window + a NotInTmuxError-style note; when it runs but reports MISS the
// path returns a FocusMissError so the operator sees an honest "couldn't raise
// the tab" instead of a false "jumped".
var osascriptExec osaRunner = defaultOsaRunner

// inTmux reports whether the current process is inside a tmux client. Overridable
// in tests via tmuxEnv.
var tmuxEnv = func() string { return os.Getenv("TMUX") }

// currentTmuxSession reports the tmux session name the operator's client is
// attached to (`tmux display-message -p "#{session_name}"`), used to choose the
// same-session vs cross-session focus branch. It is a package-level seam so a
// test can fix the "current session" without a live tmux. Returns "" when not
// resolvable (no server / not in tmux), which forces the cross-session branch
// (the safe default: never switch-client across sessions).
var currentTmuxSession = func() string {
	out, err := exec.Command("tmux", "display-message", "-p", "#{session_name}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// SwitchTo moves the OPERATOR'S terminal focus to a resolved target pane. It is
// read-only w.r.t. squad state. Its only effect is terminal focus. The whole
// product runs iTerm2 in tmux CONTROL MODE (-CC), where each tmux WINDOW is its
// own native iTerm2 window/tab, so the focus strategy is split to avoid the
// window-explosion bug:
//
//   - SAME tmux session (target session == the client's current session): focus
//     the window IN-SESSION via `tmux select-window` (+ `tmux select-pane`),
//     NEVER `switch-client`. Same-session select-window under -CC raises the
//     right native tab with NO window explosion.
//   - DIFFERENT tmux session: do NOT use switch-client (that moves the client to
//     the other session and makes -CC spawn a native window per tmux window,
//     the "jumping separates all tabs into their own windows" bug). Instead raise
//     the iTerm2 native window/tab for the pane via osascript, matched by the
//     pane title token (amq:<session>:<role>) or the tmux window name. If
//     osascript fails (not macOS / not iTerm2 / no match) fall back to a
//     best-effort `tmux select-window` and return a *NotInTmuxError-style note
//     carrying the suggested manual command.
//   - Not in tmux at all ($TMUX unset): no switch is run; a best-effort
//     select-window is attempted and a *NotInTmuxError carrying the suggested
//     command is returned so the UI can surface it.
func SwitchTo(t TmuxTarget) error {
	return switchToWithSession(t, currentTmuxSession)
}

// switchToWithSession is SwitchTo with the current-session resolver injected, so
// the same-session vs cross-session branch is testable without a live tmux.
func switchToWithSession(t TmuxTarget, curSession func() string) error {
	// select-window targets the window (id @N / pane %N resolves to it / index
	// spec); select-pane targets the exact pane (%N / index spec). Splitting them
	// keeps each tmux command on its documented target type for contract-sourced
	// targets, which carry ids rather than session:window.pane indices.
	wspec, pspec := windowTarget(t), paneTarget(t)
	if tmuxEnv() == "" {
		// Not in a tmux client at all: best-effort select-window so an iTerm2 -CC
		// attached window raises, then report the suggested command.
		_ = switchExec("tmux", "select-window", "-t", wspec)
		return &NotInTmuxError{Target: t, Command: SuggestJump(t)}
	}

	cur := ""
	if curSession != nil {
		cur = curSession()
	}
	if cur != "" && cur == t.Session {
		// SAME session: select the window (and pane) in place. No switch-client,
		// so iTerm2 -CC does NOT explode the layout.
		if err := switchExec("tmux", "select-window", "-t", wspec); err != nil {
			return fmt.Errorf("tmux select-window -t %s: %w", wspec, err)
		}
		// Best-effort pane focus within the window (ignore failure: a 1-pane
		// window has nothing to select and tmux errors harmlessly).
		_ = switchExec("tmux", "select-pane", "-t", pspec)
		return nil
	}

	// DIFFERENT session (or current session unknown): raise the iTerm2 native
	// window via osascript. NEVER switch-client across sessions. The script
	// prints "OK" when it selected a tab and "MISS" when it scanned every window
	// and matched nothing; the exit code is 0 for BOTH, so we must read stdout.
	out, err := osascriptExec("osascript", iTermActivateArgs(t)...)
	if err == nil && out == focusOK {
		return nil
	}
	if err == nil {
		// osascript ran cleanly but found no matching tab (out == "MISS" or, defensively,
		// any non-OK output). The target session is not attached to this iTerm2: no
		// select-window here can raise a tab that doesn't exist, so do NOT pretend.
		return &FocusMissError{Target: t, Command: AttachCommand(t)}
	}
	// osascript itself failed (not macOS / not iTerm2): degrade to a best-effort
	// tmux select-window and surface the manual command.
	_ = switchExec("tmux", "select-window", "-t", wspec)
	return &NotInTmuxError{Target: t, Command: SuggestJump(t)}
}

// focusOK / focusMiss are the stdout sentinels the cross-session AppleScript
// prints so the Go caller can tell a real tab raise from a silent no-match
// (osascript exits 0 for both). They are package constants so the production
// script builder and the tests agree on the exact strings.
const (
	focusOK   = "OK"
	focusMiss = "MISS"
)

// iTermFocusToken is the string the cross-session osascript matches an iTerm2
// tab/window by: the pane title token (amq:<session>:<role>) when present, else
// the tmux window name, else the "session:window" spec. Exported-shaped as a
// helper so the argv is unit-testable.
func iTermFocusToken(t TmuxTarget) string {
	if s := strings.TrimSpace(t.Title); s != "" {
		return s
	}
	if s := strings.TrimSpace(t.WindowName); s != "" {
		return s
	}
	return targetSpec(t)
}

// iTermActivateArgs builds the osascript argv that activates iTerm2 and raises
// the window whose current session's name (the tab title, which iTerm2 -CC sets
// from the tmux pane title) contains the focus token. It is split out so tests
// assert the EXACT argv without spawning osascript. The AppleScript is passed via
// repeated -e lines (no shell, no interpolation surprises): the token is a
// separate -e-quoted literal so a title with spaces stays intact.
func iTermActivateArgs(t TmuxTarget) []string {
	token := iTermFocusToken(t)
	script := `on run argv
set tok to item 1 of argv
tell application "iTerm2"
	activate
	repeat with w in windows
		repeat with tb in tabs of w
			repeat with sess in sessions of tb
				if (name of sess) contains tok then
					select tb
					tell w to select
					return "` + focusOK + `"
				end if
			end repeat
		end repeat
	end repeat
end tell
return "` + focusMiss + `"
end run`
	return []string{"-e", script, token}
}

// SuggestJump returns the human "run this to jump" command for a target. It
// still renders the tmux switch-client form because that is the universally
// recognized manual command an operator can paste, even though SwitchTo itself
// no longer runs switch-client (it uses select-window / osascript to avoid the
// iTerm2 -CC window explosion).
func SuggestJump(t TmuxTarget) string {
	return "tmux switch-client -t " + targetSpec(t)
}

// targetSpec renders a TmuxTarget as tmux's "session:window.pane" addressing.
// Missing window/pane components are omitted gracefully.
func targetSpec(t TmuxTarget) string {
	spec := t.Session
	if t.Window != "" {
		spec += ":" + t.Window
		if t.Pane != "" {
			spec += "." + t.Pane
		}
	}
	return spec
}

// windowTarget is the -t argument for select-window: the tmux window id (@N) when
// known (doc-correct target-window), else the pane id (%N) (tmux resolves it to
// its window), else the session:window index spec. Used so a contract-sourced
// jump (which has ids, not indices) selects the exact window.
func windowTarget(t TmuxTarget) string {
	if s := strings.TrimSpace(t.WindowID); s != "" {
		return s
	}
	if s := strings.TrimSpace(t.PaneID); s != "" {
		return s
	}
	return targetSpec(t)
}

// paneTarget is the -t argument for select-pane: the pane id (%N) when known
// (the precise target-pane), else the session:window.pane index spec.
func paneTarget(t TmuxTarget) string {
	if s := strings.TrimSpace(t.PaneID); s != "" {
		return s
	}
	return targetSpec(t)
}
