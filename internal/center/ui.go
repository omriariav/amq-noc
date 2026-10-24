package center

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// FoldLimit is how many rows of a group show before the "show more" fold.
const FoldLimit = 5

// RefreshInterval is how often the status call runs.
const RefreshInterval = 5 * time.Second

type mode int

const (
	modeList mode = iota
	modeHelp
	modeNew
	modeConversation
)

type itemKind int

const (
	itemBlank itemKind = iota
	itemHeader
	itemRow
	itemFold
)

// item is one line of the list: a spacer, a group header, a row, or a fold.
type item struct {
	kind  itemKind
	group string
	count int // header: rows in group; fold: hidden rows
	row   Row
}

func (i item) selectable() bool { return i.kind == itemRow || i.kind == itemFold }

// Model is the command center's bubbletea model.
type Model struct {
	client Client
	now    func() time.Time

	fleet    Fleet
	rows     []Row // every row, sorted
	counts   [tabCount]int
	tab      Tab
	items    []item
	cursor   int // index into items (always a selectable item when any exists)
	top      int // first visible line
	expanded map[string]bool

	width, height int
	mode          mode
	note          string
	loaded        bool
	loading       bool
	statusErr     string

	form    formState
	conv    convState
	convGen int

	newRunning bool // a new-task call is in flight
}

// New builds the model. now supplies the clock for relative times.
func New(c Client, now func() time.Time) *Model {
	if now == nil {
		now = time.Now
	}
	return &Model{client: c, now: now, width: 120, height: 30, expanded: map[string]bool{}}
}

// SetFleet loads a status document (used by tests and by the status message).
func (m *Model) SetFleet(f Fleet) {
	key := m.selectedKey()
	m.fleet = f
	m.rows = BuildRows(f)
	m.counts = Counts(m.rows)
	m.loaded = true
	m.rebuild()
	m.selectKey(key)
	m.fix()
}

// SetSize sets the terminal size.
func (m *Model) SetSize(w, h int) {
	m.width, m.height = w, h
	m.fix()
}

type statusMsg struct {
	fleet Fleet
	err   error
}
type tickMsg struct{}
type newTaskMsg struct {
	out string
	err error
}
type convMsg struct {
	gen  int
	msgs []Message
	err  error
}
type sentMsg struct {
	gen int
	err error
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	m.loading = true
	return tea.Batch(m.fetch(), tick())
}

func tick() tea.Cmd {
	return tea.Tick(RefreshInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *Model) fetch() tea.Cmd {
	c := m.client
	return func() tea.Msg {
		f, err := c.Status(context.Background())
		return statusMsg{fleet: f, err: err}
	}
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
	case tickMsg:
		if m.loading {
			return m, tick()
		}
		m.loading = true
		return m, tea.Batch(m.fetch(), tick())
	case statusMsg:
		m.loading = false
		if msg.err != nil {
			m.statusErr = msg.err.Error()
			m.note = "status: " + m.statusErr
		} else {
			m.statusErr = ""
			m.SetFleet(msg.fleet)
		}
	case newTaskMsg:
		m.newRunning = false
		if msg.err != nil {
			m.note = "new task failed: " + msg.err.Error()
		} else {
			m.note = "new task: " + orDash(msg.out)
		}
		m.loading = true
		return m, m.fetch()
	case convMsg:
		if msg.gen != m.convGen {
			return m, nil // a result of a conversation that is no longer open
		}
		m.conv.loading = false
		if msg.err != nil {
			m.conv.note = "thread: " + msg.err.Error()
		} else {
			m.conv.msgs = msg.msgs
			m.conv.note = ""
		}
	case sentMsg:
		if msg.gen != m.convGen {
			return m, nil
		}
		m.conv.sending = false
		if msg.err != nil {
			m.conv.note = "send failed: " + msg.err.Error()
		} else {
			m.conv.note = "sent"
			m.conv.body = ""
			m.conv.staged = false
			return m, m.readConversation()
		}
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

// key dispatches one key press by mode.
func (m *Model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	if s == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.mode {
	case modeHelp:
		if s == "esc" || s == "?" || s == "q" || s == "enter" {
			m.mode = modeList
		}
		return m, nil
	case modeNew:
		return m.keyNew(k)
	case modeConversation:
		return m.keyConversation(k)
	}
	m.note = ""
	switch s {
	case "q":
		return m, tea.Quit
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "home", "g":
		m.moveTo(0, 1)
	case "end", "G":
		m.moveTo(len(m.items)-1, -1)
	case "tab":
		m.setTab((m.tab + 1) % tabCount)
	case "shift+tab":
		m.setTab((m.tab + tabCount - 1) % tabCount)
	case "esc":
		if m.tab != TabAll {
			m.setTab(TabAll)
		}
	case "?":
		m.mode = modeHelp
	case "r":
		if !m.loading {
			m.loading = true
			return m, m.fetch()
		}
	case "enter", "right", "l":
		return m.open()
	case "n":
		return m.startNew()
	}
	m.fix()
	return m, nil
}

func (m *Model) setTab(t Tab) {
	m.tab = t
	m.cursor, m.top = 0, 0
	m.rebuild()
	m.fix()
}

func (m *Model) move(d int) {
	for i := m.cursor + d; i >= 0 && i < len(m.items); i += d {
		if m.items[i].selectable() {
			m.cursor = i
			return
		}
	}
}

func (m *Model) moveTo(start, d int) {
	for i := start; i >= 0 && i < len(m.items); i += d {
		if m.items[i].selectable() {
			m.cursor = i
			return
		}
	}
}

// open is the enter key: it expands a fold, or opens the AMQ conversation
// with the lead of the selected row's squad.
func (m *Model) open() (tea.Model, tea.Cmd) {
	if m.cursor >= len(m.items) {
		return m, nil
	}
	it := m.items[m.cursor]
	switch it.kind {
	case itemFold:
		m.expanded[it.group] = true
		m.rebuild()
		m.fix()
		return m, nil
	case itemRow:
		return m.startConversation(it.row)
	}
	return m, nil
}

func (m *Model) selected() (Row, bool) {
	if m.cursor < len(m.items) && m.items[m.cursor].kind == itemRow {
		return m.items[m.cursor].row, true
	}
	return Row{}, false
}

// rowKey identifies a row by its squad's root and session, not by the squad's
// position in the fleet, so a reordered fleet keeps the selection.
func (m *Model) rowKey(r Row) string {
	sq := ""
	if r.Squad < len(m.fleet.Squads) {
		sq = m.fleet.Squads[r.Squad].Root + "|" + m.fleet.Squads[r.Squad].Session
	}
	if r.IsTask() {
		return fmt.Sprintf("%s/task/%s", sq, r.Task.ID)
	}
	return fmt.Sprintf("%s/seat/%s", sq, r.Seat.Handle)
}

func (m *Model) selectedKey() string {
	if r, ok := m.selected(); ok {
		return m.rowKey(r)
	}
	return ""
}

func (m *Model) selectKey(k string) {
	if k == "" {
		return
	}
	for i, it := range m.items {
		if it.kind == itemRow && m.rowKey(it.row) == k {
			m.cursor = i
			return
		}
	}
}

// rebuild lays the current tab's rows out as list items.
func (m *Model) rebuild() {
	m.items = m.items[:0]
	for gi, g := range GroupRows(Filter(m.rows, m.tab)) {
		if gi > 0 {
			m.items = append(m.items, item{kind: itemBlank})
		}
		m.items = append(m.items, item{kind: itemHeader, group: g.Project, count: len(g.Rows)})
		shown := g.Rows
		if len(shown) > FoldLimit && !m.expanded[g.Project] {
			shown = shown[:FoldLimit]
		}
		for _, r := range shown {
			m.items = append(m.items, item{kind: itemRow, group: g.Project, row: r})
		}
		if hidden := len(g.Rows) - len(shown); hidden > 0 {
			m.items = append(m.items, item{kind: itemFold, group: g.Project, count: hidden})
		}
	}
	if m.cursor >= len(m.items) {
		m.cursor = len(m.items) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if len(m.items) > 0 && !m.items[m.cursor].selectable() {
		m.moveTo(m.cursor, 1)
		if !m.items[m.cursor].selectable() {
			m.moveTo(m.cursor, -1)
		}
	}
}

// fix keeps the cursor line inside the list viewport.
func (m *Model) fix() {
	h := m.listHeight()
	if h < 1 {
		h = 1
	}
	if m.cursor < m.top {
		m.top = m.cursor
		// show the group header above the first row when scrolling to the top of a group
		if m.top > 0 && m.items[m.top-1].kind == itemHeader {
			m.top--
		}
	}
	if m.cursor >= m.top+h {
		m.top = m.cursor - h + 1
	}
	if m.top < 0 {
		m.top = 0
	}
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// Lead returns the handle of the lead of a squad, if the contract names one:
// the optional lead_handle field, else the single seat whose role is "lead".
func Lead(s Status) string {
	if s.LeadHandle != "" {
		return s.LeadHandle
	}
	for _, seat := range s.Seats {
		if seat.Role == "lead" {
			return seat.Handle
		}
	}
	return ""
}

// Operator is the AMQ handle the owner speaks as in a conversation.
func Operator(_ Status) string {
	return "user"
}
