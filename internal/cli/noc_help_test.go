package cli

import (
	"strings"
	"testing"
)

// noc --help must not advertise keys the pruned router does not handle (issue
// #4.1): jump (J), command palette (p), mute (A), or context/DLQ/inbox/read
// (c/D/i/v); and it must describe the four-status pulse (waiting, not blocked).
func TestNOCHelpHasNoDeadKeys(t *testing.T) {
	stdout, stderr, _ := captureOutput(t, func() error {
		return Run([]string{"noc", "--help"}, "v-test")
	})
	help := stdout + stderr

	for _, dead := range []string{"(or J)", "COMMAND PALETTE", "palette", "press A to mute", "with 'A'", "c/D/i/v", "blocked / stale"} {
		if strings.Contains(help, dead) {
			t.Errorf("noc --help still advertises removed/dead surface %q (issue #4.1)", dead)
		}
	}
	for _, want := range []string{"waiting / stale", "Navigation is read-only"} {
		if !strings.Contains(help, want) {
			t.Errorf("noc --help missing truthful copy %q:\n%s", want, help)
		}
	}
}
