package cli

import (
	"os"
	"testing"
)

// briefPath must mirror amq-squad's own BriefPath (and
// internal/noc/namespace.go's read-only namespaceBriefPath): the default
// profile keeps the un-nested <team-home>/.amq-squad/briefs/<session>.md
// layout, while a named profile nests under a <profile> subdirectory.
// Before this fix briefPath ignored profile entirely, so a named-profile
// session's brief read/write always hit the default-profile path.
func TestBriefPathIsProfileAware(t *testing.T) {
	const teamHome = "/repo/app"
	const session = "issue-96"

	defaultWant := "/repo/app/.amq-squad/briefs/issue-96.md"
	if got := briefPath(teamHome, "", session); got != defaultWant {
		t.Fatalf("briefPath(empty profile) = %q, want %q", got, defaultWant)
	}
	if got := briefPath(teamHome, "default", session); got != defaultWant {
		t.Fatalf("briefPath(explicit default profile) = %q, want %q", got, defaultWant)
	}

	namedWant := "/repo/app/.amq-squad/briefs/review/issue-96.md"
	if got := briefPath(teamHome, "review", session); got != namedWant {
		t.Fatalf("briefPath(named profile) = %q, want %q", got, namedWant)
	}
}

func TestBriefPathEmptyInputs(t *testing.T) {
	if got := briefPath("", "review", "issue-96"); got != "" {
		t.Fatalf("briefPath(empty team-home) = %q, want empty", got)
	}
	if got := briefPath("/repo/app", "review", ""); got != "" {
		t.Fatalf("briefPath(empty session) = %q, want empty", got)
	}
}

// TestSeedAndReadBriefRoundTripsPerProfile is the end-to-end regression for
// the profile-nesting fix: a named-profile seed must land under
// .amq-squad/briefs/<profile>/, and reading the SAME (profile, session) must
// find it, while reading the default profile for the same session name must
// not (it is a structurally different workstream namespace).
func TestSeedAndReadBriefRoundTripsPerProfile(t *testing.T) {
	teamHome := t.TempDir()

	if _, err := seedBriefData(teamHome, "review", "issue-96", "file:/dev/null", false, false); err != nil {
		t.Fatalf("seedBriefData(review): %v", err)
	}

	wantPath := briefPath(teamHome, "review", "issue-96")
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("seeded brief not found at profile-nested path %s: %v", wantPath, err)
	}

	got, err := readBriefData(teamHome, "review", "issue-96")
	if err != nil {
		t.Fatalf("readBriefData(review): %v", err)
	}
	if !got.Exists || got.Path != wantPath {
		t.Fatalf("readBriefData(review) = %+v, want Exists at %s", got, wantPath)
	}

	defaultRead, err := readBriefData(teamHome, "", "issue-96")
	if err != nil {
		t.Fatalf("readBriefData(default): %v", err)
	}
	if defaultRead.Exists {
		t.Fatalf("default-profile read must not see the review-profile brief: %+v", defaultRead)
	}
}
