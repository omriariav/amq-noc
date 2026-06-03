package console

import (
	"fmt"
	"strings"

	"github.com/omriariav/amq-noc/internal/state"
)

// suggestAttach computes the INERT attach hint for the current selection. v0
// NEVER attaches: it only returns the command the operator could run to jump to
// the agent's pane. The string is advisory text; pressing `a` shows it and does
// nothing else. This function is PURE (no I/O, no process control).
//
// For an agent selection it suggests the amq attach verb plus a tmux fallback;
// for a thread selection it suggests attaching to the thread's first agent
// participant; on the board it suggests attaching to the selected session.
func (m Model) suggestAttach() string {
	sel, ok := m.selectedRow()
	if !ok {
		return "nothing selected to attach to"
	}
	switch m.route {
	case routeBoard:
		return attachSessionHint(sel.ID)
	default:
		s, sok := m.currentSession()
		switch sel.kind {
		case rowAgent:
			handle := strings.TrimPrefix(sel.ID, "agent:")
			return attachAgentHint(m.session, handle, agentByHandle(s, sok, handle))
		case rowThread:
			tid := strings.TrimPrefix(sel.ID, "thread:")
			handle := firstAgentParticipant(s, sok, tid)
			if handle == "" {
				return fmt.Sprintf("thread %s spans %s — attach to a specific agent with: amq-squad attach --session %s --agent <handle>",
					shortID(tid), threadPeersLabel(s, sok, tid), shellQuote(m.session))
			}
			return attachAgentHint(m.session, handle, agentByHandle(s, sok, handle))
		default:
			return attachSessionHint(m.session)
		}
	}
}

// attachSessionHint suggests how to open a session (inert text).
func attachSessionHint(session string) string {
	return fmt.Sprintf("to jump to this session, run:  amq-squad attach --session %s   "+
		"(or:  tmux attach -t %s )   — read-only console does not attach for you",
		shellQuote(session), shellQuote(tmuxTarget(session)))
}

// attachAgentHint suggests how to open one agent's pane (inert text). When the
// agent looks alive we prefer the amq attach verb; we always include a tmux
// fallback so the operator has a path even without amq on PATH.
func attachAgentHint(session, handle string, a state.Agent) string {
	state := ""
	if a.Liveness != "" {
		state = fmt.Sprintf(" [%s]", a.Liveness)
	}
	return fmt.Sprintf("to jump to %s%s, run:  amq-squad attach --session %s --agent %s   "+
		"(or:  tmux attach -t %s )   — read-only console does not attach for you",
		handle, state, shellQuote(session), shellQuote(handle), shellQuote(tmuxTarget(session)))
}

// agentByHandle finds an agent in a session by handle (zero Agent if absent).
func agentByHandle(s state.Session, ok bool, handle string) state.Agent {
	if !ok {
		return state.Agent{}
	}
	for _, a := range s.Agents {
		if a.Handle == handle {
			return a
		}
	}
	return state.Agent{}
}

// firstAgentParticipant returns the first thread participant that is a discovered
// agent in the session (skipping the human operator/non-agent handles), or "".
func firstAgentParticipant(s state.Session, ok bool, threadID string) string {
	if !ok {
		return ""
	}
	for _, t := range s.Coordination.Threads {
		if t.ID != threadID {
			continue
		}
		for _, p := range t.Participants {
			for _, a := range s.Agents {
				if a.Handle == p {
					return p
				}
			}
		}
	}
	return ""
}

// threadPeersLabel renders a thread's participants for the attach guidance.
func threadPeersLabel(s state.Session, ok bool, threadID string) string {
	if !ok {
		return threadID
	}
	for _, t := range s.Coordination.Threads {
		if t.ID == threadID {
			return strings.Join(t.Participants, ", ")
		}
	}
	return threadID
}

// tmuxTarget derives a plausible tmux session/window target name from the AMQ
// session name. It is advisory only (the real target depends on the operator's
// tmux layout); the hint says "or" precisely because it is best-effort.
func tmuxTarget(session string) string {
	if strings.TrimSpace(session) == "" {
		return "amq"
	}
	return session
}

// shellQuote single-quotes a token when it contains shell-significant characters,
// so the suggested command is safe to paste. It is display-only.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t'\"\\$`*?[]{}();&|<>~#") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
