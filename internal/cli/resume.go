package cli

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/omriariav/amq-noc/internal/team"
)

func runResume(args []string) error {
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	sessionFlag := fs.String("session", "", "AMQ workstream session name to resume into (default: team workstream)")
	restoreExisting := fs.Bool("restore-existing", false, "fail if no team member has restorable launch records for the workstream")
	dryRun := fs.Bool("dry-run", false, "plan-only; default behavior is already plan-only and exists for parity with other commands")
	forceDuplicate := fs.Bool("force-duplicate", false, "include commands even when a live agent is detected for a member")
	noBootstrap := fs.Bool("no-bootstrap", false, "emit fresh launch commands that skip the generated bootstrap prompt")
	trustRaw := fs.String("trust", "", "Codex trust profile for fresh members: sandboxed (default) or trusted")
	modelFlag := fs.String("model", "", "per-persona model overrides for fresh members, e.g. cto=gpt-5,fullstack=sonnet")
	codexArgsRaw := fs.String("codex-args", "", "extra Codex args for fresh members, e.g. '--enable goals'")
	claudeArgsRaw := fs.String("claude-args", "", "extra Claude args for fresh members, e.g. '--chrome'")
	projectFlag := fs.String("project", "", "project/team-home directory to resume (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to resume (default: default profile)")
	execMode := fs.Bool("exec", false, "open the planned launch commands in the terminal backend (tmux) instead of printing them")
	terminal := fs.String("terminal", "tmux", "terminal backend to use with --exec")
	target := fs.String("target", "current-window", "terminal target with --exec (tmux: current-window or new-session)")
	layout := fs.String("layout", "vertical", "terminal layout with --exec (tmux: vertical, horizontal, or tiled)")
	terminalSession := fs.String("terminal-session", "", "terminal session name when --exec --target new-session")
	stagger := fs.Duration("stagger", 750*time.Millisecond, "delay between starting agent panes with --exec")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad resume - bring the team back from launch records

Usage:
  amq-squad resume [--project DIR] [--profile NAME] [--session name] [--restore-existing]
                   [--dry-run] [--force-duplicate]
                   [--no-bootstrap] [--trust sandboxed|trusted]
                   [--model role=model,...]
                   [--codex-args args] [--claude-args args]
                   [--exec [--terminal tmux] [--target current-window|new-session]
                           [--layout vertical|horizontal|tiled]
                           [--terminal-session name] [--stagger 750ms]]

Resume an existing session. Inspects .amq-squad/team.json plus local launch
history and live-agent signals (wake locks, agent PID liveness, presence) to
choose a per-member action: restore from launch.json, launch fresh from team
intent, skip if live, or refuse if blocked.
--project targets another team-home without changing directories.

If an agent has a saved conversation, amq-squad reattaches to it. Otherwise it
starts the agent fresh and re-orients it: bootstrap is re-run so the agent
re-reads its session brief and AMQ history. In the re-orient case prior hidden
reasoning is not replayed -- only persisted session files and messages are used.

Default behavior is plan-only: prints the per-member action table plus
copy-pasteable commands. With --exec, opens those commands through the
selected terminal backend (same path as 'up'), skipping members that are
already live and refusing to start if any member is in the 'blocked'
action without --force-duplicate.

Fresh / new-session behavior belongs to 'amq-squad fork --from S --as T'.

Examples:
  amq-squad resume
  amq-squad resume --project ~/Code/app --session issue-96
  amq-squad resume --session issue-96 --restore-existing
  amq-squad resume --exec
  amq-squad resume --exec --target new-session --terminal-session squad
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *execMode && *dryRun {
		return usageErrorf("--exec and --dry-run are mutually exclusive")
	}

	profile, err := resolveProfileFlag(*profileFlag)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	projectDir, err := resolveProjectDirFlag(cwd, *projectFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	if !team.ExistsProfile(projectDir, profile) {
		return fmt.Errorf("no team configured for profile %q. Run '%s' first.", profile, profileInitCommand(profile))
	}
	mode := resumeModeDefault
	if *restoreExisting {
		mode = resumeModeRestoreExisting
	}
	exec := resumeExecOptions{}
	if *execMode {
		exec = resumeExecOptions{
			Enabled:         true,
			Terminal:        *terminal,
			Target:          *target,
			Layout:          *layout,
			TerminalSession: *terminalSession,
			Stagger:         *stagger,
		}
	}
	return executeResume(resumeExecution{
		ProjectDir:       projectDir,
		RequestedSession: *sessionFlag,
		ExplicitSession:  flagWasSet(fs, "session"),
		Mode:             mode,
		Force:            *forceDuplicate,
		NoBootstrap:      *noBootstrap,
		TrustRaw:         *trustRaw,
		ExplicitTrust:    flagWasSet(fs, "trust"),
		ModelRaw:         *modelFlag,
		CodexArgsRaw:     *codexArgsRaw,
		ClaudeArgsRaw:    *claudeArgsRaw,
		DryRun:           *dryRun,
		Profile:          profile,
		Style:            resumePrinterStyle{Label: "resume", FooterVerb: "up"},
		Exec:             exec,
	})
}
