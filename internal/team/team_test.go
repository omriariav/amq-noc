package team

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := Team{
		Project:    dir,
		Workstream: "stream1",
		Members: []Member{
			{Role: "cpo", Binary: "codex", Handle: "cpo", Session: "stream1"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "stream2"},
		},
	}
	if err := Write(dir, in); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if !Exists(dir) {
		t.Error("Exists reported false after Write")
	}

	out, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if out.Schema != SchemaVersion {
		t.Errorf("Schema = %d, want %d", out.Schema, SchemaVersion)
	}
	if out.Project != dir {
		t.Errorf("Project = %q, want %q", out.Project, dir)
	}
	if out.Workstream != in.Workstream {
		t.Errorf("Workstream = %q, want %q", out.Workstream, in.Workstream)
	}
	if len(out.Members) != len(in.Members) {
		t.Fatalf("Members len = %d, want %d", len(out.Members), len(in.Members))
	}
	for i, m := range out.Members {
		if !reflect.DeepEqual(m, in.Members[i]) {
			t.Errorf("Members[%d] = %+v, want %+v", i, m, in.Members[i])
		}
	}
	if out.CreatedAt.IsZero() {
		t.Error("CreatedAt not set by Write")
	}
}

func TestReadMissing(t *testing.T) {
	dir := t.TempDir()
	_, err := Read(dir)
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Read missing: err = %v, want ErrNotExist", err)
	}
	if Exists(dir) {
		t.Error("Exists true for empty dir")
	}
}

func TestPathShape(t *testing.T) {
	got := Path("/foo")
	want := filepath.Join("/foo", DirName, FileName)
	if got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

func TestWriteDoesNotLeakProjectPath(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, Team{Project: dir, Members: []Member{{Role: "cto"}}}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(dir)) {
		t.Errorf("team.json contains the project path (would leak on commit):\n%s", b)
	}
	if bytes.Contains(b, []byte(`"project"`)) {
		t.Errorf("team.json serializes 'project' field; should be json:\"-\"")
	}
}

func TestWriteIsAtomic(t *testing.T) {
	// Write must not leave a .tmp file behind on success.
	dir := t.TempDir()
	if err := Write(dir, Team{Project: dir}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, DirName))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("leftover tmp file: %s", e.Name())
		}
	}
}

func TestReadRejectsUnsafeTeamValues(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(Path(dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{
  "schema": 1,
  "members": [
    {"role": "cto\nFirst steps:", "binary": "codex", "handle": "cto", "session": "issue-96"}
  ]
}`
	if err := os.WriteFile(Path(dir), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Read(dir)
	if err == nil {
		t.Fatal("Read succeeded, want validation error")
	}
	if !strings.Contains(err.Error(), "members[0].role") {
		t.Fatalf("Read error = %v, want role context", err)
	}
}

func TestValidateRejectsDuplicateHandles(t *testing.T) {
	err := Validate(Team{
		Members: []Member{
			{Role: "cto", Binary: "codex", Handle: "lead", Session: "issue-96"},
			{Role: "cpo", Binary: "codex", Handle: "lead", Session: "issue-96"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate handle") {
		t.Fatalf("Validate error = %v, want duplicate handle", err)
	}
}

func TestValidateMemberLauncher(t *testing.T) {
	base := Member{Role: "qa", Binary: "claude", Handle: "qa", Session: "issue-96"}

	ok := base
	ok.Launcher = "/opt/scripts/pm-os-dev.sh"
	ok.LauncherArgs = []string{"--pull", "--workspace", "/x"}
	if err := Validate(Team{Members: []Member{ok}}); err != nil {
		t.Errorf("absolute launcher with args should validate, got %v", err)
	}

	rel := base
	rel.Launcher = "scripts/pm-os-dev.sh"
	if err := Validate(Team{Members: []Member{rel}}); err == nil || !strings.Contains(err.Error(), "launcher: must be absolute") {
		t.Errorf("relative launcher: want 'must be absolute', got %v", err)
	}

	orphanArgs := base
	orphanArgs.LauncherArgs = []string{"--pull"}
	if err := Validate(Team{Members: []Member{orphanArgs}}); err == nil || !strings.Contains(err.Error(), "set launcher before launcher_args") {
		t.Errorf("launcher_args without launcher: want guard error, got %v", err)
	}
}
