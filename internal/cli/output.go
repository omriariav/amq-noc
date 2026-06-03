package cli

import (
	"fmt"
	"os"
	"strings"
)

// outputPolicy describes the resolved global output controls for a single
// CLI invocation. Color is the final yes/no decision after NO_COLOR /
// --color / TTY detection. Quiet and Verbose are mutually exclusive at
// parse time, so at most one is true.
type outputPolicy struct {
	Quiet   bool
	Verbose bool
	Color   bool
}

// currentOutputPolicy is set by Run() before dispatch so subcommand
// handlers can read the resolved policy without threading it through every
// function signature. Tests that drive a handler directly should call
// setOutputPolicyForTest, which restores the previous value on cleanup.
var currentOutputPolicy outputPolicy

func outputPolicyCurrent() outputPolicy { return currentOutputPolicy }

// outputIsTTY and outputGetenv are seams so tests can deterministically
// drive auto-color resolution without depending on the actual stdout fd
// or process environment. Production uses real stdout + os.Getenv.
var (
	outputIsTTY  = defaultStdoutIsTTY
	outputGetenv = os.Getenv
)

func defaultStdoutIsTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// parseGlobalFlags peels --quiet, --verbose, and --color from args. The
// scan stops at the first "--" so subcommand-level child args remain
// untouched (e.g. `launch ... -- --color always` is the child binary's
// flag, not ours). Returns the remaining args plus a resolved policy.
//
// Both `--color value` (separate token) and `--color=value` (joined) are
// accepted. Anything else is left for the per-command flag set to handle
// or reject.
func parseGlobalFlags(args []string) ([]string, outputPolicy, error) {
	var (
		quiet    bool
		verbose  bool
		colorVal = "auto"
		colorSet bool
	)
	out := make([]string, 0, len(args))
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "--" {
			// Past this boundary belongs to a child binary; leave alone.
			out = append(out, args[i:]...)
			break
		}
		switch {
		case a == "--quiet":
			quiet = true
			i++
		case a == "--verbose":
			verbose = true
			i++
		case a == "--color":
			if i+1 >= len(args) {
				return nil, outputPolicy{}, UsageError("--color requires a value: auto, always, or never")
			}
			colorVal = args[i+1]
			colorSet = true
			i += 2
		case strings.HasPrefix(a, "--color="):
			colorVal = strings.TrimPrefix(a, "--color=")
			colorSet = true
			i++
		default:
			out = append(out, a)
			i++
		}
	}
	if quiet && verbose {
		return nil, outputPolicy{}, UsageError("--quiet and --verbose are mutually exclusive")
	}
	if colorSet {
		switch colorVal {
		case "auto", "always", "never":
		default:
			return nil, outputPolicy{}, UsageError("--color value must be auto, always, or never (got " + colorVal + ")")
		}
	}
	return out, outputPolicy{
		Quiet:   quiet,
		Verbose: verbose,
		Color:   resolveColorMode(colorVal),
	}, nil
}

// resolveColorMode applies the locked precedence rules:
//   - NO_COLOR set (any non-empty value) -> false. NO_COLOR wins over
//     --color=always per the slice's resolution.
//   - never -> false.
//   - always -> true.
//   - auto (default) -> outputIsTTY().
func resolveColorMode(mode string) bool {
	if strings.TrimSpace(outputGetenv("NO_COLOR")) != "" {
		return false
	}
	switch mode {
	case "never":
		return false
	case "always":
		return true
	default:
		return outputIsTTY()
	}
}

// colorize wraps s in the given ANSI color when the policy permits AND
// the caller has not opted out (e.g. when emitting under --json). The
// caller is responsible for the second check; this helper assumes the
// caller already verified that human output is appropriate.
func colorize(policy outputPolicy, color, s string) string {
	if !policy.Color || color == "" {
		return s
	}
	return color + s + ansiReset
}

const (
	ansiReset  = "\x1b[0m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiRed    = "\x1b[31m"
)

// statusANSI returns the ANSI escape for a status-like token used by
// status/down/doctor tables. Empty when the policy disables color so the
// caller emits the bare token.
func statusANSI(policy outputPolicy, status string) string {
	if !policy.Color {
		return ""
	}
	switch status {
	case "ok", "live", "wake-live", "stopped":
		return ansiGreen
	case "warn", "stale", "missing", "not-live":
		return ansiYellow
	case "fail", "failed":
		return ansiRed
	default:
		return ""
	}
}

// colorStatus returns either the colored or plain rendering of a status
// token for human tables. JSON callers must always use the plain status.
func colorStatus(policy outputPolicy, status string) string {
	if c := statusANSI(policy, status); c != "" {
		return c + status + ansiReset
	}
	return status
}

// quietNotice emits a non-data success/progress message to stderr unless
// the current policy is --quiet. Use this for the small set of locked
// notices: team init wrote, team rules init wrote/already-exists, team
// sync apply wrote-N, tmux launch success, launch dry-run note.
// Warnings and errors must NOT route through this helper.
func quietNotice(format string, args ...any) {
	if currentOutputPolicy.Quiet {
		return
	}
	fmt.Fprintf(os.Stderr, format, args...)
}

// verbosePolicyEcho writes the resolved output policy to stderr when
// --verbose is set. Used by launch/team-launch so operators can confirm
// the inherited policy without parsing the global flag scan themselves.
func verbosePolicyEcho() {
	if !currentOutputPolicy.Verbose {
		return
	}
	color := "off"
	if currentOutputPolicy.Color {
		color = "on"
	}
	fmt.Fprintf(os.Stderr, "verbose: output policy color=%s quiet=%t\n", color, currentOutputPolicy.Quiet)
}
