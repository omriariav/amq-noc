package cli

// noc_runtime_actions.go folds amq-squad's v1.5 published runtime actions into
// the CLI's deterministic action model so `--actions` and `--json` advertise the
// exact control commands amq-squad owns (focus/send/resume/status) instead of
// fallback-only templates. It is the CLI half of amq-noc#7; the TUI command
// picker already consumes the same noc.RuntimeStatus contract.
//
// Preference rule: when a squad-managed session reports AVAILABLE published
// actions, the published status/resume commands replace the fallback session
// commands, and published agent focus/send are added (the CLI has no fallback
// for those). Older/missing/partial runtime metadata leaves the fallback model
// untouched, so the catalog degrades gracefully.

import (
	"strings"

	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/team"
)

// runtimeActionFetch resolves amq-squad's published runtime contract for one
// session. Production injects defaultRuntimeActionFetch; tests inject a stub.
// A nil fetch (the default for unit tests that build nocExecution directly)
// disables the fold, preserving the pure fallback model.
type runtimeActionFetch func(dir, profile, session string) noc.RuntimeStatus

// defaultRuntimeActionFetch shells `amq-squad status --session --json` via the
// shared READ-ONLY runner. Any failure yields a zero RuntimeStatus, so the fold
// is a no-op against older amq-squad builds.
func defaultRuntimeActionFetch(dir, profile, session string) noc.RuntimeStatus {
	return noc.FetchRuntimeStatus(noc.DefaultSquadRunner, dir, profile, session)
}

// applyRuntimeActions mutates env in place, folding published runtime actions
// into each squad-managed session/agent and recomputing the flat action list and
// counts. It only queries amq-squad for team-configured projects (plain AMQ
// roots have no runtime contract) and tolerates every failure as "no actions".
func applyRuntimeActions(env *nocSnapshotEnvelopeData, fetch runtimeActionFetch) {
	if env == nil || fetch == nil {
		return
	}
	for pi := range env.Projects {
		p := &env.Projects[pi]
		if !p.TeamConfigured || strings.TrimSpace(p.Dir) == "" {
			continue
		}
		for si := range p.Sessions {
			sess := &p.Sessions[si]
			if strings.TrimSpace(sess.Name) == "" {
				continue
			}
			rs := fetch(p.Dir, sessionEnvelopeRuntimeProfile(*sess), sess.Name)
			if !rs.HasActions() {
				continue
			}
			foldRuntimeActionsIntoSession(sess, rs)
		}
	}
	env.Actions = nocFlatActions(env.Projects)
	env.ActionCount = len(env.Actions)
	env.MutatingActionCount = nocMutatingActionCount(env.Actions)
}

// sessionEnvelopeRuntimeProfile resolves the --profile to query amq-squad with,
// derived from the session's agents. A single named profile is passed through; a
// default, empty, or mixed profile resolves to "" (FetchRuntimeStatus then omits
// the --profile flag).
func sessionEnvelopeRuntimeProfile(sess nocSessionJSONData) string {
	seen := map[string]bool{}
	for _, ag := range sess.Agents {
		p := strings.TrimSpace(ag.TeamProfile)
		if p == "" {
			p = team.DefaultProfile
		}
		seen[p] = true
	}
	if len(seen) != 1 {
		return ""
	}
	for p := range seen {
		if p == team.DefaultProfile {
			return ""
		}
		return p
	}
	return ""
}

// foldRuntimeActionsIntoSession overlays a session's published runtime actions.
// Session-scope status/resume (identical across members) replace the fallback
// commands; agent-scope focus/send are attached to the matching member. Only
// AVAILABLE actions are folded, mirroring the TUI picker, so dead-pane focus/send
// never surface as runnable commands.
func foldRuntimeActionsIntoSession(sess *nocSessionJSONData, rs noc.RuntimeStatus) {
	seenSessionKind := map[string]bool{}
	for _, mem := range rs.Members {
		for _, a := range mem.Actions {
			if !a.Available || (a.Kind != "status" && a.Kind != "resume") || seenSessionKind[a.Kind] {
				continue
			}
			seenSessionKind[a.Kind] = true
			sess.Actions = replaceOrAppendNOCAction(sess.Actions, publishedSessionAction(sess.ID, a))
		}
	}
	for ai := range sess.Agents {
		ag := &sess.Agents[ai]
		mem, ok := rs.MemberByRole(ag.Role)
		if !ok {
			mem, ok = rs.MemberByRole(ag.Handle)
		}
		if !ok {
			continue
		}
		role := strings.TrimSpace(ag.Role)
		if role == "" {
			role = strings.TrimSpace(ag.Handle)
		}
		for _, a := range mem.Actions {
			if !a.Available || (a.Kind != "focus" && a.Kind != "send") {
				continue
			}
			ag.Actions = replaceOrAppendNOCAction(ag.Actions, publishedAgentAction(ag.ID, role, a))
		}
	}
}

// publishedSessionAction classifies a session-scope published action. status is
// read-only; resume mutates and is confirm-gated. The published command is exact
// and concrete, so it carries no template vars.
func publishedSessionAction(sessionID string, a noc.RuntimeAction) nocActionJSONData {
	if a.Kind == "resume" {
		return nocAction("session", sessionID, "resume", a.Command,
			"Resume this session in tmux via amq-squad's published runtime action.",
			true, true, false)
	}
	return nocAction("session", sessionID, "status", a.Command,
		"Show this session's amq-squad status via the published runtime action.",
		false, false, false)
}

// publishedAgentAction classifies an agent-scope published action. focus is a
// read-only terminal focus; send delivers a prompt over stdin (--body-file -),
// so it is marked mutating, confirm-gated, and template/input-required, and is
// refused by --run-action (see nocActionReadsStdinBody): copy and pipe a body.
func publishedAgentAction(agentID, role string, a noc.RuntimeAction) nocActionJSONData {
	if a.Kind == "send" {
		return nocAction("agent", agentID, "send", a.Command,
			"Deliver a prompt to "+role+"'s tmux pane via amq-squad. Input-required: copy and pipe a body; the NOC will not run a stdin-body send.",
			true, true, true)
	}
	return nocAction("agent", agentID, "focus", a.Command,
		"Focus "+role+"'s tmux pane via amq-squad (read-only terminal focus).",
		false, false, false)
}

// replaceOrAppendNOCAction replaces an action with the same ID (stable
// scope|target|name identity) or appends it, so a published status/resume
// supersedes its fallback while keeping the selector ID stable.
func replaceOrAppendNOCAction(list []nocActionJSONData, action nocActionJSONData) []nocActionJSONData {
	for i := range list {
		if list[i].ID == action.ID {
			list[i] = action
			return list
		}
	}
	return append(list, action)
}

// nocActionReadsStdinBody reports whether a command delivers its body over stdin
// (amq-squad send --body-file -). Such actions are display/copy only: stdin is
// already consumed by the confirm prompt and the NOC has no prompt-body plumbing,
// so --run-action refuses to execute them.
func nocActionReadsStdinBody(command string) bool {
	return strings.Contains(command, "--body-file -")
}
