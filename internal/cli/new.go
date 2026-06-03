package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/omriariav/amq-noc/internal/team"
)

// runNew is the operator-facing creation group. It is intentionally a thin
// layer over the established primitives: `new team` writes the default team
// profile, `new profile` writes a named profile via team init, and `new
// session` starts fresh work via up.
func runNew(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(os.Stderr, newUsage)
		if len(args) == 0 {
			return usageErrorf("new requires a subcommand: 'team', 'profile', or 'session'")
		}
		return nil
	}

	switch args[0] {
	case "team":
		return runNewTeam(args[1:])
	case "profile":
		return runNewProfile(args[1:])
	case "session":
		return runNewSession(args[1:])
	default:
		return usageErrorf("unknown 'new' subcommand: %q. Try 'team', 'profile', or 'session'.", args[0])
	}
}

const newUsage = `amq-squad new - create teams, profiles, and workstream sessions

Usage:
  amq-squad new team [--project DIR] [--sync] [--dry-run [--json]] [team init options]
  amq-squad new profile NAME [--project DIR] [--sync] [--dry-run [--json]] [team init options]
  amq-squad new session [--project DIR] [--profile NAME] [<session>] [up options]

new team is the create-focused alias for ` + "`team init`" + ` for the default
profile. new profile NAME is the create-focused alias for
` + "`team init --profile NAME`" + `.
Pass --sync to immediately write the managed CLAUDE.md / AGENTS.md pointer
stubs after the team profile and team-rules.md are created.
new session is the create-focused alias for ` + "`up`" + ` and keeps the same
NEW-work safety rule: it refuses a session that already exists.
It supports up's launch options, including --profile and --seed-from for
authoring the workstream brief before launch.
--project scopes creation to a team-home without requiring a prior cd.

Examples:
  amq-squad roles
  amq-squad new team --dry-run --roles cto,qa
  amq-squad new team --sync --dry-run --json --roles cto,qa
  amq-squad new team --sync --roles cto,fullstack --binary cto=codex
  amq-squad new team --roles 2,9
  amq-squad new team --roles all
  amq-squad new profile review --project ~/Code/app --roles cto
  amq-squad new profile review --roles cto,qa --sync
  amq-squad new session issue-96
  amq-squad new session issue-98 --seed-from issue:31
  amq-squad new session --project ~/Code/app issue-97
`

func runNewTeam(args []string) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprint(os.Stderr, `amq-squad new team - create a team profile

Usage:
  amq-squad new team [--project DIR] [--sync] [--dry-run [--json]] [team init options]

This delegates to 'amq-squad team init' for the default profile, including
interactive persona selection when --roles/--personas is omitted. For named
profiles, prefer 'amq-squad new profile NAME'. --roles and --personas accept
IDs, 1-based market numbers, or all. With --project, the team profile is written
in DIR without changing your shell.

Pass --sync to run 'amq-squad team sync --apply' after the profile is written.
For named profiles, the same --profile is passed through to team sync. If member
cwds are outside the team-home, pass --allow-outside with --sync.
Pass --dry-run to preview the profile and rules paths without writing files.
Add --json to emit a team_profile_plan envelope on stdout.

Examples:
  amq-squad roles
  amq-squad new team --dry-run --roles cto,qa
  amq-squad new team --sync --dry-run --json --roles cto,qa
  amq-squad new team --sync --roles cto,fullstack --binary cto=codex
  amq-squad new team --roles 2,9
  amq-squad new team --roles all
  amq-squad new team --project ~/Code/app --roles cto,qa
  amq-squad new profile review --roles cto --session review
`)
		return nil
	}
	project, rest, err := peelNewProjectFlag(args)
	if err != nil {
		return err
	}
	sync, allowOutside, rest, err := peelNewTeamSyncFlags(rest)
	if err != nil {
		return err
	}
	if allowOutside && !sync {
		return usageErrorf("--allow-outside only applies with --sync")
	}
	dryRun, err := newTeamDryRunFromArgs(rest)
	if err != nil {
		return err
	}
	return runInProject(project, func() error {
		initOpts := teamInitRunOptions{}
		if dryRun && sync {
			profile, err := newTeamProfileFromArgs(rest)
			if err != nil {
				return err
			}
			initOpts.SyncCommand = newTeamSyncCommand(project, profile, allowOutside)
		}
		if err := runTeamInitWithOptions(rest, initOpts); err != nil {
			return err
		}
		if dryRun {
			return nil
		}
		if !sync {
			return nil
		}
		profile, err := newTeamProfileFromArgs(rest)
		if err != nil {
			return err
		}
		syncArgs := []string{"--apply"}
		if profile != team.DefaultProfile {
			syncArgs = append(syncArgs, "--profile", profile)
		}
		if allowOutside {
			syncArgs = append(syncArgs, "--allow-outside")
		}
		if err := runTeamSync(syncArgs); err != nil {
			return fmt.Errorf("team created, but sync failed: %w", err)
		}
		return nil
	})
}

func newTeamDryRunFromArgs(args []string) (bool, error) {
	return newTeamBoolFlagFromArgs(args, "dry-run")
}

func newTeamBoolFlagFromArgs(args []string, name string) (bool, error) {
	dryRun := false
	long := "--" + name
	short := "-" + name
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		switch {
		case a == long || a == short:
			dryRun = true
		case strings.HasPrefix(a, long+"="):
			v, err := parseNewBoolFlag(long, strings.TrimPrefix(a, long+"="))
			if err != nil {
				return false, err
			}
			dryRun = v
		case strings.HasPrefix(a, short+"="):
			v, err := parseNewBoolFlag(long, strings.TrimPrefix(a, short+"="))
			if err != nil {
				return false, err
			}
			dryRun = v
		}
	}
	return dryRun, nil
}

func newTeamSyncCommand(project, profile string, allowOutside bool) string {
	parts := []string{"amq-squad", "team", "sync", "--apply"}
	if strings.TrimSpace(project) != "" {
		if cwd, err := os.Getwd(); err == nil {
			parts = append(parts, "--project", shellQuote(cwd))
		}
	}
	if profile != team.DefaultProfile {
		parts = append(parts, "--profile", shellQuote(profile))
	}
	if allowOutside {
		parts = append(parts, "--allow-outside")
	}
	return strings.Join(parts, " ")
}

func runNewProfile(args []string) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprint(os.Stderr, `amq-squad new profile - create a named team profile

Usage:
  amq-squad new profile NAME [--project DIR] [--sync] [--dry-run [--json]] [team init options]

This delegates to 'amq-squad team init --profile NAME'. It is the named-profile
counterpart to 'amq-squad new team', so it inherits role selection, --binary
overrides, --dry-run, --json, --project, and --sync.

Examples:
  amq-squad roles
  amq-squad new profile review --roles cto,qa
  amq-squad new profile review --sync --roles cto,qa
  amq-squad new profile review --dry-run --json --roles 2,9
  amq-squad new profile --project ~/Code/app review --roles cto
`)
		return nil
	}
	teamArgs, err := newProfileTeamArgs(args)
	if err != nil {
		return err
	}
	return runNewTeam(teamArgs)
}

func newProfileTeamArgs(args []string) ([]string, error) {
	profile := ""
	out := make([]string, 0, len(args)+2)
	valueFlags := map[string]bool{
		"--binary":      true,
		"--claude-args": true,
		"--codex-args":  true,
		"--cwd":         true,
		"--model":       true,
		"--personas":    true,
		"--project":     true,
		"--roles":       true,
		"--session":     true,
		"--trust":       true,
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			out = append(out, args[i:]...)
			break
		}
		if a == "--profile" || strings.HasPrefix(a, "--profile=") {
			return nil, usageErrorf("new profile NAME sets --profile automatically; do not pass --profile")
		}
		if strings.HasPrefix(a, "-") {
			out = append(out, a)
			name := a
			hasInlineValue := false
			if idx := strings.Index(a, "="); idx >= 0 {
				name = a[:idx]
				hasInlineValue = true
			}
			if valueFlags[name] && !hasInlineValue {
				if i+1 >= len(args) {
					return nil, usageErrorf("%s requires a value", name)
				}
				out = append(out, args[i+1])
				i++
			}
			continue
		}
		if profile != "" {
			return nil, usageErrorf("new profile takes exactly one profile name; got extra argument %q", a)
		}
		profile = strings.TrimSpace(a)
	}
	if profile == "" {
		return nil, usageErrorf("new profile requires a profile name")
	}
	if profile == team.DefaultProfile {
		return nil, usageErrorf("new profile creates named profiles; use 'new team' for the default profile")
	}
	if err := team.ValidateProfileName(profile); err != nil {
		return nil, usageErrorf("profile %q: %v", profile, err)
	}
	return append([]string{"--profile", profile}, out...), nil
}

func runNewSession(args []string) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprint(os.Stderr, `amq-squad new session - create a fresh workstream session

Usage:
  amq-squad new session [--project DIR] [--profile NAME] [<session>] [up options]

This delegates to 'amq-squad up'. It creates NEW work and refuses an existing
session; use 'amq-squad resume' to continue one or 'amq-squad up --reset' to
start one over. With --project, the session is created for that team-home
without changing your shell.
Use --profile to launch a named team profile. Use --seed-from to author the
workstream brief before launch; supported sources are file:<path>, issue:<n>,
and gh:owner/repo#<n>. With --seed-from --dry-run, only the candidate brief is
printed and nothing is written.

Examples:
  amq-squad new session issue-96
  amq-squad new session --project ~/Code/app issue-97
  amq-squad new session --profile review issue-98
  amq-squad new session issue-98 --seed-from issue:31
  amq-squad new session --dry-run --seed-from file:./brief.md issue-98
  amq-squad new session --dry-run --no-bootstrap issue-96
`)
		return nil
	}
	project, rest, err := peelNewProjectFlag(args)
	if err != nil {
		return err
	}
	return runInProject(project, func() error { return runUp(rest) })
}

func peelNewProjectFlag(args []string) (string, []string, error) {
	return peelProjectFlag(args)
}

func peelProjectFlag(args []string) (string, []string, error) {
	out := make([]string, 0, len(args))
	project := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			out = append(out, args[i:]...)
			break
		}
		if a == "--project" {
			if i+1 >= len(args) {
				return "", nil, usageErrorf("--project requires a directory")
			}
			if project != "" {
				return "", nil, usageErrorf("--project may be passed only once")
			}
			project = strings.TrimSpace(args[i+1])
			if project == "" {
				return "", nil, usageErrorf("--project requires a directory")
			}
			i++
			continue
		}
		if strings.HasPrefix(a, "--project=") {
			if project != "" {
				return "", nil, usageErrorf("--project may be passed only once")
			}
			project = strings.TrimSpace(strings.TrimPrefix(a, "--project="))
			if project == "" {
				return "", nil, usageErrorf("--project requires a directory")
			}
			continue
		}
		out = append(out, a)
	}
	return project, out, nil
}

func peelNewTeamSyncFlags(args []string) (bool, bool, []string, error) {
	out := make([]string, 0, len(args))
	sync := false
	allowOutside := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			out = append(out, args[i:]...)
			break
		}
		switch {
		case a == "--sync":
			sync = true
			continue
		case strings.HasPrefix(a, "--sync="):
			v, err := parseNewBoolFlag("--sync", strings.TrimPrefix(a, "--sync="))
			if err != nil {
				return false, false, nil, err
			}
			sync = v
			continue
		case a == "--allow-outside":
			allowOutside = true
			continue
		case strings.HasPrefix(a, "--allow-outside="):
			v, err := parseNewBoolFlag("--allow-outside", strings.TrimPrefix(a, "--allow-outside="))
			if err != nil {
				return false, false, nil, err
			}
			allowOutside = v
			continue
		}
		out = append(out, a)
	}
	return sync, allowOutside, out, nil
}

func parseNewBoolFlag(name, raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "t", "true", "yes", "y", "on":
		return true, nil
	case "0", "f", "false", "no", "n", "off":
		return false, nil
	default:
		return false, usageErrorf("%s expects a boolean value", name)
	}
}

func newTeamProfileFromArgs(args []string) (string, error) {
	profile := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		if a == "--profile" {
			if i+1 >= len(args) {
				return "", usageErrorf("--profile requires a name")
			}
			profile = args[i+1]
			i++
			continue
		}
		if strings.HasPrefix(a, "--profile=") {
			profile = strings.TrimPrefix(a, "--profile=")
		}
	}
	return resolveProfileFlag(profile)
}

func runInProject(project string, fn func() error) error {
	if strings.TrimSpace(project) == "" {
		return fn()
	}
	dir, err := expandPath(project)
	if err != nil {
		return fmt.Errorf("resolve --project: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("--project %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("--project %s is not a directory", dir)
	}
	old, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	if err := os.Chdir(dir); err != nil {
		return fmt.Errorf("chdir %s: %w", dir, err)
	}
	defer func() { _ = os.Chdir(old) }()
	return fn()
}
