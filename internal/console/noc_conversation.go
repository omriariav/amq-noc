// Package console - noc_conversation.go: conversation mode (#27), the
// operator's dialogue with an orchestrated squad's lead as a first-class
// surface. Entered with m on the orchestrated session row or its lead row;
// esc returns to the board.
//
// The transcript is participant-filtered, not thread-filtered: every message
// between the operator and the lead wherever it lives (the p2p thread plus
// lead-raised gate asks), oldest first, full bodies (state.ConversationTranscript).
// Sends use an INLINE STAGED CONFIRM - enter stages a visible about-to-send
// line naming kind + thread + delivery channel, enter again sends, esc steps
// back. That is conversation mode's documented form of preview-first, not a
// relaxation: every send is still previewed before it happens.
package console

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-noc/internal/act"
	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/state"
)

const (
	// conversationTranscriptLimit bounds the scan; older history stays
	// available through the existing thread/read controls.
	conversationTranscriptLimit = 60
	// conversationBodyPreviewLines caps one message's rendered body.
	conversationBodyPreviewLines = 10
)

// conversationView is the active conversation-mode state.
type conversationView struct {
	project  noc.ProjectSnapshot
	sess     state.Session
	operator string
	msgs     []state.Message
	scroll   int // lines scrolled UP from the bottom of the transcript
	composer conversationComposer
	note     string // last send/refresh result line
}

// conversationComposer is the inline staged-confirm editor.
type conversationComposer struct {
	staged bool
	body   string
	// Resolved at stage time and shown verbatim in the staged line:
	kind       string
	thread     string
	subject    string
	channel    string // "pane" or "amq"
	gateAnswer bool
}

// openConversation enters conversation mode for an orchestrated session.
func (m *NOCModel) openConversation(project noc.ProjectSnapshot, sess state.Session) tea.Cmd {
	leadHandle := strings.TrimSpace(sess.LeadHandle)
	if !sess.Orchestrated || leadHandle == "" {
		m.actNote = "conversation applies to an orchestrated squad (team.json orchestrated/lead)"
		return nil
	}
	if strings.TrimSpace(sess.Root) == "" {
		m.actNote = "conversation: session has no AMQ root"
		return nil
	}
	operator := operatorHandleOrDefault(project.OperatorGateHandle())
	cv := &conversationView{project: project, sess: sess, operator: operator}
	m.conversation = cv
	m.refreshConversation()
	return nil
}

// refreshConversation re-scans the transcript and re-resolves the session from
// the current snapshot so liveness and gate state stay current.
func (m *NOCModel) refreshConversation() {
	cv := m.conversation
	if cv == nil {
		return
	}
	for _, ps := range m.ms.Projects {
		if ps.Dir != cv.project.Dir {
			continue
		}
		cv.project = ps
		for _, sess := range ps.Snap.Sessions {
			if sess.Name == cv.sess.Name {
				cv.sess = sess
				break
			}
		}
		break
	}
	if m.transcript != nil {
		cv.msgs = m.transcript(cv.sess.Root, cv.sess.LeadHandle, cv.operator, conversationTranscriptLimit)
	}
}

// handleConversationKey owns every key while conversation mode is open.
func (m *NOCModel) handleConversationKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	cv := m.conversation
	c := &cv.composer
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		switch {
		case c.staged:
			c.staged = false
			cv.note = "send cancelled - back to editing"
		case strings.TrimSpace(c.body) != "":
			c.body = ""
			cv.note = "draft cleared - esc again to leave the conversation"
		default:
			m.conversation = nil
			m.actNote = ""
		}
		return m, nil
	case "enter":
		if c.staged {
			m.sendStagedConversation()
			return m, nil
		}
		m.stageConversationSend()
		return m, nil
	case "pgup":
		cv.scroll += 10
		return m, nil
	case "pgdown":
		cv.scroll -= 10
		if cv.scroll < 0 {
			cv.scroll = 0
		}
		return m, nil
	case "up":
		if strings.TrimSpace(c.body) == "" && !c.staged {
			cv.scroll++
			return m, nil
		}
	case "down":
		if strings.TrimSpace(c.body) == "" && !c.staged {
			if cv.scroll > 0 {
				cv.scroll--
			}
			return m, nil
		}
	case "ctrl+r":
		// Plain letters always type in a chat composer ("g" must not be a
		// shortcut here, or drafts starting with g lose their first rune).
		m.refreshConversation()
		cv.note = "transcript refreshed"
		return m, tea.Batch(nocRebuildCmd(m.rebuild))
	case "backspace":
		if c.staged {
			c.staged = false
		}
		c.body = dropLast(c.body)
		return m, nil
	}
	if t := keyText(msg); t != "" {
		if c.staged {
			c.staged = false
		}
		c.body += t
	}
	return m, nil
}

// stageConversationSend resolves the target and shows the about-to-send line.
// Target resolution: the newest OPEN lead-raised gate gets an answer on its
// own thread (that is what clears needs-you); otherwise the message is a
// DIRECTIVE on the operator p2p thread, delivered to the lead's pane when it
// is operational (busy-guarded) or its AMQ inbox when it is down.
func (m *NOCModel) stageConversationSend() {
	cv := m.conversation
	c := &cv.composer
	if strings.TrimSpace(c.body) == "" {
		cv.note = "type a message first"
		return
	}
	if gate, ok := openLeadGate(cv.msgs, cv.sess.LeadHandle, cv.operator); ok {
		c.kind = string(state.KindAnswer)
		c.thread = gate.Thread
		c.subject = "ANSWER: " + strings.TrimSpace(gate.Subject)
		c.channel = "amq"
		c.gateAnswer = true
	} else {
		c.kind = string(state.KindTodo)
		c.thread = directiveThread(cv.sess.LeadHandle, cv.operator)
		c.subject = directiveSubject(c.body)
		c.gateAnswer = false
		c.channel = "amq"
		if ag, ok := conversationLeadAgent(cv.sess); ok && agentOperational(ag) {
			c.channel = "pane"
		}
	}
	c.staged = true
	cv.note = ""
}

// sendStagedConversation performs the confirmed send through the existing
// seams: act.Send for AMQ writes (gate answers and inbox directives),
// amq-squad send for pane delivery. Mirrors the directive flow's channel
// semantics including the surfaced busy refusal (never --force).
func (m *NOCModel) sendStagedConversation() {
	cv := m.conversation
	c := &cv.composer
	lead := cv.sess.LeadHandle

	if c.channel == "pane" && !c.gateAnswer {
		if m.sendPrompt == nil {
			cv.note = "send unavailable (no send prompt backend)"
			return
		}
		profile := sessionCommandProfileForSession(cv.project, cv.sess)
		if profile == "PROFILE" {
			profile = ""
		}
		role := strings.TrimSpace(cv.sess.LeadRole)
		if role == "" {
			role = lead
		}
		op := sendPromptOp{
			ProjectDir: cv.project.Dir,
			Profile:    profile,
			Session:    cv.sess.Name,
			Role:       role,
			Body:       c.body,
		}
		if err := m.sendPrompt(op); err != nil {
			if isAgentBusyError(err) {
				cv.note = "lead is busy (mid-turn); retry when idle, or esc to edit"
				c.staged = false
				return
			}
			cv.note = "send failed: " + err.Error()
			c.staged = false
			return
		}
		cv.note = "DIRECTIVE delivered to " + lead + "'s pane; the acknowledgment lands in this transcript"
		*c = conversationComposer{}
		m.refreshConversation()
		return
	}

	if m.sendOp == nil {
		cv.note = "send unavailable (no AMQ backend)"
		return
	}
	op := act.OpMessage{
		Root:    cv.sess.Root,
		Me:      cv.operator,
		To:      lead,
		Subject: c.subject,
		Body:    c.body,
		Thread:  c.thread,
		Kind:    c.kind,
	}
	if err := m.sendOp(op); err != nil {
		cv.note = "send failed: " + err.Error()
		c.staged = false
		return
	}
	if c.gateAnswer {
		cv.note = "ANSWER sent on " + c.thread + "; the gate clears on refresh"
	} else {
		cv.note = "DIRECTIVE sent to " + lead + "'s AMQ inbox (read on next drain/wake)"
	}
	*c = conversationComposer{}
	m.refreshConversation()
}

// openLeadGate returns the newest lead-raised gate ask that has no later
// operator reply on the same thread - the structural "the lead is waiting on
// you" signal, derived from the transcript itself.
func openLeadGate(msgs []state.Message, lead, operator string) (state.Message, bool) {
	lastOnThread := map[string]state.Message{}
	for _, msg := range msgs {
		if !strings.HasPrefix(strings.ToLower(msg.Thread), "gate/") {
			continue
		}
		lastOnThread[msg.Thread] = msg // msgs are oldest-first
	}
	var best state.Message
	found := false
	for _, msg := range lastOnThread {
		if !strings.EqualFold(msg.From, lead) {
			continue // the operator (or someone else) spoke last: gate not open
		}
		if !messageNamesRecipient(msg, operator) {
			continue
		}
		if !found || msg.Created.After(best.Created) {
			best = msg
			found = true
		}
	}
	return best, found
}

// messageNamesRecipient reports whether handle is among the message's
// recipients (case-insensitive).
func messageNamesRecipient(msg state.Message, handle string) bool {
	for _, to := range msg.To {
		if strings.EqualFold(strings.TrimSpace(to), handle) {
			return true
		}
	}
	return false
}

// conversationLeadAgent finds the lead's agent row in the session.
func conversationLeadAgent(sess state.Session) (state.Agent, bool) {
	for _, ag := range sess.Agents {
		if ag.IsLead || strings.EqualFold(ag.Handle, sess.LeadHandle) {
			return ag, true
		}
	}
	return state.Agent{}, false
}

// conversationFrame renders conversation mode as a full-frame takeover.
func (m NOCModel) conversationFrame() string {
	cv := m.conversation
	width := m.width
	if width <= 0 {
		width = 100
	}
	textW := width - 2
	if textW < 40 {
		textW = 40
	}

	var b strings.Builder
	title := "CONVERSATION  " + cv.sess.LeadHandle + " (lead)"
	b.WriteString(m.th.paint(m.th.brand, title))
	b.WriteString(m.th.paint(m.th.dim, "  "+sessionLabel(cv.sess)+" "+m.dot()+" "+cv.project.Project) + "\n")
	if state.SessionLeadDown(cv.sess) {
		b.WriteString(m.th.paint(m.th.nocStateStyle(nocAtRisk), "lead is DOWN; messages go to its AMQ inbox") + "\n")
	}
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()) + "\n")

	// Transcript window: build every line, then show the tail minus scroll.
	lines := m.conversationTranscriptLines(cv, textW)
	winH := m.height - 12
	if winH < 5 {
		winH = 5
	}
	start := len(lines) - winH - cv.scroll
	if start < 0 {
		start = 0
	}
	end := start + winH
	if end > len(lines) {
		end = len(lines)
	}
	if start > 0 {
		b.WriteString(m.th.paint(m.th.dim, "  ("+strconv.Itoa(start)+" earlier lines; pgup to scroll)") + "\n")
	}
	for _, line := range lines[start:end] {
		b.WriteString(line + "\n")
	}
	if len(lines) == 0 {
		b.WriteString(m.th.paint(m.th.dim, "  (no messages between you and "+cv.sess.LeadHandle+" yet - type below to start)") + "\n")
	}
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()) + "\n")

	// Open-gate banner: the next staged message answers it.
	if gate, ok := openLeadGate(cv.msgs, cv.sess.LeadHandle, cv.operator); ok {
		b.WriteString(m.th.paint(m.th.needsYou, "open gate: "+truncate(gate.Subject, textW-12)+" - your next message answers it") + "\n")
	}
	if cv.note != "" {
		b.WriteString(m.th.paint(m.th.dim, cv.note) + "\n")
	}

	// Composer: editing line or the staged about-to-send block.
	c := cv.composer
	if c.staged {
		channel := "the lead's AMQ inbox"
		if c.channel == "pane" {
			channel = "the lead's pane (busy-guarded)"
		}
		b.WriteString(m.th.paint(m.th.needsYou, "about to send "+m.dot()+" kind "+c.kind+" "+m.dot()+" thread "+c.thread+" "+m.dot()+" via "+channel) + "\n")
		for _, line := range splitBoundedLines(c.body, 4) {
			b.WriteString(m.th.paint(m.th.brand, "  "+truncate(line, textW)) + "\n")
		}
		b.WriteString(m.th.paint(m.th.needsYou, "enter send "+m.dot()+" esc edit") + "\n")
	} else {
		prompt := "> "
		bodyLines := splitBoundedLines(c.body, 3)
		if len(bodyLines) == 0 {
			bodyLines = []string{""}
		}
		for i, line := range bodyLines {
			prefix := "  "
			if i == 0 {
				prefix = prompt
			}
			cursor := ""
			if i == len(bodyLines)-1 {
				cursor = "_"
			}
			b.WriteString(m.th.paint(m.th.brand, prefix+line+cursor) + "\n")
		}
		b.WriteString(m.th.paint(m.th.dim, "enter stage "+m.dot()+" esc back "+m.dot()+" pgup/pgdn scroll "+m.dot()+" ctrl+r refresh") + "\n")
	}
	return b.String()
}

// conversationTranscriptLines renders the transcript into display lines.
func (m NOCModel) conversationTranscriptLines(cv *conversationView, textW int) []string {
	var lines []string
	for _, msg := range cv.msgs {
		meta := msg.From + " -> " + strings.Join(msg.To, ",")
		if msg.Kind != "" {
			meta += " " + m.dot() + " " + string(msg.Kind)
		}
		meta += " " + m.dot() + " " + conversationThreadBadge(msg.Thread)
		if age := m.conversationAge(msg); age != "" {
			meta += " " + m.dot() + " " + age
		}
		fromLead := strings.EqualFold(msg.From, cv.sess.LeadHandle)
		metaStyle := m.th.dim
		if fromLead {
			metaStyle = m.th.brand
		}
		lines = append(lines, m.th.paint(metaStyle, truncate(meta, textW)))
		if subj := strings.TrimSpace(msg.Subject); subj != "" {
			lines = append(lines, m.th.paint(metaStyle, "  "+truncate(subj, textW-2)))
		}
		body := splitBoundedLines(msg.Body, conversationBodyPreviewLines)
		total := len(strings.Split(strings.TrimRight(msg.Body, "\n"), "\n"))
		for _, raw := range body {
			for _, wrapped := range wrapPlainText(raw, textW-2) {
				lines = append(lines, m.th.paint(m.th.dim, "  "+wrapped))
			}
		}
		if total > conversationBodyPreviewLines {
			lines = append(lines, m.th.paint(m.th.dim, "  +"+strconv.Itoa(total-conversationBodyPreviewLines)+" more lines"))
		}
		lines = append(lines, "")
	}
	return lines
}

// conversationThreadBadge compacts a thread id into a badge.
func conversationThreadBadge(thread string) string {
	low := strings.ToLower(thread)
	switch {
	case strings.HasPrefix(low, "gate/"):
		return "[gate " + strings.TrimPrefix(thread, "gate/") + "]"
	case strings.HasPrefix(low, "p2p/"):
		return "[p2p]"
	default:
		return "[" + thread + "]"
	}
}

// conversationAge renders the message age against the snapshot clock,
// clamping just-sent messages (newer than the snapshot) to "just now".
func (m NOCModel) conversationAge(msg state.Message) string {
	if msg.Created.IsZero() || m.ms.ObservedAt.IsZero() {
		return ""
	}
	d := m.ms.ObservedAt.Sub(msg.Created)
	if d < 0 {
		return "just now"
	}
	return ageLabel(d)
}
