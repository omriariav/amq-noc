// Package console — noc_palette.go: the COMMAND PALETTE (2.3 awareness+scale).
//
// The palette is a fuzzy "jump to any agent or team in ~2 keystrokes" overlay
// over every discovered project, team, and agent in the MultiSnapshot. Selecting
// a running agent performs the same gated tmux jump the tree's enter/J does.
// Selecting a stopped agent or a team row focuses an existing tmux window if
// present, or sets a suggest-up note. Selecting a creation action opens the same
// preview-gated T/N flow used by the tree. AMQ bus actions open in-NOC ops,
// inbox, receipts, drain, DLQ retry, and single-agent resume flows, or show
// exact command guidance. It never mutates squad state directly.
//
// Open with 'p'. Type to fuzzy-filter (subsequence match, case-insensitive) over
// the display label plus action aliases such as "create team" or "start
// session". Role-market and inbox rows make team and bus operations discoverable
// before selecting roles. Up/down (or ctrl+n / ctrl+p) move the selection within
// results; enter SELECTS; esc closes.
package console

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/state"
)

// paletteKind distinguishes jump targets from project rows and command actions.
type paletteKind int

const (
	palAgent paletteKind = iota
	palTeam
	palProject
	palAction
)

type paletteAction int

const (
	palNoAction paletteAction = iota
	palDoctor
	palHistory
	palResumePlan
	palForkPlan
	palStatus
	palThreads
	palBrief
	palBriefSeed
	palStop
	palResume
	palRestart
	palRoles
	palTeamRules
	palTeamProfiles
	palDeleteTeam
	palAMQOps
	palAMQWho
	palAMQEnv
	palAMQCleanup
	palPresence
	palThreadContext
	palThreadContextAny
	palReadNeedsYou
	palApprove
	palReply
	palDeny
	palBroadcast
	palInbox
	palDLQ
	palDLQRead
	palDLQRetry
	palDLQPurge
	palDLQRetryAll
	palReceipts
	palReceiptsWait
	palMessage
	palMessageWait
	palDrain
	palAgentResume
	palNewSession
	palNewTeam
	palSyncPointers
)

// paletteItem is one fuzzy candidate: a project, action, agent, or team across
// all projects/sessions. It carries everything selection needs so the handler
// never has to re-walk the snapshot (which may have moved).
type paletteItem struct {
	kind    paletteKind
	label   string // "project/session/role" — the fuzzy-match + display string
	search  string // hidden aliases for common operator verbs
	running bool   // an agent whose liveness is alive, or a team with ≥1 alive agent
	action  paletteAction
	// Carried context for the jump/focus.
	project     string // project label
	projectDir  string
	snapshot    noc.ProjectSnapshot
	session     string
	sessionRoot string
	profile     string
	agent       state.Agent // valid for palAgent
	thread      state.ThreadSummary
}

// paletteState is the open command palette: the typed query + the live-filtered
// results + the selection within them. nil on m means the palette is closed.
type paletteState struct {
	query  string
	items  []paletteItem // ALL candidates, snapshot at open time (stable while open)
	cursor int           // selection index INTO the filtered results
}

// filtered returns the items whose label or hidden search text fuzzy-matches the
// query (subsequence, case-insensitive). An empty query matches everything.
func (p *paletteState) filtered() []paletteItem {
	if strings.TrimSpace(p.query) == "" {
		return p.items
	}
	q := strings.ToLower(p.query)
	tokens := strings.Fields(q)
	type scoredItem struct {
		item  paletteItem
		score int
	}
	scored := make([]scoredItem, 0, len(p.items))
	for _, it := range p.items {
		text := strings.ToLower(it.searchText())
		if !paletteQueryMatches(text, tokens, q) {
			continue
		}
		scored = append(scored, scoredItem{item: it, score: paletteQueryScore(it, text, tokens)})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})
	out := make([]paletteItem, 0, len(scored))
	for _, it := range scored {
		out = append(out, it.item)
	}
	return out
}

func paletteQueryMatches(text string, tokens []string, raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return true
	}
	if len(tokens) <= 1 {
		return fuzzySubsequence(text, raw)
	}
	for _, token := range tokens {
		if strings.Contains(text, token) {
			continue
		}
		return false
	}
	return true
}

func paletteQueryScore(it paletteItem, text string, tokens []string) int {
	score := 0
	words := strings.Fields(text)
	actionLabel := strings.ToLower(paletteActionLabel(it))
	actionWords := strings.Fields(actionLabel)
	project := strings.ToLower(it.project)
	session := strings.ToLower(it.session)
	for _, token := range tokens {
		if stringSliceContains(words, token) {
			score += 2
		} else if strings.Contains(text, token) {
			score++
		}
		if it.kind == palAction && stringSliceContains(actionWords, token) {
			score += 5
		}
		if project != "" && strings.Contains(project, token) {
			score += 4
		}
		if session != "" && strings.Contains(session, token) {
			score += 3
		}
	}
	return score
}

func (it paletteItem) searchText() string {
	if strings.TrimSpace(it.search) == "" {
		return it.label
	}
	return it.label + " " + it.search
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// fuzzySubsequence reports whether every rune of needle appears in haystack in
// order (a classic fuzzy/subsequence match). Both are expected lowercased by the
// caller. An empty needle matches anything.
func fuzzySubsequence(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	ni := 0
	nr := []rune(needle)
	for _, hc := range haystack {
		if hc == nr[ni] {
			ni++
			if ni == len(nr) {
				return true
			}
		}
	}
	return false
}

// buildPaletteItems flattens the snapshot into palette candidates: every project
// row, useful creation actions, every team (session), and every agent. Project
// and action rows are what make empty configured projects and candidate git repos
// reachable from the fastest surface. Ordering is attention-first by project
// (snapshot order), then sessions + agents sorted the same way the tree sorts
// them, so the palette mirrors the board.
func buildPaletteItems(ms noc.MultiSnapshot) []paletteItem {
	var items []paletteItem
	for _, ps := range ms.Projects {
		if ps.Warning != "" {
			continue
		}
		projectRunning := projectHasAliveAgent(ps)
		items = append(items, paletteItem{
			kind:       palProject,
			label:      ps.Project + "/project",
			running:    projectRunning,
			project:    ps.Project,
			projectDir: ps.Dir,
			snapshot:   ps,
		})
		for _, sess := range sortedSessions(ps.Snap.Sessions) {
			sessLabel := sessionLabel(sess)
			// The team row: "project/session" with a "team" role marker so it is
			// distinct from any single agent and still fuzzy-matchable.
			teamRunning := sessionHasAliveAgent(sess)
			items = append(items, paletteItem{
				kind:        palTeam,
				label:       ps.Project + "/" + sessLabel + "/team",
				running:     teamRunning,
				project:     ps.Project,
				projectDir:  ps.Dir,
				snapshot:    ps,
				session:     sess.Name,
				sessionRoot: sess.Root,
			})
			agents := sortedAgents(sess.Agents)
			for _, ag := range agents {
				role := strings.TrimSpace(ag.Role)
				if role == "" {
					role = agentLabel(ag)
				}
				items = append(items, paletteItem{
					kind:        palAgent,
					label:       ps.Project + "/" + sessLabel + "/" + role,
					running:     ag.Liveness == state.LivenessAlive,
					project:     ps.Project,
					projectDir:  ps.Dir,
					snapshot:    ps,
					session:     sess.Name,
					sessionRoot: sess.Root,
					agent:       ag,
				})
			}
			for _, ag := range agents {
				role := strings.TrimSpace(ag.Role)
				if role == "" {
					role = agentLabel(ag)
				}
				items = appendAgentBusActionItems(items, ps, sess, sessLabel, ag, role)
			}
			items = append(items, paletteItem{
				kind:        palAction,
				label:       ps.Project + "/" + sessLabel + "/action/amq-ops",
				search:      ps.Project + " " + sessLabel + " amq ops ops queue health dlq bus health",
				action:      palAMQOps,
				project:     ps.Project,
				projectDir:  ps.Dir,
				snapshot:    ps,
				session:     sess.Name,
				sessionRoot: sess.Root,
			})
			items = append(items, paletteItem{
				kind:        palAction,
				label:       ps.Project + "/" + sessLabel + "/action/amq-cleanup",
				search:      ps.Project + " " + sessLabel + " amq cleanup tmp temp stale files bus maintenance",
				action:      palAMQCleanup,
				project:     ps.Project,
				projectDir:  ps.Dir,
				snapshot:    ps,
				session:     sess.Name,
				sessionRoot: sess.Root,
			})
			items = append(items, paletteItem{
				kind:        palAction,
				label:       ps.Project + "/" + sessLabel + "/action/presence",
				search:      ps.Project + " " + sessLabel + " presence active stale online offline heartbeat amq presence list",
				action:      palPresence,
				project:     ps.Project,
				projectDir:  ps.Dir,
				snapshot:    ps,
				session:     sess.Name,
				sessionRoot: sess.Root,
			})
			items = appendSessionBusActionItems(items, ps, sess, sessLabel)
		}
		items = appendProjectActionItems(items, ps)
	}
	return items
}

func appendSessionBusActionItems(items []paletteItem, ps noc.ProjectSnapshot, sess state.Session, sessLabel string) []paletteItem {
	common := paletteItem{
		kind:        palAction,
		project:     ps.Project,
		projectDir:  ps.Dir,
		snapshot:    ps,
		session:     sess.Name,
		sessionRoot: sess.Root,
	}
	items = append(items, paletteItem{
		kind:        common.kind,
		label:       ps.Project + "/" + sessLabel + "/action/status",
		search:      ps.Project + " " + sessLabel + " session status detail health agents live state",
		action:      palStatus,
		project:     common.project,
		projectDir:  common.projectDir,
		snapshot:    common.snapshot,
		session:     common.session,
		sessionRoot: common.sessionRoot,
	})
	items = append(items, paletteItem{
		kind:        common.kind,
		label:       ps.Project + "/" + sessLabel + "/action/threads",
		search:      ps.Project + " " + sessLabel + " threads thread list bus conversations summaries unread stale",
		action:      palThreads,
		project:     common.project,
		projectDir:  common.projectDir,
		snapshot:    common.snapshot,
		session:     common.session,
		sessionRoot: common.sessionRoot,
	})
	items = append(items, paletteItem{
		kind:        common.kind,
		label:       ps.Project + "/" + sessLabel + "/action/thread-context-any",
		search:      ps.Project + " " + sessLabel + " thread context any by id transcript conversation amq thread id",
		action:      palThreadContextAny,
		project:     common.project,
		projectDir:  common.projectDir,
		snapshot:    common.snapshot,
		session:     common.session,
		sessionRoot: common.sessionRoot,
	})
	if strings.TrimSpace(sess.Name) != "" {
		items = append(items, paletteItem{
			kind:        common.kind,
			label:       ps.Project + "/" + sessLabel + "/action/brief",
			search:      ps.Project + " " + sessLabel + " brief goal scope intent workstream md read session",
			action:      palBrief,
			project:     common.project,
			projectDir:  common.projectDir,
			snapshot:    common.snapshot,
			session:     common.session,
			sessionRoot: common.sessionRoot,
		})
		items = append(items, paletteItem{
			kind:        common.kind,
			label:       ps.Project + "/" + sessLabel + "/action/brief-seed",
			search:      ps.Project + " " + sessLabel + " brief seed seed brief brief-seed",
			action:      palBriefSeed,
			project:     common.project,
			projectDir:  common.projectDir,
			snapshot:    common.snapshot,
			session:     common.session,
			sessionRoot: common.sessionRoot,
		})
		items = append(items, paletteItem{
			kind:        common.kind,
			label:       ps.Project + "/" + sessLabel + "/action/resume",
			search:      ps.Project + " " + sessLabel + " resume session start restore relaunch lifecycle",
			action:      palResume,
			project:     common.project,
			projectDir:  common.projectDir,
			snapshot:    common.snapshot,
			session:     common.session,
			sessionRoot: common.sessionRoot,
		})
		for _, item := range forkPlanPaletteItems(common, ps, sess, sessLabel) {
			items = append(items, item)
		}
	}
	if len(sess.Agents) > 0 {
		items = append(items, paletteItem{
			kind:        common.kind,
			label:       ps.Project + "/" + sessLabel + "/action/stop",
			search:      ps.Project + " " + sessLabel + " stop session down halt lifecycle",
			action:      palStop,
			project:     common.project,
			projectDir:  common.projectDir,
			snapshot:    common.snapshot,
			session:     common.session,
			sessionRoot: common.sessionRoot,
		})
	}
	if sessionHasAliveAgent(sess) {
		items = append(items, paletteItem{
			kind:        common.kind,
			label:       ps.Project + "/" + sessLabel + "/action/restart",
			search:      ps.Project + " " + sessLabel + " restart session stop resume relaunch lifecycle",
			action:      palRestart,
			project:     common.project,
			projectDir:  common.projectDir,
			snapshot:    common.snapshot,
			session:     common.session,
			sessionRoot: common.sessionRoot,
		})
	}
	if th, ok := mostUrgent(sess.Coordination.NeedsYouThreads(), ""); ok {
		base := ps.Project + "/" + sessLabel + "/action/"
		searchBase := ps.Project + " " + sessLabel
		items = appendNeedsYouPaletteActions(items, common, base, searchBase, th)
	}
	if len(nonOperator(agentHandles(sess.Agents))) > 0 {
		items = append(items, paletteItem{
			kind:        common.kind,
			label:       ps.Project + "/" + sessLabel + "/action/broadcast",
			search:      ps.Project + " " + sessLabel + " broadcast message all agents squad team announcement status",
			action:      palBroadcast,
			project:     common.project,
			projectDir:  common.projectDir,
			snapshot:    common.snapshot,
			session:     common.session,
			sessionRoot: common.sessionRoot,
		})
	}
	return items
}

func forkPlanPaletteItems(common paletteItem, ps noc.ProjectSnapshot, sess state.Session, sessLabel string) []paletteItem {
	profiles := lifecycleProfilesForSession(sess)
	if len(profiles) == 0 {
		profiles = projectLaunchProfiles(ps)
	}
	switch len(profiles) {
	case 0:
		return nil
	case 1:
		return []paletteItem{{
			kind:        common.kind,
			label:       ps.Project + "/" + sessLabel + "/action/fork-plan",
			search:      ps.Project + " " + sessLabel + " fork plan branch workstream new workstream split session",
			action:      palForkPlan,
			project:     common.project,
			projectDir:  common.projectDir,
			snapshot:    common.snapshot,
			session:     common.session,
			sessionRoot: common.sessionRoot,
			profile:     profiles[0],
		}}
	default:
		out := make([]paletteItem, 0, len(profiles))
		for _, profile := range profiles {
			out = append(out, paletteItem{
				kind:        common.kind,
				label:       ps.Project + "/" + sessLabel + "/action/fork-plan/" + profile,
				search:      ps.Project + " " + sessLabel + " " + profile + " fork plan branch workstream new workstream split session",
				action:      palForkPlan,
				project:     common.project,
				projectDir:  common.projectDir,
				snapshot:    common.snapshot,
				session:     common.session,
				sessionRoot: common.sessionRoot,
				profile:     profile,
			})
		}
		return out
	}
}

func appendNeedsYouPaletteActions(items []paletteItem, common paletteItem, base, searchBase string, th state.ThreadSummary) []paletteItem {
	recipients := nonOperatorFor(th.Participants, operatorHandleForProject(common.snapshot))
	if len(recipients) == 0 {
		return items
	}
	items = append(items, paletteItem{
		kind:        common.kind,
		label:       base + "thread-context",
		search:      searchBase + " thread context transcript conversation needs you context amq thread",
		action:      palThreadContext,
		project:     common.project,
		projectDir:  common.projectDir,
		snapshot:    common.snapshot,
		session:     common.session,
		sessionRoot: common.sessionRoot,
		agent:       common.agent,
		thread:      th,
	})
	if strings.TrimSpace(th.LatestID) != "" {
		items = append(items, paletteItem{
			kind:        common.kind,
			label:       base + "read-needs-you",
			search:      searchBase + " read needs you read needs-you message body latest amq read",
			action:      palReadNeedsYou,
			project:     common.project,
			projectDir:  common.projectDir,
			snapshot:    common.snapshot,
			session:     common.session,
			sessionRoot: common.sessionRoot,
			agent:       common.agent,
			thread:      th,
		})
	}
	items = append(items, paletteItem{
		kind:        common.kind,
		label:       base + "reply",
		search:      searchBase + " reply answer respond needs you needs-you custom response",
		action:      palReply,
		project:     common.project,
		projectDir:  common.projectDir,
		snapshot:    common.snapshot,
		session:     common.session,
		sessionRoot: common.sessionRoot,
		agent:       common.agent,
		thread:      th,
	}, paletteItem{
		kind:        common.kind,
		label:       base + "approve",
		search:      searchBase + " approve accept needs you needs-you goal reached",
		action:      palApprove,
		project:     common.project,
		projectDir:  common.projectDir,
		snapshot:    common.snapshot,
		session:     common.session,
		sessionRoot: common.sessionRoot,
		agent:       common.agent,
		thread:      th,
	}, paletteItem{
		kind:        common.kind,
		label:       base + "deny",
		search:      searchBase + " deny reject needs you needs-you reason",
		action:      palDeny,
		project:     common.project,
		projectDir:  common.projectDir,
		snapshot:    common.snapshot,
		session:     common.session,
		sessionRoot: common.sessionRoot,
		agent:       common.agent,
		thread:      th,
	})
	return items
}

func appendAgentBusActionItems(items []paletteItem, ps noc.ProjectSnapshot, sess state.Session, sessLabel string, ag state.Agent, role string) []paletteItem {
	handle := strings.TrimSpace(ag.Handle)
	if handle == "" {
		return items
	}
	base := ps.Project + "/" + sessLabel + "/" + role + "/action/"
	common := paletteItem{
		kind:        palAction,
		project:     ps.Project,
		projectDir:  ps.Dir,
		snapshot:    ps,
		session:     sess.Name,
		sessionRoot: sess.Root,
		agent:       ag,
	}
	items = append(items, paletteItem{
		kind:        common.kind,
		label:       base + "inbox",
		search:      ps.Project + " " + sessLabel + " " + role + " " + handle + " inbox unread new mail amq list",
		action:      palInbox,
		project:     common.project,
		projectDir:  common.projectDir,
		snapshot:    common.snapshot,
		session:     common.session,
		sessionRoot: common.sessionRoot,
		agent:       common.agent,
	})
	items = append(items, paletteItem{
		kind:        common.kind,
		label:       base + "drain",
		search:      ps.Project + " " + sessLabel + " " + role + " " + handle + " drain read mail include body amq drain",
		action:      palDrain,
		project:     common.project,
		projectDir:  common.projectDir,
		snapshot:    common.snapshot,
		session:     common.session,
		sessionRoot: common.sessionRoot,
		agent:       common.agent,
	})
	items = append(items, paletteItem{
		kind:        common.kind,
		label:       base + "dlq",
		search:      ps.Project + " " + sessLabel + " " + role + " " + handle + " dlq dead letter failed corrupt inspect list errors amq dlq",
		action:      palDLQ,
		project:     common.project,
		projectDir:  common.projectDir,
		snapshot:    common.snapshot,
		session:     common.session,
		sessionRoot: common.sessionRoot,
		agent:       common.agent,
	})
	items = append(items, paletteItem{
		kind:        common.kind,
		label:       base + "dlq-read",
		search:      ps.Project + " " + sessLabel + " " + role + " " + handle + " read dlq inspect id dead letter failed corrupt amq dlq",
		action:      palDLQRead,
		project:     common.project,
		projectDir:  common.projectDir,
		snapshot:    common.snapshot,
		session:     common.session,
		sessionRoot: common.sessionRoot,
		agent:       common.agent,
	})
	items = append(items, paletteItem{
		kind:        common.kind,
		label:       base + "dlq-retry",
		search:      ps.Project + " " + sessLabel + " " + role + " " + handle + " dlq retry id dead letter remediation failed corrupt amq dlq",
		action:      palDLQRetry,
		project:     common.project,
		projectDir:  common.projectDir,
		snapshot:    common.snapshot,
		session:     common.session,
		sessionRoot: common.sessionRoot,
		agent:       common.agent,
	})
	items = append(items, paletteItem{
		kind:        common.kind,
		label:       base + "dlq-purge",
		search:      ps.Project + " " + sessLabel + " " + role + " " + handle + " dlq purge older than cleanup delete dead letter failed corrupt amq dlq",
		action:      palDLQPurge,
		project:     common.project,
		projectDir:  common.projectDir,
		snapshot:    common.snapshot,
		session:     common.session,
		sessionRoot: common.sessionRoot,
		agent:       common.agent,
	})
	items = append(items, paletteItem{
		kind:        common.kind,
		label:       base + "dlq-retry-all",
		search:      ps.Project + " " + sessLabel + " " + role + " " + handle + " dlq retry all dead letter remediation failed errors amq dlq",
		action:      palDLQRetryAll,
		project:     common.project,
		projectDir:  common.projectDir,
		snapshot:    common.snapshot,
		session:     common.session,
		sessionRoot: common.sessionRoot,
		agent:       common.agent,
	})
	items = append(items, paletteItem{
		kind:        common.kind,
		label:       base + "receipts",
		search:      ps.Project + " " + sessLabel + " " + role + " " + handle + " receipts delivery drained dlq lifecycle amq receipts",
		action:      palReceipts,
		project:     common.project,
		projectDir:  common.projectDir,
		snapshot:    common.snapshot,
		session:     common.session,
		sessionRoot: common.sessionRoot,
		agent:       common.agent,
	})
	items = append(items, paletteItem{
		kind:        common.kind,
		label:       base + "receipts-wait",
		search:      ps.Project + " " + sessLabel + " " + role + " " + handle + " receipts wait delivery drained dlq msg id timeout lifecycle amq receipts",
		action:      palReceiptsWait,
		project:     common.project,
		projectDir:  common.projectDir,
		snapshot:    common.snapshot,
		session:     common.session,
		sessionRoot: common.sessionRoot,
		agent:       common.agent,
	})
	items = append(items, paletteItem{
		kind:        common.kind,
		label:       base + "message",
		search:      ps.Project + " " + sessLabel + " " + role + " " + handle + " message send direct operator note status amq send",
		action:      palMessage,
		project:     common.project,
		projectDir:  common.projectDir,
		snapshot:    common.snapshot,
		session:     common.session,
		sessionRoot: common.sessionRoot,
		agent:       common.agent,
	})
	items = append(items, paletteItem{
		kind:        common.kind,
		label:       base + "message-wait",
		search:      ps.Project + " " + sessLabel + " " + role + " " + handle + " wait message message wait send direct drained receipt delivery timeout amq send",
		action:      palMessageWait,
		project:     common.project,
		projectDir:  common.projectDir,
		snapshot:    common.snapshot,
		session:     common.session,
		sessionRoot: common.sessionRoot,
		agent:       common.agent,
	})
	if strings.TrimSpace(ag.Role) != "" {
		items = append(items, paletteItem{
			kind:        common.kind,
			label:       base + "agent-resume",
			search:      ps.Project + " " + sessLabel + " " + role + " " + handle + " resume agent relaunch restart saved launch record",
			action:      palAgentResume,
			project:     common.project,
			projectDir:  common.projectDir,
			snapshot:    common.snapshot,
			session:     common.session,
			sessionRoot: common.sessionRoot,
			agent:       common.agent,
		})
	}
	if th, ok := mostUrgent(sess.Coordination.NeedsYouThreads(), handle); ok {
		searchBase := ps.Project + " " + sessLabel + " " + role + " " + handle
		items = appendNeedsYouPaletteActions(items, common, base, searchBase, th)
	}
	return items
}

func appendProjectActionItems(items []paletteItem, ps noc.ProjectSnapshot) []paletteItem {
	if ps.TeamConfigured || len(ps.Snap.Sessions) > 0 || ps.SessionStore {
		items = append(items, paletteItem{
			kind:       palAction,
			label:      ps.Project + "/action/status",
			search:     ps.Project + " project status board sessions health live state configured empty",
			action:     palStatus,
			project:    ps.Project,
			projectDir: ps.Dir,
			snapshot:   ps,
		})
	}
	if len(ps.Snap.Sessions) > 0 || ps.SessionStore {
		items = append(items,
			paletteItem{
				kind:       palAction,
				label:      ps.Project + "/action/amq-env",
				search:     ps.Project + " amq env root config project peers routing json base root",
				action:     palAMQEnv,
				project:    ps.Project,
				projectDir: ps.Dir,
				snapshot:   ps,
			},
			paletteItem{
				kind:       palAction,
				label:      ps.Project + "/action/amq-who",
				search:     ps.Project + " amq who inventory sessions agents active stale presence base root",
				action:     palAMQWho,
				project:    ps.Project,
				projectDir: ps.Dir,
				snapshot:   ps,
			})
	}
	if ps.TeamConfigured || ps.SessionStore || len(ps.Snap.Sessions) > 0 {
		items = append(items, paletteItem{
			kind:       palAction,
			label:      ps.Project + "/action/history",
			search:     ps.Project + " history launch records restorable restore previous sessions recovery",
			action:     palHistory,
			project:    ps.Project,
			projectDir: ps.Dir,
			snapshot:   ps,
		})
	}
	items = appendProjectResumePlanActions(items, ps)
	if ps.TeamConfigured {
		items = append(items, paletteItem{
			kind:       palAction,
			label:      ps.Project + "/action/roles",
			search:     ps.Project + " roles role market personas persona numbers role ids list roles team roles",
			action:     palRoles,
			project:    ps.Project,
			projectDir: ps.Dir,
			snapshot:   ps,
		})
		items = append(items, paletteItem{
			kind:       palAction,
			label:      ps.Project + "/action/new-session",
			search:     ps.Project + " new session start session create session launch session new workstream start workstream launch workstream",
			action:     palNewSession,
			project:    ps.Project,
			projectDir: ps.Dir,
			snapshot:   ps,
		})
		label := ps.Project + "/action/new-profile"
		if !ps.DefaultTeam {
			label = ps.Project + "/action/new-team"
		}
		items = append(items, paletteItem{
			kind:       palAction,
			label:      label,
			search:     ps.Project + " new profile create profile add profile new team create team setup team configure team roles",
			action:     palNewTeam,
			project:    ps.Project,
			projectDir: ps.Dir,
			snapshot:   ps,
		})
		items = append(items, paletteItem{
			kind:       palAction,
			label:      ps.Project + "/action/team-profiles",
			search:     ps.Project + " team profiles profiles profile configured profiles default profile named profiles list profiles",
			action:     palTeamProfiles,
			project:    ps.Project,
			projectDir: ps.Dir,
			snapshot:   ps,
		})
		items = append(items, paletteItem{
			kind:       palAction,
			label:      ps.Project + "/action/team-rules",
			search:     ps.Project + " team rules rules md norms source truth instructions team-rules",
			action:     palTeamRules,
			project:    ps.Project,
			projectDir: ps.Dir,
			snapshot:   ps,
		})
		items = append(items, paletteItem{
			kind:       palAction,
			label:      ps.Project + "/action/delete-team",
			search:     ps.Project + " delete team remove team rm team delete profile remove profile rm profile",
			action:     palDeleteTeam,
			project:    ps.Project,
			projectDir: ps.Dir,
			snapshot:   ps,
		})
		items = append(items, paletteItem{
			kind:       palAction,
			label:      ps.Project + "/action/sync-pointers",
			search:     ps.Project + " sync pointers pointer sync repair stubs claude agents managed block team sync",
			action:     palSyncPointers,
			project:    ps.Project,
			projectDir: ps.Dir,
			snapshot:   ps,
		})
		items = appendProjectDoctorAction(items, ps)
		return items
	}
	items = append(items, paletteItem{
		kind:       palAction,
		label:      ps.Project + "/action/roles",
		search:     ps.Project + " roles role market personas persona numbers role ids list roles team roles",
		action:     palRoles,
		project:    ps.Project,
		projectDir: ps.Dir,
		snapshot:   ps,
	})
	items = append(items, paletteItem{
		kind:       palAction,
		label:      ps.Project + "/action/new-team",
		search:     ps.Project + " new team create team setup team configure team init team roles",
		action:     palNewTeam,
		project:    ps.Project,
		projectDir: ps.Dir,
		snapshot:   ps,
	})
	items = appendProjectDoctorAction(items, ps)
	return items
}

func appendProjectResumePlanActions(items []paletteItem, ps noc.ProjectSnapshot) []paletteItem {
	profiles := projectLaunchProfiles(ps)
	switch len(profiles) {
	case 0:
		return items
	case 1:
		return append(items, paletteItem{
			kind:       palAction,
			label:      ps.Project + "/action/resume-plan",
			search:     ps.Project + " resume plan recovery plan restore plan relaunch plan launch plan",
			action:     palResumePlan,
			project:    ps.Project,
			projectDir: ps.Dir,
			profile:    profiles[0],
			snapshot:   ps,
		})
	default:
		for _, profile := range profiles {
			items = append(items, paletteItem{
				kind:       palAction,
				label:      ps.Project + "/action/resume-plan/" + profile,
				search:     ps.Project + " " + profile + " resume plan recovery plan restore plan relaunch plan launch plan",
				action:     palResumePlan,
				project:    ps.Project,
				projectDir: ps.Dir,
				profile:    profile,
				snapshot:   ps,
			})
		}
		return items
	}
}

func appendProjectDoctorAction(items []paletteItem, ps noc.ProjectSnapshot) []paletteItem {
	return append(items, paletteItem{
		kind:       palAction,
		label:      ps.Project + "/action/doctor",
		search:     ps.Project + " doctor health check diagnose diagnostics amq tmux wake markers pointer sync profile health",
		action:     palDoctor,
		project:    ps.Project,
		projectDir: ps.Dir,
		snapshot:   ps,
	})
}

func projectHasAliveAgent(ps noc.ProjectSnapshot) bool {
	for _, sess := range ps.Snap.Sessions {
		if sessionHasAliveAgent(sess) {
			return true
		}
	}
	return false
}

// sessionHasAliveAgent reports whether a session carries at least one alive agent
// (so a team row can show running vs stopped). It mirrors the tree's
// agent-liveness check (LivenessAlive) used for the jump affordance.
func sessionHasAliveAgent(sess state.Session) bool {
	for _, ag := range sess.Agents {
		if ag.Liveness == state.LivenessAlive {
			return true
		}
	}
	return false
}

func statusProfileForSession(it paletteItem) string {
	if strings.TrimSpace(it.session) == "" {
		return ""
	}
	profiles := lifecycleProfilesForSession(it.snapshotSession())
	if len(profiles) == 1 {
		return profiles[0]
	}
	return ""
}

func (it paletteItem) snapshotSession() state.Session {
	for _, sess := range it.snapshot.Snap.Sessions {
		if sess.Name == it.session {
			return sess
		}
	}
	return state.Session{}
}

func (m *NOCModel) beginPaletteLifecycle(it paletteItem, kind controlKind) tea.Cmd {
	sess := it.snapshotSession()
	if strings.TrimSpace(sess.Name) == "" {
		sess = state.Session{Name: it.session}
	}
	return m.beginLifecycleFor(it.projectDir, it.session, agentHandles(sess.Agents), sess, kind)
}

// openPalette opens the command palette over the current snapshot. It snapshots
// the candidate list at open time so typing/selection are stable even if a
// refresh lands while the palette is open. Opening mutates only palette UI state.
func (m *NOCModel) openPalette() tea.Cmd {
	m.palette = &paletteState{
		items: buildPaletteItems(m.ms),
	}
	return nil
}

// handlePaletteKey routes a key while the palette is open. esc closes; up/down
// (and ctrl+p / ctrl+n) move the selection within the filtered results; enter
// SELECTS; backspace edits the query; any single rune appends to the query. The
// selection is the only place an effect can begin: jump/focus may move the
// terminal view, while creation actions only open preview-gated editors.
func (m *NOCModel) handlePaletteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.palette
	switch msg.String() {
	case "esc":
		m.palette = nil
		return m, nil
	case "up", "ctrl+p":
		p.moveCursor(-1)
		return m, nil
	case "down", "ctrl+n":
		p.moveCursor(1)
		return m, nil
	case "enter":
		return m.paletteSelect()
	case "backspace":
		if len(p.query) > 0 {
			p.query = p.query[:len(p.query)-1]
			p.cursor = 0
		}
		return m, nil
	default:
		if len(msg.Runes) > 0 {
			p.query += string(msg.Runes)
			p.cursor = 0
		} else if s := msg.String(); len(s) == 1 {
			p.query += s
			p.cursor = 0
		}
		return m, nil
	}
}

// moveCursor moves the selection within the CURRENT filtered result set, clamped
// to its bounds (so a narrowing query never strands the cursor out of range).
func (p *paletteState) moveCursor(delta int) {
	n := len(p.filtered())
	if n == 0 {
		p.cursor = 0
		return
	}
	p.cursor += delta
	if p.cursor < 0 {
		p.cursor = 0
	}
	if p.cursor >= n {
		p.cursor = n - 1
	}
}

// selected returns the palette item at the cursor within the filtered results.
func (p *paletteState) selected() (paletteItem, bool) {
	res := p.filtered()
	if p.cursor < 0 || p.cursor >= len(res) {
		return paletteItem{}, false
	}
	return res[p.cursor], true
}

// paletteSelect acts on the selected candidate. A RUNNING agent JUMPS (the same
// name-first ResolveTmuxTargetForSession + switchTo seam the tree uses). A
// stopped agent / a team row focuses an existing tmux window if present (the
// focusTeam path) else sets a suggest-up note. Creation action rows open the
// same preview-gated T/N editors. The palette closes on select.
func (m *NOCModel) paletteSelect() (tea.Model, tea.Cmd) {
	it, ok := m.palette.selected()
	m.palette = nil
	if !ok {
		return m, nil
	}
	if it.kind == palAction {
		return m, m.paletteAction(it)
	}
	if it.kind == palProject {
		m.selectPaletteProject(it)
		return m, nil
	}
	if it.kind == palAgent && it.running {
		m.jumpToPaletteAgent(it)
		return m, nil
	}
	// Stopped agent or a team: focus an existing window, else suggest up/resume.
	m.focusPaletteTarget(it)
	return m, nil
}

func (m *NOCModel) paletteAction(it paletteItem) tea.Cmd {
	switch it.action {
	case palDoctor:
		m.selectPaletteProject(it)
		return m.beginProjectDoctorFor(it.projectDir)
	case palHistory:
		m.selectPaletteProject(it)
		return m.beginProjectHistoryFor(it.projectDir)
	case palResumePlan:
		m.selectPaletteProject(it)
		return m.beginProjectResumePlanFor(it.projectDir, it.profile)
	case palForkPlan:
		return m.beginForkPlanInput(it.snapshot, it.session, it.profile)
	case palStatus:
		if strings.TrimSpace(it.session) == "" {
			m.selectPaletteProject(it)
		}
		return m.beginStatusFor(it.projectDir, it.session, statusProfileForSession(it))
	case palThreads:
		return m.beginThreadsFor(it.projectDir, it.session)
	case palBrief:
		return m.beginBriefFor(it.projectDir, statusProfileForSession(it), it.session)
	case palBriefSeed:
		return m.beginBriefSeedFor(it.projectDir, statusProfileForSession(it), it.session)
	case palStop:
		return m.beginPaletteLifecycle(it, ctlStop)
	case palResume:
		return m.beginPaletteLifecycle(it, ctlResume)
	case palRestart:
		return m.beginPaletteLifecycle(it, ctlRestart)
	case palRoles:
		m.selectPaletteProject(it)
		m.roleMarket = &roleMarketOverlay{project: it.project, projectDir: it.projectDir}
		m.actNote = "ROLE MARKET: amq-noc's built-in catalog of amq-squad roles"
		return nil
	case palTeamProfiles:
		m.selectPaletteProject(it)
		m.teamProfiles = &teamProfilesOverlay{project: it.project, projectDir: it.projectDir, profiles: projectLaunchProfiles(it.snapshot)}
		m.actNote = "TEAM PROFILES read: " + squadCommandToken() + " team profiles --project " + shellToken(it.projectDir)
		return nil
	case palTeamRules:
		m.selectPaletteProject(it)
		return m.beginTeamRulesFor(it.projectDir)
	case palDeleteTeam:
		m.selectPaletteProject(it)
		return m.beginDeleteTeamForProject(it.snapshot)
	case palAMQOps:
		return m.beginAMQOpsFor(it.sessionRoot)
	case palAMQWho:
		m.selectPaletteProject(it)
		return m.beginAMQWhoFor(projectAMQRoot(it.snapshot))
	case palAMQEnv:
		m.selectPaletteProject(it)
		return m.beginAMQEnvFor(projectAMQRoot(it.snapshot))
	case palAMQCleanup:
		return m.beginAMQCleanupFor(it.sessionRoot)
	case palPresence:
		return m.beginPresenceFor(it.sessionRoot)
	case palThreadContextAny:
		return m.beginThreadContextAnyInput(it.sessionRoot)
	case palThreadContext:
		return m.beginThreadContextFor(it.sessionRoot, it.thread)
	case palReadNeedsYou:
		return m.beginReadNeedsYouFor(it.sessionRoot, it.thread, operatorHandleForProject(it.snapshot))
	case palApprove:
		return m.beginApproveOrDenyFor(it.sessionRoot, it.session, it.thread, ctlApprove, operatorHandleForProject(it.snapshot))
	case palReply:
		return m.beginReplyFor(it.sessionRoot, it.session, it.thread, operatorHandleForProject(it.snapshot))
	case palDeny:
		return m.beginApproveOrDenyFor(it.sessionRoot, it.session, it.thread, ctlDeny, operatorHandleForProject(it.snapshot))
	case palBroadcast:
		return m.beginBroadcastFor(it.sessionRoot, it.session, agentHandles(it.snapshotSession().Agents), operatorHandleForProject(it.snapshot))
	case palInbox:
		return m.beginInboxAgentFor(it.sessionRoot, it.agent.Handle)
	case palDLQ:
		return m.beginDLQAgentFor(it.sessionRoot, it.agent.Handle)
	case palDLQRead:
		return m.beginDLQReadFor(it.sessionRoot, it.agent.Handle)
	case palDLQRetry:
		return m.beginDLQRetryFor(it.sessionRoot, it.agent.Handle)
	case palDLQPurge:
		return m.beginDLQPurgeFor(it.sessionRoot, it.agent.Handle)
	case palDLQRetryAll:
		return m.beginDLQRetryAllFor(it.sessionRoot, it.agent.Handle)
	case palReceipts:
		return m.beginReceiptsAgentFor(it.sessionRoot, it.agent.Handle)
	case palReceiptsWait:
		return m.beginReceiptsWaitFor(it.sessionRoot, it.agent.Handle)
	case palMessage:
		return m.beginMessageFor(it.sessionRoot, it.agent.Handle, operatorHandleForProject(it.snapshot))
	case palMessageWait:
		return m.beginMessageWaitForOperator(it.sessionRoot, it.agent.Handle, operatorHandleForProject(it.snapshot))
	case palDrain:
		return m.beginDrainAgentFor(it.sessionRoot, it.agent.Handle)
	case palAgentResume:
		return m.beginAgentResumeFor(it.projectDir, it.agent.Role, it.session)
	case palNewSession:
		return m.beginNewSessionForProject(it.snapshot)
	case palNewTeam:
		return m.beginNewTeamForProject(it.snapshot)
	case palSyncPointers:
		return m.beginPointerSyncForProject(it.snapshot)
	default:
		m.actNote = "unknown palette action"
		return nil
	}
}

func (m *NOCModel) selectPaletteProject(it paletteItem) {
	if strings.TrimSpace(it.projectDir) == "" {
		m.actNote = "project has no directory"
		return
	}
	if m.selectVisiblePaletteProject(it) {
		return
	}
	if m.filter != "" || m.hideStale {
		m.filter = ""
		m.hideStale = false
		if m.selectVisiblePaletteProject(it) {
			return
		}
	}
	m.actNote = "project not visible: " + it.project
}

func (m *NOCModel) selectVisiblePaletteProject(it paletteItem) bool {
	ns := m.nodes()
	for i, n := range ns {
		if n.kind != nodeProject || n.project.Dir != it.projectDir {
			continue
		}
		m.cursor = i
		m.ensureCursorVisibleFor(ns)
		m.rememberSelection()
		if it.snapshot.TeamConfigured {
			m.actNote = "selected " + it.project + "; press N for a new session or T for a profile"
		} else {
			m.actNote = "selected " + it.project + "; press T to create a team"
		}
		return true
	}
	return false
}

// jumpToPaletteAgent performs the read-only tmux jump to a running agent chosen
// in the palette, mirroring NOCModel.jump exactly: resolve name-first via
// ResolveTmuxTargetForSession, then call the switchTo seam, surfacing
// SuggestJump / not-in-tmux text rather than erroring.
func (m *NOCModel) jumpToPaletteAgent(it paletteItem) {
	panes, err := m.panes()
	if err != nil {
		m.jumpNote = "tmux not available: " + err.Error()
		return
	}
	target, resolved := noc.ResolveTmuxTargetForSession(it.agent, it.session, it.projectDir, panes, m.pidTree)
	if !resolved {
		m.jumpNote = "no live tmux pane found for " + it.agent.Handle + " (resume it, or attach manually)"
		return
	}
	if err := m.switchTo(target); err != nil {
		if nit, isNIT := err.(*noc.NotInTmuxError); isNIT {
			m.jumpNote = "not inside tmux - run: " + nit.Command
			return
		}
		m.jumpNote = "jump: " + err.Error() + " (try: " + noc.SuggestJump(target) + ")"
		return
	}
	m.jumpNote = "jumped to " + noc.SuggestJump(target)
}

// focusPaletteTarget focuses an EXISTING tmux window for a stopped-agent / team
// selection, mirroring NOCModel.focusTeam: resolveSquadWindow (read-only) then
// the switchTo seam, or a suggest-up note when nothing is running. It NEVER
// spawns.
func (m *NOCModel) focusPaletteTarget(it paletteItem) {
	panes, err := m.panes()
	if err != nil {
		m.jumpNote = "tmux not available: " + err.Error()
		return
	}
	target, found := resolveSquadWindow(it.session, it.projectDir, panes)
	if !found {
		m.jumpNote = "team not running; press R to resume it, or run " + newSessionCommand(it.projectDir)
		return
	}
	if err := m.switchTo(target); err != nil {
		if nit, isNIT := err.(*noc.NotInTmuxError); isNIT {
			m.jumpNote = "not inside tmux - run: " + nit.Command
			return
		}
		m.jumpNote = "open: " + err.Error() + " (try: " + noc.SuggestJump(target) + ")"
		return
	}
	m.jumpNote = "focused " + noc.SuggestJump(target)
}

// paletteOverlayView renders the command palette: a query line + the live
// filtered list with the selection bar on the cursor row, each row labeled
// "project/session/role  ●running|○stopped". It is read-only chrome.
func (m NOCModel) paletteOverlayView() string {
	p := m.palette
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.brand, "COMMAND PALETTE - jump, focus, or create"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")

	cursor := "▏"
	if m.colorMode == ColorAscii {
		cursor = "_"
	}
	b.WriteString(m.th.paint(m.th.atRisk, "find: "+p.query+cursor))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")

	res := p.filtered()
	if len(res) == 0 {
		b.WriteString(m.th.paint(m.th.dim, "  (no matching projects, actions, agents, or teams)"))
		b.WriteString("\n")
	}
	// Cap the rendered rows so a huge fleet never overruns the frame; the cursor
	// row is always kept in view by windowing around it.
	const maxRows = 12
	start := 0
	if p.cursor >= maxRows {
		start = p.cursor - maxRows + 1
	}
	end := start + maxRows
	if end > len(res) {
		end = len(res)
	}
	for i := start; i < end; i++ {
		b.WriteString(m.paletteRow(res[i], i == p.cursor))
		b.WriteString("\n")
	}
	if len(res) > maxRows {
		b.WriteString(m.th.paint(m.th.dim, "  … "+itoaPalette(len(res))+" matches (type to narrow)"))
		b.WriteString("\n")
	}

	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	hint := "type to filter · ↑↓ move · ⏎ select · esc close"
	if m.colorMode == ColorAscii {
		hint = "type to filter | up/down move | enter select | esc close"
	}
	b.WriteString(m.th.paint(m.th.dim, hint))
	return b.String()
}

// paletteRow renders one palette result row: a selection marker, the label, and
// a compact status/action tag.
func (m NOCModel) paletteRow(it paletteItem, selected bool) string {
	var b strings.Builder
	if selected {
		b.WriteString(m.th.paint(m.th.selBar, nocGlyphSelect.glyph(m.colorMode)+" "))
	} else {
		b.WriteString("  ")
	}
	nameStyle := m.th.brand
	if it.running {
		nameStyle = m.th.running
	} else {
		nameStyle = m.th.dim
	}
	if it.kind == palAction {
		nameStyle = m.th.atRisk
	}
	b.WriteString(m.th.paint(nameStyle, padRight(it.label, 40)))
	b.WriteString("  ")
	if it.kind == palAction {
		b.WriteString(m.th.paint(m.th.atRisk, paletteActionLabel(it)))
		return b.String()
	}
	if it.kind == palProject {
		if it.snapshot.TeamConfigured {
			b.WriteString(m.th.paint(m.th.brand, "project"))
		} else {
			b.WriteString(m.th.paint(m.th.dim, "candidate"))
		}
		return b.String()
	}
	if it.running {
		dot := "●"
		if m.colorMode == ColorAscii {
			dot = "[run]"
		}
		b.WriteString(m.th.paint(m.th.running, dot+" running"))
	} else {
		dot := "○"
		if m.colorMode == ColorAscii {
			dot = "[stop]"
		}
		b.WriteString(m.th.paint(m.th.dim, dot+" stopped"))
	}
	return b.String()
}

func paletteActionLabel(it paletteItem) string {
	switch it.action {
	case palDoctor:
		return "doctor"
	case palHistory:
		return "history"
	case palResumePlan:
		return "resume plan"
	case palForkPlan:
		return "fork plan"
	case palStatus:
		if strings.TrimSpace(it.session) != "" {
			return "session status"
		}
		return "project status"
	case palThreads:
		return "threads"
	case palBrief:
		return "brief"
	case palBriefSeed:
		return "seed brief"
	case palStop:
		return "stop session"
	case palResume:
		return "resume session"
	case palRestart:
		return "restart session"
	case palRoles:
		return "role market"
	case palTeamProfiles:
		return "team profiles"
	case palTeamRules:
		return "team rules"
	case palDeleteTeam:
		return "delete team"
	case palAMQOps:
		return "AMQ ops"
	case palAMQWho:
		return "AMQ who"
	case palAMQEnv:
		return "AMQ env"
	case palAMQCleanup:
		return "AMQ cleanup"
	case palPresence:
		return "presence"
	case palThreadContextAny:
		return "thread context by id"
	case palThreadContext:
		return "thread context"
	case palReadNeedsYou:
		return "read needs-you"
	case palApprove:
		return "approve"
	case palReply:
		return "reply"
	case palDeny:
		return "deny"
	case palBroadcast:
		return "broadcast"
	case palInbox:
		return "inbox"
	case palDLQ:
		return "DLQ"
	case palDLQRead:
		return "read DLQ"
	case palDLQRetry:
		return "retry DLQ"
	case palDLQPurge:
		return "purge DLQ"
	case palDLQRetryAll:
		return "retry all DLQ"
	case palReceipts:
		return "receipts"
	case palReceiptsWait:
		return "wait receipts"
	case palMessage:
		return "message"
	case palMessageWait:
		return "wait message"
	case palDrain:
		return "drain"
	case palAgentResume:
		return "resume agent"
	case palNewSession:
		return "start session"
	case palNewTeam:
		if it.snapshot.TeamConfigured && it.snapshot.DefaultTeam {
			return "create profile"
		}
		return "create team"
	case palSyncPointers:
		return "sync pointers"
	default:
		return "action"
	}
}

func paletteAMQOpsCommand(root string) string {
	if strings.TrimSpace(root) == "" {
		return "amq doctor --ops"
	}
	return "env " + shellToken("AM_ROOT="+root) + " amq doctor --ops"
}

func paletteInboxCommand(root, handle string) string {
	return "amq list --root " + shellToken(root) + " --me " + shellToken(strings.TrimSpace(handle)) + " --new"
}

func paletteDrainCommand(root, handle string) string {
	return "amq drain --root " + shellToken(root) + " --me " + shellToken(strings.TrimSpace(handle)) + " --include-body"
}

// itoaPalette is a tiny int→string for the palette match count (avoids importing
// strconv just for one call; noc_view.go already owns strconv-based helpers but
// this file stays lean).
func itoaPalette(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
