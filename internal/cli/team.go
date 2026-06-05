package cli

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/omriariav/amq-noc/internal/catalog"
	"github.com/omriariav/amq-noc/internal/rules"
	"github.com/omriariav/amq-noc/internal/team"
)

func runTeam(args []string) error {
	if len(args) == 0 {
		return runTeamSmart()
	}
	switch args[0] {
	case "-h", "--help":
		printTeamUsage()
		return nil
	case "init":
		return runTeamInit(args[1:])
	case "show":
		// Removed in 2.0. Kept as an explicit hint (not unknown-command) for
		// one release so muscle-memory invocations get a pointer.
		return usageErrorf("'team show' was removed in 2.0; use 'up --dry-run' to preview the launch plan.")
	case "launch":
		// Removed in 2.0. Kept as an explicit hint for one release.
		return usageErrorf("'team launch' was removed in 2.0; use 'up' to bring the team up.")
	case "resume":
		return runTeamResume(args[1:])
	case "rules":
		return runTeamRules(args[1:])
	case "sync":
		return runTeamSync(args[1:])
	case "profiles":
		return runTeamProfiles(args[1:])
	case "rm", "delete":
		return runTeamRemove(args[1:])
	default:
		// Unknown subcommand. Treat as flags to the smart default so
		// `amq-squad team --help` and similar still work.
		return usageErrorf("unknown 'team' subcommand: %q. Try 'init', 'resume', 'rules', 'sync', 'profiles', or 'rm'.", args[0])
	}
}

// runTeamSmart: if team.json exists, print the launch commands. If not,
// run interactive init and then print.
func runTeamSmart() error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	if team.Exists(cwd) {
		return emitTeamCommands(cwd, emitTeamOptions{})
	}
	fmt.Fprintln(os.Stderr, "No team configured for this project yet. Let's set one up.")
	fmt.Fprintln(os.Stderr)
	if err := runTeamInit(nil); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr)
	return emitTeamCommands(cwd, emitTeamOptions{})
}

func runTeamInit(args []string) error {
	return runTeamInitWithOptions(args, teamInitRunOptions{})
}

type teamInitRunOptions struct {
	SyncCommand string
}

func runTeamInitWithOptions(args []string, opts teamInitRunOptions) error {
	fs := flag.NewFlagSet("team init", flag.ContinueOnError)
	personasFlag := fs.String("personas", "", "comma-separated persona IDs to include (alias for --roles)")
	rolesFlag := fs.String("roles", "", "comma-separated role/persona IDs to include (skips interactive prompt)")
	binaryFlag := fs.String("binary", "", "per-persona CLI overrides, e.g. fullstack=codex,qa=claude")
	sessionFlag := fs.String("session", "", "AMQ workstream session name for all members (lowercase a-z, 0-9, -, _)")
	cwdFlag := fs.String("cwd", "", "per-persona working directory overrides, e.g. qa=/path/to/sibling-project")
	modelFlag := fs.String("model", "", "per-persona model overrides, e.g. cto=gpt-5,fullstack=sonnet")
	trustRaw := fs.String("trust", "", "Codex trust profile for generated commands: sandboxed (default) or trusted")
	codexArgsRaw := fs.String("codex-args", "", "extra Codex args for every Codex member, e.g. '--enable goals'")
	claudeArgsRaw := fs.String("claude-args", "", "extra Claude args for every Claude member, e.g. '--chrome'")
	dryRun := fs.Bool("dry-run", false, "preview the team profile and rules paths without writing files")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned team_profile_plan envelope instead of the human dry-run preview")
	force := fs.Bool("force", false, "overwrite an existing team.json")
	_ = fs.String("project", "", "project/team-home directory to initialize (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to initialize (default: default profile)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad team init - set up this project's agent team

Usage:
  amq-squad team init [--project DIR] [--profile NAME] [--personas id1,id2,...|numbers|all] [--binary persona=cli,...] [--session workstream] [--model role=model,...] [--trust sandboxed|trusted] [--codex-args args] [--claude-args args] [--dry-run [--json]] [--force]
  amq-squad team init [--project DIR] [--profile NAME] [--roles id1,id2,...|numbers|all] [--binary role=cli,...] [--session workstream] [--model role=model,...] [--trust sandboxed|trusted] [--codex-args args] [--claude-args args] [--dry-run [--json]] [--force]

Without --personas or --roles, prompts interactively: first choose personas,
then choose the CLI for each persona. Writes the team config under
<cwd>/.amq-squad/: the default profile goes to team.json; --profile NAME
writes to teams/<name>.json instead. Seeds <cwd>/.amq-squad/team-rules.md
if it does not already exist (single source of truth across profiles).
The directory where this runs, or DIR from --project, becomes the team-home.
Members can live in other directories via --cwd role=/path. Relative --cwd
values under --project resolve from DIR. Default AMQ workstream sessions are
derived from the team-home directory name.

Known personas:
`)
		for _, r := range catalog.All() {
			fmt.Fprintf(os.Stderr, "  %-10s %s (default CLI: %s)\n", r.ID, r.Label, r.PreferredBinary)
		}
		fmt.Fprint(os.Stderr, `
Examples:
  amq-squad team init --roles cto,fullstack --binary cto=codex
  amq-squad team init --project ~/Code/app --roles cto,qa
  amq-squad team init --roles 2,9
  amq-squad team init --roles all
  amq-squad team init --roles cto,qa --dry-run
  amq-squad team init --roles cto,qa --dry-run --json
  amq-squad team init --profile review --roles cto --session review
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if flagWasSet(fs, "project") {
		project, rest, err := peelProjectFlag(args)
		if err != nil {
			return err
		}
		return runInProject(project, func() error { return runTeamInitWithOptions(rest, opts) })
	}
	if *jsonOut && !*dryRun {
		return usageErrorf("--json requires --dry-run on `team init`; the live write path does not have a JSON contract")
	}
	profile, err := resolveProfileFlag(*profileFlag)
	if err != nil {
		return err
	}
	trustMode, err := normalizeTrustMode(*trustRaw)
	if err != nil {
		return err
	}
	if *rolesFlag != "" && *personasFlag != "" {
		return fmt.Errorf("use either --personas or --roles, not both")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	profileExists := team.ExistsProfile(cwd, profile)
	if profileExists && !*force && !*dryRun {
		return fmt.Errorf("team config already exists at %s. Use --force to overwrite.", team.ProfilePath(cwd, profile))
	}

	binaryOverrides, err := parseKV(*binaryFlag)
	if err != nil {
		return fmt.Errorf("parse --binary: %w", err)
	}
	if flagWasSet(fs, "session") && strings.ContainsAny(*sessionFlag, "=,") {
		return fmt.Errorf("old per-role --session syntax is no longer supported; pass one shared workstream name, e.g. --session issue-96")
	}
	workstream, err := resolveWorkstreamName(cwd, *sessionFlag, flagWasSet(fs, "session"))
	if err != nil {
		return err
	}
	cwdOverrides, err := parseKV(*cwdFlag)
	if err != nil {
		return fmt.Errorf("parse --cwd: %w", err)
	}
	modelOverrides, err := parseKV(*modelFlag)
	if err != nil {
		return fmt.Errorf("parse --model: %w", err)
	}
	binaryArgs, err := parseBinaryArgFlags(*codexArgsRaw, *claudeArgsRaw)
	if err != nil {
		return err
	}
	// Validate trust + binary-args contradictions up-front so a team config
	// never persists a setting that would fail at launch time.
	if err := validateTrustCombination(trustMode, flagWasSet(fs, "trust"), false, binaryArgs); err != nil {
		return err
	}
	// Normalize override keys to lowercase so we can match them against the
	// chosen persona IDs without surprises.
	modelOverrides = lowercaseKeys(modelOverrides)

	var picked []string
	interactive := *rolesFlag == "" && *personasFlag == ""
	if *personasFlag != "" {
		picked, err = parsePersonaSelection(*personasFlag)
		if err != nil {
			return err
		}
	} else if *rolesFlag != "" {
		picked, err = parsePersonaSelection(*rolesFlag)
		if err != nil {
			return err
		}
	} else {
		reader := bufio.NewReader(os.Stdin)
		picked, err = promptPersonaSelection(reader, os.Stderr)
		if err != nil {
			return err
		}
		if err := promptBinarySelection(reader, os.Stderr, picked, binaryOverrides); err != nil {
			return err
		}
		if err := promptModelSelection(reader, os.Stderr, modelOverrides); err != nil {
			return err
		}
		if !flagWasSet(fs, "trust") {
			chosen, err := promptTrustSelection(reader, os.Stderr, trustMode)
			if err != nil {
				return err
			}
			trustMode = chosen
		}
		if !flagWasSet(fs, "session") {
			chosen, err := promptWorkstreamSelection(reader, os.Stderr, workstream)
			if err != nil {
				return err
			}
			workstream = chosen
		}
	}
	if len(picked) == 0 {
		return fmt.Errorf("no personas selected, aborting")
	}

	// Reject --model role=model where role is not a chosen persona, so a
	// typo never silently drops a model override.
	pickedSet := make(map[string]bool, len(picked))
	for _, id := range picked {
		pickedSet[strings.ToLower(strings.TrimSpace(id))] = true
	}
	if err := validateModelOverrideKeys(modelOverrides, pickedSet); err != nil {
		return err
	}

	members := make([]team.Member, 0, len(picked))
	seen := make(map[string]bool)
	for _, id := range picked {
		id = strings.TrimSpace(strings.ToLower(id))
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		r := catalog.Lookup(id)
		if r == nil {
			return fmt.Errorf("unknown persona %q. Known personas: %s", id, strings.Join(catalog.IDs(), ", "))
		}
		binary := r.PreferredBinary
		if b, ok := binaryOverrides[id]; ok {
			binary = b
		}
		if interactive && binary == "" {
			binary = r.PreferredBinary
		}
		m := team.Member{
			Role:    id,
			Binary:  binary,
			Handle:  id,
			Session: workstream,
		}
		if model, ok := modelOverrides[id]; ok {
			m.Model = strings.TrimSpace(model)
		}
		if c, ok := cwdOverrides[id]; ok {
			abs, err := expandPath(c)
			if err != nil {
				return fmt.Errorf("resolve cwd for %s: %w", id, err)
			}
			if abs != cwd {
				m.CWD = abs
			}
		}
		members = append(members, m)
	}

	t := team.Team{
		Project: cwd,
		// Intentionally do NOT stamp t.Workstream here. The pinned workstream
		// default is a deprecated shim (removal in 2.1); new teams must not pin
		// one. Live session resolution infers a shared member session or falls
		// back to the project basename. The field remains readable for old
		// team.json files. Member sessions still carry the chosen workstream.
		Trust: trustMode,
		Operator: team.OperatorConfig{
			Enabled:  true,
			Handle:   team.DefaultOperatorHandle,
			Runnable: false,
		},
		Capabilities: team.Capabilities{OperatorGates: true},
		BinaryArgs:   binaryArgs,
		Members:      members,
	}
	rulesContent, err := renderTeamRules(t)
	if err != nil {
		return fmt.Errorf("render team-rules.md: %w", err)
	}
	if *dryRun {
		return printTeamInitDryRun(teamInitDryRun{
			TeamHome:    cwd,
			Profile:     profile,
			ProfilePath: team.ProfilePath(cwd, profile),
			RulesPath:   rules.Path(cwd),
			Workstream:  workstream,
			Trust:       trustMode,
			Exists:      profileExists,
			Team:        t,
			JSON:        *jsonOut,
			SyncCommand: opts.SyncCommand,
		})
	}
	if err := team.WriteProfile(cwd, profile, t); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Wrote %s with %d members.\n", team.ProfilePath(cwd, profile), len(members))
	wroteRules, err := rules.Ensure(cwd, rulesContent)
	if err != nil {
		return fmt.Errorf("seed team-rules.md: %w", err)
	}
	if wroteRules {
		fmt.Fprintf(os.Stderr, "Wrote %s.\n", rules.Path(cwd))
	}
	if interactive {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Next:")
		fmt.Fprintln(os.Stderr, "  amq-squad up                   # bring all members up in the current tmux window")
		fmt.Fprintln(os.Stderr, "  amq-squad up --dry-run         # print one launch command per member")
	}
	return nil
}

type teamInitDryRun struct {
	TeamHome    string
	Profile     string
	ProfilePath string
	RulesPath   string
	Workstream  string
	Trust       string
	Exists      bool
	Team        team.Team
	JSON        bool
	SyncCommand string
}

type teamProfilePlanMember struct {
	Role    string `json:"role"`
	Handle  string `json:"handle"`
	Binary  string `json:"binary"`
	Model   string `json:"model,omitempty"`
	CWD     string `json:"cwd"`
	Session string `json:"session"`
}

type teamProfilePlan struct {
	TeamHome        string                  `json:"team_home"`
	Project         string                  `json:"project"`
	Profile         string                  `json:"profile"`
	ProfilePath     string                  `json:"profile_path"`
	RulesPath       string                  `json:"rules_path"`
	Workstream      string                  `json:"workstream"`
	Trust           string                  `json:"trust"`
	ExistingProfile bool                    `json:"existing_profile"`
	Members         int                     `json:"members"`
	BinaryArgs      map[string][]string     `json:"binary_args,omitempty"`
	SyncCommand     string                  `json:"sync_command,omitempty"`
	Plan            []teamProfilePlanMember `json:"plan"`
}

func printTeamInitDryRun(p teamInitDryRun) error {
	if p.JSON {
		return printJSONEnvelope("team_profile_plan", buildTeamProfilePlan(p))
	}
	fmt.Fprintln(os.Stdout, "# amq-squad team init --dry-run")
	fmt.Fprintf(os.Stdout, "# team-home: %s\n", p.TeamHome)
	fmt.Fprintf(os.Stdout, "# profile: %s\n", p.Profile)
	fmt.Fprintf(os.Stdout, "# profile-path: %s\n", p.ProfilePath)
	fmt.Fprintf(os.Stdout, "# team-rules: %s\n", p.RulesPath)
	fmt.Fprintf(os.Stdout, "# workstream: %s\n", p.Workstream)
	fmt.Fprintf(os.Stdout, "# trust: %s\n", p.Trust)
	if p.Exists {
		fmt.Fprintln(os.Stdout, "# existing-profile: yes (live run requires --force to overwrite)")
	} else {
		fmt.Fprintln(os.Stdout, "# existing-profile: no")
	}
	fmt.Fprintln(os.Stdout, "# writes: none")
	fmt.Fprintln(os.Stdout)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "ROLE\tHANDLE\tCLI\tMODEL\tCWD\tSESSION"); err != nil {
		return err
	}
	for _, m := range orderedTeamMembers(p.Team.Members) {
		model := m.Model
		if model == "" {
			model = "-"
		}
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			m.Role,
			m.Handle,
			m.Binary,
			model,
			m.EffectiveCWD(p.Team.Project),
			m.Session,
		); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if p.SyncCommand != "" {
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "# sync preview")
		fmt.Fprintln(os.Stdout, "# would run: "+p.SyncCommand)
	}
	return nil
}

func buildTeamProfilePlan(p teamInitDryRun) teamProfilePlan {
	rows := make([]teamProfilePlanMember, 0, len(p.Team.Members))
	for _, m := range orderedTeamMembers(p.Team.Members) {
		rows = append(rows, teamProfilePlanMember{
			Role:    m.Role,
			Handle:  m.Handle,
			Binary:  m.Binary,
			Model:   m.Model,
			CWD:     m.EffectiveCWD(p.Team.Project),
			Session: m.Session,
		})
	}
	return teamProfilePlan{
		TeamHome:        p.TeamHome,
		Project:         p.Team.Project,
		Profile:         p.Profile,
		ProfilePath:     p.ProfilePath,
		RulesPath:       p.RulesPath,
		Workstream:      p.Workstream,
		Trust:           p.Trust,
		ExistingProfile: p.Exists,
		Members:         len(rows),
		BinaryArgs:      p.Team.BinaryArgs,
		SyncCommand:     p.SyncCommand,
		Plan:            rows,
	}
}

// runTeamShow holds the launch-plan preview body. The `team show` subcommand
// was removed in 2.0 in favor of `up --dry-run`; this body is retained
// internal-only for the tests that exercise the preview/JSON-plan path. No
// user-facing verb dispatches to it (live `up` and `up --dry-run` call
// emitTeamCommands directly).
func runTeamShow(args []string) error {
	fs := flag.NewFlagSet("team show", flag.ContinueOnError)
	pf := registerPreviewFlags(fs)
	jsonOut := fs.Bool("json", false, "emit a schema-versioned team_plan envelope instead of the human preview")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad team show - print the launch commands for this project's team

Usage:
  amq-squad team show [--session name] [--fresh] [--no-bootstrap] [--trust sandboxed|trusted] [--model role=model,...] [--codex-args args] [--claude-args args] [--force-duplicate] [--json]

Examples:
  amq-squad team show
  amq-squad team show --session issue-96 --json
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	opts, err := pf.toEmitOptions(fs)
	if err != nil {
		return err
	}
	opts.JSON = *jsonOut
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	if !team.Exists(cwd) {
		return fmt.Errorf("no team configured. Run 'amq-squad new team' first.")
	}
	return emitTeamCommands(cwd, opts)
}

type emitTeamOptions struct {
	NoBootstrap      bool
	RequestedSession string
	ExplicitSession  bool
	Fresh            bool
	ExtraBinaryArgs  map[string][]string
	RequestedTrust   string
	ExplicitTrust    bool
	ModelOverrides   map[string]string
	ForceDuplicate   bool
	Profile          string
	// JSON requests a structured "team_plan" envelope on stdout instead of
	// the human launch-command preview. Diagnostics still go to stderr.
	JSON bool
}

// teamPlanMember is the per-member entry inside a JSON team plan.
type teamPlanMember struct {
	Role    string `json:"role"`
	Handle  string `json:"handle"`
	Binary  string `json:"binary"`
	Model   string `json:"model,omitempty"`
	CWD     string `json:"cwd"`
	Command string `json:"command"`
}

// teamPlan is the JSON-friendly representation of a launch-plan preview.
// Fields name the inputs callers need to inspect, distribute, or run the
// plan: project/team-home + workstream + trust + profile + binary args +
// per-member command lines.
type teamPlan struct {
	TeamHome   string              `json:"team_home"`
	Project    string              `json:"project"`
	Workstream string              `json:"workstream"`
	Profile    string              `json:"profile"`
	Trust      string              `json:"trust"`
	Members    int                 `json:"members"`
	BinaryArgs map[string][]string `json:"binary_args,omitempty"`
	Plan       []teamPlanMember    `json:"plan"`
}

func emitTeamCommands(projectDir string, opts emitTeamOptions) error {
	t, err := team.ReadProfile(projectDir, opts.Profile)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	if len(t.Members) == 0 {
		return fmt.Errorf("team has no members")
	}
	workstream, err := resolveTeamWorkstreamName(t, opts.RequestedSession, opts.ExplicitSession)
	if err != nil {
		return err
	}
	if opts.Fresh {
		exists, root, err := teamWorkstreamExists(t, workstream)
		if err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("workstream session %q already exists at %s", workstream, root)
		}
	}

	members := orderedTeamMembers(t.Members)
	binaryArgs := mergeBinaryArgs(t.BinaryArgs, opts.ExtraBinaryArgs)
	trustMode, err := resolveTeamTrustMode(t, opts.RequestedTrust, opts.ExplicitTrust)
	if err != nil {
		return err
	}
	// Apply the same trust-vs-binary-args contradiction check direct launch
	// uses, after effective trust + merged binary args are known. A team
	// config that combines sandboxed trust with bypass smuggled into
	// --codex-args is rejected here rather than emitted as a launch command
	// that would self-reject inside runLaunch.
	if err := validateTrustCombination(trustMode, opts.ExplicitTrust || strings.TrimSpace(t.Trust) != "", false, binaryArgs); err != nil {
		return err
	}
	// Reject --model role=model where role is not on the team, so a typo on
	// team show / team launch never silently drops the override.
	memberRoles := make(map[string]bool, len(members))
	for _, m := range members {
		memberRoles[strings.ToLower(m.Role)] = true
	}
	if err := validateModelOverrideKeys(opts.ModelOverrides, memberRoles); err != nil {
		return err
	}

	// Use the running amq-squad's absolute path so emitted commands work
	// even when amq-squad isn't on PATH.
	squadBin := "amq-squad"
	if p, err := os.Executable(); err == nil {
		squadBin = p
	}

	if opts.JSON {
		// Canonicalize the profile field so both `team show --json` (no
		// --profile flag) and `up --dry-run --json` (resolved to "default"
		// when unset) emit the same value.
		profileName := opts.Profile
		if profileName == "" {
			profileName = team.DefaultProfile
		}
		plan := teamPlan{
			TeamHome:   t.Project,
			Project:    t.Project,
			Workstream: workstream,
			Profile:    profileName,
			Trust:      trustMode,
			Members:    len(members),
			BinaryArgs: binaryArgs,
			Plan:       make([]teamPlanMember, 0, len(members)),
		}
		for _, m := range members {
			effectiveModel := memberEffectiveModel(m, opts.ModelOverrides)
			cwd := m.EffectiveCWD(t.Project)
			plan.Plan = append(plan.Plan, teamPlanMember{
				Role:   m.Role,
				Handle: m.Handle,
				Binary: m.Binary,
				Model:  effectiveModel,
				CWD:    cwd,
				Command: emitTeamCommand(emitTeamCommandInput{
					CWD:            cwd,
					SquadBin:       squadBin,
					TeamHome:       t.Project,
					Member:         m,
					NoBootstrap:    opts.NoBootstrap,
					Workstream:     workstream,
					BinaryArgs:     binaryArgs,
					TrustMode:      trustMode,
					Model:          effectiveModel,
					ForceDuplicate: opts.ForceDuplicate,
					Profile:        opts.Profile,
				}),
			})
		}
		return printJSONEnvelope("team_plan", plan)
	}

	fmt.Println("# amq-squad team - run each command in its own terminal tab")
	fmt.Println("#")
	fmt.Printf("# team-home: %s\n", t.Project)
	fmt.Printf("# workstream: %s\n", workstream)
	fmt.Printf("# trust:     %s\n", trustMode)
	fmt.Printf("# members:   %d\n", len(members))
	if formatted := formatBinaryArgs(binaryArgs); formatted != "" {
		fmt.Printf("# binary args: %s\n", formatted)
	}
	// List unique member cwds so a multi-project team is obvious at a glance.
	uniqueCWDs := uniqueMemberCWDs(t.Project, members)
	if len(uniqueCWDs) > 1 {
		fmt.Printf("# cwds:      %s\n", strings.Join(uniqueCWDs, ", "))
	}
	rulesPath := rules.Path(t.Project)
	if _, err := os.Stat(rulesPath); err == nil {
		fmt.Printf("# rules:     %s\n", rulesPath)
	} else {
		fmt.Printf("# rules:     (not set; run 'amq-squad team rules init')\n")
	}
	fmt.Println()

	for i, m := range members {
		label := m.Role
		if r := catalog.Lookup(m.Role); r != nil {
			label = r.Label
		}
		effectiveModel := memberEffectiveModel(m, opts.ModelOverrides)
		cwd := m.EffectiveCWD(t.Project)
		modelLabel := effectiveModel
		if modelLabel == "" {
			modelLabel = "(default)"
		}
		fmt.Printf("# %d. %s - %s (workstream: %s, model: %s, cwd: %s)\n", i+1, label, m.Binary, workstream, modelLabel, cwd)
		fmt.Println(emitTeamCommand(emitTeamCommandInput{
			CWD:            cwd,
			SquadBin:       squadBin,
			TeamHome:       t.Project,
			Member:         m,
			NoBootstrap:    opts.NoBootstrap,
			Workstream:     workstream,
			BinaryArgs:     binaryArgs,
			TrustMode:      trustMode,
			Model:          effectiveModel,
			Profile:        opts.Profile,
			ForceDuplicate: opts.ForceDuplicate,
		}))
		fmt.Println()
	}
	return nil
}

func resolveTeamTrustMode(t team.Team, requested string, explicit bool) (string, error) {
	if explicit {
		return normalizeTrustMode(requested)
	}
	if strings.TrimSpace(t.Trust) != "" {
		return normalizeTrustMode(t.Trust)
	}
	return trustModeSandboxed, nil
}

func memberEffectiveModel(m team.Member, overrides map[string]string) string {
	if v, ok := overrides[strings.ToLower(m.Role)]; ok {
		return strings.TrimSpace(v)
	}
	return strings.TrimSpace(m.Model)
}

// validateModelOverrideKeys rejects --model role=model entries whose role is
// not one of the known roles. Silent drops on typos are a DX trap; an error
// makes the mistake visible.
func validateModelOverrideKeys(overrides map[string]string, known map[string]bool) error {
	if len(overrides) == 0 {
		return nil
	}
	var unknown []string
	for k := range overrides {
		if !known[strings.ToLower(strings.TrimSpace(k))] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("--model has unknown role(s): %s", strings.Join(unknown, ", "))
}

func lowercaseKeys(m map[string]string) map[string]string {
	if len(m) == 0 {
		return m
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[strings.ToLower(strings.TrimSpace(k))] = v
	}
	return out
}

func orderedTeamMembers(members []team.Member) []team.Member {
	idx := make(map[string]int, len(catalog.IDs()))
	for i, id := range catalog.IDs() {
		idx[id] = i
	}
	out := append([]team.Member(nil), members...)
	sort.SliceStable(out, func(i, j int) bool {
		left, lok := idx[out[i].Role]
		right, rok := idx[out[j].Role]
		if !lok && !rok {
			return out[i].Role < out[j].Role
		}
		if !lok {
			return false
		}
		if !rok {
			return true
		}
		return left < right
	})
	return out
}

func uniqueMemberCWDs(projectDir string, members []team.Member) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, m := range members {
		cwd := m.EffectiveCWD(projectDir)
		if seen[cwd] {
			continue
		}
		seen[cwd] = true
		out = append(out, cwd)
	}
	sort.Strings(out)
	return out
}

type emitTeamCommandInput struct {
	CWD            string
	SquadBin       string
	TeamHome       string
	Member         team.Member
	NoBootstrap    bool
	Workstream     string
	BinaryArgs     map[string][]string
	TrustMode      string
	Model          string
	ForceDuplicate bool
	Profile        string
}

func emitTeamCommand(in emitTeamCommandInput) string {
	m := in.Member
	var b strings.Builder
	b.WriteString("cd ")
	b.WriteString(shellQuote(in.CWD))
	b.WriteString(" && ")
	b.WriteString(shellQuote(in.SquadBin))
	// Emit the modern single-agent surface: `agent up <binary> [flags] [-- child]`.
	// Legacy `launch <binary>` still works with a deprecation warning, but
	// generated team commands recommend the 1.0 shape.
	b.WriteString(" agent up ")
	b.WriteString(shellQuote(m.Binary))
	b.WriteString(" --role ")
	b.WriteString(shellQuote(m.Role))
	b.WriteString(" --session ")
	b.WriteString(shellQuote(in.Workstream))
	b.WriteString(" --team-workstream")
	if in.TrustMode != "" {
		b.WriteString(" --trust ")
		b.WriteString(shellQuote(in.TrustMode))
	}
	if in.Model != "" {
		b.WriteString(" --model ")
		b.WriteString(shellQuote(in.Model))
	}
	if in.TeamHome != "" {
		b.WriteString(" --team-home ")
		b.WriteString(shellQuote(in.TeamHome))
	}
	if in.Profile != "" && in.Profile != team.DefaultProfile {
		b.WriteString(" --team-profile ")
		b.WriteString(shellQuote(in.Profile))
	}
	if in.NoBootstrap {
		b.WriteString(" --no-bootstrap")
	}
	if in.ForceDuplicate {
		b.WriteString(" --force-duplicate")
	}
	if m.Handle != "" {
		// Always explicit: a role-named handle avoids collisions when the
		// same binary (e.g. codex) hosts multiple roles in one project.
		b.WriteString(" --me ")
		b.WriteString(shellQuote(m.Handle))
	}
	if m.Launcher != "" {
		b.WriteString(" --launcher ")
		b.WriteString(shellQuote(m.Launcher))
		if len(m.LauncherArgs) > 0 {
			b.WriteString(" --launcher-args=")
			b.WriteString(shellQuote(joinedAgentArgs(m.LauncherArgs)))
		}
	}
	extraDefaultArgs := binaryArgsFor(m.Binary, in.BinaryArgs)
	if len(extraDefaultArgs) > 0 {
		switch normalizedAgentBinary(m.Binary) {
		case "codex":
			b.WriteString(" --codex-args=")
			b.WriteString(shellQuote(joinedAgentArgs(extraDefaultArgs)))
		case "claude":
			b.WriteString(" --claude-args=")
			b.WriteString(shellQuote(joinedAgentArgs(extraDefaultArgs)))
		}
	}
	modelArgs := modelArgsForBinary(m.Binary, in.Model)
	if defaultArgs := launchDefaultChildArgsWithTrust(m.Binary, true, modelArgs, extraDefaultArgs, in.TrustMode); len(defaultArgs) > 0 {
		b.WriteString(" --")
		for _, arg := range defaultArgs {
			b.WriteString(" ")
			b.WriteString(shellQuote(arg))
		}
	}
	return b.String()
}

func promptPersonaSelection(reader *bufio.Reader, out io.Writer) ([]string, error) {
	fmt.Fprintln(out, "Squad market")
	printPersonaMarket(out)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Choose personas to hire:")
	fmt.Fprintln(out, "  names or numbers, comma-separated")
	fmt.Fprintln(out, "  examples: cto,junior-dev,qa | 2,8,9 | all")
	fmt.Fprintln(out)
	fmt.Fprint(out, "> ")

	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read selection: %w", err)
	}
	return parsePersonaSelection(line)
}

// promptModelSelection asks the user for per-role model overrides. Empty
// input keeps current overrides untouched. Existing values from --model are
// preserved and may be augmented.
func promptModelSelection(reader *bufio.Reader, out io.Writer, overrides map[string]string) error {
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Model overrides?")
	fmt.Fprintln(out, "  Press Enter to keep binary defaults.")
	fmt.Fprintln(out, "  Example: cto=gpt-5,fullstack=sonnet")
	fmt.Fprintln(out)
	fmt.Fprint(out, "> ")
	line, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read model overrides: %w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	parsed, err := parseKV(line)
	if err != nil {
		return fmt.Errorf("parse model overrides: %w", err)
	}
	for k, v := range parsed {
		overrides[strings.ToLower(k)] = strings.TrimSpace(v)
	}
	return nil
}

// promptWorkstreamSelection asks for the AMQ workstream session name with
// the project default offered as Enter-to-accept. The chosen value is
// validated to AMQ session naming rules.
func promptWorkstreamSelection(reader *bufio.Reader, out io.Writer, defaultName string) (string, error) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Workstream / AMQ session?")
	fmt.Fprintln(out, "  lowercase a-z, 0-9, -, _ only.")
	fmt.Fprintf(out, "  Press Enter to use %q.\n", defaultName)
	fmt.Fprintln(out)
	fmt.Fprint(out, "> ")
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read workstream: %w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultName, nil
	}
	if err := team.ValidateSessionName(line); err != nil {
		return "", err
	}
	return line, nil
}

// promptTrustSelection asks for the Codex trust profile. Default is the
// current trust mode (sandboxed). Returns the selected normalized trust mode.
func promptTrustSelection(reader *bufio.Reader, out io.Writer, current string) (string, error) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Codex trust profile?")
	fmt.Fprintln(out, "  sandboxed (recommended) - Codex prompts for approvals/sandbox")
	fmt.Fprintln(out, "  trusted   (local power) - prepends --dangerously-bypass-approvals-and-sandbox")
	fmt.Fprintf(out, "  Press Enter for %s.\n", current)
	fmt.Fprintln(out)
	fmt.Fprint(out, "> ")
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read trust profile: %w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return current, nil
	}
	mode, err := normalizeTrustMode(strings.ToLower(line))
	if err != nil {
		return "", err
	}
	return mode, nil
}

func promptBinarySelection(reader *bufio.Reader, out io.Writer, personas []string, overrides map[string]string) error {
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Squad plan")
	printSquadPlan(out, personas, overrides)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "CLI overrides?")
	fmt.Fprintln(out, "  Press Enter to keep defaults.")
	fmt.Fprintln(out, "  Example: fullstack=codex,junior-dev=claude")
	fmt.Fprintln(out)
	fmt.Fprint(out, "> ")
	line, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read CLI overrides: %w", err)
	}
	overrideLine := strings.TrimSpace(line)
	if overrideLine == "" {
		return nil
	}
	parsed, err := parseKV(overrideLine)
	if err != nil {
		return fmt.Errorf("parse CLI overrides: %w", err)
	}
	for k, v := range parsed {
		overrides[strings.ToLower(k)] = v
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Updated squad plan")
	printSquadPlan(out, personas, overrides)
	return nil
}

func printPersonaMarket(out io.Writer) {
	fmt.Fprintf(out, "  %-2s %-12s %-31s %-7s %s\n", "#", "PERSONA", "PROFILE", "CLI", "FIT")
	for i, r := range catalog.All() {
		fmt.Fprintf(out, "  %-2d %-12s %-31s %-7s %s\n", i+1, r.ID, r.Label, r.PreferredBinary, r.Profile)
	}
}

func parsePersonaSelection(line string) ([]string, error) {
	return catalog.ResolveSelection(line)
}

func printSquadPlan(out io.Writer, personas []string, overrides map[string]string) {
	fmt.Fprintf(out, "  %-12s %-31s %-7s %s\n", "PERSONA", "PROFILE", "CLI", "SESSION")
	for _, raw := range personas {
		id := strings.TrimSpace(strings.ToLower(raw))
		if id == "" {
			continue
		}
		r := catalog.Lookup(id)
		if r == nil {
			fmt.Fprintf(out, "  %-12s %-31s %-7s %s\n", id, "(unknown)", "", id)
			continue
		}
		binary := r.PreferredBinary
		if b, ok := overrides[id]; ok {
			binary = b
		}
		fmt.Fprintf(out, "  %-12s %-31s %-7s %s\n", id, r.Label, binary, id)
	}
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// expandPath resolves a user-supplied path: expands a leading "~" or "~/"
// to the user's home directory, then makes the result absolute.
func expandPath(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("home dir: %w", err)
		}
		if p == "~" {
			p = home
		} else {
			p = filepath.Join(home, p[2:])
		}
	}
	return filepath.Abs(p)
}

func parseKV(s string) (map[string]string, error) {
	out := map[string]string{}
	if s == "" {
		return out, nil
	}
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		eq := strings.IndexByte(pair, '=')
		if eq <= 0 || eq == len(pair)-1 {
			return nil, fmt.Errorf("expected key=value, got %q", pair)
		}
		k := strings.TrimSpace(pair[:eq])
		v := strings.TrimSpace(pair[eq+1:])
		out[k] = v
	}
	return out, nil
}

func runTeamRules(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad team rules - manage .amq-squad/team-rules.md

Usage:
  amq-squad team rules show [--project DIR]
                                       Print team-rules.md
  amq-squad team rules init [--project DIR] [--force]
                                       Seed or refresh team-rules.md

Examples:
  amq-squad team rules show
  amq-squad team rules show --project ~/Code/app
  amq-squad team rules init
  amq-squad team rules init --project ~/Code/app
  amq-squad team rules init --force
`)
		if len(args) == 0 {
			return usageErrorf("rules requires a subcommand (e.g. 'show' or 'init')")
		}
		return nil
	}
	switch args[0] {
	case "show":
		fs := flag.NewFlagSet("team rules show", flag.ContinueOnError)
		projectFlag := fs.String("project", "", "project/team-home directory to inspect (default: cwd)")
		fs.Usage = func() {
			fmt.Fprint(os.Stderr, `amq-squad team rules show - print .amq-squad/team-rules.md

Usage:
  amq-squad team rules show [--project DIR]

--project targets another team-home without changing directories.

Examples:
  amq-squad team rules show
  amq-squad team rules show --project ~/Code/app
`)
		}
		if err := parseFlags(fs, args[1:]); err != nil {
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
		body, err := rules.Read(projectDir)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("no team-rules.md at %s. Run 'amq-squad team rules init' first.", rules.Path(projectDir))
			}
			return fmt.Errorf("read team-rules.md: %w", err)
		}
		if _, err := fmt.Fprint(os.Stdout, body); err != nil {
			return err
		}
		if !strings.HasSuffix(body, "\n") {
			_, err = fmt.Fprintln(os.Stdout)
			return err
		}
		return nil
	case "init":
		fs := flag.NewFlagSet("team rules init", flag.ContinueOnError)
		force := fs.Bool("force", false, "overwrite an existing team-rules.md with the generated template")
		projectFlag := fs.String("project", "", "project/team-home directory to update (default: cwd)")
		fs.Usage = func() {
			fmt.Fprint(os.Stderr, `amq-squad team rules init - seed or refresh .amq-squad/team-rules.md

Usage:
  amq-squad team rules init [--project DIR] [--force]

--project targets another team-home without changing directories.

Examples:
  amq-squad team rules init
  amq-squad team rules init --project ~/Code/app
  amq-squad team rules init --force
`)
		}
		if err := parseFlags(fs, args[1:]); err != nil {
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
		content := rules.StubContent
		if t, err := team.Read(projectDir); err == nil {
			rendered, err := renderTeamRules(t)
			if err != nil {
				return fmt.Errorf("render team-rules.md: %w", err)
			}
			content = rendered
		}
		if *force {
			if err := rules.Write(projectDir, content); err != nil {
				return fmt.Errorf("write team-rules.md: %w", err)
			}
			quietNotice("Wrote %s\n", rules.Path(projectDir))
			return nil
		}
		wrote, err := rules.Ensure(projectDir, content)
		if err != nil {
			return fmt.Errorf("seed team-rules.md: %w", err)
		}
		if wrote {
			quietNotice("Wrote %s\n", rules.Path(projectDir))
		} else {
			quietNotice("%s already exists, leaving it alone.\n", rules.Path(projectDir))
		}
		return nil
	default:
		return usageErrorf("unknown 'rules' subcommand: %q", args[0])
	}
}

func runTeamSync(args []string) error {
	fs := flag.NewFlagSet("team sync", flag.ContinueOnError)
	apply := fs.Bool("apply", false, "write the planned changes (default: preview only)")
	allowOutside := fs.Bool("allow-outside", false, "allow sync writes outside the team-home directory")
	projectFlag := fs.String("project", "", "project/team-home directory to sync (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile whose member cwds to sync (default: default profile)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad team sync - sync CLAUDE.md and AGENTS.md from team-rules.md

Usage:
  amq-squad team sync [--project DIR]       Preview what would change (exit 1 if drift)
  amq-squad team sync --apply               Write the managed block into both files
  amq-squad team sync --profile NAME ...    Sync the named profile's member cwds only
  amq-squad team sync --apply --allow-outside
                                            Also write configured member cwds outside team-home

Existing content in CLAUDE.md / AGENTS.md is preserved. On first run,
existing content is adopted as the user region and a managed block is
appended between markers. Subsequent runs only refresh the managed block.

When team members span multiple directories, sync walks every unique cwd
in team.json and syncs CLAUDE.md + AGENTS.md in each. Use --allow-outside
when a member cwd is outside the team-home directory.

Examples:
  amq-squad team sync
  amq-squad team sync --project ~/Code/app --apply
  amq-squad team sync --apply
  amq-squad team sync --profile review --apply
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
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

	body, err := rules.Read(projectDir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no team-rules.md at %s. Run 'amq-squad team rules init' first.", rules.Path(projectDir))
		}
		return err
	}

	// Walk every unique cwd the selected profile spans so each project's
	// CLAUDE.md and AGENTS.md picks up the managed block. Sync is profile-
	// scoped: --profile NAME walks that profile only; no flag walks the
	// default profile only. There is no all-profiles sync. An explicit
	// non-default --profile that does not resolve to an existing config is
	// a hard error (matching up/status/down/resume/fork); a plain
	// `team sync` without --profile preserves the legacy fallback of
	// syncing just team-home when no team.json is configured.
	explicitProfile := flagWasSet(fs, "profile") && profile != team.DefaultProfile
	if explicitProfile && !team.ExistsProfile(projectDir, profile) {
		return fmt.Errorf("no team configured for profile %q. Run '%s' first.", profile, profileInitCommand(profile))
	}
	targetDirs := []string{projectDir}
	if team.ExistsProfile(projectDir, profile) {
		t, err := team.ReadProfile(projectDir, profile)
		if err != nil {
			return fmt.Errorf("read team: %w", err)
		}
		targetDirs, err = syncTargetDirs(projectDir, t.Members, *allowOutside)
		if err != nil {
			return err
		}
		// Default-profile sync keeps the legacy "always include team-home"
		// behavior so the project's root CLAUDE.md / AGENTS.md stay in
		// sync even when no member lives there. For an explicit non-default
		// profile, sync is scoped to that profile's member cwds exactly,
		// matching the locked Step 9A semantics.
		if !explicitProfile {
			targetDirs, err = ensureTeamHomeSyncTarget(targetDirs, projectDir)
			if err != nil {
				return err
			}
		}
	}

	var allPlans []rules.SyncPlan
	for _, dir := range targetDirs {
		plans, err := rules.Plan(dir, body)
		if err != nil {
			return fmt.Errorf("plan sync for %s: %w", dir, err)
		}
		allPlans = append(allPlans, plans...)
	}

	drift := false
	var currentDir string
	for _, p := range allPlans {
		dir := filepath.Dir(p.Target)
		if dir != currentDir {
			if currentDir != "" {
				fmt.Println()
			}
			fmt.Printf("# %s\n", dir)
			currentDir = dir
		}
		status := describePlan(p)
		fmt.Printf("  %-12s %s\n", p.Basename, status)
		if !p.Unchanged {
			drift = true
		}
	}

	if !*apply {
		if drift {
			quietNotice("\nPreview only. Re-run with --apply to write.\n")
			return fmt.Errorf("drift detected")
		}
		return nil
	}

	n, err := rules.Apply(allPlans)
	if err != nil {
		return err
	}
	quietNotice("Wrote %d file(s).\n", n)
	return nil
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func syncTargetDirs(projectDir string, members []team.Member, allowOutside bool) ([]string, error) {
	home, err := canonicalDir(projectDir)
	if err != nil {
		return nil, fmt.Errorf("resolve team-home: %w", err)
	}
	targets := uniqueMemberCWDs(home, members)
	if len(targets) == 0 {
		targets = []string{home}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(targets))
	for _, raw := range targets {
		dir, err := canonicalDir(raw)
		if err != nil {
			return nil, fmt.Errorf("resolve sync target %s: %w", raw, err)
		}
		if !allowOutside && !pathWithin(home, dir) {
			return nil, fmt.Errorf("sync target %s is outside team-home %s; pass --allow-outside to write there", dir, home)
		}
		if !seen[dir] {
			seen[dir] = true
			out = append(out, dir)
		}
	}
	sort.Strings(out)
	return out, nil
}

func ensureTeamHomeSyncTarget(targetDirs []string, projectDir string) ([]string, error) {
	homeDir, err := canonicalDir(projectDir)
	if err != nil {
		return nil, fmt.Errorf("resolve team-home: %w", err)
	}
	if containsString(targetDirs, homeDir) {
		return targetDirs, nil
	}
	out := append([]string(nil), targetDirs...)
	out = append(out, homeDir)
	sort.Strings(out)
	return out, nil
}

func canonicalDir(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", abs)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func describePlan(p rules.SyncPlan) string {
	switch {
	case p.Unchanged:
		return "up to date"
	case p.Creating:
		return "will create"
	case p.Adopting:
		return "will adopt existing content and add managed block"
	default:
		return "will refresh managed block"
	}
}

func printTeamUsage() {
	fmt.Print(`amq-squad team - manage this project's agent team

Usage:
  amq-squad team                      Smart default: show commands, or init if none exists
  amq-squad team init [options]       Pick personas, choose CLIs, and seed rules
  amq-squad team resume [options]     Plan the safe path to bring the team back
                                      after reboot/upgrade/terminal close.
                                      Classifies each member as live/restore/
                                      launch fresh/blocked and prints copy-
                                      pasteable commands. Plan-only by default.
  amq-squad team rules init [--force] Seed or refresh team-rules.md
  amq-squad team sync [--apply] [--profile NAME]
                                      Sync CLAUDE.md and AGENTS.md from team-rules.md
                                      (default: preview; --apply writes; --profile
                                      scopes to that profile's member cwds)
  amq-squad team profiles             List configured team profiles (read-only)
  amq-squad team rm [--profile NAME]  Delete one team profile config (confirm-gated)

To launch the team, use the top-level 'up' verb: 'amq-squad up' brings it up,
'amq-squad up --dry-run' prints one launch command per member. ('team show'
and 'team launch' were removed in 2.0.)

Most subcommands accept --profile NAME to operate on a named profile under
.amq-squad/teams/<name>.json; omit the flag (or pass --profile default) to
operate on .amq-squad/team.json.

Personas come from the built-in catalog. Run 'amq-squad team init --help' to
see them and the available flags. Use --binary fullstack=codex to run a
persona with a different CLI.

Examples:
  amq-squad team init --roles cto,fullstack --binary cto=codex
  amq-squad up --dry-run
  amq-squad team sync --apply
  amq-squad team rm --profile review
`)
}
