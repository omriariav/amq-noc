package center

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

const (
	wideWidth     = 100 // at or above this the details pane sits beside the list
	narrowDetailH = 11  // lines of the details pane below the list on a narrow screen
	chromeLines   = 4   // title, tabs, rule, footer
	statusColW    = 18
	updatedColW   = 8
)

var (
	dim      = lipgloss.NewStyle().Faint(true)
	bold     = lipgloss.NewStyle().Bold(true)
	selStyle = lipgloss.NewStyle().Reverse(true).Bold(true)
	tabOn    = lipgloss.NewStyle().Reverse(true).Bold(true)
	hot      = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true)
	green    = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
)

func (m *Model) wide() bool { return m.width >= wideWidth }

func (m *Model) bodyHeight() int {
	h := m.height - chromeLines
	if m.warningCount() > 0 {
		h-- // one line above the footer for the warning
	}
	if h < 3 {
		h = 3
	}
	return h
}

// listHeight is the number of list lines under the column header row.
func (m *Model) listHeight() int {
	if m.wide() {
		return m.bodyHeight() - 1
	}
	return m.bodyHeight() - narrowDetailH - 2
}

// columnHeader is the dim header row above the first group.
func (m *Model) columnHeader(w int) string {
	titleW := w - 2 - 2 - statusColW - 1 - updatedColW - 1
	if titleW < 8 {
		titleW = 8
	}
	return dim.Render(pad("    Tasks", 4+titleW+1) + pad("Status", statusColW) + " " + padLeft("Updated", updatedColW))
}

func (m *Model) listWidth() int {
	if m.wide() {
		return m.width * 62 / 100
	}
	return m.width
}

// View implements tea.Model.
func (m *Model) View() string {
	var b strings.Builder
	b.WriteString(m.titleLine())
	b.WriteByte('\n')
	b.WriteString(m.tabsLine())
	b.WriteByte('\n')
	b.WriteString(dim.Render(strings.Repeat("─", m.width)))
	b.WriteByte('\n')
	var body []string
	switch m.mode {
	case modeHelp:
		body = m.clampHelp(m.helpLines(), m.bodyHeight())
	case modeNew:
		body = m.newLines()
	case modeConversation:
		body = m.conversationLines()
	default:
		body = m.listBody()
	}
	h := m.bodyHeight()
	for i := 0; i < h; i++ {
		if i < len(body) {
			b.WriteString(body[i])
		}
		b.WriteByte('\n')
	}
	if w := m.warningLine(); w != "" {
		b.WriteString(w)
		b.WriteByte('\n')
	}
	b.WriteString(m.footerLine())
	return b.String()
}

// allWarnings lists every warning of every squad, one line each.
func (m *Model) allWarnings() []string {
	out := append([]string{}, m.fleet.Warnings...)
	for _, s := range m.fleet.Squads {
		out = append(out, s.Warnings...)
	}
	return out
}

// warningLine is the first warning, with "(+N more)" when there are several.
func (m *Model) warningLine() string {
	ws := m.allWarnings()
	if len(ws) == 0 {
		return ""
	}
	line := "warning: " + ws[0]
	if len(ws) > 1 {
		line += fmt.Sprintf(" (+%d more)", len(ws)-1)
	}
	return hot.Render(clamp(line, m.width))
}

func (m *Model) titleLine() string {
	t := bold.Render("Agent command center")
	switch {
	case !m.loaded && m.statusErr == "":
		t += dim.Render("  loading status...")
	case len(m.fleet.Squads) > 1:
		t += dim.Render(fmt.Sprintf("  Group: Project  %d squads", len(m.fleet.Squads)))
	default:
		t += dim.Render("  Group: Project")
	}
	if w := m.warningCount(); w > 0 {
		t += hot.Render(fmt.Sprintf("  %d warnings", w))
	}
	if m.statusErr != "" {
		t += hot.Render("  status unavailable, showing last data")
	}
	return t
}

func (m *Model) warningCount() int {
	n := len(m.fleet.Warnings)
	for _, s := range m.fleet.Squads {
		n += len(s.Warnings)
	}
	return n
}

func (m *Model) tabsLine() string {
	var parts []string
	for t := TabAll; t < tabCount; t++ {
		label := fmt.Sprintf("%s %d", tabLabel[t], m.counts[t])
		if t == m.tab {
			parts = append(parts, tabOn.Render("["+label+"]"))
		} else {
			parts = append(parts, dim.Render(" "+label+" "))
		}
	}
	left := strings.Join(parts, " ")
	right := bold.Render("tab/⇧tab") + dim.Render(" filter")
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m *Model) footerLine() string {
	keys := bold.Render("?") + dim.Render(" help  ") + bold.Render("esc") + dim.Render(" back  ") +
		bold.Render("↑/↓") + dim.Render(" move  ") + bold.Render("enter") + dim.Render(" open  ") +
		bold.Render("n") + dim.Render(" new")
	switch m.mode {
	case modeConversation:
		keys = bold.Render("enter") + dim.Render(" send  ") + bold.Render("esc") + dim.Render(" back  ") +
			bold.Render("pgup/pgdn") + dim.Render(" scroll")
	case modeNew:
		keys = bold.Render("enter") + dim.Render(" next/run  ") + bold.Render("←/→") + dim.Render(" choose  ") +
			bold.Render("esc") + dim.Render(" cancel")
	}
	if m.note != "" {
		return clamp(keys+"  "+hot.Render(m.note), m.width)
	}
	return keys
}

// listBody is the list, with the details pane beside or below it.
func (m *Model) listBody() []string {
	list := append([]string{m.columnHeader(m.listWidth())}, m.listLines(m.listWidth(), m.listHeight())...)
	det := m.detailsLines(m.detailsWidth())
	if !m.wide() {
		out := append([]string{}, list...)
		for len(out) < m.listHeight()+1 {
			out = append(out, "")
		}
		out = append(out, dim.Render(strings.Repeat("─", m.width)))
		if len(det) > narrowDetailH {
			det = det[:narrowDetailH]
		}
		return append(out, det...)
	}
	lw := m.listWidth()
	rows := m.bodyHeight()
	out := make([]string, rows)
	for i := 0; i < rows; i++ {
		l, d := "", ""
		if i < len(list) {
			l = list[i]
		}
		if i < len(det) {
			d = det[i]
		}
		out[i] = pad(l, lw) + dim.Render(" │ ") + d
	}
	return out
}

func (m *Model) detailsWidth() int {
	if m.wide() {
		return m.width - m.listWidth() - 3
	}
	return m.width
}

func (m *Model) listLines(w, h int) []string {
	if len(m.items) == 0 {
		switch {
		case !m.loaded:
			return []string{dim.Render("  waiting for the first status...")}
		case m.tab == TabAll && m.fleet.IsFleet && len(m.fleet.Squads) == 0:
			return []string{dim.Render(clamp("  no squads in the fleet", w))}
		case m.tab == TabAll:
			return []string{dim.Render("  no seats or tasks in status")}
		default:
			return []string{dim.Render("  nothing in " + tabLabel[m.tab])}
		}
	}
	end := m.top + h
	if end > len(m.items) {
		end = len(m.items)
	}
	now := m.now()
	var out []string
	for i := m.top; i < end; i++ {
		out = append(out, m.itemLine(i, w, now))
	}
	return out
}

func (m *Model) itemLine(i, w int, now time.Time) string {
	it := m.items[i]
	switch it.kind {
	case itemHeader:
		return dim.Render(clamp(fmt.Sprintf("%s  %d", it.label, it.count), w))
	case itemFold:
		line := fmt.Sprintf("    Show more (%d)", it.count)
		if i == m.cursor {
			return selStyle.Render(pad("›"+line[1:], w))
		}
		return dim.Render(clamp(line, w))
	case itemRow:
		r := it.row
		glyph := "○"
		switch r.Bucket {
		case BucketNeedsYou:
			glyph = "!"
		case BucketWorking:
			glyph = "●"
		case BucketReady:
			glyph = "◌"
		}
		if r.NotLive() {
			glyph = "⊘" // the contract says the seat is not live
		}
		status := StatusLabel(m.fleet, r)
		upd := RelTime(r.Updated, now)
		titleW := w - 2 - 2 - statusColW - 1 - updatedColW - 1
		if titleW < 8 {
			titleW = 8
		}
		prefix := "  "
		if i == m.cursor {
			prefix = "› "
		}
		line := prefix + glyph + " " + pad(clamp(oneLine(r.Title), titleW), titleW) + " " +
			pad(clamp(status, statusColW), statusColW) + " " + padLeft(upd, updatedColW)
		if i == m.cursor {
			return selStyle.Render(pad(line, w))
		}
		if r.Bucket == BucketNeedsYou {
			return hot.Render(line)
		}
		return line
	}
	return ""
}

// detailsLines is the details pane of the selected row.
func (m *Model) detailsLines(w int) []string {
	r, ok := m.selected()
	if !ok {
		if m.cursor < len(m.items) && m.items[m.cursor].kind == itemFold {
			return []string{bold.Render("Details"), "", dim.Render("enter shows the rest of this group")}
		}
		return []string{bold.Render("Details")}
	}
	if r.Waiting != nil {
		return m.waitingLines(r)
	}
	var out []string
	if r.IsTask() {
		out = append(out, bold.Render("Task details"))
	} else {
		out = append(out, bold.Render("Seat details"))
	}
	out = append(out, "")
	out = append(out, hot.Render(clamp(oneLine(r.Title), w)))
	status := StatusLabel(m.fleet, r)
	if r.State != "" || r.Reason != "" {
		status += dim.Render("  " + strings.Trim(r.State+" "+r.Reason, " "))
	}
	out = append(out, "● "+status)
	if r.NotLive() {
		out = append(out, hot.Render("⊘ not live: "+clamp("contract live=false", w-13)))
	}
	// Attention sits right under the status: it says why the row is stuck,
	// and the pane may be too short to reach the end.
	out = append(out, m.attentionLines(r, w)...)
	out = append(out, "")

	model, effort := "-", "-"
	if r.Seat != nil {
		model, effort = orDash(r.Seat.Model), orDash(r.Seat.Effort)
	} else if r.IsTask() && r.Task.Pool != nil {
		model, effort = orDash(r.Task.Pool.Model), orDash(r.Task.Pool.Effort)
	}
	tin, tout := "-", "-"
	last, prompt := "", "-"
	if r.Seat != nil {
		tin, tout = tokens(r.Seat.TokensIn), tokens(r.Seat.TokensOut)
		last = r.Seat.LastMessage
		prompt = orDash(r.Seat.PromptState)
	}
	lastLines := []string{"-"}
	if strings.TrimSpace(last) != "" {
		lastLines = wrap(last, w)
	}
	if !m.wide() {
		// compact form: every field, no spacer lines
		out = out[:len(out)-1]
		out = append(out, dim.Render("Model: ")+model+dim.Render("  Reasoning: ")+effort)
		out = append(out, dim.Render("Project: ")+clamp(DisplayPathFull(r.Project), w-9))
		out = append(out, dim.Render("Tokens: ")+tin+" in · "+tout+" out"+dim.Render("  Prompt: ")+prompt)
		if len(lastLines) > 3 {
			lastLines = append(lastLines[:2], clamp(lastLines[2], w-1)+"…")
		}
		out = append(out, dim.Render("Last message: ")+lastLines[0])
		out = append(out, lastLines[1:]...)
	} else {
		out = append(out, dim.Render("Model: ")+model, dim.Render("Reasoning: ")+effort, "")
		out = append(out, dim.Render("Project"))
		out = append(out, wrap(DisplayPathFull(r.Project), w)...)
		out = append(out, dim.Render("Tokens: ")+tin+" in · "+tout+" out", "")
		out = append(out, dim.Render("Last message"))
		out = append(out, lastLines...)
		out = append(out, "", dim.Render("Prompt"), prompt)
	}
	if g := m.gateFor(r); g != nil {
		out = append(out, "", dim.Render("Gate"),
			fmt.Sprintf("%s %s %s", g.ID, g.ActionClass, g.State), dim.Render("raised by "+orDash(g.RaisedBy)))
	}
	if r.Squad < len(m.fleet.Squads) && Lead(m.fleet.Squads[r.Squad]) == "" {
		out = append(out, "", dim.Render("enter: "+noLeadReason))
	}
	return out
}

// waitingLines is the details pane of a squad waiting for the fleet cap.
func (m *Model) waitingLines(r Row) []string {
	wq := r.Waiting
	out := []string{bold.Render("Waiting squad"), "", hot.Render(oneLine(wq.Squad)),
		"● " + orDash(wq.WaitReason) + dim.Render("  cap "+orDash(wq.Cap)), ""}
	out = append(out, dim.Render("Repository: ")+orDash(wq.Repository),
		dim.Render("Project: ")+DisplayPathFull(wq.ProjectPath),
		dim.Render("Waiting since: ")+RelTime(&wq.Since, m.now()))
	if strings.TrimSpace(wq.Detail) != "" {
		out = append(out, dim.Render("Detail: ")+oneLine(wq.Detail))
	}
	out = append(out, "", dim.Render("no squad document yet; nothing to open"))
	return out
}

// attentionLines renders the row's attention items (kind, severity, then the
// subject) and the squad-level ones (the first, with "(+N more)").
func (m *Model) attentionLines(r Row, w int) []string {
	var out []string
	if items := m.attentionFor(r); len(items) > 0 {
		out = append(out, "", dim.Render(fmt.Sprintf("Attention (%d)", len(items))))
		for _, a := range items {
			out = append(out, hot.Render(clamp(orDash(a.Kind), w))+dim.Render("  "+orDash(a.Severity)))
			out = append(out, m.fit(oneLine(a.Subject), w)...)
		}
	}
	if items := m.squadAttention(r); len(items) > 0 {
		out = append(out, "", dim.Render(fmt.Sprintf("Squad attention (%d)", len(items))))
		line := orDash(items[0].Kind) + ": " + oneLine(items[0].Subject)
		if len(items) > 1 {
			line += fmt.Sprintf(" (+%d more)", len(items)-1)
		}
		out = append(out, m.fit(line, w)...)
	}
	return out
}

// fit wraps s in the wide layout; the narrow details pane is a few lines
// high, so there s is clamped to one line to leave room for the rest.
func (m *Model) fit(s string, w int) []string {
	if m.wide() {
		return wrap(s, w)
	}
	return []string{clamp(s, w)}
}

// attentionFor lists the attention items the contract ties to the row,
// leaving out gate items, which the Gate section shows: on a task row the
// items of that task, and on any row the seat-level items (no task id) of the
// row's seat, which for a busy seat is the lease holder of its task row.
// Show only: no item's action runs here.
func (m *Model) attentionFor(r Row) []Attention {
	if r.Squad >= len(m.fleet.Squads) {
		return nil
	}
	var out []Attention
	for _, a := range m.fleet.Squads[r.Squad].Attention {
		if a.GateID != "" {
			continue
		}
		if (r.IsTask() && a.TaskID == r.Task.ID) || (a.TaskID == "" && r.Seat != nil && a.Seat != "" && a.Seat == r.Seat.Handle) {
			out = append(out, a)
		}
	}
	return out
}

// squadAttention lists the squad-level attention items, shown on every row of
// that squad: items with no seat, task or gate, and seat-level items whose
// seat has no row (a seat the status does not list), so no item is lost.
func (m *Model) squadAttention(r Row) []Attention {
	if r.Squad >= len(m.fleet.Squads) {
		return nil
	}
	shown := map[string]bool{}
	for _, row := range m.rows {
		if row.Squad == r.Squad && row.Seat != nil {
			shown[row.Seat.Handle] = true
		}
	}
	var out []Attention
	for _, a := range m.fleet.Squads[r.Squad].Attention {
		if a.TaskID == "" && a.GateID == "" && (a.Seat == "" || !shown[a.Seat]) {
			out = append(out, a)
		}
	}
	return out
}

// gateFor finds the gate that the attention queue ties to the row.
func (m *Model) gateFor(r Row) *Gate {
	if r.Squad >= len(m.fleet.Squads) {
		return nil
	}
	s := m.fleet.Squads[r.Squad]
	for _, a := range s.Attention {
		if a.GateID == "" {
			continue
		}
		if (r.IsTask() && a.TaskID == r.Task.ID) || (!r.IsTask() && a.Seat == r.Seat.Handle) {
			for i := range s.Gates {
				if s.Gates[i].ID == a.GateID {
					return &s.Gates[i]
				}
			}
		}
	}
	return nil
}

func (m *Model) helpLines() []string {
	out := m.keyHelp()
	if ws := m.allWarnings(); len(ws) > 0 {
		out = append(out, "", bold.Render(fmt.Sprintf("Warnings (%d)", len(ws))))
		for _, w := range ws {
			out = append(out, "  "+clamp(w, m.width-2))
		}
	}
	return out
}

// clampHelp fits the help overlay into h lines. When it is longer, the last
// line is a fold, "(+N more)", and up/down/pgup/pgdn scroll the rest.
func (m *Model) clampHelp(lines []string, h int) []string {
	if len(lines) <= h {
		m.helpTop = 0
		return lines
	}
	room := h - 1
	if room < 1 {
		room = 1
	}
	if max := len(lines) - room; m.helpTop > max {
		m.helpTop = max
	}
	if m.helpTop < 0 {
		m.helpTop = 0
	}
	end := m.helpTop + room
	fold := fmt.Sprintf("(+%d more)", len(lines)-end)
	if m.helpTop > 0 {
		fold = fmt.Sprintf("(%d above, +%d more)", m.helpTop, len(lines)-end)
		if end >= len(lines) {
			fold = fmt.Sprintf("(%d above)", m.helpTop)
		}
	}
	return append(append([]string{}, lines[m.helpTop:end]...), dim.Render(fold+"  up/down scroll"))
}

func (m *Model) keyHelp() []string {
	return []string{
		bold.Render("Keys"), "",
		"  ↑/↓  j/k      move between rows",
		"  tab/shift+tab  cycle All, Needs you, Working, Ready, Inactive",
		"  enter          open the AMQ conversation with the lead; on a fold, show more",
		"  n              new task (runs the status contract's new-task action)",
		"  esc            back; from a filter tab, back to All",
		"  r              refresh now",
		"  up/down pgup/pgdn  scroll this help when it is taller than the screen",
		"  ?              this help",
		"  q, ctrl+c      quit", "",
		dim.Render("Status comes from `amq-squad status --json` every 5 seconds."),
		dim.Render("Buckets are the contract's; nothing is reclassified here."),
	}
}

func (m *Model) newLines() []string {
	f := m.form
	out := []string{bold.Render("New task"), dim.Render(strings.Join(f.action.Argv, " ")), ""}
	for i, in := range f.action.Inputs {
		mark := "  "
		if i == f.field {
			mark = "› "
		}
		v := f.values[in.Name]
		if len(in.Choices) > 0 {
			var cs []string
			for _, c := range in.Choices {
				if c == v {
					cs = append(cs, "["+c+"]")
				} else {
					cs = append(cs, " "+c+" ")
				}
			}
			v = strings.Join(cs, " ")
		} else if i == f.field {
			v += "▏"
		}
		req := ""
		if in.Required {
			req = dim.Render(" (required)")
		}
		out = append(out, mark+in.Name+": "+v+req)
	}
	return out
}

func (m *Model) conversationLines() []string {
	cv := m.conv
	w := m.width
	out := []string{bold.Render("Conversation with " + cv.lead), dim.Render(P2PThread(cv.operator, cv.lead) + " on " + cv.root), ""}
	var msgs []string
	if cv.loading {
		msgs = append(msgs, dim.Render("reading thread..."))
	}
	for _, mm := range cv.msgs {
		msgs = append(msgs, bold.Render(mm.From)+dim.Render("  "+RelTime(&mm.Created, m.now())))
		for _, l := range wrap(strings.TrimSpace(mm.Body), w-2) {
			msgs = append(msgs, "  "+l)
		}
		msgs = append(msgs, "")
	}
	if cv.note != "" {
		msgs = append(msgs, hot.Render(cv.note))
	}
	room := m.bodyHeight() - len(out) - 3
	if room < 1 {
		room = 1
	}
	end := len(msgs) - cv.scroll
	if end > len(msgs) {
		end = len(msgs)
	}
	if end < 0 {
		end = 0
	}
	start := end - room
	if start < 0 {
		start = 0
	}
	out = append(out, msgs[start:end]...)
	for len(out) < m.bodyHeight()-3 {
		out = append(out, "")
	}
	out = append(out, dim.Render(strings.Repeat("─", w)))
	if cv.staged {
		out = append(out, "> "+clamp(oneLine(cv.body), w-2), hot.Render(clamp(cv.stagedLine(), w)))
	} else {
		out = append(out, "> "+clamp(oneLine(cv.body), w-3)+"▏")
	}
	return out
}

func tokens(p *int64) string {
	if p == nil {
		return "-"
	}
	n := float64(*p)
	switch {
	case *p >= 1_000_000:
		return fmt.Sprintf("%.1fM", n/1e6)
	case *p >= 10_000:
		return fmt.Sprintf("%.1fK", n/1e3)
	}
	return fmt.Sprint(*p)
}

// DisplayPathFull keeps the whole path for the details pane.
func DisplayPathFull(p string) string {
	if p == "" {
		return "-"
	}
	return p
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// clamp shortens s to w display cells, ending with an ellipsis.
func clamp(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > w {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

func pad(s string, w int) string {
	if n := w - lipgloss.Width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

func padLeft(s string, w int) string {
	if n := w - lipgloss.Width(s); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}

// wrap breaks text on spaces to lines of at most w cells; a long word is cut.
func wrap(s string, w int) []string {
	if w < 8 {
		w = 8
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			for lipgloss.Width(word) > w {
				if line != "" {
					out = append(out, line)
					line = ""
				}
				r := []rune(word)
				cut := len(r)
				for cut > 1 && lipgloss.Width(string(r[:cut])) > w {
					cut--
				}
				out = append(out, string(r[:cut]))
				word = string(r[cut:])
			}
			switch {
			case line == "":
				line = word
			case lipgloss.Width(line)+1+lipgloss.Width(word) <= w:
				line += " " + word
			default:
				out = append(out, line)
				line = word
			}
		}
		out = append(out, line)
	}
	return out
}
