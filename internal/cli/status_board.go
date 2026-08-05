package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/omriariav/amq-noc/internal/state"
)

// boardState is the rolled-up run-state of a whole session, derived from its
// agents' computed liveness. It is TEXT-led on purpose (per the DX review):
// the literal token is the source of truth and color is layered on top, never
// the other way around.
//
//   - running:  at least one agent is alive and none are at risk.
//   - degraded: at least one agent is alive, wake-live, or dead-mailbox-live
//     while another signal is incomplete; the session is up but unhealthy.
//   - stopped:  no agent is alive or wake-live (all dead / stale / missing).
type boardState string

const (
	boardStateRunning  boardState = "running"
	boardStateDegraded boardState = "degraded"
	boardStateStopped  boardState = "stopped"
)

// sessionsEnvelopeData is the kind="sessions" payload: the resolved base root
// plus one row per discovered session. This is a NEW envelope kind; the
// existing kind="status" (single-session --session detail) is unchanged and so
// is the global schema_version.
type sessionsEnvelopeData struct {
	BaseRoot string            `json:"base_root"`
	Sessions []sessionBoardRow `json:"sessions"`
	Notice   string            `json:"notice,omitempty"`
}

// briefKind classifies a session's workstream brief for the BRIEF column so the
// board never passes off an auto-generated stub as a real brief (a lie the DX
// review flagged). It is a human-rendering concern only: it is NOT part of the
// JSON sessions envelope, whose `brief` field stays the literal one-liner.
type briefKind int

const (
	// briefNone: no brief file exists for the session.
	briefNone briefKind = iota
	// briefStub: the brief file exists but is the untouched generated stub.
	briefStub
	// briefReal: the brief file has real, operator-authored content.
	briefReal
)

// sessionBoardRow is one session's board line in both the human table and the
// JSON envelope. AgentsTotal/AgentsAlive back the "N/M alive" health column;
// WakeLive flags verified wake helpers without verified agent PIDs. AtRisk
// flags the dead-mailbox-live case so an operator sees a live session that is
// actually unhealthy.
//
// briefKind is an unexported, human-only field (no JSON tag): it drives the
// distinct "(stub brief)" / "(no brief)" labels in the table without changing
// the JSON `brief` contract.
type sessionBoardRow struct {
	Name         string     `json:"name"`
	Root         string     `json:"root"`
	State        boardState `json:"state"`
	AgentsTotal  int        `json:"agents_total"`
	AgentsAlive  int        `json:"agents_alive"`
	WakeLive     int        `json:"wake_live,omitempty"`
	AtRisk       int        `json:"at_risk"`
	Brief        string     `json:"brief,omitempty"`
	LastActivity time.Time  `json:"last_activity,omitempty"`
	briefKind    briefKind
}

// statusBoardExecution carries the inputs for the multi-session board so tests
// can drive it with a seeded fixture, an injected state.Probe, and a captured
// writer — no real subprocess, no real wall clock.
type statusBoardExecution struct {
	ProjectDir string
	// BaseRoot, when set, is used verbatim and ResolveBaseRoot is NOT called.
	// Tests seed this directly; production leaves it empty and lets
	// ResolveBaseRoot shell out (best-effort) for the AMQ base root once.
	BaseRoot string
	// ResolveBaseRoot resolves the AMQ base root exactly once. Injected so the
	// front-door board degrades gracefully (and deterministically in tests)
	// when `amq` is missing or unresolvable. Defaults to scanBaseRootForProject.
	ResolveBaseRoot func(projectDir string) (string, error)
	Probe           state.Probe
	Now             func() time.Time
	Out             io.Writer
	JSON            bool
}

// runStatusBoard is the no-selector status entrypoint: a docker-ps / git
// branch-v style board over ALL discovered sessions. It is also the bare
// `amq-squad` default invocation, so it MUST NOT hard-error when `amq` is
// missing/unresolvable or there are no sessions: it resolves the base root
// best-effort and renders a clear, non-fatal guidance state instead.
func runStatusBoard(projectDir string, jsonOut bool) error {
	return executeStatusBoard(statusBoardExecution{
		ProjectDir: projectDir,
		Out:        os.Stdout,
		JSON:       jsonOut,
	})
}

func executeStatusBoard(s statusBoardExecution) error {
	resolve := s.ResolveBaseRoot
	if resolve == nil {
		resolve = scanBaseRootForProject
	}
	now := s.Now
	if now == nil {
		now = time.Now
	}

	baseRoot := s.BaseRoot
	var resolveErr error
	if baseRoot == "" {
		baseRoot, resolveErr = resolve(s.ProjectDir)
	}

	// GRACEFUL FRONT-DOOR DEGRADATION: the base root could not be resolved
	// (e.g. `amq` missing or not on PATH). This is the default invocation, so
	// render a non-fatal guidance state naming what was looked for and return
	// success — never crash the bare command.
	if resolveErr != nil || strings.TrimSpace(baseRoot) == "" {
		notice := boardUnresolvedNotice(resolveErr)
		if s.JSON {
			return writeJSONEnvelope(s.Out, "sessions", sessionsEnvelopeData{
				Sessions: []sessionBoardRow{},
				Notice:   notice,
			})
		}
		fmt.Fprintln(s.Out, notice)
		return nil
	}

	snap, buildErr := state.Build(s.ProjectDir, baseRoot, s.Probe)
	if buildErr != nil {
		// A scan failure (e.g. an unreadable base root) is still a front-door
		// invocation; degrade to guidance rather than a hard error.
		notice := fmt.Sprintf("amq-squad: could not scan AMQ base root %s: %v", baseRoot, buildErr)
		if s.JSON {
			return writeJSONEnvelope(s.Out, "sessions", sessionsEnvelopeData{
				BaseRoot: baseRoot,
				Sessions: []sessionBoardRow{},
				Notice:   notice,
			})
		}
		fmt.Fprintln(s.Out, notice)
		return nil
	}

	rows := make([]sessionBoardRow, 0, len(snap.Sessions))
	for _, sess := range snap.Sessions {
		rows = append(rows, boardRowFor(s.ProjectDir, sess))
	}

	if s.JSON {
		return writeJSONEnvelope(s.Out, "sessions", sessionsEnvelopeData{
			BaseRoot: snap.BaseRoot,
			Sessions: rows,
		})
	}
	return renderBoardTable(s.Out, snap.BaseRoot, rows, now())
}

// boardRowFor rolls one discovered session up into a board row: a state
// derived from agent liveness, an alive/total health count with an at-risk
// flag, the brief one-liner, and the most recent agent LastSeen.
func boardRowFor(projectDir string, sess state.Session) sessionBoardRow {
	row := sessionBoardRow{
		Name:        sess.Name,
		Root:        sess.Root,
		AgentsTotal: len(sess.Agents),
	}
	var latest time.Time
	for _, a := range sess.Agents {
		switch a.Liveness {
		case state.LivenessAlive:
			row.AgentsAlive++
		case state.LivenessWakeLive:
			row.WakeLive++
		case state.LivenessDeadMailboxLive:
			row.AtRisk++
		}
		if a.LastSeen.After(latest) {
			latest = a.LastSeen
		}
	}
	row.LastActivity = latest
	row.State = rollupBoardState(row.AgentsAlive, row.WakeLive, row.AtRisk, row.AgentsTotal)
	row.Brief, row.briefKind = classifyBrief(projectDir, sess.TeamProfile, sess.Name)
	return row
}

// rollupBoardState maps an alive/at-risk/total triple to the session state.
// at-risk (dead-mailbox-live) outranks a clean running roll-up: a session with
// a zombie heartbeat is degraded even if some agents are genuinely alive.
func rollupBoardState(alive, wakeLive, atRisk, total int) boardState {
	switch {
	case atRisk > 0 || wakeLive > 0:
		return boardStateDegraded
	case alive == 0:
		return boardStateStopped
	case alive < total:
		// Some agents alive, some not (dead/stale/missing) — up but incomplete.
		return boardStateDegraded
	default:
		return boardStateRunning
	}
}

// classifyBrief reads the workstream brief for session and reports both its
// first meaningful one-liner and its kind (none / stub / real). The one-liner
// is the first non-blank, non-heading, non-HTML-comment line; headings
// ("# ...") and comments are skipped so the stub's "# <session>" title is never
// the one-liner.
//
// STUB HONESTY: an untouched generated brief leads with the fixed stub prose
// (briefStubFirstLine). When the first meaningful line matches that marker we
// classify the file as briefStub rather than parroting its placeholder text as
// if it were an operator-authored brief. A missing file is briefNone; any other
// meaningful first line is briefReal.
func classifyBrief(projectDir, profile, session string) (string, briefKind) {
	path := briefPath(projectDir, profile, session)
	if path == "" {
		return "", briefNone
	}
	f, err := os.Open(path)
	if err != nil {
		// No readable brief file at all -> "(no brief)".
		return "", briefNone
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "<!--") {
			continue
		}
		if line == briefStubFirstLine {
			// The file exists but its first meaningful line is the generated
			// stub's fixed prose: it has not been filled in. Label it honestly.
			return "", briefStub
		}
		return line, briefReal
	}
	// File present but no meaningful content (e.g. only a heading): treat as a
	// real-but-empty brief rather than inventing stub/none. The cell will show
	// "-" for an empty real brief.
	return "", briefReal
}

// boardUnresolvedNotice composes the guidance line shown when the AMQ base
// root cannot be resolved. It names what was looked for (the `amq` env probe)
// so the operator knows the failure is environmental, not a missing team.
func boardUnresolvedNotice(err error) string {
	if err != nil {
		return "amq-squad: could not resolve the AMQ base root via `amq env` " +
			"(is `amq` installed and on PATH?): " + err.Error()
	}
	return "amq-squad: the AMQ base root resolved empty; no sessions to show. " +
		"Run 'amq-squad start' to launch your team, or 'amq-squad doctor' to check setup."
}

// defaultBaseRootName is the basename of the conventional AMQ base root
// (<project>/.agent-mail). When the resolved root ends in this name it is the
// default location and the summary line stays quiet about it; a non-default
// root is folded compactly into the summary so the operator notices.
const defaultBaseRootName = ".agent-mail"

// renderBoardTable writes the human-facing, TEXT-led session board:
//
//	a SUMMARY line (sessions / running / degraded / at-risk counts),
//	then columns SESSION, STATE, AGENTS, BRIEF, LAST-ACTIVITY.
//
// State is the literal token first; color (when enabled) is layered on top so
// the table is never glyph- or color-dependent. The old "# AM_BASE_ROOT:"
// debug header is gone from the default render (it read like stray debug output
// on the front-door command): the root is shown ONLY under --verbose, or folded
// compactly into the summary line when it is non-default.
func renderBoardTable(out io.Writer, baseRoot string, rows []sessionBoardRow, now time.Time) error {
	if len(rows) == 0 {
		fmt.Fprintf(out, "amq-squad: no sessions found under %s.\n", baseRoot)
		fmt.Fprintln(out, "Run 'amq-squad start' to launch your team, or 'amq-squad doctor' to check setup.")
		return nil
	}

	policy := outputPolicyCurrent()

	// Verbose keeps the full root visible without the debug-looking header.
	if policy.Verbose {
		fmt.Fprintf(out, "verbose: AMQ base root %s\n", baseRoot)
	}

	// SUMMARY line first so the operator gets the rollup before the rows.
	fmt.Fprintln(out, boardSummaryLine(baseRoot, rows))

	// ATTENTION-FIRST ORDER: degraded above running above stopped, and within a
	// state the most recently active session first. Live/degraded work floats to
	// the top instead of being buried under stopped squads.
	sortBoardRows(rows)

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SESSION\tSTATE\tAGENTS\tBRIEF\tLAST-ACTIVITY")
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			boardSessionName(r.Name),
			colorBoardState(policy, r.State),
			boardAgentsCell(r),
			boardBriefCell(r),
			boardLastActivity(r.LastActivity, now),
		)
	}
	return w.Flush()
}

// boardSummaryLine composes the one-line rollup shown above the table, e.g.:
//
//	amq-squad · 4 sessions · 1 running · 2 degraded · 1 at-risk
//
// The at-risk count is the sum of per-session dead-mailbox-live agents that
// internal/state already computes; it is always shown (even when zero) so the
// number is honest rather than conditionally hidden. When the base root is
// non-default it is appended compactly (· root: <path>) instead of leading with
// the old debug header.
//
// NOTE: a "needs you" human-action triage count joins this line in PR10, once
// the triage signal is actually defined. It is intentionally omitted now — a
// perpetually-0 "needs you" would be a dishonest number.
func boardSummaryLine(baseRoot string, rows []sessionBoardRow) string {
	var running, degraded, wakeLive, atRisk int
	for _, r := range rows {
		switch r.State {
		case boardStateRunning:
			running++
		case boardStateDegraded:
			degraded++
		}
		wakeLive += r.WakeLive
		atRisk += r.AtRisk
	}
	summary := fmt.Sprintf("amq-squad · %d %s · %d running · %d degraded · %d at-risk",
		len(rows), pluralize(len(rows), "session", "sessions"),
		running, degraded, atRisk)
	if wakeLive > 0 {
		summary += fmt.Sprintf(" · %d wake-live", wakeLive)
	}
	if !isDefaultBaseRoot(baseRoot) {
		summary += " · root: " + baseRoot
	}
	return summary
}

// isDefaultBaseRoot reports whether root is the conventional <project>/.agent-mail
// location, so the summary line stays quiet about a default root and only calls
// out a non-default one.
func isDefaultBaseRoot(root string) bool {
	root = strings.TrimRight(strings.TrimSpace(root), "/")
	if root == "" {
		return true
	}
	return filepath.Base(root) == defaultBaseRootName
}

// pluralize returns one or many depending on n, keeping the summary line's
// noun grammatical for a single session.
func pluralize(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// boardStateOrder ranks states for the attention-first sort: degraded (needs
// attention) first, then running (live), then stopped (idle) last.
func boardStateOrder(st boardState) int {
	switch st {
	case boardStateDegraded:
		return 0
	case boardStateRunning:
		return 1
	case boardStateStopped:
		return 2
	default:
		return 3
	}
}

// sortBoardRows orders rows attention-first: by state priority (degraded,
// running, stopped), then within a state by last activity DESCENDING (most
// recent first), with the session name as a final stable tiebreaker so the
// board is deterministic.
func sortBoardRows(rows []sessionBoardRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		oi, oj := boardStateOrder(rows[i].State), boardStateOrder(rows[j].State)
		if oi != oj {
			return oi < oj
		}
		if !rows[i].LastActivity.Equal(rows[j].LastActivity) {
			// Most recent activity first.
			return rows[i].LastActivity.After(rows[j].LastActivity)
		}
		return rows[i].Name < rows[j].Name
	})
}

// boardSessionName renders the session name, substituting a visible token for
// the rootless ("") layout so the column is never blank.
func boardSessionName(name string) string {
	if strings.TrimSpace(name) == "" {
		return "(root)"
	}
	return name
}

// boardAgentsCell renders the STATE-AWARE agent-health column. "0/N alive" is
// the wrong word for a stopped squad — no process is expected — so the wording
// follows the rolled-up state:
//
//   - running:  "N/N alive"
//   - degraded: "N/M alive" plus notes for wake-live and dead-mailbox-live
//   - stopped:  "stopped" when there are no agents to count, else "M agents"
//     (idle on disk) — never "0/M alive"
//
// The text is stable; callers do not depend on color here.
func boardAgentsCell(r sessionBoardRow) string {
	if r.State == boardStateStopped {
		if r.AgentsTotal == 0 {
			return "stopped"
		}
		return fmt.Sprintf("%d %s", r.AgentsTotal, pluralize(r.AgentsTotal, "agent", "agents"))
	}
	cell := fmt.Sprintf("%d/%d alive", r.AgentsAlive, r.AgentsTotal)
	if r.AtRisk > 0 {
		cell += fmt.Sprintf(" (%d at-risk)", r.AtRisk)
	}
	if r.WakeLive > 0 {
		cell += fmt.Sprintf(" (%d wake-live)", r.WakeLive)
	}
	return cell
}

// boardBriefCell renders the BRIEF column with stub honesty: a real brief shows
// its truncated one-liner; an untouched generated stub shows "(stub brief)"
// rather than parroting the placeholder prose; a missing brief shows
// "(no brief)". An empty-but-real brief falls through to "(no brief)" too.
func boardBriefCell(r sessionBoardRow) string {
	switch r.briefKind {
	case briefStub:
		return "(stub brief)"
	case briefNone:
		return "(no brief)"
	}
	brief := strings.TrimSpace(r.Brief)
	if brief == "" {
		// A real brief file with no meaningful one-liner reads like no brief.
		return "(no brief)"
	}
	const max = 60
	if len(brief) > max {
		return brief[:max-1] + "…"
	}
	return brief
}

// boardLastActivity renders an agent LastSeen as a relative age with freshness
// honesty: a zero time becomes "never" (no agent has reported), and a future
// timestamp (clock skew) is clamped to "just now" rather than a negative age.
func boardLastActivity(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
	if d < time.Minute {
		// Sub-minute (or clock-skewed future) reads as "just now" with no
		// "ago" suffix, avoiding the awkward "just now ago".
		return "just now"
	}
	return relativeAge(d) + " ago"
}

// relativeAge renders a duration as a coarse, human relative age. It stays
// honest about granularity (seconds/minutes/hours/days) rather than
// over-precising stale data.
func relativeAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d / time.Minute)
		return fmt.Sprintf("%dm", m)
	case d < 24*time.Hour:
		h := int(d / time.Hour)
		return fmt.Sprintf("%dh", h)
	default:
		days := int(d / (24 * time.Hour))
		return fmt.Sprintf("%dd", days)
	}
}

// colorBoardState returns the TEXT-led state token with color layered on when
// the policy permits. The literal token is always present; color is secondary.
func colorBoardState(policy outputPolicy, st boardState) string {
	switch st {
	case boardStateRunning:
		return colorize(policy, ansiGreen, string(st))
	case boardStateDegraded:
		return colorize(policy, ansiYellow, string(st))
	case boardStateStopped:
		return colorize(policy, ansiRed, string(st))
	default:
		return string(st)
	}
}
