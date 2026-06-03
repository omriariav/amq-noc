package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/omriariav/amq-noc/internal/state"
	"github.com/omriariav/amq-noc/internal/team"
)

func runUp(args []string) error {
	project, rest, err := peelProjectFlag(args)
	if err != nil {
		return err
	}
	if strings.TrimSpace(project) != "" {
		return runInProject(project, func() error { return runUp(rest) })
	}

	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "print the launch plan (one launch command per member) instead of bringing the team up")
	seedFrom := fs.String("seed-from", "", "seed the active brief from a deterministic source: file:<path>, issue:<n>, or gh:owner/repo#<n>")
	force := fs.Bool("force", false, "overwrite an existing active brief when --seed-from is set; with --reset, also tear down a session that still has live agents")
	reset := fs.Bool("reset", false, "tear down and remove the existing session first, then launch fresh (destructive; confirm-gated unless --yes)")
	yes := fs.Bool("yes", false, "skip the --reset confirmation prompt (for automation)")
	fs.BoolVar(yes, "y", false, "shorthand for --yes")
	profileFlag := fs.String("profile", "", "team profile to bring up (default: default profile)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned JSON envelope (requires --dry-run; the live up path stays human-only in 11A)")
	pf := registerPreviewFlags(fs)
	lf := registerLiveLaunchFlags(fs)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `amq-squad up - bring this project's team up on a NEW workstream

Usage:
  amq-squad up [<session>] [--project DIR] [--profile NAME] [--session workstream]
    [--reset [--yes|-y] [--force]]
    [--terminal tmux] [--target current-window|new-session]
    [--layout vertical|horizontal|tiled]
    [--terminal-session name] [--stagger 750ms] [--no-bootstrap]
    [--trust sandboxed|trusted] [--model role=model,...]
    [--codex-args args] [--claude-args args]
    [--force-duplicate]
    [--seed-from REF [--force]] [--dry-run]

up means NEW work. It REFUSES by default when the target session already
exists (its AMQ root holds mailbox/agent state, or a member has a restorable
launch record). To continue an existing session use 'amq-squad resume'; to
start it over use 'amq-squad up --reset'; or pick a new name.
--project targets another team-home without changing directories.

The session name comes from the <session> positional or --session (passing
both is an error). With neither, it is inferred from team members, the
deprecated team.json pin, or the sanitized team-home directory name.

--reset tears down AND removes the existing session (root + brief) first,
then launches fresh. It is destructive: it previews the footprint and prompts
for confirmation (default: No) unless --yes/-y. A session with LIVE agents is
refused unless --force; --reset never silently stops agents.

Brief: with --seed-from the active brief is authored from the source. With no
source, up AUTO-STUBS the brief and prints a one-line notice so CI and
send-keys flows keep working; edit the stub or pass --seed-from to set the goal.

--seed-from sources (deterministic only in 8A):
  file:<path>            literal file body
  issue:<n>              gh issue view <n> in the current repo
  gh:<owner>/<repo>#<n>  gh issue view <n> --repo owner/repo

With --seed-from --dry-run the candidate brief is printed to stdout and
nothing is written. With --seed-from alone, the brief is written to
.amq-squad/briefs/<session>.md before the backend launches. --force
overwrites an existing brief; --force without --seed-from (and without
--reset) is an error. --force-duplicate remains the separate
duplicate-agent flag.

Supported terminal backends: %s

Examples:
  amq-squad up issue-101
  amq-squad up --project ~/Code/app issue-101
  amq-squad up --reset issue-101 --yes
  amq-squad up --dry-run --no-bootstrap
  amq-squad up --dry-run --seed-from issue:31
`, strings.Join(registeredTeamLaunchTerminals(), ", "))
	}
	args = allowInterspersedFlags(fs, args)
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	// --fresh is reconciled away: refuse-existing is now the default, so the
	// flag is a redundant no-op kept only for back-compat. Emit a one-line
	// hint pointing at the new model and --reset start-over path.
	if flagWasSet(fs, "fresh") {
		quietNotice("notice: --fresh is now the default for 'up' (it refuses an existing session) and is a no-op. " +
			"To start an existing session over, use 'amq-squad up --reset'.\n")
	}

	// Positional session name, consistent with rm/archive. Both a positional
	// and --session is an error; otherwise the positional sets the session.
	positionalSession := ""
	if fs.NArg() > 1 {
		return usageErrorf("up takes at most one session positional; got %d", fs.NArg())
	}
	if fs.NArg() == 1 {
		positionalSession = strings.TrimSpace(fs.Arg(0))
		if flagWasSet(fs, "session") {
			return usageErrorf("pass the session name either positionally or via --session, not both")
		}
		if err := validateWorkstreamName(positionalSession); err != nil {
			return err
		}
	}

	hasBriefSource := *seedFrom != ""
	if *force && !hasBriefSource && !*reset {
		return usageErrorf("--force without --seed-from has no effect; pass --force-duplicate for live-duplicate handling, or --reset to start a session over")
	}
	if *jsonOut && !*dryRun {
		return usageErrorf("--json requires --dry-run on `up`; the live launch path does not have a JSON contract in this release")
	}
	if *reset && *dryRun {
		return usageErrorf("--reset and --dry-run are mutually exclusive; --reset is destructive and --dry-run mutates nothing")
	}
	if *yes && !*reset {
		return usageErrorf("--yes/-y only applies to --reset on `up`")
	}

	profile, err := resolveProfileFlag(*profileFlag)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	if !team.ExistsProfile(cwd, profile) {
		return fmt.Errorf("no team configured for profile %q. Run '%s' first.", profile, profileInitCommand(profile))
	}

	// Resolve --seed-from up front so source-shape failures (bad ref,
	// missing file, gh error) abort cleanly before any team-launch
	// validation. The brief is NOT written here; executeTeamLaunch performs
	// the write after preflight so a later launch-side rejection cannot
	// mutate disk.
	var seedContent string
	var seedTimestamp time.Time
	if *seedFrom != "" {
		body, err := resolveSeed(*seedFrom)
		if err != nil {
			return err
		}
		// Capture the timestamp once so the brief's frontmatter and the
		// JSON envelope's generated_at field are always identical, even
		// across an advancing clock.
		seedTimestamp = seedNow()
		seedContent = buildSeedBrief(*seedFrom, body, seedTimestamp)
	}

	if *dryRun {
		if seedContent != "" {
			if *jsonOut {
				return printJSONEnvelope("brief_candidate", briefCandidateEnvelope(*seedFrom, seedContent, seedTimestamp))
			}
			fmt.Print(seedContent)
			return nil
		}
		opts, err := pf.toEmitOptions(fs)
		if err != nil {
			return err
		}
		if positionalSession != "" {
			opts.RequestedSession = positionalSession
			opts.ExplicitSession = true
		}
		opts.Profile = profile
		opts.JSON = *jsonOut
		return emitTeamCommands(cwd, opts)
	}

	// Live path. Read the team and resolve the final session name HERE so the
	// refuse-existing / --reset gating runs against exactly the name the
	// backend will launch into. executeTeamLaunch re-resolves idempotently
	// from opts.Workstream with explicitSession=true.
	t, err := team.ReadProfile(cwd, profile)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	if len(t.Members) == 0 {
		return fmt.Errorf("team has no members")
	}
	requested := positionalSession
	explicitSession := positionalSession != "" || flagWasSet(fs, "session")
	if positionalSession == "" {
		requested = *pf.session
	}
	workstream, err := resolveTeamWorkstreamName(t, requested, explicitSession)
	if err != nil {
		return err
	}

	exists, root, err := teamWorkstreamExistsOrRestorable(t, workstream)
	if err != nil {
		return err
	}
	if exists {
		if !*reset {
			return existingSessionRefusal(workstream, root)
		}
		// --reset: tear down + remove the existing session first, reusing the
		// PR7 rm teardown (confirm-gated; --force for live; --yes skips). A
		// declined confirm cancels the whole up with zero changes.
		declined, err := resetExistingSession(cwd, workstream, *yes, *force)
		if err != nil {
			return err
		}
		if declined {
			fmt.Fprintf(os.Stdout, "up: reset declined; no changes made.\n")
			return nil
		}
	}

	opts, err := buildLiveLaunchOptions(fs, pf, lf)
	if err != nil {
		return err
	}
	// --fresh is reconciled to a no-op on `up`: refuse-existing is the default
	// gate above, so the old opts.Fresh refusal must never re-fire here.
	opts.Fresh = false
	opts.Workstream = workstream
	opts.SeedBriefContent = seedContent
	opts.SeedBriefForce = *force || *reset
	opts.Profile = profile
	// When no brief source was supplied, the launch path auto-stubs the brief
	// (ensureBriefStub) and we nudge the operator with a warn-if-stub notice
	// AFTER the launch succeeds, so the message reflects a real launch. Carry
	// the intent on opts so executeTeamLaunch can emit it post-launch.
	opts.WarnStubBrief = !hasBriefSource
	return executeTeamLaunch(opts, true, flagWasSet(fs, "trust"))
}

// existingSessionRefusal is the state-aware default refusal for `up` against
// an already-taken session. It names the three next steps: resume, --reset,
// or a new name.
func existingSessionRefusal(workstream, root string) error {
	where := ""
	if root != "" {
		where = " at " + root
	}
	return fmt.Errorf("session %q already exists%s — use 'amq-squad resume' to continue it, "+
		"or 'amq-squad up --reset' to start over, or pick a new name", workstream, where)
}

// resetExistingSession tears down and removes the existing session via the
// PR7 rm teardown. It is destructive, so it inherits rm's full safety
// contract: it previews the footprint, refuses a session with live agents
// unless force, and prompts for confirmation (default No) unless yes. It
// returns declined=true when the operator declined the confirm gate (ZERO
// filesystem changes), so runUp can cancel the launch too.
func resetExistingSession(cwd, session string, yes, force bool) (declined bool, err error) {
	var confirm io.Reader = os.Stdin
	if resetConfirmOverride != nil {
		confirm = resetConfirmOverride
	}
	probe := state.DefaultProbe
	if resetProbeOverride != nil {
		probe = *resetProbeOverride
	}
	return executeRmReportDeclined(rmExecution{
		ProjectDir: cwd,
		Session:    session,
		Mode:       rmModeDelete,
		Yes:        yes,
		Force:      force,
		Probe:      probe,
		Confirm:    confirm,
		Out:        os.Stdout,
	})
}

// resetProbeOverride and resetConfirmOverride let tests drive the --reset
// teardown deterministically: a fixed liveness probe (no ps shell-out) and a
// scripted y/N reader (no real stdin). Production leaves both nil.
var (
	resetProbeOverride   *state.Probe
	resetConfirmOverride io.Reader
)

// profileInitCommand returns the creation command to suggest when reporting a
// missing team profile.
func profileInitCommand(profile string) string {
	if profile == "" || profile == team.DefaultProfile {
		return "amq-squad new team"
	}
	return "amq-squad new profile " + profile
}

// briefCandidate is the kind="brief_candidate" payload emitted by
// `up --dry-run --json --seed-from REF`. It carries the resolved provenance
// fields the would-be brief frontmatter contains plus the rendered brief
// body so callers can inspect or write it themselves.
type briefCandidate struct {
	Source      string `json:"source"`
	GeneratedAt string `json:"generated_at"`
	Generator   string `json:"generator"`
	Content     string `json:"content"`
}

func briefCandidateEnvelope(source, content string, now time.Time) briefCandidate {
	return briefCandidate{
		Source:      source,
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Generator:   "deterministic",
		Content:     content,
	}
}
