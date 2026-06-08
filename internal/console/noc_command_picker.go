package console

import (
	"os/exec"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type commandPickerOverlay struct {
	title    string
	commands []nocCommandAction
}

type commandCopyMsg struct {
	command string
	err     error
}

func defaultClipboardCopy(text string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

func (m *NOCModel) beginCommandPicker() {
	commands := m.selectedCommandActions()
	if len(commands) == 0 {
		m.actNote = "no action commands for this row"
		return
	}
	title := "COPY COMMAND"
	if n, ok := m.selectedNode(); ok {
		switch n.kind {
		case nodeProject:
			title = "COPY COMMAND  " + n.project.Project
		case nodeSession:
			title = "COPY COMMAND  " + n.session.Name
		case nodeAgent:
			title = "COPY COMMAND  " + agentLabel(n.agent)
		}
	}
	if len(commands) > 9 {
		commands = commands[:9]
	}
	m.commandPicker = &commandPickerOverlay{title: title, commands: commands}
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
