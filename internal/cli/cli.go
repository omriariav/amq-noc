// Package cli is the top-level command dispatcher for amq-noc.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

// UsageError signals a misuse of the CLI (unknown command/flag, bad
// argument shape, missing required selector). main maps it to ExitUser
// (exit code 1) via cli.ExitCode.
type UsageError string

func (e UsageError) Error() string { return string(e) }

func usageErrorf(format string, args ...any) error {
	return UsageError(fmt.Sprintf(format, args...))
}

// Run dispatches to a subcommand. flag.ErrHelp from any --help path is
// swallowed so help output exits 0 across commands.
//
// Global output flags (--quiet, --verbose, --color) are peeled out of args
// before subcommand dispatch and stored on the package-level policy so any
// command can read them via outputPolicyCurrent. They may appear before or
// after the subcommand but never past a literal "--" boundary.
func Run(args []string, version string) error {
	args, policy, err := parseGlobalFlags(args)
	if err != nil {
		return err
	}
	prev := currentOutputPolicy
	currentOutputPolicy = policy
	defer func() { currentOutputPolicy = prev }()

	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		printUsage()
		return nil
	}
	if len(args) == 0 {
		return runNOCWithVersion(nil, version)
	}
	if args[0] == "--version" || args[0] == "-v" {
		fmt.Println("amq-noc", version)
		return nil
	}
	if args[0] == "version" {
		err := runVersion(version, args[1:])
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if strings.HasPrefix(args[0], "-") {
		err := runNOCWithVersion(args, version)
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	err = dispatch(args, version)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}

// runVersion prints the amq-noc version. With --json it emits a
// schema-versioned envelope on stdout.
func runVersion(version string, args []string) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit a schema-versioned JSON envelope instead of the human version line")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-noc version - print the amq-noc version

Usage:
  amq-noc version [--json]

Examples:
  amq-noc version
  amq-noc version --json
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *jsonOut {
		return printJSONEnvelope("version", versionEnvelopeData{Version: version})
	}
	fmt.Println("amq-noc", version)
	return nil
}

// versionEnvelopeData is the kind="version" payload.
type versionEnvelopeData struct {
	Version string `json:"version"`
}

func dispatch(args []string, version string) error {
	switch args[0] {
	case "noc":
		return runNOCWithVersion(args[1:], version)
	default:
		return usageErrorf("unknown command: %q. Run 'amq-noc --help' for usage.", args[0])
	}
}

func printUsage() {
	fmt.Print(`amq-noc - NOC command center for AMQ and amq-squad sessions

Usage:
  amq-noc [noc options]
  amq-noc noc [noc options]
  amq-noc version [--json]

Commands:
  noc       Multi-root NOC TUI, snapshots, and confirm-gated operator controls.
  version   Print the amq-noc version.

Global flags (accepted before or after the subcommand, until a literal "--"):
  --quiet              Suppress non-data success/progress notices.
  --verbose            Print additional diagnostic detail.
  --color auto|always|never
                       Control ANSI color output (default auto; honors NO_COLOR).

Exit codes:
  0  success
  1  usage / user error (unknown flag, bad argument, missing required input)
  2  system / runtime error (IO, process, config, environment)
  3  partial success (some targets succeeded, some failed)

Note: 'stop'/'down' without --force used to exit 2 ("graceful unavailable").
They now perform the SIGTERM teardown and exit 0 (or 3 on a partial run).

Examples:
  amq-noc
  amq-noc --root ~/Code
  amq-noc --filter needs-you
  amq-noc --once --root ~/Code
  amq-noc --json --root ~/Code | jq .

Mutating TUI controls remain preview-first and run through amq-squad.
Run 'amq-noc noc --help' for all NOC options.
`)
}
