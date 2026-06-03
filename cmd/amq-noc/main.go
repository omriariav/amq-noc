package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/omriariav/amq-noc/internal/cli"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], effectiveVersion(), os.Stderr))
}

// run executes cli.Run and maps the result through cli.ExitCode so the
// exit-code taxonomy (0/1/2/3) lives in one place. Extracted from main so
// tests can drive it without spawning a subprocess. stderr accepts any
// io.Writer so tests can capture or discard the human error line.
func run(args []string, version string, stderr io.Writer) int {
	err := cli.Run(args, version)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
	}
	return cli.ExitCode(err)
}

func effectiveVersion() string {
	return resolveVersion(version, buildInfoVersion())
}

func buildInfoVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	return info.Main.Version
}

func resolveVersion(ldflagVersion string, moduleVersion string) string {
	if ldflagVersion != "" && ldflagVersion != "dev" {
		return ldflagVersion
	}
	if moduleVersion == "" || moduleVersion == "(devel)" {
		return ldflagVersion
	}
	return moduleVersion
}
