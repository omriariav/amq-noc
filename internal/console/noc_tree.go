// Package console — noc_tree.go: flatten a noc.MultiSnapshot into a collapsible,
// attention-first tree of selectable nodes (root → project → session → agent).
//
// The tree is rebuilt from the immutable snapshot + the collapse set on every
// render; nothing here mutates the snapshot. Node identity (id) is STABLE across
// snapshot replacement so selection survives a refresh (project dir, then
// dir|session, then dir|session|handle).
package console

import (
	"sort"
	"strings"

	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/state"
)

// nocNodeKind distinguishes the four tree levels.
type nocNodeKind int

const (
	nodeRoot nocNodeKind = iota
	nodeProject
	nodeSession
	nodeAgent
)

// nocNode is one flattened, visible tree row.
type nocNode struct {
	kind  nocNodeKind
	id    string // stable identity across snapshots
	depth int    // 0 root, 1 project, 2 session, 3 agent
	label string // human label (root path / project / session / handle)

	state    nocState
	rollup   state.TriageRollup // for project/session: the triage tally
	child    nocChildTally      // for parent rows: visible child rows by primary state
	age      string             // right-aligned dim age (agent/thread)
	recent   string             // dim recent action / title / detail
	expanded bool               // for parents: whether children are shown
	hasKids  bool               // whether this node can expand
	last     bool               // last child of its parent (tree elbow)

	// Carried data for the detail pane / jump action.
	project noc.ProjectSnapshot
	session state.Session
	agent   state.Agent
	canJump bool // running agent → jump affordance
	warning string
}

// nocChildTally counts the visible immediate children of a parent row using the
// simplified primary statuses. Project rows count sessions/teams, session rows
// count agents, and root rows count projects.
type nocChildTally struct {
	NeedsYou int
	Blocked  int
	Waiting  int
	Running  int
	Stale    int
}

// nocTreeState holds collapse/expand overrides. Roots and sessions are expanded
// by default; projects are collapsed by default in the live NOC so the operator
// sees one line per project until drilling in.
type nocTreeState struct {
	collapsed map[string]bool
	expanded  map[string]bool
}

func newNOCTreeState() nocTreeState {
	return nocTreeState{collapsed: map[string]bool{}, expanded: map[string]bool{}}
}

func (s nocTreeState) isCollapsed(id string) bool {
	if projectNodeIDDefaultCollapsed(id) {
		return !s.expanded[id]
	}
	return s.collapsed[id]
}

func (s *nocTreeState) toggle(id string) {
	if s.collapsed[id] {
		delete(s.collapsed, id)
	} else {
		s.collapsed[id] = true
	}
}

func (s *nocTreeState) setCollapsed(id string, v bool) {
	if projectNodeIDDefaultCollapsed(id) {
		if v {
			delete(s.expanded, id)
		} else {
			s.expanded[id] = true
		}
		return
	}
	if v {
		s.collapsed[id] = true
	} else {
		delete(s.collapsed, id)
	}
}

// Stable node ids.
func rootNodeID(root string) string               { return "root|" + root }
func projectNodeID(dir string) string             { return "proj|" + dir }
func sessionNodeID(dir, sess string) string       { return "sess|" + dir + "|" + sess }
func agentNodeID(dir, sess, handle string) string { return "agent|" + dir + "|" + sess + "|" + handle }

func projectNodeIDDefaultCollapsed(id string) bool { return strings.HasPrefix(id, "proj|") }

// buildNOCTree flattens a MultiSnapshot into the visible node slice honoring the
// collapse set, the typed filter, and the hide-stale toggle. Projects keep
// noc.Collect's attention-first order; sessions and agents are sorted
// attention-first here. When hideStale is set, stopped squads carrying no live
// attention are dropped so the operator can focus on what is alive.
func buildNOCTree(ms noc.MultiSnapshot, ts nocTreeState, filter string, hideStale bool, collapseProjects bool) []nocNode {
	var nodes []nocNode

	// Roots are headers only when there is more than one (a single root is
	// implicit — its projects render at the top level). This keeps the common
	// single-root case calm and flat.
	multiRoot := len(ms.Roots) > 1

	// Group projects by which root contains them (longest-prefix match), so the
	// tree mirrors the on-disk roots the operator passed.
	byRoot := groupProjectsByRoot(ms)

	roots := append([]string(nil), ms.Roots...)
	if len(roots) == 0 {
		roots = []string{""}
	}

	for ri, root := range roots {
		projects := byRoot[root]

		// Apply the filter at the project level before rendering the root header,
		// so a root's tally/state describes the projects actually visible below it.
		visProjects := make([]noc.ProjectSnapshot, 0, len(projects))
		for _, ps := range projects {
			if hideStale && projectIsStaleOnly(ps) {
				continue
			}
			if ProjectMatchesNOCFilter(ps, filter) {
				visProjects = append(visProjects, ps)
			}
		}

		if len(visProjects) == 0 && multiRoot {
			// Show an empty root header so a configured-but-empty root is visible.
		} else if len(visProjects) == 0 {
			continue
		}

		rid := rootNodeID(root)
		rootExpanded := !ts.isCollapsed(rid)

		if multiRoot {
			nodes = append(nodes, nocNode{
				kind:     nodeRoot,
				id:       rid,
				depth:    0,
				label:    displayRoot(root),
				state:    rootState(visProjects),
				child:    childTallyProjects(visProjects),
				expanded: rootExpanded,
				hasKids:  len(visProjects) > 0,
				last:     ri == len(roots)-1,
			})
			if !rootExpanded {
				continue
			}
		}

		projDepth := 0
		if multiRoot {
			projDepth = 1
		}

		for pi, ps := range visProjects {
			pid := projectNodeID(ps.Dir)
			pExpanded := !collapseProjects || !ts.isCollapsed(pid)
			pState := projectRollupState(ps)

			sessions := sortedSessions(ps.Snap.Sessions)
			visSessions := make([]state.Session, 0, len(sessions))
			for _, sess := range sessions {
				if SessionMatchesNOCProjectFilter(ps, sess, filter) {
					visSessions = append(visSessions, sess)
				}
			}

			nodes = append(nodes, nocNode{
				kind:     nodeProject,
				id:       pid,
				depth:    projDepth,
				label:    ps.Project,
				state:    pState,
				rollup:   ps.Snap.Rollup,
				child:    childTallySessions(visSessions),
				recent:   projectRecent(ps),
				expanded: pExpanded,
				hasKids:  len(visSessions) > 0 || ps.Warning != "",
				last:     pi == len(visProjects)-1,
				project:  ps,
				warning:  ps.Warning,
			})
			if !pExpanded {
				continue
			}

			for si, sess := range visSessions {
				sid := sessionNodeID(ps.Dir, sess.Name)
				sExpanded := !ts.isCollapsed(sid)
				agents := sortedAgentsForSession(sess)
				visAgents := make([]state.Agent, 0, len(agents))
				for _, ag := range agents {
					if AgentMatchesNOCProjectFilter(ps, sess, ag, filter) {
						visAgents = append(visAgents, ag)
					}
				}

				nodes = append(nodes, nocNode{
					kind:     nodeSession,
					id:       sid,
					depth:    projDepth + 1,
					label:    sessionLabel(sess),
					state:    sessionRollupState(sess),
					rollup:   sess.Rollup,
					child:    childTallyAgents(sess, visAgents),
					expanded: sExpanded,
					hasKids:  len(visAgents) > 0,
					last:     si == len(visSessions)-1,
					project:  ps,
					session:  sess,
				})
				if !sExpanded {
					continue
				}
				for ai, ag := range visAgents {
					nodes = append(nodes, nocNode{
						kind:    nodeAgent,
						id:      agentNodeID(ps.Dir, sess.Name, ag.Handle),
						depth:   projDepth + 2,
						label:   agentLabel(ag),
						state:   agentNodeState(sess, ag),
						age:     "",
						recent:  agentRecent(ag),
						hasKids: false,
						last:    ai == len(visAgents)-1,
						project: ps,
						session: sess,
						agent:   ag,
						canJump: ag.Liveness == state.LivenessAlive,
					})
				}
			}
		}
	}
	return nodes
}

func childTallyProjects(projects []noc.ProjectSnapshot) nocChildTally {
	var t nocChildTally
	for _, ps := range projects {
		t.add(projectRollupState(ps))
	}
	return t
}

func childTallySessions(sessions []state.Session) nocChildTally {
	var t nocChildTally
	for _, sess := range sessions {
		t.add(sessionRollupState(sess))
	}
	return t
}

func childTallyAgents(sess state.Session, agents []state.Agent) nocChildTally {
	var t nocChildTally
	for _, ag := range agents {
		t.add(agentNodeState(sess, ag))
	}
	return t
}

func (t *nocChildTally) add(s nocState) {
	switch visibleState(s) {
	case nocNeedsYou:
		t.NeedsYou++
	case nocBlocked:
		t.Blocked++
	case nocWaiting:
		t.Waiting++
	case nocRunning:
		t.Running++
	default:
		t.Stale++
	}
}

// groupProjectsByRoot assigns each project to the longest matching root prefix.
func groupProjectsByRoot(ms noc.MultiSnapshot) map[string][]noc.ProjectSnapshot {
	out := map[string][]noc.ProjectSnapshot{}
	roots := ms.Roots
	for _, ps := range ms.Projects {
		best := ""
		bestLen := -1
		for _, r := range roots {
			if r != "" && strings.HasPrefix(ps.Dir, r) && len(r) > bestLen {
				best = r
				bestLen = len(r)
			}
		}
		if bestLen < 0 && len(roots) > 0 {
			best = roots[0]
		}
		out[best] = append(out[best], ps)
	}
	return out
}

// rootState rolls a root's projects up to one display state (the most urgent).
func rootState(projects []noc.ProjectSnapshot) nocState {
	best := nocEmpty
	for _, ps := range projects {
		s := projectRollupState(ps)
		if s < best {
			best = s
		}
	}
	return best
}

// sortedSessions / sortedAgents return attention-first orderings (needs-you,
// then running/live attention, then stale/stopped), ties by name/handle.
func sortedSessions(in []state.Session) []state.Session {
	out := append([]state.Session(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		si, sj := sessionRollupState(out[i]), sessionRollupState(out[j])
		if si != sj {
			return si < sj
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func sortedAgents(in []state.Agent) []state.Agent {
	out := append([]state.Agent(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		si, sj := agentState(out[i]), agentState(out[j])
		if si != sj {
			return si < sj
		}
		return out[i].Handle < out[j].Handle
	})
	return out
}

func sortedAgentsForSession(sess state.Session) []state.Agent {
	out := append([]state.Agent(nil), sess.Agents...)
	sort.SliceStable(out, func(i, j int) bool {
		si, sj := agentNodeState(sess, out[i]), agentNodeState(sess, out[j])
		if si != sj {
			return si < sj
		}
		// Within a state tier the orchestrated lead sorts first: it is the
		// human's point of contact. Attention ordering still wins above.
		if out[i].IsLead != out[j].IsLead {
			return out[i].IsLead
		}
		return out[i].Handle < out[j].Handle
	})
	return out
}

// projectRecent / agentRecent derive the dim trailing text shown on a tree row.
// projectRecent surfaces setup/guidance hints; the latest thread subject is NOT
// repeated on session/project rows (it lives in the detail pane), so a needs-you
// parent row stays a quiet "<owner> needs you" rather than echoing a subject.
func projectRecent(ps noc.ProjectSnapshot) string {
	if ps.Warning != "" {
		return "warning: " + firstLine(ps.Warning)
	}
	if ps.Candidate {
		return "candidate team-home; press T to create team"
	}
	if !ps.DefaultTeam {
		if ps.TeamConfigured {
			if profile := singleNamedProfile(ps); profile != "" {
				return "named profile " + profile + "; press N for new session"
			}
			return "named profiles; press N and choose profile"
		}
		return "no team profile; press T to create team"
	}
	if len(ps.Snap.Sessions) == 0 {
		return "team configured; press N for new session"
	}
	// Active projects keep the left tree quiet: the latest thread subject is
	// surfaced in the detail pane, not repeated as a project-row tail. Only the
	// setup/guidance hints above remain as a row tail.
	return ""
}

// topThread returns the freshest thread of a session. The tree's trailing text is
// a live signal, so it should reflect current activity rather than the oldest
// unresolved attention bucket.
func topThread(sess state.Session) (state.ThreadSummary, bool) {
	ts := sortThreadsNewest(sess, Filter{})
	if len(ts) == 0 {
		return state.ThreadSummary{}, false
	}
	return ts[0], true
}

func agentRecent(ag state.Agent) string {
	parts := []string{}
	if ag.Role != "" {
		parts = append(parts, ag.Role)
	}
	if ag.Engine != "" {
		parts = append(parts, ag.Engine)
	}
	return strings.Join(parts, " · ")
}

// agentLabel is the handle, the human-facing identity of an agent row. The
// lead of an orchestrated squad carries an explicit "(lead)" badge: it is the
// human's single point of contact and must be identifiable at a glance.
func agentLabel(ag state.Agent) string {
	label := ag.Handle
	if label == "" {
		label = "(agent)"
	}
	if ag.IsLead {
		label += " (lead)"
	}
	return label
}

// sessionLabel is the NEVER-BLANK display label for a session. The base-root
// (rootless) layout has an empty session name; rendering an empty cell reads
// like a bug, so we substitute an explicit placeholder. "(root)" marks the
// base-root layout; "(default-session)" is the generic empty-name fallback.
func sessionLabel(sess state.Session) string {
	name := strings.TrimSpace(sess.Name)
	if name == "" {
		// A session anchored directly at the base root (its Root has no extra
		// path segment under the container) is the base-root layout: call it "(root)".
		if isBaseRootSession(sess) {
			name = "(root)"
		} else {
			name = "(default-session)"
		}
	}
	if sess.Orchestrated {
		name += " (orchestrated)"
	}
	return name
}

// isBaseRootSession reports whether a session is the base-root (rootless)
// layout: its Root path basename is the .agent-mail container itself, i.e. the
// session sits directly at the base root with no named sub-directory.
func isBaseRootSession(sess state.Session) bool {
	if strings.TrimSpace(sess.Name) != "" {
		return false
	}
	if sess.Root == "" {
		return false
	}
	return strings.HasSuffix(strings.TrimRight(sess.Root, "/"), noc.AgentMailDirName)
}

// displayRoot abbreviates a root path for the header (home-relative when long).
func displayRoot(root string) string {
	if root == "" {
		return "(cwd)"
	}
	return root
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
