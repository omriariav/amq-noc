// Package console: noc_view.go renders the beautified NOC visibility surface.
//
// Layout:
//  1. HEADER "pulse": a brand rule + a single rollup line
//     "<N> squads · <n> running · <n> needs you · <n> at-risk · <n> blocked · <clock>".
//     The needs-you segment is bold/hot when >0, all-dim (calm) when 0.
//  2. MAIN two-pane: LEFT a collapsible attention-first tree (root → project →
//     session → agent); RIGHT a detail pane for the selected node.
//  3. FOOTER: keybindings.
//
// Color is the last layer: every state carries a TEXT label; glyph + color are
// secondary and fall away on NO_COLOR / dumb terminals.
package console

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/state"
)

const (
	sessionNowLimit           = 3
	sessionThreadPreviewLimit = 12
	agentThreadPreviewLimit   = 8
	// detailThreadTitleWidth bounds thread subjects in the detail pane. It is
	// generous on purpose: the two-pane composer re-clamps each row to the real
	// right-pane width, so a wide terminal shows more of the thread evidence while
	// a narrow one stays bounded. The left tree no longer repeats subjects, so the
	// detail pane is where the thread text belongs.
	detailThreadTitleWidth = 72
)

// View implements tea.Model. Pointer receiver to match Update / Init: *NOCModel
// is the type the program is driven as (tea.NewProgram(&m)).
//
// The LIVE program renders liveView() — the INTERACTIVE, cursor-aware frame
// (header pulse + a tree whose row at m.cursor carries the selection bar +
// a detail pane that reads m.showTimeline) — so every nav / collapse / drill /
// timeline / refresh / filter key produces a VISIBLE change on the next frame.
// staticView() (the cursor-LESS rollup digest) is NOT used here; it is the
// --once / non-TTY render only (runNOCOnce). Rendering the digest in the live
// path is exactly the bug that made arrows / j / k / enter / left / t / g / esc
// look dead: those keys mutate m.cursor / m.tree / m.showTimeline, which the
// digest reads none of.
func (m *NOCModel) View() string {
	if !m.ready {
		return "loading…"
	}
	if m.showHelp {
		return m.helpView()
	}
	if m.readResult != nil {
		return m.overlayFrame(m.readResultOverlayView())
	}
	if m.drainResult != nil {
		return m.overlayFrame(m.drainResultOverlayView())
	}
	if m.inboxResult != nil {
		return m.overlayFrame(m.inboxResultOverlayView())
	}
	if m.dlqResult != nil {
		return m.overlayFrame(m.dlqResultOverlayView())
	}
	if m.dlqReadResult != nil {
		return m.overlayFrame(m.dlqReadResultOverlayView())
	}
	if m.dlqRetryResult != nil {
		return m.overlayFrame(m.dlqRetryResultOverlayView())
	}
	if m.dlqPurgeResult != nil {
		return m.overlayFrame(m.dlqPurgeResultOverlayView())
	}
	if m.dlqRetryAllResult != nil {
		return m.overlayFrame(m.dlqRetryAllResultOverlayView())
	}
	if m.receiptsResult != nil {
		return m.overlayFrame(m.receiptsResultOverlayView())
	}
	if m.receiptsWaitResult != nil {
		return m.overlayFrame(m.receiptsWaitResultOverlayView())
	}
	if m.messageWaitResult != nil {
		return m.overlayFrame(m.messageWaitResultOverlayView())
	}
	if m.amqCleanupResult != nil {
		return m.overlayFrame(m.amqCleanupResultOverlayView())
	}
	if m.threadContextResult != nil {
		return m.overlayFrame(m.threadContextResultOverlayView())
	}
	if m.amqOpsResult != nil {
		return m.overlayFrame(m.amqOpsResultOverlayView())
	}
	if m.amqWhoResult != nil {
		return m.overlayFrame(m.amqWhoResultOverlayView())
	}
	if m.amqEnvResult != nil {
		return m.overlayFrame(m.amqEnvResultOverlayView())
	}
	if m.presenceResult != nil {
		return m.overlayFrame(m.presenceResultOverlayView())
	}
	if m.projectDoctorResult != nil {
		return m.overlayFrame(m.projectDoctorResultOverlayView())
	}
	if m.projectHistoryResult != nil {
		return m.overlayFrame(m.projectHistoryResultOverlayView())
	}
	if m.teamRulesResult != nil {
		return m.overlayFrame(m.teamRulesResultOverlayView())
	}
	if m.projectResumePlanResult != nil {
		return m.overlayFrame(m.projectResumePlanResultOverlayView())
	}
	if m.forkPlanResult != nil {
		return m.overlayFrame(m.forkPlanResultOverlayView())
	}
	if m.briefResult != nil {
		return m.overlayFrame(m.briefResultOverlayView())
	}
	if m.statusResult != nil {
		return m.overlayFrame(m.statusResultOverlayView())
	}
	if m.threadsResult != nil {
		return m.overlayFrame(m.threadsResultOverlayView())
	}
	if m.roleMarket != nil {
		return m.overlayFrame(m.roleMarketOverlayView())
	}
	if m.teamProfiles != nil {
		return m.overlayFrame(m.teamProfilesOverlayView())
	}
	// Control overlays render OVER the live frame so the operator's confirm /
	// type step is unmissable: the EXACT command (confirm) or the body editor
	// (input) replaces the body while the header/footer keep their bearings.
	if m.pending != nil {
		return m.overlayFrame(m.confirmOverlayView())
	}
	// The READ-ONLY focus confirm overlay (jump / J / o) renders OVER the live
	// frame like the mutating confirm so the operator's y/esc step is unmissable.
	if m.jumpPending != nil {
		return m.overlayFrame(m.focusConfirmOverlayView())
	}
	if m.input != nil {
		return m.overlayFrame(m.inputOverlayView())
	}
	// The command palette (PR18) renders OVER the live frame like the control
	// overlays so the fuzzy-jump list is unmissable while the chrome stays put.
	if m.palette != nil {
		return m.overlayFrame(m.paletteOverlayView())
	}
	return m.liveView()
}

// overlayFrame wraps a control overlay in the standard header + footer so the
// confirm/input step stays anchored in the NOC chrome (and the footer's
// control-key legend + actNote stay visible).
func (m NOCModel) overlayFrame(body string) string {
	var b strings.Builder
	b.WriteString(m.headerView())
	b.WriteString("\n")
	b.WriteString(body)
	b.WriteString("\n")
	b.WriteString(m.footerView())
	return b.String()
}

// liveView is the INTERACTIVE frame for the live TUI: the header pulse, then a
// cursor-aware two-pane main area (LEFT a collapsible attention-first tree with
// the selection bar on m.cursor, RIGHT the detail pane for the selected node),
// then the footer (keys / filter editor / hide-stale + notes). It is laid out
// within m.width/m.height (set via WindowSizeMsg under AltScreen).
//
// Unlike staticView()'s rollup digest, EVERY interactive key lands here:
//   - down/up/j/k move the selection bar (treeView marks i == m.cursor),
//   - left collapses (fewer rows) / right / enter expands (more rows) or drills,
//     all via the same m.tree expand-state the tree honors,
//   - f toggles the inter-agent flow graph in the detail pane,
//   - g refreshes (a fresh snapshot re-renders), esc clears the filter / collapses.
func (m NOCModel) liveView() string {
	var b strings.Builder
	b.WriteString(m.headerView())
	b.WriteString("\n")
	if m.guidance != "" {
		b.WriteString(m.guidance)
		b.WriteString("\n")
		b.WriteString(m.footerView())
		return b.String()
	}
	// mainView is the cursor-aware tree + detail pane (the machinery that already
	// renders the selection bar and reads m.showTimeline); it lays the two panes
	// out side by side within m.width, stacking when narrow.
	b.WriteString(m.mainView())
	b.WriteString("\n")
	b.WriteString(m.footerView())
	return b.String()
}

// staticView is the static board for the --once / non-TTY path ONLY (runNOCOnce);
// the LIVE View renders liveView() (the interactive, cursor-aware frame). Default
// --once leads with a NEEDS-ATTENTION section + PROJECT ROLLUPS (the digest, not
// the firehose); --tree/--all (fullTree) renders the full expandable tree so the
// existing full board is still one flag away. The digest is cursor-LESS by design
// (it never reads m.cursor / m.tree / m.showTimeline), which is why it must not be
// the live render.
func (m NOCModel) staticView() string {
	var b strings.Builder
	b.WriteString(m.headerView())
	b.WriteString("\n")
	if m.guidance != "" {
		b.WriteString(m.guidance)
		b.WriteString("\n")
		b.WriteString(m.footerView())
		return b.String()
	}
	if m.fullTree {
		b.WriteString(m.mainView())
	} else {
		b.WriteString(m.rollupView())
	}
	b.WriteString("\n")
	b.WriteString(m.footerView())
	return b.String()
}

// nocNeedsYouItem is one needs-you thread plus the project/session it lives in,
// the unit the NEEDS YOU block lists. Carries the typed reason so the block can
// sort + label it (approve above goal-reached above generic).
type nocNeedsYouItem struct {
	project string
	session string
	thread  state.ThreadSummary
}

// collectNeedsYou gathers every needs-you thread across the in-view squads,
// sorted for the NEEDS YOU block: APPROVE first, then GOAL-REACHED, then
// generic; within a reason oldest-first (longest-waiting human ask leads), then
// by project/session/id for determinism. Returns nil when nothing needs the
// human — the caller renders the block ONLY when this is non-empty (never
// fabricate a NEEDS YOU section on a calm board).
func collectNeedsYou(projects []noc.ProjectSnapshot) []nocNeedsYouItem {
	var items []nocNeedsYouItem
	for _, ps := range projects {
		if ps.Warning != "" {
			continue
		}
		for _, sess := range ps.Snap.Sessions {
			for _, th := range sess.Coordination.NeedsYouThreads() {
				if th.Historical {
					continue
				}
				items = append(items, nocNeedsYouItem{
					project: ps.Project,
					session: sessionLabel(sess),
					thread:  th,
				})
			}
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		ri, rj := items[i].thread.AttnReason.Rank(), items[j].thread.AttnReason.Rank()
		if ri != rj {
			return ri < rj
		}
		if !items[i].thread.LastEventAt.Equal(items[j].thread.LastEventAt) {
			return items[i].thread.LastEventAt.Before(items[j].thread.LastEventAt)
		}
		if items[i].project != items[j].project {
			return items[i].project < items[j].project
		}
		if items[i].session != items[j].session {
			return items[i].session < items[j].session
		}
		return items[i].thread.ID < items[j].thread.ID
	})
	return items
}

// needsYouSection renders the "NEEDS YOU" block: needs-you items text-first,
// glyph-second, APPROVE sorted ABOVE GOAL-REACHED above generic. APPROVE uses
// the hot/act-now accent; GOAL-REACHED a distinct cyan REVIEW accent — both with
// TEXT labels that survive NO_COLOR. GOAL-REACHED is never a bare green check: it
// stays inside NEEDS YOU below APPROVE so it never reads as "healthy / no
// action". Returns "" when nothing needs the human (the block is then omitted).
func (m NOCModel) needsYouSection(projects []noc.ProjectSnapshot) string {
	items := collectNeedsYou(projects)
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "NEEDS YOU"))
	b.WriteString(m.th.paint(m.th.dim, fmt.Sprintf(" (%d)", len(items))))
	b.WriteString("\n")
	for _, it := range items {
		b.WriteString("  " + m.needsYouRow(it) + "\n")
	}
	return b.String()
}

// needsYouRow renders one NEEDS YOU line. The SUBJECT is the owner that needs
// the operator (the agent waiting on the human, or the team/session when the
// evidence is unowned); the thread is the EVIDENCE that explains why:
//
//	⏸ APPROVE       <project>/<session> · <owner>  <who> paused · why: <subject> · <age>
//	✓ GOAL-REACHED  <project>/<session>            team done · review and close · <age>
//
// The reason label + glyph lead (text always present); the squad path + owner
// follow in the brand accent, then a dim evidence trail (phrase, the thread
// subject prefixed "why:", and the age). APPROVE is hot; GOAL-REACHED cyan.
func (m NOCModel) needsYouRow(it nocNeedsYouItem) string {
	glyph, label, style := m.attnReasonChrome(it.thread.AttnReason)
	var b strings.Builder
	b.WriteString(m.th.paint(style, glyph+" "+padRight(label, 13)))

	dot := " " + m.dot() + " "
	loc := it.project + "/" + it.session
	if owner := strings.TrimSpace(it.thread.NeedsYouOwner); owner != "" {
		loc += dot + owner
	}
	b.WriteString(" " + m.th.paint(m.th.brand, loc))

	parts := []string{attnReasonPhrase(it.thread)}
	if subj := strings.TrimSpace(threadTitle(it.thread)); subj != "" {
		parts = append(parts, "why: "+truncate(subj, 40))
	}
	if age := nocThreadAge(it.thread); age != "" {
		parts = append(parts, age)
	}
	b.WriteString(" " + m.th.paint(m.th.dim, strings.Join(parts, dot)))
	return b.String()
}

// padRight pads s with spaces to at least width w (visible runes), so the reason
// labels in the NEEDS YOU block left-align into a column.
func padRight(s string, w int) string {
	if n := len([]rune(s)); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// attnReasonChrome maps a needs-you reason to its (glyph, TEXT label, style).
// APPROVE is the hot act-now accent; GOAL-REACHED the distinct cyan review
// accent; generic falls back to the needs-you accent. The text label is always
// returned so a NO_COLOR terminal still distinguishes the reasons.
func (m NOCModel) attnReasonChrome(r state.AttnReason) (glyph, label string, style lipgloss.Style) {
	switch r {
	case state.AttnApprove:
		return nocGlyphApprove.glyph(m.colorMode), "APPROVE", m.th.needsYou
	case state.AttnGoalReached:
		return nocGlyphGoal.glyph(m.colorMode), "GOAL-REACHED", m.th.review
	default:
		return nocGlyphNeedsYou.glyph(m.colorMode), "NEEDS-YOU", m.th.needsYou
	}
}

// attnReasonInline renders the compact inline reason chip shown on a needs-you
// session tree row: glyph + TEXT label in the reason's accent (hot for approve,
// cyan review for goal-reached, hot for a plain ask). Returns "" for AttnNone.
func (m NOCModel) attnReasonInline(r state.AttnReason) string {
	if r == state.AttnNone {
		return ""
	}
	glyph, label, style := m.attnReasonChrome(r)
	return m.th.paint(style, glyph+" "+label)
}

// attnReasonPhrase is the short human phrase that follows the reason label:
// "<who> paused" for approve, "team done · review and close" for goal-reached,
// "<who> asks" for a plain question.
func attnReasonPhrase(th state.ThreadSummary) string {
	who := strings.TrimSpace(th.NeedsYouOwner)
	if who == "" {
		who = "the team"
	}
	switch th.AttnReason {
	case state.AttnApprove:
		return who + " paused"
	case state.AttnGoalReached:
		return "team done · review and close"
	default:
		return who + " asks"
	}
}

// rollupView is the --once digest: a NEEDS YOU block (operator action required)
// and a NEEDS ATTENTION section (running squads that carry live at-risk/blocked,
// or needs-you) on top, then a compact PROJECT ROLLUPS list (one line per squad,
// attention-first). Stale-only squads render dim with their stale counts
// parenthesized, never as live attention.
func (m NOCModel) rollupView() string {
	var b strings.Builder

	projects := m.visibleProjects()

	// --- NEEDS YOU: operator action required, reason-first. Rendered only when
	// something actually needs the human (never fabricated). ---
	if ny := m.needsYouSection(projects); ny != "" {
		b.WriteString(ny)
		b.WriteString("\n")
	}

	// --- NEEDS ATTENTION: live squads with something outstanding now. ---
	var attn []noc.ProjectSnapshot
	for _, ps := range projects {
		if ps.Warning != "" {
			continue
		}
		r := ps.Snap.Rollup
		if r.NeedsYou > 0 || (hasRunningAgentSnap(ps.Snap) && (r.AtRisk > 0 || r.Blocked > 0 || r.Gated > 0)) {
			attn = append(attn, ps)
		}
	}
	b.WriteString(m.th.paint(m.th.brand, "NEEDS ATTENTION"))
	b.WriteString("\n")
	if len(attn) == 0 {
		b.WriteString(m.th.paint(m.th.dim, "  (nothing live needs you right now)") + "\n")
	}
	for _, ps := range attn {
		b.WriteString("  " + m.projectRollupLine(ps, true) + "\n")
	}

	// --- PROJECT ROLLUPS: every (visible) squad, one calm line each. ---
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.brand, nocCount(len(projects), "PROJECT", "PROJECTS")))
	b.WriteString(m.th.paint(m.th.dim, fmt.Sprintf(" (%d)", len(projects))))
	b.WriteString("\n")
	if len(projects) == 0 {
		b.WriteString(m.th.paint(m.th.dim, "  (no matching squads)") + "\n")
	}
	for _, ps := range projects {
		b.WriteString("  " + m.projectRollupLine(ps, false) + "\n")
	}
	return b.String()
}

// projectRollupLine renders one squad as a single rollup row: state glyph,
// project label, a liveness phrase ("running N/M agents alive" / "stopped"), and
// the triage tally (live leading, stale dim/parenthesized). When attn is true the
// live counts are emphasized (it heads the NEEDS ATTENTION section).
func (m NOCModel) projectRollupLine(ps noc.ProjectSnapshot, attn bool) string {
	var b strings.Builder
	st := visibleState(projectRollupState(ps))
	b.WriteString(m.th.paint(m.th.nocStateStyle(st), nocStateGlyph(st, m.colorMode)+" "))

	nameStyle := m.th.brand
	if st == nocNeedsYou {
		nameStyle = m.th.needsYou
	} else if projectIsStaleOnly(ps) {
		nameStyle = m.th.dim
	}
	b.WriteString(m.th.paint(nameStyle, ps.Project))

	if ps.Warning != "" {
		b.WriteString(" " + m.th.paint(m.th.atRisk, "warning: "+firstLine(ps.Warning)))
		return b.String()
	}
	if ps.Candidate {
		b.WriteString(" " + m.th.paint(m.th.dim, "candidate team-home"))
		return b.String()
	}
	if ps.TeamConfigured && !ps.DefaultTeam {
		b.WriteString(" " + m.th.paint(m.th.dim, namedProfileSummary(ps)))
		return b.String()
	}
	if ps.DefaultTeam && len(ps.Snap.Sessions) == 0 {
		b.WriteString(" " + m.th.paint(m.th.dim, "configured, no sessions"))
		return b.String()
	}

	if st == nocNeedsYou {
		b.WriteString(" " + m.needsYouNarrative(nocNode{kind: nodeProject, project: ps}))
		return b.String()
	}

	b.WriteString(" " + m.th.paint(m.th.dim, projectLivenessPhrase(ps)))

	if tally := m.childTallyText(childTallySessions(ps.Snap.Sessions)); tally != "" {
		b.WriteString(" " + tally)
	}
	return b.String()
}

func namedProfileSummary(ps noc.ProjectSnapshot) string {
	named := namedTeamProfiles(ps)
	switch len(named) {
	case 0:
		return "profile marker only"
	case 1:
		return "named profile " + named[0]
	default:
		return fmt.Sprintf("%d named profiles", len(named))
	}
}

func namedProfileDetail(ps noc.ProjectSnapshot) string {
	named := namedTeamProfiles(ps)
	switch len(named) {
	case 0:
		return "profile marker found"
	case 1:
		return "named profile: " + named[0]
	default:
		return "named profiles: " + strings.Join(named, ", ")
	}
}

func singleNamedProfile(ps noc.ProjectSnapshot) string {
	named := namedTeamProfiles(ps)
	if len(named) == 1 {
		return named[0]
	}
	return ""
}

// visibleProjects returns the projects to render in the digest, honoring the
// SAME scope the headline counts: the hideStale toggle (drop stopped/stale
// squads) AND the active filter.
func (m NOCModel) visibleProjects() []noc.ProjectSnapshot {
	return m.scopedProjects()
}

// scopedProjects is the single source of truth for "which squads are in view":
// it applies the hide-stale toggle and the typed filter, in the order the tree
// uses them, so the pulse line, the --once digest, and the interactive tree all
// agree on the visible set.
func (m NOCModel) scopedProjects() []noc.ProjectSnapshot {
	out := make([]noc.ProjectSnapshot, 0, len(m.ms.Projects))
	for _, ps := range m.ms.Projects {
		if m.hideStale && projectIsStaleOnly(ps) {
			continue
		}
		if !ProjectMatchesNOCFilter(ps, m.filter) {
			continue
		}
		out = append(out, ps)
	}
	return out
}

// headerView renders the brand rule + the rollup pulse line + a last-activity
// summary line.
func (m NOCModel) headerView() string {
	var b strings.Builder

	brand := m.th.paint(m.th.brand, "amq-noc NOC")
	sub := m.th.paint(m.th.dim, "command center")
	b.WriteString(brand + "  " + sub + "\n")
	b.WriteString(m.th.paint(m.th.rule, m.rule()) + "\n")
	b.WriteString(m.pulseLine())
	if la := m.lastActivityLine(); la != "" {
		b.WriteString("\n" + la)
	}
	// Needs-you alert banner (PR18): shown when a session just transitioned into
	// needs-you, painted hot so it is unmissable. Cleared on the next keypress.
	if banner := m.alertBannerView(); banner != "" {
		b.WriteString("\n" + banner)
	}
	return b.String()
}

// pulseLine is the operator-scan headline. It keeps the primary model compact:
// squads, running, needs-you, blocked, stale. Granular at-risk/gated/stale
// buckets remain in row tallies, detail panes, and JSON.
func (m NOCModel) pulseLine() string {
	projects := m.scopedProjects()
	tally := childTallyProjects(projects)
	squads := len(projects)

	dim := func(s string) string { return m.th.paint(m.th.dim, s) }
	sep := dim(" " + m.dot() + " ")

	segs := []string{
		dim(nocCount(squads, "squad", "squads")),
		dim(strconv.Itoa(tally.Running) + " running"),
	}

	// needs-you: the single eye-grab for current operator action. Count visible
	// squad rows whose primary state is needs-you, not raw thread buckets.
	nyText := strconv.Itoa(tally.NeedsYou) + " needs-you"
	if tally.NeedsYou > 0 {
		segs = append(segs, m.th.paint(m.th.needsYou, nocStateGlyph(nocNeedsYou, m.colorMode)+" "+nyText))
	} else {
		segs = append(segs, dim(nyText))
	}

	// waiting/stale follow the visible project rows as well, so the pulse answers
	// "which squads are in each state?" and reconciles with the left tree.
	if tally.Waiting > 0 {
		segs = append(segs, m.th.paint(m.th.atRisk, strconv.Itoa(tally.Waiting)+" waiting"))
	} else {
		segs = append(segs, dim("0 waiting"))
	}

	if tally.Stale > 0 {
		segs = append(segs, dim(strconv.Itoa(tally.Stale)+" stale"))
	}

	segs = append(segs, dim(m.clock()))
	return strings.Join(segs, sep)
}

// lastActivityLine is the top-level "last activity across all squads" summary,
// always dim. Empty when no project recorded any activity.
func (m NOCModel) lastActivityLine() string {
	if m.ms.LastActivity.IsZero() {
		return ""
	}
	age := ""
	if !m.ms.ObservedAt.IsZero() {
		if d := m.ms.ObservedAt.Sub(m.ms.LastActivity); d > 0 {
			age = " (" + ageLabel(d) + " ago)"
		}
	}
	return m.th.paint(m.th.dim, "last activity across all squads: "+m.ms.LastActivity.Format("15:04:05")+age)
}

// clock formats the observation time.
func (m NOCModel) clock() string {
	if m.ms.ObservedAt.IsZero() {
		return ""
	}
	return m.ms.ObservedAt.Format("15:04:05")
}

// rule returns the header rule string sized to the width.
func (m NOCModel) rule() string {
	w := m.width
	if w <= 0 {
		w = 78
	}
	ch := "─"
	if m.colorMode == ColorAscii {
		ch = "-"
	}
	return strings.Repeat(ch, w)
}

// mainView lays out the LEFT tree and the RIGHT detail pane side by side. When
// the terminal is narrow (or width unknown, e.g. --once) it stacks the tree
// above the detail.
//
// Every composed row is bounded to m.width: the right detail pane is truncated
// to the columns left of the gutter, and the whole row is clamped as a backstop.
// Without this clamp a row ran ~219 cols wide in a 200-col live pane (leftW pad
// + gutter + an un-truncated detail line), so each tree row WRAPPED and the
// interactive tree rendered as one corrupted line under AltScreen — the moving
// selection bar was there but hidden in the wrap. The visible row count is also
// capped to the body height (m.height minus the header/footer chrome) so the
// frame never overruns the AltScreen viewport.
func (m NOCModel) mainView() string {
	left := m.treeView()
	right := m.detailView()

	leftW := m.leftWidth()
	if leftW <= 0 || m.width <= 0 {
		// Stacked fallback (CI / --once / narrow): tree, then detail.
		var b strings.Builder
		b.WriteString(left)
		if strings.TrimSpace(right) != "" {
			b.WriteString("\n")
			b.WriteString(m.th.paint(m.th.dim, m.thinRule()))
			b.WriteString("\n")
			b.WriteString(right)
		}
		return b.String()
	}

	leftLines := strings.Split(left, "\n")
	rightLines := strings.Split(right, "\n")
	n := len(leftLines)
	if len(rightLines) > n {
		n = len(rightLines)
	}
	if bh := m.bodyHeight(); bh > 0 && n > bh {
		n = bh
	}
	gutter := m.th.paint(m.th.dim, " │ ")
	gutterW := 3 // " │ " / " | " are both 3 visible columns
	if m.colorMode == ColorAscii {
		gutter = " | "
	}
	// Columns available to the right (detail) pane after the left column + gutter.
	rightW := m.width - leftW - gutterW
	var b strings.Builder
	for i := 0; i < n; i++ {
		l := ""
		if i < len(leftLines) {
			l = leftLines[i]
		}
		rr := ""
		if i < len(rightLines) {
			rr = rightLines[i]
		}
		// Clamp the LEFT tree row to the left column FIRST, ending in an ellipsis
		// when it overruns: padVisible only pads short rows, so without this a long
		// row (e.g. a project with a long brief) pushed past leftW into the gutter,
		// shoving the │ divider and the whole right pane out of alignment. Clamping
		// here pins the divider at exactly leftW on every row; the trailing free
		// text is what gets ellipsized, the leading glyphs/labels survive.
		l = truncateVisibleEllipsis(l, leftW, m.colorMode == ColorAscii)
		// Truncate the detail line to its column budget so the composed row never
		// exceeds m.width and wraps (the wrap is what collapsed the live tree).
		rr = truncateVisible(rr, rightW)
		row := padVisible(l, leftW) + gutter + rr
		// Backstop: clamp the whole row to m.width in case the left column itself
		// overflows its budget.
		row = truncateVisible(row, m.width)
		b.WriteString(row)
		if i < n-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// bodyHeight is the number of rows the two-pane main area may use: the AltScreen
// viewport (m.height) minus the header (4 lines: brand, rule, pulse, last-
// activity) and the footer (up to 3: rule, optional notes, keys). Returns 0 when
// the height is unknown (--once / CI / tests), which leaves the layout uncapped.
func (m NOCModel) bodyHeight() int {
	if m.height <= 0 {
		return 0
	}
	const chrome = 8 // header (~4) + blank + footer (~3)
	bh := m.height - chrome
	if bh < 1 {
		bh = 1
	}
	return bh
}

// leftWidth is the tree pane width (about 55% of the terminal, bounded).
func (m NOCModel) leftWidth() int {
	if m.width <= 0 {
		return 0
	}
	w := m.width*55/100 - 2
	if w < 24 {
		w = 24
	}
	if w > m.width-20 {
		w = m.width - 20
	}
	if w < 0 {
		return 0
	}
	return w
}

func (m NOCModel) thinRule() string {
	w := m.width
	if w <= 0 {
		w = 60
	}
	ch := "─"
	if m.colorMode == ColorAscii {
		ch = "-"
	}
	return strings.Repeat(ch, w)
}

// treeView renders the flattened, attention-first tree with the amber selection
// bar on the cursor row.
func (m NOCModel) treeView() string {
	ns := m.nodes()
	if len(ns) == 0 {
		return m.th.paint(m.th.dim, "(no matching nodes)")
	}
	start, end := m.treeWindow(len(ns))
	var b strings.Builder
	for i := start; i < end; i++ {
		n := ns[i]
		line := m.renderNode(n, i == m.cursor)
		b.WriteString(line)
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (m NOCModel) treeWindow(total int) (int, int) {
	rows := m.bodyHeight()
	if total <= 0 {
		return 0, 0
	}
	if rows <= 0 || rows >= total {
		return 0, total
	}
	start := m.scroll
	maxScroll := total - rows
	if start < 0 {
		start = 0
	}
	if start > maxScroll {
		start = maxScroll
	}
	if m.cursor < start {
		start = m.cursor
	}
	if m.cursor >= start+rows {
		start = m.cursor - rows + 1
	}
	if start < 0 {
		start = 0
	}
	if start > maxScroll {
		start = maxScroll
	}
	return start, start + rows
}

// renderNode renders one tree row: selection marker, indent + tree glyph, state
// glyph + TEXT label, label, triage tally (parents) / jump affordance (running
// agents), recent action (dim), and age (dim).
func (m NOCModel) renderNode(n nocNode, selected bool) string {
	var b strings.Builder

	// Selection marker.
	if selected {
		b.WriteString(m.th.paint(m.th.selBar, nocGlyphSelect.glyph(m.colorMode)+" "))
	} else {
		b.WriteString("  ")
	}

	// Indent by depth.
	b.WriteString(strings.Repeat("  ", n.depth))

	// Expand/collapse caret for parents.
	if n.hasKids {
		caret := nocGlyphCollapsed
		if n.expanded {
			caret = nocGlyphExpanded
		}
		b.WriteString(m.th.paint(m.th.dim, caret.glyph(m.colorMode)) + " ")
	} else {
		b.WriteString("  ")
	}

	// State glyph + TEXT label (text always present). The row shows the SIMPLIFIED
	// visible status (running / stale / waiting / needs-you): blocked/gated/at-risk
	// collapse to "waiting" via visibleState. Granular triage stays in the detail
	// pane. Sorting still uses the granular n.state, so waiting subtiers order
	// sensibly within the group.
	vs := visibleState(n.state)
	glyph := nocStateGlyph(vs, m.colorMode)
	label := nocStateText(vs)
	style := m.th.nocStateStyle(vs)
	b.WriteString(m.th.paint(style, glyph+" "+label))
	b.WriteString(" ")

	// Node label (project / session / handle / root).
	nameStyle := m.th.brand
	if n.state == nocNeedsYou {
		nameStyle = m.th.needsYou
	} else if n.kind == nodeAgent {
		nameStyle = m.th.running
		if n.state != nocRunning {
			nameStyle = m.th.dim
		}
	}
	b.WriteString(m.th.paint(nameStyle, n.label))
	// Compact, deterministic surface marker on a project row: amq-squad-managed
	// (configured team metadata) vs a plain AMQ session store. Quiet (dim) but
	// visible, so the operator knows which CLI drives the team.
	if n.kind == nodeProject {
		if tag := squadKindTag(n.project); tag != "" {
			b.WriteString(m.th.paint(m.th.dim, " "+tag))
		}
	}

	// Parent rows use team-level status, not thread evidence counts. Projects
	// count child sessions/teams; sessions count child agents; roots count child
	// projects. Granular thread rollups stay in the right detail pane.
	if n.kind == nodeProject || n.kind == nodeSession || n.kind == nodeRoot {
		if n.state == nocNeedsYou {
			b.WriteString(" " + m.needsYouNarrative(n))
			// Show the informative reason (APPROVE / GOAL-REACHED) for a session;
			// a plain ask is already conveyed by "needs you", so its chip is omitted
			// to avoid a redundant "needs you NEEDS-YOU".
			if n.kind == nodeSession {
				switch n.session.Attention.Reason {
				case state.AttnApprove, state.AttnGoalReached:
					if rl := m.attnReasonInline(n.session.Attention.Reason); rl != "" {
						b.WriteString(" " + rl)
					}
				}
			}
		} else if tally := m.childTallyText(n.child); tally != "" {
			b.WriteString(" " + tally)
		}
	}

	// Recent action / title (dim).
	if n.recent != "" {
		b.WriteString(m.th.paint(m.th.dim, "  "+truncate(n.recent, 40)))
	}

	return b.String()
}

// needsYouNarrative renders the team-level "who needs you" phrase for a needs-you
// parent row: "<owner> needs you" (or "<a>, <b> need you", or "<a> +N need you"
// for many), or "team needs you" when the needs-you evidence is unowned (a
// dead/missing asker or an operator-only thread). Painted in the needs-you accent.
func (m NOCModel) needsYouNarrative(n nocNode) string {
	owners := needsYouOwners(n)
	var phrase string
	switch len(owners) {
	case 0:
		phrase = "team needs you"
	case 1:
		phrase = owners[0] + " needs you"
	case 2:
		phrase = owners[0] + ", " + owners[1] + " need you"
	default:
		phrase = owners[0] + " +" + strconv.Itoa(len(owners)-1) + " need you"
	}
	return m.th.paint(m.th.needsYou, phrase)
}

// needsYouOwners returns the handles that own a node's needs-you evidence (session
// or project scope), so a parent row can name who the team needs. The thread's
// NeedsYouOwner is the source of truth: a structural operator gate stays owner-led
// ("<owner> needs you") even when the owner agent is NON-OPERATIONAL (stopped /
// dead-mailbox-live), which is exactly the live manual-RC case. Operational agents
// carrying attached needs-you attention are also included (some needs-you nodes are
// built straight from agent attention, with no thread carried on the node). Empty
// means the evidence is genuinely unowned (operator-only or a dead/missing asker),
// surfaced by the caller as the team itself. Liveness is unaffected: this changes
// only the narrative attribution, never an agent row's displayed run-state.
func needsYouOwners(n nocNode) []string {
	seen := map[string]bool{}
	var out []string
	addOwner := func(handle string) {
		handle = strings.TrimSpace(handle)
		if handle == "" || seen[handle] {
			return
		}
		seen[handle] = true
		out = append(out, handle)
	}
	add := func(sess state.Session) {
		for _, ag := range sess.Agents {
			if ag.Attention.State == state.TriageNeedsYou {
				addOwner(ag.Handle)
			}
		}
		// A CURRENT needs-you thread (the same set that drives rollup.NeedsYou: not
		// Historical) names its owner regardless of that owner's liveness.
		for _, th := range sess.Coordination.Threads {
			if th.Triage == state.TriageNeedsYou && !th.Historical {
				addOwner(th.NeedsYouOwner)
			}
		}
	}
	switch n.kind {
	case nodeSession:
		add(n.session)
	case nodeProject:
		for _, sess := range n.project.Snap.Sessions {
			add(sess)
		}
	}
	return out
}

// squadKindTag returns a compact, deterministic surface marker for a project:
// "squad" when it is amq-squad-managed (it carries configured team metadata),
// "amq" for a plain AMQ session store with no squad config, and "" otherwise
// (e.g. an unconfigured candidate team-home). Detection is structural - it reads
// the noc.ProjectSnapshot discovery fields, never prose.
func squadKindTag(ps noc.ProjectSnapshot) string {
	switch {
	case ps.TeamConfigured:
		return "squad"
	case ps.SessionStore:
		return "amq"
	default:
		return ""
	}
}

// kickRecoverLines returns deterministic, copy-pasteable commands to kick off or
// recover work, chosen structurally by whether the project is amq-squad-managed
// (delegate to the amq-squad CLI) or a plain AMQ surface (use the amq CLI scoped
// to the resolved root). sessionName/amqRoot narrow to a session when known.
// Display-only: nothing here is executed.
func kickRecoverLines(ps noc.ProjectSnapshot, sessionName, amqRoot string) []string {
	switch squadKindTag(ps) {
	case "squad":
		dir := shellToken(strings.TrimSpace(ps.Dir))
		if strings.TrimSpace(sessionName) != "" {
			s := shellToken(sessionName)
			return []string{
				"amq-squad status --project " + dir + " --session " + s,
				"amq-squad resume --project " + dir + " --session " + s,
			}
		}
		return []string{
			"amq-squad status --project " + dir,
			"amq-squad resume --project " + dir,
			"amq-squad up --project " + dir,
		}
	case "amq":
		root := strings.TrimSpace(amqRoot)
		if root == "" {
			return nil
		}
		rt := shellToken(root)
		// Plain AMQ: the resolved root is known; AGENT / MESSAGE_ID / THREAD_ID are
		// placeholders the operator fills in. Covers inspect (who/list/drain),
		// read/thread, and send.
		return []string{
			"amq who --root " + rt,
			"amq list --root " + rt + " --me AGENT",
			"amq drain --root " + rt + " --me AGENT --include-body",
			"amq read --root " + rt + " --me AGENT --id MESSAGE_ID",
			"amq thread --root " + rt + " --id THREAD_ID --include-body",
			"amq send --root " + rt + " --me AGENT --to AGENT --thread THREAD_ID",
		}
	}
	return nil
}

// commandsSection renders the right-pane "commands (kick off / recover)" helper
// for a project (sessionName == "") or a session, or "" when there is nothing
// deterministic to show. Quiet (dim), inline - never a shortcut or palette.
func (m NOCModel) commandsSection(ps noc.ProjectSnapshot, sessionName, amqRoot string) string {
	lines := kickRecoverLines(ps, sessionName, amqRoot)
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.detailRule() + "\n")
	b.WriteString(m.th.paint(m.th.dim, "commands (kick off / recover)") + "\n")
	for _, l := range lines {
		b.WriteString(m.th.paint(m.th.dim, "  "+l) + "\n")
	}
	return b.String()
}

// childTallyText is a compact per-parent tally over visible immediate children.
// It deliberately uses the simplified primary states so a project row says, for
// example, "(1 waiting, 2 stale)" rather than repeating thread-level evidence
// like "16 blocked stale, 5 at-risk stale".
func (m NOCModel) childTallyText(t nocChildTally) string {
	var parts []string
	if t.NeedsYou > 0 {
		parts = append(parts, m.th.paint(m.th.needsYou, strconv.Itoa(t.NeedsYou)+" needs-you"))
	}
	if t.Waiting > 0 {
		parts = append(parts, m.th.paint(m.th.atRisk, strconv.Itoa(t.Waiting)+" waiting"))
	}
	if t.Running > 0 {
		parts = append(parts, m.th.paint(m.th.running, strconv.Itoa(t.Running)+" running"))
	}
	if t.Stale > 0 {
		parts = append(parts, m.th.paint(m.th.dim, strconv.Itoa(t.Stale)+" stale"))
	}
	if len(parts) == 0 {
		return ""
	}
	open := m.th.paint(m.th.dim, "(")
	closep := m.th.paint(m.th.dim, ")")
	return open + strings.Join(parts, m.th.paint(m.th.dim, ", ")) + closep
}

// tallyText is a compact per-parent triage tally. LIVE classes lead, colored;
// STALE classes trail, dim and labeled "(stale)" so a stopped squad's ancient
// blocks read as decayed noise, not live attention, e.g.
// "(2 needs-you, 1 at-risk · 38 blocked stale)".
func (m NOCModel) tallyText(r state.TriageRollup) string {
	var live []string
	if r.NeedsYou > 0 {
		live = append(live, m.th.paint(m.th.needsYou, strconv.Itoa(r.NeedsYou)+" needs-you"))
	}
	// Simplified visible model: blocked + gated + at-risk collapse to one
	// "waiting" count. The granular split stays in the detail pane / JSON.
	if n := r.Blocked + r.Gated + r.AtRisk; n > 0 {
		live = append(live, m.th.paint(m.th.atRisk, strconv.Itoa(n)+" waiting"))
	}

	var stale []string
	if r.NeedsYouHistorical > 0 {
		stale = append(stale, strconv.Itoa(r.NeedsYouHistorical)+" needs-you history")
	}
	if r.BlockedStale > 0 {
		stale = append(stale, strconv.Itoa(r.BlockedStale)+" blocked stale")
	}
	if r.AtRiskStale > 0 {
		stale = append(stale, strconv.Itoa(r.AtRiskStale)+" at-risk stale")
	}
	if r.GatedStale > 0 {
		stale = append(stale, strconv.Itoa(r.GatedStale)+" gated stale")
	}

	if len(live) == 0 && len(stale) == 0 {
		return ""
	}
	sep := m.th.paint(m.th.dim, ", ")
	inner := strings.Join(live, sep)
	if len(stale) > 0 {
		staleText := m.th.paint(m.th.dim, strings.Join(stale, ", "))
		if inner != "" {
			inner += m.th.paint(m.th.dim, " "+m.dot()+" ") + staleText
		} else {
			inner = staleText
		}
	}
	open := m.th.paint(m.th.dim, "(")
	closep := m.th.paint(m.th.dim, ")")
	return open + inner + closep
}

// detailView renders the right pane for the selected node.
func (m NOCModel) detailView() string {
	n, ok := m.selectedNode()
	if !ok {
		return ""
	}
	switch n.kind {
	case nodeAgent:
		return m.agentDetail(n)
	case nodeSession:
		return m.sessionDetail(n)
	case nodeProject:
		return m.projectDetail(n)
	default:
		return m.rootDetail(n)
	}
}

// projectDetail summarizes a project: its triage tally, sessions, and (if any)
// its warning.
func (m NOCModel) projectDetail(n nocNode) string {
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.brand, "PROJECT  ") + m.th.paint(m.th.brand, n.label) + "\n")
	b.WriteString(m.th.paint(m.th.dim, n.project.Dir) + "\n")
	if n.warning != "" {
		b.WriteString(m.th.paint(m.th.atRisk, "warning: "+firstLine(n.warning)) + "\n")
		return b.String()
	}
	b.WriteString(m.detailRule() + "\n")
	if root := projectDetailAMQRoot(n.project); root != "" {
		b.WriteString(m.th.paint(m.th.dim, "amq base root") + "\n")
		b.WriteString(m.th.paint(m.th.dim, "  "+root) + "\n")
		b.WriteString(m.detailRule() + "\n")
	}
	if !n.project.DefaultTeam {
		b.WriteString(m.th.paint(m.th.dim, "team profile") + "\n")
		if n.project.TeamConfigured {
			b.WriteString(m.th.paint(m.th.dim, "  "+namedProfileDetail(n.project)+"; default profile missing") + "\n")
		} else {
			b.WriteString(m.th.paint(m.th.dim, "  not configured; press T to create a team profile") + "\n")
		}
		b.WriteString(m.detailRule() + "\n")
	}
	b.WriteString(m.th.paint(m.th.dim, "sessions") + "\n")
	sessions := sortedSessions(n.project.Snap.Sessions)
	if len(sessions) == 0 {
		if n.project.DefaultTeam || singleNamedProfile(n.project) != "" {
			b.WriteString(m.th.paint(m.th.dim, "  none yet; press N to start a new workstream") + "\n")
		} else if n.project.TeamConfigured {
			b.WriteString(m.th.paint(m.th.dim, "  none yet; press N and choose a profile") + "\n")
		} else {
			b.WriteString(m.th.paint(m.th.dim, "  none yet") + "\n")
		}
	}
	for _, sess := range sessions {
		ss := visibleState(sessionRollupState(sess))
		b.WriteString("  " + m.th.paint(m.th.nocStateStyle(ss), nocStateGlyph(ss, m.colorMode)+" "+nocStateText(ss)))
		b.WriteString(" " + m.th.paint(m.th.brand, sessionLabel(sess)))
		b.WriteString(m.th.paint(m.th.dim, fmt.Sprintf("  (%d agents)", len(sess.Agents))))
		b.WriteString("\n")
	}
	// Flow graph (toggled by 'f'): the inter-agent who-messages-whom for the
	// project's first/primary session. Independent of the timeline toggle.
	if m.showFlow {
		b.WriteString(m.flowSection(projectCoordination(n)))
	}
	b.WriteString(m.commandsSection(n.project, "", projectDetailAMQRoot(n.project)))
	return b.String()
}

func projectDetailAMQRoot(ps noc.ProjectSnapshot) string {
	return projectAMQRoot(ps)
}

// projectCoordination returns the coordination view used for a project node's
// flow graph: the first session's coordination (the team's edge list lives per
// session; a project node leads with its primary session). An empty project
// yields a zero Coordination, which renders the no-messages line.
func projectCoordination(n nocNode) state.Coordination {
	sessions := sortedSessions(n.project.Snap.Sessions)
	if len(sessions) == 0 {
		return state.Coordination{}
	}
	return sessions[0].Coordination
}

// flowArrow returns the directed-edge glyph: "→" normally, "->" in the ascii /
// NO_COLOR fallback so the graph stays legible without unicode.
func (m NOCModel) flowArrow() string {
	if m.colorMode == ColorAscii {
		return "->"
	}
	return "→"
}

// flowSection renders the inter-agent FLOW GRAPH sub-panel for a team-level
// (session / project) node: a divider, a header, then an adjacency listing of
// who-messages-whom built from the snapshot's already-derived edges
// (state.FlowGraph), sorted blocked-first then by descending volume. Each row
// reads "from → to  Nmsgs" with a TEXT status marker ([blocked] / [awaiting])
// so the state survives the ascii / NO_COLOR fallback (color is decoration). It
// is formatting ONLY — no new computation, no side effects. An edgeless view
// renders the "(no inter-agent messages yet)" line.
func (m NOCModel) flowSection(c state.Coordination) string {
	var b strings.Builder
	b.WriteString(m.detailRule() + "\n")
	b.WriteString(m.th.paint(m.th.dim, "flow graph") + "\n")

	edges := state.FlowGraph(c)
	if len(edges) == 0 {
		b.WriteString(m.th.paint(m.th.dim, "  (no inter-agent messages yet)") + "\n")
		return b.String()
	}

	arrow := m.flowArrow()
	// Cap so a busy session keeps the detail pane compact; the sort already put
	// the blocked / highest-volume links first, so the cap drops only the quiet
	// tail.
	const maxEdges = 8
	shown := edges
	if len(shown) > maxEdges {
		shown = shown[:maxEdges]
	}
	for _, e := range shown {
		flow := truncate(e.From+" "+arrow+" "+e.To, 30)
		b.WriteString("  " + m.th.paint(m.th.brand, flow))
		b.WriteString(m.th.paint(m.th.dim, "  "+strconv.Itoa(e.Count)+" msgs"))
		if label := e.Label(); label != "" {
			b.WriteString("  " + m.flowStatusTag(e, label))
		}
		b.WriteString("\n")
	}
	if len(edges) > maxEdges {
		b.WriteString(m.th.paint(m.th.dim, "  +"+strconv.Itoa(len(edges)-maxEdges)+" more") + "\n")
	}
	return b.String()
}

// flowStatusTag renders the per-edge outstanding marker as a colored TEXT tag so
// the meaning survives the ascii / NO_COLOR fallback (the tag text is always
// present; color is decoration). Blocked is the red/critical tier;
// awaiting-reply is the amber/warning tier.
func (m NOCModel) flowStatusTag(e state.FlowEdge, label string) string {
	tag := "[" + label + "]"
	if e.Blocked {
		return m.th.paint(m.th.blocked, tag)
	}
	return m.th.paint(m.th.atRisk, tag)
}

// sessionDetail leads with current control state, then shows a bounded thread
// preview, the agents table, and the optional recent actions timeline.
func (m NOCModel) sessionDetail(n nocNode) string {
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.brand, "SESSION  ") + m.th.paint(m.th.brand, n.label) + "\n")
	b.WriteString(m.th.paint(m.th.dim, n.project.Project) + "\n")
	b.WriteString(m.detailRule() + "\n")

	b.WriteString(m.th.paint(m.th.dim, "now") + "\n")
	nowThreads := currentControlThreads(n.session)
	if len(nowThreads) > 0 {
		shown := nowThreads
		if len(shown) > sessionNowLimit {
			shown = shown[:sessionNowLimit]
		}
		for _, th := range shown {
			b.WriteString("  " + m.compactThreadRow(th, detailThreadTitleWidth) + "\n")
			b.WriteString(m.needsYouContextLines(th))
		}
		if len(nowThreads) > len(shown) {
			b.WriteString(m.th.paint(m.th.dim, "  +"+strconv.Itoa(len(nowThreads)-len(shown))+" more current attention") + "\n")
		}
	} else if th, ok := topThread(n.session); ok {
		b.WriteString("  " + m.compactThreadRow(th, detailThreadTitleWidth) + "\n")
		b.WriteString(m.needsYouContextLines(th))
	} else {
		b.WriteString(m.th.paint(m.th.dim, "  (no current thread signal)") + "\n")
	}
	b.WriteString(m.detailRule() + "\n")

	// Thread history is a bounded newest-first preview. The full thread stream is
	// still available through the existing thread/read controls, but it should
	// not dominate the default control pane.
	threads := sortThreadsNewest(n.session, Filter{})
	header := "threads: newest"
	if len(threads) > sessionThreadPreviewLimit {
		header += " " + strconv.Itoa(sessionThreadPreviewLimit) + " of " + strconv.Itoa(len(threads))
	}
	b.WriteString(m.th.paint(m.th.dim, header) + "\n")
	if len(threads) == 0 {
		b.WriteString(m.th.paint(m.th.dim, "  (none open)") + "\n")
	}
	shownThreads := threads
	if len(shownThreads) > sessionThreadPreviewLimit {
		shownThreads = shownThreads[:sessionThreadPreviewLimit]
	}
	for _, th := range shownThreads {
		b.WriteString("  " + m.compactThreadRow(th, detailThreadTitleWidth) + "\n")
	}
	if len(threads) > len(shownThreads) {
		b.WriteString(m.th.paint(m.th.dim, "  +"+strconv.Itoa(len(threads)-len(shownThreads))+" older hidden") + "\n")
	}

	// Agents table.
	b.WriteString(m.detailRule() + "\n")
	b.WriteString(m.th.paint(m.th.dim, "agents: work state") + "\n")
	for _, ag := range sortedAgentsForSession(n.session) {
		st := visibleState(agentNodeState(n.session, ag))
		b.WriteString("  " + m.th.paint(m.th.nocStateStyle(st), nocStateGlyph(st, m.colorMode)+" "+nocStateText(st)))
		b.WriteString(" " + m.th.paint(m.th.brand, agentLabel(ag)))
		if ag.Engine != "" {
			b.WriteString(m.th.paint(m.th.dim, "  "+ag.Engine))
		}
		b.WriteString("\n")
	}

	// Flow graph (toggled by 'f'): the inter-agent who-messages-whom for this
	// session. Independent of the timeline toggle — both sub-panels may be open.
	if m.showFlow {
		b.WriteString(m.flowSection(n.session.Coordination))
	}

	// Recent actions / timeline.
	if m.showTimeline || len(threads) == 0 {
		b.WriteString(m.detailRule() + "\n")
		b.WriteString(m.th.paint(m.th.dim, "recent") + "\n")
		shown := 0
		for _, ev := range n.session.Coordination.Timeline {
			b.WriteString(m.th.paint(m.th.dim, "  "+truncate(ev.Summary, detailThreadTitleWidth)) + "\n")
			shown++
			if shown >= 5 {
				break
			}
		}
		if shown == 0 {
			b.WriteString(m.th.paint(m.th.dim, "  (no recent events)") + "\n")
		}
	}
	b.WriteString(m.commandsSection(n.project, n.session.Name, n.session.Root))
	return b.String()
}

// needsYouContextLines renders, for a NEEDS-YOU thread, the ask context inline
// (who is waiting on the operator and why, from the snapshot - no fetch) followed
// by the visible CTA. For non-needs-you threads it renders nothing. This is why
// the standalone context/read keys were removed: the context is always inline.
func (m NOCModel) needsYouContextLines(th state.ThreadSummary) string {
	if th.Triage != state.TriageNeedsYou || th.Historical {
		return ""
	}
	var b strings.Builder
	ctx := attnReasonPhrase(th)
	if age := nocThreadAge(th); age != "" {
		ctx += " " + m.dot() + " " + age
	}
	b.WriteString(m.th.paint(m.th.dim, "      "+ctx) + "\n")
	// The actual ask/context inline: the latest message body, capped + indented,
	// so the operator sees what is being asked before the CTA (no re-fetch).
	for _, ln := range askBodyLines(th.LatestBody) {
		b.WriteString(m.th.paint(m.th.dim, "      "+ln) + "\n")
	}
	b.WriteString(m.th.paint(m.th.needsYou, "      "+m.needsYouCTA(th.AttnReason)) + "\n")
	return b.String()
}

// askBodyLines renders the needs-you ask body for the right pane: non-empty lines
// each truncated to the detail width, capped to a few lines with an ellipsis,
// prefixed "ask: " on the first line and aligned under it after. Empty when there
// is no body.
func askBodyLines(body string) []string {
	const maxLines = 5
	var raw []string
	for _, ln := range strings.Split(body, "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			raw = append(raw, s)
		}
	}
	if len(raw) == 0 {
		return nil
	}
	var out []string
	for i, s := range raw {
		if i >= maxLines {
			out = append(out, "     …")
			break
		}
		prefix := "     " // align continuation lines under the first line's text
		if i == 0 {
			prefix = "ask: "
		}
		out = append(out, prefix+truncate(s, detailThreadTitleWidth))
	}
	return out
}

// needsYouCTA returns the visible call-to-action for a needs-you thread: an
// approval/decision offers approve+deny together; a plain ask offers reply+deny.
// Deny sits next to its peer choice, never as a cryptic global key.
func (m NOCModel) needsYouCTA(r state.AttnReason) string {
	sep := " " + m.dot() + " "
	switch r {
	case state.AttnApprove, state.AttnGoalReached:
		return "action: approve (a)" + sep + "deny (x)"
	default:
		return "action: reply (r)" + sep + "deny (x)"
	}
}

// agentDetail shows the selected agent's latest signal and recent threads.
func (m NOCModel) agentDetail(n nocNode) string {
	var b strings.Builder
	st := agentNodeState(n.session, n.agent)
	processState := agentState(n.agent)
	vs := visibleState(st)
	b.WriteString(m.th.paint(m.th.brand, "AGENT  ") + m.th.paint(m.th.brand, agentLabel(n.agent)))
	b.WriteString("  " + m.th.paint(m.th.nocStateStyle(vs), nocStateGlyph(vs, m.colorMode)+" "+nocStateText(vs)) + "\n")

	meta := []string{}
	if n.agent.Role != "" {
		meta = append(meta, "role "+n.agent.Role)
	}
	if n.agent.Engine != "" {
		meta = append(meta, "engine "+n.agent.Engine)
	}
	if st != processState {
		meta = append(meta, "process "+nocStateText(processState))
	}
	meta = append(meta, "session "+sessionLabel(n.session))
	b.WriteString(m.th.paint(m.th.dim, strings.Join(meta, " "+m.dot()+" ")) + "\n")
	b.WriteString(m.detailRule() + "\n")

	if agentOperational(n.agent) {
		if th, ok := newestThreadForAgent(n.session, n.agent.Handle); ok {
			parts := []string{truncate(threadTitle(th), detailThreadTitleWidth)}
			if age := nocThreadAge(th); age != "" {
				parts = append(parts, age)
			}
			b.WriteString(m.th.paint(m.th.dim, "latest signal") + "\n")
			b.WriteString("  " + m.th.paint(m.th.nocStateStyle(st), strings.Join(parts, " "+m.dot()+" ")) + "\n")
			b.WriteString(m.detailRule() + "\n")
		} else {
			b.WriteString(m.th.paint(m.th.dim, "latest signal") + "\n")
			b.WriteString("  " + m.th.paint(m.th.dim, "(no thread activity yet)") + "\n")
			b.WriteString(m.detailRule() + "\n")
		}
	}

	// Recent threads relevant to this agent: those it participates in.
	b.WriteString(m.th.paint(m.th.dim, "recent threads") + "\n")
	shown := 0
	for _, th := range sortThreadsNewest(n.session, Filter{}) {
		if !threadHasParticipant(th, n.agent.Handle) {
			continue
		}
		b.WriteString("  " + m.compactThreadRow(th, detailThreadTitleWidth) + "\n")
		shown++
		if shown >= agentThreadPreviewLimit {
			break
		}
	}
	if shown == 0 {
		b.WriteString(m.th.paint(m.th.dim, "  (no open threads)") + "\n")
	}
	return b.String()
}

// rootDetail summarizes a root header node.
func (m NOCModel) rootDetail(n nocNode) string {
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.brand, "ROOT  ") + m.th.paint(m.th.brand, n.label) + "\n")
	b.WriteString(m.th.paint(m.th.dim, "expand to see this root's projects") + "\n")
	return b.String()
}

// dot returns the inline separator dot, degraded to ascii on a dumb terminal.
func (m NOCModel) dot() string {
	if m.colorMode == ColorAscii {
		return "-"
	}
	return "·"
}

// detailRule is a short divider inside the detail pane.
func (m NOCModel) detailRule() string {
	return m.th.paint(m.th.dim, strings.Repeat(m.dot(), 28))
}

// footerView renders the keybindings (or the filter editor when active).
func (m NOCModel) footerView() string {
	if m.filterEditing {
		cursor := "▏"
		if m.colorMode == ColorAscii {
			cursor = "_"
		}
		prompt := "/filter: " + m.filter + cursor
		return m.th.paint(m.th.rule, m.thinRule()) + "\n" + m.th.paint(m.th.atRisk, prompt)
	}
	// The nav/view footer legend renders from the single-source keymap
	// (noc_keymap.go), so footer and help can never drift from the router.
	keys := nocFooterNavLegend(m.colorMode == ColorAscii)
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()) + "\n")
	notes := []string{}
	if m.filter != "" {
		notes = append(notes, m.th.paint(m.th.atRisk, "filter: "+m.filter))
	}
	if m.hideStale {
		notes = append(notes, m.th.paint(m.th.dim, "hiding stale squads (h shows all)"))
	}
	// actNote surfaces the result/decline of the last control action (mirrors
	// jumpNote for the read-only jump) so a confirm / cancel / failure is legible.
	if m.actNote != "" {
		notes = append(notes, m.th.paint(m.th.dim, m.actNote))
	}
	// refreshNote flashes "refreshed (just now)" after an explicit g press so the
	// operator sees g worked; it clears on the next keypress (set only by g, never
	// by the silent 2s tick).
	if m.refreshNote != "" {
		notes = append(notes, m.th.paint(m.th.dim, m.refreshNote))
	}
	if len(notes) > 0 {
		b.WriteString(strings.Join(notes, m.th.paint(m.th.dim, "  "+m.dot()+"  ")) + "\n")
	}
	b.WriteString(m.th.paint(m.th.dim, keys))
	b.WriteString("\n")
	// The control-key legend is a second footer row, CONTEXT-SENSITIVE (#4.2): it
	// shows only the mutating actions valid for the SELECTED row's kind, so the
	// operator is not taught keys that do nothing here. The full key map is one
	// keypress away under ? (the nav row above advertises it).
	ascii := m.colorMode == ColorAscii
	control := m.controlFooterLegendForSelection(ascii)
	if control == "" {
		control = "no actions for this row (select a squad with work; ? for all keys)"
	}
	b.WriteString(m.th.paint(m.th.dim, control))
	return b.String()
}

// controlFooterLegendForSelection renders the control footer legend filtered to
// the actions ACTUALLY available on the current selection (#4.2) - not merely
// row-kind applicable. controlActionAvailable mirrors each begin* guard, so the
// footer never advertises an action the handler would immediately reject.
func (m NOCModel) controlFooterLegendForSelection(ascii bool) string {
	sep := " · "
	if ascii {
		sep = " | "
	}
	out := ""
	for _, bnd := range nocKeyMap {
		if bnd.Group != keyGroupAction || !m.controlActionAvailable(bnd) {
			continue
		}
		tok := bnd.Footer
		if ascii {
			tok = bnd.FooterAscii
		}
		if tok == "" {
			continue
		}
		if out != "" {
			out += sep
		}
		out += tok
	}
	return out
}

// controlActionAvailable reports whether a CONTROL key would actually act on the
// current selection. It applies the same predicates the begin* handlers use:
//   - approve/reply/deny require an active needs-you thread,
//   - broadcast + stop/resume/restart require a resolvable squad,
//   - delete requires a configured team-home with profiles,
//   - new-session requires a launchable profile,
//   - new-team requires a resolvable project (candidate or configured),
//   - drain/message require an agent row (covered by the row-kind scope).
func (m NOCModel) controlActionAvailable(bnd nocKeyBinding) bool {
	n, ok := m.selectedNode()
	if !ok || !bnd.appliesTo(n.kind) {
		return false
	}
	switch bnd.Keys[0] {
	case "a", "r", "x":
		_, _, ok := m.selectedNeedsYouThread()
		return ok
	case "b", "S", "R", "X":
		_, _, _, _, _, ok := m.selectedSquad()
		return ok
	case "delete":
		p, ok := m.selectedProjectSnapshot()
		return ok && p.TeamConfigured && len(projectLaunchProfiles(p)) > 0
	case "N":
		p, ok := m.selectedProjectSnapshot()
		if !ok {
			return false
		}
		_, _, _, ready := launchProfileForNewSession(p)
		return ready
	case "T":
		_, ok := m.selectedProjectSnapshot()
		return ok
	default: // d, m: row-kind scope (agent) is the only requirement
		return true
	}
}

// helpView is the full help overlay.
func (m NOCModel) helpView() string {
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.brand, "amq-noc NOC help") + "\n")
	b.WriteString(m.th.paint(m.th.rule, m.rule()) + "\n\n")
	// NAVIGATION + VIEW render from the single-source keymap (noc_keymap.go) so the
	// help overlay can never advertise a nav/view key the router does not handle.
	lines := nocKeyHelpLines(keyGroupNav, "NAVIGATION (read-only; no tmux focus side effects)")
	lines = append(lines, "")
	lines = append(lines, nocKeyHelpLines(keyGroupView, "VIEW")...)
	lines = append(lines,
		"",
		"PRIMARY STATUS MODEL",
		"  running           team is alive and working, nothing outstanding",
		"  waiting           an operational agent is waiting on non-human work (peer review, block, or gate)",
		"  needs-you         an agent is explicitly waiting for operator action now",
		"  stale             stopped, aged, or historical context",
		"  Text labels are authoritative; color and glyphs are hints.",
		"")
	lines = append(lines, controlHelpLines()...)
	for _, l := range lines {
		b.WriteString(m.th.paint(m.th.dim, l) + "\n")
	}
	return b.String()
}

// --- small string helpers (visible-width aware enough for our ASCII labels) ---

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

// threadTitle is a thread's display title: its subject, or a short id fallback.
func threadTitle(th state.ThreadSummary) string {
	if s := strings.TrimSpace(th.Subject); s != "" {
		return s
	}
	return shortID(th.ID)
}

// nocThreadAge renders a thread's pre-computed freshness age (the snapshot ages
// it against the build clock, so this is deterministic and needs no live clock).
func nocThreadAge(th state.ThreadSummary) string {
	if th.Freshness.Age > 0 {
		return ageLabel(th.Freshness.Age)
	}
	return ""
}

func currentControlThreads(sess state.Session) []state.ThreadSummary {
	needsYou := make([]state.ThreadSummary, 0, len(sess.Coordination.Threads))
	for _, th := range sess.Coordination.Threads {
		if th.Historical || th.Stale || th.Triage != state.TriageNeedsYou {
			continue
		}
	}
	sortControlThreads(needsYou)
	return needsYou
}

func sortControlThreads(out []state.ThreadSummary) {
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := triageRank(out[i].Triage), triageRank(out[j].Triage)
		if ri != rj {
			return ri < rj
		}
		if out[i].Triage == state.TriageNeedsYou {
			ai, aj := out[i].AttnReason.Rank(), out[j].AttnReason.Rank()
			if ai != aj {
				return ai < aj
			}
		}
		if !out[i].LastEventAt.Equal(out[j].LastEventAt) {
			return out[i].LastEventAt.After(out[j].LastEventAt)
		}
		return out[i].ID < out[j].ID
	})
}

func (m NOCModel) compactThreadRow(th state.ThreadSummary, titleMax int) string {
	st := triageState(th.Triage)
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.nocStateStyle(st), nocStateGlyph(st, m.colorMode)))
	if label := compactThreadStateText(th); label != "" {
		b.WriteString(" " + m.th.paint(m.th.nocStateStyle(st), label))
	}
	b.WriteString(" " + truncate(threadTitle(th), titleMax))
	if detail := compactThreadDetail(th); detail != "" {
		b.WriteString(m.th.paint(m.th.dim, "  "+detail))
	}
	return b.String()
}

func compactThreadStateText(th state.ThreadSummary) string {
	if th.Triage == state.TriageClear {
		return ""
	}
	if th.Historical {
		return "history"
	}
	return nocStateText(triageState(th.Triage))
}

func compactThreadDetail(th state.ThreadSummary) string {
	age := nocThreadAge(th)
	if th.Triage == state.TriageNeedsYou {
		if th.Historical {
			if age != "" {
				return "unresolved " + age
			}
			return "unresolved"
		}
		reason := "user answer"
		switch th.AttnReason {
		case state.AttnApprove:
			reason = "user approval"
		case state.AttnGoalReached:
			reason = "review or close"
		}
		if age != "" {
			return reason + " " + age
		}
		return reason
	}
	return age
}

func newestThreadForAgent(sess state.Session, handle string) (state.ThreadSummary, bool) {
	var best state.ThreadSummary
	found := false
	for _, th := range sess.Coordination.Threads {
		if !threadHasParticipant(th, handle) {
			continue
		}
		if !found || th.LastEventAt.After(best.LastEventAt) || (th.LastEventAt.Equal(best.LastEventAt) && th.ID < best.ID) {
			best = th
			found = true
		}
	}
	return best, found
}

// threadHasParticipant reports whether handle is among a thread's participants.
func threadHasParticipant(th state.ThreadSummary, handle string) bool {
	for _, p := range th.Participants {
		if strings.EqualFold(p, handle) {
			return true
		}
	}
	return false
}

// nocCount renders a counted noun, e.g. "1 squad" / "3 squads".
func nocCount(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// padVisible pads s to width w accounting for ANSI escape sequences so the
// two-pane gutter aligns even when the left cell contains color codes.
func padVisible(s string, w int) string {
	vis := visibleWidth(s)
	if vis >= w {
		return s
	}
	return s + strings.Repeat(" ", w-vis)
}

// visibleWidth returns the rune count of s ignoring ANSI escape sequences.
func visibleWidth(s string) int {
	n := 0
	inEsc := false
	for _, r := range s {
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == '\x1b' {
			inEsc = true
			continue
		}
		n++
	}
	return n
}

// truncateVisible clamps s to at most w VISIBLE columns, preserving ANSI escape
// sequences (which cost zero columns) and always appending a reset so a cut mid-
// style never bleeds color into the rest of the frame. This is what keeps a live
// two-pane row inside m.width: an un-truncated detail pane made each composed row
// ~219 cols wide in a 200-col pane, so every tree row WRAPPED and the live tree
// rendered as one corrupted line (arrows/enter/t still mutated state, but the
// over-wide wrap hid the moving selection bar). w <= 0 yields "".
func truncateVisible(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if visibleWidth(s) <= w {
		return s
	}
	var b strings.Builder
	cols := 0
	inEsc := false
	wrote := false
	for _, r := range s {
		if inEsc {
			b.WriteRune(r)
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == '\x1b' {
			inEsc = true
			b.WriteRune(r)
			continue
		}
		if cols >= w {
			wrote = true
			break
		}
		b.WriteRune(r)
		cols++
	}
	if wrote {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// truncateVisibleEllipsis clamps s to at most w VISIBLE columns like
// truncateVisible, but appends an ellipsis ("…", or "..." in ascii mode) when s
// is actually shortened so a clipped row reads as truncated rather than as a
// hard cut. ANSI escape sequences cost zero columns and are preserved, and a
// trailing reset is appended whenever any escape was emitted so a cut mid-style
// never bleeds color. This is what pins the left tree column at leftW: a long
// brief is ellipsized at the column edge instead of overrunning the divider.
// w <= 0 yields "".
func truncateVisibleEllipsis(s string, w int, ascii bool) string {
	if w <= 0 {
		return ""
	}
	if visibleWidth(s) <= w {
		return s
	}
	ell := "…"
	if ascii {
		ell = "..."
	}
	ellW := len([]rune(ell))
	// Not even room for the ellipsis: fall back to a plain hard clip.
	if w <= ellW {
		return truncateVisible(s, w)
	}
	// Reserve columns for the ellipsis, copy up to that budget preserving ANSI.
	budget := w - ellW
	var b strings.Builder
	cols := 0
	inEsc := false
	sawEsc := false
	for _, r := range s {
		if inEsc {
			b.WriteRune(r)
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == '\x1b' {
			inEsc = true
			sawEsc = true
			b.WriteRune(r)
			continue
		}
		if cols >= budget {
			break
		}
		b.WriteRune(r)
		cols++
	}
	if sawEsc {
		b.WriteString("\x1b[0m")
	}
	b.WriteString(ell)
	return b.String()
}

// nocNoProjectsGuidance is the clear, never-a-crash empty state.
func nocNoProjectsGuidance(roots []string) string {
	var b strings.Builder
	b.WriteString("No amq-squad projects found under:\n")
	if len(roots) == 0 {
		b.WriteString("  (current directory)\n")
	}
	for _, r := range roots {
		b.WriteString("  " + displayRoot(r) + "\n")
	}
	b.WriteString("\nA project is any directory containing .agent-mail/, .amq-squad/team.json, or a .git marker.\n")
	b.WriteString("Try a different --root, increase --depth, or run 'amq-squad new team --project <team-home>' then 'amq-squad new session --project <team-home> <name>'.\n")
	return b.String()
}
