package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-noc/internal/team"
)

type bootstrapWorkstream struct {
	Name        string
	Handles     string
	LastTouched string
}

func defaultWorkstreamName(projectDir string) string {
	base := filepath.Base(projectDir)
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = "project"
	}
	return sanitizeWorkstreamName(base)
}

func resolveWorkstreamName(projectDir, requested string, explicit bool) (string, error) {
	name := strings.TrimSpace(requested)
	if explicit && name == "" {
		return "", fmt.Errorf("session name cannot be empty")
	}
	if name == "" {
		return defaultWorkstreamName(projectDir), nil
	}
	if err := validateWorkstreamName(name); err != nil {
		return "", err
	}
	return name, nil
}

// resolveTeamWorkstreamName resolves the live workstream session for a team
// invocation. With no explicit flag and no request, the resolution order is:
//
//  1. a shared, non-legacy session inferred across members -> use it silently.
//  2. else the pinned t.Workstream, when set -> use it AND emit a deprecation
//     notice. This pin is a DEPRECATED SHIM (removal in 2.1): it is still read
//     here, clearly marked, but member-session inference now wins over it.
//  3. else the sanitized project basename.
//
// The shim notice is emitted via quietNotice (silenced by --quiet) ONLY when
// tier 2 is the actual resolved source; explicit/request/inference paths never
// print it.
func resolveTeamWorkstreamName(t team.Team, requested string, explicit bool) (string, error) {
	name := strings.TrimSpace(requested)
	if explicit {
		return resolveWorkstreamName(t.Project, name, true)
	}
	if name != "" {
		return resolveWorkstreamName(t.Project, name, false)
	}
	// Tier 1: member-session inference wins over the pin. No notice.
	if inferred := inferredSharedMemberSession(t); inferred != "" {
		return resolveWorkstreamName(t.Project, inferred, false)
	}
	// Tier 2: the deprecated pin shim. Still read, but emit a notice.
	if pinned := strings.TrimSpace(t.Workstream); pinned != "" {
		resolved, err := resolveWorkstreamName(t.Project, pinned, false)
		if err != nil {
			return "", err
		}
		quietNotice("notice: using session %q from the deprecated 'workstream' default pinned in team.json. "+
			"This pin is deprecated and will be removed in 2.1; pass --session %s explicitly (or re-init the team) instead.\n",
			resolved, resolved)
		return resolved, nil
	}
	// Tier 3: sanitized project basename.
	return resolveWorkstreamName(t.Project, defaultWorkstreamName(t.Project), false)
}

// inferredSharedMemberSession returns the workstream session shared by every
// member when that session is non-legacy (i.e. not merely a copy of a member's
// role or handle), or "" when no such shared session exists. Unlike the former
// defaultTeamWorkstreamName, it does NOT fall back to the project basename, so
// callers can order the inference, pin, and basename tiers explicitly.
func inferredSharedMemberSession(t team.Team) string {
	unique := ""
	shared := true
	hasNonLegacyMember := false
	for _, m := range t.Members {
		session := strings.TrimSpace(m.Session)
		if session == "" {
			shared = false
			continue
		}
		if unique == "" {
			unique = session
		} else if session != unique {
			shared = false
		}
		handle := memberHandle(m)
		if session != m.Role && session != handle {
			hasNonLegacyMember = true
		}
	}
	if shared && unique != "" && hasNonLegacyMember {
		return unique
	}
	return ""
}

func validateWorkstreamName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("session name cannot be empty")
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			continue
		}
		return fmt.Errorf("invalid session name %q: AMQ session names allow lowercase a-z, 0-9, - and _ only; replace dots, spaces, or uppercase with -", name)
	}
	return nil
}

func sanitizeWorkstreamName(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(s) {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "project"
	}
	return out
}

func canonicalP2PThread(a, b string) string {
	handles := []string{a, b}
	sort.Strings(handles)
	return "p2p/" + handles[0] + "__" + handles[1]
}

func flagWasSet(fs *flag.FlagSet, name string) bool {
	seen := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			seen = true
		}
	})
	return seen
}

// teamWorkstreamExistsOrRestorable reports whether the named workstream is
// already "taken" for this team — the condition `up` refuses by default and
// fork treats as a valid source. Two signals count: the AMQ root for the
// session already exists on disk (mailbox/agent state) OR at least one
// configured member has a restorable launch record matching the session. The
// returned root is the first existing AMQ root found (empty when the only
// signal is a restorable record), purely for human-facing messages. No
// message bodies are inspected.
func teamWorkstreamExistsOrRestorable(t team.Team, workstream string) (bool, string, error) {
	if exists, root, err := teamWorkstreamExists(t, workstream); err != nil {
		return false, "", err
	} else if exists {
		return true, root, nil
	}
	for _, m := range t.Members {
		baseRoot, err := scanBaseRootForProject(m.EffectiveCWD(t.Project))
		if err != nil || baseRoot == "" {
			continue
		}
		if _, found := findMemberRestoreRecord(baseRoot, t.Project, m.EffectiveCWD(t.Project), workstream, m.Role, m.Handle); found {
			return true, "", nil
		}
	}
	return false, "", nil
}

func teamWorkstreamExists(t team.Team, workstream string) (bool, string, error) {
	seen := map[string]bool{}
	for _, m := range t.Members {
		cwd := m.EffectiveCWD(t.Project)
		handle := memberHandle(m)
		key := cwd + "\x00" + handle
		if seen[key] {
			continue
		}
		seen[key] = true
		root, err := resolveAMQRootInDir(cwd, "", workstream, handle)
		if err != nil {
			return false, "", err
		}
		if _, err := os.Stat(root); err == nil {
			return true, root, nil
		} else if !os.IsNotExist(err) {
			return false, "", err
		}
	}
	return false, "", nil
}

func siblingWorkstreamSummaries(currentRoot, currentSession string) []bootstrapWorkstream {
	if currentRoot == "" || currentSession == "" {
		return nil
	}
	base := filepath.Dir(currentRoot)
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var out []bootstrapWorkstream
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == currentSession {
			continue
		}
		sessionRoot := filepath.Join(base, e.Name())
		handles := workstreamHandles(sessionRoot)
		if len(handles) == 0 {
			continue
		}
		out = append(out, bootstrapWorkstream{
			Name:        e.Name(),
			Handles:     strings.Join(handles, ", "),
			LastTouched: formatOptionalTime(latestWorkstreamModTime(sessionRoot)),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func workstreamHandles(sessionRoot string) []string {
	agentEntries, err := os.ReadDir(filepath.Join(sessionRoot, "agents"))
	if err != nil {
		return nil
	}
	var handles []string
	for _, e := range agentEntries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(sessionRoot, "agents", e.Name(), "inbox")); err == nil {
			handles = append(handles, e.Name())
		}
	}
	sort.Strings(handles)
	return handles
}

func latestWorkstreamModTime(root string) time.Time {
	var latest time.Time
	observe := func(path string) {
		info, err := os.Stat(path)
		if err == nil && info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	observe(root)
	agentsRoot := filepath.Join(root, "agents")
	observe(agentsRoot)
	agentEntries, err := os.ReadDir(agentsRoot)
	if err != nil {
		return latest
	}
	for _, e := range agentEntries {
		if !e.IsDir() {
			continue
		}
		agentRoot := filepath.Join(agentsRoot, e.Name())
		observe(agentRoot)
		for _, name := range []string{"inbox", "outbox", "acks", "receipts", "dlq"} {
			observe(filepath.Join(agentRoot, name))
		}
	}
	return latest
}

func formatOptionalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04")
}
