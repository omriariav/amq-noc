package cli

import (
	"reflect"
	"testing"

	"github.com/omriariav/amq-noc/internal/console"
)

// consoleUpArgs must stay in lockstep with the console preview
// (lifecycleOp.command()'s lifecycleUp case): same flags, same order, no
// --session (amq-squad derives the workstream from the team config, #22).
func TestConsoleUpArgs(t *testing.T) {
	got := consoleUpArgs("/tmp/team home", "")
	want := []string{
		"start",
		"--project", "/tmp/team home",
		"--yes",
		"--target", "new-session",
		"--terminal-session", "amq-squad-team-home",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("consoleUpArgs = %v, want %v", got, want)
	}
}

func TestConsoleUpArgsCarriesProfile(t *testing.T) {
	got := consoleUpArgs("/tmp/p", "review")
	want := []string{
		"start",
		"--project", "/tmp/p",
		"--profile", "review",
		"--yes",
		"--target", "new-session",
		"--terminal-session", "amq-squad-p",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("consoleUpArgs = %v, want %v", got, want)
	}
}

func TestConsoleUpArgsDefaultProfileOmitted(t *testing.T) {
	got := consoleUpArgs("/tmp/p", "default")
	for _, a := range got {
		if a == "--profile" {
			t.Fatalf("default profile must be omitted, got %v", got)
		}
	}
}

func TestConsoleLifecycleRejectsUnknownVerb(t *testing.T) {
	// The verb switch must stay closed: an unknown verb errors instead of
	// silently no-opping, so a console/cli verb drift is loud.
	if err := consoleLifecycle(console.LifecycleRequest{Verb: "bogus", ProjectDir: "/tmp/p"}); err == nil {
		t.Fatal("unknown verb must error")
	}
}
