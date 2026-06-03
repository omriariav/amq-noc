// Package console implements the read-only Mission Control TUI for amq-squad.
//
// SCAFFOLD PHASE: this package stands up the Bubble Tea root Model, the
// fsnotify-backed watcher, the snapshot-rebuild update flow, and the
// non-interactive (--once) static board. The INTERACTIVE VIEWS (the routed
// session/thread/timeline panes and key handling beyond quit) arrive in the
// next phase.
//
// Architectural invariants this package upholds:
//
//   - internal/state stays stdlib-only. This package is the ONLY place that
//     imports bubbletea / bubbles / lipgloss / fsnotify; the read-only state
//     model never learns about the TUI.
//
//   - The Model holds the *state.Snapshot as an IMMUTABLE value. No view reads
//     the filesystem; no goroutine mutates the Model. The data flow is strictly
//     one-directional: a filesystem event -> a debounced rebuild Cmd -> a goroutine
//     runs state.Build -> a tea.Msg carries a FRESH snapshot back -> Update
//     replaces the held snapshot -> the next render reads only that value.
//
//   - The full-screen program renders to /dev/tty, never os.Stdout, so the
//     stdout-purity contract the other verbs' tests assert stays intact. The
//     --once path is the sole exception: it writes ONE static board to the
//     supplied writer (stdout) and never starts a tea.Program.
package console

import (
	"time"

	"github.com/charmbracelet/bubbles/viewport"

	"github.com/omriariav/amq-noc/internal/state"
)

// route is the top-level pane the console is focused on. The scaffold only ever
// sits on routeBoard; the session/thread/timeline routes are reserved for the
// interactive-views phase so the type is stable across PRs.
type route int

const (
	// routeBoard: the multi-session board (the landing pane).
	routeBoard route = iota
	// routeSession: a single session's agents + coordination detail (next phase).
	routeSession
	// routeThread: a single thread's message lineage (next phase).
	routeThread
	// routeTimeline: the derived cross-session timeline (next phase).
	routeTimeline
)

// overlay is a modal layer drawn ON TOP of the active route. Overlays are
// strictly READ-ONLY: peek shows recent output / unread / block reason, attach
// shows the suggested (inert) jump command, help shows the keymap. None of them
// mutate state, send a message, or start/stop a process.
type overlay int

const (
	// overlayNone: no modal layer; the route renders normally.
	overlayNone overlay = iota
	// overlayPeek: the read-only peek pane for the selected agent/thread.
	overlayPeek
	// overlayAttach: the inert attach overlay showing the suggested command.
	overlayAttach
	// overlayHelp: the keymap help.
	overlayHelp
)

// Filter narrows what the views surface. It is a PURE predicate over the
// snapshot, applied in every view; an empty Filter matches everything. Triage /
// Session stay exported for callers (e.g. the CLI's --session flag) that want to
// preset a scope; the typed-filter machinery lives in the unexported fields and
// is parsed by parseFilter.
type Filter struct {
	// Triage, when non-empty, limits views to threads/sessions at this tier.
	Triage state.Triage
	// Session, when non-empty, scopes views to a single session name.
	Session string

	// Raw is the typed filter expression exactly as entered (for display).
	Raw string
	// kind is the parsed predicate family.
	kind filterKind
	// arg is the operand for agent:/model:/session: filters.
	arg string
	// unknown is set when Raw was non-empty but not a recognized expression.
	unknown bool
}

// Model is the Bubble Tea root model for Mission Control. It is a value type:
// Bubble Tea calls Update with a copy and expects the next Model back, so every
// field must be safe to copy. The held snapshot is an immutable value handed in
// by the rebuild goroutine; the Model never scans the filesystem itself.
type Model struct {
	// snapshot is the current immutable read-only view. It is REPLACED wholesale
	// on every snapshotMsg; it is never mutated in place.
	snapshot state.Snapshot

	// route/focus/filter drive which view renders.
	route  route
	focus  int    // index of the focused row within the active route's row set
	filter Filter // pure predicate applied in every view

	// selectedID is the STABLE identity of the focused row, preserved across
	// snapshot replacement so a refresh never jumps the cursor. focus is a
	// derived index recomputed from selectedID against the live row set on every
	// render; selectedID is the source of truth, focus the cache.
	selectedID string

	// session is the name of the session the detail view is drilled into. Empty
	// on the board. It is a STABLE key (the session name), so a snapshot replace
	// re-finds the same session.
	session string

	// overlay is the active modal layer (peek/attach/help) drawn over the route,
	// or overlayNone. Overlays are READ-ONLY: they never mutate state, send a
	// message, or start/stop a process.
	overlay overlay

	// attachHint holds the suggested (INERT) attach command computed when the
	// user pressed `a`. v0 NEVER attaches; it only shows the command to run.
	attachHint string

	// filtering is true while the `/` filter input line is open; runes are
	// appended to filterInput until enter applies or esc cancels.
	filtering   bool
	filterInput string

	// timeline toggles the detail view's TIMELINE pane (state transitions) in
	// place of the bus/edge panes. `t` flips it.
	timeline bool

	// viewport scrolls the rendered board when it overflows the terminal.
	viewport viewport.Model
	ready    bool // viewport has received its first WindowSizeMsg

	// width/height track the last known terminal size for layout math.
	width  int
	height int

	// err carries the most recent non-fatal error (a failed rebuild, a watch
	// error). It is rendered as a status line, never panicked on.
	err error

	// quitting is set once the user asks to leave so View can render a final
	// frame deterministically before tea.Quit takes effect.
	quitting bool

	// rebuild captures everything the debounced rebuild Cmd needs to produce a
	// fresh snapshot off the UI goroutine. It is config, not mutable state.
	rebuild rebuildConfig

	// noTeam marks the degraded "no team / unresolvable root" failure-state
	// screen: the console explains rather than aborting. The accompanying
	// message is held in noTeamNotice.
	noTeam       bool
	noTeamNotice string
}

// rebuildConfig is the immutable set of inputs a rebuild goroutine needs to call
// state.Build. It is copied into the Model and never mutated, so handing it to a
// goroutine carries no shared state back into the UI loop.
type rebuildConfig struct {
	ProjectDir string
	BaseRoot   string
	Probe      state.Probe
	Thresholds state.Thresholds
}

// newModel builds the initial Model from a resolved config. When baseRoot is
// empty (unresolvable) or noTeamNotice is set, the Model boots into the
// degraded NoTeam failure-state screen instead of attempting a rebuild.
func newModel(cfg rebuildConfig, initial state.Snapshot, noTeamNotice string) Model {
	m := Model{
		snapshot:     initial,
		rebuild:      cfg,
		noTeamNotice: noTeamNotice,
		noTeam:       noTeamNotice != "",
	}
	return m
}

// now is the render-layer clock: the SAME probe clock state.Build used, so the
// interactive views age agent/thread signals deterministically in tests and
// honestly in production. A nil probe clock falls back to the real wall-clock.
func (m Model) now() func() time.Time { return clockOrDefault(m.rebuild.Probe) }

// Snapshot exposes the currently-held immutable snapshot. Tests use it to assert
// the Update flow replaced the snapshot; production views read it for rendering.
func (m Model) Snapshot() state.Snapshot { return m.snapshot }

// Route exposes the active route (test/observability seam).
func (m Model) Route() route { return m.route }

// Err exposes the last non-fatal error (test/observability seam).
func (m Model) Err() error { return m.err }

// IsQuitting reports whether the model has begun quitting.
func (m Model) IsQuitting() bool { return m.quitting }

// Overlay exposes the active modal layer (test/observability seam).
func (m Model) Overlay() overlay { return m.overlay }

// Selected exposes the stable id of the focused row (test/observability seam).
func (m Model) Selected() string { return m.selectedID }

// SessionName exposes the drilled-into session name, empty on the board.
func (m Model) SessionName() string { return m.session }

// Filtering reports whether the `/` filter input line is open.
func (m Model) Filtering() bool { return m.filtering }

// FilterInput exposes the in-progress filter text while typing.
func (m Model) FilterInput() string { return m.filterInput }

// ActiveFilter exposes the applied filter (test/observability seam).
func (m Model) ActiveFilter() Filter { return m.filter }

// AttachHint exposes the inert suggested-attach command computed by `a`.
func (m Model) AttachHint() string { return m.attachHint }

// TimelineOn reports whether the detail timeline pane is toggled on.
func (m Model) TimelineOn() bool { return m.timeline }

// snapshotMsg carries a freshly-built immutable snapshot from a rebuild
// goroutine back into the UI loop. buildErr is non-nil when state.Build failed;
// the Model surfaces it as a status line and keeps the prior snapshot.
type snapshotMsg struct {
	snapshot state.Snapshot
	buildErr error
}

// tickMsg is the periodic ticker fallback: even with no filesystem event, the
// console rebuilds on this cadence so ages advance and missed events self-heal.
type tickMsg struct {
	at time.Time
}

// watchErrMsg reports a watcher-layer error. Per the watcher contract every
// watch error is treated as a reason to RESYNC (a full rebuild), never to crash.
type watchErrMsg struct {
	err error
}
