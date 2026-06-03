// Package console — noc_filter.go: the typed-filter mini-language for the NOC
// command center. Self-contained (the session console's predicates are
// unexported and shaped for a single snapshot); same surface syntax.
//
// Syntax (case-insensitive), evaluated against a project / session / agent:
//
//	needs-you | gated | at-risk | blocked -> triage class (matches the rolled-up state)
//	agent:<h>                       -> agent handle prefix/substring
//	model:<e>                       -> agent engine (claude/codex/...)
//	project:<p>                     -> project name
//	session:<s>                     -> session name
//	label:<l> / orchestrator:<o>    -> AMQ integration metadata
//	<bare text>                     -> matches project / session / handle / role
package console

import (
	"strings"

	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/state"
)

// nocFilterClause is one parsed filter token.
type nocFilterClause struct {
	key string // "", "agent", "model", "project", "session", "triage"
	val string // lowercased value
}

// parseNOCFilter splits a filter string into clauses (AND-combined).
func parseNOCFilter(filter string) []nocFilterClause {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return nil
	}
	var clauses []nocFilterClause
	for _, tok := range strings.Fields(filter) {
		low := strings.ToLower(tok)
		switch low {
		case "needs-you", "needsyou", "needs-user", "needsuser", "needs_user":
			clauses = append(clauses, nocFilterClause{key: "triage", val: "needs-you"})
			continue
		case "gated":
			clauses = append(clauses, nocFilterClause{key: "triage", val: "gated"})
			continue
		case "at-risk", "atrisk":
			clauses = append(clauses, nocFilterClause{key: "triage", val: "at-risk"})
			continue
		case "blocked":
			clauses = append(clauses, nocFilterClause{key: "triage", val: "blocked"})
			continue
		case "stale-blocked", "staleblocked", "stale_blocked":
			clauses = append(clauses, nocFilterClause{key: "triage", val: "stale-blocked"})
			continue
		}
		if i := strings.IndexByte(tok, ':'); i >= 0 {
			key := strings.ToLower(tok[:i])
			val := strings.ToLower(tok[i+1:])
			switch key {
			case "agent", "model", "project", "session", "label", "orchestrator":
				clauses = append(clauses, nocFilterClause{key: key, val: val})
				continue
			}
		}
		clauses = append(clauses, nocFilterClause{key: "", val: low})
	}
	return clauses
}

// ProjectMatchesNOCFilter keeps a project if it (or any of its sessions/agents)
// satisfies every clause. An empty filter keeps everything.
func ProjectMatchesNOCFilter(ps noc.ProjectSnapshot, filter string) bool {
	clauses := parseNOCFilter(filter)
	if len(clauses) == 0 {
		return true
	}
	for _, c := range clauses {
		if !projectSatisfies(ps, c) {
			return false
		}
	}
	return true
}

func projectSatisfies(ps noc.ProjectSnapshot, c nocFilterClause) bool {
	switch c.key {
	case "triage":
		return triageMatchesRollup(c.val, ps.Snap.Rollup)
	case "project":
		return strings.Contains(strings.ToLower(ps.Project), c.val)
	default:
		if c.key == "" && strings.Contains(strings.ToLower(ps.Project), c.val) {
			return true
		}
		for _, sess := range ps.Snap.Sessions {
			if sessionSatisfies(sess, c) {
				return true
			}
		}
		return false
	}
}

// SessionMatchesNOCFilter keeps a session if it (or any agent) satisfies every
// clause.
func SessionMatchesNOCFilter(sess state.Session, filter string) bool {
	clauses := parseNOCFilter(filter)
	if len(clauses) == 0 {
		return true
	}
	for _, c := range clauses {
		if !sessionSatisfies(sess, c) {
			return false
		}
	}
	return true
}

// SessionMatchesNOCProjectFilter keeps a session when it satisfies every clause
// in the context of its parent project. Bare project-name matches scope the
// project and should not hide all of its child sessions.
func SessionMatchesNOCProjectFilter(ps noc.ProjectSnapshot, sess state.Session, filter string) bool {
	clauses := parseNOCFilter(filter)
	if len(clauses) == 0 {
		return true
	}
	for _, c := range clauses {
		if !sessionSatisfiesInProject(ps, sess, c) {
			return false
		}
	}
	return true
}

func sessionSatisfiesInProject(ps noc.ProjectSnapshot, sess state.Session, c nocFilterClause) bool {
	if c.key == "" && strings.Contains(strings.ToLower(ps.Project), c.val) {
		return true
	}
	return sessionSatisfies(sess, c)
}

func sessionSatisfies(sess state.Session, c nocFilterClause) bool {
	switch c.key {
	case "triage":
		return triageMatchesRollup(c.val, sess.Rollup)
	case "session":
		return strings.Contains(strings.ToLower(sess.Name), c.val)
	case "project":
		// A session does not carry its project name; defer to project scope.
		return true
	case "label", "orchestrator":
		for _, th := range sess.Coordination.Threads {
			if threadSatisfies(th, c) {
				return true
			}
		}
		return false
	case "agent", "model":
		for _, ag := range sess.Agents {
			if agentSatisfies(ag, c) {
				return true
			}
		}
		return false
	default:
		if strings.Contains(strings.ToLower(sess.Name), c.val) {
			return true
		}
		for _, th := range sess.Coordination.Threads {
			if threadSatisfies(th, c) {
				return true
			}
		}
		for _, ag := range sess.Agents {
			if agentSatisfies(ag, c) {
				return true
			}
		}
		return false
	}
}

func threadSatisfies(th state.ThreadSummary, c nocFilterClause) bool {
	switch c.key {
	case "label":
		for _, label := range th.Labels {
			if strings.Contains(strings.ToLower(label), c.val) {
				return true
			}
		}
		return false
	case "orchestrator":
		return strings.Contains(strings.ToLower(th.Orchestrator), c.val)
	default:
		if strings.Contains(strings.ToLower(th.Orchestrator), c.val) ||
			strings.Contains(strings.ToLower(th.FromProject), c.val) ||
			strings.Contains(strings.ToLower(th.ReplyProject), c.val) {
			return true
		}
		for _, label := range th.Labels {
			if strings.Contains(strings.ToLower(label), c.val) {
				return true
			}
		}
		return false
	}
}

// AgentMatchesNOCFilter keeps an agent if it satisfies every clause.
func AgentMatchesNOCFilter(ag state.Agent, filter string) bool {
	clauses := parseNOCFilter(filter)
	if len(clauses) == 0 {
		return true
	}
	for _, c := range clauses {
		if !agentSatisfies(ag, c) {
			return false
		}
	}
	return true
}

// AgentMatchesNOCProjectFilter keeps an agent when it satisfies every clause in
// the context of its project and session. Bare project/session matches scope the
// parent row and should pass all children underneath that parent.
func AgentMatchesNOCProjectFilter(ps noc.ProjectSnapshot, sess state.Session, ag state.Agent, filter string) bool {
	clauses := parseNOCFilter(filter)
	if len(clauses) == 0 {
		return true
	}
	for _, c := range clauses {
		if !agentSatisfiesInProjectSession(ps, sess, ag, c) {
			return false
		}
	}
	return true
}

func agentSatisfiesInProjectSession(ps noc.ProjectSnapshot, sess state.Session, ag state.Agent, c nocFilterClause) bool {
	if c.key == "" {
		if strings.Contains(strings.ToLower(ps.Project), c.val) {
			return true
		}
		if strings.Contains(strings.ToLower(sess.Name), c.val) {
			return true
		}
		for _, th := range sess.Coordination.Threads {
			if threadSatisfies(th, c) {
				return true
			}
		}
	}
	if c.key == "label" || c.key == "orchestrator" {
		for _, th := range sess.Coordination.Threads {
			if threadSatisfies(th, c) {
				return true
			}
		}
	}
	return agentSatisfies(ag, c)
}

func agentSatisfies(ag state.Agent, c nocFilterClause) bool {
	switch c.key {
	case "agent":
		return strings.Contains(strings.ToLower(ag.Handle), c.val)
	case "model":
		return strings.Contains(strings.ToLower(ag.Engine), c.val)
	case "triage":
		// Agent rows reflect liveness; a triage filter passes the agent through
		// so its (possibly matching) session still shows. Session/project scope
		// enforces the triage class.
		return true
	case "session", "project":
		return true
	default:
		hay := strings.ToLower(ag.Handle + " " + ag.Role + " " + ag.Engine)
		return strings.Contains(hay, c.val)
	}
}

// triageMatchesRollup reports whether a rollup has the requested triage class.
func triageMatchesRollup(class string, r state.TriageRollup) bool {
	switch class {
	case "needs-you":
		return r.NeedsYou > 0
	case "at-risk":
		return r.AtRisk > 0
	case "blocked":
		return r.Blocked > 0
	case "gated":
		return r.Gated > 0
	case "stale-blocked":
		return r.BlockedStale > 0
	}
	return false
}
