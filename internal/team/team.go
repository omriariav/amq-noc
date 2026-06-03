// Package team persists the set of agents the user wants booted for a
// project. It's a thin wrapper around a JSON file at <project>/.amq-squad/team.json.
package team

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func sortStrings(s []string) { sort.Strings(s) }

const (
	SchemaVersion = 2
	DirName       = ".amq-squad"
	FileName      = "team.json"
	TeamsDirName  = "teams"
	// DefaultProfile names the implicit project-default profile. It maps to
	// .amq-squad/team.json; a file at .amq-squad/teams/default.json is never
	// created (the on-disk encoding is the project root, not the teams dir).
	DefaultProfile = "default"
)

// Member is one row of the team: a role picked from the catalog plus the
// overrides the user chose at team init time.
//
// CWD is the working directory this agent runs from. Empty means "same as
// the team's project dir". Members can live in different directories; the
// team-home (where team.json lives) is just one of them.
//
// Session stores the member's default workstream hint. Current launch commands
// may override it with a shared workstream for the whole team run.
type Member struct {
	Role    string `json:"role"`    // catalog role ID, e.g. "cpo"
	Binary  string `json:"binary"`  // "claude" or "codex"
	Handle  string `json:"handle"`  // AMQ handle, defaults to Role
	Session string `json:"session"` // AMQ workstream session name
	Model   string `json:"model,omitempty"`
	CWD     string `json:"cwd,omitempty"`
	// Launcher is an optional wrapper command exec'd in place of Binary while
	// the member still receives AMQ identity, bootstrap, and a launch record.
	// LauncherArgs precede the agent's normal child args; the launcher is
	// expected to forward the trailing args to Binary so bootstrap survives.
	Launcher     string   `json:"launcher,omitempty"`
	LauncherArgs []string `json:"launcher_args,omitempty"`
}

// EffectiveCWD returns the member's working directory, falling back to the
// team's project dir when CWD is empty.
func (m Member) EffectiveCWD(projectDir string) string {
	if m.CWD != "" {
		return m.CWD
	}
	return projectDir
}

// Team is the persisted team config.
//
// Project is not serialized: it's always the directory that contains
// .amq-squad/team.json, derived at Read time. Persisting an absolute path
// would leak local paths into shared repos and break when the repo moves.
// CreatedAt is informational. Member sessions are legacy/default workstream
// hints; the live workstream can be overridden at launch time. Trust controls
// Codex trust defaults for generated launch commands ("sandboxed" or
// "trusted"; empty means sandboxed). BinaryArgs stores extra native CLI args
// by binary name, for example codex or claude.
//
// Workstream is a DEPRECATED SHIM (scheduled for removal in 2.1). It once
// pinned the team's default shared AMQ session, but it is no longer
// auto-stamped on init and member-session inference now wins over it. The
// field is still read so old team.json files keep loading; when it is the
// resolved source the CLI emits a deprecation notice steering operators to
// pass --session explicitly or re-init.
type Team struct {
	Schema  int    `json:"schema"`
	Project string `json:"-"`
	// Workstream: deprecated shim, see the Team doc comment. Removal in 2.1.
	Workstream string              `json:"workstream,omitempty"`
	Trust      string              `json:"trust,omitempty"`
	BinaryArgs map[string][]string `json:"binary_args,omitempty"`
	Members    []Member            `json:"members"`
	CreatedAt  time.Time           `json:"created_at"`
}

// Path returns the team.json path for the default profile under projectDir.
// It is preserved for compatibility with callers that don't care about
// non-default profiles; use ProfilePath when a profile name is in play.
func Path(projectDir string) string {
	return ProfilePath(projectDir, DefaultProfile)
}

// ProfilePath returns the team.json path for the given profile under
// projectDir. The default profile (or empty string) maps to
// <projectDir>/.amq-squad/team.json. Named profiles map to
// <projectDir>/.amq-squad/teams/<profile>.json.
func ProfilePath(projectDir, profile string) string {
	if profile == "" || profile == DefaultProfile {
		return filepath.Join(projectDir, DirName, FileName)
	}
	return filepath.Join(projectDir, DirName, TeamsDirName, profile+".json")
}

// ValidateProfileName enforces the profile-name slug rules: lowercase a-z,
// 0-9, hyphen, and underscore. The implicit "default" profile name is
// permitted by callers but does not need to match these rules; the on-disk
// encoding for "default" lives outside the teams/ directory.
func ValidateProfileName(s string) error {
	return validateSlug("profile name", s, true)
}

// Read loads the default-profile team config from projectDir. Returns
// os.ErrNotExist if no team.json is present.
func Read(projectDir string) (Team, error) {
	return ReadProfile(projectDir, DefaultProfile)
}

// ReadProfile loads a named profile from projectDir.
func ReadProfile(projectDir, profile string) (Team, error) {
	if profile != "" && profile != DefaultProfile {
		if err := ValidateProfileName(profile); err != nil {
			return Team{}, err
		}
	}
	p := ProfilePath(projectDir, profile)
	b, err := os.ReadFile(p)
	if err != nil {
		return Team{}, err
	}
	var t Team
	if err := json.Unmarshal(b, &t); err != nil {
		return Team{}, fmt.Errorf("parse %s: %w", p, err)
	}
	t.Project = projectDir
	if err := Validate(t); err != nil {
		return Team{}, fmt.Errorf("validate %s: %w", p, err)
	}
	return t, nil
}

// Write atomically persists the default-profile team config under projectDir.
func Write(projectDir string, t Team) error {
	return WriteProfile(projectDir, DefaultProfile, t)
}

// WriteProfile atomically persists a named profile under projectDir. The
// schema field is unconditionally set to the current SchemaVersion so
// reading a schema 1 file and writing it back upgrades the on-disk shape.
func WriteProfile(projectDir, profile string, t Team) error {
	if profile != "" && profile != DefaultProfile {
		if err := ValidateProfileName(profile); err != nil {
			return err
		}
	}
	path := ProfilePath(projectDir, profile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("ensure %s: %w", filepath.Dir(path), err)
	}
	t.Schema = SchemaVersion
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	if err := Validate(t); err != nil {
		return fmt.Errorf("validate team: %w", err)
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal team: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// Exists reports whether a default-profile team.json is present.
func Exists(projectDir string) bool {
	return ExistsProfile(projectDir, DefaultProfile)
}

// ExistsProfile reports whether a named profile config exists.
func ExistsProfile(projectDir, profile string) bool {
	_, err := os.Stat(ProfilePath(projectDir, profile))
	return err == nil
}

// DeleteProfile removes one persisted team profile. It only deletes the profile
// JSON file: AMQ sessions, briefs, team-rules.md, and managed pointer stubs are
// intentionally left for their own explicit commands.
func DeleteProfile(projectDir, profile string) error {
	if profile != "" && profile != DefaultProfile {
		if err := ValidateProfileName(profile); err != nil {
			return err
		}
	}
	path := ProfilePath(projectDir, profile)
	if err := os.Remove(path); err != nil {
		return err
	}
	if profile != "" && profile != DefaultProfile {
		_ = os.Remove(filepath.Dir(path))
	}
	return nil
}

// ListProfiles returns the named profiles present under projectDir, sorted
// alphabetically. The default profile is NOT included; callers prepend
// "default" themselves when the default file exists. Returns nil with no
// error when no teams/ directory exists.
func ListProfiles(projectDir string) ([]string, error) {
	dir := filepath.Join(projectDir, DirName, TeamsDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	out := []string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		profile := strings.TrimSuffix(name, ".json")
		if profile == "" || profile == DefaultProfile {
			continue
		}
		if err := ValidateProfileName(profile); err != nil {
			continue
		}
		out = append(out, profile)
	}
	sortStrings(out)
	return out, nil
}

func Validate(t Team) error {
	if t.Workstream != "" {
		if err := ValidateSessionName(t.Workstream); err != nil {
			return fmt.Errorf("workstream: %w", err)
		}
	}
	if t.Trust != "" && t.Trust != "sandboxed" && t.Trust != "trusted" {
		return fmt.Errorf("trust: invalid trust mode %q: use sandboxed or trusted", t.Trust)
	}
	for binary, args := range t.BinaryArgs {
		if err := ValidateDisplayValue("binary_args key", binary); err != nil {
			return fmt.Errorf("binary_args[%q]: %w", binary, err)
		}
		for i, arg := range args {
			if err := ValidateDisplayValue("arg", arg); err != nil {
				return fmt.Errorf("binary_args[%q][%d]: %w", binary, i, err)
			}
		}
	}
	seenHandles := map[string]bool{}
	for i, m := range t.Members {
		prefix := fmt.Sprintf("members[%d]", i)
		if err := validateMember(prefix, m); err != nil {
			return err
		}
		handle := m.Handle
		if handle == "" {
			handle = m.Role
		}
		if handle != "" {
			if seenHandles[handle] {
				return fmt.Errorf("%s: duplicate handle %q", prefix, handle)
			}
			seenHandles[handle] = true
		}
	}
	return nil
}

func validateMember(prefix string, m Member) error {
	if m.Role == "" {
		return fmt.Errorf("%s.role: cannot be empty", prefix)
	}
	if err := ValidateRoleID(m.Role); err != nil {
		return fmt.Errorf("%s.role: %w", prefix, err)
	}
	if m.Handle != "" {
		if err := ValidateHandle(m.Handle); err != nil {
			return fmt.Errorf("%s.handle: %w", prefix, err)
		}
	}
	if m.Session != "" {
		if err := ValidateSessionName(m.Session); err != nil {
			return fmt.Errorf("%s.session: %w", prefix, err)
		}
	}
	if m.Binary != "" {
		if err := ValidateDisplayValue("binary", m.Binary); err != nil {
			return fmt.Errorf("%s.binary: %w", prefix, err)
		}
	}
	if m.Model != "" {
		if err := ValidateDisplayValue("model", m.Model); err != nil {
			return fmt.Errorf("%s.model: %w", prefix, err)
		}
	}
	if m.CWD != "" {
		if err := ValidateDisplayValue("cwd", m.CWD); err != nil {
			return fmt.Errorf("%s.cwd: %w", prefix, err)
		}
		if !filepath.IsAbs(m.CWD) {
			return fmt.Errorf("%s.cwd: must be absolute", prefix)
		}
	}
	if m.Launcher != "" {
		if err := ValidateDisplayValue("launcher", m.Launcher); err != nil {
			return fmt.Errorf("%s.launcher: %w", prefix, err)
		}
		if !filepath.IsAbs(m.Launcher) {
			return fmt.Errorf("%s.launcher: must be absolute", prefix)
		}
	}
	for i, a := range m.LauncherArgs {
		if err := ValidateDisplayValue("launcher_args", a); err != nil {
			return fmt.Errorf("%s.launcher_args[%d]: %w", prefix, i, err)
		}
	}
	if m.Launcher == "" && len(m.LauncherArgs) > 0 {
		return fmt.Errorf("%s.launcher_args: set launcher before launcher_args", prefix)
	}
	return nil
}

func ValidateRoleID(s string) error {
	return validateSlug("role", s, true)
}

func ValidateHandle(s string) error {
	return validateSlug("handle", s, true)
}

func ValidateSessionName(s string) error {
	return validateSlug("session name", s, true)
}

func validateSlug(label, s string, allowHyphen bool) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("%s cannot be empty", label)
	}
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || allowHyphen && r == '-' {
			continue
		}
		return fmt.Errorf("invalid %s %q: use lowercase a-z, 0-9, - and _ only", label, s)
	}
	return nil
}

func ValidateDisplayValue(label, s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("%s cannot be empty", label)
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s contains control characters", label)
		}
	}
	return nil
}
