package team

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestProfilePathDefaultLivesAtTeamJson(t *testing.T) {
	dir := t.TempDir()
	got := ProfilePath(dir, DefaultProfile)
	want := filepath.Join(dir, DirName, FileName)
	if got != want {
		t.Errorf("default profile path = %q, want %q", got, want)
	}
	if Path(dir) != got {
		t.Errorf("Path(dir) must equal ProfilePath(dir, default)")
	}
	// Empty profile name is also the default.
	if ProfilePath(dir, "") != got {
		t.Errorf("empty profile name should map to default")
	}
}

func TestProfilePathNamedLivesUnderTeamsDir(t *testing.T) {
	dir := t.TempDir()
	got := ProfilePath(dir, "review")
	want := filepath.Join(dir, DirName, TeamsDirName, "review.json")
	if got != want {
		t.Errorf("named profile path = %q, want %q", got, want)
	}
}

func TestValidateProfileNameSlugRules(t *testing.T) {
	good := []string{"review", "code-review", "feature_x", "v1-0-0"}
	for _, name := range good {
		if err := ValidateProfileName(name); err != nil {
			t.Errorf("ValidateProfileName(%q): %v", name, err)
		}
	}
	bad := []string{"", "Review", "code review", "feature/x", "v1.0", "café"}
	for _, name := range bad {
		if err := ValidateProfileName(name); err == nil {
			t.Errorf("ValidateProfileName(%q): want error", name)
		}
	}
}

func TestWriteProfileNeverCreatesDefaultUnderTeamsDir(t *testing.T) {
	dir := t.TempDir()
	if err := WriteProfile(dir, DefaultProfile, Team{
		Members: []Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}},
	}); err != nil {
		t.Fatal(err)
	}
	// Default profile lives at .amq-squad/team.json, not teams/default.json.
	if _, err := os.Stat(filepath.Join(dir, DirName, TeamsDirName, "default.json")); err == nil {
		t.Fatal("WriteProfile(default) accidentally created teams/default.json")
	}
	if _, err := os.Stat(filepath.Join(dir, DirName, FileName)); err != nil {
		t.Fatalf("default team.json not written: %v", err)
	}
}

func TestWriteProfileNamedCreatesTeamsDir(t *testing.T) {
	dir := t.TempDir()
	if err := WriteProfile(dir, "review", Team{
		Members: []Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "review"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, DirName, TeamsDirName, "review.json")); err != nil {
		t.Fatalf("named profile file not written: %v", err)
	}
}

func TestReadProfileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := Team{
		Workstream: "review",
		Members:    []Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "review"}},
	}
	if err := WriteProfile(dir, "review", in); err != nil {
		t.Fatal(err)
	}
	got, err := ReadProfile(dir, "review")
	if err != nil {
		t.Fatalf("ReadProfile: %v", err)
	}
	if got.Workstream != "review" {
		t.Errorf("workstream = %q", got.Workstream)
	}
	if got.Project != dir {
		t.Errorf("Project not stamped: %q", got.Project)
	}
}

// Schema 1 files must still load. Writes always re-emit schema 2 so a
// future Read sees the upgraded value.
func TestReadProfileAcceptsSchema1ThenWriteEmitsSchema2(t *testing.T) {
	dir := t.TempDir()
	teamsDir := filepath.Join(dir, DirName)
	if err := os.MkdirAll(teamsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{
  "schema": 1,
  "members": [{"role":"cto","binary":"codex","handle":"cto","session":"s"}],
  "created_at": "2026-01-01T00:00:00Z"
}
`
	if err := os.WriteFile(filepath.Join(teamsDir, FileName), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read schema 1: %v", err)
	}
	if got.Schema != 1 {
		t.Errorf("Read should preserve original Schema field as 1, got %d", got.Schema)
	}
	if err := Write(dir, got); err != nil {
		t.Fatalf("Write: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(teamsDir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Schema int `json:"schema"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Schema != 2 {
		t.Errorf("on-disk schema after Write = %d, want 2", decoded.Schema)
	}
}

func TestListProfilesDefaultExcludedSortedAlpha(t *testing.T) {
	dir := t.TempDir()
	// Default profile present at top-level team.json should NOT appear in
	// ListProfiles output; callers prepend "default" themselves.
	if err := WriteProfile(dir, DefaultProfile, Team{
		Members: []Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"review", "alpha", "feature_x"} {
		if err := WriteProfile(dir, name, Team{
			Members: []Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: name}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ListProfiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha", "feature_x", "review"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListProfiles = %v, want %v", got, want)
	}
}

func TestListProfilesIgnoresInvalidNamesAndNonJSON(t *testing.T) {
	dir := t.TempDir()
	teamsDir := filepath.Join(dir, DirName, TeamsDirName)
	if err := os.MkdirAll(teamsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Manually plant junk files; ListProfiles must skip them.
	for _, name := range []string{"BadCase.json", "ok.json", "no-extension", "feature x.json"} {
		if err := os.WriteFile(filepath.Join(teamsDir, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ListProfiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "ok" {
		t.Errorf("ListProfiles = %v, want [ok]", got)
	}
}

func TestExistsProfileSeparatesDefaultFromNamed(t *testing.T) {
	dir := t.TempDir()
	if Exists(dir) {
		t.Fatal("Exists should be false on empty project")
	}
	if err := WriteProfile(dir, "review", Team{
		Members: []Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "review"}},
	}); err != nil {
		t.Fatal(err)
	}
	if Exists(dir) {
		t.Error("named profile must not satisfy default-profile Exists()")
	}
	if !ExistsProfile(dir, "review") {
		t.Error("ExistsProfile(review) should be true")
	}
	if ExistsProfile(dir, "missing") {
		t.Error("ExistsProfile(missing) should be false")
	}
}

func TestReadProfileRejectsBadName(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadProfile(dir, "Bad/Name"); err == nil || !strings.Contains(err.Error(), "profile name") {
		t.Errorf("want profile-name validation error, got %v", err)
	}
	if err := WriteProfile(dir, "Bad/Name", Team{}); err == nil || !strings.Contains(err.Error(), "profile name") {
		t.Errorf("want profile-name validation error on write, got %v", err)
	}
}
