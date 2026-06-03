package cli

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

func parseBinaryArgFlags(codexRaw, claudeRaw string) (map[string][]string, error) {
	out := map[string][]string{}
	if strings.TrimSpace(codexRaw) != "" {
		args, err := parseAgentArgs(codexRaw)
		if err != nil {
			return nil, fmt.Errorf("parse --codex-args: %w", err)
		}
		if len(args) > 0 {
			out["codex"] = args
		}
	}
	if strings.TrimSpace(claudeRaw) != "" {
		args, err := parseAgentArgs(claudeRaw)
		if err != nil {
			return nil, fmt.Errorf("parse --claude-args: %w", err)
		}
		if len(args) > 0 {
			out["claude"] = args
		}
	}
	return out, nil
}

func parseAgentArgs(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	args := []string{}
	var b strings.Builder
	var quote rune
	tokenStarted := false
	escaped := false
	for _, r := range raw {
		if escaped {
			b.WriteRune(r)
			tokenStarted = true
			escaped = false
			continue
		}
		if quote == '\'' {
			if r == '\'' {
				quote = 0
			} else {
				b.WriteRune(r)
				tokenStarted = true
			}
			continue
		}
		if r == '\\' {
			escaped = true
			tokenStarted = true
			continue
		}
		if quote == '"' {
			if r == '"' {
				quote = 0
			} else {
				b.WriteRune(r)
				tokenStarted = true
			}
			continue
		}
		switch {
		case r == '\'' || r == '"':
			quote = r
			tokenStarted = true
		case unicode.IsSpace(r):
			if tokenStarted {
				args = append(args, b.String())
				b.Reset()
				tokenStarted = false
			}
		default:
			b.WriteRune(r)
			tokenStarted = true
		}
	}
	if escaped {
		return nil, fmt.Errorf("trailing escape")
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote")
	}
	if tokenStarted {
		args = append(args, b.String())
	}
	return args, nil
}

func binaryArgsFor(binary string, binaryArgs map[string][]string) []string {
	if len(binaryArgs) == 0 {
		return nil
	}
	args := binaryArgs[normalizedAgentBinary(binary)]
	return append([]string(nil), args...)
}

func mergeBinaryArgs(base, extra map[string][]string) map[string][]string {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	out := map[string][]string{}
	for k, args := range base {
		key := strings.ToLower(strings.TrimSpace(k))
		if key == "" {
			continue
		}
		out[key] = append(out[key], args...)
	}
	for k, args := range extra {
		key := strings.ToLower(strings.TrimSpace(k))
		if key == "" {
			continue
		}
		out[key] = append(out[key], args...)
	}
	return out
}

func joinedAgentArgs(args []string) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func formatBinaryArgs(binaryArgs map[string][]string) string {
	if len(binaryArgs) == 0 {
		return ""
	}
	keys := make([]string, 0, len(binaryArgs))
	for k := range binaryArgs {
		if len(binaryArgs[k]) > 0 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+": "+joinedAgentArgs(binaryArgs[k]))
	}
	return strings.Join(parts, ", ")
}
