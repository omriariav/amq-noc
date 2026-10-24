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
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-noc - command center for amq-squad squads

Usage:
  amq-noc [--fleet] [--once]

Reads 'amq-squad status --json' (with --fleet: 'amq-squad status --fleet --json')
every 5 seconds. Keys: arrows move, enter opens the AMQ conversation with the
lead, n runs the status contract's new-task action, tab/shift+tab filter,
esc back, ? help.

Flags:
  --fleet   read the fleet status instead of one squad's
  --once    print one frame and exit; COLUMNS and LINES set the size
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	client := center.Client{Runner: center.ExecRunner{}, Fleet: *fleet}
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
