package console

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-noc/internal/state"
)

// Init kicks off the console: an immediate rebuild (so the first frame reflects
// disk, not just the seeded snapshot) and the periodic ticker. The fsnotify
// watcher is started by the runner via a long-lived listener command; Init
// returns the commands that are safe to issue before the program has a tty.
func (m Model) Init() tea.Cmd {
	if m.noTeam {
		// Degraded screen: nothing to rebuild or tick. The view explains itself.
		return nil
	}
	return tea.Batch(
		rebuildCmd(m.rebuild, false),
		tickCmd(m.rebuild.Thresholds),
	)
}

// Update is the single, side-effect-free-on-the-Model reducer. It NEVER scans
// the filesystem and NEVER mutates shared state: a fresh immutable snapshot
// arrives as a snapshotMsg and is swapped in wholesale. Returning a new Model by
// value is the only way state changes.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if !m.ready {
			m.viewport = newViewport(msg.Width, viewportHeight(msg.Height))
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = viewportHeight(msg.Height)
		}
		m.viewport.SetContent(m.renderBody())
		return m, nil

	case snapshotMsg:
		// Replace the held snapshot wholesale with the fresh immutable value. On
		// a build error, keep the prior snapshot and surface the error as a status
		// line — a transient scan failure must not blank the board.
		if msg.buildErr != nil {
			m.err = msg.buildErr
		} else {
			m.snapshot = msg.snapshot
			m.err = nil
		}
		// SELECTION STABILITY: re-resolve the cursor by stable id against the
		// fresh rows so a refresh never jumps the selection or resets scroll.
		m = m.reselect()
		if m.ready {
			m.viewport.SetContent(m.renderBody())
		}
		return m, nil

	case tickMsg:
		// Periodic resync fallback: rebuild even with no fs event, then re-arm.
		return m, tea.Batch(
			rebuildCmd(m.rebuild, false),
			tickCmd(m.rebuild.Thresholds),
		)

	case watchErrMsg:
		// EVERY watch error is a resync, never a crash. Record it and rebuild.
		m.err = msg.err
		return m, rebuildCmd(m.rebuild, true)

	default:
		return m, nil
	}
}

// handleKey processes keyboard input. It is the keymap's single dispatch point.
//
// HARD invariant (the reviewer's requirement): this function is READ-ONLY. No
// key mutates the snapshot, sends a message, or starts/stops a process. The only
// state a key changes is NAVIGATION/VIEW state on the Model (route, focus,
// filter, overlay) plus the resync-now command (which re-READS disk, never
// writes). `enter` NEVER doubles as peek or attach.
//
// The keymap:
//
//	space  peek (read-only overlay)         l  logs/tail mode
//	enter  expand/drill (board->detail,     a  attach (INERT: shows the command)
//	       thread->expand)                  t  timeline pane toggle (detail)
//	/      filter entry                     esc back / close overlay / cancel
//	j/k/↑/↓ move      g  refresh-now (resync)   q  quit      ?  help
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// While the filter input line is open, keystrokes edit the query — they do
	// NOT trigger navigation. enter applies, esc cancels.
	if m.filtering {
		return m.handleFilterKey(msg)
	}

	key := msg.String()

	// ctrl+c always quits, regardless of overlay. (esc is back/close, not quit,
	// once we have routes/overlays to step out of.)
	if key == "ctrl+c" {
		m.quitting = true
		return m, tea.Quit
	}

	// An open overlay captures esc (close it) and ignores navigation until closed.
	if m.overlay != overlayNone {
		switch key {
		case "esc", "q":
			m.overlay = overlayNone
			m.attachHint = ""
			return m.afterNav(), nil
		default:
			// Overlays are read-only and modal: swallow other keys.
			return m, nil
		}
	}

	switch key {
	case "q":
		m.quitting = true
		return m, tea.Quit

	case "?":
		m.overlay = overlayHelp
		return m, nil

	case "esc":
		// Back out one level: detail -> board. On the board, esc is a no-op (q
		// quits). Never mutates data.
		if m.route != routeBoard {
			m.route = routeBoard
			m.timeline = false
			// Restore the selection to the session row we drilled from.
			m.selectedID = m.session
			m.session = ""
			return m.afterNav(), nil
		}
		return m, nil

	case "j", "down":
		m = m.moveFocus(1)
		return m.afterNav(), nil

	case "k", "up":
		m = m.moveFocus(-1)
		return m.afterNav(), nil

	case "enter":
		// enter = expand/drill ONLY. board: drill the selected session into the
		// detail view. detail: expand the selected thread (peek-less expand). It
		// NEVER peeks and NEVER attaches.
		return m.handleDrill(), nil

	case " ", "space":
		// space = peek: a read-only overlay for the selected agent/thread.
		m.overlay = overlayPeek
		return m, nil

	case "l":
		// l = logs/tail mode: raw chronological messages (clearly secondary).
		if m.route == routeSession {
			m.route = routeThread // routeThread is the log/tail surface in v0
			return m.afterNav(), nil
		}
		return m, nil

	case "a":
		// a = attach: INERT in v0. Compute and show the suggested jump command;
		// NEVER actually attach or mutate anything.
		m.attachHint = m.suggestAttach()
		m.overlay = overlayAttach
		return m, nil

	case "t":
		// t = timeline pane toggle (detail view only).
		if m.route == routeSession {
			m.timeline = !m.timeline
			if m.ready {
				m.viewport.SetContent(m.renderBody())
			}
		}
		return m, nil

	case "/":
		// / = filter entry: open the input line. Subsequent keys edit the query.
		m.filtering = true
		m.filterInput = m.filter.Raw
		return m, nil

	case "g":
		// g = refresh-now: force a full resync. This re-READS disk; it never
		// writes, sends, or stops anything.
		return m, rebuildCmd(m.rebuild, true)

	default:
		// Unbound key: let the viewport scroll (pgup/pgdn/etc). Pure view state.
		if m.ready {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
		return m, nil
	}
}

// handleFilterKey edits the `/` filter query. enter applies the parsed filter and
// re-resolves the selection; esc cancels and restores the prior filter; backspace
// deletes; printable runes append. No key here touches disk or state.
func (m Model) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.filter = parseFilter(m.filterInput)
		m.filtering = false
		m.filterInput = ""
		return m.afterNav(), nil
	case tea.KeyEsc:
		m.filtering = false
		m.filterInput = ""
		return m, nil
	case tea.KeyBackspace, tea.KeyDelete:
		if n := len(m.filterInput); n > 0 {
			m.filterInput = m.filterInput[:n-1]
		}
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		// KeySpace already carries a space rune in Runes; only synthesize one when
		// the message arrives without runes, so a space is never duplicated.
		if len(msg.Runes) > 0 {
			m.filterInput += string(msg.Runes)
		} else if msg.Type == tea.KeySpace {
			m.filterInput += " "
		}
		return m, nil
	default:
		return m, nil
	}
}

// handleDrill implements enter: board -> detail (drill the selected session),
// detail -> nothing structural beyond keeping focus (thread expand is the peek
// overlay's job via space; enter on a thread keeps it selected/expanded in the
// bus). It only changes navigation state.
func (m Model) handleDrill() Model {
	sel, ok := m.selectedRow()
	if !ok {
		return m
	}
	switch m.route {
	case routeBoard:
		if sel.kind == rowSession {
			m.session = sel.ID
			m.route = routeSession
			m.timeline = false
			// Land the cursor on the first row of the detail view.
			m.selectedID = ""
			m.focus = 0
			m = m.reselect()
			if m.ready {
				m.viewport.SetContent(m.renderBody())
			}
		}
	default:
		// In detail, enter on a thread keeps it selected (the bus shows it
		// expanded); enter on an agent is a no-op beyond selection. No drill
		// deeper than detail in v0 — the peek overlay (space) is the read path.
	}
	return m
}

// afterNav recomputes selection stability and refreshes the viewport content
// after a navigation change, so the highlighted row and the scroll position
// stay coherent. It performs NO I/O.
func (m Model) afterNav() Model {
	m = m.reselect()
	if m.ready {
		m.viewport.SetContent(m.renderBody())
	}
	return m
}

// rebuildCmd returns a tea.Cmd that runs state.Build OFF the UI goroutine and
// delivers the result as a snapshotMsg. The config is captured by value, so the
// goroutine shares no mutable state with the Model. resync is accepted so the
// watcher path can request a full re-walk; at the state layer a build IS a full
// re-walk, so the flag is currently informational (it lets the next phase add a
// cheaper incremental path without changing the watcher contract).
func rebuildCmd(cfg rebuildConfig, _ bool) tea.Cmd {
	return func() tea.Msg {
		snap, err := state.BuildWithThresholds(cfg.ProjectDir, cfg.BaseRoot, cfg.Probe, cfg.Thresholds)
		return snapshotMsg{snapshot: snap, buildErr: err}
	}
}

// tickCmd schedules the next periodic resync. The cadence comes from the
// configured refresh; thresholds carry it indirectly via the runner, so we read
// the package default here and let the runner override the timer at the program
// level. Kept as a tea.Tick so it composes with the Bubble Tea scheduler.
func tickCmd(_ state.Thresholds) tea.Cmd {
	return tea.Tick(DefaultRefresh, func(t time.Time) tea.Msg {
		return tickMsg{at: t}
	})
}
