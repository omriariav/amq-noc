package cli

import (
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

func TestOldNocSubcommandIsGone(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return Run([]string{"noc", "--once"}, "v-test")
	})
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("amq-noc noc must be an unknown command, got %v", err)
	}
}
