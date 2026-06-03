package console

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/omriariav/amq-noc/internal/state"
)

// Layout reserves rows for a header and a footer; the rest is the scrollable
// body. These are the only magic numbers in the layout and are kept here so the
// interactive phase can grow the chrome without hunting through render code.
const (
	headerRows = 2
	footerRows = 1
)

func viewportHeight(total int) int {
	h := total - headerRows - footerRows
	if h < 1 {
		return 1
	}
	return h
}

func newViewport(width, height int) viewport.Model {
	vp := viewport.New(width, height)
	return vp
}

// Style palette. lipgloss is confined to this package; the literal text is
// always the source of truth and color is layered on top (matching the
// TEXT-led contract the status board established). lipgloss's default renderer
// auto-detects the terminal's color capability, so on a non-TTY / NO_COLOR /
// dumb terminal these styles emit plain text — the literal stays readable.
var (
	styleHeader   = lipgloss.NewStyle().Bold(true)
	styleFooter   = lipgloss.NewStyle().Faint(true)
	styleFaint    = lipgloss.NewStyle().Faint(true)
	styleSelected = lipgloss.NewStyle().Bold(true).Reverse(true)
	styleErr      = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))  // red
	styleNeedsYou = lipgloss.NewStyle().Foreground(lipgloss.Color("13")) // magenta
	styleAtRisk   = lipgloss.NewStyle().Foreground(lipgloss.Color("11")) // yellow
	styleBlocked  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))  // red
	styleAlive    = lipgloss.NewStyle().Foreground(lipgloss.Color("10")) // green
)

// colorEnabled reports whether the terminal supports color. On the uncolored
// (Ascii) profile — a non-TTY, NO_COLOR, or dumb terminal — decorative glyphs
// fall back to plain ASCII markers so the board stays legible without ANSI.
func colorEnabled() bool {
	return lipgloss.ColorProfile().Name() != "Ascii"
}

// glyph returns a decorative marker for a triage/state, with a no-color ASCII
// fallback. The glyph is DECORATIVE: the literal tier word always accompanies it,
// so dropping the glyph never loses meaning.
func triageGlyph(t state.Triage) string {
	if !colorEnabled() {
		switch t {
		case state.TriageNeedsYou:
			return "!"
		case state.TriageBlocked:
			return "x"
		case state.TriageGated:
			return "g"
		case state.TriageAtRisk:
			return "~"
		default:
			return "-"
		}
	}
	switch t {
	case state.TriageNeedsYou:
		return "●"
	case state.TriageBlocked:
		return "■"
	case state.TriageGated:
		return "◆"
	case state.TriageAtRisk:
		return "▲"
	default:
		return "·"
	}
}

// livenessGlyph returns a decorative run-state marker with a no-color fallback.
func livenessGlyph(l state.Liveness) string {
	if !colorEnabled() {
		switch l {
		case state.LivenessAlive:
			return "+"
		case state.LivenessWakeLive:
			return "w"
		case state.LivenessDeadMailboxLive:
			return "*"
		case state.LivenessStale:
			return "~"
		default:
			return "-"
		}
	}
	switch l {
	case state.LivenessAlive:
		return "●"
	case state.LivenessWakeLive:
		return "◆"
	case state.LivenessDeadMailboxLive:
		return "◐"
	case state.LivenessStale:
		return "○"
	default:
		return "·"
	}
}

// triageStyle picks the color for a triage tier (no-op on the Ascii profile).
func triageStyle(t state.Triage) lipgloss.Style {
	switch t {
	case state.TriageNeedsYou:
		return styleNeedsYou
	case state.TriageBlocked:
		return styleBlocked
	case state.TriageGated:
		return styleHeader
	case state.TriageAtRisk:
		return styleAtRisk
	default:
		return styleFaint
	}
}

// livenessStyle picks the color for a run-state (no-op on the Ascii profile).
func livenessStyle(l state.Liveness) lipgloss.Style {
	switch l {
	case state.LivenessAlive:
		return styleAlive
	case state.LivenessWakeLive:
		return styleAtRisk
	case state.LivenessDeadMailboxLive:
		return styleAtRisk
	case state.LivenessStale:
		return styleFaint
	default:
		return styleFaint
	}
}

// View renders the current frame. Before the first WindowSizeMsg sizes the
// viewport, it falls back to a plain board so a resize race never blanks the
// screen. The degraded NoTeam state renders its own explanatory screen. An open
// overlay (peek/attach/help) is drawn over the route as a read-only modal.
func (m Model) View() string {
	if m.noTeam {
		return m.renderNoTeam()
	}
	// Overlays are modal: when one is open it owns the frame (with the header for
	// context), so the read-only pane is unambiguous.
	if m.overlay != overlayNone {
		var b strings.Builder
		b.WriteString(m.renderHeader())
		b.WriteString("\n\n")
		b.WriteString(m.renderOverlay())
		b.WriteString("\n")
		b.WriteString(m.renderFooter())
		return b.String()
	}
	if !m.ready {
		// No size yet: render the headline + body directly so the first paint is
		// never empty and always leads with the triage rollup.
		return m.renderHeader() + "\n" + m.renderBody() + "\n" + m.renderFooter()
	}
	var b strings.Builder
	b.WriteString(m.renderHeader())
	b.WriteString("\n")
	b.WriteString(m.viewport.View())
	b.WriteString("\n")
	b.WriteString(m.renderFooter())
	return b.String()
}

// renderHeader composes the snapshot-wide triage rollup line — the headline an
// operator scans first — plus a breadcrumb of the active route and any filter.
func (m Model) renderHeader() string {
	head := styleHeader.Render(rollupHeadline(m.snapshot))
	crumb := m.breadcrumb()
	if crumb != "" {
		head += "\n" + styleFaint.Render(crumb)
	}
	return head
}

// breadcrumb names the current route and any active filter so the operator knows
// where they are. Empty on the bare board with no filter.
func (m Model) breadcrumb() string {
	var parts []string
	switch m.route {
	case routeSession:
		parts = append(parts, "session: "+m.session)
	case routeThread:
		parts = append(parts, "session: "+m.session+" · logs")
	}
	if m.filter.active() {
		parts = append(parts, "filter: "+m.filter.Raw)
	} else if m.filter.unknown {
		parts = append(parts, "filter: "+m.filter.Raw+" (unknown — showing all)")
	}
	return strings.Join(parts, "   ")
}

// renderFooter shows the key hints (route-aware) plus any sticky error or the
// live filter-input line.
func (m Model) renderFooter() string {
	if m.filtering {
		return styleHeader.Render("/"+m.filterInput) + styleFooter.Render("   (needs-you · gated · at-risk · blocked · unread · agent:<h> · model:<m> · session:<n>;  enter apply · esc cancel)")
	}
	hint := styleFooter.Render(m.keyHints())
	if m.err != nil {
		return styleErr.Render("warning: "+m.err.Error()) + "  " + hint
	}
	return hint
}

// keyHints returns the route-appropriate footer key legend.
func (m Model) keyHints() string {
	switch m.overlay {
	case overlayPeek, overlayAttach:
		return "esc close · q close"
	case overlayHelp:
		return "esc close"
	}
	switch m.route {
	case routeBoard:
		return "↑/↓ move · enter drill · space peek · a copy attach cmd · / filter · g refresh · ? help · q quit"
	case routeSession:
		return "↑/↓ move · enter expand · space peek · l logs · a copy attach cmd · t timeline · / filter · esc back · g refresh · q quit"
	case routeThread:
		return "↑/↓ scroll · esc back · g refresh · q quit"
	default:
		return "q quit"
	}
}

// renderBody renders the active route's scrollable content. The board reuses the
// SAME data the --once StaticBoard renders (attention-first sessions), so the
// interactive and CI surfaces never drift; detail/log are interactive-only.
func (m Model) renderBody() string {
	switch m.route {
	case routeBoard:
		return m.renderBoard()
	case routeSession:
		return m.renderDetail()
	case routeThread:
		return m.renderLog()
	default:
		return m.renderBoard()
	}
}

// renderNoTeam is the degraded failure-state screen: it explains why the
// console cannot show live data and points at setup, rather than aborting. The
// notice text is supplied by the runner (no team configured, or an unresolvable
// AMQ base root).
func (m Model) renderNoTeam() string {
	var b strings.Builder
	b.WriteString(styleHeader.Render("amq-squad console") + "\n\n")
	b.WriteString(m.noTeamNotice)
	if !strings.HasSuffix(m.noTeamNotice, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(styleFooter.Render("q quit"))
	return b.String()
}

// --- Interactive route renderers (board / detail / log) ---------------------

// renderBoard renders the SQUADS BOARD home view: a summary line from the triage
// rollup, then sessions grouped attention-first (needs-you / blocked / at-risk /
// running above stopped), reusing the same columns as the status board. The
// selected row is highlighted by stable id.
func (m Model) renderBoard() string {
	sessions := boardSessions(m.snapshot, m.filter)
	if len(sessions) == 0 {
		if m.filter.active() {
			return "no sessions match filter " + m.filter.Raw + ".\n(press / to change, esc to clear)"
		}
		return "no sessions found under " + m.snapshot.BaseRoot + ".\n" +
			"Run 'amq-squad up' to launch your team, or 'amq-squad doctor' to check setup."
	}
	// The brief's summary line: "<n> needs you · <n> at risk · <n> blocked".
	r := m.snapshot.Rollup
	summary := fmt.Sprintf("%s needs you · %s at risk · %s blocked",
		styleNeedsYou.Render(fmt.Sprint(r.NeedsYou)),
		styleAtRisk.Render(fmt.Sprint(r.AtRisk)),
		styleBlocked.Render(fmt.Sprint(r.Blocked)))

	var b strings.Builder
	b.WriteString(summary)
	b.WriteString("\n\n")

	lastGroup := ""
	for i, s := range sessions {
		g := boardGroup(s)
		if g != lastGroup {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(styleFaint.Render(groupHeading(g)) + "\n")
			lastGroup = g
		}
		b.WriteString(m.boardRow(s, sessionKey(s) == m.selectedID))
		b.WriteString("\n")
	}
	return b.String()
}

// boardRow renders one session line for the board: a triage glyph, the name, the
// rollup counts, and an agent-liveness summary — the same columns as the status
// board. The literal text is always present; color/glyph are decorative.
func (m Model) boardRow(s state.Session, selected bool) string {
	tier := sessionTier(s)
	glyph := triageStyle(tier).Render(triageGlyph(tier))
	name := sessionKey(s)
	counts := s.Rollup.String()
	live := agentLivenessSummary(s, m.now())
	line := fmt.Sprintf("%s %-20s  [%s]  %s", glyph, name, counts, live)
	return selectRow(line, selected)
}

// agentLivenessSummary renders a compact per-session agent run-state column with
// the session-aware vocabulary ("cto alive  qa stale-heartbeat (3d)"); a stopped
// session reads "stopped" for every agent. Decorative glyphs/color layer on top
// of the literal label; the label is the source of truth.
func agentLivenessSummary(s state.Session, now func() time.Time) string {
	if len(s.Agents) == 0 {
		return styleFaint.Render("(no agents)")
	}
	stopped := sessionStopped(s)
	parts := make([]string, 0, len(s.Agents))
	for _, a := range detailAgents(s, Filter{}) {
		g := livenessStyle(a.Liveness).Render(livenessGlyph(a.Liveness))
		parts = append(parts, fmt.Sprintf("%s %s %s", a.Handle, g, agentStateLabel(a, stopped, now)))
	}
	return strings.Join(parts, "  ")
}

// renderDetail renders the SQUAD DETAIL view: agents grouped by triage/state at
// the top, the COLLAPSED-THREAD BUS (one row per thread, urgency-sorted) in the
// middle, the EDGE list, and — when toggled with `t` — a TIMELINE pane of state
// transitions instead of the bus/edges.
func (m Model) renderDetail() string {
	s, ok := m.currentSession()
	if !ok {
		return "session " + m.session + " is no longer present.\n(press esc to return to the board)"
	}

	var b strings.Builder
	// Top: agent roster grouped by run-state.
	b.WriteString(styleHeader.Render("agents") + "\n")
	agents := detailAgents(s, m.filter)
	if len(agents) == 0 {
		b.WriteString(styleFaint.Render("  (no agents match)") + "\n")
	}
	stopped := sessionStopped(s)
	now := m.now()
	for _, a := range agents {
		id := "agent:" + a.Handle
		g := livenessStyle(a.Liveness).Render(livenessGlyph(a.Liveness))
		wake := ""
		if a.WakeHealth != "" {
			wake = "  wake:" + string(a.WakeHealth)
		}
		line := fmt.Sprintf("  %s %-12s %-7s %-18s%s", g, a.Handle, a.Engine, agentStateLabel(a, stopped, now), wake)
		b.WriteString(selectRow(line, id == m.selectedID) + "\n")
	}
	b.WriteString("\n")

	if m.timeline {
		// TIMELINE pane (state transitions, not raw messages).
		b.WriteString(styleHeader.Render("timeline") + "\n")
		b.WriteString(renderTimeline(s))
		return b.String()
	}

	// Middle: the collapsed-thread bus.
	b.WriteString(styleHeader.Render("threads") + "\n")
	threads := sortThreads(s, m.filter)
	if len(threads) == 0 {
		b.WriteString(styleFaint.Render("  (no threads)") + "\n")
	}
	for _, t := range threads {
		id := "thread:" + t.ID
		b.WriteString(selectRow("  "+busRow(t, m.snapshot.Rollup), id == m.selectedID) + "\n")
	}

	// Edge list.
	b.WriteString("\n")
	b.WriteString(styleHeader.Render("edges") + "\n")
	if len(s.Coordination.Edges) == 0 {
		b.WriteString(styleFaint.Render("  (no edges)") + "\n")
	}
	for _, e := range s.Coordination.Edges {
		b.WriteString(fmt.Sprintf("  %s -> %s  %dx\n", e.From, e.To, e.Count))
	}
	return b.String()
}

// busRow renders one collapsed-thread bus line, urgency-sorted upstream:
// "qa <-> cto  blocked · sign-off  3 msgs · 7m". The triage word and age are the
// load-bearing literals; the glyph/color are decorative.
func busRow(t state.ThreadSummary, _ state.TriageRollup) string {
	glyph := triageStyle(t.Triage).Render(triageGlyph(t.Triage))
	peers := peerPair(t.Participants)
	subj := t.Subject
	if subj == "" {
		subj = shortID(t.ID)
	}
	statusWord := triageStyle(t.Triage).Render(string(t.Triage))
	unread := ""
	if n := len(t.UnreadBy); n > 0 {
		unread = fmt.Sprintf(" · %d unread", n)
	}
	return fmt.Sprintf("%s %-15s  %s · %s  %d msgs · %s%s",
		glyph, peers, statusWord, subj, t.MessageCount, ageLabel(t.Freshness.Age), unread)
}

// peerPair renders a thread's participants as "qa <-> cto" (or a comma list for
// 3+), the bus's left column.
func peerPair(parts []string) string {
	switch len(parts) {
	case 0:
		return "(unknown)"
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " <-> " + parts[1]
	default:
		return strings.Join(parts, ", ")
	}
}

// renderTimeline renders the derived state-transition timeline for a session.
func renderTimeline(s state.Session) string {
	if len(s.Coordination.Timeline) == 0 {
		return styleFaint.Render("  (no state transitions observed)") + "\n"
	}
	var b strings.Builder
	for _, ev := range s.Coordination.Timeline {
		b.WriteString(fmt.Sprintf("  %-6s  %s  %s\n",
			ageLabel(timeSinceObserved(s, ev)), ev.Summary, styleFaint.Render(shortID(ev.Source))))
	}
	return b.String()
}

// timeSinceObserved returns the age of a timeline event relative to the freshest
// thread observation, so the timeline reads in relative ages without re-reading a
// clock (the snapshot already embedded the ages via Freshness).
func timeSinceObserved(s state.Session, ev state.TimelineEvent) time.Duration {
	for _, t := range s.Coordination.Threads {
		if t.ID == ev.Source {
			return t.Freshness.Age
		}
	}
	return 0
}

// renderLog renders the LOG/tail mode: raw chronological thread rows for the
// session, clearly labeled as the secondary view. v0 lists the threads in
// time order with their last event; it is intentionally plainer than the bus.
func (m Model) renderLog() string {
	s, ok := m.currentSession()
	if !ok {
		return "session " + m.session + " is no longer present.\n(press esc to return)"
	}
	var b strings.Builder
	b.WriteString(styleFaint.Render("logs (raw, chronological — secondary view; press esc to return)") + "\n\n")
	threads := append([]state.ThreadSummary(nil), s.Coordination.Threads...)
	// Chronological: oldest first.
	sortByTime(threads)
	if len(threads) == 0 {
		b.WriteString(styleFaint.Render("  (no messages)") + "\n")
	}
	for _, t := range threads {
		subj := t.Subject
		if subj == "" {
			subj = shortID(t.ID)
		}
		b.WriteString(fmt.Sprintf("  %s  %s  %s  %d msgs\n",
			ageLabel(t.Freshness.Age), peerPair(t.Participants), subj, t.MessageCount))
	}
	return b.String()
}

// --- Overlays (read-only modals) --------------------------------------------

// renderOverlay dispatches to the active read-only modal.
func (m Model) renderOverlay() string {
	switch m.overlay {
	case overlayPeek:
		return m.renderPeek()
	case overlayAttach:
		return m.renderAttach()
	case overlayHelp:
		return renderHelp()
	default:
		return ""
	}
}

// renderPeek renders the READ-ONLY peek overlay for the selected agent or thread:
// recent output, unread inbox, what it is blocked on, and freshness/source. It
// states plainly that replies are unavailable in read-only mode — there is no
// working reply box in v0.
func (m Model) renderPeek() string {
	var b strings.Builder
	b.WriteString(styleHeader.Render("peek (read-only)") + "\n\n")

	sel, ok := m.selectedRow()
	if !ok {
		b.WriteString("nothing selected.\n")
		b.WriteString("\n" + styleFaint.Render("response unavailable in read-only mode"))
		return b.String()
	}
	s, sok := m.currentSession()

	switch {
	case m.route == routeBoard && sel.kind == rowSession:
		b.WriteString(m.peekSession(sel.ID))
	case sel.kind == rowAgent:
		handle := strings.TrimPrefix(sel.ID, "agent:")
		b.WriteString(peekAgent(s, sok, handle, m.now()))
	case sel.kind == rowThread:
		tid := strings.TrimPrefix(sel.ID, "thread:")
		b.WriteString(peekThread(s, sok, tid))
	default:
		b.WriteString("nothing peekable selected.\n")
	}

	b.WriteString("\n" + styleFaint.Render("response unavailable in read-only mode"))
	return b.String()
}

// peekSession summarizes a selected board session (its triage + agents). The
// agent run-states use the session-aware vocabulary and the "agents: N/M alive"
// reconciliation line so the peek agrees with the board.
func (m Model) peekSession(name string) string {
	for _, s := range m.snapshot.Sessions {
		if sessionKey(s) == name {
			var b strings.Builder
			fmt.Fprintf(&b, "session %s  [%s]\n", name, s.Rollup.String())
			fmt.Fprintf(&b, "%s\n\n", agentsAliveLine(s))
			stopped := sessionStopped(s)
			now := m.now()
			b.WriteString("agents:\n")
			for _, a := range detailAgents(s, Filter{}) {
				fmt.Fprintf(&b, "  %s %s (%s): %s\n", livenessGlyph(a.Liveness), a.Handle, a.Engine, agentStateLabel(a, stopped, now))
			}
			return b.String()
		}
	}
	return "session " + name + " not found.\n"
}

// peekAgent renders the read-only agent peek: run-state (session-aware label),
// freshness/source, what it is blocked on (derived from its threads), and unread
// inbox count.
func peekAgent(s state.Session, ok bool, handle string, now func() time.Time) string {
	if !ok {
		return "agent " + handle + " unavailable.\n"
	}
	a := agentByHandle(s, true, handle)
	var b strings.Builder
	fmt.Fprintf(&b, "agent %s (%s)\n", handle, a.Engine)
	fmt.Fprintf(&b, "  run-state: %s\n", agentStateLabel(a, sessionStopped(s), now))
	if a.WakeHealth != "" {
		fmt.Fprintf(&b, "  wake: %s\n", a.WakeHealth)
	}
	fmt.Fprintf(&b, "  freshness: %s (source: %s)\n", freshnessLabel(a.LastSeen, s), agentSource(a))

	// Unread inbox + blocked-on, derived from the agent's threads.
	unread := 0
	var blockedOn []string
	for _, t := range s.Coordination.Threads {
		if !participates(t, handle) {
			continue
		}
		for _, u := range t.UnreadBy {
			if u == handle {
				unread++
			}
		}
		if t.Status == state.ThreadBlocked {
			blockedOn = append(blockedOn, shortID(t.ID))
		}
	}
	fmt.Fprintf(&b, "  unread inbox: %d\n", unread)
	if len(blockedOn) > 0 {
		fmt.Fprintf(&b, "  blocked on: %s\n", strings.Join(blockedOn, ", "))
	} else {
		b.WriteString("  blocked on: nothing\n")
	}
	return b.String()
}

// peekThread renders the read-only thread peek: participants, latest subject/
// kind, recent output (the latest transition), unread recipients, what it is
// blocked on, and freshness/source.
func peekThread(s state.Session, ok bool, tid string) string {
	if !ok {
		return "thread " + tid + " unavailable.\n"
	}
	for _, t := range s.Coordination.Threads {
		if t.ID != tid {
			continue
		}
		var b strings.Builder
		fmt.Fprintf(&b, "thread %s\n", shortID(t.ID))
		fmt.Fprintf(&b, "  participants: %s\n", strings.Join(t.Participants, ", "))
		if t.Subject != "" {
			fmt.Fprintf(&b, "  subject: %s\n", t.Subject)
		}
		fmt.Fprintf(&b, "  status: %s · kind: %s · %d msgs\n", t.Status, kindLabel(t.Kind), t.MessageCount)
		// Recent output: the derived latest transition for this thread.
		if line := latestTransition(s, t.ID); line != "" {
			fmt.Fprintf(&b, "  recent: %s\n", line)
		}
		if len(t.UnreadBy) > 0 {
			fmt.Fprintf(&b, "  unread by: %s\n", strings.Join(t.UnreadBy, ", "))
		} else {
			b.WriteString("  unread by: nobody\n")
		}
		if t.Status == state.ThreadBlocked {
			b.WriteString("  blocked: yes\n")
		}
		fmt.Fprintf(&b, "  freshness: %s (source: %s)\n", ageLabel(t.Freshness.Age), t.Freshness.Source)
		return b.String()
	}
	return "thread " + tid + " not found.\n"
}

// renderAttach renders the INERT attach overlay. Its text STARTS with the plain
// "Read-only mode: not attaching" disclaimer — so the operator can never mistake
// this pane for an action — BEFORE showing the suggested command to copy. v0
// never attaches.
func (m Model) renderAttach() string {
	var b strings.Builder
	b.WriteString(styleHeader.Render("Read-only mode: not attaching") + "\n")
	b.WriteString(styleFaint.Render("nothing was attached — copy the command below to jump yourself") + "\n\n")
	hint := m.attachHint
	if hint == "" {
		hint = m.suggestAttach()
	}
	b.WriteString(hint + "\n")
	return b.String()
}

// renderHelp renders the keymap help overlay.
func renderHelp() string {
	lines := []string{
		styleHeader.Render("amq-squad console — keys"),
		"",
		"  ↑/↓ or j/k   move selection",
		"  enter        expand / drill (board → detail, thread → expand)",
		"  space        peek (read-only output, unread, blocked-on, freshness)",
		"  l            logs / tail (raw chronological messages)",
		"  a            copy attach cmd (shows the suggested command — does NOT attach)",
		"  t            timeline pane (state transitions)",
		"  /            filter (needs-you · gated · at-risk · blocked · unread · agent:<h> · model:<m> · session:<n>)",
		"  esc          back / close overlay / cancel filter",
		"  g            refresh now (force resync)",
		"  ?            this help",
		"  q            quit",
		"",
		styleFaint.Render("this console is READ-ONLY: no key sends a message or starts/stops an agent."),
	}
	return strings.Join(lines, "\n")
}

// --- Small display helpers ---------------------------------------------------

// selectRow highlights a line when it is the selected row.
func selectRow(line string, selected bool) string {
	if selected {
		return styleSelected.Render(line)
	}
	return line
}

// groupHeading labels an attention group on the board.
func groupHeading(g string) string {
	switch g {
	case string(state.TriageNeedsYou):
		return "NEEDS YOU"
	case string(state.TriageBlocked):
		return "BLOCKED"
	case string(state.TriageAtRisk):
		return "AT RISK"
	case "running":
		return "RUNNING"
	case "degraded":
		return "DEGRADED"
	case "stopped":
		return "STOPPED"
	default:
		return strings.ToUpper(g)
	}
}

// participates reports whether handle is a participant of a thread.
func participates(t state.ThreadSummary, handle string) bool {
	for _, p := range t.Participants {
		if p == handle {
			return true
		}
	}
	return false
}

// latestTransition returns the timeline summary for a thread (its current
// derived state), or "".
func latestTransition(s state.Session, tid string) string {
	for _, ev := range s.Coordination.Timeline {
		if ev.Source == tid {
			return ev.Summary
		}
	}
	return ""
}

// agentSource labels where the agent's freshness signal came from.
func agentSource(a state.Agent) string {
	if !a.LastSeen.IsZero() {
		return "presence.json"
	}
	if a.Source != "" {
		return a.Source
	}
	return "observed"
}

// freshnessLabel renders an agent's last-seen age relative to the snapshot's
// clock. The snapshot does not carry the clock, so we report the raw last-seen
// when present and "unknown" otherwise; the thread freshness carries computed
// ages for the trust-aware paths.
func freshnessLabel(lastSeen time.Time, _ state.Session) string {
	if lastSeen.IsZero() {
		return "unknown"
	}
	return lastSeen.UTC().Format("15:04:05") + "Z"
}

// ageLabel renders a duration compactly: "7m", "2h", "3d", "5s".
func ageLabel(d time.Duration) string {
	if d <= 0 {
		return "now"
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// kindLabel renders a message kind, mapping the empty/unknown kind to a word.
func kindLabel(k state.Kind) string {
	if k == state.KindUnknown {
		return "(unknown)"
	}
	return string(k)
}

// shortID trims a namespace prefix for compact display ("p2p/cto__qa" -> "cto__qa").
func shortID(id string) string {
	if i := strings.IndexByte(id, '/'); i >= 0 && i+1 < len(id) {
		return id[i+1:]
	}
	return id
}

// sortByTime sorts threads oldest-first by last event, then id, for the log view.
func sortByTime(threads []state.ThreadSummary) {
	sort.SliceStable(threads, func(i, j int) bool {
		if !threads[i].LastEventAt.Equal(threads[j].LastEventAt) {
			return threads[i].LastEventAt.Before(threads[j].LastEventAt)
		}
		return threads[i].ID < threads[j].ID
	})
}

// rollupHeadline composes the snapshot-wide triage headline with color layered
// on the literal counts. It mirrors state.TriageRollup.String so the console
// and the board agree, but adds per-tier coloring for the live surface.
//
// The triage numbers are THREAD counts, so the headline labels them as such
// ("blocked threads") and separates each concept with " · " — a reader never
// conflates the per-session agent liveness with the thread triage tallies. Each
// noun is pluralized on its own count, exactly like sessions, so a single one
// reads "1 blocked thread" rather than "1 blocked threads".
func rollupHeadline(snap state.Snapshot) string {
	r := snap.Rollup
	sessions := len(snap.Sessions)
	parts := []string{
		fmt.Sprintf("%d %s", sessions, plural(sessions, "session", "sessions")),
		styleNeedsYou.Render(fmt.Sprintf("%d needs-you %s", r.NeedsYou, plural(r.NeedsYou, "thread", "threads"))),
		styleBlocked.Render(fmt.Sprintf("%d blocked %s", r.Blocked, plural(r.Blocked, "thread", "threads"))),
		styleHeader.Render(fmt.Sprintf("%d gated %s", r.Gated, plural(r.Gated, "thread", "threads"))),
		styleAtRisk.Render(fmt.Sprintf("%d at-risk %s", r.AtRisk, plural(r.AtRisk, "thread", "threads"))),
	}
	return "amq-squad mission control · " + strings.Join(parts, " · ")
}

// staticBoardBody renders the plain-text body shared by the TUI viewport and the
// --once static board. It is deliberately color-free so it is byte-stable for CI
// assertions; the interactive header layers color separately. now ages the
// agent/thread signals.
func staticBoardBody(snap state.Snapshot, now func() time.Time) string {
	if len(snap.Sessions) == 0 {
		return "no sessions found under " + snap.BaseRoot + ".\n" +
			"Run 'amq-squad up' to launch your team, or 'amq-squad doctor' to check setup."
	}
	var b strings.Builder
	for i, s := range snap.Sessions {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(sessionBlock(s, now))
	}
	return b.String()
}

// sessionBlock renders one session's heading, the per-session THREAD-triage
// rollup ("[... blocked]"), an "agents: N/M alive" line (a SEPARATE concept from
// the thread counts), the state-aware agent roster, and — for a still-running
// session — the top unresolved coordination threads so "who is blocked on whom"
// is answerable straight from --once.
//
// The "[... blocked]" rollup and the headline both count THREADS; the
// "agents: N/M alive" line counts PROCESSES. Keeping them on distinct labeled
// lines means a reader never conflates "2 blocked (threads)" with "3 stale
// agents".
func sessionBlock(s state.Session, now func() time.Time) string {
	var b strings.Builder
	name := s.Name
	if strings.TrimSpace(name) == "" {
		name = "(root)"
	}
	stopped := sessionStopped(s)
	fmt.Fprintf(&b, "%s  [%s]\n", name, s.Rollup.String())
	// Agent liveness is its own labeled line so it never reads as a thread count.
	fmt.Fprintf(&b, "  %s\n", agentsAliveLine(s))
	for _, a := range s.Agents {
		fmt.Fprintf(&b, "  - %s (%s): %s\n", a.Handle, a.Engine, agentStateLabel(a, stopped, now))
	}
	// Unresolved coordination threads (at-risk / blocked), urgency-sorted. Omitted
	// entirely when there is nothing unresolved to surface.
	if sec := unresolvedSection(s, now); sec != "" {
		b.WriteString(sec)
	}
	return b.String()
}

// plural returns one or many depending on n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Compile-time assurance the Model satisfies the Bubble Tea interface.
var _ tea.Model = Model{}
