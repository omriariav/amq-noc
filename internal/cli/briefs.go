package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-noc/internal/rules"
)

// briefsDirName is the per-team-home directory holding workstream briefs.
const briefsDirName = "briefs"

// briefPath returns the absolute path to the brief for a (teamHome, session)
// pair. teamHome is normalized to an absolute path so callers passing a
// relative argument (e.g. "." from a current-cwd fallback) still produce
// the absolute path bootstrap names in the priming prompt. Returns "" when
// either input is empty.
func briefPath(teamHome, session string) string {
	teamHome = strings.TrimSpace(teamHome)
	session = strings.TrimSpace(session)
	if teamHome == "" || session == "" {
		return ""
	}
	abs, err := filepath.Abs(teamHome)
	if err != nil {
		abs = filepath.Clean(teamHome)
	}
	return filepath.Join(abs, ".amq-squad", briefsDirName, session+".md")
}

// resolveBriefHome returns the directory under which the active brief lives,
// or "" when no brief should be resolved. The rule, shared by bootstrap and
// the live-launch ensure step so the two never disagree: prefer an explicit
// teamHome; fall back to cwd only if cwd contains a team-rules.md (i.e.
// cwd is an amq-squad project). Otherwise return "" so direct launches
// against unconfigured cwds stay read-only for project files.
func resolveBriefHome(teamHome, cwd string) string {
	if strings.TrimSpace(teamHome) != "" {
		return teamHome
	}
	if cwd == "" {
		return ""
	}
	if _, err := os.Stat(rules.Path(cwd)); err == nil {
		return cwd
	}
	return ""
}

// briefStubFirstLine is the first meaningful (non-heading, non-blank) line of
// the generated stub template. It is session-independent prose, so the status
// board can recognize an untouched stub by matching the brief's first
// meaningful line against it. Kept beside briefStubContent so the two never
// drift: briefStubContent emits this exact line right after the "# <session>"
// heading.
const briefStubFirstLine = "Use this brief to capture the active workstream's goal, scope, and"

// briefStubContent returns the markdown body used when seeding a new brief
// for session under teamHome. Existing brief files are preserved by
// ensureBriefStub and never re-templated through this content.
func briefStubContent(session string) string {
	if session == "" {
		session = "(unknown)"
	}
	return "# " + session + "\n" +
		"\n" +
		briefStubFirstLine + "\n" +
		"pointers to source-of-truth issues, PRs, or docs. Agents read it at\n" +
		"session start; team-rules.md links to this convention.\n" +
		"\n" +
		"## Goal\n" +
		"\n" +
		"TODO: one-sentence description of what this workstream ships.\n" +
		"\n" +
		"## Scope\n" +
		"\n" +
		"TODO: in-scope vs out-of-scope items.\n" +
		"\n" +
		"## Status\n" +
		"\n" +
		"TODO: current step / blocker / next action.\n"
}

// ensureBriefStub writes a brief stub for (teamHome, session) when no file
// already exists at the resolved path. Returns the resolved path, whether a
// new file was created, and any error. Returns ("", false, nil) when the
// inputs cannot resolve a path (callers can ignore the result silently and
// skip the bootstrap brief line).
//
// Existing brief content is preserved verbatim: creation uses
// O_CREATE|O_EXCL so a second concurrent caller observes os.ErrExist and
// returns created=false rather than racing to overwrite. The first writer
// genuinely wins.
func ensureBriefStub(teamHome, session string) (string, bool, error) {
	path := briefPath(teamHome, session)
	if path == "" {
		return "", false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return path, false, fmt.Errorf("create briefs dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return path, false, nil
		}
		return path, false, fmt.Errorf("create brief %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(briefStubContent(session)); err != nil {
		return path, false, fmt.Errorf("write brief %s: %w", path, err)
	}
	return path, true, nil
}
