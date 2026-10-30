package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAmqNOCVersionText(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return Run([]string{"version"}, "v-test")
	})
	if err != nil {
		t.Fatalf("Run version: %v\nstderr:\n%s", err, stderr)
	}
	if strings.TrimSpace(stdout) != "amq-noc v-test" {
		t.Fatalf("version stdout = %q, want amq-noc v-test", stdout)
	}
}

// amq-noc 1.0 has one view; help names it and the old board commands are gone.
func TestHelpDescribesCommandCenterOnly(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return Run([]string{"--help"}, "v-test")
	})
	if err != nil {
		t.Fatalf("Run --help: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{"command center", "amq-squad status --json", "--fleet"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help missing %q:\n%s", want, stdout)
		}
	}
	for _, gone := range []string{"noc options", "Multi-root NOC", "confirm-gated"} {
		if strings.Contains(stdout, gone) {
			t.Errorf("help still mentions the old board: %q", gone)
		}
	}
}

func TestHelpHasNoDoubleBlankLine(t *testing.T) {
	stdout, _, err := captureOutput(t, func() error { return Run([]string{"--help"}, "v-test") })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout, "\n\n\n") {
		t.Fatalf("help has a double blank line:\n%s", stdout)
	}
}

func TestOldNocSubcommandIsGone(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return Run([]string{"noc", "--once"}, "v-test")
	})
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("amq-noc noc must be an unknown command, got %v", err)
	}
}

// Acceptance 7: amq-noc starts in the command center (a bare run reads the
// status call and renders it), and no file of the old board remains.
func TestBareRunRendersCommandCenter(t *testing.T) {
	dir := t.TempDir()
	doc := `{"schema_version":1,"producer":"amq-squad","generated_at":"2026-10-24T12:00:00Z","root":"/r","session":"s","project_path":"/p","capabilities":[],"buckets":[],"seats":[],"tasks":[{"id":"t1","title":"Only task","bucket":"working","state":"online","project_path":"/p","updated_at":"2026-10-24T11:59:00Z","created_at":"2026-10-24T11:00:00Z"}],"attention":[],"gates":[],"actions":{"new_task":{"id":"task.new","label":"new task","argv":["amq-squad","task","add"],"inputs":[]}},"scheduler":{},"warnings":[]}`
	if err := os.WriteFile(filepath.Join(dir, "status.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nif [ \"$1 $2\" != \"status --json\" ]; then echo \"unexpected: $*\" >&2; exit 1; fi\n/bin/cat '" + filepath.Join(dir, "status.json") + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "amq-squad"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	stdout, stderr, err := captureOutput(t, func() error { return Run([]string{"--once"}, "v-test") })
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	for _, want := range []string{"Agent command center", "All 1", "Only task", "Working"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("frame missing %q:\n%s", want, stdout)
		}
	}
}

func TestOldBoardFilesAreGone(t *testing.T) {
	for _, p := range []string{"../console", "../noc", "../state", "../act", "../catalog", "../launch", "../rules", "../team",
		"noc.go", "status_board.go", "status.go"} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s still exists", p)
		}
	}
}

// An unknown bucket reaches the screen: the --once frame carries the warning text.
func TestOnceFrameShowsBucketWarning(t *testing.T) {
	dir := t.TempDir()
	doc := `{"schema_version":1,"producer":"amq-squad","generated_at":"2026-10-24T12:00:00Z","root":"/r","session":"s","project_path":"/p","capabilities":[],"buckets":[],"seats":[],"tasks":[{"id":"t1","title":"Odd task","bucket":"surprise","state":"online","project_path":"/p","updated_at":"2026-10-24T11:59:00Z","created_at":"2026-10-24T11:00:00Z"}],"attention":[],"gates":[],"actions":{"new_task":{"id":"task.new","label":"new task","argv":["amq-squad","task","add"],"inputs":[]}},"scheduler":{},"warnings":[]}`
	if err := os.WriteFile(filepath.Join(dir, "status.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n/bin/cat '" + filepath.Join(dir, "status.json") + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "amq-squad"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	stdout, stderr, err := captureOutput(t, func() error { return Run([]string{"--once"}, "v-test") })
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	for _, want := range []string{"task t1: unknown bucket", "Odd task", "Inactive (unknown)"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("frame lacks %q:\n%s", want, stdout)
		}
	}
}

func TestRootAndFleetTogetherIsAUsageError(t *testing.T) {
	_, _, err := captureOutput(t, func() error { return Run([]string{"--root", "/r", "--fleet", "--once"}, "v-test") })
	if err == nil || !strings.Contains(err.Error(), "--root and --fleet cannot be used together") || ExitCode(err) != 1 {
		t.Fatalf("err = %v (exit %d)", err, ExitCode(err))
	}
}

// Outside a squad's shell (no AM_ROOT, no --root), a failed status read says
// how to point amq-noc at a squad.
func TestNoRootHint(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\necho 'error: status: no store root; pass --root, set AM_ROOT, or pass --session' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "amq-squad"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("AM_ROOT", "")
	_, _, err := captureOutput(t, func() error { return Run([]string{"--once"}, "v-test") })
	if err == nil || !strings.Contains(err.Error(), "no store root") || !strings.Contains(err.Error(), "pass --root DIR, or use --fleet") {
		t.Fatalf("err = %v", err)
	}
	t.Setenv("AM_ROOT", "/r")
	_, _, err = captureOutput(t, func() error { return Run([]string{"--once"}, "v-test") })
	if err == nil || strings.Contains(err.Error(), "use --fleet") {
		t.Fatalf("with AM_ROOT set the hint must not show: %v", err)
	}
}
