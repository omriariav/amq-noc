package state

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-noc/internal/launch"
)

// Build constructs a Snapshot by scanning the filesystem under baseRoot. It
// resolves NOTHING via subprocess: the caller is responsible for resolving the
// AMQ base root once (e.g. a single `amq env` call) and passing it in. Per
// agent, liveness and wake-health are computed using the injected probe and the
// probe's clock; `amq` is never invoked.
//
// projectRoot is the directory the agents were launched from; it is only used
// by the launch scanners to infer legacy records and is otherwise opaque here.
//
// Build uses the default triage thresholds and operator handle. To tune those
// (the at-risk/heartbeat/review windows or a non-"user" operator handle), use
// BuildWithThresholds.
func Build(projectRoot, baseRoot string, probe Probe) (Snapshot, error) {
	return BuildWithThresholds(projectRoot, baseRoot, probe, Thresholds{})
}

// BuildWithThresholds is Build with explicit triage thresholds. Zero-valued
// fields fall back to the documented defaults, so callers may override only the
// windows or operator handle they care about. The probe's clock is the single
// source of "now" for both liveness and the coordination model.
func BuildWithThresholds(projectRoot, baseRoot string, probe Probe, th Thresholds) (Snapshot, error) {
	probe = withDefaults(probe)
	th = withThresholdDefaults(th)

	entries, err := launch.ScanRestorableEntriesInRoot(projectRoot, baseRoot)
	if err != nil {
		return Snapshot{}, err
	}

	// Group entries by session, preserving a stable session root per name.
	type bucket struct {
		name    string
		profile string
		root    string
		agents  []Agent
	}
	bySession := map[string]*bucket{}
	var order []string
	for _, e := range entries {
		name := e.Record.Session
		profile := normalizedLaunchProfile(e.Record.TeamProfile)
		key := sessionBucketKey(profile, name)
		root := sessionRoot(projectRoot, baseRoot, e.Record)
		b, ok := bySession[key]
		if !ok {
			b = &bucket{name: name, profile: profile, root: root}
			bySession[key] = b
			order = append(order, key)
		}
		b.agents = append(b.agents, classifyAgent(e, probe))
	}

	sort.Strings(order)
	now := probe.Now()
	sessions := make([]Session, 0, len(order))
	var snapRollup TriageRollup
	for _, key := range order {
		b := bySession[key]
		sortAgents(b.agents)

		coord := coordinateSession(b.root, b.agents, now, th)
		coord.AttentionQueue = buildAttentionQueue(coord.Threads, b.agents, th, b.name)
		sessAttention, unowned := attachAttention(b.agents, coord.Threads)
		sessions = append(sessions, Session{
			Name:             b.name,
			TeamProfile:      b.profile,
			Root:             b.root,
			Agents:           b.agents,
			Coordination:     coord,
			Rollup:           coord.Rollup,
			Attention:        sessAttention,
			UnownedAttention: unowned,
		})
		snapRollup.Add(coord.Rollup)
	}

	return Snapshot{BaseRoot: baseRoot, Sessions: sessions, Rollup: snapRollup}, nil
}

// coordinateSession scans every agent's mailbox in a session, then collapses the
// observed messages into the derived coordination model (threads, edges,
// timeline, triage). The scan reads only the filesystem under each agent dir;
// no subprocess is spawned. Warnings (torn files, schema mismatches) are
// aggregated onto the returned Coordination.
func coordinateSession(sessionRoot string, agents []Agent, now time.Time, th Thresholds) Coordination {
	var msgs []Message
	var warns []Warning
	scanned := map[string]bool{}
	for _, a := range agents {
		if a.AgentDir == "" {
			continue
		}
		scanned[filepath.Clean(a.AgentDir)] = true
		m, w := scanMailbox(a.AgentDir, a.Handle, func() time.Time { return now })
		msgs = append(msgs, m...)
		warns = append(warns, w...)
	}
	if op := strings.TrimSpace(th.OperatorHandle); op != "" && strings.TrimSpace(sessionRoot) != "" {
		dir := filepath.Join(sessionRoot, "agents", op)
		if !scanned[filepath.Clean(dir)] && dirExists(dir) {
			m, w := scanMailbox(dir, op, func() time.Time { return now })
			msgs = append(msgs, m...)
			warns = append(warns, w...)
		}
	}
	return buildCoordination(collapseInput{messages: msgs, agents: agents, warnings: warns}, now, th)
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// sessionRoot derives the directory that anchors a session. It prefers the
// record's own Root (which the launcher captured); when absent it derives it
// from baseRoot + session name, matching the AMQ layouts the scanners walk.
func sessionRoot(projectRoot, baseRoot string, rec launch.Record) string {
	if rec.Root != "" {
		return absoluteLaunchRoot(projectRoot, rec.CWD, rec.Root)
	}
	if rec.Session == "" {
		return baseRoot
	}
	if profile := normalizedLaunchProfile(rec.TeamProfile); profile != "" && profile != "default" {
		return filepath.Join(baseRoot, profile, rec.Session)
	}
	return filepath.Join(baseRoot, rec.Session)
}

func sessionBucketKey(profile, session string) string {
	return normalizedLaunchProfile(profile) + "\x00" + session
}

func normalizedLaunchProfile(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return "default"
	}
	return profile
}

func absoluteLaunchRoot(projectRoot, launchCWD, root string) string {
	root = strings.TrimSpace(root)
	if root == "" || filepath.IsAbs(root) {
		return root
	}
	base := strings.TrimSpace(launchCWD)
	if base == "" {
		base = strings.TrimSpace(projectRoot)
	}
	if base == "" {
		return root
	}
	return filepath.Clean(filepath.Join(base, root))
}

func sortAgents(agents []Agent) {
	sort.Slice(agents, func(i, j int) bool {
		if agents[i].Role != agents[j].Role {
			return agents[i].Role < agents[j].Role
		}
		if agents[i].Handle != agents[j].Handle {
			return agents[i].Handle < agents[j].Handle
		}
		return agents[i].Source < agents[j].Source
	})
}

// classifyAgent computes the run-state for a single discovered launch entry.
// It reads launch.json (already in the entry), presence.json, and .wake.lock
// from the agent dir, then derives Liveness + WakeHealth via the probe. It
// performs NO subprocess calls beyond the probe (which the caller controls).
func classifyAgent(e launch.Entry, probe Probe) Agent {
	rec := e.Record
	a := Agent{
		Handle:           rec.Handle,
		Engine:           rec.Binary,
		Role:             rec.Role,
		AgentPID:         rec.AgentPID,
		Conversation:     rec.Conversation,
		AgentDir:         e.AgentDir,
		Source:           e.Source,
		LaunchRecordPath: launch.ExistingPath(e.AgentDir),
		TeamProfile:      rec.TeamProfile,
	}

	pres, presErr := readPresence(e.AgentDir)
	if presErr == nil {
		a.Presence = pres.Status
		a.LastSeen = pres.LastSeen
	}

	// --- Agent PID liveness (the authoritative live signal). ---
	agentPIDAlive := false
	agentPIDLive := false
	if rec.AgentPID > 0 && probe.PIDAlive(rec.AgentPID) {
		agentPIDAlive = true
		if rec.Binary == "" || probe.ProcessMatch(rec.AgentPID, agentProcessMatcher(rec.Binary)) {
			agentPIDLive = true
		}
	}

	// --- Presence freshness (a recently-touched mailbox heartbeat). ---
	presenceActiveFresh := false
	presenceOfflineFresh := false
	mailboxRecentlyTouched := false
	if presErr == nil && !pres.LastSeen.IsZero() {
		recent := probe.Now().Sub(pres.LastSeen) <= PresenceFreshness
		handleOK := pres.Handle == "" || pres.Handle == rec.Handle
		if recent && handleOK {
			mailboxRecentlyTouched = true
			if strings.EqualFold(pres.Status, "active") {
				presenceActiveFresh = true
			}
			if strings.EqualFold(pres.Status, "offline") {
				presenceOfflineFresh = true
			}
		}
	}

	// --- Wake state (PID is the WAKE pid, not the agent pid). ---
	wakePID, wakeAlive := wakeState(e.AgentDir, rec, probe)
	a.WakePID = wakePID

	// --- Derive Liveness. ---
	hasAnyDiskSignal := rec.AgentPID > 0 || wakePID > 0 || presErr == nil
	switch {
	case agentPIDLive:
		a.Liveness = LivenessAlive
	case wakeAlive:
		a.Liveness = LivenessWakeLive
	case presenceActiveFresh:
		// Presence says active and fresh. If the recorded agent PID is confirmed
		// dead, surface the explicit dead-process / live-mailbox case. If the PID
		// is alive but process-args verification failed or was unavailable, trust
		// the fresh active mailbox instead of misclassifying a live agent.
		if rec.AgentPID > 0 && !agentPIDAlive {
			a.Liveness = LivenessDeadMailboxLive
		} else {
			a.Liveness = LivenessAlive
		}
	case mailboxRecentlyTouched && rec.AgentPID > 0 && !presenceOfflineFresh:
		// Mailbox touched recently (presence not "active", e.g. "idle") yet the
		// recorded agent PID is dead: something is still writing while the agent
		// is gone. Distinct dead-mailbox-live signal. A fresh "offline" status is
		// excluded: that write is the expected final act of a clean stop, not a
		// zombie heartbeat, so it falls through to stale below instead of pinning
		// the agent visible-online for the freshness window (amq-squad#109).
		a.Liveness = LivenessDeadMailboxLive
	case !hasAnyDiskSignal:
		a.Liveness = LivenessMissing
	case hasLiveLeaningSignal(rec, wakePID, wakeAlive):
		// A live-pointing disk signal exists (recorded agent PID, a wake lock,
		// or an alive wake) but nothing verifies a running agent: stale.
		a.Liveness = LivenessStale
	default:
		a.Liveness = LivenessDead
	}

	// --- Derive WakeHealth (only when the agent looks active enough). ---
	a.WakeHealth = wakeHealth(a.Liveness, wakePID, wakeAlive)

	return a
}

// hasLiveLeaningSignal reports whether disk carries a signal that, by itself,
// suggests an agent MAY have been live (a recorded agent PID or a wake lock
// PID). Used to distinguish "stale" (had a live-pointing signal that failed
// verification) from "dead" (signals expired entirely).
func hasLiveLeaningSignal(rec launch.Record, wakePID int, wakeAlive bool) bool {
	return rec.AgentPID > 0 || wakePID > 0 || wakeAlive
}

// wakeState reads .wake.lock and returns the wake PID (0 when absent/corrupt)
// and whether that PID verifies as a live amq wake for this handle/root.
func wakeState(agentDir string, rec launch.Record, probe Probe) (pid int, alive bool) {
	lock, err := readWakeLock(agentDir)
	if err != nil {
		return 0, false
	}
	if lock.PID <= 0 {
		return 0, false
	}
	if !probe.PIDAlive(lock.PID) {
		return lock.PID, false
	}
	expectedRoot := rec.Root
	if lock.Root != "" {
		expectedRoot = lock.Root
	}
	if !probe.ProcessMatch(lock.PID, wakeProcessMatcher(rec.Handle, expectedRoot)) {
		return lock.PID, false
	}
	return lock.PID, true
}

// wakeHealth maps the raw wake state into a WakeHealth label, mirroring
// list.go's wakeHealthForEntry: only meaningful when the agent looks active.
//   - WakePID/"pid:N" when a live wake verified
//   - "missing" when active but no usable wake lock
//   - "stale"   when a wake lock exists but its PID is dead/unrelated
//   - "" (none) when the agent does not look active enough to investigate
func wakeHealth(live Liveness, wakePID int, wakeAlive bool) WakeHealth {
	if !looksActiveForWake(live) {
		return WakeHealthNone
	}
	if wakeAlive && wakePID > 0 {
		return WakeHealth("pid:" + itoa(wakePID))
	}
	if wakePID > 0 {
		return WakeHealthStale
	}
	return WakeHealthMissing
}

// looksActiveForWake reports whether a liveness state is fresh enough that wake
// health is worth reporting. Mirrors list.go's looksActive gate: we only chase
// wake state for agents that are alive or whose mailbox is being touched while
// the process is gone.
func looksActiveForWake(live Liveness) bool {
	switch live {
	case LivenessAlive, LivenessWakeLive, LivenessDeadMailboxLive:
		return true
	default:
		return false
	}
}

// withDefaults fills any nil probe seam with the production implementation so
// callers may pass a partially-specified probe (and tests may override only the
// seams they care about).
func withDefaults(p Probe) Probe {
	if p.PIDAlive == nil {
		p.PIDAlive = defaultPIDAlive
	}
	if p.ProcessMatch == nil {
		p.ProcessMatch = defaultProcessMatch
	}
	if p.Now == nil {
		p.Now = DefaultProbe.Now
	}
	return p
}
