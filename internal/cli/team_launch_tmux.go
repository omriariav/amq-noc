package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-noc/internal/team"
)

func init() {
	registerTeamLaunchBackend(tmuxTeamLaunchBackend{})
}

type tmuxTeamLaunchBackend struct{}

type tmuxLaunchPlan struct {
	Session    string
	Workstream string
	Target     string
	Layout     string
	Panes      []teamLaunchPane
	StartDelay time.Duration
}

type tmuxClient struct {
	TTY         string
	ControlMode bool
	Flags       string
}

func (tmuxTeamLaunchBackend) Name() string {
	return "tmux"
}

func (tmuxTeamLaunchBackend) Validate(opts teamLaunchOptions) error {
	if opts.Target != "new-session" && opts.Target != "current-window" {
		return fmt.Errorf("unsupported tmux target %q: use new-session or current-window", opts.Target)
	}
	if opts.Layout != "vertical" && opts.Layout != "horizontal" && opts.Layout != "tiled" {
		return fmt.Errorf("unsupported tmux layout %q: use vertical, horizontal, or tiled", opts.Layout)
	}
	if opts.Stagger < 0 {
		return fmt.Errorf("--stagger cannot be negative")
	}
	return nil
}

func (b tmuxTeamLaunchBackend) DryRun(t team.Team, opts teamLaunchOptions) error {
	printTmuxLaunchPlan(b.buildPlan(t, opts))
	return nil
}

func (b tmuxTeamLaunchBackend) Launch(t team.Team, opts teamLaunchOptions) error {
	plan := b.buildPlan(t, opts)
	controlClients := tmuxControlModeClients()
	if len(controlClients) > 0 {
		warnTmuxControlModeClients(controlClients)
	}
	return runTmuxLaunchPlan(plan)
}

func (tmuxTeamLaunchBackend) buildPlan(t team.Team, opts teamLaunchOptions) tmuxLaunchPlan {
	if opts.TerminalSession == "" {
		opts.TerminalSession = defaultTmuxSessionName(t.Project)
	}
	return buildTmuxLaunchPlan(t, opts)
}

func buildTmuxLaunchPlan(t team.Team, opts teamLaunchOptions) tmuxLaunchPlan {
	return tmuxLaunchPlan{
		Session:    opts.TerminalSession,
		Workstream: opts.Workstream,
		Target:     opts.Target,
		Layout:     opts.Layout,
		Panes:      buildTeamLaunchPanes(t, opts),
		StartDelay: opts.Stagger,
	}
}

func defaultTmuxSessionName(projectDir string) string {
	base := filepath.Base(projectDir)
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = "project"
	}
	return "amq-squad-" + sanitizeTmuxSessionName(base)
}

func sanitizeTmuxSessionName(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(s) {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "project"
	}
	return out
}

func printTmuxLaunchPlan(plan tmuxLaunchPlan) {
	fmt.Println("# amq-squad team launch - tmux")
	fmt.Printf("# target:  %s\n", plan.Target)
	fmt.Printf("# layout:  %s\n", plan.Layout)
	if plan.Workstream != "" {
		fmt.Printf("# workstream: %s\n", plan.Workstream)
	}
	fmt.Printf("# session: %s\n", plan.Session)
	fmt.Printf("# panes:   %d\n\n", len(plan.Panes))
	for _, line := range tmuxDryRunLines(plan) {
		fmt.Println(line)
	}
}

func tmuxDryRunLines(plan tmuxLaunchPlan) []string {
	if len(plan.Panes) == 0 {
		return nil
	}
	windowTarget := plan.Session + ":0"
	firstTarget := plan.Session + ":0.0"
	lines := []string{}
	if plan.Target == "current-window" {
		windowTarget = "$window"
		firstTarget = "$first_pane"
		lines = append(lines,
			`window=$(tmux display-message -p -t "${TMUX_PANE:?run amq-squad start from inside a tmux pane}" '#{session_name}:#{window_index}')`,
			`first_pane="$TMUX_PANE"`,
		)
	} else {
		lines = append(lines, shellCommand("tmux", "new-session", "-d", "-s", plan.Session, "-n", "squad", "-c", plan.Panes[0].CWD))
	}
	targets := []string{firstTarget}
	lines = append(lines, tmuxSelectPaneDryRunLine(firstTarget, paneTitleToken(plan.Session, plan.Panes[0].Role)))
	for i, pane := range plan.Panes[1:] {
		paneVar := fmt.Sprintf("pane_%d", i+1)
		targets = append(targets, "$"+paneVar)
		lines = append(lines,
			tmuxSplitDryRunLine(paneVar, windowTarget, pane.CWD, plan.Layout),
			tmuxSelectPaneDryRunLine("$"+paneVar, paneTitleToken(plan.Session, pane.Role)),
		)
	}
	if len(plan.Panes) > 1 {
		lines = append(lines, tmuxSelectLayoutDryRunLine(windowTarget, plan.Layout))
	}
	for i, pane := range plan.Panes {
		lines = append(lines, tmuxSendKeysDryRunLine(targets[i], pane.Command))
		if i < len(plan.Panes)-1 && plan.StartDelay > 0 {
			lines = append(lines, sleepDryRunLine(plan.StartDelay))
		}
	}
	if plan.Target == "current-window" {
		lines = append(lines, "# using current tmux window; no attach needed")
	} else {
		lines = append(lines, "# attach later with: "+shellCommand("tmux", "attach-session", "-t", plan.Session))
	}
	return lines
}

func tmuxSplitDryRunLine(varName, target, cwd, layout string) string {
	return varName + "=$(tmux split-window -P -F '#{pane_id}' -t " + shellTarget(target) + " " + tmuxSplitDirection(layout) + " -c " + shellQuote(cwd) + ")"
}

func tmuxSelectLayoutDryRunLine(target, layout string) string {
	return "tmux select-layout -t " + shellTarget(target) + " " + tmuxSelectLayout(layout)
}

func shellTarget(target string) string {
	if strings.HasPrefix(target, "$") {
		return `"` + target + `"`
	}
	return shellQuote(target)
}

func sleepDryRunLine(d time.Duration) string {
	seconds := fmt.Sprintf("%.3f", d.Seconds())
	seconds = strings.TrimRight(strings.TrimRight(seconds, "0"), ".")
	if seconds == "" {
		seconds = "0"
	}
	return "sleep " + seconds
}

func tmuxSelectPaneDryRunLine(target, title string) string {
	return "tmux select-pane -t " + shellTarget(target) + " -T " + shellQuote(title)
}

// paneTitleToken builds the deterministic, machine-parseable pane title that the
// NOC jump resolves name-first: "amq:<session>:<role>". The role is unique per
// agent within a session, so two agents that share a repo AND an engine (the
// bug: cpo·codex + cto·codex in the same dir) still get distinct titles. The
// token doubles as a human-facing label since it carries the role. When the role
// is empty it is omitted, leaving "amq:<session>:" — callers always pass a role
// for real members, but this stays parseable. MUST stay in lockstep with the
// resolver's expectedPaneToken in internal/noc/tmux.go.
func paneTitleToken(session, role string) string {
	return "amq:" + session + ":" + role
}

func tmuxSendKeysDryRunLine(target, command string) string {
	return "tmux send-keys -t " + shellTarget(target) + " " + shellQuote(command) + " C-m"
}

func runTmuxLaunchPlan(plan tmuxLaunchPlan) error {
	if len(plan.Panes) == 0 {
		return fmt.Errorf("tmux plan has no panes")
	}
	windowTarget := plan.Session + ":0"
	firstTarget := plan.Session + ":0.0"
	switch plan.Target {
	case "current-window":
		paneID := strings.TrimSpace(os.Getenv("TMUX_PANE"))
		if os.Getenv("TMUX") == "" || paneID == "" {
			return fmt.Errorf("--target current-window requires running inside tmux (TMUX_PANE unset); attach a tmux session and launch from one of its panes, or use --target new-session")
		}
		// Anchor on the launching shell's own pane ($TMUX_PANE), never tmux's
		// focused pane. `tmux display-message -p` without -t reports whichever
		// window is active *now*, so under iTerm2 -CC a focus change mid-launch
		// rehomes the panes onto an unrelated tab (#40). Resolving the window
		// from TMUX_PANE with -t makes targeting deterministic regardless of focus.
		var err error
		windowTarget, err = outputCommand("tmux", "display-message", "-p", "-t", paneID, "#{session_name}:#{window_index}")
		if err != nil {
			return err
		}
		windowTarget = strings.TrimSpace(windowTarget)
		firstTarget = paneID
	case "new-session":
		if err := tmuxEnsureSessionAbsent(plan.Session); err != nil {
			return err
		}
		if err := runCommand("tmux", "new-session", "-d", "-s", plan.Session, "-n", "squad", "-c", plan.Panes[0].CWD); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported tmux target %q", plan.Target)
	}
	if err := runCommand("tmux", "select-pane", "-t", firstTarget, "-T", paneTitleToken(plan.Session, plan.Panes[0].Role)); err != nil {
		return err
	}
	targets := []string{firstTarget}
	for _, pane := range plan.Panes[1:] {
		paneID, err := outputCommand("tmux", "split-window", "-P", "-F", "#{pane_id}", "-t", windowTarget, tmuxSplitDirection(plan.Layout), "-c", pane.CWD)
		if err != nil {
			return err
		}
		paneID = strings.TrimSpace(paneID)
		if paneID == "" {
			return fmt.Errorf("tmux split-window returned empty pane id")
		}
		targets = append(targets, paneID)
		if err := runCommand("tmux", "select-pane", "-t", paneID, "-T", paneTitleToken(plan.Session, pane.Role)); err != nil {
			return err
		}
	}
	if len(plan.Panes) > 1 {
		if err := runCommand("tmux", "select-layout", "-t", windowTarget, tmuxSelectLayout(plan.Layout)); err != nil {
			return err
		}
	}
	for i, pane := range plan.Panes {
		if err := runCommand("tmux", "send-keys", "-t", targets[i], pane.Command, "C-m"); err != nil {
			return err
		}
		if i < len(plan.Panes)-1 && plan.StartDelay > 0 {
			time.Sleep(plan.StartDelay)
		}
	}
	if plan.Target == "current-window" {
		quietNotice("Added team panes to current tmux window.\n")
		verbosePolicyEcho()
		return nil
	}
	quietNotice("Created tmux session %s. Attach with: tmux attach -t %s\n", plan.Session, shellQuote(plan.Session))
	verbosePolicyEcho()
	return nil
}

func tmuxSplitDirection(layout string) string {
	if layout == "horizontal" {
		return "-v"
	}
	return "-h"
}

func tmuxSelectLayout(layout string) string {
	switch layout {
	case "horizontal":
		return "even-vertical"
	case "vertical":
		return "even-horizontal"
	default:
		return "tiled"
	}
}

func tmuxControlModeClients() []tmuxClient {
	cmd := exec.Command("tmux", "list-clients", "-F", "#{client_tty}\t#{client_control_mode}\t#{client_flags}")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseTmuxClients(string(out))
}

func parseTmuxClients(out string) []tmuxClient {
	var clients []tmuxClient
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 {
			continue
		}
		flags := ""
		if len(parts) == 3 {
			flags = parts[2]
		}
		control := parts[1] == "1" || strings.Contains(flags, "control-mode")
		if !control {
			continue
		}
		clients = append(clients, tmuxClient{
			TTY:         parts[0],
			ControlMode: true,
			Flags:       flags,
		})
	}
	return clients
}

func warnTmuxControlModeClients(clients []tmuxClient) {
	fmt.Fprintf(os.Stderr, "warning: detected %d tmux control-mode client(s). iTerm2 tmux -CC can pause when several agent TUIs start at once.\n", len(clients))
	for _, c := range clients {
		fmt.Fprintf(os.Stderr, "warning: control client %s flags: %s\n", c.TTY, c.Flags)
	}
	fmt.Fprintln(os.Stderr, "warning: starting panes with a stagger to reduce the initial output burst.")
	fmt.Fprintln(os.Stderr, "warning: if input stalls, recover from a non-tmux shell with: tmux detach-client -t <tty>")
	fmt.Fprintln(os.Stderr, "warning: consider raising the control client limit with: tmux refresh-client -f pause-after=0")
}

func tmuxEnsureSessionAbsent(session string) error {
	err := exec.Command("tmux", "has-session", "-t", session).Run()
	if err == nil {
		return fmt.Errorf("tmux session %q already exists. Attach with 'tmux attach -t %s' or choose --terminal-session", session, session)
	}
	return nil
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("%s: %w: %s", shellCommand(name, args...), err, msg)
		}
		return fmt.Errorf("%s: %w", shellCommand(name, args...), err)
	}
	return nil
}

func outputCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("%s: %w: %s", shellCommand(name, args...), err, msg)
		}
		return "", fmt.Errorf("%s: %w", shellCommand(name, args...), err)
	}
	return string(out), nil
}
