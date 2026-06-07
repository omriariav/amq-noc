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

func TestAmqNOCHelpIsNOCFirst(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return Run([]string{"--help"}, "v-test")
	})
	if err != nil {
		t.Fatalf("Run --help: %v", err)
	}
	combined := stdout + stderr
	for _, want := range []string{
		"amq-noc - NOC command center",
		"amq-noc [noc options]",
		"amq-noc --root ~/Code",
		"Confirmed mutating actions remain preview-first",
	} {
		if !strings.Contains(combined, want) {
			t.Fatalf("help missing %q:\n%s", want, combined)
		}
	}
}

func TestAmqNOCBareRunsOnceBoard(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return Run([]string{"--once"}, "v-test")
	})
	if err != nil {
		t.Fatalf("Run --once: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "NOC") {
		t.Fatalf("bare --once should render NOC output, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "v-test") {
		t.Fatalf("bare --once should render binary version, got:\n%s", stdout)
	}
}
