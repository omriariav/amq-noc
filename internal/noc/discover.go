// Package noc is the NOC ("network operations center") data + jump layer: a
// UI-free, testable foundation for a multi-project command center over
// amq-squad sessions. It discovers every amq-squad project or candidate
// team-home under a set of roots, collects a per-project state.Snapshot, rolls
// the triage headlines up across projects, and resolves a running agent to a
// concrete tmux pane so the operator can jump straight to it.
//
// Design rules (mirrored from internal/state):
//
//   - PURE: stdlib only, plus os/exec strictly for READ-ONLY tmux pane listing
//     and the explicit jump action. It MAY import internal/state and
//     internal/launch. It MUST NOT import internal/console or any TUI library
//     (bubbletea / lipgloss): the NOC layer is data + actions, never rendering.
//
//   - NEVER FATAL on a single bad project: discovery prunes heavy/uninteresting
//     directories and a project that errors during collection becomes a
//     ProjectSnapshot carrying a recorded warning, not a crash.
package noc

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// AgentMailDirName is the conventional per-project sessions container. Every
// amq-squad project anchors its sessions under a directory of this name; the
// project itself is its PARENT. This is the convention validated in PR12
// (chooseProjectBaseRoot recognizes the real container by this basename).
const AgentMailDirName = ".agent-mail"

// SquadDirName is the amq-squad control directory. A team profile here is
// enough to make a project visible even before any session has created
// .agent-mail.
const SquadDirName = ".amq-squad"

const gitDirName = ".git"

// DefaultDepth bounds how deep Discover descends from each root before giving
// up. Four levels comfortably covers ~/Code/<org>/<repo> style layouts while
// keeping the walk cheap.
const DefaultDepth = 4

// prunedDirs are directory basenames that are never descended into during
// discovery: VCS metadata, dependency caches, build outputs, and the macOS
// Library tree. The .agent-mail container itself is also pruned once matched —
// we record the project and never walk a container's children (its sessions /
// agents subtree is large and uninteresting for discovery).
var prunedDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	".cache":       true,
	"Library":      true,
}

// Discover recursively walks each root and returns the PROJECT directories. A
// project is anchored by any of:
//   - a .agent-mail/ session container,
//   - a configured .amq-squad team profile,
//   - a git repository marker, which is a candidate team-home for NOC new-team.
//
// The walk prunes heavy/uninteresting directories (see prunedDirs) and never
// descends into matched .agent-mail, .amq-squad, or .git containers.
//
// depth <= 0 falls back to DefaultDepth. depth is measured relative to each
// root: the root itself is depth 0, its immediate children depth 1, and so on;
// a .agent-mail directory is considered only when its own depth is <= depth.
//
// The returned project dirs are absolute (when the root resolves to absolute),
// de-duplicated, and sorted for determinism. Roots that do not exist or cannot
// be walked are skipped silently rather than failing the whole discovery; an
// error is returned only for a non-skippable walk failure.
func Discover(roots []string, depth int) ([]string, error) {
	if depth <= 0 {
		depth = DefaultDepth
	}

	found := map[string]bool{}
	for _, root := range roots {
		if root == "" {
			continue
		}
		absRoot, err := filepath.Abs(root)
		if err != nil {
			absRoot = root
		}
		info, err := os.Stat(absRoot)
		if err != nil || !info.IsDir() {
			// A missing or non-directory root is skipped, not fatal.
			continue
		}
		if err := walkRoot(absRoot, depth, found); err != nil {
			return nil, err
		}
	}

	out := make([]string, 0, len(found))
	for dir := range found {
		out = append(out, dir)
	}
	sort.Strings(out)
	return out, nil
}

// walkRoot walks a single absolute root, recording the parent of every
// .agent-mail directory into found. It prunes by basename and by depth, and it
// does not descend into matched containers.
func walkRoot(absRoot string, depth int, found map[string]bool) error {
	return filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Unreadable entry (permissions, race): skip its subtree but keep
			// walking siblings. Discovery must never be fatal on one bad dir.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(absRoot, path)
		if relErr != nil {
			return fs.SkipDir
		}
		curDepth := relDepth(rel)

		name := d.Name()
		if !d.IsDir() {
			// Git worktrees and submodules often store .git as a file that points
			// to the real control dir. Treat it as the same candidate marker.
			if name == gitDirName && curDepth <= depth {
				found[filepath.Dir(path)] = true
			}
			return nil
		}

		if name == AgentMailDirName {
			// Record the PARENT (the project dir) when within the depth bound,
			// then never descend into the container's children.
			if curDepth <= depth {
				found[filepath.Dir(path)] = true
			}
			return fs.SkipDir
		}
		if name == SquadDirName {
			if curDepth <= depth && hasTeamProfileMarker(path) {
				found[filepath.Dir(path)] = true
			}
			return fs.SkipDir
		}
		if name == gitDirName {
			if curDepth <= depth {
				found[filepath.Dir(path)] = true
			}
			return fs.SkipDir
		}

		// Prune heavy/uninteresting trees (but never the root itself, whose
		// basename could coincidentally match — depth 0 is always walked).
		if curDepth > 0 && prunedDirs[name] {
			return fs.SkipDir
		}

		// Stop descending once we are at the depth bound: children would be at
		// depth+1, beyond what the caller asked for.
		if curDepth >= depth {
			return fs.SkipDir
		}
		return nil
	})
}

func hasTeamProfileMarker(squadDir string) bool {
	if info, err := os.Stat(filepath.Join(squadDir, "team.json")); err == nil && !info.IsDir() {
		return true
	}
	matches, err := filepath.Glob(filepath.Join(squadDir, "teams", "*.json"))
	return err == nil && len(matches) > 0
}

// relDepth returns the directory depth of a filepath.Rel result relative to its
// base. "." is depth 0; "a" is depth 1; "a/b" is depth 2.
func relDepth(rel string) int {
	if rel == "." || rel == "" {
		return 0
	}
	depth := 1
	for i := 0; i < len(rel); i++ {
		if rel[i] == filepath.Separator {
			depth++
		}
	}
	return depth
}
