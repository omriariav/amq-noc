package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-noc/internal/console"
)

// TestConsoleSendPromptArgsDefaultProfile pins the `amq-squad send` argv for a
// default-profile send-prompt: --profile is omitted, and the body is read from
// STDIN (--body-file -), never passed as a flag.
func TestConsoleSendPromptArgsDefaultProfile(t *testing.T) {
	got, err := consoleSendPromptArgs(console.SendPromptRequest{
		ProjectDir: "/tmp/team home",
		Session:    "issue-1",
		Role:       "qa",
		Body:       "rerun the smoke suite",
	})
	if err != nil {
		t.Fatalf("consoleSendPromptArgs: %v", err)
	}
	want := []string{
		"send",
		"--project", "/tmp/team home",
		"--session", "issue-1",
		"--role", "qa",
		"--body-file", "-",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("consoleSendPromptArgs = %#v, want %#v", got, want)
	}
}

// TestConsoleSendPromptArgsNamedProfile pins the argv when a non-default profile
// is set: --profile is included between --project and --session.
func TestConsoleSendPromptArgsNamedProfile(t *testing.T) {
	got, err := consoleSendPromptArgs(console.SendPromptRequest{
		ProjectDir: "/tmp/team home",
		Profile:    "review",
		Session:    "issue-2",
		Role:       "dev",
		Body:       "hi",
	})
	if err != nil {
		t.Fatalf("consoleSendPromptArgs: %v", err)
	}
	want := []string{
		"send",
		"--project", "/tmp/team home",
		"--profile", "review",
		"--session", "issue-2",
		"--role", "dev",
		"--body-file", "-",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("consoleSendPromptArgs = %#v, want %#v", got, want)
	}
}

func TestConsoleSendPromptArgsRejectsEmptyRole(t *testing.T) {
	_, err := consoleSendPromptArgs(console.SendPromptRequest{Role: " ", Body: "hi"})
	if err == nil {
		t.Fatal("consoleSendPromptArgs should reject an empty role")
	}
	if !strings.Contains(err.Error(), "role cannot be empty") {
		t.Fatalf("consoleSendPromptArgs error = %v", err)
	}
}

// writeSquadStub installs a fake `amq-squad` on a stub path and redirects
// generatedSquadCommandOverride at it. The script body is the test's choice; it
// runs as the child amq-noc shells, so a test can capture its stdin or force a
// non-zero exit.
func writeSquadStub(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	stub := filepath.Join(dir, "amq-squad-stub")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	old := generatedSquadCommandOverride
	generatedSquadCommandOverride = stub
	t.Cleanup(func() { generatedSquadCommandOverride = old })
	return dir
}

// TestConsoleSendPromptPipesBodyToStdin exercises the REAL exec path
// (delegateSquadStdin): the stub captures its STDIN to a file, and the test
// asserts the typed body arrived on the child's stdin (not as an argv flag).
func TestConsoleSendPromptPipesBodyToStdin(t *testing.T) {
	dir := writeSquadStub(t, `cat > "$STDIN_CAPTURE"`)
	capture := filepath.Join(dir, "stdin.txt")
	t.Setenv("STDIN_CAPTURE", capture)

	body := "please rerun the smoke suite\nthen report back"
	err := consoleSendPrompt(console.SendPromptRequest{
		ProjectDir: dir, // a real dir so the child cwd is valid
		Session:    "issue-1",
		Role:       "qa",
		Body:       body,
	})
	if err != nil {
		t.Fatalf("consoleSendPrompt should succeed against the stub, got %v", err)
	}
	got, readErr := os.ReadFile(capture)
	if readErr != nil {
		t.Fatalf("reading captured stdin: %v", readErr)
	}
	if string(got) != body {
		t.Fatalf("send-prompt body should be piped to stdin verbatim:\n got %q\nwant %q", string(got), body)
	}
}

// TestConsoleSendPromptSurfacesBusyError checks that amq-squad's mid-turn
// refusal (non-zero exit with a "busy" message) is returned with the detail
// intact, so the console layer's "busy" detection can match it.
func TestConsoleSendPromptSurfacesBusyError(t *testing.T) {
	dir := writeSquadStub(t, `echo "agent is busy (mid-turn); pass --force to override" >&2
exit 1`)

	err := consoleSendPrompt(console.SendPromptRequest{
		ProjectDir: dir,
		Session:    "issue-1",
		Role:       "qa",
		Body:       "hi",
	})
	if err == nil {
		t.Fatal("a busy mid-turn refusal should be returned as an error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "busy") {
		t.Fatalf("busy refusal detail should be preserved for the NOC to match, got %v", err)
	}
}
