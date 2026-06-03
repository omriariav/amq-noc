package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/omriariav/amq-noc/internal/launch"
)

type listRecord struct {
	Role         string    `json:"role"`
	Handle       string    `json:"handle"`
	Binary       string    `json:"binary"`
	Session      string    `json:"session"`
	Conversation string    `json:"conversation,omitempty"`
	Source       string    `json:"source"`
	CWD          string    `json:"cwd"`
	Wake         string    `json:"wake,omitempty"`
	StartedAt    time.Time `json:"started_at,omitempty"`
}

// runList is the legacy registered-agent scanner. The top-level `list` verb
// was removed in 2.0 in favor of `status` (live agents) and `history`
// (restorable records). The body is retained internal-only for the tests
// that still exercise the scan/JSON-envelope helpers; no user-facing verb
// dispatches to it.
func runList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	projectDirs := fs.String("project", "", "comma-separated project directories to scan (default: cwd)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned list envelope instead of the human table")

	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad list - list restorable agents

Usage:
  amq-squad list [--project dir1,dir2,...] [--json]

Includes full amq-squad launch records and older AMQ-only mailbox history
where the original binary can be inferred.

Examples:
  amq-squad list
  amq-squad list --project ~/repos/foo --json
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	var dirs []string
	if *projectDirs == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getwd: %w", err)
		}
		dirs = []string{cwd}
	} else {
		for _, d := range strings.Split(*projectDirs, ",") {
			if d = strings.TrimSpace(d); d != "" {
				dirs = append(dirs, d)
			}
		}
	}

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

	sortRestorableEntries(all)
	rows := listRecordsFromEntries(all)

	if *jsonOut {
		return printJSONEnvelope("list", rows)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ROLE\tHANDLE\tBINARY\tSESSION\tCONVERSATION\tSOURCE\tWAKE\tCWD\tSTARTED")
	for _, r := range rows {
		role := r.Role
		if role == "" {
			role = "(none)"
		}
		wake := r.Wake
		if wake == "" {
			wake = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			role, r.Handle, r.Binary, r.Session, r.Conversation, r.Source, wake, r.CWD, formatListTime(r.StartedAt))
	}
	return w.Flush()
}

func sortRestorableEntries(entries []launch.Entry) {
	sort.Slice(entries, func(i, j int) bool {
		a := entries[i].Record
		b := entries[j].Record
		if a.Role != b.Role {
			return a.Role < b.Role
		}
		if a.Handle != b.Handle {
			return a.Handle < b.Handle
		}
		return entries[i].Source < entries[j].Source
	})
}

func listRecordsFromEntries(entries []launch.Entry) []listRecord {
	rows := make([]listRecord, 0, len(entries))
	for _, e := range entries {
		r := e.Record
		rows = append(rows, listRecord{
			Role:         r.Role,
			Handle:       r.Handle,
			Binary:       r.Binary,
			Session:      r.Session,
			Conversation: r.Conversation,
			Source:       sourceLabel(e.Source),
			CWD:          r.CWD,
			Wake:         wakeHealthForEntry(e, defaultDuplicateLaunchProbe),
			StartedAt:    r.StartedAt,
		})
	}
	return rows
}

// wakeHealthForEntry returns a short label describing wake state. Empty when
// the record does not look active enough to investigate. Output values:
//   - "pid:<n>": wake.lock present and wake process alive
//   - "missing": agent looks active but no wake.lock found
//   - "stale":   wake.lock present but PID dead or unrelated
func wakeHealthForEntry(e launch.Entry, probe duplicateLaunchProbe) string {
	if !looksActive(e, probe) {
		return ""
	}
	lockPath := wakeLockPath(e.AgentDir)
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return "missing"
	}
	var lock wakeLockFile
	if err := json.Unmarshal(data, &lock); err != nil {
		return "stale"
	}
	if lock.PID <= 0 || !probe.PIDAlive(lock.PID) {
		return "stale"
	}
	expectedRoot := e.Record.Root
	if lock.Root != "" {
		expectedRoot = lock.Root
	}
	if !probe.ProcessMatch(lock.PID, wakeProcessMatcher(e.Record.Handle, expectedRoot)) {
		return "stale"
	}
	return fmt.Sprintf("pid:%d", lock.PID)
}

// looksActive reports whether a launch entry is fresh enough to bother
// inspecting wake state. A record is "active-looking" when its agent PID is
// alive and the binary command matches, or when presence is fresh.
func looksActive(e launch.Entry, probe duplicateLaunchProbe) bool {
	rec := e.Record
	if rec.AgentPID > 0 && probe.PIDAlive(rec.AgentPID) {
		if rec.Binary == "" || probe.ProcessMatch(rec.AgentPID, agentProcessMatcher(rec.Binary)) {
			return true
		}
	}
	pres, err := readPresenceForEntry(e.AgentDir)
	if err != nil {
		return false
	}
	if !strings.EqualFold(pres.Status, "active") {
		return false
	}
	if pres.LastSeen.IsZero() {
		return false
	}
	return probe.Now().Sub(pres.LastSeen) <= presenceFreshness
}

func readPresenceForEntry(agentDir string) (presenceFile, error) {
	data, err := os.ReadFile(presencePath(agentDir))
	if err != nil {
		return presenceFile{}, err
	}
	var pres presenceFile
	if err := json.Unmarshal(data, &pres); err != nil {
		return presenceFile{}, err
	}
	return pres, nil
}

func presencePath(agentDir string) string {
	return filepath.Join(agentDir, "presence.json")
}

func formatListTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04")
}

func printJSON(recs any) error {
	enc := jsonEncoder()
	return enc.Encode(recs)
}
