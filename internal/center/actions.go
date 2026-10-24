package center

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// formState is the "new task" form: one field per input of the contract's
// new_task action, in the action's own order.
type formState struct {
	squad  int
	action Action
	field  int
	values map[string]string
}

func (f formState) current() ActionInput { return f.action.Inputs[f.field] }

func (m *Model) startNew() (tea.Model, tea.Cmd) {
	squad := 0
	if r, ok := m.selected(); ok {
		squad = r.Squad
	}
	if squad >= len(m.fleet.Squads) {
		m.note = "no status loaded yet"
		return m, nil
	}
	a := m.fleet.Squads[squad].Actions.NewTask
	if err := validateAction(a); err != nil {
		m.note = "new: " + err.Error()
		return m, nil
	}
	m.form = formState{squad: squad, action: a, values: map[string]string{}}
	// Pre-select the first choice of a choice input.
	for _, in := range a.Inputs {
		if len(in.Choices) > 0 {
			m.form.values[in.Name] = in.Choices[0]
		}
	}
	m.mode = modeNew
	return m, nil
}

func (m *Model) keyNew(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := &m.form
	in := f.current()
	switch k.String() {
	case "esc":
		m.mode = modeList
		return m, nil
	case "enter":
		if in.Required && strings.TrimSpace(f.values[in.Name]) == "" {
			m.note = in.Name + " is required"
			return m, nil
		}
		m.note = ""
		if f.field+1 < len(f.action.Inputs) {
			f.field++
			return m, nil
		}
		m.mode = modeList
		c, a, vals := m.client, f.action, f.values
		m.note = "running " + a.Label + "..."
		return m, func() tea.Msg {
			out, err := c.NewTask(context.Background(), a, vals)
			return newTaskMsg{out: out, err: err}
		}
	case "up", "shift+tab":
		if f.field > 0 {
			f.field--
		}
	case "down", "tab":
		if f.field+1 < len(f.action.Inputs) {
			f.field++
		}
	case "left", "right":
		if len(in.Choices) > 0 {
			i := indexOf(in.Choices, f.values[in.Name])
			if k.String() == "left" {
				i = (i + len(in.Choices) - 1) % len(in.Choices)
			} else {
				i = (i + 1) % len(in.Choices)
			}
			f.values[in.Name] = in.Choices[i]
		}
	case "backspace":
		if len(in.Choices) == 0 {
			r := []rune(f.values[in.Name])
			if len(r) > 0 {
				f.values[in.Name] = string(r[:len(r)-1])
			}
		}
	default:
		if len(in.Choices) == 0 && (k.Type == tea.KeyRunes || k.Type == tea.KeySpace) {
			f.values[in.Name] += string(k.Runes)
		}
	}
	return m, nil
}

func indexOf(xs []string, s string) int {
	for i, x := range xs {
		if x == s {
			return i
		}
	}
	return 0
}

// convState is the lead conversation.
type convState struct {
	squad    int
	lead     string
	operator string
	root     string
	msgs     []Message
	loading  bool
	body     string
	staged   bool
	note     string
	scroll   int
}

func (m *Model) startConversation(r Row) (tea.Model, tea.Cmd) {
	if r.Squad >= len(m.fleet.Squads) {
		return m, nil
	}
	s := m.fleet.Squads[r.Squad]
	lead := Lead(s)
	if lead == "" {
		m.note = noLeadReason
		return m, nil
	}
	if s.Root == "" {
		m.note = "status carries no AMQ root"
		return m, nil
	}
	m.conv = convState{squad: r.Squad, lead: lead, operator: Operator(s), root: s.Root, loading: true}
	m.mode = modeConversation
	return m, m.readConversation()
}

// noLeadReason is shown in the details pane and as the note when enter has
// no lead to open a conversation with.
const noLeadReason = "no lead seat in status"

const conversationLimit = 60

func (m *Model) readConversation() tea.Cmd {
	c, cv := m.client, m.conv
	return func() tea.Msg {
		msgs, err := c.Conversation(context.Background(), cv.root, cv.operator, cv.lead, conversationLimit)
		return convMsg{msgs: msgs, err: err}
	}
}

func (m *Model) keyConversation(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	cv := &m.conv
	switch k.String() {
	case "esc":
		if cv.staged {
			cv.staged = false
			return m, nil
		}
		m.mode = modeList
		return m, nil
	case "enter":
		if strings.TrimSpace(cv.body) == "" {
			return m, nil
		}
		if !cv.staged {
			cv.staged = true // preview first: the staged line names thread and recipient
			return m, nil
		}
		c, root, op, lead, body := m.client, cv.root, cv.operator, cv.lead, cv.body
		cv.note = "sending..."
		return m, func() tea.Msg {
			return sentMsg{err: c.Send(context.Background(), root, op, lead, body)}
		}
	case "pgup":
		cv.scroll += 5
	case "pgdown":
		if cv.scroll -= 5; cv.scroll < 0 {
			cv.scroll = 0
		}
	case "backspace":
		cv.staged = false
		r := []rune(cv.body)
		if len(r) > 0 {
			cv.body = string(r[:len(r)-1])
		}
	default:
		if k.Type == tea.KeyRunes || k.Type == tea.KeySpace {
			cv.staged = false
			cv.body += string(k.Runes)
		}
	}
	return m, nil
}

func (cv convState) stagedLine() string {
	return fmt.Sprintf("enter again to send to %s on %s, esc to edit", cv.lead, P2PThread(cv.operator, cv.lead))
}
