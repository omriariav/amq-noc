package console

import (
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher tuning defaults. The console debounces a burst of filesystem events
// into a single rebuild, and falls back to a periodic resync so a missed or
// dropped event never leaves the board permanently stale.
const (
	// DefaultDebounce coalesces a burst of fsnotify events into one rebuild.
	// 50-150ms is short enough to feel live and long enough to absorb the
	// multi-file writes a single agent turn produces (presence + maildir).
	DefaultDebounce = 100 * time.Millisecond

	// DefaultRefresh is the periodic ticker cadence: a full resync even with no
	// filesystem event, so relative ages advance and any missed/overflow event
	// self-heals.
	DefaultRefresh = 2 * time.Second
)

// watchDecision is the unit-testable verdict of classifying a single watcher
// signal. It deliberately separates "should we rebuild?" from "must we do a
// FULL resync (re-walk + re-establish watches)?" so the debounce/resync policy
// can be tested without an OS watcher.
type watchDecision struct {
	// Rebuild is true when this signal should (eventually, after debounce)
	// trigger a state.Build.
	Rebuild bool
	// Resync is true when this signal indicates we may have MISSED events
	// (overflow, an error, or a directory create/remove that changes the set of
	// dirs we must watch). A resync re-walks the tree AND re-establishes watches,
	// never just rebuilds the snapshot.
	Resync bool
}

// classifyEvent maps a raw fsnotify event to a rebuild/resync decision. The
// policy:
//
//   - A create or remove of a directory changes the set of session/agent dirs
//     we must watch, so it forces a RESYNC (re-walk + re-watch), not just a
//     rebuild.
//   - A write/chmod/rename to a leaf (presence.json, a maildir message) is an
//     ordinary content change: rebuild, no re-watch needed.
//   - An empty/zero event op is treated conservatively as a resync (we cannot
//     tell what changed).
//
// It NEVER returns "do nothing" for a real event: a watched path firing always
// means the board is potentially stale.
func classifyEvent(ev fsnotify.Event) watchDecision {
	switch {
	case ev.Op == 0:
		// Unknown/zero op: be conservative and resync.
		return watchDecision{Rebuild: true, Resync: true}
	case ev.Op&fsnotify.Create != 0, ev.Op&fsnotify.Remove != 0, ev.Op&fsnotify.Rename != 0:
		// A dir appeared/vanished/moved: the watch set may be wrong now.
		return watchDecision{Rebuild: true, Resync: true}
	default:
		// Write / Chmod to an existing leaf: content changed, watch set is fine.
		return watchDecision{Rebuild: true, Resync: false}
	}
}

// classifyWatchError maps a watcher error into a decision. EVERY watch error is
// a resync: we assume we may have dropped events and must re-walk + re-watch,
// rather than crash. (fsnotify surfaces overflow as an error on the Errors
// channel, which is exactly the missed-event case this rule covers.)
func classifyWatchError(err error) watchDecision {
	if err == nil {
		return watchDecision{}
	}
	return watchDecision{Rebuild: true, Resync: true}
}

// debouncer coalesces a burst of rebuild requests into a single one. It is a
// pure, clock-injected state machine so the debounce window can be tested
// deterministically without sleeping. Call Request on each incoming signal with
// the current time; Ready reports whether the debounce window has elapsed since
// the FIRST pending request, at which point the caller fires one rebuild and
// calls Reset.
type debouncer struct {
	window  time.Duration
	pending bool
	first   time.Time // time of the first request in the current burst
	resync  bool      // any request in the burst demanded a full resync
}

// newDebouncer builds a debouncer with the given coalescing window. A zero or
// negative window falls back to DefaultDebounce.
func newDebouncer(window time.Duration) *debouncer {
	if window <= 0 {
		window = DefaultDebounce
	}
	return &debouncer{window: window}
}

// Request records an incoming signal at time now. The first request in a burst
// starts the window; subsequent requests within the window are coalesced. A
// resync request anywhere in the burst makes the eventual rebuild a full resync.
func (d *debouncer) Request(now time.Time, dec watchDecision) {
	if !dec.Rebuild && !dec.Resync {
		return
	}
	if !d.pending {
		d.pending = true
		d.first = now
	}
	if dec.Resync {
		d.resync = true
	}
}

// Ready reports whether the debounce window has elapsed since the first pending
// request and there is a request to fire. It does NOT clear state; the caller
// calls Reset after firing so the next burst starts clean.
func (d *debouncer) Ready(now time.Time) bool {
	if !d.pending {
		return false
	}
	return now.Sub(d.first) >= d.window
}

// Pending reports whether any request is currently coalescing.
func (d *debouncer) Pending() bool { return d.pending }

// WantsResync reports whether the pending (or just-fired) burst demanded a full
// resync. Valid until Reset is called.
func (d *debouncer) WantsResync() bool { return d.resync }

// Reset clears the burst after a rebuild has been fired.
func (d *debouncer) Reset() {
	d.pending = false
	d.resync = false
	d.first = time.Time{}
}

// watchTargets computes the set of directories to watch for a snapshot: the
// per-session agent-parent dirs (base/<session>/agents and the session root),
// NOT every leaf maildir. fsnotify on the parent dir catches creates/removes of
// the agent dirs and writes to the immediate children; watching every leaf
// would blow the descriptor budget on a large team. The base root itself is
// always included so brand-new sessions are noticed.
//
// It is exported-for-test indirectly via the watcher; kept pure (no I/O) so the
// target-selection policy is unit-testable from a snapshot fixture.
func watchTargets(baseRoot string, sessions []sessionDirs) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	add(baseRoot)
	for _, s := range sessions {
		add(s.Root)
		if s.Root != "" {
			add(filepath.Join(s.Root, "agents"))
		}
		for _, a := range s.AgentDirs {
			add(a)
		}
	}
	return out
}

// sessionDirs is the minimal per-session directory shape watchTargets needs,
// decoupled from state.Session so the watch-target policy can be unit-tested
// without constructing a full snapshot.
type sessionDirs struct {
	Root      string
	AgentDirs []string
}
