package noc

import (
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/omriariav/amq-noc/internal/state"
)

// ProjectSnapshot is one discovered amq-squad project plus its computed
// state.Snapshot. Warning is non-empty when the project could not be collected
// (e.g. the build failed); in that case Snap is the zero snapshot. Collect
// NEVER drops a discovered project — a failed one is surfaced with its warning
// so the operator sees it instead of it silently vanishing.
type ProjectSnapshot struct {
	Project        string         // basename of Dir, the human-facing project label
	Dir            string         // absolute project directory
	TeamConfigured bool           // true when .amq-squad contains any team profile
	DefaultTeam    bool           // true when .amq-squad/team.json exists
	Profiles       []string       // valid team profiles, with "default" first when present
	SessionStore   bool           // true when .agent-mail exists
	SessionNames   []string       // existing AMQ session directories under .agent-mail
	Candidate      bool           // true when this is an unconfigured team-home candidate
	Snap           state.Snapshot // per-project discovery + coordination snapshot
	Warning        string         // non-empty when collection failed for this project
}

// MultiSnapshot is the full NOC view across every discovered project, plus a
// global triage rollup summing all per-project rollups and the observation
// time. It is the single read-only datum a command-center surface renders.
type MultiSnapshot struct {
	Roots      []string
	Projects   []ProjectSnapshot
	Rollup     state.TriageRollup // global headline across all projects
	ObservedAt time.Time
	// LiveProjects is the count of projects that are RUNNING (>=1 alive agent).
	// It leads the headline: "what is alive" before "what has the most noise".
	LiveProjects int
	// LastActivity is the freshest last-event time across every thread in every
	// project — the "last activity across all squads" summary. Zero when no
	// project recorded any thread activity.
	LastActivity time.Time
}

// Collect discovers every amq-squad project or candidate team-home under roots
// (bounded by depth) and builds a per-project state.Snapshot via
// state.BuildWithThresholds, scanning each project's <projectDir>/.agent-mail
// container when one exists. The per-project triage rollups are summed into the
// global MultiSnapshot.Rollup.
//
// Collect is NEVER fatal: discovery failures and per-project build failures are
// recorded (a failed project becomes a ProjectSnapshot with a Warning) rather
// than aborting the whole collection. ObservedAt is taken from the probe clock
// so the result is deterministic under an injected probe.
//
// Projects are returned attention-first by the COMPOSITE tier sort
// (sortProjectsAttentionFirst): needs-you, then RUNNING-with-live-at-risk/blocked,
// then running-healthy, then recently-active-but-stopped, then stale/archived;
// within a tier, freshest last-activity first. This rewards LIVENESS — a running
// squad active just now leads; a stopped squad whose only blocks are days old
// (past the stale window) sinks to the bottom rather than outranking live work.
func Collect(roots []string, depth int, probe state.Probe, th state.Thresholds) MultiSnapshot {
	probe = withProbeDefaults(probe)
	observedAt := probe.Now()

	ms := MultiSnapshot{
		Roots:      append([]string(nil), roots...),
		ObservedAt: observedAt,
	}

	dirs, err := Discover(roots, depth)
	if err != nil {
		// Discovery itself failed: record a single synthetic warning project so
		// the failure is visible, but still return a usable (empty) snapshot.
		ms.Projects = []ProjectSnapshot{{
			Project: "(discovery)",
			Warning: "discover: " + err.Error(),
		}}
		return ms
	}

	for _, dir := range dirs {
		baseRoot := filepath.Join(dir, AgentMailDirName)
		defaultTeam := hasDefaultTeamProfile(dir)
		ps := ProjectSnapshot{
			Project:        filepath.Base(dir),
			Dir:            dir,
			TeamConfigured: hasTeamProfile(dir),
			DefaultTeam:    defaultTeam,
			Profiles:       listTeamProfiles(dir, defaultTeam),
			SessionStore:   dirExists(filepath.Join(dir, AgentMailDirName)),
			SessionNames:   listAMQSessionNames(dir),
		}
		ps.Candidate = !ps.TeamConfigured && !ps.SessionStore
		snap, buildErr := state.BuildWithThresholds(dir, baseRoot, probe, th)
		if buildErr != nil {
			ps.Warning = buildErr.Error()
		} else {
			ps.Snap = snap
			ms.Rollup.Add(snap.Rollup)
			if hasRunningAgent(snap) {
				ms.LiveProjects++
			}
			if la := projectLastActivity(snap); la.After(ms.LastActivity) {
				ms.LastActivity = la
			}
		}
		ms.Projects = append(ms.Projects, ps)
	}

	sortProjectsAttentionFirst(ms.Projects, observedAt, staleWindow(th))
	return ms
}

func hasTeamProfile(projectDir string) bool {
	return hasTeamProfileMarker(filepath.Join(projectDir, SquadDirName))
}

func hasDefaultTeamProfile(projectDir string) bool {
	info, err := os.Stat(filepath.Join(projectDir, SquadDirName, "team.json"))
	return err == nil && !info.IsDir()
}

func listTeamProfiles(projectDir string, defaultTeam bool) []string {
	out := []string{}
	if defaultTeam {
		out = append(out, "default")
	}
	dir := filepath.Join(projectDir, SquadDirName, "teams")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	named := []string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if filepath.Ext(name) != ".json" {
			continue
		}
		profile := name[:len(name)-len(".json")]
		if !validNamedTeamProfile(profile) {
			continue
		}
		named = append(named, profile)
	}
	sort.Strings(named)
	return append(out, named...)
}

func listAMQSessionNames(projectDir string) []string {
	dir := filepath.Join(projectDir, AgentMailDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := []string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "" || name == ".archive" {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func validNamedTeamProfile(s string) bool {
	if s == "" || s == "default" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// staleWindow resolves the configured stale-after duration, defaulted. The NOC
// attention sort + recently-active check key off the SAME window the per-thread
// staleness used in state.BuildWithThresholds, so the ranking and the rendered
// stale markers agree.
func staleWindow(th state.Thresholds) time.Duration {
	if th.StaleAfter > 0 {
		return th.StaleAfter
	}
	return state.DefaultStaleAfter
}

// projectLastActivity returns the freshest thread last-event time across all of
// a project's sessions (zero when the project has no thread activity).
func projectLastActivity(snap state.Snapshot) time.Time {
	var last time.Time
	for _, sess := range snap.Sessions {
		for _, th := range sess.Coordination.Threads {
			if th.LastEventAt.After(last) {
				last = th.LastEventAt
			}
		}
	}
	return last
}

// Attention tiers, most urgent (lowest) first. This is the composite sort that
// makes the NOC reward LIVENESS, not ancient noise: a RUNNING squad with live
// at-risk threads outranks a long-STOPPED squad whose only "blocked" threads are
// stale. The tiers (mirroring PR13b's spec, lowest = most urgent):
//
//	tierNeedsYou      T1: a human action is required (never decays).
//	tierRunningAtRisk T2: RUNNING (>=1 alive) AND carries LIVE at-risk/blocked.
//	tierRunningHealthy T3: running, nothing outstanding (or only stale noise).
//	tierRecentlyActive T4: stopped but recently active (< staleWindow) with open
//	                       threads — worth a glance, not yet ancient.
//	tierStale         T5: stopped / stale / archived (only stale or no threads).
const (
	tierNeedsYou = iota
	tierRunningAtRisk
	tierRunningHealthy
	tierRecentlyActive
	tierStale
)

// attentionTier ranks a project into one of the composite tiers above. now is
// the observation clock (injected) used to decide "recently active".
func attentionTier(ps ProjectSnapshot, now time.Time, staleWindow time.Duration) int {
	r := ps.Snap.Rollup
	running := hasRunningAgent(ps.Snap)

	switch {
	case r.NeedsYou > 0:
		return tierNeedsYou
	case running && (r.AtRisk > 0 || r.Blocked > 0 || r.Gated > 0):
		// LIVE at-risk/blocked on a running squad — the live-attention case.
		return tierRunningAtRisk
	case running:
		// Running but nothing LIVE outstanding (stale at-risk/blocked don't count).
		return tierRunningHealthy
	case projectRecentlyActive(ps.Snap, now, staleWindow):
		// Stopped, but its freshest thread is within the stale window and it still
		// has open (non-resolved) threads — recently put down, still worth a look.
		return tierRecentlyActive
	default:
		// Stopped + stale/archived (or no threads at all): the bottom tier where
		// 30-day-old blocked threads belong.
		return tierStale
	}
}

// projectRecentlyActive reports whether a stopped project was active within the
// stale window AND still has an open (non-resolved) thread. Such a project is
// "recently put down" rather than ancient.
func projectRecentlyActive(snap state.Snapshot, now time.Time, staleWindow time.Duration) bool {
	la := projectLastActivity(snap)
	if la.IsZero() || staleWindow <= 0 {
		return false
	}
	if now.Sub(la) > staleWindow {
		return false
	}
	for _, sess := range snap.Sessions {
		for _, th := range sess.Coordination.Threads {
			if th.Status != state.ThreadResolved {
				return true
			}
		}
	}
	return false
}

// hasRunningAgent reports whether any agent in the snapshot is currently
// operational (alive, wake-live, or dead-mailbox-live).
func hasRunningAgent(snap state.Snapshot) bool {
	for _, sess := range snap.Sessions {
		for _, ag := range sess.Agents {
			if ag.Liveness == state.LivenessAlive || ag.Liveness == state.LivenessWakeLive || ag.Liveness == state.LivenessDeadMailboxLive {
				return true
			}
		}
	}
	return false
}

// sortProjectsAttentionFirst orders projects by composite attention tier, then
// WITHIN a tier by last-activity DESC (freshest first), then by name. This is
// the net effect the brief demands on real data: a running squad active "just
// now" ranks at/near the top; a stopped squad whose only blocks are 30 days old
// drops to the bottom tier.
func sortProjectsAttentionFirst(projects []ProjectSnapshot, now time.Time, staleWindow time.Duration) {
	sort.SliceStable(projects, func(i, j int) bool {
		ti := attentionTier(projects[i], now, staleWindow)
		tj := attentionTier(projects[j], now, staleWindow)
		if ti != tj {
			return ti < tj
		}
		// Freshest activity first within a tier.
		ai := projectLastActivity(projects[i].Snap)
		aj := projectLastActivity(projects[j].Snap)
		if !ai.Equal(aj) {
			return ai.After(aj)
		}
		return projects[i].Project < projects[j].Project
	})
}

// withProbeDefaults fills any nil probe seam with the production probe, so a
// caller may pass the zero Probe (e.g. NOC entrypoints) and still get a real
// clock for ObservedAt. state.BuildWithThresholds also fills defaults, but we
// need a non-nil Now here for the snapshot timestamp.
func withProbeDefaults(p state.Probe) state.Probe {
	if p.PIDAlive == nil {
		p.PIDAlive = state.DefaultProbe.PIDAlive
	}
	if p.ProcessMatch == nil {
		p.ProcessMatch = state.DefaultProbe.ProcessMatch
	}
	if p.Now == nil {
		p.Now = state.DefaultProbe.Now
	}
	return p
}
