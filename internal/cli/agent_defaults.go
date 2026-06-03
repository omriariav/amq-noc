package cli

import "strings"

const (
	trustModeSandboxed = "sandboxed"
	trustModeTrusted   = "trusted"
)

var codexTrustedArgs = []string{"--dangerously-bypass-approvals-and-sandbox"}

func normalizeTrustMode(mode string) (string, error) {
	switch mode {
	case "", trustModeSandboxed:
		return trustModeSandboxed, nil
	case trustModeTrusted:
		return trustModeTrusted, nil
	default:
		return "", usageErrorf("invalid trust mode %q: use sandboxed or trusted", mode)
	}
}

func defaultChildArgsForBinary(binary string) []string {
	return defaultChildArgsForBinaryWithTrust(binary, trustModeSandboxed)
}

func defaultChildArgsForBinaryWithTrust(binary, trustMode string) []string {
	switch defaultHandleFor(binary) {
	case "codex":
		if trustMode == trustModeTrusted {
			return append([]string(nil), codexTrustedArgs...)
		}
		return nil
	case "claude":
		return []string{"--permission-mode", "auto"}
	default:
		return nil
	}
}

func launchDefaultChildArgs(binary string, includeBuiltIn bool, modelArgs, extraArgs []string) []string {
	return launchDefaultChildArgsWithTrust(binary, includeBuiltIn, modelArgs, extraArgs, trustModeSandboxed)
}

func launchDefaultChildArgsWithTrust(binary string, includeBuiltIn bool, modelArgs, extraArgs []string, trustMode string) []string {
	out := []string{}
	if includeBuiltIn {
		out = append(out, defaultChildArgsForBinaryWithTrust(binary, trustMode)...)
	}
	out = append(out, modelArgs...)
	out = append(out, extraArgs...)
	return out
}

// validateTrustCombination rejects user input that contradicts the trust mode.
// trusted plus --no-default-args is incoherent: trust would prepend the bypass
// flag while no-default-args asks to omit defaults. sandboxed plus a manually
// supplied bypass via --codex-args is also rejected to keep the trust boundary
// the single, visible source of truth.
func validateTrustCombination(trustMode string, trustExplicit, noDefaultArgs bool, binaryArgs map[string][]string) error {
	if trustMode == trustModeTrusted && noDefaultArgs {
		return usageErrorf("--trust trusted cannot be combined with --no-default-args; trusted prepends the Codex permission flag, --no-default-args opts out of defaults")
	}
	if trustMode != trustModeTrusted {
		for _, arg := range binaryArgs["codex"] {
			if arg == "--dangerously-bypass-approvals-and-sandbox" {
				if trustExplicit {
					return usageErrorf("--trust sandboxed cannot be combined with --codex-args containing --dangerously-bypass-approvals-and-sandbox; pass --trust trusted instead")
				}
				return usageErrorf("--codex-args contains --dangerously-bypass-approvals-and-sandbox; pass --trust trusted instead so the trust boundary is explicit")
			}
		}
	}
	return nil
}

// modelArgsForBinary returns the native model-selection flag for the binary.
// codex and claude both accept --model <name>; unknown binaries get nothing.
func modelArgsForBinary(binary, model string) []string {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil
	}
	switch normalizedAgentBinary(binary) {
	case "codex", "claude":
		return []string{"--model", model}
	default:
		return nil
	}
}

func ensureDefaultChildArgs(binary string, childArgs []string) []string {
	return ensureLeadingChildArgs(defaultChildArgsForBinary(binary), childArgs)
}

func ensureLeadingChildArgs(defaultArgs, childArgs []string) []string {
	if len(defaultArgs) == 0 || hasLeadingArgs(defaultArgs, childArgs) {
		return childArgs
	}
	prefixLen := leadingDefaultPrefixLen(defaultArgs, childArgs)
	out := append([]string(nil), defaultArgs...)
	return append(out, childArgs[prefixLen:]...)
}

func hasLeadingDefaultChildArgs(binary string, childArgs []string) bool {
	return hasLeadingArgs(defaultChildArgsForBinary(binary), childArgs)
}

func hasLeadingArgs(defaultArgs, childArgs []string) bool {
	if len(defaultArgs) == 0 || len(childArgs) < len(defaultArgs) {
		return false
	}
	for i := range defaultArgs {
		if childArgs[i] != defaultArgs[i] {
			return false
		}
	}
	return true
}

func leadingDefaultPrefixLen(defaultArgs, childArgs []string) int {
	max := len(defaultArgs)
	if len(childArgs) < max {
		max = len(childArgs)
	}
	for n := max; n > 0; n-- {
		match := true
		for i := 0; i < n; i++ {
			if childArgs[i] != defaultArgs[i] {
				match = false
				break
			}
		}
		if match {
			return n
		}
	}
	return 0
}

func shouldAppendBootstrap(binary string, childArgs []string) bool {
	return shouldAppendBootstrapWithDefaults(childArgs, defaultChildArgsForBinary(binary))
}

func shouldAppendBootstrapWithDefaults(childArgs []string, defaultArgs []string) bool {
	if len(childArgs) == 0 {
		return true
	}
	if len(childArgs) != len(defaultArgs) {
		return false
	}
	for i := range childArgs {
		if childArgs[i] != defaultArgs[i] {
			return false
		}
	}
	return true
}
