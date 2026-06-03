// Package console — noc_theme.go: the NOC ("command center") color + glyph
// system. It is a self-contained, refined-industrial "mission control" palette
// resolved from the same ColorMode the session console uses, so a NOC surface
// honors NO_COLOR / dumb-terminal degradation identically.
//
// Design law: COLOR IS THE LAST LAYER. A TEXT label for every state is always
// present; glyph and color are secondary decoration that fall away on
// no-color / no-unicode terminals. The single eye-grab is needs-you (hot
// magenta + bold); everything else is calm at rest (amber chrome, green alive,
// amber at-risk, red blocked, dim grey stopped/idle).
package console

import (
	"io"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/state"
)

// nocGlyph is a unicode/ascii pair for one marker; the ascii form is the
// dumb-terminal fallback and is rendered verbatim with no color.
type nocGlyph struct {
	unicode string
	ascii   string
}

// State markers. The TEXT label (see nocStateText) is always shown alongside;
// these glyphs are decoration only.
var (
	nocGlyphRunning  = nocGlyph{unicode: "●", ascii: "[run]"}
	nocGlyphDegraded = nocGlyph{unicode: "◐", ascii: "[deg]"}
	nocGlyphStopped  = nocGlyph{unicode: "○", ascii: "[stop]"}
	nocGlyphNeedsYou = nocGlyph{unicode: "⚠", ascii: "[!]"}
	nocGlyphBlocked  = nocGlyph{unicode: "✕", ascii: "[x]"}
	nocGlyphGated    = nocGlyph{unicode: "◆", ascii: "[gate]"}
	nocGlyphWaiting  = nocGlyph{unicode: "◌", ascii: "[wait]"}
	nocGlyphStale    = nocGlyph{unicode: "×", ascii: "[old]"}

	// Tree drawing glyphs degrade to ascii box-art.
	nocGlyphExpanded  = nocGlyph{unicode: "▾", ascii: "-"}
	nocGlyphCollapsed = nocGlyph{unicode: "▸", ascii: "+"}
	nocGlyphSelect    = nocGlyph{unicode: "►", ascii: ">"}
	nocGlyphJump      = nocGlyph{unicode: "⏎ jump", ascii: "[jump]"}

	// NEEDS YOU reason glyphs. The TEXT label (APPROVE / GOAL-REACHED) always
	// accompanies these so they survive NO_COLOR; goal-reached deliberately is
	// NOT a bare green check — it carries its own glyph + label inside NEEDS YOU.
	nocGlyphApprove = nocGlyph{unicode: "⏸", ascii: "[approve]"}
	nocGlyphGoal    = nocGlyph{unicode: "✓", ascii: "[goal]"}
)

// glyph returns the active form for a mode (unicode for full/none, ascii for
// dumb terminals).
func (g nocGlyph) glyph(mode ColorMode) string {
	if mode == ColorAscii {
		return g.ascii
	}
	return g.unicode
}

// nocTheme holds resolved lipgloss styles for one ColorMode. Built once at model
// construction. In ColorNone/ColorAscii every style is the identity (no escape
// codes are emitted) so output is plain text the --once / NO_COLOR tests assert.
type nocTheme struct {
	mode ColorMode

	brand    lipgloss.Style // amber/gold brand text (header)
	rule     lipgloss.Style // amber header rule
	dim      lipgloss.Style // dim grey chrome (ages, recent action, idle)
	selBar   lipgloss.Style // the amber selection bar (► + subtle bg)
	needsYou lipgloss.Style // HOT magenta + bold — the single eye-grab
	atRisk   lipgloss.Style // amber/degraded
	blocked  lipgloss.Style // red
	running  lipgloss.Style // green alive
	stopped  lipgloss.Style // dim grey stopped
	review   lipgloss.Style // cyan — GOAL-REACHED "review and close" accent
}

// newNOCTheme builds the styles for a mode.
//
// ColorFull uses a dedicated lipgloss renderer pinned to a true-color profile so
// the NOC surface emits ANSI deterministically once we have DECIDED to color
// (the decision already honored NO_COLOR / TTY / dumb-terminal in
// resolveColorMode). Pinning avoids lipgloss's own renderer re-detecting a
// non-TTY (e.g. under `go test`) and silently dropping the color we asked for.
func newNOCTheme(mode ColorMode) nocTheme {
	t := nocTheme{mode: mode}
	if mode != ColorFull {
		// No color: every style is the zero lipgloss.Style (identity render).
		return t
	}

	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.TrueColor)
	r.SetHasDarkBackground(true)

	amber := lipgloss.AdaptiveColor{Light: "#B8860B", Dark: "#FFB000"}
	green := lipgloss.AdaptiveColor{Light: "#2E7D32", Dark: "#5FD75F"}
	amberWarn := lipgloss.AdaptiveColor{Light: "#C77800", Dark: "#FFAF00"}
	magenta := lipgloss.AdaptiveColor{Light: "#A2007A", Dark: "#FF5FFF"}
	red := lipgloss.AdaptiveColor{Light: "#C62828", Dark: "#FF5F5F"}
	grey := lipgloss.AdaptiveColor{Light: "#9E9E9E", Dark: "#6C6C6C"}
	cyan := lipgloss.AdaptiveColor{Light: "#00838F", Dark: "#5FD7D7"}
	selBG := lipgloss.AdaptiveColor{Light: "#FFF3D6", Dark: "#3A2E12"}

	t.brand = r.NewStyle().Bold(true).Foreground(amber)
	t.rule = r.NewStyle().Foreground(amber)
	t.dim = r.NewStyle().Foreground(grey)
	t.selBar = r.NewStyle().Bold(true).Foreground(amber).Background(selBG)
	t.needsYou = r.NewStyle().Bold(true).Foreground(magenta)
	t.atRisk = r.NewStyle().Foreground(amberWarn)
	t.blocked = r.NewStyle().Foreground(red)
	t.running = r.NewStyle().Foreground(green)
	t.stopped = r.NewStyle().Foreground(grey)
	t.review = r.NewStyle().Foreground(cyan)
	return t
}

// paint applies a style only in ColorFull mode; otherwise returns s untouched so
// no escape codes ever reach a NO_COLOR / dumb terminal or a non-TTY pipe.
func (t nocTheme) paint(style lipgloss.Style, s string) string {
	if t.mode != ColorFull {
		return s
	}
	return style.Render(s)
}

// nocState is the rolled-up display state of a tree node, in attention order.
type nocState int

const (
	nocNeedsYou     nocState = iota // hot - operator action required
	nocBlocked                      // red - blocked
	nocGated                        // cyan - intentionally gated
	nocAtRisk                       // amber - at-risk / degraded
	nocWaiting                      // live but no actionable work
	nocRunning                      // green - at least one live agent
	nocStaleBlocked                 // stopped with stale blocked history
	nocStopped                      // dim - discovered but nothing live
	nocEmpty                        // dim - scaffolding / no agents
)

// nocStateText is the ALWAYS-PRESENT text label for a state. Glyph + color are
// layered on top of this; this text alone is sufficient on a dumb terminal.
func nocStateText(s nocState) string {
	switch s {
	case nocNeedsYou:
		return "needs-you"
	case nocBlocked:
		return "blocked"
	case nocGated:
		return "gated"
	case nocAtRisk:
		return "at-risk"
	case nocWaiting:
		return "waiting"
	case nocRunning:
		return "running"
	case nocStaleBlocked:
		return "stale"
	case nocStopped:
		return "stopped"
	default:
		return "idle"
	}
}

// nocStateGlyph returns the marker glyph for a state.
func nocStateGlyph(s nocState, mode ColorMode) string {
	switch s {
	case nocNeedsYou:
		return nocGlyphNeedsYou.glyph(mode)
	case nocBlocked:
		return nocGlyphBlocked.glyph(mode)
	case nocGated:
		return nocGlyphGated.glyph(mode)
	case nocAtRisk:
		return nocGlyphDegraded.glyph(mode)
	case nocWaiting:
		return nocGlyphWaiting.glyph(mode)
	case nocRunning:
		return nocGlyphRunning.glyph(mode)
	case nocStaleBlocked:
		return nocGlyphStale.glyph(mode)
	default:
		return nocGlyphStopped.glyph(mode)
	}
}

// nocStateStyle returns the lipgloss style for a state. Calm at rest; only
// needs-you is the hot eye-grab.
func (t nocTheme) nocStateStyle(s nocState) lipgloss.Style {
	switch s {
	case nocNeedsYou:
		return t.needsYou
	case nocBlocked:
		return t.blocked
	case nocGated:
		return t.review
	case nocAtRisk:
		return t.atRisk
	case nocWaiting:
		return t.dim
	case nocRunning:
		return t.running
	case nocStaleBlocked:
		return t.dim
	default:
		return t.stopped
	}
}

// rollupState reduces a TriageRollup + liveness facts to a single display state.
func rollupState(r state.TriageRollup, hasRunning, hasAny bool) nocState {
	switch {
	case r.NeedsYou > 0:
		return nocNeedsYou
	case hasRunning:
		return nocRunning
	case r.Blocked > 0 || r.AtRisk > 0 || r.Gated > 0:
		return nocBlocked
	case r.NeedsYouHistorical > 0 || r.AtRiskStale > 0 || r.BlockedStale > 0 || r.GatedStale > 0:
		return nocStaleBlocked
	case hasAny:
		return nocStopped
	default:
		return nocEmpty
	}
}

// agentState maps a single agent's liveness to a display state. Per-agent triage
// is carried by the session (the collapsed-thread bus), so an agent row reflects
// liveness only: alive=running, dead-but-mailbox-live=degraded, dead=stopped.
func agentState(a state.Agent) nocState {
	switch a.Liveness {
	case state.LivenessAlive:
		return nocRunning
	case state.LivenessWakeLive:
		return nocAtRisk
	case state.LivenessDeadMailboxLive:
		return nocAtRisk
	case state.LivenessDead:
		return nocStopped
	default:
		return nocStopped
	}
}

// agentNodeState is the work-facing agent state used by the NOC tree/detail.
// Process liveness still controls jump availability, but an agent involved in a
// current non-historical attention thread should not look calmly green.
func agentNodeState(sess state.Session, ag state.Agent) nocState {
	base := agentState(ag)
	if !agentOperational(ag) {
		return base
	}
	best := nocEmpty
	for _, th := range sess.Coordination.Threads {
		if th.Historical || th.Stale || !threadHasParticipant(th, ag.Handle) {
			continue
		}
		if th.Triage == state.TriageNeedsYou && !strings.EqualFold(threadAsker(th), ag.Handle) {
			continue
		}
		st := triageState(th.Triage)
		if st == nocRunning {
			continue
		}
		if st < best {
			best = st
		}
	}
	if best != nocEmpty {
		return best
	}
	return base
}

// triageState maps a thread/agent triage class to a display state.
func triageState(tr state.Triage) nocState {
	switch tr {
	case state.TriageNeedsYou:
		return nocNeedsYou
	case state.TriageBlocked:
		return nocBlocked
	case state.TriageGated:
		return nocGated
	case state.TriageAtRisk:
		return nocAtRisk
	default:
		return nocRunning
	}
}

// projectRollupState computes a project's display state from its snapshot.
func projectRollupState(ps noc.ProjectSnapshot) nocState {
	if ps.Warning != "" {
		return nocAtRisk
	}
	hasRunning := false
	hasAny := false
	for _, sess := range ps.Snap.Sessions {
		for _, ag := range sess.Agents {
			hasAny = true
			if agentOperational(ag) {
				hasRunning = true
			}
		}
	}
	return rollupState(ps.Snap.Rollup, hasRunning, hasAny)
}

// sessionRollupState computes a session's display state.
func sessionRollupState(sess state.Session) nocState {
	hasRunning := false
	hasAny := false
	for _, ag := range sess.Agents {
		hasAny = true
		if agentOperational(ag) {
			hasRunning = true
		}
	}
	return rollupState(sess.Rollup, hasRunning, hasAny)
}

// hasRunningAgentSnap reports whether any agent in the snapshot is live
// (alive or dead-mailbox-live). Console-side mirror of noc.hasRunningAgent so the
// digest can decide liveness without re-exporting the noc helper.
func hasRunningAgentSnap(snap state.Snapshot) bool {
	for _, sess := range snap.Sessions {
		for _, ag := range sess.Agents {
			if agentOperational(ag) {
				return true
			}
		}
	}
	return false
}

// projectIsStaleOnly reports whether a project is stopped (no live agent) and
// carries NO live attention (no needs-you, no live at-risk/blocked). Such a
// project is the "stale / archived" bottom tier the operator may want to hide.
// A warning project is NOT stale-only (it wants the operator's eye).
func projectIsStaleOnly(ps noc.ProjectSnapshot) bool {
	if ps.Warning != "" {
		return false
	}
	if hasRunningAgentSnap(ps.Snap) {
		return false
	}
	return !ps.Snap.Rollup.HasLiveAttention()
}

// projectLivenessPhrase renders a squad's liveness UNAMBIGUOUSLY. The N/M counts
// AGENTS (alive-or-mailbox-live of total discovered agents), not sessions, so it
// is labeled "N/M agents alive" — the reviewer flagged a bare "running 4/10" as
// ambiguous (4 alive of 10 agents? 4 sessions of 10?). A squad with no live
// agent reads "stopped". The agent count is what drives the phrase everywhere it
// appears (digest + tree project rows).
func projectLivenessPhrase(ps noc.ProjectSnapshot) string {
	live, wakeLive, total := projectAgentLiveness(ps)
	operational := live + wakeLive
	if operational > 0 {
		label := " agents alive"
		if wakeLive > 0 {
			label = " agents reachable"
		}
		return "running " + strconv.Itoa(operational) + "/" + strconv.Itoa(total) + label
	}
	return "stopped"
}

// projectAgentLiveness returns verified-live, wake-live, and total discovered
// agents across a project's sessions. It is the counter behind the liveness
// phrase.
func projectAgentLiveness(ps noc.ProjectSnapshot) (live, wakeLive, total int) {
	for _, sess := range ps.Snap.Sessions {
		for _, ag := range sess.Agents {
			total++
			switch ag.Liveness {
			case state.LivenessAlive, state.LivenessDeadMailboxLive:
				live++
			case state.LivenessWakeLive:
				wakeLive++
			}
		}
	}
	return live, wakeLive, total
}

func agentOperational(ag state.Agent) bool {
	switch ag.Liveness {
	case state.LivenessAlive, state.LivenessWakeLive, state.LivenessDeadMailboxLive:
		return true
	default:
		return false
	}
}
