package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-noc/internal/center"
)

// runCenter starts the amq-noc 1.0 command center, the only view. Its single
// input is `amq-squad status --json` (or `status --fleet --json` with
// --fleet).
func runCenter(args []string, version string) error {
	fs := flag.NewFlagSet("amq-noc", flag.ContinueOnError)
	fleet := fs.Bool("fleet", false, "read `amq-squad status --fleet --json` instead of one squad's status")
	once := fs.Bool("once", false, "print one frame of the command center and exit (no terminal control)")
	root := fs.String("root", "", "the squad's AMQ session root, passed to `amq-squad status --root`")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-noc - command center for amq-squad squads

Usage:
  amq-noc [--root DIR | --fleet] [--once]

Reads 'amq-squad status --json' every 5 seconds: with --root DIR,
'amq-squad status --root DIR --json'; with --fleet, 'amq-squad status
--fleet --json'. Without either, amq-squad uses AM_ROOT (set inside a
squad's shell).

Keys: arrows move, enter opens the AMQ conversation with the lead, n runs
the status contract's new-task action, tab/shift+tab filter, esc back,
? help.

Flags:
  --root    the squad's AMQ session root (default: AM_ROOT)
  --fleet   read the fleet status (every squad amq-squad's fleet lists)
  --once    print one frame and exit; COLUMNS and LINES set the size
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *fleet && *root != "" {
		return usageErrorf("--root and --fleet cannot be used together")
	}
	client := center.Client{Runner: center.ExecRunner{}, Fleet: *fleet, Root: *root}
	if !*fleet && *root == "" && os.Getenv("AM_ROOT") == "" {
		client.Hint = "AM_ROOT is not set: run inside a squad's shell, pass --root DIR, or use --fleet"
	}
	if *once {
		f, err := client.Status(context.Background())
		if err != nil {
			return err
		}
		m := center.New(client, time.Now)
		m.SetSize(envInt("COLUMNS", 120), envInt("LINES", 40))
		m.SetFleet(f)
		fmt.Println(m.View())
		return nil
	}
	p := tea.NewProgram(center.New(client, time.Now), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func envInt(name string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(name)); err == nil && n > 0 {
		return n
	}
	return def
}
