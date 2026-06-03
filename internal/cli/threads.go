package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/omriariav/amq-noc/internal/state"
)

const defaultThreadsLimit = 20

type threadsEnvelopeData struct {
	ProjectDir    string            `json:"project_dir"`
	BaseRoot      string            `json:"base_root"`
	Session       string            `json:"session"`
	Root          string            `json:"root"`
	ThreadCount   int               `json:"thread_count"`
	ReturnedCount int               `json:"returned_count"`
	Limit         int               `json:"limit,omitempty"`
	Threads       []threadRow       `json:"threads"`
	Warnings      []threadWarning   `json:"warnings,omitempty"`
	Rollup        threadsRollupData `json:"rollup"`
}

type threadRow struct {
	ID           string          `json:"id"`
	LatestID     string          `json:"latest_id,omitempty"`
	Participants []string        `json:"participants,omitempty"`
	Subject      string          `json:"subject,omitempty"`
	Kind         string          `json:"kind,omitempty"`
	Labels       []string        `json:"labels,omitempty"`
	Orchestrator string          `json:"orchestrator,omitempty"`
	FromProject  string          `json:"from_project,omitempty"`
	ReplyProject string          `json:"reply_project,omitempty"`
	Status       string          `json:"status"`
	Triage       string          `json:"triage"`
	AttnReason   string          `json:"attn_reason,omitempty"`
	Stale        bool            `json:"stale,omitempty"`
	Historical   bool            `json:"historical,omitempty"`
	LastEventAt  time.Time       `json:"last_event_at,omitempty"`
	MessageCount int             `json:"message_count"`
	UnreadBy     []string        `json:"unread_by,omitempty"`
	Freshness    threadFreshness `json:"freshness"`
}

type threadFreshness struct {
	Source   string        `json:"source,omitempty"`
	Observed time.Time     `json:"observed,omitempty"`
	Age      time.Duration `json:"age,omitempty"`
	Stale    bool          `json:"stale,omitempty"`
}

type threadWarning struct {
	Path   string `json:"path,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type threadsRollupData struct {
	NeedsYou     int `json:"needs_you"`
	AtRisk       int `json:"at_risk"`
	Blocked      int `json:"blocked"`
	Gated        int `json:"gated"`
	AtRiskStale  int `json:"at_risk_stale"`
	BlockedStale int `json:"blocked_stale"`
	GatedStale   int `json:"gated_stale"`
	Clear        int `json:"clear"`
}

type threadsExecution struct {
	ProjectDir      string
	Session         string
	Limit           int
	BaseRoot        string
	ResolveBaseRoot func(projectDir string) (string, error)
	Probe           state.Probe
	Now             func() time.Time
	Out             io.Writer
	JSON            bool
}

func runThreads(args []string) error {
	fs := flag.NewFlagSet("threads", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory to inspect (default: cwd)")
	sessionFlag := fs.String("session", "", "AMQ workstream session name to inspect")
	limitFlag := fs.Int("limit", defaultThreadsLimit, "maximum thread rows to show (0 = all)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned threads envelope instead of the human table")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad threads - list derived AMQ thread summaries for one session

Usage:
  amq-squad threads --session NAME [--project DIR] [--limit N] [--json]

Reads the existing amq-squad snapshot model and prints one collapsed row per
thread in the selected workstream. This is read-only: it scans mailboxes and
does not move unread mail.

Examples:
  amq-squad threads --session issue-96
  amq-squad threads --project ~/Code/app --session issue-96 --limit 50
  amq-squad threads --session issue-96 --json
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if strings.TrimSpace(*sessionFlag) == "" {
		return usageErrorf("threads requires --session")
	}
	if *limitFlag < 0 {
		return usageErrorf("--limit must be >= 0")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	projectDir, err := resolveProjectDirFlag(cwd, *projectFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	return executeThreads(threadsExecution{
		ProjectDir: projectDir,
		Session:    *sessionFlag,
		Limit:      *limitFlag,
		Out:        os.Stdout,
		JSON:       *jsonOut,
	})
}

func executeThreads(s threadsExecution) error {
	out := s.Out
	if out == nil {
		out = os.Stdout
	}
	now := s.Now
	if now == nil {
		now = time.Now
	}
	session := strings.TrimSpace(s.Session)
	if session == "" {
		return usageErrorf("threads requires --session")
	}
	if s.Limit < 0 {
		return usageErrorf("--limit must be >= 0")
	}
	resolve := s.ResolveBaseRoot
	if resolve == nil {
		resolve = scanBaseRootForProject
	}
	baseRoot := strings.TrimSpace(s.BaseRoot)
	var err error
	if baseRoot == "" {
		baseRoot, err = resolve(s.ProjectDir)
		if err != nil {
			return fmt.Errorf("resolve AMQ base root: %w", err)
		}
	}
	if baseRoot == "" {
		return fmt.Errorf("resolve AMQ base root: empty root")
	}
	snap, err := state.Build(s.ProjectDir, baseRoot, s.Probe)
	if err != nil {
		return fmt.Errorf("scan AMQ base root: %w", err)
	}
	sess, ok := findThreadsSession(snap.Sessions, session)
	if !ok {
		return fmt.Errorf("session %q not found under %s", session, baseRoot)
	}
	rows := threadRows(sess.Coordination.Threads)
	total := len(rows)
	if s.Limit > 0 && len(rows) > s.Limit {
		rows = rows[:s.Limit]
	}
	env := threadsEnvelopeData{
		ProjectDir:    s.ProjectDir,
		BaseRoot:      snap.BaseRoot,
		Session:       sess.Name,
		Root:          sess.Root,
		ThreadCount:   total,
		ReturnedCount: len(rows),
		Limit:         s.Limit,
		Threads:       rows,
		Warnings:      threadWarnings(sess.Coordination.Warnings),
		Rollup:        threadsRollup(sess.Rollup),
	}
	if s.JSON {
		return writeJSONEnvelope(out, "threads", env)
	}
	return renderThreadsTable(out, env, now())
}

func findThreadsSession(sessions []state.Session, name string) (state.Session, bool) {
	for _, sess := range sessions {
		if sess.Name == name {
			return sess, true
		}
	}
	return state.Session{}, false
}

func threadRows(threads []state.ThreadSummary) []threadRow {
	sorted := append([]state.ThreadSummary(nil), threads...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ti, tj := cliThreadTriageRank(sorted[i].Triage), cliThreadTriageRank(sorted[j].Triage)
		if ti != tj {
			return ti < tj
		}
		si, sj := cliThreadStatusRank(sorted[i].Status), cliThreadStatusRank(sorted[j].Status)
		if si != sj {
			return si < sj
		}
		if !sorted[i].LastEventAt.Equal(sorted[j].LastEventAt) {
			return sorted[i].LastEventAt.After(sorted[j].LastEventAt)
		}
		return sorted[i].ID < sorted[j].ID
	})
	out := make([]threadRow, 0, len(sorted))
	for _, th := range sorted {
		out = append(out, threadRow{
			ID:           th.ID,
			LatestID:     th.LatestID,
			Participants: append([]string(nil), th.Participants...),
			Subject:      th.Subject,
			Kind:         string(th.Kind),
			Labels:       append([]string(nil), th.Labels...),
			Orchestrator: th.Orchestrator,
			FromProject:  th.FromProject,
			ReplyProject: th.ReplyProject,
			Status:       string(th.Status),
			Triage:       string(th.Triage),
			AttnReason:   string(th.AttnReason),
			Stale:        th.Stale,
			Historical:   th.Historical,
			LastEventAt:  th.LastEventAt,
			MessageCount: th.MessageCount,
			UnreadBy:     append([]string(nil), th.UnreadBy...),
			Freshness: threadFreshness{
				Source:   string(th.Freshness.Source),
				Observed: th.Freshness.Observed,
				Age:      th.Freshness.Age,
				Stale:    th.Freshness.Stale,
			},
		})
	}
	return out
}

func cliThreadTriageRank(t state.Triage) int {
	switch t {
	case state.TriageNeedsYou:
		return 0
	case state.TriageBlocked:
		return 1
	case state.TriageGated:
		return 2
	case state.TriageAtRisk:
		return 3
	default:
		return 4
	}
}

func cliThreadStatusRank(s state.ThreadStatus) int {
	switch s {
	case state.ThreadBlocked:
		return 0
	case state.ThreadAwaitingReply:
		return 1
	case state.ThreadOpen:
		return 2
	default:
		return 3
	}
}

func threadWarnings(warnings []state.Warning) []threadWarning {
	if len(warnings) == 0 {
		return nil
	}
	out := make([]threadWarning, 0, len(warnings))
	for _, w := range warnings {
		out = append(out, threadWarning{Path: w.Path, Reason: w.Reason})
	}
	return out
}

func threadsRollup(r state.TriageRollup) threadsRollupData {
	return threadsRollupData{
		NeedsYou:     r.NeedsYou,
		AtRisk:       r.AtRisk,
		Blocked:      r.Blocked,
		Gated:        r.Gated,
		AtRiskStale:  r.AtRiskStale,
		BlockedStale: r.BlockedStale,
		GatedStale:   r.GatedStale,
		Clear:        r.Clear,
	}
}

func renderThreadsTable(out io.Writer, env threadsEnvelopeData, now time.Time) error {
	fmt.Fprintf(out, "# amq-squad threads\n")
	fmt.Fprintf(out, "# project: %s\n", env.ProjectDir)
	fmt.Fprintf(out, "# session: %s\n", env.Session)
	fmt.Fprintf(out, "# root: %s\n", env.Root)
	if env.Limit > 0 && env.ReturnedCount < env.ThreadCount {
		fmt.Fprintf(out, "# showing: %d/%d\n", env.ReturnedCount, env.ThreadCount)
	}
	if len(env.Warnings) > 0 {
		fmt.Fprintf(out, "# warnings: %d\n", len(env.Warnings))
	}
	fmt.Fprintln(out)
	if len(env.Threads) == 0 {
		fmt.Fprintln(out, "(no threads)")
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TRIAGE\tSTATUS\tTHREAD\tSUBJECT\tLAST\tMSGS\tPARTICIPANTS\tUNREAD")
	for _, r := range env.Threads {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			threadTriageCell(r),
			emptyDash(r.Status),
			r.ID,
			truncateASCII(emptyDash(r.Subject), 72),
			threadLastCell(r.LastEventAt, now),
			r.MessageCount,
			emptyDash(strings.Join(r.Participants, ",")),
			emptyDash(strings.Join(r.UnreadBy, ",")),
		)
	}
	return w.Flush()
}

func threadTriageCell(r threadRow) string {
	triage := strings.TrimSpace(r.Triage)
	if triage == "" {
		triage = "clear"
	}
	if r.Stale && triage != string(state.TriageNeedsYou) {
		return triage + "(stale)"
	}
	return triage
}

func threadLastCell(t, now time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	age := relativeAge(d)
	if age == "just now" {
		return age
	}
	return age + " ago"
}

func emptyDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func truncateASCII(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
