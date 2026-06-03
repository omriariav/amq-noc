// Package rules manages .amq-squad/team-rules.md, the single source of
// truth for team-wide norms and approval flow, and keeps CLAUDE.md +
// AGENTS.md in sync with it.
//
// Design rules:
//   - team-rules.md is the only authoritative source. No structured
//     approver map, no parallel config fields. Rules are prose.
//   - CLAUDE.md / AGENTS.md get a "managed block" delimited by markers.
//     Content outside the markers belongs to the user and is never touched.
//   - Sync never writes on its own. The caller previews, then opts in.
package rules

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	FileName = "team-rules.md"

	BeginMarker = "<!-- amq-squad:managed:begin -->"
	EndMarker   = "<!-- amq-squad:managed:end -->"

	ClaudeFile = "CLAUDE.md"
	AgentsFile = "AGENTS.md"
)

// Path returns the team-rules.md path for the given project directory.
func Path(projectDir string) string {
	return filepath.Join(projectDir, ".amq-squad", FileName)
}

// StubContent is what we seed team-rules.md with on first `rules init`.
const StubContent = `# Team Rules

Shared norms and workflow for this project's agent squad. Every agent reads
this file via their priming prompt regardless of binary.

## Workflow

- TODO: describe how work flows from intent to shipped (e.g. "All code
  changes ship via a pull request").

## Approvals

- TODO: list required approvals (e.g. "Every PR needs CTO approval before
  merge").

## Communication

- TODO: describe how agents should talk to each other (channels,
  escalation, when to page a peer).
`

// Read returns the team-rules.md contents. Returns os.ErrNotExist if the
// file isn't there.
func Read(projectDir string) (string, error) {
	b, err := os.ReadFile(Path(projectDir))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// EnsureStub writes a stub team-rules.md if one does not already exist.
// Returns true if it wrote a new file.
func EnsureStub(projectDir string) (bool, error) {
	return Ensure(projectDir, StubContent)
}

// Ensure writes team-rules.md with content if one does not already exist.
// Returns true if it wrote a new file.
func Ensure(projectDir, content string) (bool, error) {
	p := Path(projectDir)
	if _, err := os.Stat(p); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := Write(projectDir, content); err != nil {
		return false, err
	}
	return true, nil
}

// Write writes team-rules.md with content, creating .amq-squad if needed.
func Write(projectDir, content string) error {
	p := Path(projectDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return err
	}
	return nil
}

// SyncPlan describes what sync would do for a single target file.
type SyncPlan struct {
	Target    string // absolute path
	Basename  string // "CLAUDE.md" or "AGENTS.md"
	Before    string // current file contents ("" if missing)
	After     string // desired contents
	Adopting  bool   // true = first-time wrap of existing content
	Creating  bool   // true = file doesn't exist yet, will be created
	Unchanged bool   // true = already in desired state
}

// Plan builds a SyncPlan for CLAUDE.md and AGENTS.md under projectDir. The
// managed block is a small pointer stub that references team-rules.md, the
// agent's role.md, and the active workstream pointer; the rulesBody argument
// is accepted for signature compatibility but never copied into root
// instruction files.
func Plan(projectDir, rulesBody string) ([]SyncPlan, error) {
	_ = rulesBody
	managed := buildManagedBlock("")
	plans := make([]SyncPlan, 0, 2)
	for _, name := range []string{ClaudeFile, AgentsFile} {
		p := filepath.Join(projectDir, name)
		before, existed, err := readIfExists(p)
		if err != nil {
			return nil, err
		}
		after, adopting := renderTarget(before, existed, managed)
		plans = append(plans, SyncPlan{
			Target:    p,
			Basename:  name,
			Before:    before,
			After:     after,
			Adopting:  adopting,
			Creating:  !existed,
			Unchanged: before == after,
		})
	}
	return plans, nil
}

// Apply writes each plan whose After differs from Before. Returns the
// number of files touched.
func Apply(plans []SyncPlan) (int, error) {
	n := 0
	for _, p := range plans {
		if p.Unchanged {
			continue
		}
		if err := verifyPlanCurrent(p); err != nil {
			return n, err
		}
		mode, err := targetMode(p.Target, 0o644)
		if err != nil {
			return n, err
		}
		if err := atomicWriteFile(p.Target, []byte(p.After), mode); err != nil {
			return n, fmt.Errorf("write %s: %w", p.Target, err)
		}
		n++
	}
	return n, nil
}

func verifyPlanCurrent(p SyncPlan) error {
	current, existed, err := readIfExists(p.Target)
	if err != nil {
		return fmt.Errorf("read current %s: %w", p.Target, err)
	}
	if p.Creating {
		if existed {
			return fmt.Errorf("%s changed since sync plan was created", p.Target)
		}
		return nil
	}
	if !existed {
		return fmt.Errorf("%s changed since sync plan was created", p.Target)
	}
	if current != p.Before {
		return fmt.Errorf("%s changed since sync plan was created", p.Target)
	}
	return nil
}

func targetMode(path string, fallback os.FileMode) (os.FileMode, error) {
	info, err := os.Stat(path)
	if err == nil {
		return info.Mode().Perm(), nil
	}
	if os.IsNotExist(err) {
		return fallback, nil
	}
	return 0, fmt.Errorf("stat %s: %w", path, err)
}

func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmp, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	return syncDir(dir)
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Sync(); err != nil {
		return err
	}
	return nil
}

// buildManagedBlock returns the canonical pointer-stub managed block. The
// block intentionally does not embed team-rules.md; agents follow the
// pointer at runtime so the root instruction file stays small and the
// source of truth for norms stays in one place. The brief line names the
// per-session convention; bootstrap injects the exact resolved path at
// launch time.
func buildManagedBlock(_ string) string {
	return BeginMarker + "\n" +
		"This project uses amq-squad for agent team coordination.\n" +
		"\n" +
		"- **Team norms:** `.amq-squad/team-rules.md`\n" +
		"- **Your role:** when launched via amq-squad, `<your-agent-dir>/role.md` carries your persona.\n" +
		"- **Active brief:** read `.amq-squad/briefs/<session>.md` for the current workstream (bootstrap names the exact path).\n" +
		"\n" +
		"These files are the source of truth. Do not duplicate their content here.\n" +
		EndMarker
}

// renderTarget returns the desired file contents and whether this is a
// first-time adoption of pre-existing user content.
func renderTarget(before string, existed bool, managed string) (string, bool) {
	if !existed {
		return managed + "\n", false
	}
	if hasMarkers(before) {
		return replaceManagedBlock(before, managed), false
	}
	// Adopt: keep existing content as user region, append managed block.
	trimmed := strings.TrimRight(before, "\n")
	if trimmed == "" {
		return managed + "\n", true
	}
	return trimmed + "\n\n" + managed + "\n", true
}

func hasMarkers(s string) bool {
	return strings.Contains(s, BeginMarker) && strings.Contains(s, EndMarker)
}

func replaceManagedBlock(existing, managed string) string {
	beginIdx := strings.Index(existing, BeginMarker)
	endIdx := strings.Index(existing, EndMarker)
	if beginIdx < 0 || endIdx < 0 || endIdx < beginIdx {
		// Shouldn't happen: hasMarkers guards this. Fall back to append.
		return strings.TrimRight(existing, "\n") + "\n\n" + managed + "\n"
	}
	endIdx += len(EndMarker)
	return existing[:beginIdx] + managed + existing[endIdx:]
}

func readIfExists(p string) (string, bool, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return string(b), true, nil
}
