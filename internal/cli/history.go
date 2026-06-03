package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/omriariav/amq-noc/internal/launch"
)

// historyEnvelopeData is the kind="history" payload: the projects scanned
// (resolved from --project flags or cwd) plus the restorable records.
type historyEnvelopeData struct {
	Projects []string        `json:"projects"`
	Records  []historyRecord `json:"records"`
}

// historyRecord is the row shape emitted by `amq-squad history`. Unlike
// listRecord, it does not carry a Wake field: history is restorable-launch
// history only; live wake-health is reported by `status` after the split.
type historyRecord struct {
	Role         string    `json:"role"`
	Handle       string    `json:"handle"`
	Binary       string    `json:"binary"`
	Session      string    `json:"session"`
	Conversation string    `json:"conversation,omitempty"`
	Source       string    `json:"source"`
	CWD          string    `json:"cwd"`
	StartedAt    time.Time `json:"started_at,omitempty"`
}

func runHistory(args []string) error {
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	projectDirs := fs.String("project", "", "comma-separated project directories to scan (default: cwd)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned history envelope instead of the human table")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad history - list restorable launch records

Usage:
  amq-squad history [--project dir1,dir2,...] [--json]

Reports launch history and inferred legacy AMQ mailbox entries that can be
restored. Live wake-health is intentionally not computed here; use
'amq-squad status' for the current configured team's live state.

Examples:
  amq-squad history
  amq-squad history --project ~/repos/foo,~/repos/bar --json
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	dirs, err := historyProjectDirs(*projectDirs)
	if err != nil {
		return err
	}
	all := scanHistoryEntries(dirs)
	sortRestorableEntries(all)
	rows := historyRecordsFromEntries(all)

	if *jsonOut {
		return printJSONEnvelope("history", historyEnvelopeData{
			Projects: dirs,
			Records:  rows,
		})
	}
	return writeHistoryTable(os.Stdout, rows)
}

func writeHistoryTable(out io.Writer, rows []historyRecord) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ROLE\tHANDLE\tBINARY\tSESSION\tCONVERSATION\tSOURCE\tCWD\tSTARTED")
	for _, r := range rows {
		role := r.Role
		if role == "" {
			role = "(none)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			role, r.Handle, r.Binary, r.Session, r.Conversation, r.Source, r.CWD, formatListTime(r.StartedAt))
	}
	return w.Flush()
}

func historyProjectDirs(projectFlag string) ([]string, error) {
	if projectFlag == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("getwd: %w", err)
		}
		return []string{cwd}, nil
	}
	var out []string
	for _, d := range strings.Split(projectFlag, ",") {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		dir, err := resolveProjectDirFlag("", d, true)
		if err != nil {
			return nil, err
		}
		out = append(out, dir)
	}
	if len(out) == 0 {
		return nil, usageErrorf("--project requires at least one directory")
	}
	return out, nil
}

func scanHistoryEntries(dirs []string) []launch.Entry {
	var all []launch.Entry
	for _, dir := range dirs {
		baseRoot, err := scanBaseRootForProject(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: resolve amq env for %s: %v\n", dir, err)
			baseRoot = ""
		}
		var entries []launch.Entry
		if baseRoot != "" {
			entries, err = launch.ScanRestorableEntriesInRoot(dir, baseRoot)
		} else {
			entries, err = launch.ScanRestorableEntries(dir)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: scan %s: %v\n", dir, err)
			continue
		}
		all = append(all, entries...)
	}
	return all
}

func historyRecordsFromEntries(entries []launch.Entry) []historyRecord {
	rows := make([]historyRecord, 0, len(entries))
	for _, e := range entries {
		r := e.Record
		rows = append(rows, historyRecord{
			Role:         r.Role,
			Handle:       r.Handle,
			Binary:       r.Binary,
			Session:      r.Session,
			Conversation: r.Conversation,
			Source:       sourceLabel(e.Source),
			CWD:          r.CWD,
			StartedAt:    r.StartedAt,
		})
	}
	return rows
}
