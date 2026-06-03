package cli

import (
	"fmt"
	"os/exec"
	"time"

	"github.com/omriariav/amq-noc/internal/team"
)

func init() {
	registerTeamLaunchBackend(tmuxSessionTeamLaunchBackend{})
}

// tmuxSessionBinary is the user CLI this backend drives. It is a package var
// so tests can assert binary resolution without depending on the real wrapper
// being installed, and so the LIVE path that spawns iTerm2 -CC stays out of CI.
const tmuxSessionBinary = "tmux-session"

// tmuxSessionLookPath resolves the tmux-session wrapper on PATH. Indirected
// through a var so Validate can be unit-tested both ways (present/absent)
// without touching the real PATH or spawning anything.
var tmuxSessionLookPath = exec.LookPath

// tmuxSessionTeamLaunchBackend is the opt-in window-per-agent backend selected
// via `--terminal tmux-session`. Unlike the default tmux backend (which splits
// panes inside one window), it drives the user's `tmux-session` CLI so EACH
// agent lands in its own named iTerm2 window. The LIVE path goes through
// iTerm2 -CC and cannot be verified headlessly; only the emitted command shape
// is unit-tested. The default tmux backend is unchanged and stays the default.
type tmuxSessionTeamLaunchBackend struct{}

// tmuxSessionLaunchPlan is the resolved, backend-agnostic launch description
// the emitter and exec path both consume. Workstream is the AMQ session that
// also names the tmux-session session (one session, one window per agent).
type tmuxSessionLaunchPlan struct {
	Workstream string
	Panes      []teamLaunchPane
	StartDelay time.Duration
}

func (tmuxSessionTeamLaunchBackend) Name() string {
	return "tmux-session"
}

// Validate fails fast with an actionable message when the tmux-session wrapper
// is not on PATH, so the operator is told exactly how to recover (install it or
// fall back to the default tmux backend) before any pane work begins.
func (tmuxSessionTeamLaunchBackend) Validate(opts teamLaunchOptions) error {
	if opts.Stagger < 0 {
		return fmt.Errorf("--stagger cannot be negative")
	}
	if _, err := tmuxSessionLookPath(tmuxSessionBinary); err != nil {
		return fmt.Errorf("%s not found on PATH — install it or use --terminal tmux", tmuxSessionBinary)
	}
	return nil
}

func (b tmuxSessionTeamLaunchBackend) DryRun(t team.Team, opts teamLaunchOptions) error {
	printTmuxSessionLaunchPlan(b.buildPlan(t, opts))
	return nil
}

func (b tmuxSessionTeamLaunchBackend) Launch(t team.Team, opts teamLaunchOptions) error {
	return runTmuxSessionLaunchPlan(b.buildPlan(t, opts))
}

func (tmuxSessionTeamLaunchBackend) buildPlan(t team.Team, opts teamLaunchOptions) tmuxSessionLaunchPlan {
	return tmuxSessionLaunchPlan{
		Workstream: opts.Workstream,
		Panes:      buildTeamLaunchPanes(t, opts),
		StartDelay: opts.Stagger,
	}
}

// tmuxSessionCreateArgv is the PURE per-agent argv for adding (and attaching) a
// named window in the workstream session. Keeping it a standalone function
// means emission is unit-testable without spawning iTerm2: it is the single
// source of truth used by both the dry-run printer and the live exec path.
//
//	tmux-session --session <workstream> --create <role> <cwd>
func tmuxSessionCreateArgv(workstream, role, cwd string) []string {
	return []string{"--session", workstream, "--create", role, cwd}
}

// tmuxSessionRenameArgv is the PURE per-agent argv that stamps the window with
// the deterministic name-first jump token (amq:<session>:<role>) by renaming
// the just-created <role> window. Reusing paneTitleToken keeps the NOC
// resolver's expectedPaneToken in lockstep across both backends.
//
//	tmux-session --session <workstream> --rename <role> amq:<session>:<role>
func tmuxSessionRenameArgv(workstream, role string) []string {
	return []string{"--session", workstream, "--rename", role, paneTitleToken(workstream, role)}
}

// tmuxSessionResumeArgv is the PURE final argv that resumes/attaches the whole
// session for focus once every agent window exists.
//
//	tmux-session --session <workstream> --resume
func tmuxSessionResumeArgv(workstream string) []string {
	return []string{"--session", workstream, "--resume"}
}

func printTmuxSessionLaunchPlan(plan tmuxSessionLaunchPlan) {
	fmt.Println("# amq-squad team launch - tmux-session (window per agent)")
	if plan.Workstream != "" {
		fmt.Printf("# workstream: %s\n", plan.Workstream)
	}
	fmt.Printf("# windows: %d\n\n", len(plan.Panes))
	for _, line := range tmuxSessionDryRunLines(plan) {
		fmt.Println(line)
	}
}

// tmuxSessionDryRunLines renders the faithful, copy-pasteable preview for the
// window-per-agent plan. For each agent it emits, in order: a --create for the
// agent's own named window, a tmux send-keys that types the agent command into
// that window, and a --rename that stamps the amq:<session>:<role> jump token.
// A trailing --resume focuses the session. The emitted lines mirror exactly
// what runTmuxSessionLaunchPlan executes.
func tmuxSessionDryRunLines(plan tmuxSessionLaunchPlan) []string {
	if len(plan.Panes) == 0 {
		return nil
	}
	lines := make([]string, 0, len(plan.Panes)*3+1)
	for i, pane := range plan.Panes {
		lines = append(lines,
			shellCommand(tmuxSessionBinary, tmuxSessionCreateArgv(plan.Workstream, pane.Role, pane.CWD)...),
			tmuxSessionSendKeysDryRunLine(plan.Workstream, pane.Role, pane.Command),
			shellCommand(tmuxSessionBinary, tmuxSessionRenameArgv(plan.Workstream, pane.Role)...),
		)
		if i < len(plan.Panes)-1 && plan.StartDelay > 0 {
			lines = append(lines, sleepDryRunLine(plan.StartDelay))
		}
	}
	lines = append(lines, shellCommand(tmuxSessionBinary, tmuxSessionResumeArgv(plan.Workstream)...))
	return lines
}

// tmuxSessionSendKeysDryRunLine targets the agent's own named window by its
// "<session>:<role>" tmux target (the window-per-agent equivalent of the
// pane-id targeting the default backend uses) and types the agent command.
func tmuxSessionSendKeysDryRunLine(workstream, role, command string) string {
	return tmuxSendKeysDryRunLine(workstream+":"+role, command)
}

func runTmuxSessionLaunchPlan(plan tmuxSessionLaunchPlan) error {
	if len(plan.Panes) == 0 {
		return fmt.Errorf("tmux-session plan has no panes")
	}
	for i, pane := range plan.Panes {
		if err := runCommand(tmuxSessionBinary, tmuxSessionCreateArgv(plan.Workstream, pane.Role, pane.CWD)...); err != nil {
			return err
		}
		if err := runCommand("tmux", "send-keys", "-t", plan.Workstream+":"+pane.Role, pane.Command, "C-m"); err != nil {
			return err
		}
		if err := runCommand(tmuxSessionBinary, tmuxSessionRenameArgv(plan.Workstream, pane.Role)...); err != nil {
			return err
		}
		if i < len(plan.Panes)-1 && plan.StartDelay > 0 {
			time.Sleep(plan.StartDelay)
		}
	}
	if err := runCommand(tmuxSessionBinary, tmuxSessionResumeArgv(plan.Workstream)...); err != nil {
		return err
	}
	quietNotice("Opened %d agent window(s) in tmux-session %s.\n", len(plan.Panes), shellQuote(plan.Workstream))
	verbosePolicyEcho()
	return nil
}
