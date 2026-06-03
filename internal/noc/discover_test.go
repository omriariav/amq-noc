package noc

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// mkdirs creates every dir under root and returns the absolute root.
func mkdirs(t *testing.T, root string, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
}

func TestDiscover_FindsAgentMailParents(t *testing.T) {
	root := t.TempDir()
	// Three real projects, each anchored by a .agent-mail container.
	mkdirs(t, root,
		"proj-a/.agent-mail/main/agents/codex",
		"org/proj-b/.agent-mail/issue-1/agents/claude",
		"org/nested/proj-c/.agent-mail/agents/codex",
	)

	got, err := Discover([]string{root}, DefaultDepth)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	want := []string{
		filepath.Join(root, "org", "nested", "proj-c"),
		filepath.Join(root, "org", "proj-b"),
		filepath.Join(root, "proj-a"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Discover projects mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestDiscover_FindsTeamProfilesWithoutAgentMail(t *testing.T) {
	root := t.TempDir()
	defaultProfile := filepath.Join(root, "configured", SquadDirName)
	if err := os.MkdirAll(defaultProfile, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(defaultProfile, "team.json"), []byte(`{"schema":2}`), 0o600); err != nil {
		t.Fatal(err)
	}

	namedProfile := filepath.Join(root, "named-only", SquadDirName, "teams")
	if err := os.MkdirAll(namedProfile, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(namedProfile, "release.json"), []byte(`{"schema":2}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Discover([]string{root}, DefaultDepth)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	want := []string{
		filepath.Join(root, "configured"),
		filepath.Join(root, "named-only"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("team-profile discovery mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestDiscover_FindsGitCandidateProjects(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "repo-a/.git", "org/repo-b/.git")
	worktree := filepath.Join(root, "worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: ../.git/worktrees/worktree\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Discover([]string{root}, DefaultDepth)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	want := []string{
		filepath.Join(root, "org", "repo-b"),
		filepath.Join(root, "repo-a"),
		filepath.Join(root, "worktree"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("git candidate discovery mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestDiscover_PrunesHeavyDirs(t *testing.T) {
	root := t.TempDir()
	// A real project, plus .agent-mail-looking dirs buried inside pruned trees
	// that MUST be skipped.
	mkdirs(t, root,
		"real/.agent-mail/main/agents/codex",
		"real/node_modules/dep/.agent-mail/x", // pruned: node_modules
		"real/.git/worktrees/.agent-mail/y",   // pruned: .git
		"vendor/pkg/.agent-mail/z",            // pruned: vendor
		"build/out/.agent-mail/w",             // pruned: build
		"dist/out/.agent-mail/v",              // pruned: dist
		".cache/x/.agent-mail/u",              // pruned: .cache
		"Library/App/.agent-mail/t",           // pruned: Library
	)

	got, err := Discover([]string{root}, DefaultDepth)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	want := []string{filepath.Join(root, "real")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Discover should prune heavy dirs\n got: %v\nwant: %v", got, want)
	}
}

func TestDiscover_DoesNotDescendIntoContainerChildren(t *testing.T) {
	root := t.TempDir()
	// A nested .agent-mail INSIDE another project's container must not be
	// reported: once a container is matched we never walk its children, so a
	// stray ".agent-mail" deep inside sessions/agents is invisible.
	mkdirs(t, root,
		"proj/.agent-mail/main/agents/codex/.agent-mail/sneaky",
	)

	got, err := Discover([]string{root}, DefaultDepth)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	want := []string{filepath.Join(root, "proj")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Discover must not descend into container children\n got: %v\nwant: %v", got, want)
	}
}

func TestDiscover_RespectsDepthBound(t *testing.T) {
	root := t.TempDir()
	// .agent-mail at depth 1 (root/a/.agent-mail -> a is depth 1) and at depth 5.
	mkdirs(t, root,
		"a/.agent-mail/x",       // a at depth1, .agent-mail at depth2
		"b/c/d/e/.agent-mail/x", // .agent-mail at depth5
	)

	// depth 2 should find a but not the deep one.
	got, err := Discover([]string{root}, 2)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	want := []string{filepath.Join(root, "a")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("depth=2 mismatch\n got: %v\nwant: %v", got, want)
	}

	// A generous depth should find both.
	got, err = Discover([]string{root}, 8)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	want = []string{
		filepath.Join(root, "a"),
		filepath.Join(root, "b", "c", "d", "e"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("depth=8 mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestDiscover_MultipleRootsAndMissingRoot(t *testing.T) {
	r1 := t.TempDir()
	r2 := t.TempDir()
	mkdirs(t, r1, "p1/.agent-mail/x")
	mkdirs(t, r2, "p2/.agent-mail/x")

	got, err := Discover([]string{r1, r2, filepath.Join(r1, "does-not-exist"), ""}, DefaultDepth)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	want := []string{
		filepath.Join(r1, "p1"),
		filepath.Join(r2, "p2"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("multi-root mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestDiscover_DefaultDepthOnNonPositive(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "p/.agent-mail/x")
	got, err := Discover([]string{root}, 0) // 0 -> DefaultDepth
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	want := []string{filepath.Join(root, "p")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("default-depth mismatch\n got: %v\nwant: %v", got, want)
	}
}
