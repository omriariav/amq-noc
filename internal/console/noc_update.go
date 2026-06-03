// Package console — noc_update.go: the tea.Update reducer + key routing + the
// (only) side effect, the read-only tmux jump.
package console

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/state"
)

// Update implements tea.Model. It folds immutable messages into new model state
// and never blocks; data feeds deliver snapshots / ticks / watch events.
//
// POINTER receiver: the program is driven as *NOCModel (tea.NewProgram(&m)), so
// every key handler's cursor / collapse / filter mutation lands on the SAME
// model the Bubble Tea event loop re-binds and renders on the next frame
// (tea.go: `model, cmd = model.Update(msg)` then `model.View()`). The movement
// helpers (moveCursor / clampCursor / preserveSelection / rememberSelection) are
// pointer-receiver; a VALUE Update would mutate a throwaway copy of the model
// and the live surface would look frozen (arrows / j / k dead), so Update / Init
// / View are all pointer-receiver to keep one consistent model.
func (m *NOCModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		return m, nil

	case nocTickMsg:
		return m, tea.Batch(nocRebuildCmd(m.rebuild), nocTickCmd(m.rebuild.refreshCadence()))

	case nocRediscoverMsg:
		// Re-discovery is the same Collect call (it re-walks the roots), so a
		// fresh rebuild already surfaces new/removed projects. Reschedule.
		return m, tea.Batch(nocRebuildCmd(m.rebuild), nocRediscoverTickCmd())

	case nocWatchMsg:
		return m, nocRebuildCmd(m.rebuild)

	case nocSnapshotMsg:
		// Detect needs-you transitions (0 → >0 per session) against the PRIOR
		// snapshot BEFORE replacing m.ms, then ring the bell + set the banner once
		// (read-only; suppressed when muted). This is the only place alerts fire so
		// they key off real snapshot deltas, not every keypress.
		alerts := m.detectNeedsYouTransitions(msg.ms)
		m.ms = msg.ms
		m.ready = true
		m.fireNeedsYouAlerts(alerts)
		m.refreshGuidance()
		m.clampCursor()
		m.preserveSelection()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// refreshGuidance sets the no-projects guidance state when the roots are empty.
func (m *NOCModel) refreshGuidance() {
	if m.hasProjects() {
		m.guidance = ""
		return
	}
	m.guidance = nocNoProjectsGuidance(m.rebuild.Roots)
}

// handleKey routes a key press. The keymap is NON-OVERLOADED and READ-ONLY: the
// only side effect is the tmux jump.
//
// Keymap:
//
//	↑/↓ or j/k     move selection
//	→/l or enter   expand a collapsed parent / drill in; on a RUNNING agent,
//	               enter JUMPS (tmux switch). A dedicated 'J' also jumps.
//	←              collapse the current node (or ascend to its parent)
//	h              toggle hiding stopped/archived (stale) squads
//	/              filter (needs-you/gated/at-risk/blocked/agent:/model:/project:/session:)
//	t              toggle the timeline in the detail pane
//	f              toggle the inter-agent flow graph in the detail pane
//	g              refresh now
//	esc            clear filter / collapse / back
//	q              quit
//	?              help
func (m *NOCModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Control overlays take the key first so a mutating action is always two-step
	// and self-contained: while the confirm overlay is open ONLY y/esc/other are
	// meaningful (handleConfirmKey gates the single seam call); while the body
	// editor is open keys feed the buffer. Both are checked before nav so a
	// control flow never leaks a keystroke into the read-only keymap.
	if m.pending != nil {
		return m.handleConfirmKey(msg.String())
	}
	// The read-only FOCUS confirm overlay (jump / J / o) is gated the same way the
	// mutating confirm is: while it is open ONLY y/Y/enter performs the focus; any
	// other key (esc included) cancels with zero effect. It is checked before the
	// input editor and nav so a confirm flow never leaks a keystroke.
	if m.jumpPending != nil {
		return m.handleFocusConfirmKey(msg.String())
	}
	if m.input != nil {
		return m.handleInputKey(msg)
	}
	// The command palette (PR18) is an overlay; like the control overlays it takes
	// the key first so typing/selection never leaks into the nav keymap. Jump and
	// focus choices use the existing gated tmux movement; create choices open the
	// same preview-gated T/N editors.
	if m.palette != nil {
		return m.handlePaletteKey(msg)
	}
	if m.filterEditing {
		return m.handleFilterKey(msg)
	}
	if m.showHelp {
		// Any key dismisses help except quit.
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		default:
			m.showHelp = false
			return m, nil
		}
	}
	if m.readResult != nil || m.drainResult != nil || m.inboxResult != nil || m.dlqResult != nil || m.dlqReadResult != nil || m.dlqRetryResult != nil || m.dlqPurgeResult != nil || m.dlqRetryAllResult != nil || m.receiptsResult != nil || m.receiptsWaitResult != nil || m.messageWaitResult != nil || m.amqCleanupResult != nil || m.threadContextResult != nil || m.amqOpsResult != nil || m.amqWhoResult != nil || m.amqEnvResult != nil || m.presenceResult != nil || m.projectDoctorResult != nil || m.projectHistoryResult != nil || m.teamRulesResult != nil || m.projectResumePlanResult != nil || m.forkPlanResult != nil || m.briefResult != nil || m.statusResult != nil || m.threadsResult != nil || m.roleMarket != nil || m.teamProfiles != nil {
		return m.handleResultKey(msg.String())
	}

	m.jumpNote = ""
	m.actNote = ""
	// The refresh flash ("refreshed (just now)") is acknowledged by any keypress,
	// mirroring jumpNote/actNote/alertBanner. It is set ONLY by an explicit g
	// refresh below; the silent 2s ticker never sets it.
	m.refreshNote = ""
	// The needs-you alert banner is acknowledged by any keypress (it persists
	// across silent 2s refreshes but clears once the operator acts), mirroring
	// jumpNote/actNote. It re-appears on the next 0→N transition.
	m.alertBanner = ""

	// Control keys are ADDITIVE and checked before the read-only keymap: a key
	// the control layer owns (i/v/d/a/r/x/m/b/S/R/X/N/T/o) opens a read or
	// preview/confirm flow. For 'o' it opens a read-only focus. Any other key
	// falls through to the unchanged nav/peek/filter/jump keymap below.
	if m.controlEnabled() {
		if cmd, handled := m.handleControlKey(msg.String()); handled {
			return m, cmd
		}
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "?":
		m.showHelp = true
		return m, nil
	case "p", "ctrl+p":
		// Open the command palette: fuzzy-find projects, actions, teams, and
		// agents across all discovered projects.
		return m, m.openPalette()
	case "A":
		// Toggle needs-you alerts (bell + banner). Mirrors the --no-bell flag.
		m.alertsMuted = !m.alertsMuted
		if m.alertsMuted {
			m.actNote = "alerts muted (A to unmute)"
			m.alertBanner = ""
		} else {
			m.actNote = "alerts on (A to mute)"
		}
		return m, nil
	case "t":
		m.showTimeline = !m.showTimeline
		return m, nil
	case "f":
		// Toggle the inter-agent FLOW GRAPH in the detail pane (2.3). Read-only:
		// it renders the snapshot's already-derived edges (who-messages-whom) with
		// volume + blocked/awaiting markers; no new computation, no side effects.
		// Independent of the timeline toggle ('t') — both sub-panels may be open.
		m.showFlow = !m.showFlow
		return m, nil
	case "h":
		// Toggle hiding stopped/archived (stale) squads so the operator can focus
		// on what is alive. (Collapse is left/←/esc; 'h' is no longer overloaded
		// onto collapse — see footer/help.) Re-anchor the selection afterward.
		m.hideStale = !m.hideStale
		m.clampCursor()
		m.preserveSelection()
		return m, nil
	case "g":
		// Explicit refresh: kick a fresh snapshot AND flash a visible note so the
		// operator sees g worked (the rebuild is otherwise indistinguishable from
		// the silent 2s tick). The note is set here, on the explicit g press only —
		// never on nocTickMsg — and clears on the next keypress (block above).
		m.refreshNote = "refreshed (just now)"
		return m, nocRebuildCmd(m.rebuild)
	case "/":
		m.filterEditing = true
		return m, nil
	case "up", "k":
		m.moveCursor(-1)
		return m, nil
	case "down", "j":
		m.moveCursor(1)
		return m, nil
	case "right", "l":
		return m.expandOrDrill()
	case "enter":
		return m.enter()
	case "J":
		return m.jump()
	case "left":
		return m.collapseOrAscend()
	case "esc":
		if m.filter != "" {
			m.filter = ""
			m.clampCursor()
			m.preserveSelection()
			return m, nil
		}
		return m.collapseOrAscend()
	}
	return m, nil
}

// handleFilterKey edits the filter string while the editor is open.
func (m *NOCModel) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.filterEditing = false
		m.clampCursor()
		m.preserveSelection()
		return m, nil
	case "esc":
		m.filterEditing = false
		m.filter = ""
		m.clampCursor()
		m.preserveSelection()
		return m, nil
	case "backspace":
		if len(m.filter) > 0 {
			m.filter = m.filter[:len(m.filter)-1]
		}
		m.clampCursor()
		return m, nil
	default:
		if len(msg.String()) == 1 {
			m.filter += msg.String()
			m.clampCursor()
		}
		return m, nil
	}
}

// moveCursor moves selection by delta and remembers the new id.
func (m *NOCModel) moveCursor(delta int) {
	ns := m.nodes()
	if len(ns) == 0 {
		m.scroll = 0
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(ns) {
		m.cursor = len(ns) - 1
	}
	m.ensureCursorVisibleFor(ns)
	m.rememberSelection()
}

// expandOrDrill expands a collapsed parent or, when already expanded, moves into
// its first child. On a running agent it does nothing (use enter/J to jump).
func (m *NOCModel) expandOrDrill() (tea.Model, tea.Cmd) {
	n, ok := m.selectedNode()
	if !ok {
		return m, nil
	}
	if n.hasKids {
		if m.tree.isCollapsed(n.id) {
			m.tree.setCollapsed(n.id, false)
			m.preserveSelection()
			return m, nil
		}
		// Already expanded: drill to the first child (the next node deeper).
		m.moveCursor(1)
		return m, nil
	}
	return m, nil
}

// enter JUMPS only on a RUNNING-AGENT row; on every other row it DRILLS/EXPANDS
// and never teleports into tmux (the jump guard). A STOPPED agent row leaves a
// note explaining there is nothing live to jump to, rather than silently doing
// nothing.
//
//   - running agent (nodeAgent && canJump): jump (the only tmux side effect).
//   - stopped agent (nodeAgent && !canJump): a note, no jump.
//   - project / session / root: expand or drill — never a jump.
func (m *NOCModel) enter() (tea.Model, tea.Cmd) {
	n, ok := m.selectedNode()
	if !ok {
		return m, nil
	}
	if n.kind == nodeAgent {
		if n.canJump {
			return m.jump()
		}
		// Not jumpable: explain WHY from the agent's real computed liveness rather
		// than a flat "not running" (which misleads when the row shows an
		// active-looking state like dead-mailbox-live).
		m.jumpNote = noJumpReason(n.agent.Liveness)
		return m, nil
	}
	// PARENT row (project / session / root): expand/drill WITHOUT a confirm. The
	// focus guard applies only to the actual jump/focus on a running-agent row.
	return m.expandOrDrill()
}

// noJumpReason returns a state-accurate explanation for why a non-jumpable agent
// cannot be jumped to, keyed off its computed liveness. Only a verified-alive
// agent is jumpable (there is a live process + pane to attach to); every other
// state explains its own situation instead of the misleading flat "not running".
func noJumpReason(l state.Liveness) string {
	switch l {
	case state.LivenessWakeLive:
		return "wake helper is live, but no verified agent process or pane is available to jump to"
	case state.LivenessDeadMailboxLive:
		return "agent process is gone but its mailbox is still active (dead-mailbox-live) — nothing live to attach to"
	case state.LivenessStale:
		return "agent looks stale (no recent heartbeat) — nothing live to attach to"
	case state.LivenessDead:
		return "agent process is dead — resume the session to bring it back"
	case state.LivenessMissing:
		return "no live process for this agent — resume the session to launch it"
	default:
		return "agent is not running — nothing to jump to (enter jumps only on a running agent)"
	}
}

// collapseOrAscend collapses an expanded parent, or moves selection to the
// parent node when the current node is a leaf / already collapsed.
func (m *NOCModel) collapseOrAscend() (tea.Model, tea.Cmd) {
	n, ok := m.selectedNode()
	if !ok {
		return m, nil
	}
	if n.hasKids && !m.tree.isCollapsed(n.id) {
		m.tree.setCollapsed(n.id, true)
		m.rememberSelection()
		m.clampCursor()
		return m, nil
	}
	// Ascend: walk up to the nearest shallower node before the cursor.
	ns := m.nodes()
	for i := m.cursor - 1; i >= 0; i-- {
		if ns[i].depth < n.depth {
			m.cursor = i
			m.ensureCursorVisibleFor(ns)
			m.rememberSelection()
			return m, nil
		}
	}
	return m, nil
}

// jump opens the READ-ONLY focus CONFIRM overlay for the selected running agent
// (QA-2 / QA-4b). It does NOT focus immediately: it previews "Jump to <role> in
// <session>? (focus its iTerm2 window)" and only a confirmed y/Y/enter runs the
// focus (performAgentJump). Any other key / esc cancels with zero effect. The
// jump is read-only — the only effect is terminal focus, never squad state.
func (m *NOCModel) jump() (tea.Model, tea.Cmd) {
	n, ok := m.selectedNode()
	if !ok || n.kind != nodeAgent {
		return m, nil
	}
	if !n.canJump {
		m.jumpNote = "agent is not running — cannot jump"
		return m, nil
	}
	who := strings.TrimSpace(n.agent.Role)
	if who == "" {
		who = strings.TrimSpace(n.agent.Handle)
	}
	m.jumpPending = &pendingFocus{
		prompt: "Jump to " + who + " in " + n.session.Name + "? (focus its iTerm2 window)",
		run:    func(m *NOCModel) { m.performAgentJump(n.agent, n.session.Name, n.project.Dir, n.agent.Handle) },
	}
	return m, nil
}

// performAgentJump performs the READ-ONLY tmux focus to a running agent: resolve
// the live pane (rotation-proof via cwd+engine name-first, PID-tree bonus), then
// call the injected switcher. If no pane resolves, or the switch reports a
// not-in-tmux condition, it surfaces SuggestJump text rather than erroring out.
// It is reached ONLY from the focus-confirm gate (handleFocusConfirmKey), so a
// switchTo call here always corresponds to an operator confirm.
func (m *NOCModel) performAgentJump(agent state.Agent, session, projectDir, handle string) {
	panes, err := m.panes()
	if err != nil {
		m.jumpNote = "tmux not available: " + err.Error()
		return
	}
	target, resolved := noc.ResolveTmuxTargetForSession(agent, session, projectDir, panes, m.pidTree)
	if !resolved {
		m.jumpNote = "no live tmux pane found for " + handle + " (resume it, or attach manually)"
		return
	}
	if err := m.switchTo(target); err != nil {
		if nit, isNIT := err.(*noc.NotInTmuxError); isNIT {
			m.jumpNote = "not inside tmux - run: " + nit.Command
			return
		}
		m.jumpNote = "jump: " + err.Error() + " (try: " + noc.SuggestJump(target) + ")"
		return
	}
	// A successful focus raises the agent's pane/window; leave a note so a
	// returning operator sees what happened.
	m.jumpNote = "jumped to " + noc.SuggestJump(target)
}

// pendingFocus is the confirm overlay's state for a READ-ONLY focus action
// (jump on a running agent / J / o). prompt is what the overlay shows; run is
// the focus to perform on a confirmed y/Y/enter. It is the read-only analogue of
// pendingAction: the only effect of run is terminal focus — never a squad
// mutation, never a spawn. The closure captures the node context at key-press
// time so a refresh that lands while the overlay is open never re-targets it.
type pendingFocus struct {
	prompt string
	run    func(m *NOCModel)
}

// handleFocusConfirmKey gates a previewed focus on an explicit y/Y/enter. ANY
// other key (esc included) cancels with zero effect — the focus seam is never
// reached on a decline. This is the single gate between a jump/J/o keypress and
// the read-only terminal focus.
func (m *NOCModel) handleFocusConfirmKey(key string) (tea.Model, tea.Cmd) {
	p := m.jumpPending
	switch key {
	case "y", "Y", "enter":
		m.jumpPending = nil
		if p != nil && p.run != nil {
			p.run(m)
		}
		return m, nil
	default:
		// Decline: clear the overlay, focus NOTHING.
		m.jumpPending = nil
		m.jumpNote = "cancelled - nothing focused"
		return m, nil
	}
}
