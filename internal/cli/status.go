package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/omriariav/amq-noc/internal/launch"
	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/state"
	"github.com/omriariav/amq-noc/internal/team"
)

// statusPaneLister lists live tmux panes so status can detect a live agent that
// was relaunched OUTSIDE amq-squad (its recorded PID is dead, but a replacement
// process is running). Injected as a package var so tests supply a fake and the
// classifier never shells real tmux. Defaults to the same read-only lister the
// NOC jump resolver uses, keeping detection consistent across surfaces.
var statusPaneLister = noc.DefaultPaneLister

// statusState is the precise state vocabulary emitted by `amq-squad status`.
// Definitions:
//   - live:      launch-record PID alive AND binary matches; the agent is running.
//   - wake-live: wake helper is verified live for this handle/root, but the
//     agent PID itself is not verified.
//   - stale:     live signals exist on disk (launch record, wake lock, or
//     presence) but none verify as usable for this handle.
//   - missing:   no launch record, no wake lock, no presence file. The member
//     is configured but has never run in the resolved session.
type statusState string

const (
	statusStateLive     statusState = "live"
	statusStateWakeLive statusState = "wake-live"
	statusStateStale    statusState = "stale"
	statusStateMissing  statusState = "missing"
)

type statusSignals struct {
	AgentPID    int       `json:"agent_pid,omitempty"`
	AgentAlive  bool      `json:"agent_alive,omitempty"`
	BinaryMatch bool      `json:"binary_match,omitempty"`
	WakePID     int       `json:"wake_pid,omitempty"`
	WakeAlive   bool      `json:"wake_alive,omitempty"`
	Presence    string    `json:"presence,omitempty"`
	LastSeen    time.Time `json:"last_seen,omitempty"`
}

// statusEnvelopeData is the kind="status" payload: resolved team-home,
// workstream, profile, and the per-member records.
type statusEnvelopeData struct {
	TeamHome   string         `json:"team_home"`
	Workstream string         `json:"workstream"`
	Profile    string         `json:"profile,omitempty"`
	Records    []statusRecord `json:"records"`
}

type statusRecord struct {
	Role     string        `json:"role"`
	Handle   string        `json:"handle"`
	Binary   string        `json:"binary"`
	Session  string        `json:"session"`
	CWD      string        `json:"cwd"`
	Root     string        `json:"root,omitempty"`
	AgentDir string        `json:"agent_dir,omitempty"`
	Status   statusState   `json:"status"`
	Detail   string        `json:"detail,omitempty"`
	Signals  statusSignals `json:"signals"`
}

func runStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	sessionName := fs.String("session", "", "AMQ workstream session name (default: a board over all discovered sessions)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned status envelope instead of the human table")
	projectFlag := fs.String("project", "", "project/team-home directory to inspect (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to inspect (default: default profile)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad status - live state of this project's sessions and team

Usage:
  amq-squad status [--project DIR] [--json]
  amq-squad status --session NAME [--project DIR] [--profile NAME] [--json]

With no --session, prints a multi-session BOARD over every discovered
session (docker-ps / git branch -v style): session name, rolled-up state
(running/stopped/degraded), agent health (N/M alive + at-risk), a one-line
brief, and last-activity. This is also the bare 'amq-squad' default.

With --session NAME, prints the single-session detail table: each
configured team member's live state in that session, using launch-record
PID + binary match, wake-lock PID + handle/root match, and fresh presence.

Examples:
  amq-squad status
  amq-squad status --project ~/Code/app
  amq-squad status --json
  amq-squad status --session issue-96 --json
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	projectDir, err := resolveProjectDirFlag(cwd, *projectFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	// No --session: the multi-session board over ALL discovered sessions.
	// This is the front-door default, so it degrades gracefully rather than
	// hard-erroring when `amq` is missing or there are no sessions.
	if !flagWasSet(fs, "session") {
		return runStatusBoard(projectDir, *jsonOut)
	}
	profile, err := resolveProfileFlag(*profileFlag)
	if err != nil {
		return err
	}
	if !team.ExistsProfile(projectDir, profile) {
		return fmt.Errorf("no team configured for profile %q. Run '%s' first.", profile, profileInitCommand(profile))
	}
	return executeStatus(statusExecution{
		ProjectDir:       projectDir,
		RequestedSession: *sessionName,
		ExplicitSession:  flagWasSet(fs, "session"),
		Profile:          profile,
		Probe:            defaultDuplicateLaunchProbe,
		Out:              os.Stdout,
		JSON:             *jsonOut,
	})
}

type statusExecution struct {
	ProjectDir       string
	RequestedSession string
	ExplicitSession  bool
	Profile          string
	Probe            duplicateLaunchProbe
	Out              io.Writer
	JSON             bool
}

func executeStatus(s statusExecution) error {
	t, err := team.ReadProfile(s.ProjectDir, s.Profile)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	if len(t.Members) == 0 {
		return fmt.Errorf("team has no members")
	}
	workstream, err := resolveTeamWorkstreamName(t, s.RequestedSession, s.ExplicitSession)
	if err != nil {
		return err
	}

	members := orderedTeamMembers(t.Members)
	rows := make([]statusRecord, 0, len(members))
	for _, m := range members {
		rows = append(rows, classifyMemberStatus(t, m, workstream, s.Probe))
	}
	if s.JSON {
		return writeJSONEnvelope(s.Out, "status", statusEnvelopeData{
			TeamHome:   t.Project,
			Workstream: workstream,
			Profile:    s.Profile,
			Records:    rows,
		})
	}
	policy := outputPolicyCurrent()
	fmt.Fprintf(s.Out, "# workstream: %s\n", workstream)
	if root := firstStatusRoot(rows); root != "" {
		fmt.Fprintf(s.Out, "# AM_ROOT:    %s\n", root)
	}
	fmt.Fprintln(s.Out)
	w := tabwriter.NewWriter(s.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ROLE\tHANDLE\tBINARY\tSESSION\tSTATUS\tDETAIL")
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Role, r.Handle, r.Binary, r.Session, colorStatus(policy, string(r.Status)), r.Detail)
	}
	return w.Flush()
}

func firstStatusRoot(rows []statusRecord) string {
	for _, r := range rows {
		if r.Root != "" {
			return r.Root
		}
	}
	return ""
}

func classifyMemberStatus(t team.Team, m team.Member, workstream string, probe duplicateLaunchProbe) statusRecord {
	rec := statusRecord{
		Role:    m.Role,
		Handle:  m.Handle,
		Binary:  m.Binary,
		Session: workstream,
		CWD:     m.EffectiveCWD(t.Project),
	}
	env, err := resolveAMQEnvInDir(rec.CWD, "", workstream, m.Handle)
	if err != nil {
		rec.Status = statusStateMissing
		rec.Detail = "amq env unresolved: " + err.Error()
		return rec
	}
	if env.Me != "" {
		rec.Handle = env.Me
	}
	root := absoluteAMQRoot(rec.CWD, env.Root)
	rec.Root = root
	rec.AgentDir = filepath.Join(root, "agents", rec.Handle)

	launchRec, launchErr := launch.Read(rec.AgentDir)
	wakeLock, wakeErr := readWakeLock(rec.AgentDir)
	presence, presenceErr := readPresenceForEntry(rec.AgentDir)

	hasLaunchPID := launchErr == nil && launchRec.AgentPID > 0
	hasWakePID := wakeErr == nil && wakeLock.PID > 0

	if hasLaunchPID {
		rec.Signals.AgentPID = launchRec.AgentPID
		if probe.PIDAlive(launchRec.AgentPID) {
			rec.Signals.AgentAlive = true
			binary := strings.TrimSpace(launchRec.Binary)
			if binary == "" {
				binary = m.Binary
			}
			if binary != "" && probe.ProcessMatch(launchRec.AgentPID, agentProcessMatcher(binary)) {
				rec.Signals.BinaryMatch = true
			}
		}
	}
	if hasWakePID {
		rec.Signals.WakePID = wakeLock.PID
		if probe.PIDAlive(wakeLock.PID) {
			expectedRoot := rec.Root
			if wakeLock.Root != "" {
				expectedRoot = wakeLock.Root
			}
			if probe.ProcessMatch(wakeLock.PID, wakeProcessMatcher(rec.Handle, expectedRoot)) {
				rec.Signals.WakeAlive = true
			}
		}
	}
	// Apply the same freshness/active/handle rules preflight and list use so
	// status agrees with them about what counts as a live presence signal.
	presenceLive := false
	presenceMismatched := false
	if presenceErr == nil {
		rec.Signals.Presence = presence.Status
		rec.Signals.LastSeen = presence.LastSeen
		fresh := !presence.LastSeen.IsZero() && probe.Now().Sub(presence.LastSeen) <= presenceFreshness
		active := strings.EqualFold(presence.Status, "active")
		handleOK := presence.Handle == "" || presence.Handle == rec.Handle
		switch {
		case fresh && active && handleOK:
			presenceLive = true
		case fresh && active && !handleOK:
			presenceMismatched = true
		}
	}

	if rec.Signals.AgentAlive && rec.Signals.BinaryMatch {
		rec.Status = statusStateLive
		rec.Detail = fmt.Sprintf("agent pid %d alive (%s)", rec.Signals.AgentPID, m.Binary)
		return rec
	}
	if presenceLive {
		rec.Status = statusStateLive
		rec.Detail = fmt.Sprintf("fresh active presence, no verified pid (last seen %s)", presence.LastSeen.UTC().Format(time.RFC3339))
		return rec
	}
	if rec.Signals.WakeAlive {
		rec.Status = statusStateWakeLive
		rec.Detail = wakeLiveDetail(rec.Signals)
		return rec
	}
	// Not live. Stale requires a live-pointing disk signal for this handle.
	// Lone stale/inactive/old presence does not count; it collapses to missing
	// so old presence files don't lock a member into "stale" forever.
	hasLiveSignal := hasLaunchPID || hasWakePID || presenceMismatched
	if !hasLiveSignal {
		rec.Status = statusStateMissing
		rec.Detail = "no live signals for this handle"
		return rec
	}
	// Before settling on stale: the recorded PID may be dead because the agent
	// was relaunched OUTSIDE amq-squad (e.g. "relaunching as dogfood QA"), leaving
	// a live replacement process the launch record never learned about. Look for a
	// live tmux pane that resolves to this member (same engine + cwd, title-first,
	// via the SAME resolver the NOC jump uses). If found, report live with a
	// re-register hint rather than a misleading stale.
	if target, ok := liveReplacementPane(m, rec, workstream); ok {
		rec.Status = statusStateLive
		rec.Detail = fmt.Sprintf("recorded pid dead; live %s at %s — relaunch via amq-squad to re-register", m.Binary, target)
		return rec
	}

	rec.Status = statusStateStale
	rec.Detail = staleDetail(rec.Signals, presenceMismatched) + "; relaunch via amq-squad to re-register"
	return rec
}

// liveReplacementPane reports a live tmux pane that resolves to this member when
// its recorded PID is dead — the case where the agent was relaunched outside
// amq-squad. It reuses the NOC jump resolver (title-first amq:<session>:<role>,
// then engine+cwd) so detection is consistent and conservative: only a
// SAME-ENGINE match is attributed, never a bare differently-engined pane. The
// pane lister is injectable (statusPaneLister) so tests never shell real tmux;
// any tmux/lister error degrades to "not found" (the caller stays stale).
func liveReplacementPane(m team.Member, rec statusRecord, workstream string) (string, bool) {
	panes, err := statusPaneLister()
	if err != nil || len(panes) == 0 {
		return "", false
	}
	ag := state.Agent{
		Handle: rec.Handle,
		Role:   m.Role,
		Engine: m.Binary,
	}
	target, ok := noc.ResolveTmuxTargetForSession(ag, workstream, rec.CWD, panes, nil)
	if !ok {
		return "", false
	}
	return noc.SuggestJump(target), true
}

func readWakeLock(agentDir string) (wakeLockFile, error) {
	path := wakeLockPath(agentDir)
	data, err := os.ReadFile(path)
	if err != nil {
		return wakeLockFile{}, err
	}
	var lock wakeLockFile
	if err := json.Unmarshal(data, &lock); err != nil {
		return wakeLockFile{}, err
	}
	return lock, nil
}

func staleDetail(s statusSignals, presenceMismatched bool) string {
	var parts []string
	if s.AgentPID > 0 {
		switch {
		case !s.AgentAlive:
			parts = append(parts, fmt.Sprintf("agent pid %d not alive", s.AgentPID))
		case !s.BinaryMatch:
			parts = append(parts, fmt.Sprintf("agent pid %d binary mismatch", s.AgentPID))
		}
	}
	if s.WakePID > 0 && !s.WakeAlive {
		parts = append(parts, fmt.Sprintf("wake pid %d not alive or unrelated", s.WakePID))
	}
	if s.WakeAlive {
		parts = append(parts, fmt.Sprintf("wake pid %d alive without verified agent", s.WakePID))
	}
	if presenceMismatched {
		parts = append(parts, "fresh presence for unrelated handle")
	}
	if len(parts) == 0 {
		return "stale signals on disk"
	}
	return strings.Join(parts, "; ")
}

func wakeLiveDetail(s statusSignals) string {
	parts := []string{fmt.Sprintf("wake pid %d alive", s.WakePID)}
	if s.AgentPID > 0 {
		switch {
		case !s.AgentAlive:
			parts = append(parts, fmt.Sprintf("agent pid %d not alive", s.AgentPID))
		case !s.BinaryMatch:
			parts = append(parts, fmt.Sprintf("agent pid %d binary mismatch", s.AgentPID))
		}
	} else {
		parts = append(parts, "no verified agent pid")
	}
	return strings.Join(parts, "; ")
}
