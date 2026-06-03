package cli

import (
	"fmt"
	"os"
	"strings"
)

// runAgent dispatches the `agent` subgroup: `agent up <binary>` launches a
// single agent and `agent resume <role>` re-launches a saved one. These are
// the canonical single-agent verbs in 2.0 (the legacy top-level `launch` and
// `restore` verbs were removed).
func runAgent(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(os.Stderr, agentUsage)
		if len(args) == 0 {
			return usageErrorf("agent requires a subcommand: 'up' or 'resume'")
		}
		return nil
	}
	switch args[0] {
	case "up":
		return runAgentUp(args[1:])
	case "resume":
		return runAgentResume(args[1:])
	default:
		return usageErrorf("unknown 'agent' subcommand: %q. Try 'up' or 'resume'.", args[0])
	}
}

const agentUsage = `amq-squad agent - launch or resume a single agent

Usage:
  amq-squad agent up <binary> [launch options]
  amq-squad agent resume <role> [--project dir1,dir2,...] [restore options]

agent up launches a single agent with role metadata.
agent resume re-launches a saved agent by role from local launch history.

Examples:
  amq-squad agent up codex --role cto --session issue-96
  amq-squad agent resume cto
`

// runAgentUp is the single-agent launcher front door. The binary positional
// and all launch flags are accepted; it translates the modern flags-after-
// binary shape and delegates to the shared launcher body (runLaunch).
func runAgentUp(args []string) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprint(os.Stderr, `amq-squad agent up - launch a single agent with role metadata

Usage:
  amq-squad agent up <binary> [--project DIR] [launch options] [-- <binary-flags>]

Run 'amq-squad agent up <binary> --help' for the full flag surface.

Examples:
  amq-squad agent up codex --role cto --session issue-96
  amq-squad agent up codex --project ~/Code/app --session issue-96
  amq-squad agent up claude --no-bootstrap
`)
		return nil
	}
	// agent up syntax is `agent up <binary> [launch flags] [-- child args]`.
	// runLaunch's parser expects `[flags] <binary> [-- child]`, so translate
	// before delegating: lift the binary to after the launch flags. This lets
	// modern callers type flags after the binary while runLaunch's flag
	// parser still sees flags first.
	translated := translateAgentUpArgs(args)
	return runLaunch(translated)
}

// translateAgentUpArgs reorders `agent up <binary> [flags...] [-- child]`
// into the legacy `launch [flags...] <binary> [-- child]` shape so
// runLaunch's parser sees flags first. Inputs that do not start with a
// binary positional (e.g. `--help`, empty) are passed through unchanged
// so runLaunch reports the right error.
//
// When the caller omits the `--` boundary, this function preserves
// behavior parity with legacy `launch [flags...] <binary> <child...>`:
// only recognized launch flags (and their values) after `<binary>` are
// kept as launch flags. The first unrecognized token (whether a non-flag
// positional or an unknown `-...` flag) becomes the implicit child
// boundary, and the synthesized `--` is inserted before it so native
// child flags like `--foo` do not collide with runLaunch's flag parser.
func translateAgentUpArgs(args []string) []string {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return args
	}
	binary := args[0]
	rest := args[1:]

	// Explicit `--` boundary: keep child block verbatim.
	for i, a := range rest {
		if a == "--" {
			flags := rest[:i]
			child := rest[i:]
			out := append([]string{}, flags...)
			out = append(out, binary)
			out = append(out, child...)
			return out
		}
	}

	// No explicit `--`. Walk rest, consuming only recognized launch flags
	// (and their values for string-valued flags). Stop at the first
	// unrecognized token; everything from there is the child block.
	flagsEnd := len(rest)
walk:
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if !strings.HasPrefix(a, "-") {
			// Bare positional after the binary: child boundary.
			flagsEnd = i
			break walk
		}
		// `--flag=value` syntax: split and check name.
		name := a
		hasEq := strings.Contains(a, "=")
		if hasEq {
			name = a[:strings.IndexByte(a, '=')]
		}
		kind := launchKnownFlag(name)
		switch kind {
		case "string":
			if hasEq {
				continue
			}
			// Bare `--role` is only a complete launch flag if the next
			// token is a non-flag value. If the next token is missing
			// or dash-prefixed, that token is part of the child block
			// (legacy `launch` parses it that way too because the
			// binary positional came first). Match that parity.
			if i+1 >= len(rest) || strings.HasPrefix(rest[i+1], "-") {
				flagsEnd = i
				break walk
			}
			i++ // consume value token
			continue
		case "string-accepts-dash":
			if hasEq {
				continue
			}
			// `--codex-args` / `--claude-args` are documented to carry
			// native child-flag strings that commonly start with `-`
			// (e.g. `--enable goals`, `--chrome`). Always consume the
			// next token as the value when present; only the trailing
			// no-value case falls back to child boundary.
			if i+1 >= len(rest) {
				flagsEnd = i
				break walk
			}
			i++ // consume value token (may start with `-`)
			continue
		case "bool":
			continue
		default:
			// Unknown `-...` token: child block starts here.
			flagsEnd = i
			break walk
		}
	}
	flags := rest[:flagsEnd]
	child := rest[flagsEnd:]
	out := append([]string{}, flags...)
	out = append(out, binary)
	if len(child) > 0 {
		out = append(out, "--")
		out = append(out, child...)
	}
	return out
}

// launchKnownFlag classifies a token as a launch flag for translation.
//
//   - "string"              : value-consuming flag whose value is a normal
//     (non-dash-prefixed) token. Bare or
//     dash-followed forms terminate the launch
//     region for child-arg parity with legacy
//     launch.
//   - "string-accepts-dash" : value-consuming flag documented to carry
//     native child-arg strings (often starting
//     with `-`), e.g. --codex-args / --claude-args.
//   - "bool"                : flag-only.
//   - ""                    : unknown.
//
// Mirrors the fs.String / fs.Bool registrations in launch.go so `agent up`
// can split launch flags from native child flags when no `--` boundary is
// given.
func launchKnownFlag(name string) string {
	switch name {
	case "--codex-args", "--claude-args", "--launcher-args",
		"-codex-args", "-claude-args", "-launcher-args":
		return "string-accepts-dash"
	case "--role", "--session", "--me", "--root", "--project", "--team-home", "--team-profile",
		"--conversation", "--conversation-id", "--trust", "--model", "--launcher",
		"-role", "-session", "-me", "-root", "-project", "-team-home", "-team-profile",
		"-conversation", "-conversation-id", "-trust", "-model", "-launcher":
		return "string"
	case "--team-workstream", "--no-bootstrap", "--no-default-args",
		"--force-duplicate", "--dry-run",
		"-team-workstream", "-no-bootstrap", "-no-default-args",
		"-force-duplicate", "-dry-run",
		// Help flags after the binary should fall through to runLaunch
		// (which prints amq-squad launch help) rather than flow to the
		// child binary as `-- --help`. Native child help is still
		// reachable behind an explicit `--` boundary.
		"--help", "-help", "-h":
		return "bool"
	}
	return ""
}

// runAgentResume turns `agent resume <role> [extras]` into the shared replay
// body's `--exec --role <role> [extras]` call. The role is the only required
// positional; extras flow into the replay body unchanged so callers can still
// filter by --handle / --session / --conversation / --project.
func runAgentResume(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad agent resume - re-launch a saved agent by role

Usage:
  amq-squad agent resume <role> [--project dir1,dir2,...] [restore options]

Re-launches a saved agent from local launch history. Exactly one saved
record must match; on match, amq-squad changes to that record's cwd and
execs the saved launch through 'amq coop exec'.
Default scope is the current working directory. Pass --project to scan one
or more other team-homes without changing directories.

Examples:
  amq-squad agent resume cto
  amq-squad agent resume cto --project ~/Code/app
  amq-squad agent resume fullstack --session issue-96
`)
		if len(args) == 0 {
			return usageErrorf("agent resume requires a role positional argument")
		}
		return nil
	}
	role := args[0]
	rest := args[1:]
	forwarded := append([]string{"--exec", "--role", role}, rest...)
	return runRestore(forwarded)
}
