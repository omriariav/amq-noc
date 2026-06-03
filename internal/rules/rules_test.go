package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanCreateWhenMissing(t *testing.T) {
	project := t.TempDir()
	// Pass a populated rules body to prove it is intentionally not copied
	// into the managed block.
	plans, err := Plan(project, "# Team Rules\n\n- one rule\n")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plans) != 2 {
		t.Fatalf("got %d plans, want 2", len(plans))
	}
	for _, p := range plans {
		if !p.Creating {
			t.Errorf("%s: Creating = false, want true", p.Basename)
		}
		if p.Unchanged {
			t.Errorf("%s: Unchanged = true, want false", p.Basename)
		}
		if p.Adopting {
			t.Errorf("%s: Adopting = true, want false", p.Basename)
		}
		if !strings.Contains(p.After, BeginMarker) || !strings.Contains(p.After, EndMarker) {
			t.Errorf("%s: After missing markers", p.Basename)
		}
		for _, want := range []string{
			"This project uses amq-squad for agent team coordination.",
			"**Team norms:** `.amq-squad/team-rules.md`",
			"**Your role:**",
			"**Active brief:** read `.amq-squad/briefs/<session>.md`",
			"These files are the source of truth.",
		} {
			if !strings.Contains(p.After, want) {
				t.Errorf("%s: After missing canonical pointer text %q", p.Basename, want)
			}
		}
		// Rules body must never leak into the managed block.
		if strings.Contains(p.After, "one rule") {
			t.Errorf("%s: rules body leaked into managed block:\n%s", p.Basename, p.After)
		}
		// Pointer stub moved off ACTIVE-EPIC in step 7.
		if strings.Contains(p.After, "ACTIVE-EPIC") {
			t.Errorf("%s: pointer stub still references ACTIVE-EPIC:\n%s", p.Basename, p.After)
		}
	}
}

func TestPlanAdoptsExistingContent(t *testing.T) {
	project := t.TempDir()
	existing := "# My Project\n\nUser docs.\n"
	if err := os.WriteFile(filepath.Join(project, ClaudeFile), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	plans, err := Plan(project, "# Team Rules\n")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	byName := map[string]SyncPlan{}
	for _, p := range plans {
		byName[p.Basename] = p
	}
	claude := byName[ClaudeFile]
	if !claude.Adopting {
		t.Error("CLAUDE.md: Adopting = false, want true")
	}
	if claude.Creating {
		t.Error("CLAUDE.md: Creating = true for pre-existing file")
	}
	if !strings.Contains(claude.After, "User docs.") {
		t.Error("CLAUDE.md: user content not preserved")
	}
	if !strings.Contains(claude.After, BeginMarker) {
		t.Error("CLAUDE.md: no markers inserted")
	}
	// User content must come before the managed block.
	userIdx := strings.Index(claude.After, "User docs.")
	markerIdx := strings.Index(claude.After, BeginMarker)
	if userIdx >= markerIdx {
		t.Error("CLAUDE.md: managed block not placed after user content")
	}

	agents := byName[AgentsFile]
	if !agents.Creating {
		t.Error("AGENTS.md: should be created fresh")
	}
}

func TestPlanRefreshesManagedBlockOnly(t *testing.T) {
	project := t.TempDir()
	// File already has markers with stale full-rules content (the pre-1.0
	// behavior that copied team-rules.md into the managed block).
	userPrefix := "# My Project\n\nUser docs.\n\n"
	stale := userPrefix + BeginMarker + "\n# Team Rules\n\n- OLD RULE\n\nLots of body.\n" + EndMarker + "\n"
	for _, name := range []string{ClaudeFile, AgentsFile} {
		if err := os.WriteFile(filepath.Join(project, name), []byte(stale), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	plans, err := Plan(project, "# Team Rules\n\n- NEW RULE\n")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, p := range plans {
		if p.Adopting || p.Creating {
			t.Errorf("%s: Adopting=%v Creating=%v, want both false", p.Basename, p.Adopting, p.Creating)
		}
		if p.Unchanged {
			t.Errorf("%s: Unchanged = true when migrating from full-copy to stub", p.Basename)
		}
		if strings.Contains(p.After, "OLD RULE") || strings.Contains(p.After, "Lots of body.") {
			t.Errorf("%s: stale full-copy content not removed:\n%s", p.Basename, p.After)
		}
		// The new rules body must NOT be copied into the stub.
		if strings.Contains(p.After, "NEW RULE") {
			t.Errorf("%s: rules body leaked into pointer stub:\n%s", p.Basename, p.After)
		}
		if !strings.Contains(p.After, "**Team norms:** `.amq-squad/team-rules.md`") {
			t.Errorf("%s: canonical pointer text missing:\n%s", p.Basename, p.After)
		}
		if !strings.Contains(p.After, "User docs.") {
			t.Errorf("%s: user content outside markers clobbered", p.Basename)
		}
	}
}

// TestPlanIdempotentOnceCanonical proves a second Plan call on an already-
// canonical file produces Unchanged.
func TestPlanIdempotentOnceCanonical(t *testing.T) {
	project := t.TempDir()
	plans, err := Plan(project, "# Team Rules\n")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if _, err := Apply(plans); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	plans, err = Plan(project, "# Team Rules\n\n- a totally different rule\n")
	if err != nil {
		t.Fatalf("Plan second: %v", err)
	}
	for _, p := range plans {
		if !p.Unchanged {
			t.Errorf("%s: Unchanged = false on identical pointer stub; rules body should not perturb output:\n%s", p.Basename, p.After)
		}
	}
}

// TestPlanRulesBodyNeverCopied is a direct assertion of cto's contract: no
// substring of the rules body should appear in the managed block.
func TestPlanRulesBodyNeverCopied(t *testing.T) {
	project := t.TempDir()
	rules := "# Team Rules\n\nSECRET-RULES-BODY-MARKER-99fd0\n\n## Approvals\n\n- cto reviews everything\n"
	plans, err := Plan(project, rules)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, p := range plans {
		if strings.Contains(p.After, "SECRET-RULES-BODY-MARKER-99fd0") {
			t.Errorf("%s: rules body leaked into managed block:\n%s", p.Basename, p.After)
		}
		if strings.Contains(p.After, "## Approvals") {
			t.Errorf("%s: rules body section heading leaked:\n%s", p.Basename, p.After)
		}
	}
}

func TestPlanUnchangedWhenAlreadyInSync(t *testing.T) {
	project := t.TempDir()
	body := "# Team Rules\n\n- a rule\n"

	// First pass: Plan + Apply to get files into canonical state.
	plans, err := Plan(project, body)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if _, err := Apply(plans); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Second pass with same body: all unchanged.
	plans, err = Plan(project, body)
	if err != nil {
		t.Fatalf("Plan second: %v", err)
	}
	for _, p := range plans {
		if !p.Unchanged {
			t.Errorf("%s: Unchanged = false on no-op sync", p.Basename)
		}
	}
}

func TestApplyOnlyWritesChanged(t *testing.T) {
	project := t.TempDir()
	plans, err := Plan(project, "# Team Rules\n")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	n, err := Apply(plans)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if n != 2 {
		t.Errorf("Apply touched %d files, want 2", n)
	}

	// No changes now.
	plans, _ = Plan(project, "# Team Rules\n")
	n, err = Apply(plans)
	if err != nil {
		t.Fatalf("Apply 2: %v", err)
	}
	if n != 0 {
		t.Errorf("Apply touched %d files on no-op, want 0", n)
	}
}

func TestApplyRejectsChangedFileSincePlan(t *testing.T) {
	project := t.TempDir()
	plans, err := Plan(project, "# Team Rules\n")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, ClaudeFile), []byte("user edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Apply(plans)
	if err == nil || !strings.Contains(err.Error(), "changed since sync plan") {
		t.Fatalf("Apply error = %v, want stale plan error", err)
	}
}

func TestApplyRejectsNewEmptyFileSincePlan(t *testing.T) {
	project := t.TempDir()
	plans, err := Plan(project, "# Team Rules\n")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, ClaudeFile), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Apply(plans)
	if err == nil || !strings.Contains(err.Error(), "changed since sync plan") {
		t.Fatalf("Apply error = %v, want stale plan error", err)
	}
}

func TestApplyDoesNotLeaveTempFiles(t *testing.T) {
	project := t.TempDir()
	plans, err := Plan(project, "# Team Rules\n")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if _, err := Apply(plans); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	entries, err := os.ReadDir(project)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("left temp file behind: %s", e.Name())
		}
	}
}

func TestApplyPreservesExistingFileMode(t *testing.T) {
	project := t.TempDir()
	path := filepath.Join(project, ClaudeFile)
	if err := os.WriteFile(path, []byte("# Existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plans, err := Plan(project, "# Team Rules\n")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if _, err := Apply(plans); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}
}

func TestEnsureStub(t *testing.T) {
	project := t.TempDir()
	wrote, err := EnsureStub(project)
	if err != nil {
		t.Fatalf("EnsureStub: %v", err)
	}
	if !wrote {
		t.Error("first EnsureStub should write")
	}

	// Second call: don't clobber.
	if err := os.WriteFile(Path(project), []byte("USER EDIT"), 0o644); err != nil {
		t.Fatal(err)
	}
	wrote, err = EnsureStub(project)
	if err != nil {
		t.Fatalf("EnsureStub 2: %v", err)
	}
	if wrote {
		t.Error("EnsureStub wrote over existing file")
	}
	b, _ := os.ReadFile(Path(project))
	if string(b) != "USER EDIT" {
		t.Error("EnsureStub clobbered user content")
	}
}
