package console

import (
	"os/exec"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-noc/internal/noc"
)

type commandPickerOverlay struct {
	title    string
	commands []nocCommandAction
	// selectionID is the node the picker was opened for; an async
	// runtimeActionsMsg only augments the picker when it still matches, so a
	// late fetch never bleeds into a different row.
	selectionID string
}

type commandCopyMsg struct {
	command string
	err     error
}

// runtimeActionsMsg carries the runtime control commands amq-squad advertises
// for the row the picker was opened on (fetched asynchronously so the picker
// opens instantly with static commands and gains live actions when they land).
type runtimeActionsMsg struct {
	selectionID string
	actions     []nocCommandAction
}

func defaultClipboardCopy(text string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

func defaultRuntimeFetch(dir, profile, session string) noc.RuntimeStatus {
	return noc.FetchRuntimeStatus(noc.DefaultSquadRunner, dir, profile, session)
}

// beginCommandPicker opens the copy-command overlay immediately with the static
// commands (so it is always snappy), and — for a session/agent row — returns an
// async command that fetches amq-squad's live runtime actions and folds them in
// when they arrive. Returns nil when there is nothing to fetch.
func (m *NOCModel) beginCommandPicker() tea.Cmd {
	commands := m.selectedCommandActions()
	if len(commands) == 0 {
		m.actNote = "no action commands for this row"
		return nil
	}
	n, ok := m.selectedNode()
	title := "COPY COMMAND"
	if ok {
		switch n.kind {
		case nodeProject:
			title = "COPY COMMAND  " + n.project.Project
		case nodeSession:
			title = "COPY COMMAND  " + n.session.Name
		case nodeAgent:
			title = "COPY COMMAND  " + agentLabel(n.agent)
		}
	}
	picker := &commandPickerOverlay{title: title, commands: capCommands(commands)}
	if ok {
		picker.selectionID = n.id
	}
	m.commandPicker = picker
	if !ok || m.runtimeFetch == nil {
		return nil
	}
	dir, profile, session := runtimeFetchScope(n)
	if dir == "" || session == "" {
		return nil
	}
	node := n
	id := n.id
	fetch := m.runtimeFetch
	return func() tea.Msg {
		rs := fetch(dir, profile, session)
		return runtimeActionsMsg{selectionID: id, actions: runtimeCommandActions(rs, node)}
	}
}

// handleRuntimeActions folds amq-squad's live runtime actions into the open
// picker, but only if it is still the row they were fetched for. Runtime actions
// lead (they are the exact, live control commands); static commands follow;
// duplicates by command string are dropped; the list is capped to 9.
func (m *NOCModel) handleRuntimeActions(msg runtimeActionsMsg) {
	if m.commandPicker == nil || m.commandPicker.selectionID != msg.selectionID || len(msg.actions) == 0 {
		return
	}
	merged := append([]nocCommandAction{}, msg.actions...)
	merged = append(merged, m.commandPicker.commands...)
	m.commandPicker.commands = capCommands(dedupeCommands(merged))
}

func capCommands(in []nocCommandAction) []nocCommandAction {
	if len(in) > 9 {
		return in[:9]
	}
	return in
}

func dedupeCommands(in []nocCommandAction) []nocCommandAction {
	seen := make(map[string]bool, len(in))
	out := in[:0]
	for _, a := range in {
		key := strings.TrimSpace(a.Command)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, a)
	}
	return out
}

// runtimeFetchScope resolves (project dir, profile, session) to query for a
// session/agent row. The "PROFILE" placeholder (mixed profiles in one session)
// maps to the default profile rather than passing a literal flag value.
func runtimeFetchScope(n nocNode) (dir, profile, session string) {
	switch n.kind {
	case nodeSession:
		dir, session = strings.TrimSpace(n.project.Dir), strings.TrimSpace(n.session.Name)
		profile = sessionCommandProfile(n.project, n.session.Name)
	case nodeAgent:
		dir, session = strings.TrimSpace(n.project.Dir), strings.TrimSpace(n.session.Name)
		profile = sessionCommandProfile(n.project, n.session.Name)
	default:
		return "", "", ""
	}
	if profile == "PROFILE" {
		profile = ""
	}
	return dir, profile, session
}

// runtimeCommandActions maps amq-squad's advertised, AVAILABLE runtime actions
// for the row into copyable command entries (focus/send/resume/status). For an
// agent row only that member's actions; for a session row every member's.
// Unavailable actions (e.g. focus/send on a dead pane) are omitted.
func runtimeCommandActions(rs noc.RuntimeStatus, n nocNode) []nocCommandAction {
	if !rs.HasActions() {
		return nil
	}
	var members []noc.RuntimeMember
	switch n.kind {
	case nodeAgent:
		if mem, ok := rs.MemberByRole(n.agent.Role); ok {
			members = []noc.RuntimeMember{mem}
		}
	case nodeSession:
		members = rs.Members
	default:
		return nil
	}
	var out []nocCommandAction
	for _, mem := range members {
		role := strings.TrimSpace(mem.Role)
		if role == "" {
			role = strings.TrimSpace(mem.Handle)
		}
		for _, a := range mem.Actions {
			if !a.Available {
				continue
			}
			out = append(out, commandAction(strings.TrimSpace(a.Kind+" "+role), a.Command, runtimeActionDesc(a.Kind, role)))
		}
	}
	return out
}

func runtimeActionDesc(kind, role string) string {
	switch kind {
	case "focus":
		return "focus " + role + "'s tmux pane (amq-squad)"
	case "send":
		return "deliver a prompt to " + role + "'s pane (amq-squad)"
	case "resume":
		return "resume " + role + " from its launch record"
	case "status":
		return "show " + role + "'s amq-squad status"
	default:
		return "amq-squad " + strings.TrimSpace(kind) + " for " + role
	}
}

func (m *NOCModel) selectedCommandActions() []nocCommandAction {
	n, ok := m.selectedNode()
	if !ok {
		return nil
	}
	switch n.kind {
	case nodeProject:
		return kickRecoverActions(n.project, "", projectDetailAMQRoot(n.project))
	case nodeSession:
		return kickRecoverActions(n.project, n.session.Name, n.session.Root)
	case nodeAgent:
		return agentCommandActions(n.project, n.session, n.agent)
	default:
		return nil
	}
}

func (m *NOCModel) handleCommandPickerKey(key string) tea.Cmd {
	switch key {
	case "esc", "q", "ctrl+c":
		m.commandPicker = nil
		return nil
	}
	if len(key) != 1 || key[0] < '1' || key[0] > '9' {
		return nil
	}
	idx := int(key[0] - '1')
	if m.commandPicker == nil || idx < 0 || idx >= len(m.commandPicker.commands) {
		return nil
	}
	command := m.commandPicker.commands[idx].Command
	copyFn := m.copyText
	if copyFn == nil {
		copyFn = defaultClipboardCopy
	}
	m.commandPicker = nil
	return func() tea.Msg {
		return commandCopyMsg{command: command, err: copyFn(command)}
	}
}

func (m *NOCModel) handleCommandCopyResult(msg commandCopyMsg) {
	if msg.err != nil {
		m.actNote = "copy failed: " + msg.err.Error()
		return
	}
	m.actNote = "copied command to clipboard"
}

func (m NOCModel) commandPickerOverlayView() string {
	p := m.commandPicker
	if p == nil {
		return ""
	}
	width := m.overlayContentWidth()
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.brand, p.title) + "\n")
	b.WriteString(m.th.paint(m.th.dim, "press 1-"+strconv.Itoa(len(p.commands))+" to copy; esc cancels") + "\n")
	b.WriteString(m.detailRule() + "\n")
	for i, command := range p.commands {
		label := strconv.Itoa(i+1) + ". "
		actionLabel := strings.TrimSpace(command.Label)
		if actionLabel == "" {
			actionLabel = "command"
		}
		head := actionLabel + ": " + command.Command
		lines := wrapPlainText(head, width-visibleWidth(label)-2)
		if len(lines) == 0 {
			lines = []string{""}
		}
		b.WriteString("  " + m.th.paint(m.th.brand, label) + m.th.paint(m.th.dim, lines[0]) + "\n")
		for _, line := range lines[1:] {
			b.WriteString("  " + strings.Repeat(" ", visibleWidth(label)) + m.th.paint(m.th.dim, line) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m NOCModel) overlayContentWidth() int {
	if m.width <= 0 {
		return 78
	}
	w := m.width - 4
	if w > 110 {
		w = 110
	}
	if w < 24 {
		w = 24
	}
	return w
}

func wrapPlainText(s string, width int) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if width <= 0 {
		return []string{s}
	}
	words := strings.Fields(s)
	var lines []string
	cur := ""
	flush := func() {
		if cur != "" {
			lines = append(lines, cur)
			cur = ""
		}
	}
	for _, word := range words {
		for visibleWidth(word) > width {
			flush()
			part, rest := splitVisibleWord(word, width)
			lines = append(lines, part)
			word = rest
		}
		if cur == "" {
			cur = word
			continue
		}
		if visibleWidth(cur)+1+visibleWidth(word) <= width {
			cur += " " + word
			continue
		}
		flush()
		cur = word
	}
	flush()
	return lines
}

func splitVisibleWord(s string, width int) (string, string) {
	if width <= 0 {
		return s, ""
	}
	runes := []rune(s)
	if len(runes) <= width {
		return s, ""
	}
	return string(runes[:width]), string(runes[width:])
}
