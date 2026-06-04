package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/omriariav/amq-noc/internal/team"
)

// profileRow is one entry inside the team profiles list. Reused by both
// the human tabwriter output and the team_profiles JSON envelope so the
// two views can never drift.
type profileRow struct {
	Profile      string                 `json:"profile"`
	Path         string                 `json:"path"`
	Members      int                    `json:"members"`
	Workstream   string                 `json:"workstream,omitempty"`
	Operator     profileOperatorRow     `json:"operator"`
	Capabilities profileCapabilitiesRow `json:"capabilities"`
}

type profileOperatorRow struct {
	Enabled  bool   `json:"enabled"`
	Handle   string `json:"handle,omitempty"`
	Runnable bool   `json:"runnable"`
}

type profileCapabilitiesRow struct {
	OperatorGates bool `json:"operator_gates"`
}

// teamProfilesEnvelopeData is the kind="team_profiles" payload.
type teamProfilesEnvelopeData struct {
	Profiles []profileRow `json:"profiles"`
}

// resolveProfileFlag normalizes a --profile value: empty or "default" maps
// to the implicit default profile; non-default names are validated against
// the slug rules. Returns the canonical profile name and any error.
func resolveProfileFlag(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == team.DefaultProfile {
		return team.DefaultProfile, nil
	}
	if err := team.ValidateProfileName(name); err != nil {
		return "", fmt.Errorf("--profile: %w", err)
	}
	return name, nil
}

func runTeamProfiles(args []string) error {
	fs := flag.NewFlagSet("team profiles", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit a schema-versioned team_profiles envelope instead of the human table")
	projectFlag := fs.String("project", "", "project/team-home directory to inspect (default: cwd)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad team profiles - list configured team profiles

Usage:
  amq-squad team profiles [--project DIR] [--json]

Default first, then named profiles sorted alphabetically. Columns: PROFILE,
PATH, MEMBERS, WORKSTREAM. Read-only. Use 'amq-squad new profile NAME' to add
a profile and 'amq-squad team rm --profile NAME' to delete one.

Examples:
  amq-squad team profiles
  amq-squad team profiles --project ~/Code/app
  amq-squad team profiles --json
`)
	}
	if err := parseFlags(fs, args); err != nil {
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

	var rows []profileRow
	if team.Exists(projectDir) {
		t, err := team.Read(projectDir)
		if err != nil {
			// Mirror the named-profile path: warn on stderr so the broken
			// config is visible while stdout (especially under --json) stays
			// a valid envelope of whatever was readable.
			fmt.Fprintf(os.Stderr, "warning: read profile %s: %v\n", team.DefaultProfile, err)
		} else {
			rows = append(rows, profileRow{
				Profile:      team.DefaultProfile,
				Path:         team.Path(projectDir),
				Members:      len(t.Members),
				Workstream:   profileDisplayWorkstream(t),
				Operator:     profileOperator(t),
				Capabilities: profileCapabilities(t),
			})
		}
	}
	named, err := team.ListProfiles(projectDir)
	if err != nil {
		return fmt.Errorf("list profiles: %w", err)
	}
	for _, name := range named {
		t, err := team.ReadProfile(projectDir, name)
		if err != nil {
			// Skip unreadable profile but warn so the user sees the
			// breakage. JSON mode still emits a valid envelope on stdout;
			// warnings only land on stderr.
			fmt.Fprintf(os.Stderr, "warning: read profile %s: %v\n", name, err)
			continue
		}
		rows = append(rows, profileRow{
			Profile:      name,
			Path:         team.ProfilePath(projectDir, name),
			Members:      len(t.Members),
			Workstream:   profileDisplayWorkstream(t),
			Operator:     profileOperator(t),
			Capabilities: profileCapabilities(t),
		})
	}
	if *jsonOut {
		return printJSONEnvelope("team_profiles", teamProfilesEnvelopeData{Profiles: rows})
	}
	if len(rows) == 0 {
		fmt.Fprintln(os.Stderr, "No team profiles configured. Run 'amq-squad new team' to create one.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROFILE\tPATH\tMEMBERS\tWORKSTREAM")
	for _, r := range rows {
		ws := r.Workstream
		if ws == "" {
			ws = "(default)"
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", r.Profile, r.Path, r.Members, ws)
	}
	return w.Flush()
}

func profileOperator(t team.Team) profileOperatorRow {
	return profileOperatorRow{
		Enabled:  t.Operator.Enabled,
		Handle:   t.Operator.Handle,
		Runnable: t.Operator.Runnable,
	}
}

func profileCapabilities(t team.Team) profileCapabilitiesRow {
	return profileCapabilitiesRow{OperatorGates: t.Capabilities.OperatorGates}
}

func profileDisplayWorkstream(t team.Team) string {
	if inferred := inferredSharedMemberSession(t); inferred != "" {
		return inferred
	}
	if pinned := strings.TrimSpace(t.Workstream); pinned != "" {
		return pinned
	}
	return defaultWorkstreamName(t.Project)
}
