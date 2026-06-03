package cli

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-noc/internal/team"
)

type teamRemoveExecution struct {
	ProjectDir  string
	Profile     string
	AllProfiles bool
	Yes         bool
	DryRun      bool
	Confirm     io.Reader
	Out         io.Writer
}

func runTeamRemove(args []string) error {
	fs := flag.NewFlagSet("team rm", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory to target (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to delete (default: default profile)")
	allProfiles := fs.Bool("all-profiles", false, "delete every configured team profile file for the project")
	yes := fs.Bool("yes", false, "skip the confirmation prompt (for automation)")
	fs.BoolVar(yes, "y", false, "shorthand for --yes")
	dryRun := fs.Bool("dry-run", false, "preview the deletion without removing files")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad team rm - delete team profile config

Usage:
  amq-squad team rm [PROFILE] [--project DIR] [--profile NAME] [--dry-run] [--yes|-y]
  amq-squad team rm --all-profiles [--project DIR] [--dry-run] [--yes|-y]
  amq-squad team delete [PROFILE] [--project DIR] [--profile NAME] [--dry-run] [--yes|-y]

Deletes the selected team profile config only:
  default profile -> .amq-squad/team.json
  named profile   -> .amq-squad/teams/<profile>.json

With --all-profiles, deletes every configured team profile file for the project
and requires typing the project directory name unless --yes is passed.

It does not delete AMQ session mailboxes, workstream briefs, team-rules.md, or
CLAUDE.md / AGENTS.md pointer stubs. Use top-level archive/rm for sessions.

By default the command previews the exact profile file and prompts for
confirmation (default: No). Pass --dry-run to preview without a prompt, or
--yes/-y for automation.

Examples:
  amq-squad team rm --dry-run
  amq-squad team rm --profile review
  amq-squad team delete review --project ~/Code/app --yes
`)
	}
	args = allowInterspersedFlags(fs, args)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return usageErrorf("team rm takes at most one profile argument; got %d", fs.NArg())
	}
	if fs.NArg() == 1 && flagWasSet(fs, "profile") {
		return usageErrorf("pass the profile either positionally or with --profile, not both")
	}
	if *allProfiles && (fs.NArg() > 0 || flagWasSet(fs, "profile")) {
		return usageErrorf("--all-profiles cannot be combined with a positional profile or --profile")
	}
	profileArg := *profileFlag
	if fs.NArg() == 1 {
		profileArg = fs.Arg(0)
	}
	profile, err := resolveProfileFlag(profileArg)
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
	return executeTeamRemove(teamRemoveExecution{
		ProjectDir:  projectDir,
		Profile:     profile,
		AllProfiles: *allProfiles,
		Yes:         *yes,
		DryRun:      *dryRun,
		Confirm:     os.Stdin,
		Out:         os.Stdout,
	})
}

func executeTeamRemove(e teamRemoveExecution) error {
	projectDir := strings.TrimSpace(e.ProjectDir)
	if projectDir == "" {
		return fmt.Errorf("project dir cannot be empty")
	}
	if e.AllProfiles {
		return executeTeamRemoveAll(e, projectDir)
	}
	profile, err := resolveProfileFlag(e.Profile)
	if err != nil {
		return err
	}
	if !team.ExistsProfile(projectDir, profile) {
		return fmt.Errorf("no team configured for profile %q at %s. Nothing removed.", profile, team.ProfilePath(projectDir, profile))
	}
	t, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return fmt.Errorf("read team profile %q: %w", profile, err)
	}
	out := e.Out
	if out == nil {
		out = os.Stdout
	}
	path := team.ProfilePath(projectDir, profile)
	fmt.Fprintln(out, "Team profile removal preview")
	fmt.Fprintf(out, "project:  %s\n", projectDir)
	fmt.Fprintf(out, "profile:  %s\n", profile)
	fmt.Fprintf(out, "path:     %s\n", path)
	fmt.Fprintf(out, "members:  %d\n", len(t.Members))
	fmt.Fprintln(out, "keeps:    AMQ sessions, briefs, team-rules.md, pointer stubs")
	if e.DryRun {
		fmt.Fprintln(out, "Dry run: no files removed.")
		return nil
	}
	if !e.Yes {
		if !confirmTeamRemove(out, e.Confirm, profile) {
			fmt.Fprintln(out, "team rm: aborted; no files removed.")
			return nil
		}
	}
	if err := team.DeleteProfile(projectDir, profile); err != nil {
		return fmt.Errorf("delete team profile %q: %w", profile, err)
	}
	fmt.Fprintf(out, "Removed team profile %s at %s.\n", profile, path)
	return nil
}

func executeTeamRemoveAll(e teamRemoveExecution, projectDir string) error {
	profiles, err := configuredTeamProfiles(projectDir)
	if err != nil {
		return err
	}
	if len(profiles) == 0 {
		return fmt.Errorf("no team profiles configured at %s. Nothing removed.", projectDir)
	}
	out := e.Out
	if out == nil {
		out = os.Stdout
	}
	fmt.Fprintln(out, "Team profile removal preview")
	fmt.Fprintf(out, "project:  %s\n", projectDir)
	fmt.Fprintln(out, "mode:     all team config")
	fmt.Fprintln(out, "paths:")
	for _, profile := range profiles {
		t, err := team.ReadProfile(projectDir, profile)
		if err != nil {
			return fmt.Errorf("read team profile %q: %w", profile, err)
		}
		fmt.Fprintf(out, "  - %s (%s, %d members)\n", team.ProfilePath(projectDir, profile), profile, len(t.Members))
	}
	fmt.Fprintln(out, "keeps:    AMQ sessions, briefs, team-rules.md, pointer stubs")
	if e.DryRun {
		fmt.Fprintln(out, "Dry run: no files removed.")
		return nil
	}
	if !e.Yes {
		if !confirmTeamRemoveAll(out, e.Confirm, filepath.Base(projectDir)) {
			fmt.Fprintln(out, "team rm: aborted; no files removed.")
			return nil
		}
	}
	for _, profile := range profiles {
		if err := team.DeleteProfile(projectDir, profile); err != nil {
			return fmt.Errorf("delete team profile %q: %w", profile, err)
		}
	}
	fmt.Fprintf(out, "Removed %d team profile files from %s.\n", len(profiles), projectDir)
	return nil
}

func configuredTeamProfiles(projectDir string) ([]string, error) {
	var profiles []string
	if team.ExistsProfile(projectDir, team.DefaultProfile) {
		profiles = append(profiles, team.DefaultProfile)
	}
	named, err := team.ListProfiles(projectDir)
	if err != nil {
		return nil, fmt.Errorf("list profiles: %w", err)
	}
	profiles = append(profiles, named...)
	return profiles, nil
}

func confirmTeamRemove(out io.Writer, r io.Reader, profile string) bool {
	if r == nil {
		r = os.Stdin
	}
	fmt.Fprintf(out, "Delete team profile %s? [y/N] ", profile)
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

func confirmTeamRemoveAll(out io.Writer, r io.Reader, projectName string) bool {
	if r == nil {
		r = os.Stdin
	}
	fmt.Fprintf(out, "Type project name %s to delete all team profile files: ", projectName)
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return false
	}
	return strings.TrimSpace(line) == projectName
}
