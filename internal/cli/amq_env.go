package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type amqEnv struct {
	SchemaVersion int               `json:"schema_version"`
	AMQVersion    string            `json:"amq_version"`
	Root          string            `json:"root"`
	BaseRoot      string            `json:"base_root"`
	SessionName   string            `json:"session_name"`
	InSession     bool              `json:"in_session"`
	Me            string            `json:"me"`
	Project       string            `json:"project"`
	RootSource    string            `json:"root_source"`
	Peers         map[string]string `json:"peers"`
}

func resolveAMQEnv(rootFlag, session, handle string) (amqEnv, error) {
	return resolveAMQEnvInDir("", rootFlag, session, handle)
}

func resolveAMQEnvInDir(cwd, rootFlag, session, handle string) (amqEnv, error) {
	args := []string{"env", "--json"}
	if handle != "" {
		args = append(args, "--me", handle)
	}
	// amq treats --session NAME as shorthand for --root .agent-mail/<name>
	// and refuses both together. Prefer --session when both are supplied,
	// since session is the more specific selector. Callers like restore
	// historically passed both; we filter at the boundary so any other
	// future caller cannot trip the same trap.
	//
	// When BOTH are supplied (e.g. an operator typed `agent up --session X
	// --root Y` directly), warn so the dropped --root is visible: silently
	// launching into the session-derived root when the operator passed an
	// explicit conflicting --root would be worse than the old failure.
	if session != "" && rootFlag != "" {
		fmt.Fprintf(os.Stderr, "warning: amq-squad: --session %q implies --root .agent-mail/%s; ignoring conflicting --root %q.\n", session, session, rootFlag)
	}
	if session != "" {
		args = append(args, "--session", session)
	} else if rootFlag != "" {
		args = append(args, "--root", rootFlag)
	}
	cmd := exec.Command("amq", args...)
	// Strip AMQ identity vars unconditionally: the operator has already passed
	// --root/--session/--me on the wire, and a stale AM_ROOT/AM_ME from a
	// previous shell session must not silently override them.
	cmd.Env = envWithoutAMQIdentity(os.Environ())
	if cwd != "" {
		cmd.Dir = cwd
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return amqEnv{}, fmt.Errorf("amq env: %w: %s", err, msg)
		}
		return amqEnv{}, fmt.Errorf("amq env: %w", err)
	}
	var parsed amqEnv
	if err := json.Unmarshal(out, &parsed); err != nil {
		return amqEnv{}, fmt.Errorf("parse amq env output: %w", err)
	}
	if parsed.Root == "" {
		return amqEnv{}, fmt.Errorf("amq env returned empty root")
	}
	if parsed.BaseRoot == "" {
		parsed.BaseRoot = parsed.Root
	}
	if parsed.Me == "" {
		parsed.Me = handle
	}
	if parsed.SessionName == "" {
		parsed.SessionName = session
	}
	return parsed, nil
}

func absoluteAMQRoot(cwd, root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	if filepath.IsAbs(root) {
		return filepath.Clean(root)
	}
	base := strings.TrimSpace(cwd)
	if base == "" {
		abs, err := filepath.Abs(root)
		if err == nil {
			return abs
		}
		return filepath.Clean(root)
	}
	return filepath.Clean(filepath.Join(base, root))
}

func envWithoutAMQIdentity(env []string) []string {
	remove := map[string]bool{
		"AM_ROOT":      true,
		"AM_BASE_ROOT": true,
		"AM_ME":        true,
	}
	out := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || !remove[key] {
			out = append(out, entry)
		}
	}
	return out
}

func scanBaseRootForProject(projectDir string) (string, error) {
	env, err := resolveAMQEnvInDir(projectDir, "", "", "amq-squad")
	if err != nil {
		return "", err
	}
	return chooseProjectBaseRoot(projectDir, env), nil
}

// chooseProjectBaseRoot picks the directory the board/console should scan for
// sessions (it walks <chosen>/<session>/agents). It exists because `amq env`
// reports an UNRELIABLE base_root when amq believes it is "in a session": in
// that mode base_root points at the PARENT (the project dir, or "."/".."),
// while the real sessions container — the conventional `.agent-mail` directory
// — is reported in `root`. Trusting base_root then makes the board scan the
// wrong directory and falsely report "no sessions" when sessions exist.
//
// The fix is to recognize the real container by its name: in every observed
// layout the correct directory is the candidate whose final path element is
// `.agent-mail`. We try base_root, then root, then the conventional
// <projectDir>/.agent-mail, resolving each to an absolute path (relative ones
// joined against projectDir), and return the FIRST whose basename is
// `.agent-mail` AND which is an existing directory.
//
// When none qualifies (amq missing, a custom non-.agent-mail root, or nothing
// on disk yet) we fall back to abs(base_root) — or abs(root) when base_root is
// empty — so graceful degradation downstream is preserved exactly as before.
func chooseProjectBaseRoot(projectDir string, env amqEnv) string {
	abs := func(p string) string {
		if p == "" {
			return ""
		}
		if filepath.IsAbs(p) {
			return filepath.Clean(p)
		}
		return filepath.Join(projectDir, p)
	}

	candidates := []string{
		abs(env.BaseRoot),
		abs(env.Root),
		abs(filepath.Join(projectDir, defaultBaseRootName)),
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if filepath.Base(c) != defaultBaseRootName {
			continue
		}
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}

	// No `.agent-mail` container found: preserve graceful degradation by
	// returning the resolved base_root (or root when base_root is empty).
	if fallback := abs(env.BaseRoot); fallback != "" {
		return fallback
	}
	return abs(env.Root)
}
