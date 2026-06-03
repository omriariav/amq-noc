package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// seedNow is the clock used for the provenance frontmatter. Overridable
// from tests so generated_at comparisons stay deterministic.
var seedNow = func() time.Time { return time.Now().UTC() }

// seedGhRun shells out to `gh` for issue resolution. Overridable from
// tests so the matrix can run without a real gh binary. Stderr is folded
// into the returned error so auth/network failures are actionable for
// the user.
var seedGhRun = func(args ...string) ([]byte, error) {
	cmd := exec.Command("gh", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	return out, nil
}

// seedReadFile reads a file body for the file: source. Overridable from
// tests so error cases can be exercised without filesystem setup.
var seedReadFile = func(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// resolveSeed turns a --seed-from reference into a brief body (no
// provenance frontmatter; buildSeedBrief prepends that). The reference
// shape is <kind>:<rest> where kind is one of: file, issue, gh, claude,
// codex, transcript. claude/codex are intentionally rejected in 8A.
func resolveSeed(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("--seed-from requires a value (file:..., issue:..., or gh:owner/repo#N)")
	}
	idx := strings.IndexByte(ref, ':')
	if idx <= 0 {
		return "", fmt.Errorf("--seed-from %q: missing kind prefix; want file:, issue:, or gh:", ref)
	}
	kind := ref[:idx]
	rest := ref[idx+1:]
	switch kind {
	case "file":
		return resolveSeedFile(rest)
	case "issue":
		return resolveSeedIssue("", rest)
	case "gh":
		return resolveSeedGh(rest)
	case "claude", "codex":
		return "", fmt.Errorf("--seed-from %s: %s extraction is not implemented in this slice", ref, kind)
	case "transcript":
		return "", fmt.Errorf("--seed-from %s: transcript: source is not implemented in this slice", ref)
	default:
		return "", fmt.Errorf("--seed-from %s: unknown kind %q", ref, kind)
	}
}

func resolveSeedFile(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("--seed-from file:: empty path")
	}
	data, err := seedReadFile(path)
	if err != nil {
		return "", fmt.Errorf("--seed-from file:%s: %w", path, err)
	}
	return string(data), nil
}

func resolveSeedIssue(repo, number string) (string, error) {
	number = strings.TrimSpace(number)
	if number == "" {
		return "", fmt.Errorf("--seed-from issue:: missing number")
	}
	if _, err := strconv.Atoi(number); err != nil {
		return "", fmt.Errorf("--seed-from issue:%s: not a number", number)
	}
	args := []string{"issue", "view", number, "--json", "title,body,url"}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	out, err := seedGhRun(args...)
	if err != nil {
		ref := "issue:" + number
		if repo != "" {
			ref = "gh:" + repo + "#" + number
		}
		return "", fmt.Errorf("--seed-from %s: gh: %w", ref, err)
	}
	var parsed struct {
		Title string `json:"title"`
		Body  string `json:"body"`
		URL   string `json:"url"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return "", fmt.Errorf("--seed-from issue:%s: parse gh output: %w", number, err)
	}
	return renderIssueBody(parsed.Title, parsed.URL, parsed.Body), nil
}

func resolveSeedGh(rest string) (string, error) {
	rest = strings.TrimSpace(rest)
	hash := strings.IndexByte(rest, '#')
	if hash <= 0 || hash == len(rest)-1 {
		return "", fmt.Errorf("--seed-from gh:%s: want owner/repo#N", rest)
	}
	repo := rest[:hash]
	number := rest[hash+1:]
	if !strings.Contains(repo, "/") {
		return "", fmt.Errorf("--seed-from gh:%s: repo must look like owner/repo", rest)
	}
	return resolveSeedIssue(repo, number)
}

func renderIssueBody(title, url, body string) string {
	var b strings.Builder
	if title != "" {
		b.WriteString("# ")
		b.WriteString(title)
		b.WriteString("\n\n")
	}
	if url != "" {
		b.WriteString("URL: ")
		b.WriteString(url)
		b.WriteString("\n\n")
	}
	b.WriteString(strings.TrimRight(body, "\n"))
	b.WriteString("\n")
	return b.String()
}

// buildSeedBrief composes the provenance frontmatter and the resolved body.
// Frontmatter is intentionally minimal: source, generated_at, generator.
// The body is appended verbatim after the closing `---\n\n` so file:
// sources keep byte-for-byte fidelity (including leading newlines and the
// absence of a trailing newline).
func buildSeedBrief(ref, body string, now time.Time) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("source: ")
	b.WriteString(ref)
	b.WriteString("\n")
	b.WriteString("generated_at: ")
	b.WriteString(now.UTC().Format(time.RFC3339))
	b.WriteString("\n")
	b.WriteString("generator: deterministic\n")
	b.WriteString("---\n\n")
	b.WriteString(body)
	return b.String()
}

// writeSeedBrief writes the seeded brief to the resolved path.
//
// force=false: the target is opened with O_CREATE|O_EXCL so first-writer-
// wins. A second concurrent caller observes os.ErrExist and returns the
// existing-brief error without touching the file.
//
// force=true: the target is replaced via a temp-file + rename so a
// partially-written file never appears at the brief path.
func writeSeedBrief(teamHome, session, content string, force bool) (string, error) {
	path := briefPath(teamHome, session)
	if path == "" {
		return "", fmt.Errorf("seed brief: team-home or session is empty (cannot resolve target path)")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return path, fmt.Errorf("create briefs dir: %w", err)
	}
	if !force {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				return path, fmt.Errorf("brief %s already exists; rerun with --force to overwrite", path)
			}
			return path, fmt.Errorf("create brief %s: %w", path, err)
		}
		defer f.Close()
		if _, err := f.WriteString(content); err != nil {
			return path, fmt.Errorf("write brief %s: %w", path, err)
		}
		return path, nil
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return path, fmt.Errorf("create seed tmp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return path, fmt.Errorf("write seed tmp: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return path, fmt.Errorf("chmod seed tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return path, fmt.Errorf("close seed tmp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return path, fmt.Errorf("rename seed tmp: %w", err)
	}
	cleanup = false
	return path, nil
}
