package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/omriariav/amq-noc/internal/launch"
)

// presenceFreshness defines how recently a presence.json must have been
// updated for it to count as a live signal. Older presence is treated as
// stale and ignored.
const presenceFreshness = 90 * time.Second

// duplicateBlocker describes a refusal reason raised by preflight.
type duplicateBlocker struct {
	Handle     string
	Workstream string
	Root       string
	AgentDir   string
	Reasons    []blockerReason
}

type blockerReason struct {
	// Source is a short label: "wake", "launch", "presence".
	Source string
	// Message is a human-readable description of what was detected.
	Message string
	// Hint is a single suggested next action.
	Hint string
}

// Error implements error so a blocker can be returned as the launch error.
func (b *duplicateBlocker) Error() string {
	var lines []string
	header := fmt.Sprintf("refusing to launch %q", b.Handle)
	if b.Workstream != "" {
		header += fmt.Sprintf(" in workstream %q", b.Workstream)
	}
	header += ":"
	lines = append(lines, header)
	for _, r := range b.Reasons {
		lines = append(lines, "  - "+r.Source+": "+r.Message)
	}
	if b.Root != "" {
		lines = append(lines, "  root: "+b.Root)
	}
	if b.AgentDir != "" {
		lines = append(lines, "  agent dir: "+b.AgentDir)
	}
	lines = append(lines, "Next actions:")
	lines = append(lines, "  - attach the existing terminal, or stop the old process")
	lines = append(lines, "  - rerun with --force-duplicate to launch anyway")
	return strings.Join(lines, "\n")
}

// agentLaunchPreflight inspects existing artifacts for a target agent identity
// and reports whether a launch should be refused.
type agentLaunchPreflight struct {
	AgentDir   string
	Handle     string
	Workstream string
	Root       string
	Binary     string
	Force      bool
	// DryRun keeps the preflight inspection read-only: stale lock files are
	// detected as non-blocking but never removed. Required for --dry-run paths
	// where the operator expects zero side effects on disk.
	DryRun bool
}

// duplicateLaunchProbe abstracts liveness and process-inspection checks so
// tests can substitute deterministic implementations.
type duplicateLaunchProbe struct {
	PIDAlive     func(pid int) bool
	ProcessMatch func(pid int, predicate func(args string) bool) bool
	Now          func() time.Time
}

var defaultDuplicateLaunchProbe = duplicateLaunchProbe{
	PIDAlive:     defaultPIDAlive,
	ProcessMatch: defaultProcessMatch,
	Now:          time.Now,
}

func defaultPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// defaultProcessMatch shells out to ps to read the process command line for
// pid and applies the predicate. Returns false on any error (best effort).
func defaultProcessMatch(pid int, predicate func(args string) bool) bool {
	if pid <= 0 || predicate == nil {
		return false
	}
	cmd := exec.Command("ps", "-o", "args=", "-p", fmt.Sprintf("%d", pid))
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	args := strings.TrimSpace(string(out))
	return predicate(args)
}

// check inspects wake locks, prior launch records, and presence. It returns
// a *duplicateBlocker (as an error) when a hard-block condition is met and
// Force is false. Stale artifacts are removed in place. The second return
// value is reserved for I/O errors that should abort preflight.
func (p agentLaunchPreflight) check(probe duplicateLaunchProbe) (*duplicateBlocker, error) {
	blocker := &duplicateBlocker{
		Handle:     p.Handle,
		Workstream: p.Workstream,
		Root:       p.Root,
		AgentDir:   p.AgentDir,
	}

	// Presence first, before wake-lock cleanup runs. inspectPresence consults
	// the wake.lock and launch.json writer-liveness to decide whether the
	// presence file is a zombie heartbeat; inspectWakeLock will rewrite the
	// disk by removing stale locks, which would otherwise make wake writer
	// status look "unknown" instead of "known dead" to the presence check.
	if reason, err := p.inspectPresence(probe); err != nil {
		return nil, err
	} else if reason != nil {
		blocker.Reasons = append(blocker.Reasons, *reason)
	}

	// Wake lock.
	if reason, err := p.inspectWakeLock(probe); err != nil {
		return nil, err
	} else if reason != nil {
		// Wake comes first in the reported blocker list for stable
		// ordering with prior versions.
		blocker.Reasons = append([]blockerReason{*reason}, blocker.Reasons...)
	}

	// Prior launch record (PID alive + plausible command).
	if reason, err := p.inspectLaunchRecord(probe); err != nil {
		return nil, err
	} else if reason != nil {
		// Launch slots in between wake and presence.
		idx := 0
		if len(blocker.Reasons) > 0 && blocker.Reasons[0].Source == "wake" {
			idx = 1
		}
		blocker.Reasons = append(blocker.Reasons[:idx], append([]blockerReason{*reason}, blocker.Reasons[idx:]...)...)
	}

	if len(blocker.Reasons) == 0 {
		return nil, nil
	}
	if p.Force {
		// Honor force but echo what was overridden so the operator sees it.
		fmt.Fprintln(os.Stderr, "warning: --force-duplicate overrode the following live-agent signals:")
		for _, r := range blocker.Reasons {
			fmt.Fprintln(os.Stderr, "  - "+r.Source+": "+r.Message)
		}
		return nil, nil
	}
	return blocker, nil
}

func (p agentLaunchPreflight) inspectWakeLock(probe duplicateLaunchProbe) (*blockerReason, error) {
	path := wakeLockPath(p.AgentDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read wake lock: %w", err)
	}
	var lock wakeLockFile
	if err := json.Unmarshal(data, &lock); err != nil {
		// Corrupt lock file: treat as stale.
		p.removeIfNotDryRun(path)
		return nil, nil
	}
	if lock.PID <= 0 || !probe.PIDAlive(lock.PID) {
		p.removeIfNotDryRun(path)
		return nil, nil
	}
	expectedRoot := p.Root
	if lock.Root != "" {
		expectedRoot = lock.Root
	}
	if !probe.ProcessMatch(lock.PID, wakeProcessMatcher(p.Handle, expectedRoot)) {
		// PID reuse or unrelated process: stale. The lock dir anchors the
		// handle, but the live PID's --root must still point to the same
		// workstream root we're targeting; otherwise it belongs to another
		// project's wake.
		p.removeIfNotDryRun(path)
		return nil, nil
	}
	msg := fmt.Sprintf("live amq wake at pid %d", lock.PID)
	if lock.TTY != "" && lock.TTY != "unknown" {
		msg += " (tty " + lock.TTY + ")"
	}
	if !lock.Started.IsZero() {
		msg += " started " + lock.Started.UTC().Format(time.RFC3339)
	}
	msg += "; lock " + path
	return &blockerReason{
		Source:  "wake",
		Message: msg,
		Hint:    "stop wake (kill " + fmt.Sprintf("%d", lock.PID) + ") or attach its terminal",
	}, nil
}

func (p agentLaunchPreflight) inspectLaunchRecord(probe duplicateLaunchProbe) (*blockerReason, error) {
	rec, err := launch.Read(p.AgentDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		// Tolerate parse errors: previous record is informational only.
		return nil, nil
	}
	if rec.AgentPID <= 0 {
		return nil, nil
	}
	if !probe.PIDAlive(rec.AgentPID) {
		return nil, nil
	}
	binary := strings.TrimSpace(rec.Binary)
	if binary == "" {
		binary = p.Binary
	}
	if binary == "" || !probe.ProcessMatch(rec.AgentPID, agentProcessMatcher(binary)) {
		// PID reuse: not our agent.
		return nil, nil
	}
	msg := fmt.Sprintf("live %s agent at pid %d", binary, rec.AgentPID)
	if rec.AgentTTY != "" && rec.AgentTTY != "unknown" {
		msg += " (tty " + rec.AgentTTY + ")"
	}
	if !rec.StartedAt.IsZero() {
		msg += " started " + rec.StartedAt.UTC().Format(time.RFC3339)
	}
	msg += "; record " + launch.ExistingPath(p.AgentDir)
	return &blockerReason{
		Source:  "launch",
		Message: msg,
		Hint:    "stop pid " + fmt.Sprintf("%d", rec.AgentPID) + " or attach its terminal",
	}, nil
}

func (p agentLaunchPreflight) inspectPresence(probe duplicateLaunchProbe) (*blockerReason, error) {
	path := filepath.Join(p.AgentDir, "presence.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, nil
	}
	var pres presenceFile
	if err := json.Unmarshal(data, &pres); err != nil {
		return nil, nil
	}
	if !strings.EqualFold(pres.Status, "active") {
		return nil, nil
	}
	if pres.LastSeen.IsZero() {
		return nil, nil
	}
	now := probe.Now()
	if now.Sub(pres.LastSeen) > presenceFreshness {
		return nil, nil
	}
	if pres.Handle != "" && pres.Handle != p.Handle {
		return nil, nil
	}
	// Zombie-heartbeat guard (#38, #44): presence "fresh" only means SOMETHING
	// has written the file in the last 90s. If we have launch+wake records on
	// disk and both their recorded PIDs are dead (or PID-reused by an unrelated
	// process), the file is a leftover from a writer that has since died — not
	// a live agent. inspectWakeLock and inspectLaunchRecord will (a) already
	// surface a blocker themselves when a live writer is present, and (b)
	// remove the stale lock/record so a subsequent up succeeds. We must not
	// keep the presence-only block once both signals are confirmed dead, or
	// the operator hits the same wall a clean down → up would otherwise clear.
	if p.presenceWriterIsKnownDead(probe) {
		return nil, nil
	}
	msg := fmt.Sprintf("active presence updated %s", pres.LastSeen.UTC().Format(time.RFC3339))
	msg += "; presence " + path
	return &blockerReason{
		Source:  "presence",
		Message: msg,
		Hint:    "wait for presence to expire, stop the running agent, or use --force-duplicate",
	}, nil
}

// presenceWriterIsKnownDead reports whether the on-disk wake.lock and
// launch.json both point at processes that are gone (dead PID or PID-reuse
// by an unrelated process). When either record is missing we cannot prove
// the writer is dead, so we keep the conservative behavior (presence
// blocks). Only when both records exist and both are confirmed dead do we
// treat the presence file as a zombie heartbeat.
func (p agentLaunchPreflight) presenceWriterIsKnownDead(probe duplicateLaunchProbe) bool {
	lockDead, lockKnown := p.wakeWriterDead(probe)
	if !lockKnown {
		return false
	}
	launchDead, launchKnown := p.launchWriterDead(probe)
	if !launchKnown {
		return false
	}
	return lockDead && launchDead
}

// wakeWriterDead inspects .wake.lock. Returns (dead, known): "known" is true
// when the file existed, parsed, and carried a usable PID; "dead" is true
// when the PID is gone or the live PID does not match an amq wake for this
// handle/root. A corrupt or unparseable lock is reported as unknown (not
// dead): we have no evidence either way and the conservative answer for
// the zombie-presence guard is to keep blocking. The stale-cleanup path in
// inspectWakeLock still removes the corrupt file on its own.
func (p agentLaunchPreflight) wakeWriterDead(probe duplicateLaunchProbe) (dead, known bool) {
	data, err := os.ReadFile(wakeLockPath(p.AgentDir))
	if err != nil {
		return false, false
	}
	var lock wakeLockFile
	if err := json.Unmarshal(data, &lock); err != nil {
		return false, false
	}
	if lock.PID <= 0 {
		return false, false
	}
	if !probe.PIDAlive(lock.PID) {
		return true, true
	}
	expectedRoot := p.Root
	if lock.Root != "" {
		expectedRoot = lock.Root
	}
	if !probe.ProcessMatch(lock.PID, wakeProcessMatcher(p.Handle, expectedRoot)) {
		return true, true
	}
	return false, true
}

// launchWriterDead inspects launch.json. Returns (dead, known): "known" is
// true when the record existed and parsed and carries a captured AgentPID;
// "dead" is true when that PID is gone or the live PID does not match the
// expected agent binary.
func (p agentLaunchPreflight) launchWriterDead(probe duplicateLaunchProbe) (dead, known bool) {
	rec, err := launch.Read(p.AgentDir)
	if err != nil {
		return false, false
	}
	if rec.AgentPID <= 0 {
		// No pid was captured at launch (e.g. codex seats). We cannot prove
		// the writer is dead from this record alone.
		return false, false
	}
	if !probe.PIDAlive(rec.AgentPID) {
		return true, true
	}
	binary := strings.TrimSpace(rec.Binary)
	if binary == "" {
		binary = p.Binary
	}
	if binary == "" || !probe.ProcessMatch(rec.AgentPID, agentProcessMatcher(binary)) {
		return true, true
	}
	return false, true
}

// wakeLockFile mirrors AMQ's wake.lock JSON shape.
type wakeLockFile struct {
	PID     int       `json:"pid"`
	TTY     string    `json:"tty,omitempty"`
	Root    string    `json:"root,omitempty"`
	Started time.Time `json:"started"`
}

type presenceFile struct {
	Schema   int       `json:"schema"`
	Handle   string    `json:"handle"`
	Status   string    `json:"status"`
	LastSeen time.Time `json:"last_seen"`
}

func wakeLockPath(agentDir string) string {
	return filepath.Join(agentDir, ".wake.lock")
}

// wakeProcessMatcher returns a predicate that recognizes an "amq wake"
// process for the given handle and expected root. The match anchors on the
// process command shape (`amq ... wake ... --me <handle>`) AND on the
// --root token when present. The --me value is parsed as a token, not a
// substring, so handle="cto" does not false-match a sibling wake with
// --me cto2 or --me=cto-extra. Root comparison tolerates relative /
// absolute equivalents but rejects unrelated roots. If the process args
// do not carry a --root token (root may be passed via env) the match
// accepts on the agent-dir anchoring of the lock alone.
func wakeProcessMatcher(handle, expectedRoot string) func(args string) bool {
	return func(args string) bool {
		if args == "" {
			return false
		}
		if !strings.Contains(args, "amq") {
			return false
		}
		if !strings.Contains(args, "wake") {
			return false
		}
		if handle != "" {
			me, found := extractMeFromArgs(args)
			if !found || me != handle {
				return false
			}
		}
		if expectedRoot == "" {
			return true
		}
		// Fast path: expectedRoot present literally in ps args, bounded on
		// the right by whitespace, end of string, or a quote character.
		// Survives path tokens that contain spaces (where strings.Fields
		// would otherwise split the value) without falsely accepting a
		// sibling root that has expectedRoot only as a prefix, e.g.
		// expected /a/issue-96 vs actual /a/issue-96-old.
		if rootSubstringMatchesBounded(args, expectedRoot) {
			return true
		}
		if psRoot, ok := extractRootFromArgs(args); ok {
			return rootsMatch(psRoot, expectedRoot)
		}
		return true
	}
}

// rootSubstringMatchesBounded reports whether expectedRoot occurs in args
// bounded on both sides by a value boundary. The right boundary is
// end-of-string or whitespace/quote; the left boundary is start-of-string
// or whitespace/quote/`=`. Without both checks the fast path would accept
// expected as a prefix of a longer, unrelated root (e.g. /a/issue-96 vs
// /a/issue-96-old) or as a suffix of a different absolute root (e.g.
// /a/issue-96 inside /tmp/a/issue-96).
func rootSubstringMatchesBounded(args, expectedRoot string) bool {
	if expectedRoot == "" {
		return false
	}
	for i := 0; ; {
		idx := strings.Index(args[i:], expectedRoot)
		if idx < 0 {
			return false
		}
		start := i + idx
		end := start + len(expectedRoot)
		if isRootBoundary(start, args, true) && isRootBoundary(end, args, false) {
			return true
		}
		i = start + 1
	}
}

// isRootBoundary reports whether position pos in args is a valid value
// boundary on the side indicated by left. left=true checks the character
// immediately before pos; left=false checks the character at pos. The valid
// boundary characters are end/start of string, whitespace, quote, and `=`
// on the left (matching --root=value).
func isRootBoundary(pos int, args string, left bool) bool {
	if left {
		if pos == 0 {
			return true
		}
		switch args[pos-1] {
		case ' ', '\t', '\n', '"', '\'', '=':
			return true
		}
		return false
	}
	if pos == len(args) {
		return true
	}
	switch args[pos] {
	case ' ', '\t', '\n', '"', '\'':
		return true
	}
	return false
}

// extractMeFromArgs pulls the --me <value> or --me=<value> token out of a
// ps args string. Returns ("", false) when no --me flag is present. The
// match is strict-equal on the token so handle="cto" does not match
// --me cto2 or --me=cto-extra.
func extractMeFromArgs(args string) (string, bool) {
	fields := strings.Fields(args)
	for i, f := range fields {
		if f == "--me" {
			if i+1 < len(fields) {
				return fields[i+1], true
			}
			return "", false
		}
		if strings.HasPrefix(f, "--me=") {
			return strings.TrimPrefix(f, "--me="), true
		}
	}
	return "", false
}

// extractRootFromArgs pulls the --root <value> or --root=<value> value out
// of a ps args string. Values that contain spaces are reconstructed by
// concatenating tokens after --root until the next flag token (anything
// starting with "--"). Returns ("", false) when no --root flag is present.
func extractRootFromArgs(args string) (string, bool) {
	fields := strings.Fields(args)
	for i, f := range fields {
		if f == "--root" {
			joined := joinNonFlagTokens(fields[i+1:])
			return joined, joined != ""
		}
		if strings.HasPrefix(f, "--root=") {
			head := strings.TrimPrefix(f, "--root=")
			tail := joinNonFlagTokens(fields[i+1:])
			if tail != "" {
				head = head + " " + tail
			}
			return head, true
		}
	}
	return "", false
}

// joinNonFlagTokens reassembles a path token that strings.Fields split on
// internal whitespace. It stops at the next flag token (prefix "--").
func joinNonFlagTokens(fields []string) string {
	var b strings.Builder
	for i, f := range fields {
		if strings.HasPrefix(f, "--") {
			break
		}
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(f)
	}
	return b.String()
}

// rootsMatch reports whether two AMQ root paths refer to the same location,
// tolerating one being relative and the other absolute. Both relative paths
// must match literally; otherwise the absolute side must end with the
// relative side's cleaned form.
func rootsMatch(actual, expected string) bool {
	a := filepath.Clean(actual)
	b := filepath.Clean(expected)
	if a == b {
		return true
	}
	if filepath.IsAbs(a) && filepath.IsAbs(b) {
		return canonicalRootForMatch(a) == canonicalRootForMatch(b)
	}
	return relativeRootMatchesAbsolute(a, b)
}

func canonicalRootForMatch(root string) string {
	if root == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		return resolved
	}
	return filepath.Clean(root)
}

func relativeRootMatchesAbsolute(a, b string) bool {
	if filepath.IsAbs(a) && filepath.IsAbs(b) {
		// Different absolute paths: not the same root.
		return false
	}
	rel, abs := a, b
	if filepath.IsAbs(rel) {
		rel, abs = abs, rel
	}
	if !filepath.IsAbs(abs) {
		// Neither side is absolute and they didn't match literally.
		return false
	}
	return abs == rel || strings.HasSuffix(abs, string(filepath.Separator)+rel)
}

// removeIfNotDryRun removes path unless the preflight is in dry-run mode.
// In dry-run, stale artifacts are detected but left untouched on disk.
func (p agentLaunchPreflight) removeIfNotDryRun(path string) {
	if p.DryRun {
		return
	}
	_ = os.Remove(path)
}

// agentProcessMatcher returns a predicate that recognizes the agent binary.
func agentProcessMatcher(binary string) func(args string) bool {
	binary = strings.TrimSpace(binary)
	return func(args string) bool {
		if args == "" || binary == "" {
			return false
		}
		base := filepath.Base(binary)
		// Match either the absolute or basename form. Avoid false positives
		// like "claude-mem" by anchoring on a token boundary.
		fields := strings.Fields(args)
		if len(fields) == 0 {
			return false
		}
		first := filepath.Base(fields[0])
		return first == base || first == binary
	}
}

// preflightTeam runs preflight for every member and aggregates blockers.
// It returns a single error containing all blockers when any member is
// blocked. force=true downgrades blockers to warnings (delegated to
// agentLaunchPreflight.check via Force).
func preflightTeam(plans []agentLaunchPreflight, probe duplicateLaunchProbe) error {
	var blockers []*duplicateBlocker
	for _, plan := range plans {
		blocker, err := plan.check(probe)
		if err != nil {
			return err
		}
		if blocker != nil {
			blockers = append(blockers, blocker)
		}
	}
	if len(blockers) == 0 {
		return nil
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("refusing to launch team: %d member(s) blocked by live-agent signals", len(blockers)))
	for _, b := range blockers {
		lines = append(lines, "")
		lines = append(lines, b.Error())
	}
	return errors.New(strings.Join(lines, "\n"))
}

// currentLaunchTTY returns a best-effort identifier for the launching TTY.
// This is recorded in launch.json so duplicate-launch errors can name the
// terminal that owns an existing agent.
func currentLaunchTTY() string {
	if tty := strings.TrimSpace(os.Getenv("TTY")); tty != "" {
		return tty
	}
	for _, fd := range []string{"/dev/stdin", "/dev/stdout", "/dev/stderr"} {
		if name, err := os.Readlink(fd); err == nil && name != "" {
			return name
		}
	}
	return ""
}
