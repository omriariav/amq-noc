// Package state provides a reusable, fast, read-only session-discovery and
// per-agent liveness foundation shared by the `status` board and the upcoming
// Mission Control console.
//
// The package is pure stdlib (os, os/exec, encoding/json, path/filepath, time,
// strings). It deliberately does NOT import internal/cli to avoid an import
// cycle; the status / Mission Control surfaces live there and will consume this
// package, not the other way around. It MAY reuse internal/launch scanners.
//
// Two perf/correctness rules drive the design:
//
//   - DISCOVERY shells out at most ONCE for the AMQ base root (the caller is
//     expected to resolve it and pass it in). Sessions and per-agent dirs are
//     then found by SCANNING THE FILESYSTEM under that base root, reusing the
//     existing launch.ScanRestorableEntriesInRoot globbing. The historical
//     landmine — running `amq env` once per member during classification — is
//     eliminated: Snapshot never execs `amq`.
//
//   - LIVENESS is COMPUTED, never read from presence.status (which lies: a
//     running agent can show "offline" and a long-dead agent can show
//     "active"). The algorithm mirrors the canonical liveness already in the
//     repo (internal/cli/preflight.go presenceFreshness + PID-liveness probe,
//     internal/cli/list.go looksActive / wakeHealthForEntry).
package state

import (
	"os"
	"os/exec"
	"strings"
	"time"
)

// PresenceFreshness defines how recently a presence.json must have been updated
// for it to count as a live signal. Mirrors internal/cli.presenceFreshness so
// this package agrees with status / preflight / list about what "fresh" means.
const PresenceFreshness = 90 * time.Second

// Liveness is the computed run-state of a single agent. It is derived from the
// launch record PID, the agent process command line, and presence — NOT read
// verbatim from presence.status.
type Liveness string

const (
	// LivenessAlive: launch.json agent_pid is alive (signal-0) AND its process
	// args match the agent binary; OR presence.status=="active" and fresh with
	// no contradicting dead PID. The agent is running.
	LivenessAlive Liveness = "alive"

	// LivenessWakeLive: the AMQ wake helper PID is verified alive for this
	// handle/root, but the agent PID itself is not verified. The agent is
	// reachable enough to surface AMQ messages, but should be re-registered.
	LivenessWakeLive Liveness = "wake-live"

	// LivenessStale: a presence/launch/wake signal exists on disk but none of
	// them verify as a running agent for this handle, and there is no evidence
	// the mailbox is being actively touched. The disk record is leftover.
	LivenessStale Liveness = "stale"

	// LivenessDead: the agent PID is dead (or never recorded) and presence is
	// neither fresh-active nor recently touched. The agent is gone.
	LivenessDead Liveness = "dead"

	// LivenessMissing: no launch record, no wake lock, no presence file. The
	// agent dir has no live signals at all.
	LivenessMissing Liveness = "missing"

	// LivenessDeadMailboxLive is the explicit, distinct dead-process /
	// live-mailbox case: the recorded agent PID is dead (or unverifiable) yet
	// the mailbox/presence was touched within PresenceFreshness. Something is
	// still writing to the mailbox while the agent process is gone. This MUST
	// NOT collapse into "stale" or "alive": it is its own signal that the
	// operator likely has a zombie heartbeat or a detached wake.
	LivenessDeadMailboxLive Liveness = "dead-mailbox-live"
)

// WakeHealth labels the state of the AMQ wake helper for an agent, derived from
// .wake.lock. Remember the wake PID is NOT the agent PID.
type WakeHealth string

const (
	// WakeHealthNone means no wake state was inspected (the agent does not look
	// active enough to bother) — empty label.
	WakeHealthNone WakeHealth = ""

	// WakeHealthMissing: the agent looks active but no .wake.lock was found.
	WakeHealthMissing WakeHealth = "missing"

	// WakeHealthStale: a .wake.lock exists but its PID is dead or the live PID
	// is an unrelated process (PID reuse / wrong root).
	WakeHealthStale WakeHealth = "stale"
)

// WakePID is the WakeHealth value when a live wake process was verified. Use
// IsWakeAlive / WakePIDValue helpers rather than parsing the string.

// Agent is a single discovered agent plus its computed run-state.
type Agent struct {
	Handle       string
	Engine       string // the launch binary: "claude", "codex", ...
	Role         string
	AgentPID     int
	WakePID      int
	Liveness     Liveness
	WakeHealth   WakeHealth
	LastSeen     time.Time
	Presence     string // raw presence.status as found on disk (informational)
	Conversation string
	AgentDir     string
	Source       string // launch source label, e.g. "launch.json" or "amq history"
	TeamProfile  string // launch team profile; empty means the default profile
}

// Session groups the agents discovered under one AMQ session root, plus the
// derived read-only coordination model (threads/edges/timeline/triage) the
// Mission Control console renders. Coordination is computed by collapsing the
// per-agent mailboxes; PR1's discovery + liveness fields are unchanged.
type Session struct {
	Name         string // workstream/session name; "" for the rootless layout
	Root         string // the session root directory (base root or base root/<name>)
	Agents       []Agent
	Coordination Coordination // derived threads/edges/timeline/triage for this session
	Rollup       TriageRollup // triage headline for this session
}

// Snapshot is the full read-only view of all discovered sessions and agents,
// plus a snapshot-wide triage rollup aggregating every session.
type Snapshot struct {
	BaseRoot string
	Sessions []Session
	Rollup   TriageRollup // snapshot-wide triage headline across all sessions
}

// Probe abstracts PID liveness and process-args inspection so tests can supply
// deterministic fakes. It mirrors internal/cli.duplicateLaunchProbe so the two
// packages stay semantically aligned.
type Probe struct {
	// PIDAlive reports whether pid is a live process (signal-0 probe).
	PIDAlive func(pid int) bool
	// ProcessMatch reports whether the live pid's command line satisfies
	// predicate (typically a ps -o args= read).
	ProcessMatch func(pid int, predicate func(args string) bool) bool
	// Now returns the current time. Injected so liveness freshness is
	// deterministic in tests and so the package never depends on a real
	// wall-clock that the sandbox may forbid.
	Now func() time.Time
}

// DefaultProbe is the production probe: real signal-0 liveness, a real `ps`
// read for process matching, and the real wall clock.
var DefaultProbe = Probe{
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
	return proc.Signal(syscallSignalZero) == nil
}

// defaultProcessMatch reads the process command line for pid via `ps` and
// applies predicate. Returns false on any error (best effort).
func defaultProcessMatch(pid int, predicate func(args string) bool) bool {
	if pid <= 0 || predicate == nil {
		return false
	}
	cmd := exec.Command("ps", "-o", "args=", "-p", itoa(pid))
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return predicate(strings.TrimSpace(string(out)))
}
