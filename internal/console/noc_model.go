// Package console: noc_model.go is the Bubble Tea model for the NOC command
// center, a visibility-first multi-project TUI over noc.MultiSnapshot.
//
// Safety contract: ordinary navigation and inspection keys are read-only.
// Mutating control keys use preview-first confirm overlays, and all process
// effects go through injected seams so tests can prove a declined overlay
// mutates nothing. Data feeds deliver immutable nocSnapshotMsg values and no
// goroutine mutates the model.
package console

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-noc/internal/act"
	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/state"
)

// NOCDefaultRefresh is the periodic resync cadence when unset.
const NOCDefaultRefresh = 2 * time.Second

// NOCDefaultRediscover is how often the roots are re-walked so new/removed
// projects appear without a restart.
const NOCDefaultRediscover = 20 * time.Second

// NOCRebuildConfig is everything a NOC snapshot rebuild needs. Immutable across
// the program's lifetime.
type NOCRebuildConfig struct {
	Roots      []string
	Depth      int
	Probe      state.Probe
	Thresholds state.Thresholds
	// Refresh is the periodic resync cadence the refresh ticker uses. Zero falls
	// back to NOCDefaultRefresh.
	Refresh time.Duration
}

// refreshCadence returns the configured refresh, defaulted.
func (c NOCRebuildConfig) refreshCadence() time.Duration {
	if c.Refresh <= 0 {
		return NOCDefaultRefresh
	}
	return c.Refresh
}

// nocSwitcher is the injectable jump seam. Production points it at noc.SwitchTo;
// tests record the resolved target. It is a struct field (not a package global)
// so concurrent tests never collide.
type nocSwitcher func(noc.TmuxTarget) error

// nocPaneLister is the injectable tmux pane-list seam (read-only).
type nocPaneLister func() ([]noc.TmuxPane, error)

// NOCModel is the Bubble Tea model. Pure view + UI state.
type NOCModel struct {
	rebuild NOCRebuildConfig
	version string

	ms      noc.MultiSnapshot
	lastErr error

	tree   nocTreeState
	cursor int
	scroll int
	width  int
	height int
	ready  bool

	filter        string
	filterEditing bool
	showHelp      bool
	showTimeline  bool
	// showFlow toggles the inter-agent FLOW GRAPH in the detail pane (2.3): an
	// ASCII edge list of who-messages-whom for the selected session/project,
	// built from the snapshot's already-derived edges + thread status. Toggled by
	// 'f'. It is INDEPENDENT of showTimeline — both sub-panels can be open at once
	// (flow first, then timeline). Read-only: it only formats existing state.
	showFlow bool

	// commandPicker is a local clipboard helper over the deterministic commands
	// shown in the right pane. It writes only to the operator clipboard seam, not
	// AMQ/squad state.
	commandPicker *commandPickerOverlay

	// hideStale hides STOPPED / stale (archived) squads so the operator can focus
	// on what is alive. Toggled by 'h'. Off by default.
	hideStale bool

	// fullTree forces the full root→project→session→agent expansion in the static
	// (--once) render. Off by default: --once leads with project rollups + a
	// needs-attention section, not the firehose. The interactive TUI always
	// drills via the tree regardless of this flag.
	fullTree bool

	// jumpNote is a transient status line (e.g. SuggestJump text when a jump
	// could not resolve a live pane). Cleared on the next navigation.
	jumpNote string

	// selectedID is the stable node id preserved across snapshot replacement.
	selectedID string

	colorMode ColorMode
	th        nocTheme

	// guidance is shown when no projects were found under the roots.
	guidance string

	// --- Control layer (PR15). All MUTATING control state lives here; the
	// read-only fields above are untouched. ---

	// pending is the confirm overlay: non-nil means a MUTATING action has been
	// previewed and is awaiting an explicit y/enter (any other key cancels with
	// zero effect). It is the single gate between a keypress and a seam call.
	pending *pendingAction

	// readResult is a post-confirm result overlay for a needs-you read. The read
	// itself has already run through the confirmed seam; this field only displays
	// the returned body until the operator dismisses it.
	readResult *readResultOverlay
	// drainResult is a post-confirm result overlay for an agent inbox drain. The
	// drain itself has already run through the confirmed seam; this field only
	// displays the returned drain output until the operator dismisses it.
	drainResult *drainResultOverlay
	// inboxResult is a read-only result overlay for an agent inbox list. The AMQ
	// list itself has already run through the injected seam; this field only
	// displays stdout until the operator dismisses it.
	inboxResult *inboxResultOverlay
	// dlqResult is a read-only result overlay for an agent DLQ list. It inspects
	// failed/corrupt AMQ messages without retrying or purging.
	dlqResult *dlqResultOverlay
	// dlqReadResult is a post-confirm result overlay for reading one DLQ item.
	dlqReadResult *dlqReadResultOverlay
	// dlqRetryResult is a post-confirm result overlay for retrying one DLQ item.
	dlqRetryResult *dlqRetryResultOverlay
	// dlqPurgeResult is a post-confirm result overlay for purging old DLQ items.
	dlqPurgeResult *dlqPurgeResultOverlay
	// dlqRetryAllResult is a post-confirm result overlay for retrying every new
	// DLQ item for one agent.
	dlqRetryAllResult *dlqRetryAllResultOverlay
	// receiptsResult is a read-only result overlay for an agent receipts list.
	receiptsResult *receiptsResultOverlay
	// receiptsWaitResult is a post-confirm result overlay for one receipt wait.
	receiptsWaitResult *receiptsWaitResultOverlay
	// messageWaitResult is a post-confirm result overlay for send --wait-for.
	messageWaitResult *messageWaitResultOverlay
	// amqCleanupResult is a post-confirm result overlay for AMQ tmp cleanup.
	amqCleanupResult *amqCleanupResultOverlay
	// threadContextResult is a read-only result overlay for a needs-you thread
	// transcript. It uses AMQ thread and does not move mail between boxes.
	threadContextResult *threadContextResultOverlay
	// amqOpsResult is a read-only result overlay for AMQ doctor --ops output.
	amqOpsResult *amqOpsResultOverlay
	// amqWhoResult is a read-only result overlay for AMQ who project inventory.
	amqWhoResult *amqWhoResultOverlay
	// amqEnvResult is a read-only result overlay for AMQ env project routing.
	amqEnvResult *amqEnvResultOverlay
	// presenceResult is a read-only result overlay for AMQ presence list output.
	presenceResult *presenceResultOverlay
	// roleMarket is a read-only overlay for the built-in role catalog, opened
	// from project/action/roles so team creation stays inside the NOC.
	roleMarket *roleMarketOverlay
	// teamProfiles is a read-only overlay for the configured profiles on a
	// team-home, opened from project/action/team-profiles.
	teamProfiles *teamProfilesOverlay
	// projectDoctorResult is a read-only result overlay for project doctor
	// output, opened from project/action/doctor.
	projectDoctorResult *projectDoctorResultOverlay
	// projectHistoryResult is a read-only result overlay for project launch
	// history, opened from project/action/history.
	projectHistoryResult *projectHistoryResultOverlay
	// teamRulesResult is a read-only result overlay for durable team norms,
	// opened from project/action/team-rules.
	teamRulesResult *teamRulesResultOverlay
	// projectResumePlanResult is a read-only result overlay for project recovery
	// planning, opened from project/action/resume-plan.
	projectResumePlanResult *projectResumePlanResultOverlay
	// forkPlanResult is a read-only result overlay for branching a workstream,
	// opened from session/action/fork-plan.
	forkPlanResult *forkPlanResultOverlay
	// briefResult is a read-only result overlay for a workstream brief, opened
	// from session/action/brief.
	briefResult *briefResultOverlay
	// statusResult is a read-only result overlay for project/session status
	// output, opened from status palette rows.
	statusResult *statusResultOverlay
	// threadsResult is a read-only result overlay for session AMQ thread
	// summaries, opened from session/action/threads.
	threadsResult *threadsResultOverlay

	// input is the (non-mutating) body/subject editor for reply/message/broadcast/deny:
	// the operator types here, then the action transitions to the pending overlay
	// so the EXACT command is previewed before any confirm.
	input *inputAction

	// actNote surfaces the result/decline of the last control action, mirroring
	// jumpNote for the read-only jump.
	actNote string

	// jumpPending is the confirm overlay for a READ-ONLY focus action (jump on a
	// running agent, J, or o/focus-team). Non-nil means a focus has been previewed
	// and is awaiting an explicit y/Y/enter; any other key (esc included) cancels
	// with zero effect. It mirrors pending (the mutating confirm overlay) but its
	// only effect is terminal focus — never a squad mutation, never a spawn. It is
	// a SEPARATE field from pending so the read-only focus guard and the mutating
	// control guard never alias each other.
	jumpPending *pendingFocus

	// refreshNote is a brief visible flash ("refreshed (just now)") set ONLY by an
	// explicit g refresh so the operator sees g worked (the silent 2s ticker never
	// sets it). It clears on the next keypress like jumpNote/actNote/alertBanner.
	refreshNote string

	// --- Awareness layer (PR18 / 2.3). The command palette is a locator and
	// launcher: jump/focus selections perform only the existing gated tmux
	// movement, while creation selections open the same preview-gated T/N flows.
	// Needs-you alerts only ring a bell + set a banner.

	// palette is the command-palette overlay: non-nil means the fuzzy
	// project/action/team/agent overlay is open. Selecting a creation action opens
	// an input editor; the backend seam is still reached only after confirm.
	palette *paletteState

	// priorNeedsYou is the per-session needs-you count from the LAST snapshot,
	// keyed by sessionAlertKey(projectDir, session). It is the transition-only
	// bookkeeping for needs-you alerts: a session alerts only when its count goes
	// 0 → >0 between snapshots (not on every 2s refresh while it stays needs-you).
	priorNeedsYou map[string]int

	// priorSeeded is false until the FIRST snapshot has established the needs-you
	// baseline. The first snapshot never alerts (it only records state) so a
	// freshly-opened NOC over an already-needs-you board neither rings a bell for
	// pre-existing state nor shifts the frame layout on frame zero.
	priorSeeded bool

	// alertBanner is the visible needs-you alert ("🔔 <project>/<session> needs
	// you") set on a 0→N transition; cleared on the next navigation like jumpNote.
	alertBanner string

	// alertsMuted suppresses BOTH the bell and the banner. Set by --no-bell at
	// startup and toggled interactively by 'A'. Default false (alerts ON).
	alertsMuted bool

	// Seams.
	switchTo nocSwitcher
	panes    nocPaneLister
	pidTree  func(pid int) []int
	copyText func(string) error
	// runtimeFetch consumes amq-squad's runtime contract (status --json
	// actions[]/pane_alive) for a session, so the command picker offers the
	// exact, runnable control commands amq-squad advertises. Total: a zero
	// RuntimeStatus on any failure, so the picker degrades to static commands.
	runtimeFetch func(dir, profile, session string) noc.RuntimeStatus
	// paneCapture reads the tail of a live tmux pane for the read-only
	// view-output affordance (#21); defaults to noc.CapturePaneTail. The pane
	// id always comes from the published runtime contract, never scraping.
	paneCapture func(paneID string, lines int) ([]string, error)
	// agentOutput is the v-key output preview for the selected agent row.
	agentOutput *agentOutputView
	// transcript reads the operator-lead conversation (#27); defaults to
	// state.ConversationTranscript. Read-only filesystem scan.
	transcript func(sessionRoot, a, b string, limit int) []state.Message
	// conversation is the active conversation-mode state (nil = board).
	conversation *conversationView

	// notify is the opt-in desktop-notification seam (#28): fired on the same
	// needs-you 0->N transition as the bell, independently of the bell mute.
	// nil (the default) means notifications are off.
	notify func(title, body string)
	// bell is the injected terminal-bell seam for needs-you alerts. Production
	// writes "\a" to the tty (wired by RunNOC); tests inject a counter so they can
	// assert the bell fired exactly once on a 0→N transition without a real tty. A
	// nil bell degrades to "banner only" (no panic).
	bell func()

	// Control seams (PR15). Both are MUTATING and are reached ONLY after the
	// operator confirms the preview overlay; tests swap them for fakes so no
	// `make ci` run ever writes a real squad. sendOp writes an OpMessage into
	// AMQ (defaults to act.Send when nil). lifecycle stops/resumes/restarts a squad,
	// newSession starts a detached workstream, newTeam creates a team profile,
	// teamDelete removes a team profile config, sessionCleanup archives/removes
	// one workstream, and readNeedsYou runs AMQ
	// read for the operator mailbox, drainAgent drains an agent mailbox, and
	// agentResume starts one saved launch record. These are injected from cli so
	// there is no console import cycle.
	sendOp            func(act.OpMessage) error
	lifecycle         func(lifecycleOp) error
	sendPrompt        func(sendPromptOp) error
	agentResume       func(agentResumeOp) error
	sessionCleanup    func(sessionCleanupOp) error
	newSession        func(newSessionOp) error
	newTeam           func(newTeamOp) error
	teamDelete        func(teamDeleteOp) error
	pointerSync       func(pointerSyncOp) error
	readNeedsYou      func(readNeedsYouOp) (readNeedsYouResult, error)
	drainAgent        func(drainAgentOp) (drainAgentResult, error)
	inboxAgent        func(inboxAgentOp) (inboxAgentResult, error)
	dlqAgent          func(dlqAgentOp) (dlqAgentResult, error)
	dlqRead           func(dlqReadOp) (dlqReadResult, error)
	dlqRetry          func(dlqRetryOp) (dlqRetryResult, error)
	dlqPurge          func(dlqPurgeOp) (dlqPurgeResult, error)
	dlqRetryAll       func(dlqRetryAllOp) (dlqRetryAllResult, error)
	receiptsAgent     func(receiptsAgentOp) (receiptsAgentResult, error)
	receiptsWait      func(receiptsWaitOp) (receiptsWaitResult, error)
	messageWait       func(messageWaitOp) (messageWaitResult, error)
	amqCleanup        func(amqCleanupOp) (amqCleanupResult, error)
	threadContext     func(threadContextOp) (threadContextResult, error)
	amqOps            func(amqOpsOp) (amqOpsResult, error)
	amqWho            func(amqWhoOp) (amqWhoResult, error)
	amqEnv            func(amqEnvOp) (amqEnvResult, error)
	presence          func(presenceOp) (presenceResult, error)
	projectDoctor     func(projectDoctorOp) (projectDoctorResult, error)
	projectHistory    func(projectHistoryOp) (projectHistoryResult, error)
	teamRules         func(teamRulesOp) (teamRulesResult, error)
	projectResumePlan func(projectResumePlanOp) (projectResumePlanResult, error)
	forkPlan          func(forkPlanOp) (forkPlanResult, error)
	brief             func(briefOp) (briefResult, error)
	briefSeed         func(briefSeedOp) error
	status            func(statusOp) (statusResult, error)
	threads           func(threadsOp) (threadsResult, error)
}

// newNOCModel builds a model from an immutable rebuild config. The control
// AMQ-write seam defaults to act.Send; the lifecycle seam stays nil here and is
// wired by RunNOC from cli (lifecycle verbs) so a nil lifecycle degrades to a
// note rather than panicking, and unit tests can leave it nil or inject a fake.
func newNOCModel(rebuild NOCRebuildConfig) NOCModel {
	mode := resolveColorMode(true)
	return NOCModel{
		rebuild:      rebuild,
		tree:         newNOCTreeState(),
		colorMode:    mode,
		th:           newNOCTheme(mode),
		switchTo:     noc.SwitchTo,
		panes:        noc.DefaultPaneLister,
		pidTree:      defaultPidTree,
		copyText:     defaultClipboardCopy,
		runtimeFetch: defaultRuntimeFetch,
		paneCapture:  noc.CapturePaneTail,
		transcript:   state.ConversationTranscript,
		sendOp:       act.Send,
	}
}

// Init implements tea.Model: start the refresh + rediscover tickers. Pointer
// receiver to match Update / View: the program is driven as *NOCModel.
func (m *NOCModel) Init() tea.Cmd {
	return tea.Batch(nocTickCmd(m.rebuild.refreshCadence()), nocRediscoverTickCmd())
}

// nocSnapshotMsg delivers a freshly collected MultiSnapshot.
type nocSnapshotMsg struct {
	ms noc.MultiSnapshot
}

// nocTickMsg is the periodic refresh signal.
type nocTickMsg struct{}

// nocRediscoverMsg is the periodic re-discovery signal.
type nocRediscoverMsg struct{}

// nocWatchMsg signals a filesystem change under a watched .agent-mail tree.
type nocWatchMsg struct{}

// nodes returns the flattened visible tree for the current snapshot + state,
// honoring the hide-stale toggle.
func (m NOCModel) nodes() []nocNode {
	return buildNOCTree(m.ms, m.tree, m.filter, m.hideStale, !m.fullTree)
}

// selectedNode returns the node at the cursor.
func (m NOCModel) selectedNode() (nocNode, bool) {
	ns := m.nodes()
	if m.cursor < 0 || m.cursor >= len(ns) {
		return nocNode{}, false
	}
	return ns[m.cursor], true
}

// clampCursor keeps the cursor inside the visible node bounds.
func (m *NOCModel) clampCursor() {
	ns := m.nodes()
	if m.cursor >= len(ns) {
		m.cursor = len(ns) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.ensureCursorVisibleFor(ns)
}

// ensureCursorVisibleFor keeps the tree window anchored around the selected row.
func (m *NOCModel) ensureCursorVisibleFor(ns []nocNode) {
	rows := m.bodyHeight()
	if len(ns) == 0 || rows <= 0 || rows >= len(ns) {
		m.scroll = 0
		return
	}
	maxScroll := len(ns) - rows
	if m.scroll < 0 {
		m.scroll = 0
	}
	if m.scroll > maxScroll {
		m.scroll = maxScroll
	}
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	}
	if m.cursor >= m.scroll+rows {
		m.scroll = m.cursor - rows + 1
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
	if m.scroll > maxScroll {
		m.scroll = maxScroll
	}
}

// rememberSelection records the id at the cursor for cross-snapshot stability.
func (m *NOCModel) rememberSelection() {
	if n, ok := m.selectedNode(); ok {
		m.selectedID = n.id
	}
}

// preserveSelection re-locates the previously selected id after a snapshot or
// collapse change so a refresh never yanks the selection.
func (m *NOCModel) preserveSelection() {
	ns := m.nodes()
	if m.selectedID == "" {
		m.ensureCursorVisibleFor(ns)
		return
	}
	for i, n := range ns {
		if n.id == m.selectedID {
			m.cursor = i
			m.ensureCursorVisibleFor(ns)
			return
		}
	}
	// The selected node vanished (collapsed parent / removed project): clamp.
	m.clampCursor()
}

// hasProjects reports whether the snapshot found any project.
func (m NOCModel) hasProjects() bool {
	return len(m.ms.Projects) > 0
}

// Compile-time assurance *NOCModel satisfies the Bubble Tea interface. The
// program is driven as a POINTER (tea.NewProgram(&m)) so a key handler's cursor
// / collapse / filter mutation lands on the model the event loop renders next.
var _ tea.Model = (*NOCModel)(nil)
