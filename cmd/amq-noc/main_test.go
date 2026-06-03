package main

import (
	"io"
	"os"
	"testing"
)

func TestResolveVersionPrefersLdflagVersion(t *testing.T) {
	if got := resolveVersion("v1.2.3", "v9.9.9"); got != "v1.2.3" {
		t.Fatalf("resolveVersion() = %q, want %q", got, "v1.2.3")
	}
}

func TestResolveVersionFallsBackToModuleVersion(t *testing.T) {
	if got := resolveVersion("dev", "v1.2.3"); got != "v1.2.3" {
		t.Fatalf("resolveVersion() = %q, want %q", got, "v1.2.3")
	}
}

func TestResolveVersionKeepsDevForDevelBuild(t *testing.T) {
	if got := resolveVersion("dev", "(devel)"); got != "dev" {
		t.Fatalf("resolveVersion() = %q, want %q", got, "dev")
	}
}

// TestRunMapsExitCodes proves main routes errors through cli.ExitCode
// rather than hardcoding UsageError=2/generic=1.
func TestRunMapsExitCodes(t *testing.T) {
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	cases := []struct {
		name string
		args []string
		want int
	}{
		{"help", []string{"--help"}, 0},
		{"version", []string{"version"}, 0},
		{"unknown command -> usage", []string{"definitely-not-a-command"}, 1},
		{"unknown noc flag -> usage", []string{"--banana"}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := run(tc.args, "test", io.Discard)
			if got != tc.want {
				t.Errorf("run(%v) = %d, want %d", tc.args, got, tc.want)
			}
		})
	}
}
