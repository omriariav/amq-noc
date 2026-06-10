package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/omriariav/amq-noc/internal/act"
	"github.com/omriariav/amq-noc/internal/catalog"
	"github.com/omriariav/amq-noc/internal/console"
	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/rules"
	"github.com/omriariav/amq-noc/internal/state"
	"github.com/omriariav/amq-noc/internal/team"
)

// rootList is a repeatable string flag: each --root DIR appends one root. The
// stdlib flag package has no built-in slice flag, so this implements flag.Value.
type rootList []string

func (r *rootList) String() string {
	if r == nil {
		return ""
	}
	return fmt.Sprint([]string(*r))
}

func (r *rootList) Set(v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return fmt.Errorf("--root requires a directory")
	}
	dir, err := expandPath(v)
	if err != nil {
		return fmt.Errorf("resolve --root: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("--root %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("--root %s is not a directory", dir)
	}
	*r = append(*r, dir)
	return nil
}

type nocTemplateVars map[string]string

func (v *nocTemplateVars) String() string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(map[string]string(*v))
}

func (v *nocTemplateVars) Set(s string) error {
	eq := strings.IndexByte(s, '=')
	if eq <= 0 || eq == len(s)-1 {
		return fmt.Errorf("expected key=value, got %q", s)
	}
	if *v == nil {
		*v = nocTemplateVars{}
	}
	key := normalizeNOCActionVarKey(s[:eq])
	if key == "" {
		return fmt.Errorf("expected key=value, got %q", s)
	}
	(*v)[key] = strings.TrimSpace(s[eq+1:])
	return nil
}

var (
	nocActionRunnerOverride  func(string) error
	nocActionConfirmOverride io.Reader
)

// runNOC is the `amq-noc noc` verb: the full-screen NOC command center over
// EVERY discovered amq-squad project under one or more roots. With --once it
// renders a single static multi-root board to stdout (CI / no-TTY).
//
// Visibility is read-only by default. Control keys are explicit, preview-first,
// and confirm-gated before they mutate squad state.
//
// It degrades gracefully: no projects found under the roots renders a clear
// guidance state, never a crash.
func runNOC(args []string) error {
	return runNOCWithVersion(args, "")
}

func runNOCWithVersion(args []string, version string) error {
	fs := flag.NewFlagSet("noc", flag.ContinueOnError)
	var roots rootList
	fs.Var(&roots, "root", "directory to scan for amq-squad projects (repeatable; default: the project's parent, or cwd)")
	depth := fs.Int("depth", noc.DefaultDepth, "how deep to scan under each root for .agent-mail projects")
	refresh := fs.Duration("refresh", console.NOCDefaultRefresh, "periodic resync cadence (e.g. 2s)")
	atRiskWait := fs.Duration("at-risk-wait", state.DefaultAtRiskWait, "an awaiting-reply thread older than this is at risk")
	reviewAge := fs.Duration("review-age", state.DefaultReviewAge, "an unanswered review/question older than this is at risk")
	staleAfter := fs.Duration("stale-after", state.DefaultStaleAfter, "a thread untouched longer than this is STALE: age-decayed, demoted below live squads, rendered dim")
	filter := fs.String("filter", "", "start with this NOC filter (e.g. needs-you, agent:cto, project:api, session:issue-96)")
	once := fs.Bool("once", false, "render one static board to stdout and exit (non-TTY / CI)")
	tree := fs.Bool("tree", false, "with --once: render the full root->project->session->agent tree instead of the rollup digest")
	all := fs.Bool("all", false, "alias for --tree (full expansion under --once)")
	hideStale := fs.Bool("hide-stale", false, "hide stopped/archived (stale) squads - focus on what is alive")
	showStale := fs.Bool("show-stale", false, "with the live TUI: start with stopped/archived (stale) squads visible")
	noBell := fs.Bool("no-bell", false, "mute needs-you alerts: no terminal bell + no banner when a session first needs you (default: alerts ON)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned noc_snapshot envelope and exit")
	actionsOut := fs.Bool("actions", false, "emit the flat NOC action queue and exit (human table by default; with --json emits noc_actions)")
	actionFilter := fs.String("action", "", "with --actions: only include action names matching this comma-separated list")
	actionIDFilter := fs.String("action-id", "", "with --actions: only include exact action IDs matching this comma-separated list")
	targetIDFilter := fs.String("target-id", "", "with --actions: only include exact target row IDs matching this comma-separated list")
	scopeFilter := fs.String("scope", "", "with --actions: only include scopes matching this comma-separated list (project,session,agent)")
	runActionID := fs.String("run-action", "", "execute one action by exact action ID or unique action name (mutating actions require confirmation or --yes)")
	var actionVars nocTemplateVars
	fs.Var(&actionVars, "set", "with --run-action: fill template variable key=value (repeatable)")
	actionDryRun := fs.Bool("dry-run", false, "with --run-action: resolve and preview the action without executing it")
	yes := fs.Bool("yes", false, "with --run-action: skip mutating action confirmation")
	fs.BoolVar(yes, "y", false, "shorthand for --yes")
	mutatingOnly := fs.Bool("mutating", false, "with --actions: only include mutating actions")
	commandsOnly := fs.Bool("commands", false, "with --actions: print selected action commands only")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-noc noc - live NOC command center across all your squads

Usage:
  amq-noc noc [--root DIR ...] [--depth N] [--refresh 2s]
                [--at-risk-wait 5m] [--review-age 15m] [--stale-after 72h]
                [--filter EXPR] [--once] [--tree|--all] [--hide-stale]
                [--show-stale] [--no-bell] [--json] [--actions [--action NAME[,NAME]]
                [--action-id ID[,ID]] [--target-id ID[,ID]]
                [--scope project,session,agent] [--mutating] [--commands]]
                [--run-action ID_OR_NAME [--set key=value ...] [--dry-run] [--yes|-y]]

A full-screen TUI ("network operations center") over EVERY discovered amq-squad
project or candidate team-home under the given roots. Discovery includes
.agent-mail session stores, .amq-squad team profiles, and git repos that can be
turned into teams. It shows a header pulse (squads / online / needs-you /
waiting / stale), a collapsible attention-first tree
(root -> project -> session -> agent), and a detail pane for the selection.
Navigation is read-only: view movement never stops, starts, messages, or deletes
an agent.

The NOC rewards LIVENESS: an online squad active just now sorts to the top, while
a stopped squad whose only blocked threads are days old (older than --stale-after)
is age-decayed to the bottom and rendered dim. The live TUI starts with stale
squads hidden; press h to show them, or start with --show-stale. For --once,
--json, and --actions, use --hide-stale to opt into the same focused scope.

When a session FIRST needs you (its needs-you count goes 0->N) the NOC rings the
terminal bell once and shows a banner; start muted with --no-bell. The banner
clears on the next keypress. (The same actions are scriptable as --action names;
see the --actions/--action/--run-action surface below.)

Mutating control keys are preview-first and confirm-gated. Press T on a project, session,
or agent row to type a team spec (role IDs, market numbers, all, with optional
role=binary overrides) and create a team profile. Press N to choose
a profile when needed, type a new workstream name, and launch that team in a
detached tmux session; existing names are rejected in the editor, including
empty AMQ session directories, so you can resume or restart instead. Press S/R/X
to stop/resume/restart; mixed-profile sessions ask which profile to operate on.
Agent rows can be messaged, drained, or sent a prompt (press p to deliver a typed
prompt to the agent's pane via amq-squad send). Needs-you rows can be approved,
replied to, or denied. Squad rows can receive broadcasts or lifecycle controls. The live
footer shows only actions currently valid for the selected row; press ? for the
full, always-current key map.

--filter accepts the same typed filter as the TUI: needs-you, needs-user,
online (or running as an alias), waiting, stale, gated, at-risk, blocked,
stale-blocked, agent:<handle>, model:<engine>,
project:<name>, session:<name>, label:<label>, orchestrator:<name>, or bare text.
It scopes the live TUI, --once render, and --json snapshot.

--root is repeatable and defaults to the project's parent (so sibling squads
appear) or the current directory. The TUI renders to /dev/tty; stdout stays
clean. With --once it renders a rollup digest (needs-attention + project
rollups) to STDOUT; add --tree (or --all) for the full expansion.
With --json it emits a noc_snapshot envelope to STDOUT and exits, including
top thread summaries plus read-only and confirm-required action commands for
projects, sessions, and agents. Project rows with a session store include amq_env for AMQ env JSON and amq_who for AMQ session and agent inventory. Configured team project rows include team_rules for durable team-rules.md inspection. Team/create-capable project rows include a read-only roles action so
the role market is discoverable from the same queue. Session rows include
brief for the full workstream brief, confirm-required brief_seed to write one, presence for AMQ presence list, amq_ops for AMQ doctor --ops, and confirm-required amq_cleanup for stale AMQ tmp-file cleanup; needs-you session and agent rows include
read-only thread_context plus confirm-required read_needs_you, reply, approve,
and deny actions for the top human-needed thread. read_needs_you uses AMQ read,
so it moves unread mail to cur like AMQ read does. Session rows also include
confirm-required broadcast actions; agent rows include inbox, receipts, receipts_wait, dlq, confirm-gated
dlq_read, dlq_retry, dlq_retry_all, dlq_purge, message, message_wait, and drain actions over that agent's AMQ mailbox. With --actions it emits just the
flat action queue; add --json for a noc_actions envelope. Add --action
doctor,history,resume_plan,fork_plan,status,amq_env,amq_who,route_explain,roles,team_rules,team_profiles,delete_team,threads,thread_context_any,brief,brief_seed,presence,amq_ops,amq_cleanup,stop,resume,restart,new_team,new_profile,new_session,sync_pointers,thread_context,read_needs_you,reply,approve,deny,dlq,dlq_read,dlq_retry,dlq_retry_all,dlq_purge,receipts,receipts_wait,inbox,message,message_wait,broadcast,archive,remove,agent_resume, --action-id,
--target-id, --scope, or --mutating to narrow that queue. Add --commands for one selected
command per line. Add --run-action ID_OR_NAME to execute exactly one action;
mutating actions preview and prompt unless --yes is set. Exact action IDs always
win; otherwise the action name must resolve uniquely after --filter and
--hide-stale are applied. Template actions such as resume_plan, fork_plan, new_team, new_profile,
new_session, delete_team, brief_seed, sync_pointers, reply, deny, message, message_wait, broadcast, amq_cleanup, dlq_read, dlq_retry, dlq_purge, receipts_wait, and mixed-profile resume accept --set
key=value values for placeholders; new_team/new_profile accept optional
session=<name>, role=binary inside roles, or optional binary=role=cli,...
overrides. sync_pointers accepts optional allow-outside=true; brief_seed accepts optional force=true. Optional
--session, --binary, --force, and --allow-outside flags are omitted when not set. JSON
actions expose a vars array naming required values, optional values, derived
values such as tmux-session from session, and choices when known for profile or
boolean variables. Open-ended values include examples, such as role selections.
The actions table marks known choices in the VARS column. Unknown --set keys are
rejected before execution; values with published choices must match one of those
choices. Add --dry-run to render without running; add --json with --dry-run for
a noc_action_plan envelope. Creation
template values are preflighted locally for session/profile slug validity, brief
seed reference shape, role selections, binary overrides, and duplicates across
the selected profile's team-home and member AMQ roots before execution.
In the live TUI, T (new-team) accepts optional initial sessions such as
"cto,qa,session=issue-96", and N (new-session) accepts inline seeds such as
"issue-97 seed-from=issue:31" before preview.

Examples:
  amq-noc noc
  amq-noc noc --root ~/Code --depth 5
  amq-noc noc --filter needs-you
  amq-noc noc --once | less -R
  amq-noc noc --json | jq .
  amq-noc noc --actions --filter needs-you
  amq-noc noc --actions --action threads --commands
  amq-noc noc --actions --action team_rules --commands
  amq-noc noc --actions --action amq_env --commands
  amq-noc noc --actions --action amq_who --commands
  amq-noc noc --actions --action presence --commands
  amq-noc noc --actions --filter needs-you --action thread_context,read_needs_you,reply,approve,deny
  amq-noc noc --actions --action resume --mutating
  amq-noc noc --actions --action message,broadcast
  amq-noc noc --filter session:issue-96 --run-action amq_cleanup --set tmp-older-than=36h --yes
  amq-noc noc --actions --action dlq --commands
  amq-noc noc --filter agent:cto --run-action dlq_retry --set dlq-id=dlq_123 --yes
  amq-noc noc --filter agent:cto --run-action dlq_purge --set older-than=168h --yes
  amq-noc noc --actions --action receipts --commands
  amq-noc noc --filter agent:cto --run-action receipts_wait --set msg-id=msg_123 --set stage=drained --set timeout=60s
  amq-noc noc --actions --action inbox --commands
  amq-noc noc --filter agent:cto --run-action message_wait --set body='Please check status' --set timeout=60s --yes
  amq-noc noc --actions --target-id 'session|/repo/app|issue-96' --scope session
  amq-noc noc --actions --action archive,remove --commands
  amq-noc noc --actions --action resume --commands
  amq-noc noc --run-action sync_pointers --set profile=review --set allow-outside=true --yes
  amq-noc noc --actions --json | jq .
  amq-noc noc --filter project:app --run-action new_session --set session=issue-97 --dry-run --json
  amq-noc noc --filter project:app --run-action new_session --set session=issue-97 --set seed-from=issue:31 --yes
  amq-noc noc --run-action 'project|/repo/app|action|new_team' --set roles=cto,qa --set session=issue-96 --yes
  amq-noc noc --run-action 'agent|/repo/app|issue-96|cto|action|message' --set body='Please check status' --yes
  amq-noc noc --once --tree
  amq-noc noc --hide-stale --stale-after 24h
  amq-noc noc --show-stale
  amq-noc noc --no-bell
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
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
	if *hideStale && *showStale {
		return usageErrorf("--hide-stale and --show-stale cannot be combined")
	}
	startHideStale := nocStartHideStale(*once, *jsonOut, *actionsOut, runActionSet, *hideStale, *showStale)

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	confirm := io.Reader(os.Stdin)
	if nocActionConfirmOverride != nil {
		confirm = nocActionConfirmOverride
	}

	return executeNOC(nocExecution{
		Cwd:              cwd,
		Version:          version,
		Roots:            []string(roots),
		Depth:            *depth,
		Refresh:          *refresh,
		AtRiskWait:       *atRiskWait,
		ReviewAge:        *reviewAge,
		StaleAfter:       *staleAfter,
		Filter:           *filter,
		Once:             *once,
		Tree:             *tree || *all,
		HideStale:        startHideStale,
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
		Confirm:          confirm,
		StdoutIsTTY:      outputIsTTY(),
		RunActionCommand: nocActionRunnerOverride,
		RunNOC:           console.RunNOC,
		RuntimeFetch:     defaultRuntimeActionFetch,
	})
}

func nocStartHideStale(once, jsonOut, actionsOut, runActionSet, hideFlag, showFlag bool) bool {
	if showFlag {
		return false
	}
	if hideFlag {
		return true
	}
	return !once && !jsonOut && !actionsOut && !runActionSet
}

// nocExecution carries the resolved inputs for the noc verb so tests can drive
// dispatch with seams (no real TTY, a captured RunNOC) without starting a
// Bubble Tea program.
type nocExecution struct {
	Cwd            string
	Version        string
	Roots          []string
	Depth          int
	Refresh        time.Duration
	AtRiskWait     time.Duration
	ReviewAge      time.Duration
	StaleAfter     time.Duration
	Filter         string
	Once           bool
	Tree           bool
	HideStale      bool
	NoBell         bool
	JSON           bool
	Actions        bool
	ActionFilter   string
	ActionIDFilter string
	TargetIDFilter string
	ScopeFilter    string
	RunActionID    string
	ActionVars     map[string]string
	DryRun         bool
	Yes            bool
	MutatingOnly   bool
	CommandsOnly   bool
	Out            io.Writer
	Confirm        io.Reader

	// Seams.
	StdoutIsTTY      bool
	RunActionCommand func(string) error
	// RunNOC runs the NOC surface. Injected so tests assert the assembled config
	// without launching a real program; production passes console.RunNOC.
	RunNOC func(console.NOCConfig) error
	// RuntimeFetch resolves amq-squad's published runtime actions for a session so
	// --actions/--json prefer them over fallback templates. Production injects
	// defaultRuntimeActionFetch; a nil fetch disables the fold (pure fallback).
	RuntimeFetch runtimeActionFetch
}

// executeNOC resolves the roots and the TTY gating, then hands an assembled
// console.NOCConfig to RunNOC.
//
//   - No explicit --root: default to defaultNOCRoots(cwd).
//   - Interactive requested but no TTY: fall back to a single static board on
//     stdout (so a piped invocation still works) rather than failing to open
//     /dev/tty.
func executeNOC(s nocExecution) error {
	roots := s.Roots
	if len(roots) == 0 {
		roots = defaultNOCRoots(s.Cwd)
	}

	thresholds := state.Thresholds{
		AtRiskWait: s.AtRiskWait,
		ReviewAge:  s.ReviewAge,
		StaleAfter: s.StaleAfter,
	}
	if strings.TrimSpace(s.RunActionID) != "" {
		return executeNOCRunAction(s, roots, thresholds)
	}
	if s.Actions {
		ms := noc.Collect(roots, s.Depth, state.DefaultProbe, thresholds)
		ms = filterNOCSnapshot(ms, s.Filter, s.HideStale)
		env := nocSnapshotEnvelope(ms, s.Filter, s.HideStale)
		applyRuntimeActions(&env, s.RuntimeFetch)
		env.Actions = filterNOCActions(env.Actions, nocActionSelector{
			Names:        s.ActionFilter,
			ActionIDs:    s.ActionIDFilter,
			TargetIDs:    s.TargetIDFilter,
			Scopes:       s.ScopeFilter,
			MutatingOnly: s.MutatingOnly,
		})
		env.ActionCount = len(env.Actions)
		env.MutatingActionCount = nocMutatingActionCount(env.Actions)
		if s.JSON {
			return writeJSONEnvelope(s.Out, "noc_actions", nocActionsEnvelope(env))
		}
		if s.CommandsOnly {
			return writeNOCActionsCommands(s.Out, env.Actions)
		}
		return writeNOCActionsTable(s.Out, env.Actions)
	}
	if s.JSON {
		ms := noc.Collect(roots, s.Depth, state.DefaultProbe, thresholds)
		ms = filterNOCSnapshot(ms, s.Filter, s.HideStale)
		env := nocSnapshotEnvelope(ms, s.Filter, s.HideStale)
		applyRuntimeActions(&env, s.RuntimeFetch)
		return writeJSONEnvelope(s.Out, "noc_snapshot", env)
	}

	cfg := console.NOCConfig{
		Version:       s.Version,
		Roots:         roots,
		Depth:         s.Depth,
		Thresholds:    thresholds,
		Refresh:       s.Refresh,
		Once:          s.Once,
		Tree:          s.Tree,
		HideStale:     s.HideStale,
		NoBell:        s.NoBell,
		Out:           s.Out,
		InitialFilter: strings.TrimSpace(s.Filter),
	}

	// Interactive requested but no TTY: render a single static board to stdout.
	if !s.Once && !s.StdoutIsTTY {
		cfg.Once = true
	}

	// Inject the mutating seams. cli owns these verbs; the NOC reaches them ONLY
	// after the operator confirms the preview overlay. The --once path is
	// non-interactive, so the seams are never invoked there even though set.
	cfg.Lifecycle = consoleLifecycle
	cfg.AgentResume = consoleAgentResume
	cfg.SessionCleanup = consoleSessionCleanup
	cfg.NewSession = consoleNewSession
	cfg.NewTeam = consoleNewTeam
	cfg.TeamDelete = consoleTeamDelete
	cfg.PointerSync = consolePointerSync
	cfg.ReadNeedsYou = consoleReadNeedsYou
	cfg.DrainAgent = consoleDrainAgent
	cfg.InboxAgent = consoleInboxAgent
	cfg.DLQAgent = consoleDLQAgent
	cfg.DLQRead = consoleDLQRead
	cfg.DLQRetry = consoleDLQRetry
	cfg.DLQPurge = consoleDLQPurge
	cfg.DLQRetryAll = consoleDLQRetryAll
	cfg.ReceiptsAgent = consoleReceiptsAgent
	cfg.ReceiptsWait = consoleReceiptsWait
	cfg.MessageWait = consoleMessageWait
	cfg.AMQCleanup = consoleAMQCleanup
	cfg.ThreadContext = consoleThreadContext
	cfg.AMQOps = consoleAMQOps
	cfg.AMQWho = consoleAMQWho
	cfg.AMQEnv = consoleAMQEnv
	cfg.Presence = consolePresence
	cfg.ProjectDoctor = consoleProjectDoctor
	cfg.ProjectHistory = consoleProjectHistory
	cfg.TeamRules = consoleTeamRules
	cfg.ProjectResumePlan = consoleProjectResumePlan
	cfg.ForkPlan = consoleForkPlan
	cfg.Brief = consoleBrief
	cfg.BriefSeed = consoleBriefSeed
	cfg.SendPrompt = consoleSendPrompt
	cfg.Status = consoleStatus
	cfg.Threads = consoleThreads
	return s.RunNOC(cfg)
}

type nocSnapshotEnvelopeData struct {
	Roots               []string             `json:"roots"`
	ObservedAt          time.Time            `json:"observed_at"`
	Filter              string               `json:"filter,omitempty"`
	HideStale           bool                 `json:"hide_stale,omitempty"`
	ProjectCount        int                  `json:"project_count"`
	LiveProjects        int                  `json:"live_projects"`
	ActionCount         int                  `json:"action_count"`
	MutatingActionCount int                  `json:"mutating_action_count"`
	LastActivity        *time.Time           `json:"last_activity,omitempty"`
	Rollup              nocRollupData        `json:"rollup"`
	Actions             []nocActionJSONData  `json:"actions,omitempty"`
	Projects            []nocProjectJSONData `json:"projects"`
}

type nocActionsEnvelopeData struct {
	Roots               []string            `json:"roots"`
	ObservedAt          time.Time           `json:"observed_at"`
	Filter              string              `json:"filter,omitempty"`
	HideStale           bool                `json:"hide_stale,omitempty"`
	ActionCount         int                 `json:"action_count"`
	MutatingActionCount int                 `json:"mutating_action_count"`
	Actions             []nocActionJSONData `json:"actions"`
}

type nocActionPlanEnvelopeData struct {
	Roots                []string           `json:"roots"`
	ObservedAt           time.Time          `json:"observed_at"`
	Filter               string             `json:"filter,omitempty"`
	HideStale            bool               `json:"hide_stale,omitempty"`
	Selector             string             `json:"selector"`
	Action               nocActionJSONData  `json:"action"`
	Command              string             `json:"command"`
	DryRun               bool               `json:"dry_run"`
	WouldExecute         bool               `json:"would_execute"`
	RequiresConfirmation bool               `json:"requires_confirmation,omitempty"`
	TemplateValues       map[string]string  `json:"template_values,omitempty"`
	Preflight            []nocPreflightData `json:"preflight,omitempty"`
}

type nocPreflightData struct {
	Check   string `json:"check"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type nocProjectJSONData struct {
	ID             string                  `json:"id"`
	Project        string                  `json:"project"`
	Dir            string                  `json:"dir"`
	BaseRoot       string                  `json:"base_root,omitempty"`
	State          string                  `json:"state"`
	ReasonCode     string                  `json:"reason_code,omitempty"`
	TeamConfigured bool                    `json:"team_configured"`
	DefaultTeam    bool                    `json:"default_team"`
	Profiles       []string                `json:"profiles,omitempty"`
	Operator       nocOperatorJSONData     `json:"operator"`
	Capabilities   nocCapabilitiesJSONData `json:"capabilities"`
	Candidate      bool                    `json:"candidate"`
	SessionStore   bool                    `json:"session_store"`
	SessionNames   []string                `json:"session_names,omitempty"`
	Warning        string                  `json:"warning,omitempty"`
	SessionCount   int                     `json:"session_count"`
	AgentsTotal    int                     `json:"agents_total"`
	AgentsAlive    int                     `json:"agents_alive"`
	LastActivity   *time.Time              `json:"last_activity,omitempty"`
	Rollup         nocRollupData           `json:"rollup"`
	Sessions       []nocSessionJSONData    `json:"sessions,omitempty"`
	Actions        []nocActionJSONData     `json:"actions,omitempty"`
}

type nocOperatorJSONData struct {
	Enabled  bool   `json:"enabled"`
	Handle   string `json:"handle,omitempty"`
	Runnable bool   `json:"runnable"`
}

type nocCapabilitiesJSONData struct {
	OperatorGates  bool `json:"operator_gates"`
	RuntimeActions bool `json:"runtime_actions,omitempty"`
}

type nocSessionJSONData struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	Root            string              `json:"root"`
	State           string              `json:"state"`
	ReasonCode      string              `json:"reason_code,omitempty"`
	AgentsTotal     int                 `json:"agents_total"`
	AgentsAlive     int                 `json:"agents_alive"`
	ThreadCount     int                 `json:"thread_count"`
	ThreadsReturned int                 `json:"threads_returned,omitempty"`
	Attention       string              `json:"attention"`
	AttentionReason string              `json:"attention_reason,omitempty"`
	UnownedEvidence string              `json:"unowned_evidence,omitempty"`
	Orchestrated    bool                `json:"orchestrated,omitempty"`
	Lead            string              `json:"lead,omitempty"`
	LeadHandle      string              `json:"lead_handle,omitempty"`
	LeadDown        bool                `json:"lead_down,omitempty"`
	Rollup          nocRollupData       `json:"rollup"`
	Threads         []threadRow         `json:"threads,omitempty"`
	Agents          []nocAgentJSONData  `json:"agents,omitempty"`
	Actions         []nocActionJSONData `json:"actions,omitempty"`
}

type nocAgentJSONData struct {
	ID              string              `json:"id"`
	Handle          string              `json:"handle"`
	Role            string              `json:"role,omitempty"`
	Engine          string              `json:"engine,omitempty"`
	Liveness        string              `json:"liveness"`
	WakeHealth      string              `json:"wake_health,omitempty"`
	Attention       string              `json:"attention"`
	AttentionReason string              `json:"attention_reason,omitempty"`
	LastSeen        *time.Time          `json:"last_seen,omitempty"`
	Presence        string              `json:"presence,omitempty"`
	Conversation    string              `json:"conversation,omitempty"`
	Source          string              `json:"source,omitempty"`
	TeamProfile     string              `json:"team_profile"`
	IsLead          bool                `json:"is_lead,omitempty"`
	Actions         []nocActionJSONData `json:"actions,omitempty"`
}

type nocRollupData struct {
	NeedsYou           int `json:"needs_you"`
	NeedsYouHistorical int `json:"needs_you_historical,omitempty"`
	AtRisk             int `json:"at_risk"`
	Blocked            int `json:"blocked"`
	Gated              int `json:"gated"`
	AtRiskStale        int `json:"at_risk_stale"`
	BlockedStale       int `json:"blocked_stale"`
	GatedStale         int `json:"gated_stale"`
	Clear              int `json:"clear"`
}

type nocActionJSONData struct {
	Name                 string                  `json:"name"`
	ID                   string                  `json:"id"`
	Scope                string                  `json:"scope"`
	TargetID             string                  `json:"target_id"`
	Command              string                  `json:"command"`
	Description          string                  `json:"description,omitempty"`
	Mutates              bool                    `json:"mutates"`
	RequiresConfirmation bool                    `json:"requires_confirmation,omitempty"`
	Template             bool                    `json:"template,omitempty"`
	Vars                 []nocActionVariableData `json:"vars,omitempty"`
}

type nocActionVariableData struct {
	Name        string   `json:"name"`
	Required    bool     `json:"required"`
	DerivedFrom string   `json:"derived_from,omitempty"`
	Description string   `json:"description,omitempty"`
	Choices     []string `json:"choices,omitempty"`
	Examples    []string `json:"examples,omitempty"`
}

func filterNOCSnapshot(ms noc.MultiSnapshot, filter string, hideStale bool) noc.MultiSnapshot {
	filter = strings.TrimSpace(filter)
	out := noc.MultiSnapshot{
		Roots:      append([]string(nil), ms.Roots...),
		ObservedAt: ms.ObservedAt,
	}
	for _, ps := range ms.Projects {
		if hideStale && nocProjectStaleOnly(ps) {
			continue
		}
		scoped, ok := scopedNOCProject(ps, filter)
		if !ok {
			continue
		}
		out.Projects = append(out.Projects, scoped)
		if scoped.Warning != "" {
			continue
		}
		out.Rollup.Add(scoped.Snap.Rollup)
		if nocProjectHasLiveAgent(scoped) {
			out.LiveProjects++
		}
		if la := nocProjectLastActivity(scoped); la.After(out.LastActivity) {
			out.LastActivity = la
		}
	}
	return out
}

func scopedNOCProject(ps noc.ProjectSnapshot, filter string) (noc.ProjectSnapshot, bool) {
	if !console.ProjectMatchesNOCFilter(ps, filter) {
		return noc.ProjectSnapshot{}, false
	}
	scoped := ps
	scoped.Snap.Sessions = nil
	scoped.Snap.Rollup = state.TriageRollup{}
	for _, sess := range ps.Snap.Sessions {
		if !console.SessionMatchesNOCProjectFilter(ps, sess, filter) {
			continue
		}
		scopedSession := sess
		scopedSession.Agents = nil
		for _, ag := range sess.Agents {
			if console.AgentMatchesNOCProjectFilter(ps, sess, ag, filter) {
				scopedSession.Agents = append(scopedSession.Agents, ag)
			}
		}
		scoped.Snap.Sessions = append(scoped.Snap.Sessions, scopedSession)
		scoped.Snap.Rollup.Add(scopedSession.Rollup)
	}
	return scoped, true
}

func nocSnapshotEnvelope(ms noc.MultiSnapshot, filter string, hideStale bool) nocSnapshotEnvelopeData {
	projects := make([]nocProjectJSONData, 0, len(ms.Projects))
	for _, ps := range ms.Projects {
		projects = append(projects, nocProjectEnvelope(ps))
	}
	actions := nocFlatActions(projects)
	return nocSnapshotEnvelopeData{
		Roots:               append([]string(nil), ms.Roots...),
		ObservedAt:          ms.ObservedAt,
		Filter:              strings.TrimSpace(filter),
		HideStale:           hideStale,
		ProjectCount:        len(ms.Projects),
		LiveProjects:        ms.LiveProjects,
		ActionCount:         len(actions),
		MutatingActionCount: nocMutatingActionCount(actions),
		LastActivity:        jsonTimePtr(ms.LastActivity),
		Rollup:              nocRollupEnvelope(ms.Rollup),
		Actions:             actions,
		Projects:            projects,
	}
}

func nocActionsEnvelope(env nocSnapshotEnvelopeData) nocActionsEnvelopeData {
	return nocActionsEnvelopeData{
		Roots:               append([]string(nil), env.Roots...),
		ObservedAt:          env.ObservedAt,
		Filter:              env.Filter,
		HideStale:           env.HideStale,
		ActionCount:         env.ActionCount,
		MutatingActionCount: env.MutatingActionCount,
		Actions:             append([]nocActionJSONData(nil), env.Actions...),
	}
}

func writeNOCActionsTable(w io.Writer, actions []nocActionJSONData) error {
	if len(actions) == 0 {
		_, err := fmt.Fprintln(w, "No NOC actions found.")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSCOPE\tMUTATES\tTEMPLATE\tVARS\tCOMMAND")
	for _, action := range actions {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			action.ID,
			action.Scope,
			yesNo(action.Mutates),
			yesNo(action.Template),
			nocActionVarsSummary(action.Vars),
			action.Command)
	}
	return tw.Flush()
}

func nocActionVarsSummary(vars []nocActionVariableData) string {
	if len(vars) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(vars))
	for _, v := range vars {
		part := v.Name
		if len(v.Choices) > 0 {
			part += "[" + strings.Join(sortedUniqueStrings(v.Choices), "|") + "]"
		} else if v.Required && len(v.Examples) > 0 {
			part += "(ex:" + v.Examples[0] + ")"
		}
		if v.DerivedFrom != "" {
			part += "(from " + v.DerivedFrom + ")"
		} else if !v.Required {
			part += "(optional)"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ",")
}

func writeNOCActionsCommands(w io.Writer, actions []nocActionJSONData) error {
	for _, action := range actions {
		if _, err := fmt.Fprintln(w, action.Command); err != nil {
			return err
		}
	}
	return nil
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func nocFlatActions(projects []nocProjectJSONData) []nocActionJSONData {
	var actions []nocActionJSONData
	for _, project := range projects {
		actions = append(actions, project.Actions...)
		for _, session := range project.Sessions {
			actions = append(actions, session.Actions...)
			for _, agent := range session.Agents {
				actions = append(actions, agent.Actions...)
			}
		}
	}
	return actions
}

type nocActionSelector struct {
	Names        string
	ActionIDs    string
	TargetIDs    string
	Scopes       string
	MutatingOnly bool
}

func nocActionSelectorFlagWasSet(fs *flag.FlagSet) bool {
	for _, name := range []string{"action", "action-id", "target-id", "scope", "mutating", "commands"} {
		if flagWasSet(fs, name) {
			return true
		}
	}
	return false
}

func filterNOCActions(actions []nocActionJSONData, selector nocActionSelector) []nocActionJSONData {
	nameSet := parseNOCSelectorSet(selector.Names, strings.ToLower)
	actionIDSet := parseNOCSelectorSet(selector.ActionIDs, nil)
	targetIDSet := parseNOCSelectorSet(selector.TargetIDs, nil)
	scopeSet := parseNOCSelectorSet(selector.Scopes, strings.ToLower)
	if len(nameSet) == 0 && len(actionIDSet) == 0 && len(targetIDSet) == 0 && len(scopeSet) == 0 && !selector.MutatingOnly {
		return actions
	}
	out := make([]nocActionJSONData, 0, len(actions))
	for _, action := range actions {
		if selector.MutatingOnly && !action.Mutates {
			continue
		}
		if len(nameSet) > 0 && !nameSet[strings.ToLower(action.Name)] {
			continue
		}
		if len(actionIDSet) > 0 && !actionIDSet[action.ID] {
			continue
		}
		if len(targetIDSet) > 0 && !targetIDSet[action.TargetID] {
			continue
		}
		if len(scopeSet) > 0 && !scopeSet[strings.ToLower(action.Scope)] {
			continue
		}
		out = append(out, action)
	}
	return out
}

func parseNOCSelectorSet(value string, normalize func(string) string) map[string]bool {
	out := map[string]bool{}
	for _, part := range strings.Split(value, ",") {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		if normalize != nil {
			item = normalize(item)
		}
		out[item] = true
	}
	return out
}

func executeNOCRunAction(s nocExecution, roots []string, thresholds state.Thresholds) error {
	if s.JSON && !s.DryRun {
		return usageErrorf("--run-action --json requires --dry-run; use --actions --json to inspect actions")
	}
	ms := noc.Collect(roots, s.Depth, state.DefaultProbe, thresholds)
	ms = filterNOCSnapshot(ms, s.Filter, s.HideStale)
	env := nocSnapshotEnvelope(ms, s.Filter, s.HideStale)
	applyRuntimeActions(&env, s.RuntimeFetch)
	selector := strings.TrimSpace(s.RunActionID)
	action, err := resolveNOCRunAction(env.Actions, selector)
	if err != nil {
		return err
	}
	command, values, err := renderNOCActionCommand(action, s.ActionVars)
	if err != nil {
		return err
	}
	preflight, err := preflightNOCAction(action, values, env.Projects)
	if err != nil {
		return err
	}
	if err := validateNOCActionVarChoices(action, values); err != nil {
		return err
	}
	if s.JSON {
		out := s.Out
		if out == nil {
			out = os.Stdout
		}
		return writeJSONEnvelope(out, "noc_action_plan", nocActionPlanEnvelopeData{
			Roots:                append([]string(nil), env.Roots...),
			ObservedAt:           env.ObservedAt,
			Filter:               env.Filter,
			HideStale:            env.HideStale,
			Selector:             selector,
			Action:               action,
			Command:              command,
			DryRun:               s.DryRun,
			WouldExecute:         !s.DryRun,
			RequiresConfirmation: action.Mutates || action.RequiresConfirmation,
			TemplateValues:       values,
			Preflight:            preflight,
		})
	}
	return runNOCActionCommand(s, action, command, preflight)
}

func resolveNOCRunAction(actions []nocActionJSONData, selector string) (nocActionJSONData, error) {
	if selector == "" {
		return nocActionJSONData{}, usageErrorf("--run-action requires an action ID or action name")
	}
	for _, action := range actions {
		if action.ID == selector {
			return action, nil
		}
	}
	var matches []nocActionJSONData
	name := strings.ToLower(selector)
	for _, action := range actions {
		if strings.ToLower(action.Name) == name {
			matches = append(matches, action)
		}
	}
	switch len(matches) {
	case 0:
		return nocActionJSONData{}, usageErrorf("NOC action %q not found under the selected roots/filter", selector)
	case 1:
		return matches[0], nil
	default:
		ids := make([]string, 0, len(matches))
		for _, action := range matches {
			ids = append(ids, action.ID)
		}
		sort.Strings(ids)
		return nocActionJSONData{}, usageErrorf("NOC action name %q matches multiple actions under the selected roots/filter; use an exact action ID or narrow --filter:\n  %s",
			selector, strings.Join(ids, "\n  "))
	}
}

func renderNOCActionCommand(action nocActionJSONData, vars map[string]string) (string, map[string]string, error) {
	command := action.Command
	placeholders := nocActionPlaceholders(command)
	placeholderSet := nocActionPlaceholderSet(placeholders)
	values := normalizeNOCActionVars(vars)
	if len(placeholders) == 0 {
		if len(values) > 0 {
			keys := sortedMapKeys(values)
			return "", nil, usageErrorf("NOC action %q does not accept template values; remove --set %s", action.ID, strings.Join(keys, ", "))
		}
		return command, nil, nil
	}
	if err := validateNOCActionVars(action, placeholders, values); err != nil {
		return "", nil, err
	}
	var err error
	values, err = normalizeNOCTeamActionVars(action, values)
	if err != nil {
		return "", nil, err
	}
	if values["tmux-session"] == "" && values["session"] != "" {
		values["tmux-session"] = nocTerminalSessionName(nocActionTargetProjectDir(action.TargetID), values["session"])
	}
	var missing []string
	for _, placeholder := range placeholders {
		key := normalizeNOCActionVarKey(placeholder)
		if nocActionValueDerived(key, placeholderSet) || nocActionValueOptional(action.Name, key) {
			continue
		}
		if strings.TrimSpace(values[key]) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return "", nil, usageErrorf("NOC action %q requires template values: %s", action.ID, nocActionMissingValueHints(action, missing))
	}
	usedValues := map[string]string{}
	for _, placeholder := range placeholders {
		key := normalizeNOCActionVarKey(placeholder)
		value := shellQuote(values[key])
		if key == "binary-flag" {
			binary := strings.TrimSpace(values["binary"])
			if binary != "" {
				usedValues["binary"] = binary
			}
			value = nocActionBinaryFlagValue(binary)
		} else if key == "binary" {
			binary := strings.TrimSpace(values["binary"])
			if binary == "" {
				command = removeNOCOptionalBinaryPlaceholder(command, placeholder)
				continue
			}
			usedValues["binary"] = binary
			value = shellQuote(binary)
		} else if key == "seed-from" {
			seedFrom := strings.TrimSpace(values["seed-from"])
			if seedFrom == "" {
				command = removeNOCOptionalFlagPlaceholder(command, "seed-from", placeholder)
				continue
			}
			usedValues[key] = seedFrom
			value = shellQuote(seedFrom)
		} else if key == "session" && nocActionValueOptional(action.Name, key) {
			session := strings.TrimSpace(values["session"])
			if session == "" {
				command = removeNOCOptionalFlagPlaceholder(command, "session", placeholder)
				continue
			}
			usedValues[key] = session
			value = shellQuote(session)
		} else if key == "allow-outside" {
			enabled, set, err := parseNOCOptionalBool("allow-outside", values["allow-outside"])
			if err != nil {
				return "", nil, err
			}
			if !set {
				command = removeNOCStandalonePlaceholder(command, placeholder)
				continue
			}
			if enabled {
				usedValues[key] = "true"
				value = "--allow-outside"
			} else {
				usedValues[key] = "false"
				command = removeNOCStandalonePlaceholder(command, placeholder)
				continue
			}
		} else if key == "force" {
			enabled, set, err := parseNOCOptionalBool("force", values["force"])
			if err != nil {
				return "", nil, err
			}
			if !set {
				command = removeNOCStandalonePlaceholder(command, placeholder)
				continue
			}
			if enabled {
				usedValues[key] = "true"
				value = "--force"
			} else {
				usedValues[key] = "false"
				command = removeNOCStandalonePlaceholder(command, placeholder)
				continue
			}
		} else if action.Name == "deny" && key == "reason" {
			reason := strings.TrimSpace(values["reason"])
			usedValues[key] = reason
			value = shellQuote(nocDenyBody(reason))
		} else {
			usedValues[key] = values[key]
		}
		command = strings.ReplaceAll(command, "'<"+placeholder+">'", value)
		command = strings.ReplaceAll(command, "<"+placeholder+">", value)
	}
	if remaining := nocActionPlaceholders(command); len(remaining) > 0 {
		sort.Strings(remaining)
		return "", nil, usageErrorf("NOC action %q still has unresolved template values: %s", action.ID, strings.Join(remaining, ", "))
	}
	return command, usedValues, nil
}

func nocActionMissingValueHints(action nocActionJSONData, missing []string) string {
	parts := make([]string, 0, len(missing))
	for _, key := range missing {
		part := "--set " + key + "=<value>"
		if choices := nocActionVarChoices(action, key); len(choices) > 0 {
			part += " (choices: " + strings.Join(choices, ", ") + ")"
		} else if examples := nocActionVarExamples(action, key); len(examples) > 0 {
			part += " (examples: " + strings.Join(examples, "; ") + ")"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, " ")
}

func validateNOCActionVars(action nocActionJSONData, placeholders []string, values map[string]string) error {
	allowed := nocActionAllowedVarKeys(placeholders)
	var unknown []string
	for key := range values {
		if !allowed[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	allowedKeys := make([]string, 0, len(allowed))
	for key := range allowed {
		allowedKeys = append(allowedKeys, key)
	}
	sort.Strings(allowedKeys)
	return usageErrorf("NOC action %q does not accept template value(s): %s. Accepted values: %s",
		action.ID, strings.Join(unknown, ", "), strings.Join(allowedKeys, ", "))
}

func validateNOCActionVarChoices(action nocActionJSONData, values map[string]string) error {
	for _, v := range action.Vars {
		choices := nocActionVarChoices(action, v.Name)
		if len(choices) == 0 {
			continue
		}
		key := normalizeNOCActionVarKey(v.Name)
		value := strings.TrimSpace(values[key])
		if value == "" || stringInSlice(choices, value) {
			continue
		}
		return usageErrorf("NOC action %q template value %s=%q is not valid; choose one of: %s",
			action.ID, v.Name, value, strings.Join(choices, ", "))
	}
	return nil
}

func nocActionVarChoices(action nocActionJSONData, name string) []string {
	key := normalizeNOCActionVarKey(name)
	for _, v := range action.Vars {
		if normalizeNOCActionVarKey(v.Name) == key {
			return sortedUniqueStrings(v.Choices)
		}
	}
	return nil
}

func nocActionVarExamples(action nocActionJSONData, name string) []string {
	key := normalizeNOCActionVarKey(name)
	for _, v := range action.Vars {
		if normalizeNOCActionVarKey(v.Name) == key {
			return append([]string(nil), v.Examples...)
		}
	}
	return nil
}

func nocActionAllowedVarKeys(placeholders []string) map[string]bool {
	out := map[string]bool{}
	for _, placeholder := range placeholders {
		key := normalizeNOCActionVarKey(placeholder)
		if key == "" {
			continue
		}
		if key == "binary-flag" {
			out["binary"] = true
			continue
		}
		out[key] = true
	}
	return out
}

func preflightNOCAction(action nocActionJSONData, values map[string]string, projects []nocProjectJSONData) ([]nocPreflightData, error) {
	project, ok := nocActionProject(projects, action.TargetID)
	if !ok {
		return nil, nil
	}
	switch action.Name {
	case "fork_plan":
		session := strings.TrimSpace(values["session"])
		if session == "" {
			return nil, nil
		}
		checks, err := preflightNOCSessionName(session)
		if err != nil {
			return checks, err
		}
		profileValues := values
		if strings.TrimSpace(profileValues["profile"]) == "" {
			if profile := nocActionCommandProfile(action); profile != "" {
				profileValues = cloneStringMap(values)
				profileValues["profile"] = profile
			}
		}
		profileChecks, profile, err := preflightNOCSessionProfile(project, profileValues)
		checks = append(checks, profileChecks...)
		if err != nil {
			return checks, err
		}
		source := nocActionTargetSession(action.TargetID)
		if source == "" {
			check := nocPreflightData{Check: "fork_source", Status: "error", Message: "fork source session could not be resolved from action target"}
			return append(checks, check), usageErrorf("%s", check.Message)
		}
		if source == session {
			check := nocPreflightData{Check: "fork_target", Status: "error", Message: fmt.Sprintf("fork source and target must differ; got %q for both", source)}
			return append(checks, check), usageErrorf("%s", check.Message)
		}
		if stringInSlice(project.SessionNames, session) {
			check := nocPreflightData{
				Check:   "session_available",
				Status:  "error",
				Message: fmt.Sprintf("session %q already exists in %s", session, project.Dir),
			}
			return append(checks, check), usageErrorf("%s; choose a new target session", check.Message)
		}
		deepChecks, err := preflightNOCSessionTeamAvailability(project, profile, session)
		checks = append(checks, deepChecks...)
		if err != nil {
			return checks, err
		}
		return append(checks, nocPreflightData{
			Check:   "fork_source",
			Status:  "ok",
			Message: fmt.Sprintf("session %q can be used as the fork source", source),
		}), nil
	case "brief_seed":
		return preflightNOCSeedFrom(values["seed-from"])
	case "new_session":
		session := strings.TrimSpace(values["session"])
		if session == "" {
			return nil, nil
		}
		checks, err := preflightNOCSessionName(session)
		if err != nil {
			return checks, err
		}
		profileChecks, profile, err := preflightNOCSessionProfile(project, values)
		checks = append(checks, profileChecks...)
		if err != nil {
			return checks, err
		}
		seedChecks, err := preflightNOCSeedFrom(values["seed-from"])
		checks = append(checks, seedChecks...)
		if err != nil {
			return checks, err
		}
		if stringInSlice(project.SessionNames, session) {
			check := nocPreflightData{
				Check:   "session_available",
				Status:  "error",
				Message: fmt.Sprintf("session %q already exists in %s", session, project.Dir),
			}
			return append(checks, check), usageErrorf("%s; choose a new session name or run resume/restart", check.Message)
		}
		deepChecks, err := preflightNOCSessionTeamAvailability(project, profile, session)
		checks = append(checks, deepChecks...)
		if err != nil {
			return checks, err
		}
		return append(checks, nocPreflightData{
			Check:   "session_available",
			Status:  "ok",
			Message: fmt.Sprintf("session %q is available for profile %q in %s", session, profile, project.Dir),
		}), nil
	case "new_profile":
		profile := strings.TrimSpace(values["profile"])
		if profile == "" {
			return nil, nil
		}
		checks, err := preflightNOCProfileName(profile)
		if err != nil {
			return checks, err
		}
		roleChecks, err := preflightNOCRoles(values["roles"])
		checks = append(checks, roleChecks...)
		if err != nil {
			return checks, err
		}
		binaryChecks, err := preflightNOCBinaryOverrides(values["roles"], values["binary"])
		checks = append(checks, binaryChecks...)
		if err != nil {
			return checks, err
		}
		sessionChecks, err := preflightNOCOptionalSession(values["session"])
		checks = append(checks, sessionChecks...)
		if err != nil {
			return checks, err
		}
		if profile == team.DefaultProfile {
			check := nocPreflightData{
				Check:   "profile_available",
				Status:  "error",
				Message: "profile \"default\" is reserved for the default team; use the new_team action",
			}
			return append(checks, check), usageErrorf("%s", check.Message)
		}
		if stringInSlice(project.Profiles, profile) {
			check := nocPreflightData{
				Check:   "profile_available",
				Status:  "error",
				Message: fmt.Sprintf("profile %q already exists in %s", profile, project.Dir),
			}
			return append(checks, check), usageErrorf("%s; choose a new profile name", check.Message)
		}
		return append(checks, nocPreflightData{
			Check:   "profile_available",
			Status:  "ok",
			Message: fmt.Sprintf("profile %q is available in %s", profile, project.Dir),
		}), nil
	case "new_team":
		checks, err := preflightNOCRoles(values["roles"])
		if err != nil {
			return checks, err
		}
		binaryChecks, err := preflightNOCBinaryOverrides(values["roles"], values["binary"])
		checks = append(checks, binaryChecks...)
		if err != nil {
			return checks, err
		}
		sessionChecks, err := preflightNOCOptionalSession(values["session"])
		checks = append(checks, sessionChecks...)
		if err != nil {
			return checks, err
		}
		if project.DefaultTeam {
			check := nocPreflightData{
				Check:   "default_profile_missing",
				Status:  "error",
				Message: fmt.Sprintf("default team already exists in %s", project.Dir),
			}
			return append(checks, check), usageErrorf("%s; use new_profile for another team shape", check.Message)
		}
		return append(checks, nocPreflightData{
			Check:   "default_profile_missing",
			Status:  "ok",
			Message: fmt.Sprintf("default team is not configured in %s", project.Dir),
		}), nil
	case "sync_pointers":
		checks, profile, err := preflightNOCSessionProfile(project, values)
		if err != nil {
			return checks, err
		}
		syncChecks, err := preflightNOCPointerSync(project, profile, values["allow-outside"])
		checks = append(checks, syncChecks...)
		if err != nil {
			return checks, err
		}
		return checks, nil
	case "delete_team":
		checks, _, err := preflightNOCSessionProfile(project, values)
		return checks, err
	case "dlq_read", "dlq_retry":
		return preflightNOCDLQID(values["dlq-id"])
	case "dlq_purge":
		return preflightNOCDLQPurgeOlderThan(values["older-than"])
	case "amq_cleanup":
		return preflightNOCAMQCleanupTmpOlderThan(values["tmp-older-than"])
	case "message_wait":
		return preflightNOCMessageWaitTimeout(values["timeout"])
	case "receipts_wait":
		return preflightNOCReceiptsWaitTimeout(values["timeout"])
	default:
		return nil, nil
	}
}

func preflightNOCSessionName(session string) ([]nocPreflightData, error) {
	if err := validateWorkstreamName(session); err != nil {
		check := nocPreflightData{Check: "session_valid", Status: "error", Message: err.Error()}
		return []nocPreflightData{check}, usageErrorf("%s", check.Message)
	}
	return []nocPreflightData{{
		Check:   "session_valid",
		Status:  "ok",
		Message: fmt.Sprintf("session %q is a valid AMQ session name", session),
	}}, nil
}

func preflightNOCOptionalSession(session string) ([]nocPreflightData, error) {
	session = strings.TrimSpace(session)
	if session == "" {
		return nil, nil
	}
	return preflightNOCSessionName(session)
}

func preflightNOCPointerSync(project nocProjectJSONData, profile, allowOutsideRaw string) ([]nocPreflightData, error) {
	checks := []nocPreflightData{}
	rulesPath := rules.Path(project.Dir)
	if _, err := os.Stat(rulesPath); err != nil {
		check := nocPreflightData{
			Check:   "team_rules",
			Status:  "error",
			Message: fmt.Sprintf("team rules not found at %s", rulesPath),
		}
		if os.IsNotExist(err) {
			return append(checks, check), usageErrorf("%s; run amq-squad team rules init first", check.Message)
		}
		check.Message = fmt.Sprintf("cannot inspect team rules at %s: %v", rulesPath, err)
		return append(checks, check), usageErrorf("%s", check.Message)
	}
	checks = append(checks, nocPreflightData{
		Check:   "team_rules",
		Status:  "ok",
		Message: fmt.Sprintf("team rules found at %s", rulesPath),
	})

	allowOutside, _, err := parseNOCOptionalBool("allow-outside", allowOutsideRaw)
	if err != nil {
		check := nocPreflightData{Check: "allow_outside_valid", Status: "error", Message: err.Error()}
		return append(checks, check), err
	}
	t, err := team.ReadProfile(project.Dir, profile)
	if err != nil {
		check := nocPreflightData{
			Check:   "sync_targets",
			Status:  "error",
			Message: fmt.Sprintf("cannot read profile %q in %s: %v", profile, project.Dir, err),
		}
		return append(checks, check), usageErrorf("%s", check.Message)
	}
	targets, err := syncTargetDirs(project.Dir, t.Members, allowOutside)
	if err != nil {
		check := nocPreflightData{
			Check:   "sync_targets",
			Status:  "error",
			Message: err.Error(),
		}
		return append(checks, check), usageErrorf("%s; set allow-outside=true if those member cwds are intentional", check.Message)
	}
	if profile == team.DefaultProfile {
		var ensureErr error
		targets, ensureErr = ensureTeamHomeSyncTarget(targets, project.Dir)
		if ensureErr != nil {
			check := nocPreflightData{Check: "sync_targets", Status: "error", Message: ensureErr.Error()}
			return append(checks, check), usageErrorf("%s", check.Message)
		}
	}
	return append(checks, nocPreflightData{
		Check:   "sync_targets",
		Status:  "ok",
		Message: fmt.Sprintf("pointer sync will update %d target directorie(s) for profile %q", len(targets), profile),
	}), nil
}

func preflightNOCSessionProfile(project nocProjectJSONData, values map[string]string) ([]nocPreflightData, string, error) {
	profile := strings.TrimSpace(values["profile"])
	if profile == "" {
		if len(project.Profiles) == 1 {
			profile = project.Profiles[0]
		} else {
			profile = team.DefaultProfile
		}
	}
	if err := team.ValidateProfileName(profile); err != nil {
		check := nocPreflightData{Check: "profile_valid", Status: "error", Message: err.Error()}
		return []nocPreflightData{check}, profile, usageErrorf("%s", check.Message)
	}
	checks := []nocPreflightData{{
		Check:   "profile_valid",
		Status:  "ok",
		Message: fmt.Sprintf("profile %q is a valid profile name", profile),
	}}
	if !stringInSlice(project.Profiles, profile) {
		check := nocPreflightData{
			Check:   "profile_configured",
			Status:  "error",
			Message: fmt.Sprintf("profile %q is not configured in %s", profile, project.Dir),
		}
		return append(checks, check), profile, usageErrorf("%s; choose one of: %s", check.Message, strings.Join(project.Profiles, ", "))
	}
	return append(checks, nocPreflightData{
		Check:   "profile_configured",
		Status:  "ok",
		Message: fmt.Sprintf("profile %q is configured in %s", profile, project.Dir),
	}), profile, nil
}

func preflightNOCSeedFrom(raw string) ([]nocPreflightData, error) {
	seedFrom := strings.TrimSpace(raw)
	if seedFrom == "" {
		return nil, nil
	}
	check := nocPreflightData{Check: "seed_from_valid"}
	kind, rest, ok := strings.Cut(seedFrom, ":")
	if !ok || strings.TrimSpace(kind) == "" || strings.TrimSpace(rest) == "" {
		check.Status = "error"
		check.Message = "invalid seed-from " + seedFrom
		return []nocPreflightData{check}, usageErrorf("%s; use file:<path>, issue:<n>, or gh:owner/repo#<n>", check.Message)
	}
	switch kind {
	case "file":
		check.Status = "ok"
		check.Message = "brief seed reference accepted"
		return []nocPreflightData{check}, nil
	case "issue":
		if !isPositiveDecimal(rest) {
			check.Status = "error"
			check.Message = "invalid seed-from " + seedFrom
			return []nocPreflightData{check}, usageErrorf("%s; issue seeds must look like issue:<n>", check.Message)
		}
		check.Status = "ok"
		check.Message = "brief seed reference accepted"
		return []nocPreflightData{check}, nil
	case "gh":
		if !looksLikeGitHubIssueSeed(rest) {
			check.Status = "error"
			check.Message = "invalid seed-from " + seedFrom
			return []nocPreflightData{check}, usageErrorf("%s; GitHub seeds must look like gh:owner/repo#<n>", check.Message)
		}
		check.Status = "ok"
		check.Message = "brief seed reference accepted"
		return []nocPreflightData{check}, nil
	default:
		check.Status = "error"
		check.Message = "invalid seed-from " + seedFrom
		return []nocPreflightData{check}, usageErrorf("%s; use file:<path>, issue:<n>, or gh:owner/repo#<n>", check.Message)
	}
}

func isPositiveDecimal(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return strings.TrimLeft(s, "0") != ""
}

func looksLikeGitHubIssueSeed(s string) bool {
	repo, number, ok := strings.Cut(strings.TrimSpace(s), "#")
	if !ok || !isPositiveDecimal(number) {
		return false
	}
	owner, name, ok := strings.Cut(repo, "/")
	return ok && strings.TrimSpace(owner) != "" && strings.TrimSpace(name) != ""
}

func preflightNOCProfileName(profile string) ([]nocPreflightData, error) {
	if err := team.ValidateProfileName(profile); err != nil {
		check := nocPreflightData{Check: "profile_valid", Status: "error", Message: err.Error()}
		return []nocPreflightData{check}, usageErrorf("%s", check.Message)
	}
	return []nocPreflightData{{
		Check:   "profile_valid",
		Status:  "ok",
		Message: fmt.Sprintf("profile %q is a valid profile name", profile),
	}}, nil
}

func preflightNOCSessionTeamAvailability(project nocProjectJSONData, profile, session string) ([]nocPreflightData, error) {
	t, err := team.ReadProfile(project.Dir, profile)
	if err != nil {
		return nil, fmt.Errorf("read team profile %q: %w", profile, err)
	}
	exists, root, err := teamWorkstreamExistsOrRestorable(t, session)
	if err != nil {
		return []nocPreflightData{{
			Check:   "session_member_roots",
			Status:  "warning",
			Message: fmt.Sprintf("could not check member AMQ roots for profile %q: %v", profile, err),
		}}, nil
	}
	if exists {
		where := ""
		if root != "" {
			where = " at " + root
		}
		check := nocPreflightData{
			Check:   "session_available",
			Status:  "error",
			Message: fmt.Sprintf("session %q already exists or has restorable launch records for profile %q%s", session, profile, where),
		}
		return []nocPreflightData{check}, usageErrorf("%s; choose a new session name or run resume/restart", check.Message)
	}
	return []nocPreflightData{{
		Check:   "session_member_roots",
		Status:  "ok",
		Message: fmt.Sprintf("no member AMQ roots or launch records found for session %q in profile %q", session, profile),
	}}, nil
}

func preflightNOCRoles(roles string) ([]nocPreflightData, error) {
	roles = strings.TrimSpace(roles)
	if roles == "" {
		return nil, nil
	}
	resolved, err := catalog.ResolveSelection(roles)
	if err != nil {
		check := nocPreflightData{Check: "roles_valid", Status: "error", Message: err.Error()}
		return []nocPreflightData{check}, usageErrorf("%s", check.Message)
	}
	return []nocPreflightData{{
		Check:   "roles_valid",
		Status:  "ok",
		Message: fmt.Sprintf("roles selection resolves to %s", strings.Join(resolved, ", ")),
	}}, nil
}

func preflightNOCBinaryOverrides(roles, binary string) ([]nocPreflightData, error) {
	binary = strings.TrimSpace(binary)
	if binary == "" {
		return nil, nil
	}
	resolved, err := catalog.ResolveSelection(roles)
	if err != nil {
		check := nocPreflightData{Check: "binary_valid", Status: "error", Message: err.Error()}
		return []nocPreflightData{check}, usageErrorf("%s", check.Message)
	}
	known := map[string]bool{}
	for _, role := range resolved {
		known[strings.ToLower(strings.TrimSpace(role))] = true
	}
	overrides, err := parseKV(binary)
	if err != nil {
		check := nocPreflightData{Check: "binary_valid", Status: "error", Message: err.Error()}
		return []nocPreflightData{check}, usageErrorf("%s", check.Message)
	}
	var unknown []string
	for role, value := range overrides {
		key := strings.ToLower(strings.TrimSpace(role))
		if !known[key] {
			unknown = append(unknown, role)
			continue
		}
		if err := team.ValidateDisplayValue("binary", value); err != nil {
			check := nocPreflightData{Check: "binary_valid", Status: "error", Message: err.Error()}
			return []nocPreflightData{check}, usageErrorf("%s", check.Message)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		check := nocPreflightData{
			Check:   "binary_valid",
			Status:  "error",
			Message: fmt.Sprintf("binary override has unknown role(s): %s", strings.Join(unknown, ", ")),
		}
		return []nocPreflightData{check}, usageErrorf("%s", check.Message)
	}
	return []nocPreflightData{{
		Check:   "binary_valid",
		Status:  "ok",
		Message: fmt.Sprintf("binary overrides apply to %s", strings.Join(sortedMapKeys(overrides), ", ")),
	}}, nil
}

func preflightNOCDLQID(raw string) ([]nocPreflightData, error) {
	id := strings.TrimSpace(raw)
	if id == "" {
		return nil, nil
	}
	check := nocPreflightData{Check: "dlq_id_valid"}
	if strings.HasPrefix(id, ".") || id == "." || id == ".." || filepath.Base(id) != id || strings.ContainsAny(id, `/\`) {
		check.Status = "error"
		check.Message = "invalid DLQ id " + id
		return []nocPreflightData{check}, usageErrorf("%s; use the ID from amq dlq list, not a path", check.Message)
	}
	check.Status = "ok"
	check.Message = "DLQ id accepted"
	return []nocPreflightData{check}, nil
}

func preflightNOCDLQPurgeOlderThan(raw string) ([]nocPreflightData, error) {
	olderThan := strings.TrimSpace(raw)
	if olderThan == "" {
		return nil, nil
	}
	check := nocPreflightData{Check: "dlq_purge_age_valid"}
	dur, err := time.ParseDuration(olderThan)
	if err != nil || dur <= 0 {
		check.Status = "error"
		check.Message = "invalid DLQ purge age " + olderThan
		return []nocPreflightData{check}, usageErrorf("%s; use a positive duration like 24h or 168h", check.Message)
	}
	check.Status = "ok"
	check.Message = "DLQ purge age accepted"
	return []nocPreflightData{check}, nil
}

func preflightNOCAMQCleanupTmpOlderThan(raw string) ([]nocPreflightData, error) {
	olderThan := strings.TrimSpace(raw)
	if olderThan == "" {
		return nil, nil
	}
	check := nocPreflightData{Check: "amq_cleanup_tmp_age_valid"}
	dur, err := time.ParseDuration(olderThan)
	if err != nil || dur <= 0 {
		check.Status = "error"
		check.Message = "invalid AMQ cleanup tmp age " + olderThan
		return []nocPreflightData{check}, usageErrorf("%s; use a positive duration like 36h or 168h", check.Message)
	}
	check.Status = "ok"
	check.Message = "AMQ cleanup tmp age accepted"
	return []nocPreflightData{check}, nil
}

func preflightNOCMessageWaitTimeout(raw string) ([]nocPreflightData, error) {
	timeout := strings.TrimSpace(raw)
	if timeout == "" {
		return nil, nil
	}
	check := nocPreflightData{Check: "message_wait_timeout_valid"}
	dur, err := time.ParseDuration(timeout)
	if err != nil || dur < 0 {
		check.Status = "error"
		check.Message = "invalid message wait timeout " + timeout
		return []nocPreflightData{check}, usageErrorf("%s; use a non-negative duration like 60s or 5m", check.Message)
	}
	check.Status = "ok"
	check.Message = "message wait timeout accepted"
	return []nocPreflightData{check}, nil
}

func preflightNOCReceiptsWaitTimeout(raw string) ([]nocPreflightData, error) {
	timeout := strings.TrimSpace(raw)
	if timeout == "" {
		return nil, nil
	}
	check := nocPreflightData{Check: "receipts_wait_timeout_valid"}
	dur, err := time.ParseDuration(timeout)
	if err != nil || dur < 0 {
		check.Status = "error"
		check.Message = "invalid receipts wait timeout " + timeout
		return []nocPreflightData{check}, usageErrorf("%s; use a non-negative duration like 60s or 5m", check.Message)
	}
	check.Status = "ok"
	check.Message = "receipts wait timeout accepted"
	return []nocPreflightData{check}, nil
}

func sortedMapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func nocActionProject(projects []nocProjectJSONData, targetID string) (nocProjectJSONData, bool) {
	dir := nocActionTargetProjectDir(targetID)
	if dir == "" {
		return nocProjectJSONData{}, false
	}
	for _, project := range projects {
		if project.Dir == dir {
			return project, true
		}
	}
	return nocProjectJSONData{}, false
}

func stringInSlice(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func cloneStringMap(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func nocActionCommandProfile(action nocActionJSONData) string {
	fields := strings.Fields(action.Command)
	for i, field := range fields {
		if field != "--profile" || i+1 >= len(fields) {
			continue
		}
		return strings.Trim(strings.TrimSpace(fields[i+1]), "'\"")
	}
	return ""
}

func normalizeNOCActionVars(vars map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range vars {
		key := normalizeNOCActionVarKey(k)
		if key != "" {
			out[key] = strings.TrimSpace(v)
		}
	}
	return out
}

func normalizeNOCTeamActionVars(action nocActionJSONData, values map[string]string) (map[string]string, error) {
	if action.Name != "new_team" && action.Name != "new_profile" {
		return values, nil
	}
	roles := strings.TrimSpace(values["roles"])
	if roles == "" || !strings.Contains(roles, "=") {
		return values, nil
	}
	spec, err := parseNOCActionTeamSpec(roles)
	if err != nil {
		return nil, usageErrorf("%s", err.Error())
	}
	out := make(map[string]string, len(values)+1)
	for k, v := range values {
		out[k] = v
	}
	out["roles"] = spec.Roles
	if spec.Binary != "" {
		binary, err := mergeNOCBinaryOverrides(spec.Binary, out["binary"])
		if err != nil {
			return nil, err
		}
		out["binary"] = binary
	}
	return out, nil
}

type nocActionTeamSpec struct {
	Roles  string
	Binary string
}

func parseNOCActionTeamSpec(raw string) (nocActionTeamSpec, error) {
	parts := strings.Split(raw, ",")
	roles := make([]string, 0, len(parts))
	seen := map[string]bool{}
	binaryByRole := map[string]string{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		rolePart, binary, hasBinary := strings.Cut(part, "=")
		selection := strings.TrimSpace(rolePart)
		if selection == "" {
			return nocActionTeamSpec{}, fmt.Errorf("role cannot be empty")
		}
		resolved, err := catalog.ResolveSelection(selection)
		if err != nil {
			return nocActionTeamSpec{}, err
		}
		if hasBinary {
			binary = strings.TrimSpace(binary)
			if err := team.ValidateDisplayValue("binary", binary); err != nil {
				return nocActionTeamSpec{}, err
			}
		}
		for _, role := range resolved {
			if hasBinary {
				binaryByRole[role] = binary
			}
			if !seen[role] {
				seen[role] = true
				roles = append(roles, role)
			}
		}
	}
	if len(roles) == 0 {
		return nocActionTeamSpec{}, fmt.Errorf("enter at least one role, for example cto,fullstack")
	}
	return nocActionTeamSpec{
		Roles:  strings.Join(roles, ","),
		Binary: formatNOCBinaryOverrides(binaryByRole, roles),
	}, nil
}

func mergeNOCBinaryOverrides(inline, explicit string) (string, error) {
	inline = strings.TrimSpace(inline)
	explicit = strings.TrimSpace(explicit)
	if explicit == "" {
		return inline, nil
	}
	if inline == "" {
		return explicit, nil
	}
	inlineMap, err := parseKV(inline)
	if err != nil {
		return "", usageErrorf("%s", err.Error())
	}
	explicitMap, err := parseKV(explicit)
	if err != nil {
		return "", usageErrorf("%s", err.Error())
	}
	merged := map[string]string{}
	order := []string{}
	for _, source := range []map[string]string{inlineMap, explicitMap} {
		for role, binary := range source {
			key := strings.ToLower(strings.TrimSpace(role))
			if key == "" {
				continue
			}
			if previous, ok := merged[key]; ok && previous != binary {
				return "", usageErrorf("binary override for role %q is specified more than once", key)
			}
			if _, ok := merged[key]; !ok {
				order = append(order, key)
			}
			merged[key] = binary
		}
	}
	return formatNOCBinaryOverrides(merged, order), nil
}

func formatNOCBinaryOverrides(overrides map[string]string, order []string) string {
	if len(overrides) == 0 {
		return ""
	}
	seen := map[string]bool{}
	var parts []string
	for _, role := range order {
		role = strings.ToLower(strings.TrimSpace(role))
		if role == "" || seen[role] {
			continue
		}
		if binary := strings.TrimSpace(overrides[role]); binary != "" {
			parts = append(parts, role+"="+binary)
			seen[role] = true
		}
	}
	var rest []string
	for role := range overrides {
		role = strings.ToLower(strings.TrimSpace(role))
		if role != "" && !seen[role] {
			rest = append(rest, role)
		}
	}
	sort.Strings(rest)
	for _, role := range rest {
		if binary := strings.TrimSpace(overrides[role]); binary != "" {
			parts = append(parts, role+"="+binary)
		}
	}
	return strings.Join(parts, ",")
}

func normalizeNOCActionVarKey(k string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(k), "_", "-"))
}

func nocActionPlaceholders(command string) []string {
	seen := map[string]bool{}
	for start := strings.Index(command, "<"); start >= 0; start = strings.Index(command, "<") {
		command = command[start+1:]
		end := strings.Index(command, ">")
		if end < 0 {
			break
		}
		name := strings.TrimSpace(command[:end])
		if name != "" {
			seen[name] = true
		}
		command = command[end+1:]
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func nocActionPlaceholderSet(placeholders []string) map[string]bool {
	out := map[string]bool{}
	for _, placeholder := range placeholders {
		key := normalizeNOCActionVarKey(placeholder)
		if key != "" {
			out[key] = true
		}
	}
	return out
}

func nocActionValueDerived(key string, placeholders map[string]bool) bool {
	return key == "tmux-session" && placeholders["session"] || key == "binary-flag"
}

func nocActionValueOptional(actionName, key string) bool {
	switch key {
	case "binary", "allow-outside", "force":
		return true
	case "seed-from":
		return actionName == "new_session"
	case "session":
		return actionName == "new_team" || actionName == "new_profile"
	default:
		return false
	}
}

func nocActionBinaryFlagValue(binary string) string {
	binary = strings.TrimSpace(binary)
	if binary == "" {
		return ""
	}
	return "--binary " + shellQuote(binary)
}

func removeNOCOptionalBinaryPlaceholder(command, placeholder string) string {
	for _, pattern := range []string{
		" --binary " + shellQuote("<"+placeholder+">"),
		" --binary <" + placeholder + ">",
	} {
		command = strings.ReplaceAll(command, pattern, "")
	}
	return command
}

func removeNOCOptionalFlagPlaceholder(command, flag, placeholder string) string {
	for _, pattern := range []string{
		" --" + flag + " " + shellQuote("<"+placeholder+">"),
		" --" + flag + " <" + placeholder + ">",
	} {
		command = strings.ReplaceAll(command, pattern, "")
	}
	return command
}

func removeNOCStandalonePlaceholder(command, placeholder string) string {
	for _, pattern := range []string{
		" " + shellQuote("<"+placeholder+">"),
		" <" + placeholder + ">",
	} {
		command = strings.ReplaceAll(command, pattern, "")
	}
	return command
}

func parseNOCOptionalBool(name, raw string) (bool, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false, false, nil
	}
	v, err := parseNewBoolFlag("--set "+name, raw)
	if err != nil {
		return false, true, usageErrorf("%s", err.Error())
	}
	return v, true, nil
}

func nocActionTargetProjectDir(targetID string) string {
	switch {
	case strings.HasPrefix(targetID, "project|"):
		return strings.TrimPrefix(targetID, "project|")
	case strings.HasPrefix(targetID, "session|"):
		rest := strings.TrimPrefix(targetID, "session|")
		idx := strings.LastIndex(rest, "|")
		if idx > 0 {
			return rest[:idx]
		}
	case strings.HasPrefix(targetID, "agent|"):
		rest := strings.TrimPrefix(targetID, "agent|")
		for i := 0; i < 2; i++ {
			idx := strings.LastIndex(rest, "|")
			if idx <= 0 {
				return ""
			}
			rest = rest[:idx]
		}
		return rest
	}
	return ""
}

func nocActionTargetSession(targetID string) string {
	if !strings.HasPrefix(targetID, "session|") {
		return ""
	}
	rest := strings.TrimPrefix(targetID, "session|")
	idx := strings.LastIndex(rest, "|")
	if idx <= 0 || idx == len(rest)-1 {
		return ""
	}
	return rest[idx+1:]
}

func runNOCActionCommand(s nocExecution, action nocActionJSONData, command string, preflight []nocPreflightData) error {
	out := s.Out
	if out == nil {
		out = os.Stdout
	}
	fmt.Fprintf(out, "NOC action: %s\n", action.ID)
	fmt.Fprintf(out, "Scope: %s\n", action.Scope)
	fmt.Fprintf(out, "Target: %s\n", action.TargetID)
	fmt.Fprintf(out, "Command: %s\n", command)
	for _, check := range preflight {
		fmt.Fprintf(out, "Preflight: %s %s - %s\n", check.Status, check.Check, check.Message)
	}
	if s.DryRun {
		fmt.Fprintln(out, "Dry run: no command executed.")
		return nil
	}
	if nocActionReadsStdinBody(command) {
		return usageErrorf("NOC action %q delivers a prompt body over stdin (--body-file -); the NOC will not run a stdin-body send. Copy the command and pipe a body explicitly, or use --dry-run to inspect it.", action.ID)
	}
	if action.Mutates || action.RequiresConfirmation {
		if !s.Yes {
			confirm := s.Confirm
			if confirm == nil {
				confirm = os.Stdin
			}
			if !confirmNOCAction(out, confirm, action) {
				fmt.Fprintln(out, "noc: action aborted; no command executed.")
				return nil
			}
		}
	}
	run := s.RunActionCommand
	if run == nil {
		run = runNOCActionShellCommand
	}
	if err := run(command); err != nil {
		return fmt.Errorf("run NOC action %q: %w", action.ID, err)
	}
	return nil
}

func confirmNOCAction(out io.Writer, r io.Reader, action nocActionJSONData) bool {
	fmt.Fprintf(out, "Run mutating action %q? [y/N] ", action.ID)
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

func runNOCActionShellCommand(command string) error {
	cmd := exec.Command("sh", "-c", command)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func nocMutatingActionCount(actions []nocActionJSONData) int {
	count := 0
	for _, action := range actions {
		if action.Mutates {
			count++
		}
	}
	return count
}

func nocProjectEnvelope(ps noc.ProjectSnapshot) nocProjectJSONData {
	sessions := make([]nocSessionJSONData, 0, len(ps.Snap.Sessions))
	live, total := 0, 0
	for _, sess := range ps.Snap.Sessions {
		row := nocSessionEnvelope(ps, sess)
		live += row.AgentsAlive
		total += row.AgentsTotal
		sessions = append(sessions, row)
	}
	projectID := nocProjectJSONID(ps.Dir)
	return nocProjectJSONData{
		ID:             projectID,
		Project:        ps.Project,
		Dir:            ps.Dir,
		BaseRoot:       nocProjectBaseRoot(ps),
		State:          nocProjectJSONState(ps, live, total),
		ReasonCode:     nocProjectJSONReasonCode(ps, live, total),
		TeamConfigured: ps.TeamConfigured,
		DefaultTeam:    ps.DefaultTeam,
		Profiles:       append([]string(nil), ps.Profiles...),
		Operator: nocOperatorJSONData{
			Enabled:  ps.Operator.Enabled,
			Handle:   ps.Operator.Handle,
			Runnable: ps.Operator.Runnable,
		},
		Capabilities: nocCapabilitiesJSONData{
			OperatorGates:  ps.Capabilities.OperatorGates,
			RuntimeActions: ps.Capabilities.RuntimeActions,
		},
		Candidate:    ps.Candidate,
		SessionStore: ps.SessionStore,
		SessionNames: append([]string(nil), ps.SessionNames...),
		Warning:      ps.Warning,
		SessionCount: len(sessions),
		AgentsTotal:  total,
		AgentsAlive:  live,
		LastActivity: jsonTimePtr(nocProjectLastActivity(ps)),
		Rollup:       nocRollupEnvelope(ps.Snap.Rollup),
		Sessions:     sessions,
		Actions:      nocProjectActions(ps, projectID, len(sessions)),
	}
}

func nocProjectBaseRoot(ps noc.ProjectSnapshot) string {
	if baseRoot := strings.TrimSpace(ps.Snap.BaseRoot); baseRoot != "" {
		return baseRoot
	}
	if ps.SessionStore || len(ps.SessionNames) > 0 || len(ps.Snap.Sessions) > 0 {
		return filepath.Join(ps.Dir, noc.AgentMailDirName)
	}
	return ""
}

func nocSessionEnvelope(ps noc.ProjectSnapshot, sess state.Session) nocSessionJSONData {
	agents := make([]nocAgentJSONData, 0, len(sess.Agents))
	liveVisible, liveOperational := 0, 0
	sessionID := nocSessionJSONID(ps.Dir, sess.Name)
	for _, ag := range sess.Agents {
		if nocAgentVisibleOnline(ag) {
			liveVisible++
		}
		if nocAgentOperational(ag) {
			liveOperational++
		}
		agentAttention := nocAgentPrimaryAttention(sess, ag)
		profile := strings.TrimSpace(ag.TeamProfile)
		if profile == "" {
			profile = team.DefaultProfile
		}
		agents = append(agents, nocAgentJSONData{
			Handle:          ag.Handle,
			Role:            ag.Role,
			Engine:          ag.Engine,
			Liveness:        string(ag.Liveness),
			WakeHealth:      string(ag.WakeHealth),
			Attention:       string(agentAttention.State),
			AttentionReason: string(agentAttention.Reason),
			LastSeen:        jsonTimePtr(ag.LastSeen),
			Presence:        ag.Presence,
			Conversation:    ag.Conversation,
			Source:          ag.Source,
			TeamProfile:     profile,
			IsLead:          ag.IsLead,
			ID:              nocAgentJSONID(ps.Dir, sess.Name, ag.Handle),
			Actions:         nocAgentActions(ps, sess, ag),
		})
	}
	threads := threadRows(sess.Coordination.Threads)
	threadCount := len(threads)
	if len(threads) > defaultThreadsLimit {
		threads = threads[:defaultThreadsLimit]
	}
	sessionAttention := nocSessionPrimaryAttentionDetail(sess)
	return nocSessionJSONData{
		ID:              sessionID,
		Name:            sess.Name,
		Root:            sess.Root,
		State:           nocSessionJSONState(sess, liveVisible, len(sess.Agents)),
		ReasonCode:      nocSessionJSONReasonCode(sess, liveVisible, len(sess.Agents)),
		AgentsTotal:     len(agents),
		AgentsAlive:     liveVisible,
		ThreadCount:     threadCount,
		ThreadsReturned: len(threads),
		Attention:       string(sessionAttention.State),
		AttentionReason: string(sessionAttention.Reason),
		UnownedEvidence: liveAttentionString(sess.UnownedAttention),
		Orchestrated:    sess.Orchestrated,
		Lead:            sess.LeadRole,
		LeadHandle:      sess.LeadHandle,
		LeadDown:        state.SessionLeadDown(sess),
		Rollup:          nocRollupEnvelope(sess.Rollup),
		Threads:         threads,
		Agents:          agents,
		Actions:         nocSessionActions(ps, sess, sessionID, liveOperational, len(agents)),
	}
}

// liveAttentionString renders an attention tier for JSON, returning "" for clear
// so an omitempty field is dropped when there is nothing outstanding.
func liveAttentionString(a state.Attention) string {
	if a.State == "" || a.State == state.TriageClear {
		return ""
	}
	return string(a.State)
}

func nocRollupEnvelope(r state.TriageRollup) nocRollupData {
	return nocRollupData{
		NeedsYou:           r.NeedsYou,
		NeedsYouHistorical: r.NeedsYouHistorical,
		AtRisk:             r.AtRisk,
		Blocked:            r.Blocked,
		Gated:              r.Gated,
		AtRiskStale:        r.AtRiskStale,
		BlockedStale:       r.BlockedStale,
		GatedStale:         r.GatedStale,
		Clear:              r.Clear,
	}
}

func nocProjectActions(ps noc.ProjectSnapshot, projectID string, sessionCount int) []nocActionJSONData {
	actions := []nocActionJSONData{
		nocAction("project", projectID, "doctor",
			shellCommand("amq-squad", "doctor", "--project", ps.Dir, "--all-profiles"),
			"Check AMQ, tmux, wake, markers, and all team profile health for this team-home.",
			false, false, false),
	}
	if ps.TeamConfigured || ps.SessionStore || sessionCount > 0 {
		actions = append(actions, nocAction("project", projectID, "history",
			shellCommand("amq-squad", "history", "--project", ps.Dir),
			"Show restorable launch records for this team-home.",
			false, false, false))
	}
	if ps.TeamConfigured {
		resumePlan := nocAction("project", projectID, "resume_plan",
			nocResumePlanCommand(ps.Dir, nocProjectActionProfile(ps)),
			"Show the per-member recovery plan for this team profile without launching it.",
			false, false, len(ps.Profiles) > 1)
		resumePlan = withNOCActionVarChoices(resumePlan, "profile", ps.Profiles)
		actions = append(actions, resumePlan)
	}
	if ps.TeamConfigured || sessionCount > 0 || ps.SessionStore {
		actions = append(actions, nocAction("project", projectID, "status",
			shellCommand("amq-squad", "status", "--project", ps.Dir),
			"Show the project-scoped session board.",
			false, false, false))
	}
	if sessionCount > 0 || ps.SessionStore {
		root := nocProjectBaseRoot(ps)
		actions = append(actions,
			nocAction("project", projectID, "amq_env",
				nocAMQEnvCommand(root),
				"Show AMQ env JSON for this project's base root.",
				false, false, false),
			nocAction("project", projectID, "amq_who",
				nocAMQWhoCommand(root),
				"List AMQ sessions and agents for this project's base root.",
				false, false, false),
			nocAction("project", projectID, "route_explain",
				nocRouteExplainCommand(root, nocOperatorHandle(ps), "<to>", ""),
				"Explain AMQ routing from the operator to a recipient in this project.",
				false, false, true))
	}
	if ps.TeamConfigured || !ps.DefaultTeam {
		actions = append(actions, nocAction("project", projectID, "roles",
			shellCommand("amq-squad", "roles"),
			"List built-in role IDs and market numbers for team creation.",
			false, false, false))
	}
	if ps.TeamConfigured {
		actions = append(actions, nocAction("project", projectID, "team_profiles",
			shellCommand("amq-squad", "team", "profiles", "--project", ps.Dir),
			"List configured team profiles for this team-home.",
			false, false, false))
		actions = append(actions, nocAction("project", projectID, "team_rules",
			nocTeamRulesCommand(ps.Dir),
			"Show this team-home's durable team-rules.md.",
			false, false, false))
		deleteTeam := nocAction("project", projectID, "delete_team",
			nocDeleteTeamCommand(ps.Dir),
			"Delete one configured team profile file. Does not delete AMQ sessions, briefs, team-rules.md, or pointer stubs.",
			true, true, true)
		deleteTeam = withNOCActionVarChoices(deleteTeam, "profile", ps.Profiles)
		actions = append(actions, deleteTeam)
		syncPointers := nocAction("project", projectID, "sync_pointers",
			nocTeamSyncTemplateCommand(ps.Dir, nocProjectActionProfile(ps)),
			"Write managed CLAUDE.md / AGENTS.md pointer stubs for a configured profile.",
			true, true, true)
		syncPointers = withNOCActionVarChoices(syncPointers, "profile", ps.Profiles)
		syncPointers = withNOCActionVarChoices(syncPointers, "allow-outside", []string{"false", "true"})
		actions = append(actions, syncPointers)
		newSession := nocAction("project", projectID, "new_session",
			nocNewSessionTemplateCommand(ps.Dir, nocProjectActionProfile(ps)),
			"Create a fresh workstream session for this team.",
			true, true, true)
		newSession = withNOCActionVarChoices(newSession, "profile", ps.Profiles)
		actions = append(actions, newSession)
		actions = append(actions, nocAction("project", projectID, "new_profile",
			nocNewProfileTemplateCommand(ps.Dir),
			"Create an additional named team profile and sync pointer stubs.",
			true, true, true))
	}
	if !ps.DefaultTeam {
		actions = append(actions, nocAction("project", projectID, "new_team",
			nocNewTeamTemplateCommand(ps.Dir, team.DefaultProfile),
			"Create the default team profile and sync pointer stubs.",
			true, true, true))
	}
	return actions
}

func nocSessionActions(ps noc.ProjectSnapshot, sess state.Session, sessionID string, live, total int) []nocActionJSONData {
	profile, template := nocSessionActionProfile(sess)
	profileChoices := nocSessionProfileChoices(sess)
	actions := []nocActionJSONData{
		nocAction("session", sessionID, "status",
			shellCommand("amq-squad", "status", "--project", ps.Dir, "--session", sess.Name),
			"Show this session's detail table.",
			false, false, false),
		nocAction("session", sessionID, "threads",
			nocThreadsCommand(ps.Dir, sess.Name),
			"Show this session's collapsed AMQ thread summaries.",
			false, false, false),
		nocAction("session", sessionID, "thread_context_any",
			nocThreadContextAnyCommand(ps.Dir, sess.Name),
			"Read any thread transcript in this session by thread id without moving unread mail.",
			false, false, true),
		nocAction("session", sessionID, "amq_ops",
			nocAMQDoctorOpsCommand(sess.Root),
			"Show AMQ queue depth, DLQ, presence, and integration health for this session.",
			false, false, false),
		nocAction("session", sessionID, "presence",
			shellCommand("amq", "presence", "list", "--root", sess.Root),
			"List AMQ presence records for this session.",
			false, false, false),
		nocAction("session", sessionID, "route_explain",
			nocRouteExplainCommand(sess.Root, nocOperatorHandle(ps), "<to>", sess.Name),
			"Explain AMQ routing from the operator to a recipient in this session.",
			false, false, true),
		nocAction("session", sessionID, "amq_cleanup",
			nocAMQCleanupCommand(sess.Root),
			"Remove stale AMQ tmp files for this session after a local age preflight; extension metadata is left to its owning layer.",
			true, true, true),
		withNOCActionVarChoices(nocAction("session", sessionID, "resume",
			nocResumeCommand(ps.Dir, profile, sess.Name),
			"Resume this workstream in a detached tmux session.",
			true, true, template), "profile", profileChoices),
	}
	if strings.TrimSpace(sess.Name) != "" {
		actions = append(actions,
			nocAction("session", sessionID, "brief",
				shellCommand("amq-squad", "brief", "--project", ps.Dir, "--session", sess.Name),
				"Show this session's full workstream brief.",
				false, false, false),
			withNOCActionVarChoices(nocAction("session", sessionID, "brief_seed",
				nocBriefSeedCommand(ps.Dir, sess.Name),
				"Seed or overwrite this session's workstream brief from file, issue, or GitHub issue source.",
				true, true, true), "force", []string{"false", "true"}),
			withNOCActionVarChoices(nocAction("session", sessionID, "fork_plan",
				nocForkPlanCommand(ps.Dir, profile, sess.Name),
				"Plan a fresh target workstream branched from this session without launching it.",
				false, false, true), "profile", profileChoices))
	}
	if th, ok := nocTopNeedsYouThread(sess, ""); ok {
		actions = append(actions, nocNeedsYouActions("session", sessionID, sess.Root, th, nocOperatorHandle(ps))...)
	}
	if recipients := nocSessionRecipients(sess, nocOperatorHandle(ps)); len(recipients) > 0 {
		actions = append(actions, nocAction("session", sessionID, "broadcast",
			nocBroadcastCommand(sess.Root, recipients, nocOperatorHandle(ps)),
			"Send an operator status broadcast to every agent in this session.",
			true, true, true))
	}
	if total > 0 {
		actions = append(actions, withNOCActionVarChoices(nocAction("session", sessionID, "stop",
			nocStopCommand(ps.Dir, profile, sess.Name),
			"Stop all agents in this session while preserving resumable state.",
			true, true, template), "profile", profileChoices))
	}
	if live > 0 {
		actions = append(actions, withNOCActionVarChoices(nocAction("session", sessionID, "restart",
			nocStopCommand(ps.Dir, profile, sess.Name)+" && "+nocResumeCommand(ps.Dir, profile, sess.Name),
			"Stop and then resume this session.",
			true, true, template), "profile", profileChoices))
	}
	if strings.TrimSpace(sess.Name) != "" {
		actions = append(actions,
			nocAction("session", sessionID, "archive",
				shellCommand("amq-squad", "archive", "--project", ps.Dir, "--yes", sess.Name),
				"Move this session aside into the project archive without deleting it.",
				true, true, false),
			nocAction("session", sessionID, "remove",
				shellCommand("amq-squad", "rm", "--project", ps.Dir, "--yes", sess.Name),
				"Permanently remove this session root and brief.",
				true, true, false))
	}
	return actions
}

func nocAgentActions(ps noc.ProjectSnapshot, sess state.Session, ag state.Agent) []nocActionJSONData {
	handle := strings.TrimSpace(ag.Handle)
	if handle == "" {
		return nil
	}
	agentID := nocAgentJSONID(ps.Dir, sess.Name, ag.Handle)
	actions := []nocActionJSONData{
		nocAction("agent", agentID, "inbox",
			shellCommand("amq", "list", "--root", sess.Root, "--me", handle, "--new"),
			"List unread AMQ messages for this agent without moving them.",
			false, false, false),
		nocAction("agent", agentID, "dlq",
			shellCommand("amq", "dlq", "list", "--root", sess.Root, "--me", handle),
			"List failed or corrupt AMQ messages for this agent without retrying or purging.",
			false, false, false),
		nocAction("agent", agentID, "receipts",
			shellCommand("amq", "receipts", "list", "--root", sess.Root, "--me", handle),
			"List delivery receipts emitted by this agent.",
			false, false, false),
		nocAction("agent", agentID, "route_explain",
			nocRouteExplainCommand(sess.Root, nocOperatorHandle(ps), handle, sess.Name),
			"Explain AMQ routing from the operator to this agent.",
			false, false, false),
		withNOCActionVarChoices(nocAction("agent", agentID, "receipts_wait",
			shellCommand("amq", "receipts", "wait", "--root", sess.Root, "--me", handle, "--msg-id", "<msg-id>", "--stage", "<stage>", "--timeout", "<timeout>"),
			"Wait for a specific delivery receipt stage for this agent.",
			false, false, true), "stage", []string{"drained", "dlq"}),
		nocAction("agent", agentID, "dlq_read",
			shellCommand("amq", "dlq", "read", "--root", sess.Root, "--me", handle, "--id", "<dlq-id>"),
			"Read one DLQ message and mark it inspected.",
			true, true, true),
		nocAction("agent", agentID, "dlq_retry",
			shellCommand("amq", "dlq", "retry", "--root", sess.Root, "--me", handle, "--id", "<dlq-id>"),
			"Retry one DLQ message by moving the original back to the agent inbox.",
			true, true, true),
		nocAction("agent", agentID, "dlq_retry_all",
			shellCommand("amq", "dlq", "retry", "--root", sess.Root, "--me", handle, "--all"),
			"Retry every new DLQ message for this agent.",
			true, true, false),
		nocAction("agent", agentID, "dlq_purge",
			shellCommand("amq", "dlq", "purge", "--root", sess.Root, "--me", handle, "--older-than", "<older-than>", "--yes"),
			"Permanently purge this agent's DLQ messages older than a required age threshold.",
			true, true, true),
		nocAction("agent", agentID, "message",
			nocMessageCommand(sess.Root, handle, nocOperatorHandle(ps)),
			"Send a direct operator message to this agent.",
			true, true, true),
		nocAction("agent", agentID, "message_wait",
			nocMessageWaitCommand(sess.Root, handle, nocOperatorHandle(ps)),
			"Send a direct operator message and wait for the drained receipt.",
			true, true, true),
		nocAction("agent", agentID, "drain",
			shellCommand("amq", "drain", "--root", sess.Root, "--me", handle, "--include-body"),
			"Drain this agent's unread AMQ messages, moving them to cur and emitting receipts.",
			true, true, false),
	}
	if th, ok := nocTopNeedsYouThread(sess, handle); ok {
		actions = append(actions, nocNeedsYouActions("agent", agentID, sess.Root, th, nocOperatorHandle(ps))...)
	}
	if role := strings.TrimSpace(ag.Role); role != "" {
		actions = append(actions, nocAction("agent", agentID, "agent_resume",
			shellCommand("amq-squad", "agent", "resume", role, "--project", ps.Dir, "--session", sess.Name),
			"Resume this saved agent launch record by role.",
			true, true, false))
	}
	return actions
}

func nocAction(scope, targetID, name, command, description string, mutates, confirm, template bool) nocActionJSONData {
	return nocActionJSONData{
		Name:                 name,
		ID:                   targetID + "|action|" + name,
		Scope:                scope,
		TargetID:             targetID,
		Command:              command,
		Description:          description,
		Mutates:              mutates,
		RequiresConfirmation: confirm,
		Template:             template,
		Vars:                 nocActionVariables(name, command),
	}
}

func withNOCActionVarChoices(action nocActionJSONData, name string, choices []string) nocActionJSONData {
	choices = sortedUniqueStrings(choices)
	if len(choices) == 0 {
		return action
	}
	for i := range action.Vars {
		if action.Vars[i].Name == name {
			action.Vars[i].Choices = append([]string(nil), choices...)
		}
	}
	return action
}

func nocActionVariables(actionName, command string) []nocActionVariableData {
	placeholders := nocActionPlaceholders(command)
	if len(placeholders) == 0 {
		return nil
	}
	set := nocActionPlaceholderSet(placeholders)
	vars := make([]nocActionVariableData, 0, len(placeholders))
	seen := map[string]bool{}
	for _, placeholder := range placeholders {
		key := normalizeNOCActionVarKey(placeholder)
		if key == "" {
			continue
		}
		name := key
		if key == "binary-flag" {
			name = "binary"
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		v := nocActionVariableData{
			Name:        name,
			Required:    !nocActionValueDerived(key, set) && !nocActionValueOptional(actionName, key),
			Description: nocActionVariableDescription(key),
			Examples:    nocActionVariableExamples(key),
		}
		if nocActionValueDerived(key, set) {
			switch key {
			case "tmux-session":
				v.DerivedFrom = "session"
			}
		}
		vars = append(vars, v)
	}
	return vars
}

func sortedUniqueStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func nocActionVariableDescription(key string) string {
	switch key {
	case "session":
		return "New or selected AMQ workstream session name."
	case "profile":
		return "Team profile name."
	case "seed-from":
		return "Brief seed source: file:<path>, issue:<n>, or gh:owner/repo#<n>."
	case "subject":
		return "AMQ message subject."
	case "body":
		return "AMQ message body."
	case "reason":
		return "Denial reason; command sends DENIED plus this reason."
	case "msg-id":
		return "AMQ message ID to inspect or wait for."
	case "thread-id":
		return "AMQ thread ID to inspect."
	case "to":
		return "AMQ recipient handle to explain routing to."
	case "stage":
		return "AMQ receipt stage."
	case "dlq-id":
		return "DLQ message ID from amq dlq list."
	case "older-than":
		return "Positive age threshold for DLQ purge, such as 24h or 168h."
	case "tmp-older-than":
		return "Positive age threshold for AMQ tmp-file cleanup, such as 36h or 168h."
	case "timeout":
		return "Non-negative wait timeout for AMQ receipt waiting."
	case "roles":
		return "Comma-separated role IDs, role market numbers, or all."
	case "binary", "binary-flag":
		return "Optional comma-separated role=binary CLI overrides."
	case "allow-outside":
		return "Optional true/false flag to sync member cwds outside the team-home."
	case "force":
		return "Optional true/false flag to overwrite an existing brief or team-rules.md."
	case "tmux-session":
		return "Detached terminal session name."
	default:
		return ""
	}
}

func nocActionVariableExamples(key string) []string {
	switch key {
	case "session":
		return []string{"issue-97", "v0-5-0"}
	case "profile":
		return []string{"review"}
	case "seed-from":
		return []string{"issue:31", "file:./brief.md", "gh:owner/repo#31"}
	case "subject":
		return []string{"Status update", "Need review"}
	case "body":
		return []string{"Please check the latest run.", "Proceed with the plan."}
	case "reason":
		return []string{"Not safe to run yet", "Need tests first"}
	case "msg-id":
		return []string{"msg_123", "20260601T090000Z_abc123"}
	case "thread-id":
		return []string{"p2p/cto__fullstack", "decision/ship"}
	case "to":
		return []string{"cto", "qa"}
	case "dlq-id":
		return []string{"dlq_123", "dlq_123.md"}
	case "stage":
		return []string{"drained", "dlq"}
	case "older-than":
		return []string{"24h", "168h"}
	case "tmp-older-than":
		return []string{"36h", "168h"}
	case "timeout":
		return []string{"60s", "5m"}
	case "roles":
		return []string{"cto,qa", "2,9", "all", "cto=codex,qa"}
	case "binary", "binary-flag":
		return []string{"qa=codex", "cto=claude,qa=codex"}
	case "allow-outside":
		return []string{"true", "false"}
	case "force":
		return []string{"true", "false"}
	default:
		return nil
	}
}

func nocProjectJSONID(dir string) string {
	return "project|" + dir
}

func nocSessionJSONID(dir, session string) string {
	return "session|" + dir + "|" + session
}

func nocAgentJSONID(dir, session, handle string) string {
	return "agent|" + dir + "|" + session + "|" + handle
}

func nocProjectActionProfile(ps noc.ProjectSnapshot) string {
	switch len(ps.Profiles) {
	case 0:
		return team.DefaultProfile
	case 1:
		return ps.Profiles[0]
	default:
		return "<profile>"
	}
}

func nocSessionActionProfile(sess state.Session) (string, bool) {
	profiles := map[string]bool{}
	for _, ag := range sess.Agents {
		profile := strings.TrimSpace(ag.TeamProfile)
		if profile == "" {
			profile = team.DefaultProfile
		}
		profiles[profile] = true
	}
	if len(profiles) == 0 {
		return team.DefaultProfile, false
	}
	if len(profiles) > 1 {
		return "<profile>", true
	}
	for profile := range profiles {
		return profile, false
	}
	return team.DefaultProfile, false
}

func nocSessionProfileChoices(sess state.Session) []string {
	profiles := map[string]bool{}
	for _, ag := range sess.Agents {
		profile := strings.TrimSpace(ag.TeamProfile)
		if profile == "" {
			profile = team.DefaultProfile
		}
		profiles[profile] = true
	}
	out := make([]string, 0, len(profiles))
	for profile := range profiles {
		out = append(out, profile)
	}
	sort.Strings(out)
	return out
}

func nocNewSessionTemplateCommand(projectDir, profile string) string {
	args := []string{"new", "session", "--project", projectDir}
	if profile := strings.TrimSpace(profile); profile != "" && profile != team.DefaultProfile {
		args = append(args, "--profile", profile)
	}
	args = append(args, "--seed-from", "<seed-from>", "--target", "new-session", "--terminal-session", "<tmux-session>", "<session>")
	return shellCommand("amq-squad", args...)
}

func nocResumePlanCommand(projectDir, profile string) string {
	args := []string{"resume", "--project", projectDir}
	if profile := strings.TrimSpace(profile); profile != "" && profile != team.DefaultProfile {
		args = append(args, "--profile", profile)
	}
	return shellCommand("amq-squad", args...)
}

func nocForkPlanCommand(projectDir, profile, fromSession string) string {
	args := []string{"fork", "--project", projectDir}
	if profile := strings.TrimSpace(profile); profile != "" && profile != team.DefaultProfile {
		args = append(args, "--profile", profile)
	}
	args = append(args, "--from", fromSession, "--as", "<session>")
	return shellCommand("amq-squad", args...)
}

func nocBriefSeedCommand(projectDir, session string) string {
	return shellCommand("amq-squad", "brief", "seed",
		"--project", projectDir,
		"--session", session,
		"--seed-from", "<seed-from>",
		"<force>")
}

func nocAMQCleanupCommand(root string) string {
	return shellCommand("amq", "cleanup",
		"--root", root,
		"--tmp-older-than", "<tmp-older-than>",
		"--yes")
}

func nocAMQEnvCommand(root string) string {
	return shellCommand("amq", "env", "--root", root, "--json")
}

func nocAMQWhoCommand(root string) string {
	return shellCommand("amq", "who", "--root", root)
}

func nocRouteExplainCommand(root, from, to, session string) string {
	args := []string{"route", "explain", "--from-root", root, "--me", from, "--to", to, "--json"}
	if strings.TrimSpace(session) != "" {
		args = append(args, "--session", session)
	}
	return shellCommand("amq", args...)
}

func nocTeamRulesCommand(projectDir string) string {
	return shellCommand("amq-squad", "team", "rules", "show", "--project", projectDir)
}

func nocDeleteTeamCommand(projectDir string) string {
	return shellCommand("amq-squad", "team", "rm", "--project", projectDir, "--profile", "<profile>", "--yes")
}

func nocNewTeamTemplateCommand(projectDir, profile string) string {
	profile = strings.TrimSpace(profile)
	args := []string{"new", "team"}
	if profile != "" && profile != team.DefaultProfile {
		args = []string{"new", "profile", profile}
	}
	args = append(args, "--project", projectDir)
	args = append(args, "--roles", "<roles>", "--binary", "<binary>", "--session", "<session>")
	return shellCommand("amq-squad", args...)
}

func nocNewProfileTemplateCommand(projectDir string) string {
	args := []string{"new", "profile", "<profile>", "--project", projectDir, "--roles", "<roles>", "--binary", "<binary>", "--session", "<session>"}
	return shellCommand("amq-squad", args...)
}

func nocTeamSyncTemplateCommand(projectDir, profile string) string {
	args := []string{"team", "sync", "--project", projectDir}
	if profile := strings.TrimSpace(profile); profile != "" && profile != team.DefaultProfile {
		args = append(args, "--profile", profile)
	}
	args = append(args, "<allow-outside>", "--apply")
	return shellCommand("amq-squad", args...)
}

func nocOperatorHandle(ps noc.ProjectSnapshot) string {
	if handle := ps.OperatorGateHandle(); handle != "" {
		return handle
	}
	return state.DefaultOperatorHandle
}

func nocMessageCommand(root, handle, operatorHandle string) string {
	return shellCommand("amq", "send",
		"--root", root,
		"--me", operatorHandle,
		"--to", handle,
		"--subject", "Message from operator",
		"--body", "<body>",
		"--kind", string(state.KindStatus))
}

func nocMessageWaitCommand(root, handle, operatorHandle string) string {
	return shellCommand("amq", "send",
		"--root", root,
		"--me", operatorHandle,
		"--to", handle,
		"--subject", "Message from operator",
		"--body", "<body>",
		"--kind", string(state.KindStatus),
		"--wait-for", "drained",
		"--wait-timeout", "<timeout>")
}

func nocBroadcastCommand(root string, recipients []string, operatorHandle string) string {
	return shellCommand("amq", "send",
		"--root", root,
		"--me", operatorHandle,
		"--to", strings.Join(recipients, ","),
		"--subject", "<subject>",
		"--body", "<body>",
		"--thread", act.BroadcastThread,
		"--kind", string(state.KindStatus))
}

func nocNeedsYouActions(scope, targetID, root string, th state.ThreadSummary, operatorHandle string) []nocActionJSONData {
	if len(nocThreadRecipients(th, operatorHandle)) == 0 {
		return nil
	}
	actions := []nocActionJSONData{
		nocAction(scope, targetID, "thread_context",
			nocThreadContextCommand(root, th.ID),
			"Read the top needs-you thread transcript without moving unread mail.",
			false, false, false),
	}
	if messageID := strings.TrimSpace(th.LatestID); messageID != "" {
		actions = append(actions, nocAction(scope, targetID, "read_needs_you",
			nocReadNeedsYouCommand(root, messageID, operatorHandle),
			"Read the top needs-you message body for this row; moves it to cur like amq read.",
			true, true, false))
	}
	return append(actions,
		nocAction(scope, targetID, "reply",
			nocReplyCommand(root, th, operatorHandle),
			"Reply to the top needs-you thread for this row with a custom answer.",
			true, true, true),
		nocAction(scope, targetID, "approve",
			nocApproveCommand(root, th, operatorHandle),
			"Approve the top needs-you thread for this row.",
			true, true, false),
		nocAction(scope, targetID, "deny",
			nocDenyCommand(root, th, operatorHandle),
			"Deny the top needs-you thread for this row with a reason.",
			true, true, true))
}

func nocThreadContextCommand(root, threadID string) string {
	return shellCommand("amq", "thread",
		"--root", root,
		"--id", threadID,
		"--include-body",
		"--limit", "20")
}

func nocThreadsCommand(projectDir, session string) string {
	return shellCommand("amq-squad", "threads",
		"--project", projectDir,
		"--session", session,
		"--limit", fmt.Sprint(defaultThreadsLimit))
}

func nocThreadContextAnyCommand(projectDir, session string) string {
	return shellCommand("amq-squad", "thread",
		"--project", projectDir,
		"--session", session,
		"--id", "<thread-id>",
		"--include-body",
		"--limit", fmt.Sprint(defaultThreadTranscriptLimit))
}

func nocReadNeedsYouCommand(root, messageID, operatorHandle string) string {
	return shellCommand("amq", "read",
		"--root", root,
		"--me", operatorHandle,
		"--id", messageID,
		"--json")
}

func nocReplyCommand(root string, th state.ThreadSummary, operatorHandle string) string {
	return shellCommand("amq", "send",
		"--root", root,
		"--me", operatorHandle,
		"--to", strings.Join(nocThreadRecipients(th, operatorHandle), ","),
		"--subject", nocReplySubject(th.Subject),
		"--body", "<body>",
		"--thread", th.ID,
		"--kind", string(state.KindAnswer))
}

func nocApproveCommand(root string, th state.ThreadSummary, operatorHandle string) string {
	return shellCommand("amq", "send",
		"--root", root,
		"--me", operatorHandle,
		"--to", strings.Join(nocThreadRecipients(th, operatorHandle), ","),
		"--subject", nocReplySubject(th.Subject),
		"--body", "APPROVED",
		"--thread", th.ID,
		"--kind", string(state.KindAnswer))
}

func nocDenyCommand(root string, th state.ThreadSummary, operatorHandle string) string {
	return shellCommand("amq", "send",
		"--root", root,
		"--me", operatorHandle,
		"--to", strings.Join(nocThreadRecipients(th, operatorHandle), ","),
		"--subject", nocReplySubject(th.Subject),
		"--body", "<reason>",
		"--thread", th.ID,
		"--kind", string(state.KindAnswer))
}

func nocReplySubject(subject string) string {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "Re:"
	}
	if strings.HasPrefix(strings.ToLower(subject), "re:") {
		return subject
	}
	return "Re: " + subject
}

func nocDenyBody(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "DENIED"
	}
	return "DENIED: " + reason
}

func nocAMQDoctorOpsCommand(root string) string {
	return shellCommand("env", "AM_ROOT="+root, "amq", "doctor", "--ops")
}

func nocResumeCommand(projectDir, profile, session string) string {
	args := consoleResumeArgs(projectDir, profile, session)
	return shellCommand("amq-squad", args...)
}

func nocStopCommand(projectDir, profile, session string) string {
	args := []string{"stop", "--project", projectDir, "--all"}
	if profile := strings.TrimSpace(profile); profile != "" && profile != team.DefaultProfile {
		args = append(args, "--profile", profile)
	}
	args = append(args, "--session", session)
	return shellCommand("amq-squad", args...)
}

func nocProjectJSONState(ps noc.ProjectSnapshot, live, total int) string {
	if ps.Warning != "" {
		return "warning"
	}
	// Lead with the most-urgent operational-OWNED attention across the project's
	// sessions (mirrors console projectRollupState). Unowned raw-rollup evidence
	// does not drive the primary state - it stays in the rollup detail.
	if s := nocAttnJSONState(nocProjectOwnedAttn(ps), nocProjectHasHardStop(ps)); s != "" {
		return s
	}
	switch s := nocRollupJSONState(ps.Snap.Rollup, live, total); s {
	case "empty":
		// No agents and no evidence: fall through to the team-home descriptor.
		if ps.Candidate {
			return "candidate"
		}
		if ps.TeamConfigured {
			return "configured"
		}
		if ps.SessionStore {
			return "empty"
		}
		return "unknown"
	default:
		return s
	}
}

func nocProjectJSONReasonCode(ps noc.ProjectSnapshot, live, total int) string {
	if ps.Warning != "" {
		return "warning"
	}
	if rc := nocAttnReasonCode(nocProjectOwnedAttn(ps), nocProjectHasHardStop(ps)); rc != "" {
		return rc
	}
	return nocRollupReasonCode(ps.Snap.Rollup, live, total, projectFallbackReason(ps))
}

func projectFallbackReason(ps noc.ProjectSnapshot) string {
	if ps.Candidate {
		return "candidate"
	}
	if ps.TeamConfigured {
		return "configured"
	}
	if ps.SessionStore {
		return "empty"
	}
	return "unknown"
}

func nocSessionJSONState(sess state.Session, liveVisible, total int) string {
	// Lead with the operational-OWNED attention. nocSessionGatedAttention re-applies
	// the strict operational-owner gate (mirroring console.sessionRollupState), so a
	// non-human wait surfaces as the primary work state only when an operational
	// agent owns it; a fresh-presence-only (dead-mailbox-live) session falls through
	// to the rollup path and stays online, with the evidence retained in the rollup +
	// unowned_evidence detail. liveVisible counts visible-online agents (alive or
	// dead-mailbox-live), so the online determination matches the TUI.
	if s := nocAttnJSONState(nocSessionGatedAttention(sess), state.SessionHasHardStop(sess)); s != "" {
		return s
	}
	return nocRollupJSONState(sess.Rollup, liveVisible, total)
}

func nocSessionJSONReasonCode(sess state.Session, liveVisible, total int) string {
	if rc := nocAttnReasonCode(nocSessionGatedAttention(sess), state.SessionHasHardStop(sess)); rc != "" {
		return rc
	}
	return nocRollupReasonCode(sess.Rollup, liveVisible, total, "empty")
}

// nocSessionGatedAttention returns the session's primary attention tier ONLY when it
// is a human needs-you ask OR an operational agent owns it; otherwise TriageClear.
// This mirrors console.sessionRollupState's defensive operational-owner gate so a
// fresh-presence-only (dead-mailbox-live) session never promotes non-human evidence
// into a live waiting/blocked/gated/at-risk state, even if upstream attention gating
// regresses. needs-you persists as a human action item regardless of liveness.
func nocSessionGatedAttention(sess state.Session) state.Triage {
	att := nocSessionPrimaryAttentionDetail(sess).State
	if att == state.TriageNeedsYou || nocSessionHasOperationalAgent(sess) {
		return att
	}
	return state.TriageClear
}

// nocSessionHasOperationalAgent reports whether any agent in the session is a
// verified foreground (operational) owner of a non-human wait. dead-mailbox-live
// presence does not count.
func nocSessionHasOperationalAgent(sess state.Session) bool {
	for _, ag := range sess.Agents {
		if nocAgentOperational(ag) {
			return true
		}
	}
	return false
}

func nocSessionPrimaryAttentionDetail(sess state.Session) state.Attention {
	att := sess.Attention
	if att.State == state.TriageAtRisk && nocSessionHasOperationalAgent(sess) && nocSessionHasNewerClearActivity(sess) {
		return state.Attention{State: state.TriageClear}
	}
	return att
}

func nocAgentPrimaryAttention(sess state.Session, ag state.Agent) state.Attention {
	att := ag.Attention
	if att.State == state.TriageAtRisk && nocAgentOperational(ag) && nocSessionHasNewerClearActivity(sess) {
		return state.Attention{State: state.TriageClear}
	}
	return att
}

// nocAttnJSONState maps an operational-OWNED attention tier to the JSON primary
// state vocabulary, returning "" for clear/empty so the caller falls through to the
// rollup-based state.
func nocAttnJSONState(t state.Triage, hardStop bool) string {
	switch t {
	case state.TriageNeedsYou:
		return "needs-you"
	case state.TriageBlocked:
		if !hardStop {
			return "waiting"
		}
		return "blocked"
	case state.TriageGated, state.TriageAtRisk:
		return "waiting"
	default:
		return ""
	}
}

// nocAttnReasonCode is nocAttnJSONState's reason-code companion.
func nocAttnReasonCode(t state.Triage, hardStop bool) string {
	switch t {
	case state.TriageNeedsYou:
		return "needs_user"
	case state.TriageBlocked:
		if !hardStop {
			return "waiting"
		}
		return "blocked"
	case state.TriageGated, state.TriageAtRisk:
		return "waiting"
	default:
		return ""
	}
}

// nocRollupJSONState maps a raw triage rollup to the JSON primary-state vocabulary
// on the FALLBACK path - when NO operational agent owns a current attention. It
// mirrors the console rollupState contract: needs-you persists as a human action
// item even with no live agent; an operational agent that owns no wait is online;
// zero operational agents with only outstanding, decayed, or unowned evidence is
// stale (the evidence is retained in the rollup + unowned_evidence detail, never a
// live blocked/gated/at-risk wait); agents with nothing outstanding are stopped.
func nocRollupJSONState(r state.TriageRollup, live, total int) string {
	switch {
	case r.NeedsYou > 0:
		return "needs-you"
	case live > 0:
		return "online"
	case nocRollupHasOutstanding(r):
		return "stale-blocked"
	case total > 0:
		return "stopped"
	default:
		return "empty"
	}
}

func nocRollupReasonCode(r state.TriageRollup, live, total int, fallback string) string {
	switch {
	case r.NeedsYou > 0:
		return "needs_user"
	case live > 0:
		return "online"
	case nocRollupHasOutstanding(r):
		return "stale_blocked"
	case total > 0:
		return "stopped"
	default:
		return fallback
	}
}

// nocRollupHasOutstanding reports whether a rollup carries ANY unresolved
// non-needs-you evidence - live, age-decayed, or historical. On the zero-operational
// fallback path this evidence is unowned, so it yields a STALE primary state rather
// than a live wait.
func nocRollupHasOutstanding(r state.TriageRollup) bool {
	return r.Blocked > 0 || r.AtRisk > 0 || r.Gated > 0 ||
		r.NeedsYouHistorical > 0 || r.AtRiskStale > 0 || r.BlockedStale > 0 || r.GatedStale > 0
}

// nocProjectOwnedAttn returns the most-urgent operational-OWNED attention across a
// project's sessions (TriageClear when none owns one). sess.Attention is already
// gated to operational agents in attachAttention, so this never reflects unowned or
// stopped-session evidence.
func nocProjectOwnedAttn(ps noc.ProjectSnapshot) state.Triage {
	best := state.TriageClear
	for _, sess := range ps.Snap.Sessions {
		att := nocSessionGatedAttention(sess)
		if nocTriageSeverity(att) < nocTriageSeverity(best) {
			best = att
		}
	}
	return best
}

func nocProjectHasHardStop(ps noc.ProjectSnapshot) bool {
	for _, sess := range ps.Snap.Sessions {
		if state.SessionHasHardStop(sess) {
			return true
		}
	}
	return false
}

func nocSessionHasNewerClearActivity(sess state.Session) bool {
	var newestAtRisk time.Time
	var newestClear time.Time
	for _, th := range sess.Coordination.Threads {
		if th.Historical || th.Stale || th.LastEventAt.IsZero() {
			continue
		}
		switch th.Triage {
		case state.TriageAtRisk:
			if th.LastEventAt.After(newestAtRisk) {
				newestAtRisk = th.LastEventAt
			}
		case state.TriageClear:
			if th.LastEventAt.After(newestClear) {
				newestClear = th.LastEventAt
			}
		}
	}
	return !newestAtRisk.IsZero() && newestClear.After(newestAtRisk)
}

// nocTriageSeverity mirrors state's documented order (NeedsYou > Blocked > Gated >
// AtRisk > Clear); lower is more severe. Local copy because state.triageSeverity is
// unexported.
func nocTriageSeverity(t state.Triage) int {
	switch t {
	case state.TriageNeedsYou:
		return 0
	case state.TriageBlocked:
		return 1
	case state.TriageGated:
		return 2
	case state.TriageAtRisk:
		return 3
	default:
		return 4
	}
}

func nocProjectLastActivity(ps noc.ProjectSnapshot) time.Time {
	var last time.Time
	for _, sess := range ps.Snap.Sessions {
		for _, th := range sess.Coordination.Threads {
			if th.LastEventAt.After(last) {
				last = th.LastEventAt
			}
		}
	}
	return last
}

// nocProjectHasLiveAgent reports whether the project has any visible-online agent
// (alive or dead-mailbox-live). This drives the live_projects counter and the
// stale-only hide filter, so a fresh-presence-only project counts as online, not
// stale, matching the TUI.
func nocProjectHasLiveAgent(ps noc.ProjectSnapshot) bool {
	for _, sess := range ps.Snap.Sessions {
		for _, ag := range sess.Agents {
			if nocAgentVisibleOnline(ag) {
				return true
			}
		}
	}
	return false
}

func nocSessionRecipients(sess state.Session, operatorHandle string) []string {
	seen := map[string]bool{}
	var out []string
	for _, ag := range sess.Agents {
		handle := strings.TrimSpace(ag.Handle)
		if handle == "" || handle == operatorHandle || seen[handle] {
			continue
		}
		seen[handle] = true
		out = append(out, handle)
	}
	sort.Strings(out)
	return out
}

func nocTopNeedsYouThread(sess state.Session, handle string) (state.ThreadSummary, bool) {
	handle = strings.TrimSpace(handle)
	for _, th := range sess.Coordination.NeedsYouThreads() {
		if handle == "" || stringInSlice(th.Participants, handle) {
			return th, true
		}
	}
	return state.ThreadSummary{}, false
}

func nocThreadRecipients(th state.ThreadSummary, operatorHandle string) []string {
	seen := map[string]bool{}
	var out []string
	for _, handle := range th.Participants {
		handle = strings.TrimSpace(handle)
		if handle == "" || handle == operatorHandle || seen[handle] {
			continue
		}
		seen[handle] = true
		out = append(out, handle)
	}
	sort.Strings(out)
	return out
}

func nocProjectStaleOnly(ps noc.ProjectSnapshot) bool {
	if ps.Warning != "" {
		return false
	}
	if nocProjectHasLiveAgent(ps) {
		return false
	}
	return !ps.Snap.Rollup.HasLiveAttention()
}

// nocAgentVisibleOnline mirrors console.agentVisibleOnline (the TUI agentState ==
// nocRunning contract): an alive or dead-mailbox-live agent renders as online
// presence. dead-mailbox-live has no verified pid, but amq-squad reports a fresh
// active wake presence, so the operator TUI and these JSON snapshots both treat it
// as reachable/online. This drives JSON agents_alive, the "online" primary state,
// and project live counts, keeping JSON snapshots in the SAME primary vocabulary as
// the TUI. wake-live renders as at-risk (a wait sub-tier), not online, so it is
// excluded here and handled by nocAgentOperational.
func nocAgentVisibleOnline(ag state.Agent) bool {
	return ag.Liveness == state.LivenessAlive || ag.Liveness == state.LivenessDeadMailboxLive
}

// nocAgentOperational mirrors console.agentOperational / state.agentOperational: a
// verified foreground agent (alive or wake-live) that may OWN a non-human
// waiting/blocked/gated/at-risk state. A dead-mailbox-live agent is fresh presence
// but its process is gone, so it is NOT operational and never promotes unowned
// evidence into a live wait. This strictly gates the attention determination,
// exactly as the TUI does, so a fresh-presence-only session stays online (never
// waiting/blocked) while its evidence is retained in the rollup + attention detail.
func nocAgentOperational(ag state.Agent) bool {
	return ag.Liveness == state.LivenessAlive || ag.Liveness == state.LivenessWakeLive
}

func jsonTimePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

// consoleLifecycle is the cli-side stop/resume/restart seam handed to the NOC.
// It is invoked ONLY for a confirmed lifecycle action from the TUI. Stop drives
// the same executeDown path as `amq-squad stop --all`; resume shells back into
// the live `amq-squad resume --exec --target new-session` path so it actually
// brings the team back without writing into the NOC AltScreen. Restart runs
// those two steps in order.
func consoleLifecycle(req console.LifecycleRequest) error {
	dir := req.ProjectDir
	if strings.TrimSpace(dir) == "" {
		if cwd, err := os.Getwd(); err == nil {
			dir = cwd
		}
	}
	switch req.Verb {
	case "stop":
		return consoleStop(dir, req.Profile, req.Session)
	case "resume":
		return consoleResume(dir, req.Profile, req.Session)
	case "restart":
		if err := consoleStop(dir, req.Profile, req.Session); err != nil {
			return err
		}
		return consoleResume(dir, req.Profile, req.Session)
	default:
		return fmt.Errorf("unknown lifecycle verb %q", req.Verb)
	}
}

// delegateSquad runs the installed amq-squad CLI for a confirmed squad
// lifecycle/config action. amq-noc owns visibility and orchestration but
// delegates squad mutations to amq-squad rather than running copied
// implementations or its own (noc/version-only) command surface. The binary is
// resolved via generatedSquadCommand so previews and execution stay in lockstep
// and tests can redirect both with generatedSquadCommandOverride. dir, when set,
// becomes the child working directory; the squad subcommands also carry an
// explicit --project.
func delegateSquad(dir string, args ...string) error {
	cmd := exec.Command(generatedSquadCommand(), args...)
	if d := strings.TrimSpace(dir); d != "" {
		cmd.Dir = d
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		if detail := strings.TrimSpace(string(out)); detail != "" {
			return fmt.Errorf("%w: %s", err, detail)
		}
		return err
	}
	return nil
}

// delegateSquadStdin is the stdin-capable sibling of delegateSquad: it runs the
// installed amq-squad CLI for a confirmed squad action that reads its payload
// from STDIN (currently `amq-squad send --body-file -`, where stdin carries the
// prompt body). It mirrors delegateSquad's binary resolution, working directory,
// and combined-output error surfacing so a non-zero exit (e.g. amq-squad's
// mid-turn "busy" refusal) is returned verbatim for the NOC to render. The
// error detail is preserved so the console's "busy" detection can match it.
func delegateSquadStdin(dir, stdin string, args ...string) error {
	cmd := exec.Command(generatedSquadCommand(), args...)
	if d := strings.TrimSpace(dir); d != "" {
		cmd.Dir = d
	}
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if detail := strings.TrimSpace(string(out)); detail != "" {
			return fmt.Errorf("%w: %s", err, detail)
		}
		return err
	}
	return nil
}

// consoleSendPrompt is the cli-side send-prompt seam handed to the NOC. It is
// invoked ONLY for a confirmed send-prompt action from the TUI. It delivers the
// typed prompt to the selected agent's tmux pane via `amq-squad send`, piping the
// body to the child over STDIN (--body-file -). amq-squad owns the delivery and
// the mid-turn "busy" refusal; a non-zero exit is surfaced back to the NOC.
func consoleSendPrompt(req console.SendPromptRequest) error {
	args, err := consoleSendPromptArgs(req)
	if err != nil {
		return err
	}
	return delegateSquadStdin(req.ProjectDir, req.Body, args...)
}

// consoleSendPromptArgs builds the `amq-squad send` argv for a confirmed
// send-prompt action. It mirrors sendPromptOp.command(): --profile only when
// non-default/non-empty, and --body-file - so amq-squad reads the body from the
// stdin delegateSquadStdin pipes in.
func consoleSendPromptArgs(req console.SendPromptRequest) ([]string, error) {
	role := strings.TrimSpace(req.Role)
	if role == "" {
		return nil, fmt.Errorf("role cannot be empty")
	}
	args := []string{"send"}
	if dir := strings.TrimSpace(req.ProjectDir); dir != "" {
		args = append(args, "--project", dir)
	}
	if profile := strings.TrimSpace(req.Profile); profile != "" && profile != team.DefaultProfile {
		args = append(args, "--profile", profile)
	}
	if session := strings.TrimSpace(req.Session); session != "" {
		args = append(args, "--session", session)
	}
	args = append(args, "--role", role, "--body-file", "-")
	return args, nil
}

func consoleStop(dir, profile, session string) error {
	return delegateSquad(dir, consoleStopArgs(dir, profile, session)...)
}

// consoleStopArgs mirrors the previous in-process stop (target every configured
// member, optionally scoped to one session) as an installed `amq-squad stop`
// invocation.
func consoleStopArgs(dir, profile, session string) []string {
	args := []string{"stop", "--all"}
	if dir := strings.TrimSpace(dir); dir != "" {
		args = append(args, "--project", dir)
	}
	if profile := strings.TrimSpace(profile); profile != "" && profile != team.DefaultProfile {
		args = append(args, "--profile", profile)
	}
	if session := strings.TrimSpace(session); session != "" {
		args = append(args, "--session", session)
	}
	return args
}

func consoleResume(dir, profile, session string) error {
	session = strings.TrimSpace(session)
	if session == "" {
		return fmt.Errorf("session name cannot be empty")
	}
	return delegateSquad(dir, consoleResumeArgs(dir, profile, session)...)
}

func consoleResumeArgs(dir, profile, session string) []string {
	args := []string{"resume"}
	if dir := strings.TrimSpace(dir); dir != "" {
		args = append(args, "--project", dir)
	}
	if profile := strings.TrimSpace(profile); profile != "" && profile != team.DefaultProfile {
		args = append(args, "--profile", profile)
	}
	return append(args,
		"--exec",
		"--target", "new-session",
		"--terminal-session", nocTerminalSessionName(dir, session),
		"--session", session,
	)
}

func consoleAgentResume(req console.AgentResumeRequest) error {
	args, err := consoleAgentResumeArgs(req)
	if err != nil {
		return err
	}
	return delegateSquad(req.ProjectDir, args...)
}

func consoleAgentResumeArgs(req console.AgentResumeRequest) ([]string, error) {
	role := strings.TrimSpace(req.Role)
	if role == "" {
		return nil, fmt.Errorf("role cannot be empty")
	}
	args := []string{"agent", "resume", role}
	if dir := strings.TrimSpace(req.ProjectDir); dir != "" {
		args = append(args, "--project", dir)
	}
	if session := strings.TrimSpace(req.Session); session != "" {
		args = append(args, "--session", session)
	}
	return args, nil
}

func consoleSessionCleanup(req console.SessionCleanupRequest) error {
	args, err := consoleSessionCleanupArgs(req)
	if err != nil {
		return err
	}
	return delegateSquad(req.ProjectDir, args...)
}

func consoleSessionCleanupArgs(req console.SessionCleanupRequest) ([]string, error) {
	session := strings.TrimSpace(req.Session)
	if session == "" {
		return nil, fmt.Errorf("session name cannot be empty")
	}
	verb := "rm"
	if req.Archive {
		verb = "archive"
	}
	args := []string{verb}
	if dir := strings.TrimSpace(req.ProjectDir); dir != "" {
		args = append(args, "--project", dir)
	}
	return append(args, "--yes", session), nil
}

func resolvedNOCProfile(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return team.DefaultProfile
	}
	return profile
}

func consoleNewSession(req console.NewSessionRequest) error {
	args, err := consoleNewSessionArgs(req)
	if err != nil {
		return err
	}
	return delegateSquad(req.ProjectDir, args...)
}

func consoleNewSessionArgs(req console.NewSessionRequest) ([]string, error) {
	session := strings.TrimSpace(req.Session)
	if session == "" {
		return nil, fmt.Errorf("session name cannot be empty")
	}
	args := []string{"new", "session"}
	if dir := strings.TrimSpace(req.ProjectDir); dir != "" {
		args = append(args, "--project", dir)
	}
	if profile := strings.TrimSpace(req.Profile); profile != "" && profile != team.DefaultProfile {
		args = append(args, "--profile", profile)
	}
	if seedFrom := strings.TrimSpace(req.SeedFrom); seedFrom != "" {
		args = append(args, "--seed-from", seedFrom)
	}
	args = append(args, "--target", "new-session", "--terminal-session", nocTerminalSessionName(req.ProjectDir, session), session)
	return args, nil
}

func consoleNewTeam(req console.NewTeamRequest) error {
	args, err := consoleNewTeamArgs(req)
	if err != nil {
		return err
	}
	return delegateSquad(req.ProjectDir, args...)
}

func consoleTeamDelete(req console.TeamDeleteRequest) error {
	args, err := consoleTeamDeleteArgs(req)
	if err != nil {
		return err
	}
	return delegateSquad(req.ProjectDir, args...)
}

// consoleTeamDeleteArgs mirrors the previous in-process team removal as an
// installed `amq-squad team rm` invocation. --all-profiles cannot be combined
// with a positional/--profile selector, and the default profile is implied when
// no --profile is passed.
func consoleTeamDeleteArgs(req console.TeamDeleteRequest) ([]string, error) {
	args := []string{"team", "rm"}
	if dir := strings.TrimSpace(req.ProjectDir); dir != "" {
		args = append(args, "--project", dir)
	}
	if req.AllProfiles {
		args = append(args, "--all-profiles")
	} else {
		profile, err := resolveProfileFlag(req.Profile)
		if err != nil {
			return nil, err
		}
		if profile != team.DefaultProfile {
			args = append(args, "--profile", profile)
		}
	}
	return append(args, "--yes"), nil
}

func consolePointerSync(req console.PointerSyncRequest) error {
	return delegateSquad(req.ProjectDir, consolePointerSyncArgs(req)...)
}

func consolePointerSyncArgs(req console.PointerSyncRequest) []string {
	args := []string{"team", "sync"}
	if dir := strings.TrimSpace(req.ProjectDir); dir != "" {
		args = append(args, "--project", dir)
	}
	if profile := strings.TrimSpace(req.Profile); profile != "" && profile != team.DefaultProfile {
		args = append(args, "--profile", profile)
	}
	if req.AllowOutside {
		args = append(args, "--allow-outside")
	}
	return append(args, "--apply")
}

func consoleReadNeedsYou(req console.ReadNeedsYouRequest) (console.ReadNeedsYouResult, error) {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return console.ReadNeedsYouResult{}, fmt.Errorf("root cannot be empty")
	}
	messageID := strings.TrimSpace(req.MessageID)
	if messageID == "" {
		return console.ReadNeedsYouResult{}, fmt.Errorf("message id cannot be empty")
	}
	cmd := exec.Command("amq", "read",
		"--root", root,
		"--me", firstNonEmpty(req.OperatorHandle, state.DefaultOperatorHandle),
		"--id", messageID,
		"--json")
	cmd.Env = envWithoutAMQIdentity(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return console.ReadNeedsYouResult{}, fmt.Errorf("%w: %s", err, detail)
		}
		return console.ReadNeedsYouResult{}, err
	}
	var parsed struct {
		Header struct {
			ID      string `json:"id"`
			Thread  string `json:"thread"`
			Subject string `json:"subject"`
		} `json:"header"`
		Body string `json:"body"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return console.ReadNeedsYouResult{}, fmt.Errorf("parse amq read output: %w", err)
	}
	result := console.ReadNeedsYouResult{
		MessageID: firstNonEmpty(parsed.Header.ID, messageID),
		Thread:    firstNonEmpty(parsed.Header.Thread, req.Thread),
		Subject:   firstNonEmpty(parsed.Header.Subject, req.Subject),
		Body:      parsed.Body,
	}
	return result, nil
}

func consoleDrainAgent(req console.DrainAgentRequest) (console.DrainAgentResult, error) {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return console.DrainAgentResult{}, fmt.Errorf("root cannot be empty")
	}
	handle := strings.TrimSpace(req.Handle)
	if handle == "" {
		return console.DrainAgentResult{}, fmt.Errorf("agent handle cannot be empty")
	}
	cmd := exec.Command("amq", "drain",
		"--root", root,
		"--me", handle,
		"--include-body")
	cmd.Env = envWithoutAMQIdentity(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return console.DrainAgentResult{}, fmt.Errorf("%w: %s", err, detail)
		}
		return console.DrainAgentResult{}, err
	}
	return console.DrainAgentResult{Handle: handle, Output: string(out)}, nil
}

func consoleInboxAgent(req console.InboxAgentRequest) (console.InboxAgentResult, error) {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return console.InboxAgentResult{}, fmt.Errorf("root cannot be empty")
	}
	handle := strings.TrimSpace(req.Handle)
	if handle == "" {
		return console.InboxAgentResult{}, fmt.Errorf("agent handle cannot be empty")
	}
	cmd := exec.Command("amq", "list",
		"--root", root,
		"--me", handle,
		"--new")
	cmd.Env = envWithoutAMQIdentity(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return console.InboxAgentResult{}, fmt.Errorf("%w: %s", err, detail)
		}
		return console.InboxAgentResult{}, err
	}
	return console.InboxAgentResult{Handle: handle, Output: string(out)}, nil
}

func consoleDLQAgent(req console.DLQAgentRequest) (console.DLQAgentResult, error) {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return console.DLQAgentResult{}, fmt.Errorf("root cannot be empty")
	}
	handle := strings.TrimSpace(req.Handle)
	if handle == "" {
		return console.DLQAgentResult{}, fmt.Errorf("agent handle cannot be empty")
	}
	cmd := exec.Command("amq", "dlq", "list",
		"--root", root,
		"--me", handle)
	cmd.Env = envWithoutAMQIdentity(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return console.DLQAgentResult{}, fmt.Errorf("%w: %s", err, detail)
		}
		return console.DLQAgentResult{}, err
	}
	return console.DLQAgentResult{Handle: handle, Output: string(out)}, nil
}

func consoleDLQRead(req console.DLQReadRequest) (console.DLQReadResult, error) {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return console.DLQReadResult{}, fmt.Errorf("root cannot be empty")
	}
	handle := strings.TrimSpace(req.Handle)
	if handle == "" {
		return console.DLQReadResult{}, fmt.Errorf("agent handle cannot be empty")
	}
	id := strings.TrimSpace(req.ID)
	if id == "" {
		return console.DLQReadResult{}, fmt.Errorf("DLQ id cannot be empty")
	}
	args, err := consoleDLQReadArgs(req)
	if err != nil {
		return console.DLQReadResult{}, err
	}
	cmd := exec.Command("amq", args...)
	cmd.Env = envWithoutAMQIdentity(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return console.DLQReadResult{}, fmt.Errorf("%w: %s", err, detail)
		}
		return console.DLQReadResult{}, err
	}
	return console.DLQReadResult{Handle: handle, ID: id, Output: string(out)}, nil
}

func consoleDLQReadArgs(req console.DLQReadRequest) ([]string, error) {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return nil, fmt.Errorf("root cannot be empty")
	}
	handle := strings.TrimSpace(req.Handle)
	if handle == "" {
		return nil, fmt.Errorf("agent handle cannot be empty")
	}
	id := strings.TrimSpace(req.ID)
	if id == "" {
		return nil, fmt.Errorf("DLQ id cannot be empty")
	}
	return []string{"dlq", "read", "--root", root, "--me", handle, "--id", id}, nil
}

func consoleDLQRetry(req console.DLQRetryRequest) (console.DLQRetryResult, error) {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return console.DLQRetryResult{}, fmt.Errorf("root cannot be empty")
	}
	handle := strings.TrimSpace(req.Handle)
	if handle == "" {
		return console.DLQRetryResult{}, fmt.Errorf("agent handle cannot be empty")
	}
	id := strings.TrimSpace(req.ID)
	if id == "" {
		return console.DLQRetryResult{}, fmt.Errorf("DLQ id cannot be empty")
	}
	args, err := consoleDLQRetryArgs(req)
	if err != nil {
		return console.DLQRetryResult{}, err
	}
	cmd := exec.Command("amq", args...)
	cmd.Env = envWithoutAMQIdentity(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return console.DLQRetryResult{}, fmt.Errorf("%w: %s", err, detail)
		}
		return console.DLQRetryResult{}, err
	}
	return console.DLQRetryResult{Handle: handle, ID: id, Output: string(out)}, nil
}

func consoleDLQRetryArgs(req console.DLQRetryRequest) ([]string, error) {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return nil, fmt.Errorf("root cannot be empty")
	}
	handle := strings.TrimSpace(req.Handle)
	if handle == "" {
		return nil, fmt.Errorf("agent handle cannot be empty")
	}
	id := strings.TrimSpace(req.ID)
	if id == "" {
		return nil, fmt.Errorf("DLQ id cannot be empty")
	}
	return []string{"dlq", "retry", "--root", root, "--me", handle, "--id", id}, nil
}

func consoleDLQPurge(req console.DLQPurgeRequest) (console.DLQPurgeResult, error) {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return console.DLQPurgeResult{}, fmt.Errorf("root cannot be empty")
	}
	handle := strings.TrimSpace(req.Handle)
	if handle == "" {
		return console.DLQPurgeResult{}, fmt.Errorf("agent handle cannot be empty")
	}
	olderThan := strings.TrimSpace(req.OlderThan)
	if olderThan == "" {
		return console.DLQPurgeResult{}, fmt.Errorf("DLQ purge age cannot be empty")
	}
	args, err := consoleDLQPurgeArgs(req)
	if err != nil {
		return console.DLQPurgeResult{}, err
	}
	cmd := exec.Command("amq", args...)
	cmd.Env = envWithoutAMQIdentity(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return console.DLQPurgeResult{}, fmt.Errorf("%w: %s", err, detail)
		}
		return console.DLQPurgeResult{}, err
	}
	return console.DLQPurgeResult{Handle: handle, OlderThan: olderThan, Output: string(out)}, nil
}

func consoleDLQPurgeArgs(req console.DLQPurgeRequest) ([]string, error) {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return nil, fmt.Errorf("root cannot be empty")
	}
	handle := strings.TrimSpace(req.Handle)
	if handle == "" {
		return nil, fmt.Errorf("agent handle cannot be empty")
	}
	olderThan := strings.TrimSpace(req.OlderThan)
	if olderThan == "" {
		return nil, fmt.Errorf("DLQ purge age cannot be empty")
	}
	dur, err := time.ParseDuration(olderThan)
	if err != nil || dur <= 0 {
		return nil, fmt.Errorf("invalid DLQ purge age %s; use a positive duration like 24h or 168h", olderThan)
	}
	return []string{"dlq", "purge", "--root", root, "--me", handle, "--older-than", olderThan, "--yes"}, nil
}

func consoleDLQRetryAll(req console.DLQRetryAllRequest) (console.DLQRetryAllResult, error) {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return console.DLQRetryAllResult{}, fmt.Errorf("root cannot be empty")
	}
	handle := strings.TrimSpace(req.Handle)
	if handle == "" {
		return console.DLQRetryAllResult{}, fmt.Errorf("agent handle cannot be empty")
	}
	args, err := consoleDLQRetryAllArgs(req)
	if err != nil {
		return console.DLQRetryAllResult{}, err
	}
	cmd := exec.Command("amq", args...)
	cmd.Env = envWithoutAMQIdentity(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return console.DLQRetryAllResult{}, fmt.Errorf("%w: %s", err, detail)
		}
		return console.DLQRetryAllResult{}, err
	}
	return console.DLQRetryAllResult{Handle: handle, Output: string(out)}, nil
}

func consoleDLQRetryAllArgs(req console.DLQRetryAllRequest) ([]string, error) {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return nil, fmt.Errorf("root cannot be empty")
	}
	handle := strings.TrimSpace(req.Handle)
	if handle == "" {
		return nil, fmt.Errorf("agent handle cannot be empty")
	}
	return []string{"dlq", "retry", "--root", root, "--me", handle, "--all"}, nil
}

func consoleReceiptsAgent(req console.ReceiptsAgentRequest) (console.ReceiptsAgentResult, error) {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return console.ReceiptsAgentResult{}, fmt.Errorf("root cannot be empty")
	}
	handle := strings.TrimSpace(req.Handle)
	if handle == "" {
		return console.ReceiptsAgentResult{}, fmt.Errorf("agent handle cannot be empty")
	}
	cmd := exec.Command("amq", "receipts", "list",
		"--root", root,
		"--me", handle)
	cmd.Env = envWithoutAMQIdentity(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return console.ReceiptsAgentResult{}, fmt.Errorf("%w: %s", err, detail)
		}
		return console.ReceiptsAgentResult{}, err
	}
	return console.ReceiptsAgentResult{Handle: handle, Output: string(out)}, nil
}

func consoleReceiptsWait(req console.ReceiptsWaitRequest) (console.ReceiptsWaitResult, error) {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return console.ReceiptsWaitResult{}, fmt.Errorf("root cannot be empty")
	}
	handle := strings.TrimSpace(req.Handle)
	if handle == "" {
		return console.ReceiptsWaitResult{}, fmt.Errorf("agent handle cannot be empty")
	}
	msgID := strings.TrimSpace(req.MsgID)
	if msgID == "" {
		return console.ReceiptsWaitResult{}, fmt.Errorf("message ID cannot be empty")
	}
	stage := strings.TrimSpace(req.Stage)
	if stage == "" {
		return console.ReceiptsWaitResult{}, fmt.Errorf("stage cannot be empty")
	}
	timeout := strings.TrimSpace(req.Timeout)
	if timeout == "" {
		return console.ReceiptsWaitResult{}, fmt.Errorf("timeout cannot be empty")
	}
	args, err := consoleReceiptsWaitArgs(req)
	if err != nil {
		return console.ReceiptsWaitResult{}, err
	}
	cmd := exec.Command("amq", args...)
	cmd.Env = envWithoutAMQIdentity(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return console.ReceiptsWaitResult{}, fmt.Errorf("%w: %s", err, detail)
		}
		return console.ReceiptsWaitResult{}, err
	}
	return console.ReceiptsWaitResult{Handle: handle, MsgID: msgID, Stage: stage, Timeout: timeout, Output: string(out)}, nil
}

func consoleReceiptsWaitArgs(req console.ReceiptsWaitRequest) ([]string, error) {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return nil, fmt.Errorf("root cannot be empty")
	}
	handle := strings.TrimSpace(req.Handle)
	if handle == "" {
		return nil, fmt.Errorf("agent handle cannot be empty")
	}
	msgID := strings.TrimSpace(req.MsgID)
	if msgID == "" {
		return nil, fmt.Errorf("message ID cannot be empty")
	}
	stage := strings.TrimSpace(req.Stage)
	if stage != "drained" && stage != "dlq" {
		return nil, fmt.Errorf("stage must be drained or dlq")
	}
	timeout := strings.TrimSpace(req.Timeout)
	if timeout == "" {
		return nil, fmt.Errorf("timeout cannot be empty")
	}
	dur, err := time.ParseDuration(timeout)
	if err != nil || dur < 0 {
		return nil, fmt.Errorf("invalid receipts wait timeout %s; use a non-negative duration like 60s or 5m", timeout)
	}
	return []string{"receipts", "wait", "--root", root, "--me", handle, "--msg-id", msgID, "--stage", stage, "--timeout", timeout}, nil
}

func consoleMessageWait(req console.MessageWaitRequest) (console.MessageWaitResult, error) {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return console.MessageWaitResult{}, fmt.Errorf("root cannot be empty")
	}
	handle := strings.TrimSpace(req.Handle)
	if handle == "" {
		return console.MessageWaitResult{}, fmt.Errorf("agent handle cannot be empty")
	}
	timeout := strings.TrimSpace(req.Timeout)
	if timeout == "" {
		return console.MessageWaitResult{}, fmt.Errorf("timeout cannot be empty")
	}
	args, err := consoleMessageWaitArgs(req)
	if err != nil {
		return console.MessageWaitResult{}, err
	}
	cmd := exec.Command("amq", args...)
	cmd.Env = envWithoutAMQIdentity(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return console.MessageWaitResult{}, fmt.Errorf("%w: %s", err, detail)
		}
		return console.MessageWaitResult{}, err
	}
	return console.MessageWaitResult{Handle: handle, Timeout: timeout, Output: string(out)}, nil
}

func consoleMessageWaitArgs(req console.MessageWaitRequest) ([]string, error) {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return nil, fmt.Errorf("root cannot be empty")
	}
	handle := strings.TrimSpace(req.Handle)
	if handle == "" {
		return nil, fmt.Errorf("agent handle cannot be empty")
	}
	body := strings.TrimSpace(req.Body)
	if body == "" {
		return nil, fmt.Errorf("body cannot be empty")
	}
	timeout := strings.TrimSpace(req.Timeout)
	if timeout == "" {
		return nil, fmt.Errorf("timeout cannot be empty")
	}
	dur, err := time.ParseDuration(timeout)
	if err != nil || dur < 0 {
		return nil, fmt.Errorf("invalid message wait timeout %s; use a non-negative duration like 60s or 5m", timeout)
	}
	return []string{
		"send",
		"--root", root,
		"--me", firstNonEmpty(req.OperatorHandle, state.DefaultOperatorHandle),
		"--to", handle,
		"--subject", "Message from operator",
		"--body", body,
		"--kind", string(state.KindStatus),
		"--wait-for", "drained",
		"--wait-timeout", timeout,
	}, nil
}

func consoleThreadContext(req console.ThreadContextRequest) (console.ThreadContextResult, error) {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return console.ThreadContextResult{}, fmt.Errorf("root cannot be empty")
	}
	threadID := strings.TrimSpace(req.Thread)
	if threadID == "" {
		return console.ThreadContextResult{}, fmt.Errorf("thread id cannot be empty")
	}
	cmd := exec.Command("amq", "thread",
		"--root", root,
		"--id", threadID,
		"--include-body",
		"--limit", "20")
	cmd.Env = envWithoutAMQIdentity(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return console.ThreadContextResult{}, fmt.Errorf("%w: %s", err, detail)
		}
		return console.ThreadContextResult{}, err
	}
	return console.ThreadContextResult{Thread: threadID, Subject: strings.TrimSpace(req.Subject), Output: string(out)}, nil
}

func consoleAMQOps(req console.AMQOpsRequest) (console.AMQOpsResult, error) {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return console.AMQOpsResult{}, fmt.Errorf("root cannot be empty")
	}
	cmd := exec.Command("amq", "doctor", "--ops")
	cmd.Env = append(envWithoutAMQIdentity(os.Environ()), "AM_ROOT="+root)
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return console.AMQOpsResult{}, fmt.Errorf("%w: %s", err, detail)
		}
		return console.AMQOpsResult{}, err
	}
	return console.AMQOpsResult{Root: root, Output: string(out)}, nil
}

func consoleAMQWho(req console.AMQWhoRequest) (console.AMQWhoResult, error) {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return console.AMQWhoResult{}, fmt.Errorf("root cannot be empty")
	}
	cmd := exec.Command("amq", "who", "--root", root)
	cmd.Env = envWithoutAMQIdentity(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return console.AMQWhoResult{}, fmt.Errorf("%w: %s", err, detail)
		}
		return console.AMQWhoResult{}, err
	}
	return console.AMQWhoResult{Root: root, Output: string(out)}, nil
}

func consoleAMQEnv(req console.AMQEnvRequest) (console.AMQEnvResult, error) {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return console.AMQEnvResult{}, fmt.Errorf("root cannot be empty")
	}
	cmd := exec.Command("amq", "env", "--root", root, "--json")
	cmd.Env = envWithoutAMQIdentity(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return console.AMQEnvResult{}, fmt.Errorf("%w: %s", err, detail)
		}
		return console.AMQEnvResult{}, err
	}
	return console.AMQEnvResult{Root: root, Output: string(out)}, nil
}

func consoleAMQCleanup(req console.AMQCleanupRequest) (console.AMQCleanupResult, error) {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return console.AMQCleanupResult{}, fmt.Errorf("root cannot be empty")
	}
	olderThan := strings.TrimSpace(req.TmpOlderThan)
	if olderThan == "" {
		return console.AMQCleanupResult{}, fmt.Errorf("tmp older-than cannot be empty")
	}
	args, err := consoleAMQCleanupArgs(req)
	if err != nil {
		return console.AMQCleanupResult{}, err
	}
	cmd := exec.Command("amq", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return console.AMQCleanupResult{}, fmt.Errorf("%w: %s", err, detail)
		}
		return console.AMQCleanupResult{}, err
	}
	return console.AMQCleanupResult{Root: root, TmpOlderThan: olderThan, Output: string(out)}, nil
}

func consoleAMQCleanupArgs(req console.AMQCleanupRequest) ([]string, error) {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return nil, fmt.Errorf("root cannot be empty")
	}
	olderThan := strings.TrimSpace(req.TmpOlderThan)
	if olderThan == "" {
		return nil, fmt.Errorf("tmp older-than cannot be empty")
	}
	dur, err := time.ParseDuration(olderThan)
	if err != nil || dur <= 0 {
		return nil, fmt.Errorf("invalid AMQ cleanup tmp age %s; use a positive duration like 36h or 168h", olderThan)
	}
	return []string{"cleanup", "--root", root, "--tmp-older-than", olderThan, "--yes"}, nil
}

func consolePresence(req console.PresenceRequest) (console.PresenceResult, error) {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return console.PresenceResult{}, fmt.Errorf("root cannot be empty")
	}
	cmd := exec.Command("amq", "presence", "list", "--root", root)
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return console.PresenceResult{}, fmt.Errorf("%w: %s", err, detail)
		}
		return console.PresenceResult{}, err
	}
	return console.PresenceResult{Root: root, Output: string(out)}, nil
}

func consoleProjectDoctor(req console.ProjectDoctorRequest) (console.ProjectDoctorResult, error) {
	projectDir := strings.TrimSpace(req.ProjectDir)
	if projectDir == "" {
		return console.ProjectDoctorResult{}, fmt.Errorf("project dir cannot be empty")
	}
	var out bytes.Buffer
	d := defaultDoctorExecution(projectDir)
	d.Out = &out
	d.AllProfiles = true
	if err := executeDoctor(d); err != nil {
		if strings.HasPrefix(err.Error(), "doctor:") {
			fmt.Fprintf(&out, "\n%v\n", err)
		} else {
			return console.ProjectDoctorResult{}, err
		}
	}
	return console.ProjectDoctorResult{ProjectDir: projectDir, Output: out.String()}, nil
}

func consoleProjectHistory(req console.ProjectHistoryRequest) (console.ProjectHistoryResult, error) {
	projectDir := strings.TrimSpace(req.ProjectDir)
	if projectDir == "" {
		return console.ProjectHistoryResult{}, fmt.Errorf("project dir cannot be empty")
	}
	entries := scanHistoryEntries([]string{projectDir})
	sortRestorableEntries(entries)
	var out bytes.Buffer
	if err := writeHistoryTable(&out, historyRecordsFromEntries(entries)); err != nil {
		return console.ProjectHistoryResult{}, err
	}
	return console.ProjectHistoryResult{ProjectDir: projectDir, Output: out.String()}, nil
}

func consoleTeamRules(req console.TeamRulesRequest) (console.TeamRulesResult, error) {
	projectDir := strings.TrimSpace(req.ProjectDir)
	if projectDir == "" {
		return console.TeamRulesResult{}, fmt.Errorf("project dir cannot be empty")
	}
	body, err := rules.Read(projectDir)
	if err != nil {
		if os.IsNotExist(err) {
			return console.TeamRulesResult{}, fmt.Errorf("no team-rules.md at %s. Run 'amq-squad team rules init' first.", rules.Path(projectDir))
		}
		return console.TeamRulesResult{}, fmt.Errorf("read team-rules.md: %w", err)
	}
	return console.TeamRulesResult{ProjectDir: projectDir, Path: rules.Path(projectDir), Content: body}, nil
}

func consoleProjectResumePlan(req console.ProjectResumePlanRequest) (console.ProjectResumePlanResult, error) {
	projectDir := strings.TrimSpace(req.ProjectDir)
	if projectDir == "" {
		return console.ProjectResumePlanResult{}, fmt.Errorf("project dir cannot be empty")
	}
	profile := resolvedNOCProfile(req.Profile)
	var out bytes.Buffer
	err := executeResume(resumeExecution{
		ProjectDir: projectDir,
		Profile:    profile,
		Style:      resumePrinterStyle{Label: "resume", FooterVerb: "up"},
		Out:        &out,
	})
	if err != nil {
		return console.ProjectResumePlanResult{}, err
	}
	return console.ProjectResumePlanResult{ProjectDir: projectDir, Profile: profileForDisplay(profile), Output: out.String()}, nil
}

func consoleForkPlan(req console.ForkPlanRequest) (console.ForkPlanResult, error) {
	projectDir := strings.TrimSpace(req.ProjectDir)
	if projectDir == "" {
		return console.ForkPlanResult{}, fmt.Errorf("project dir cannot be empty")
	}
	from := strings.TrimSpace(req.FromSession)
	if from == "" {
		return console.ForkPlanResult{}, fmt.Errorf("source session cannot be empty")
	}
	to := strings.TrimSpace(req.ToSession)
	if to == "" {
		return console.ForkPlanResult{}, fmt.Errorf("target session cannot be empty")
	}
	if err := validateWorkstreamName(from); err != nil {
		return console.ForkPlanResult{}, fmt.Errorf("invalid source session: %w", err)
	}
	if err := validateWorkstreamName(to); err != nil {
		return console.ForkPlanResult{}, fmt.Errorf("invalid target session: %w", err)
	}
	if from == to {
		return console.ForkPlanResult{}, fmt.Errorf("source and target sessions must differ")
	}
	profile := resolvedNOCProfile(req.Profile)
	t, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return console.ForkPlanResult{}, fmt.Errorf("read team: %w", err)
	}
	if len(t.Members) == 0 {
		return console.ForkPlanResult{}, fmt.Errorf("team has no members")
	}
	if !forkSourceHasState(t, from) {
		return console.ForkPlanResult{}, fmt.Errorf("source session %q has no local workstream state or restorable launch records", from)
	}
	var out bytes.Buffer
	err = executeResume(resumeExecution{
		ProjectDir:       projectDir,
		RequestedSession: to,
		ExplicitSession:  true,
		Mode:             resumeModeFresh,
		Profile:          profile,
		Style: resumePrinterStyle{
			Label:      "fork",
			FooterVerb: "up",
			ForkFrom:   from,
			ForkTo:     to,
		},
		Out: &out,
	})
	if err != nil {
		return console.ForkPlanResult{}, err
	}
	return console.ForkPlanResult{
		ProjectDir:  projectDir,
		Profile:     profileForDisplay(profile),
		FromSession: from,
		ToSession:   to,
		Output:      out.String(),
	}, nil
}

func consoleBrief(req console.BriefRequest) (console.BriefResult, error) {
	projectDir := strings.TrimSpace(req.ProjectDir)
	if projectDir == "" {
		return console.BriefResult{}, fmt.Errorf("project dir cannot be empty")
	}
	session := strings.TrimSpace(req.Session)
	if session == "" {
		return console.BriefResult{}, fmt.Errorf("session cannot be empty")
	}
	data, err := readBriefData(projectDir, session)
	if err != nil {
		return console.BriefResult{}, err
	}
	return console.BriefResult{
		ProjectDir: data.ProjectDir,
		Session:    data.Session,
		Path:       data.Path,
		Kind:       data.Kind,
		Exists:     data.Exists,
		Content:    data.Content,
	}, nil
}

func consoleBriefSeed(req console.BriefSeedRequest) error {
	projectDir := strings.TrimSpace(req.ProjectDir)
	if projectDir == "" {
		return fmt.Errorf("project dir cannot be empty")
	}
	session := strings.TrimSpace(req.Session)
	if session == "" {
		return fmt.Errorf("session cannot be empty")
	}
	_, err := seedBriefData(projectDir, session, req.SeedFrom, req.Force, false)
	return err
}

func consoleStatus(req console.StatusRequest) (console.StatusResult, error) {
	projectDir := strings.TrimSpace(req.ProjectDir)
	if projectDir == "" {
		return console.StatusResult{}, fmt.Errorf("project dir cannot be empty")
	}
	session := strings.TrimSpace(req.Session)
	profile := resolvedNOCProfile(req.Profile)
	var out bytes.Buffer
	if session == "" {
		err := executeStatusBoard(statusBoardExecution{
			ProjectDir: projectDir,
			Probe:      state.DefaultProbe,
			Out:        &out,
		})
		if err != nil {
			return console.StatusResult{}, err
		}
		return console.StatusResult{ProjectDir: projectDir, Output: out.String()}, nil
	}
	err := executeStatus(statusExecution{
		ProjectDir:       projectDir,
		RequestedSession: session,
		ExplicitSession:  true,
		Profile:          profile,
		Probe:            defaultDuplicateLaunchProbe,
		Out:              &out,
	})
	if err != nil {
		return console.StatusResult{}, err
	}
	return console.StatusResult{ProjectDir: projectDir, Session: session, Profile: profileForDisplay(profile), Output: out.String()}, nil
}

func consoleStatusArgs(req console.StatusRequest) ([]string, error) {
	projectDir := strings.TrimSpace(req.ProjectDir)
	if projectDir == "" {
		return nil, fmt.Errorf("project dir cannot be empty")
	}
	args := []string{"status", "--project", projectDir}
	if profile := strings.TrimSpace(req.Profile); profile != "" && profile != team.DefaultProfile {
		args = append(args, "--profile", profile)
	}
	if session := strings.TrimSpace(req.Session); session != "" {
		args = append(args, "--session", session)
	}
	return args, nil
}

func consoleThreads(req console.ThreadsRequest) (console.ThreadsResult, error) {
	projectDir := strings.TrimSpace(req.ProjectDir)
	if projectDir == "" {
		return console.ThreadsResult{}, fmt.Errorf("project dir cannot be empty")
	}
	session := strings.TrimSpace(req.Session)
	if session == "" {
		return console.ThreadsResult{}, fmt.Errorf("session cannot be empty")
	}
	var out bytes.Buffer
	err := executeThreads(threadsExecution{
		ProjectDir: projectDir,
		Session:    session,
		Limit:      defaultThreadsLimit,
		Out:        &out,
	})
	if err != nil {
		return console.ThreadsResult{}, err
	}
	return console.ThreadsResult{ProjectDir: projectDir, Session: session, Output: out.String()}, nil
}

func profileForDisplay(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" || profile == team.DefaultProfile {
		return ""
	}
	return profile
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func consoleNewTeamArgs(req console.NewTeamRequest) ([]string, error) {
	roles := strings.TrimSpace(req.Roles)
	if roles == "" {
		return nil, fmt.Errorf("roles cannot be empty")
	}
	profile := strings.TrimSpace(req.Profile)
	args := []string{"new", "team"}
	if profile != "" && profile != team.DefaultProfile {
		args = []string{"new", "profile", profile}
	}
	if dir := strings.TrimSpace(req.ProjectDir); dir != "" {
		args = append(args, "--project", dir)
	}
	args = append(args, "--roles", roles)
	if binary := strings.TrimSpace(req.Binary); binary != "" {
		args = append(args, "--binary", binary)
	}
	if session := strings.TrimSpace(req.Session); session != "" {
		args = append(args, "--session", session)
	}
	return args, nil
}

func nocTerminalSessionName(projectDir, session string) string {
	base := defaultTmuxSessionName(projectDir)
	if strings.TrimSpace(session) == "" {
		return base
	}
	workstream := sanitizeTmuxSessionName(session)
	baseKey := strings.TrimPrefix(base, "amq-squad-")
	if workstream == "" || workstream == "project" || workstream == baseKey {
		return base
	}
	return base + "-" + workstream
}

// defaultNOCRoots picks the default scan roots for a starting directory:
//
//   - If cwd is itself an amq-squad project (it has a .agent-mail child), scan
//     its PARENT so sibling squads under the same workspace appear too.
//   - Otherwise scan cwd itself.
//
// This matches the brief's "default to the project's parent if a single project,
// else cwd" intent while staying a pure function of cwd (no filesystem walk
// beyond the single stat).
func defaultNOCRoots(cwd string) []string {
	if cwd == "" {
		return nil
	}
	if isAMQProject(cwd) {
		parent := filepath.Dir(cwd)
		if parent != "" && parent != cwd {
			return []string{parent}
		}
	}
	return []string{cwd}
}

// isAMQProject reports whether dir contains a .agent-mail child directory.
func isAMQProject(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, noc.AgentMailDirName))
	return err == nil && info.IsDir()
}
