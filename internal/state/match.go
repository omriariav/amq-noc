package state

import (
	"path/filepath"
	"strings"
)

// agentProcessMatcher returns a predicate that recognizes the agent binary in a
// ps args string. Ported from internal/cli/preflight.go so this package agrees
// with status/list/down about what counts as "this agent's process". Anchors on
// a token boundary so "claude-mem" does not false-match "claude".
func agentProcessMatcher(binary string) func(args string) bool {
	binary = strings.TrimSpace(binary)
	return func(args string) bool {
		if args == "" || binary == "" {
			return false
		}
		base := filepath.Base(binary)
		fields := strings.Fields(args)
		if len(fields) == 0 {
			return false
		}
		first := filepath.Base(fields[0])
		return first == base || first == binary
	}
}

// wakeProcessMatcher returns a predicate that recognizes an "amq wake" process
// for the given handle and expected root. Ported from preflight.go. The --me
// value is parsed as a token (not a substring) and the --root token, when
// present, must point at the same workstream root.
func wakeProcessMatcher(handle, expectedRoot string) func(args string) bool {
	return func(args string) bool {
		if args == "" {
			return false
		}
		if !strings.Contains(args, "amq") {
			return false
		}
		if !strings.Contains(args, "wake") {
			return false
		}
		if handle != "" {
			me, found := extractMeFromArgs(args)
			if !found || me != handle {
				return false
			}
		}
		if expectedRoot == "" {
			return true
		}
		if rootSubstringMatchesBounded(args, expectedRoot) {
			return true
		}
		if psRoot, ok := extractRootFromArgs(args); ok {
			return rootsMatch(psRoot, expectedRoot)
		}
		return true
	}
}

// rootSubstringMatchesBounded reports whether expectedRoot occurs in args
// bounded on both sides by a value boundary.
func rootSubstringMatchesBounded(args, expectedRoot string) bool {
	if expectedRoot == "" {
		return false
	}
	for i := 0; ; {
		idx := strings.Index(args[i:], expectedRoot)
		if idx < 0 {
			return false
		}
		start := i + idx
		end := start + len(expectedRoot)
		if isRootBoundary(start, args, true) && isRootBoundary(end, args, false) {
			return true
		}
		i = start + 1
	}
}

func isRootBoundary(pos int, args string, left bool) bool {
	if left {
		if pos == 0 {
			return true
		}
		switch args[pos-1] {
		case ' ', '\t', '\n', '"', '\'', '=':
			return true
		}
		return false
	}
	if pos == len(args) {
		return true
	}
	switch args[pos] {
	case ' ', '\t', '\n', '"', '\'':
		return true
	}
	return false
}

// extractMeFromArgs pulls the --me <value> or --me=<value> token out of a ps
// args string. Strict-equal on the token.
func extractMeFromArgs(args string) (string, bool) {
	fields := strings.Fields(args)
	for i, f := range fields {
		if f == "--me" {
			if i+1 < len(fields) {
				return fields[i+1], true
			}
			return "", false
		}
		if strings.HasPrefix(f, "--me=") {
			return strings.TrimPrefix(f, "--me="), true
		}
	}
	return "", false
}

// extractRootFromArgs pulls the --root <value> or --root=<value> value out of a
// ps args string, reconstructing values that contain spaces.
func extractRootFromArgs(args string) (string, bool) {
	fields := strings.Fields(args)
	for i, f := range fields {
		if f == "--root" {
			joined := joinNonFlagTokens(fields[i+1:])
			return joined, joined != ""
		}
		if strings.HasPrefix(f, "--root=") {
			head := strings.TrimPrefix(f, "--root=")
			tail := joinNonFlagTokens(fields[i+1:])
			if tail != "" {
				head = head + " " + tail
			}
			return head, true
		}
	}
	return "", false
}

func joinNonFlagTokens(fields []string) string {
	var b strings.Builder
	for i, f := range fields {
		if strings.HasPrefix(f, "--") {
			break
		}
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(f)
	}
	return b.String()
}

// rootsMatch reports whether two AMQ root paths refer to the same location,
// tolerating one being relative and the other absolute.
func rootsMatch(actual, expected string) bool {
	a := filepath.Clean(actual)
	b := filepath.Clean(expected)
	if a == b {
		return true
	}
	if filepath.IsAbs(a) && filepath.IsAbs(b) {
		return canonicalRootForMatch(a) == canonicalRootForMatch(b)
	}
	return relativeRootMatchesAbsolute(a, b)
}

func canonicalRootForMatch(root string) string {
	if root == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		return resolved
	}
	return filepath.Clean(root)
}

func relativeRootMatchesAbsolute(a, b string) bool {
	if filepath.IsAbs(a) && filepath.IsAbs(b) {
		return false
	}
	rel, abs := a, b
	if filepath.IsAbs(rel) {
		rel, abs = abs, rel
	}
	if !filepath.IsAbs(abs) {
		return false
	}
	return abs == rel || strings.HasSuffix(abs, string(filepath.Separator)+rel)
}
