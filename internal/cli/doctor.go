package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/omriariav/amq-noc/internal/rules"
	"github.com/omriariav/amq-noc/internal/team"
)

// doctorMinAMQVersion is the lowest AMQ release this build of amq-squad
// expects to interoperate with. Bumped manually when amq-squad starts to
// depend on newer AMQ behavior; the doctor check compares the running amq
// binary's reported version against this floor.
const doctorMinAMQVersion = "0.51.1"

type doctorStatus string

const (
	doctorOK   doctorStatus = "ok"
	doctorWarn doctorStatus = "warn"
	doctorFail doctorStatus = "fail"
)

// doctorCheck is one diagnostic result.
type doctorCheck struct {
	Name   string       `json:"name"`
	Status doctorStatus `json:"status"`
	Detail string       `json:"detail,omitempty"`
}

// doctorEnvelopeData is the kind="doctor" payload. team_home is the project
// root the report was scoped to. profile is the selected team profile.
// workstream is set only when that profile resolves one.
type doctorEnvelopeData struct {
	TeamHome   string                      `json:"team_home"`
	Profile    string                      `json:"profile,omitempty"`
	Workstream string                      `json:"workstream,omitempty"`
	Checks     []doctorCheck               `json:"checks"`
	Profiles   []doctorProfileEnvelopeData `json:"profiles,omitempty"`
}

// doctorProfileEnvelopeData is one per-profile doctor result when
// `doctor --all-profiles --json` is requested.
type doctorProfileEnvelopeData struct {
	Profile    string        `json:"profile"`
	Workstream string        `json:"workstream,omitempty"`
	Checks     []doctorCheck `json:"checks"`
}

// doctorExecution is the shared seam every doctor check reads from. Mirrors
// the statusExecution / downExecution pattern so unit tests can supply
// fakes for `amq env`, tmux discovery, and the liveness probe without
// touching the real binaries.
type doctorExecution struct {
	ProjectDir     string
	Out            io.Writer
	JSON           bool
	AllProfiles    bool
	ResolveAMQEnv  func(projectDir string) (amqEnv, error)
	LookPath       func(name string) (string, error)
	Probe          duplicateLaunchProbe
	WakeOverride   func(t team.Team, workstream string) []doctorCheck
	WorkstreamHint string
	Profile        string
}

func defaultDoctorExecution(projectDir string) doctorExecution {
	return doctorExecution{
		ProjectDir: projectDir,
		Out:        os.Stdout,
		ResolveAMQEnv: func(projectDir string) (amqEnv, error) {
			return resolveAMQEnvInDir(projectDir, "", "", "amq-squad")
		},
		LookPath: exec.LookPath,
		Probe:    defaultDuplicateLaunchProbe,
	}
}

func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit a schema-versioned doctor envelope instead of the human table")
	projectFlag := fs.String("project", "", "project/team-home directory to check (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to check (default: default profile)")
	allProfiles := fs.Bool("all-profiles", false, "check every configured team profile instead of one selected profile")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad doctor - check this project's amq-squad / AMQ setup

Usage:
  amq-squad doctor [--project DIR] [--profile NAME|--all-profiles] [--json]

Checks: AMQ version, selected team profile, tmux availability, configured
members' wake health, and CLAUDE.md / AGENTS.md marker integrity plus
pointer-sync drift for the selected profile's sync targets. Use
--all-profiles for NOC-style project health across every configured profile.
Read-only. Exits non-zero if any check is "fail".

Examples:
  amq-squad doctor
  amq-squad doctor --project ~/Code/app
  amq-squad doctor --profile review
  amq-squad doctor --all-profiles
  amq-squad doctor --json | jq '.data.checks[] | select(.status=="fail")'
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("doctor takes no positional arguments; got %d", fs.NArg())
	}
	if *allProfiles && flagWasSet(fs, "profile") {
		return usageErrorf("--all-profiles cannot be combined with --profile")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	projectDir, err := resolveProjectDirFlag(cwd, *projectFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	profile, err := resolveProfileFlag(*profileFlag)
	if err != nil {
		return err
	}
	d := defaultDoctorExecution(projectDir)
	d.JSON = *jsonOut
	d.Profile = profile
	d.AllProfiles = *allProfiles
	return executeDoctor(d)
}

func resolveProjectDirFlag(cwd, project string, explicit bool) (string, error) {
	if !explicit {
		return cwd, nil
	}
	project = strings.TrimSpace(project)
	if project == "" {
		return "", usageErrorf("--project requires a directory")
	}
	dir, err := expandPath(project)
	if err != nil {
		return "", fmt.Errorf("resolve --project: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("--project %s: %w", dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("--project %s is not a directory", dir)
	}
	return dir, nil
}

func executeDoctor(d doctorExecution) error {
	if d.AllProfiles {
		return executeDoctorAllProfiles(d)
	}
	checks, workstream := runDoctorChecks(d)
	if d.JSON {
		if err := writeJSONEnvelope(d.Out, "doctor", doctorEnvelopeData{
			TeamHome:   d.ProjectDir,
			Profile:    doctorProfile(d),
			Workstream: workstream,
			Checks:     checks,
		}); err != nil {
			return err
		}
	} else {
		writeDoctorTable(d.Out, checks)
	}
	if fails := countFails(checks); fails > 0 {
		return fmt.Errorf("doctor: %d check(s) failed", fails)
	}
	return nil
}

func executeDoctorAllProfiles(d doctorExecution) error {
	profiles, err := doctorProfilesToCheck(d.ProjectDir)
	if err != nil {
		return fmt.Errorf("list profiles: %w", err)
	}
	results := make([]doctorProfileEnvelopeData, 0, len(profiles))
	summaries := make([]doctorCheck, 0, len(profiles))
	totalFails := 0
	for _, profile := range profiles {
		profileExec := d
		profileExec.Profile = profile
		profileExec.AllProfiles = false
		checks, workstream := runDoctorChecks(profileExec)
		fails := countFails(checks)
		totalFails += fails
		nonOK := countNonOK(checks)
		summaries = append(summaries, doctorCheck{
			Name:   "profile " + profile,
			Status: doctorProfileSummaryStatus(fails, nonOK),
			Detail: doctorProfileSummaryDetail(workstream, len(checks), fails, nonOK),
		})
		results = append(results, doctorProfileEnvelopeData{
			Profile:    profile,
			Workstream: workstream,
			Checks:     checks,
		})
	}
	if d.JSON {
		if err := writeJSONEnvelope(d.Out, "doctor", doctorEnvelopeData{
			TeamHome: d.ProjectDir,
			Profile:  "all",
			Checks:   summaries,
			Profiles: results,
		}); err != nil {
			return err
		}
	} else {
		for i, result := range results {
			if i > 0 {
				fmt.Fprintln(d.Out)
			}
			workstream := result.Workstream
			if workstream == "" {
				workstream = "(default)"
			}
			fmt.Fprintf(d.Out, "PROFILE %s  WORKSTREAM %s\n", result.Profile, workstream)
			writeDoctorTable(d.Out, result.Checks)
		}
	}
	if totalFails > 0 {
		return fmt.Errorf("doctor: %d check(s) failed", totalFails)
	}
	return nil
}

func doctorProfilesToCheck(projectDir string) ([]string, error) {
	var profiles []string
	if team.Exists(projectDir) {
		profiles = append(profiles, team.DefaultProfile)
	}
	named, err := team.ListProfiles(projectDir)
	if err != nil {
		return nil, err
	}
	profiles = append(profiles, named...)
	if len(profiles) == 0 {
		return []string{team.DefaultProfile}, nil
	}
	return profiles, nil
}

func doctorProfileSummaryStatus(fails, nonOK int) doctorStatus {
	if fails > 0 {
		return doctorFail
	}
	if nonOK > 0 {
		return doctorWarn
	}
	return doctorOK
}

func doctorProfileSummaryDetail(workstream string, checks, fails, nonOK int) string {
	if workstream == "" {
		workstream = "(default)"
	}
	return fmt.Sprintf("workstream %s; %d checks, %d failed, %d non-ok", workstream, checks, fails, nonOK)
}

func countFails(checks []doctorCheck) int {
	n := 0
	for _, c := range checks {
		if c.Status == doctorFail {
			n++
		}
	}
	return n
}

func writeDoctorTable(out io.Writer, checks []doctorCheck) {
	policy := outputPolicyCurrent()
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "STATUS\tCHECK\tDETAIL")
	for _, c := range checks {
		detail := c.Detail
		if detail == "" {
			detail = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", colorStatus(policy, string(c.Status)), c.Name, detail)
	}
	w.Flush()
	if policy.Verbose {
		fmt.Fprintf(out, "\nverbose: %d checks total (%d fail / %d non-ok)\n", len(checks), countFails(checks), countNonOK(checks))
	}
}

func countNonOK(checks []doctorCheck) int {
	n := 0
	for _, c := range checks {
		if c.Status != doctorOK {
			n++
		}
	}
	return n
}

func runDoctorChecks(d doctorExecution) ([]doctorCheck, string) {
	checks := []doctorCheck{}
	checks = append(checks, doctorCheckAMQVersion(d))
	checks = append(checks, doctorCheckTeamConfig(d))
	checks = append(checks, doctorCheckTmux(d))
	checks = append(checks, doctorCheckMarkerIntegrity(d)...)
	checks = append(checks, doctorCheckPointerSync(d)...)
	wakeChecks, workstream := doctorCheckWake(d)
	checks = append(checks, wakeChecks...)
	return checks, workstream
}

func doctorProfile(d doctorExecution) string {
	if strings.TrimSpace(d.Profile) == "" {
		return team.DefaultProfile
	}
	return strings.TrimSpace(d.Profile)
}

func doctorCheckAMQVersion(d doctorExecution) doctorCheck {
	env, err := d.ResolveAMQEnv(d.ProjectDir)
	if err != nil {
		return doctorCheck{
			Name:   "amq version",
			Status: doctorFail,
			Detail: fmt.Sprintf("amq env failed: %v", err),
		}
	}
	got := strings.TrimSpace(env.AMQVersion)
	if got == "" {
		return doctorCheck{
			Name:   "amq version",
			Status: doctorFail,
			Detail: "amq env returned no version (compatibility unknown)",
		}
	}
	parsed, ok := parseSemverParts(got)
	if !ok {
		return doctorCheck{
			Name:   "amq version",
			Status: doctorFail,
			Detail: fmt.Sprintf("amq returned unparseable version %q", got),
		}
	}
	min, _ := parseSemverParts(doctorMinAMQVersion)
	if compareSemverParts(parsed, min) < 0 {
		return doctorCheck{
			Name:   "amq version",
			Status: doctorFail,
			Detail: fmt.Sprintf("amq %s is older than required %s", got, doctorMinAMQVersion),
		}
	}
	return doctorCheck{
		Name:   "amq version",
		Status: doctorOK,
		Detail: fmt.Sprintf("amq %s (min %s)", got, doctorMinAMQVersion),
	}
}

func doctorCheckTeamConfig(d doctorExecution) doctorCheck {
	profile := doctorProfile(d)
	path := team.ProfilePath(d.ProjectDir, profile)
	if !team.ExistsProfile(d.ProjectDir, profile) {
		if profile == team.DefaultProfile {
			if profiles, err := team.ListProfiles(d.ProjectDir); err == nil && len(profiles) > 0 {
				return doctorCheck{
					Name:   "team config",
					Status: doctorWarn,
					Detail: "no default team profile; configured profiles: " + strings.Join(profiles, ", ") + " (run 'amq-squad doctor --profile <name>')",
				}
			}
		}
		return doctorCheck{
			Name:   "team config",
			Status: doctorWarn,
			Detail: fmt.Sprintf("no team profile %q (run '%s')", profile, profileInitCommand(profile)),
		}
	}
	if _, err := team.ReadProfile(d.ProjectDir, profile); err != nil {
		return doctorCheck{
			Name:   "team config",
			Status: doctorFail,
			Detail: fmt.Sprintf("invalid %s: %v", filepath.Base(path), err),
		}
	}
	return doctorCheck{
		Name:   "team config",
		Status: doctorOK,
		Detail: path,
	}
}

func doctorCheckTmux(d doctorExecution) doctorCheck {
	path, err := d.LookPath("tmux")
	if err != nil {
		return doctorCheck{
			Name:   "tmux",
			Status: doctorFail,
			Detail: "tmux not found on PATH (required for team launch)",
		}
	}
	return doctorCheck{Name: "tmux", Status: doctorOK, Detail: path}
}

func doctorCheckMarkerIntegrity(d doctorExecution) []doctorCheck {
	dirs, err := doctorMarkerDirs(d)
	if err != nil {
		return []doctorCheck{{
			Name:   "markers",
			Status: doctorWarn,
			Detail: err.Error(),
		}}
	}
	files := []string{rules.ClaudeFile, rules.AgentsFile}
	out := make([]doctorCheck, 0, len(files)*len(dirs))
	for _, dir := range dirs {
		for _, name := range files {
			check := inspectMarkerIntegrity(dir, name)
			if len(dirs) > 1 {
				check.Name = "markers " + filepath.Base(dir) + "/" + name
			}
			out = append(out, check)
		}
	}
	return out
}

func doctorMarkerDirs(d doctorExecution) ([]string, error) {
	profile := doctorProfile(d)
	if !team.ExistsProfile(d.ProjectDir, profile) {
		return []string{d.ProjectDir}, nil
	}
	t, err := team.ReadProfile(d.ProjectDir, profile)
	if err != nil {
		return nil, fmt.Errorf("read team profile %q for marker targets: %w", profile, err)
	}
	projectDir := t.Project
	if strings.TrimSpace(projectDir) == "" {
		projectDir = d.ProjectDir
	}
	dirs, err := syncTargetDirs(projectDir, t.Members, true)
	if err != nil {
		return nil, err
	}
	if profile == team.DefaultProfile {
		dirs, err = ensureTeamHomeSyncTarget(dirs, d.ProjectDir)
		if err != nil {
			return nil, err
		}
	}
	if len(dirs) == 0 {
		return []string{d.ProjectDir}, nil
	}
	return dirs, nil
}

func inspectMarkerIntegrity(dir, name string) doctorCheck {
	path := filepath.Join(dir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return doctorCheck{
				Name:   "markers " + name,
				Status: doctorWarn,
				Detail: name + " not found (run 'amq-squad team sync --apply' to create it)",
			}
		}
		return doctorCheck{
			Name:   "markers " + name,
			Status: doctorFail,
			Detail: fmt.Sprintf("read %s: %v", path, err),
		}
	}
	body := string(data)
	begins := strings.Count(body, rules.BeginMarker)
	ends := strings.Count(body, rules.EndMarker)
	if begins == 0 && ends == 0 {
		return doctorCheck{
			Name:   "markers " + name,
			Status: doctorWarn,
			Detail: "no amq-squad managed block (run 'amq-squad team sync --apply')",
		}
	}
	if begins != 1 || ends != 1 {
		return doctorCheck{
			Name:   "markers " + name,
			Status: doctorFail,
			Detail: fmt.Sprintf("unbalanced markers: %d begin / %d end", begins, ends),
		}
	}
	beginIdx := strings.Index(body, rules.BeginMarker)
	endIdx := strings.Index(body, rules.EndMarker)
	if beginIdx > endIdx {
		return doctorCheck{
			Name:   "markers " + name,
			Status: doctorFail,
			Detail: "end marker appears before begin marker",
		}
	}
	return doctorCheck{
		Name:   "markers " + name,
		Status: doctorOK,
		Detail: path,
	}
}

func doctorCheckPointerSync(d doctorExecution) []doctorCheck {
	body, err := rules.Read(d.ProjectDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []doctorCheck{{
				Name:   "pointer sync",
				Status: doctorWarn,
				Detail: "no team-rules.md at " + rules.Path(d.ProjectDir) + " (run 'amq-squad team rules init')",
			}}
		}
		return []doctorCheck{{
			Name:   "pointer sync",
			Status: doctorFail,
			Detail: fmt.Sprintf("read %s: %v", rules.Path(d.ProjectDir), err),
		}}
	}
	dirs, err := doctorMarkerDirs(d)
	if err != nil {
		return []doctorCheck{{
			Name:   "pointer sync",
			Status: doctorWarn,
			Detail: err.Error(),
		}}
	}
	hint := doctorSyncCommandHint(d, dirs)
	out := []doctorCheck{}
	for _, dir := range dirs {
		plans, err := rules.Plan(dir, body)
		if err != nil {
			out = append(out, doctorCheck{
				Name:   "pointer sync " + filepath.Base(dir),
				Status: doctorFail,
				Detail: fmt.Sprintf("plan %s: %v", dir, err),
			})
			continue
		}
		for _, p := range plans {
			name := "pointer sync " + p.Basename
			if len(dirs) > 1 {
				name = "pointer sync " + filepath.Base(dir) + "/" + p.Basename
			}
			if p.Unchanged {
				out = append(out, doctorCheck{Name: name, Status: doctorOK, Detail: p.Target})
				continue
			}
			out = append(out, doctorCheck{
				Name:   name,
				Status: doctorWarn,
				Detail: describePointerSyncDrift(p) + " (run '" + hint + "')",
			})
		}
	}
	return out
}

func describePointerSyncDrift(p rules.SyncPlan) string {
	switch {
	case p.Creating:
		return p.Basename + " missing"
	case p.Adopting:
		return p.Basename + " has no managed block"
	default:
		return p.Basename + " managed block out of date"
	}
}

func doctorSyncCommandHint(d doctorExecution, dirs []string) string {
	args := []string{"amq-squad", "team", "sync"}
	profile := doctorProfile(d)
	if profile != team.DefaultProfile {
		args = append(args, "--profile", profile)
	}
	args = append(args, "--apply")
	if doctorSyncNeedsAllowOutside(d.ProjectDir, dirs) {
		args = append(args, "--allow-outside")
	}
	return strings.Join(args, " ")
}

func doctorSyncNeedsAllowOutside(projectDir string, dirs []string) bool {
	home, err := canonicalDir(projectDir)
	if err != nil {
		return false
	}
	for _, dir := range dirs {
		clean, err := canonicalDir(dir)
		if err != nil {
			continue
		}
		if !pathWithin(home, clean) {
			return true
		}
	}
	return false
}

// doctorCheckWake reuses classifyMemberStatus to classify the selected
// profile's configured members. Returns the resolved workstream alongside
// the checks so the JSON envelope can include it. No live signals or
// "missing" map to ok; "stale" maps to warn. AMQ env resolution failures
// surface as warn with the underlying error in detail.
func doctorCheckWake(d doctorExecution) ([]doctorCheck, string) {
	profile := doctorProfile(d)
	if d.WakeOverride != nil {
		// Tests can inject a deterministic wake-check builder.
		t, err := team.ReadProfile(d.ProjectDir, profile)
		if err != nil {
			return nil, ""
		}
		workstream, err := resolveTeamWorkstreamName(t, d.WorkstreamHint, d.WorkstreamHint != "")
		if err != nil {
			return nil, ""
		}
		return d.WakeOverride(t, workstream), workstream
	}
	if !team.ExistsProfile(d.ProjectDir, profile) {
		return []doctorCheck{{
			Name:   "wake",
			Status: doctorWarn,
			Detail: fmt.Sprintf("no team configured for profile %q; skipping wake checks", profile),
		}}, ""
	}
	t, err := team.ReadProfile(d.ProjectDir, profile)
	if err != nil {
		return []doctorCheck{{
			Name:   "wake",
			Status: doctorWarn,
			Detail: "team config unreadable; skipping wake checks",
		}}, ""
	}
	workstream, err := resolveTeamWorkstreamName(t, d.WorkstreamHint, d.WorkstreamHint != "")
	if err != nil {
		return []doctorCheck{{
			Name:   "wake",
			Status: doctorWarn,
			Detail: fmt.Sprintf("could not resolve workstream: %v", err),
		}}, ""
	}
	probe := d.Probe
	checks := make([]doctorCheck, 0, len(t.Members))
	for _, m := range orderedTeamMembers(t.Members) {
		rec := classifyMemberStatus(t, m, workstream, probe)
		checks = append(checks, doctorCheckFromStatus(rec))
	}
	return checks, workstream
}

// doctorCheckFromStatus maps a statusRecord into a doctorCheck. live and
// missing are ok; stale is warn. Any amq-env failure that classifyMember
// reports as missing-with-detail surfaces as warn so the operator sees the
// underlying error.
func doctorCheckFromStatus(s statusRecord) doctorCheck {
	name := "wake " + s.Role
	switch s.Status {
	case statusStateLive:
		return doctorCheck{Name: name, Status: doctorOK, Detail: s.Detail}
	case statusStateMissing:
		if strings.HasPrefix(s.Detail, "amq env unresolved") {
			return doctorCheck{Name: name, Status: doctorWarn, Detail: s.Detail}
		}
		return doctorCheck{Name: name, Status: doctorOK, Detail: s.Detail}
	case statusStateStale:
		return doctorCheck{Name: name, Status: doctorWarn, Detail: s.Detail}
	default:
		return doctorCheck{Name: name, Status: doctorWarn, Detail: s.Detail}
	}
}

// compareSemverParts returns -1, 0, or 1 comparing a to b.
func compareSemverParts(a, b [3]int) int {
	for i := 0; i < 3; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}

// parseSemverParts parses a major.minor.patch version string. Accepts a
// leading "v" and strips "-pre" / "+build" suffixes. Returns ok=false when
// no numeric major/minor/patch component is present so the caller can flag
// the version as unparseable.
func parseSemverParts(s string) ([3]int, bool) {
	var out [3]int
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	for _, sep := range []string{"-", "+"} {
		if idx := strings.Index(s, sep); idx >= 0 {
			s = s[:idx]
		}
	}
	if s == "" {
		return out, false
	}
	parts := strings.SplitN(s, ".", 3)
	for i := 0; i < 3; i++ {
		if i >= len(parts) {
			return out, false
		}
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
