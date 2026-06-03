package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/omriariav/amq-noc/internal/console"
	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/state"
	"github.com/omriariav/amq-noc/internal/team"
)

// runConsole is the `amq-squad console` verb: a read-only, full-screen Mission
// Control TUI over every discovered session, or — with --once — a single static
// text board to stdout for non-TTY / CI use.
//
// The interactive TUI renders to /dev/tty (never os.Stdout), so the
// stdout-purity contract the other verbs' tests assert stays intact. --once is
// the sole path that writes the board to stdout.
//
// It degrades gracefully: no team configured (or no TTY) emits guidance; an
// unresolvable AMQ base root degrades to an explanatory state rather than
// crashing.
func runConsole(args []string) error {
	fs := flag.NewFlagSet("console", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory to inspect (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to inspect (default: default profile)")
	sessionFlag := fs.String("session", "", "scope the console to a single session (default: all sessions)")
	refresh := fs.Duration("refresh", console.DefaultRefresh, "periodic resync cadence (e.g. 2s)")
	atRiskWait := fs.Duration("at-risk-wait", state.DefaultAtRiskWait, "an awaiting-reply thread older than this is at risk")
	reviewAge := fs.Duration("review-age", state.DefaultReviewAge, "an unanswered review/question older than this is at risk")
	staleAfter := fs.Duration("stale-after", state.DefaultStaleAfter, "a thread untouched longer than this is stale")
	filter := fs.String("filter", "", "start with this filter (e.g. needs-you, agent:cto, session:issue-96)")
	once := fs.Bool("once", false, "render one static board to stdout and exit (non-TTY / CI)")
	// --root makes console reach the SAME multi-root NOC command center the `noc`
	// verb drives. It is repeatable; given one or more, console hands off to NOC.
	var roots rootList
	fs.Var(&roots, "root", "scan this directory for amq-squad projects via the multi-root NOC view (repeatable)")
	depth := fs.Int("depth", noc.DefaultDepth, "NOC scan depth under each --root")
	tree := fs.Bool("tree", false, "with --root --once: render the full NOC tree instead of the rollup digest")
	all := fs.Bool("all", false, "alias for --tree when using --root")
	hideStale := fs.Bool("hide-stale", false, "with --root: hide stopped/archived (stale) squads")
	noBell := fs.Bool("no-bell", false, "with --root: mute needs-you alerts")
	jsonOut := fs.Bool("json", false, "with --root: emit a schema-versioned noc_snapshot envelope and exit")
	actionsOut := fs.Bool("actions", false, "with --root: emit the flat NOC action queue and exit")
	actionFilter := fs.String("action", "", "with --root --actions: only include action names matching this comma-separated list")
	actionIDFilter := fs.String("action-id", "", "with --root --actions: only include exact action IDs matching this comma-separated list")
	targetIDFilter := fs.String("target-id", "", "with --root --actions: only include exact target row IDs matching this comma-separated list")
	scopeFilter := fs.String("scope", "", "with --root --actions: only include scopes matching this comma-separated list (project,session,agent)")
	runActionID := fs.String("run-action", "", "with --root: execute one NOC action by exact action ID or unique action name")
	var actionVars nocTemplateVars
	fs.Var(&actionVars, "set", "with --root --run-action: fill template variable key=value")
	actionDryRun := fs.Bool("dry-run", false, "with --root --run-action: resolve and preview the action without executing it")
	yes := fs.Bool("yes", false, "with --root --run-action: skip mutating action confirmation")
	fs.BoolVar(yes, "y", false, "shorthand for --yes")
	mutatingOnly := fs.Bool("mutating", false, "with --root --actions: only include mutating actions")
	commandsOnly := fs.Bool("commands", false, "with --root --actions: print selected action commands only")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad console - live read-only Mission Control over your sessions

Usage:
  amq-squad console [--project DIR] [--profile NAME] [--session NAME] [--refresh 2s]
                    [--at-risk-wait 5m] [--review-age 15m] [--stale-after 72h]
                    [--filter EXPR] [--once]
  amq-squad console --root DIR [--root DIR ...] [--depth N] [--filter EXPR]
                    [--hide-stale] [--stale-after 72h] [--once|--json|--actions]
                    [--action NAME[,NAME]] [--action-id ID[,ID]]
                    [--target-id ID[,ID]] [--scope project,session,agent]
                    [--mutating] [--commands]
  amq-squad console --root DIR --run-action ID_OR_NAME [--set key=value ...] [--dry-run] [--yes|-y]

A full-screen, read-only TUI showing every discovered session, its triage
rollup (needs-you / blocked / gated / at-risk), and per-agent liveness. The TUI
renders to your terminal (/dev/tty); stdout stays clean.
--project targets another team-home without changing directories.
--session is shorthand for --filter session:<name>; use --filter for broader
typed filters such as needs-you, gated, agent:<handle>, model:<engine>, or bare text.

With one or more --root it reaches the multi-root NOC command center across
EVERY discovered project under those roots (the same surface as 'amq-squad noc').
--project and --root are mutually exclusive.

With --once it renders a single static board to STDOUT and exits — use this
in CI or when there is no terminal attached.

Examples:
  amq-squad console
  amq-squad console --project ~/Code/app --once
  amq-squad console --once
  amq-squad console --root ~/Code --once
  amq-squad console --root ~/Code --filter needs-you --json
  amq-squad console --root ~/Code --actions --filter needs-you
  amq-squad console --root ~/Code --actions --action resume --mutating
  amq-squad console --root ~/Code --actions --scope session --mutating
  amq-squad console --root ~/Code --actions --action resume --commands
  amq-squad console --root ~/Code --filter project:app --run-action new_session --set session=issue-97 --dry-run
  amq-squad console --session issue-96 --at-risk-wait 5m
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	if len(roots) > 0 && flagWasSet(fs, "project") {
		return usageErrorf("--project cannot be used with --root; --root selects the multi-root NOC surface")
	}
	if len(roots) == 0 {
		for _, name := range []string{"tree", "all", "hide-stale", "no-bell", "json", "actions", "action", "action-id", "target-id", "scope", "run-action", "set", "dry-run", "yes", "y", "mutating", "commands"} {
			if flagWasSet(fs, name) {
				return usageErrorf("--%s requires --root; use 'amq-squad noc --%s' for the multi-root surface", name, name)
			}
		}
	}
	runActionSet := strings.TrimSpace(*runActionID) != ""
	if !*actionsOut && !runActionSet && nocActionSelectorFlagWasSet(fs) {
		return usageErrorf("--action, --action-id, --target-id, --scope, --mutating, and --commands require --actions")
	}
	if runActionSet && *actionsOut {
		return usageErrorf("--run-action cannot be combined with --actions")
	}
	if runActionSet && nocActionSelectorFlagWasSet(fs) {
		return usageErrorf("--run-action cannot be combined with --action, --action-id, --target-id, --scope, --mutating, or --commands")
	}
	if *commandsOnly && *jsonOut {
		return usageErrorf("--commands cannot be used with --json; use --actions --json for a noc_actions envelope")
	}
	if runActionSet && *jsonOut && !*actionDryRun {
		return usageErrorf("--run-action --json requires --dry-run; use --actions --json to inspect actions")
	}
	if !runActionSet && flagWasSet(fs, "dry-run") {
		return usageErrorf("--dry-run requires --run-action")
	}
	if !runActionSet && flagWasSet(fs, "set") {
		return usageErrorf("--set requires --run-action")
	}
	if !runActionSet && (flagWasSet(fs, "yes") || flagWasSet(fs, "y")) {
		return usageErrorf("--yes/-y requires --run-action")
	}
	effectiveFilter := strings.TrimSpace(*filter)
	if *sessionFlag != "" && effectiveFilter != "" {
		return usageErrorf("--session cannot be used with --filter; use --filter session:%s", *sessionFlag)
	}
	if *sessionFlag != "" {
		effectiveFilter = "session:" + *sessionFlag
	}

	// --root routes console to the multi-root NOC command center, so both verbs
	// reach the same surface.
	if len(roots) > 0 {
		return executeNOC(nocExecution{
			Cwd:              cwd,
			Roots:            []string(roots),
			Depth:            *depth,
			Refresh:          *refresh,
			AtRiskWait:       *atRiskWait,
			ReviewAge:        *reviewAge,
			StaleAfter:       *staleAfter,
			Filter:           effectiveFilter,
			Once:             *once,
			Tree:             *tree || *all,
			HideStale:        *hideStale,
			NoBell:           *noBell,
			JSON:             *jsonOut,
			Actions:          *actionsOut,
			ActionFilter:     *actionFilter,
			ActionIDFilter:   *actionIDFilter,
			TargetIDFilter:   *targetIDFilter,
			ScopeFilter:      *scopeFilter,
			RunActionID:      *runActionID,
			ActionVars:       map[string]string(actionVars),
			DryRun:           *actionDryRun,
			Yes:              *yes,
			MutatingOnly:     *mutatingOnly,
			CommandsOnly:     *commandsOnly,
			Out:              os.Stdout,
			Confirm:          nocActionConfirmOverride,
			StdoutIsTTY:      outputIsTTY(),
			RunActionCommand: nocActionRunnerOverride,
			RunNOC:           console.RunNOC,
		})
	}
	projectDir, err := resolveProjectDirFlag(cwd, *projectFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}

	return executeConsole(consoleExecution{
		ProjectDir:  projectDir,
		Profile:     *profileFlag,
		Session:     "",
		Refresh:     *refresh,
		AtRiskWait:  *atRiskWait,
		ReviewAge:   *reviewAge,
		StaleAfter:  *staleAfter,
		Filter:      effectiveFilter,
		Once:        *once,
		Out:         os.Stdout,
		Stderr:      os.Stderr,
		TeamExists:  team.ExistsProfile,
		ResolveBase: scanBaseRootForProject,
		StdoutIsTTY: outputIsTTY(),
		RunConsole:  console.Run,
	})
}

// consoleExecution carries the inputs for the console verb so tests can drive
// flag parsing + dispatch with seams (no real TTY, an injected resolver and a
// captured RunConsole) without ever starting a Bubble Tea program.
type consoleExecution struct {
	ProjectDir string
	Profile    string
	Session    string
	Refresh    time.Duration
	AtRiskWait time.Duration
	ReviewAge  time.Duration
	StaleAfter time.Duration
	Filter     string
	Once       bool
	Out        io.Writer
	Stderr     io.Writer

	// Seams.
	TeamExists  func(projectDir, profile string) bool
	ResolveBase func(projectDir string) (string, error)
	StdoutIsTTY bool
	// RunConsole runs Mission Control. Injected so tests assert the assembled
	// Config without launching a real program; production passes console.Run.
	RunConsole func(console.Config) error
}

// executeConsole resolves the profile, the AMQ base root, and the team/TTY
// gating, then hands an assembled console.Config to RunConsole. The gating
// rules:
//
//   - No team configured + (--once OR no TTY): write clear guidance to stdout
//     and exit 0 (it is not a crash to have no team). Returns nonzero only when
//     truly unusable.
//   - No team configured + TTY: hand a NoTeam notice to the console so it shows
//     the explanatory failure-state screen instead of aborting.
//   - Unresolvable base root: degrade to a NoTeam-style guidance state (notice
//     names the `amq env` probe), never panic.
func executeConsole(s consoleExecution) error {
	profile, err := resolveProfileFlag(s.Profile)
	if err != nil {
		return err
	}

	// A --filter preset is handed to the console's typed-filter machinery, so the
	// same predicates apply in every view and in the --once board. --session is a
	// convenience shorthand for the common single-session scope.
	initialFilter := strings.TrimSpace(s.Filter)
	if s.Session != "" {
		if initialFilter != "" {
			return usageErrorf("--session cannot be used with --filter; use --filter session:%s", s.Session)
		}
		initialFilter = "session:" + s.Session
	}

	teamExists := s.TeamExists != nil && s.TeamExists(s.ProjectDir, profile)

	// No team configured. On a non-interactive surface (--once or no TTY) this is
	// guidance to stdout, not a crash. On an interactive surface, hand the
	// console a NoTeam notice so it shows the explanatory screen.
	if !teamExists {
		notice := fmt.Sprintf("amq-squad: no team configured for profile %q. "+
			"Run '%s' first, then 'amq-squad console'.",
			profile, profileInitCommand(profile))
		if s.Once || !s.StdoutIsTTY {
			fmt.Fprintln(s.Out, notice)
			return nil
		}
		return s.RunConsole(console.Config{
			ProjectDir:   s.ProjectDir,
			Once:         false,
			Out:          s.Out,
			NoTeamNotice: notice,
		})
	}

	// Team exists: resolve the AMQ base root once. An unresolvable root degrades
	// to a guidance/NoTeam state naming the probe, never a panic.
	var baseRoot string
	if s.ResolveBase != nil {
		baseRoot, err = s.ResolveBase(s.ProjectDir)
	}
	notice := ""
	if err != nil || baseRoot == "" {
		notice = "amq-squad: could not resolve the AMQ base root via `amq env` " +
			"(is `amq` installed and on PATH?)."
		if err != nil {
			notice += " " + err.Error()
		}
	}

	cfg := console.Config{
		ProjectDir: s.ProjectDir,
		BaseRoot:   baseRoot,
		Thresholds: state.Thresholds{
			AtRiskWait: s.AtRiskWait,
			ReviewAge:  s.ReviewAge,
			StaleAfter: s.StaleAfter,
		},
		Refresh:       s.Refresh,
		Once:          s.Once,
		Out:           s.Out,
		NoTeamNotice:  notice,
		InitialFilter: initialFilter,
	}

	// Non-interactive (--once) on a degraded root still emits guidance to stdout
	// and exits 0; the console.runOnce path handles the notice.
	if !s.Once && !s.StdoutIsTTY && notice == "" {
		// Interactive requested but no TTY and a healthy root: fall back to a
		// single static board on stdout rather than failing to open /dev/tty.
		cfg.Once = true
	}

	return s.RunConsole(cfg)
}
