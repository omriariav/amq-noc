package console

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fsnotify/fsnotify"

	"github.com/omriariav/amq-noc/internal/state"
)

// Config is everything runConsole needs to render Mission Control. The caller
// (the CLI verb) resolves the AMQ base root once, picks the probe, and tunes the
// triage thresholds; this package never shells out.
type Config struct {
	// ProjectDir is the directory the team was launched from (passed to the
	// state scanners). Required.
	ProjectDir string
	// BaseRoot is the resolved AMQ base root to scan. When empty, runConsole
	// boots into the degraded NoTeam screen rather than scanning.
	BaseRoot string
	// Probe is the liveness probe used by state.Build. A zero Probe falls back
	// to the production probe inside state.Build.
	Probe state.Probe
	// Thresholds tune the triage windows. Zero fields fall back to documented
	// defaults inside state.Build.
	Thresholds state.Thresholds
	// Refresh is the periodic resync cadence. Zero falls back to DefaultRefresh.
	Refresh time.Duration
	// Debounce coalesces fsnotify bursts. Zero falls back to DefaultDebounce.
	Debounce time.Duration

	// Once renders ONE static board to Out and returns; no tea.Program starts.
	// This is the non-TTY / CI path. When false the full-screen TUI runs against
	// /dev/tty.
	Once bool
	// Out is where the --once board is written (stdout in production; a buffer in
	// tests). Ignored for the interactive path, which renders to /dev/tty.
	Out io.Writer

	// NoTeamNotice, when non-empty, forces the degraded NoTeam state: --once
	// writes it to Out, the TUI renders the explanatory screen. The runner sets
	// this when no team is configured or the base root is unresolvable.
	NoTeamNotice string

	// InitialFilter, when non-empty, presets the view filter (e.g. from the
	// --session flag: "session:<name>"). It uses the same typed-filter grammar
	// the `/` entry parses, so a preset scope behaves identically to a typed one.
	InitialFilter string

	// ttyOpener is a seam so tests can avoid opening a real /dev/tty. Production
	// leaves it nil and openTTY is used.
	ttyOpener func() (*os.File, error)
}

// Run is the Mission Control entrypoint. The non-interactive (--once) path
// renders a single static board to Out and returns. The interactive path opens
// /dev/tty (NEVER os.Stdout, so the stdout-purity contract holds), builds the
// initial snapshot, wires the fsnotify watcher, and runs the Bubble Tea program.
//
// It NEVER panics on a missing team or an unresolvable root: those degrade to
// the NoTeam screen (TTY) or a guidance line (--once).
func Run(cfg Config) error {
	if cfg.Out == nil {
		cfg.Out = os.Stdout
	}

	rebuild := rebuildConfig{
		ProjectDir: cfg.ProjectDir,
		BaseRoot:   cfg.BaseRoot,
		Probe:      cfg.Probe,
		Thresholds: cfg.Thresholds,
	}

	// --once: render one static board (or the degraded notice) to Out and exit.
	// No tea.Program, no /dev/tty, no watcher.
	if cfg.Once {
		return runOnce(cfg, rebuild)
	}

	return runInteractive(cfg, rebuild)
}

// runOnce builds a single snapshot and writes the static board (or the NoTeam
// guidance) to cfg.Out. It is the CI / non-TTY surface and the one place the
// console writes to a caller-supplied (stdout) writer.
func runOnce(cfg Config, rebuild rebuildConfig) error {
	if cfg.NoTeamNotice != "" {
		fmt.Fprintln(cfg.Out, cfg.NoTeamNotice)
		return nil
	}
	snap, err := state.BuildWithThresholds(rebuild.ProjectDir, rebuild.BaseRoot, rebuild.Probe, rebuild.Thresholds)
	if err != nil {
		// A scan failure on the --once path is still informational, not fatal:
		// render guidance naming the failure and return success so CI piping does
		// not break on a transient unreadable root.
		fmt.Fprintf(cfg.Out, "amq-squad: could not scan AMQ base root %s: %v\n", rebuild.BaseRoot, err)
		return nil
	}
	// A preset filter (e.g. --session) narrows the --once board too, so the same
	// scope applies whether or not a TTY is attached.
	if f := parseFilter(cfg.InitialFilter); f.active() {
		snap = filterSnapshot(snap, f)
	}
	// The render layer ages agent/thread signals against the SAME clock the
	// snapshot was built with (the probe's Now), so --once ages are deterministic
	// in tests and honest in production.
	fmt.Fprintln(cfg.Out, StaticBoard(snap, clockOrDefault(cfg.Probe)))
	return nil
}

// StaticBoard renders the full non-interactive board for a snapshot: the triage
// rollup headline followed by the per-session body. It is exported so the CLI
// verb (and tests) can render the same board the --once path emits.
//
// now is the injected clock used to age agent LastSeen / thread events in the
// body; a nil clock falls back to the real wall-clock.
//
// The headline separates concepts and labels the triage numbers as the THREAD
// counts they are: "<N> sessions · <X> needs-you threads · <Y> blocked threads ·
// <Z> gated threads · <W> at-risk threads", " · "-separated, with each noun
// pluralized on its own count (so a single one reads "1 blocked thread").
func StaticBoard(snap state.Snapshot, now func() time.Time) string {
	if now == nil {
		now = time.Now
	}
	r := snap.Rollup
	sessions := len(snap.Sessions)
	headline := "amq-squad mission control · " +
		fmt.Sprintf("%d %s · %d needs-you %s · %d blocked %s · %d gated %s · %d at-risk %s",
			sessions, plural(sessions, "session", "sessions"),
			r.NeedsYou, plural(r.NeedsYou, "thread", "threads"),
			r.Blocked, plural(r.Blocked, "thread", "threads"),
			r.Gated, plural(r.Gated, "thread", "threads"),
			r.AtRisk, plural(r.AtRisk, "thread", "threads"))
	return headline + "\n\n" + staticBoardBody(snap, now)
}

// runInteractive opens /dev/tty, builds the initial snapshot, starts the
// watcher, and runs the Bubble Tea program against the tty. The program's I/O is
// pinned to /dev/tty so os.Stdout stays pure for the other verbs' tests.
func runInteractive(cfg Config, rebuild rebuildConfig) error {
	// Degraded boot: no team / unresolvable root. Still render the explanatory
	// screen on the tty so the operator sees WHY, rather than aborting.
	initial, _ := state.BuildWithThresholds(rebuild.ProjectDir, rebuild.BaseRoot, rebuild.Probe, rebuild.Thresholds)
	m := newModel(rebuild, initial, cfg.NoTeamNotice)
	if cfg.InitialFilter != "" {
		m.filter = parseFilter(cfg.InitialFilter)
		m = m.reselect()
	}

	opener := cfg.ttyOpener
	if opener == nil {
		opener = openTTY
	}
	tty, err := opener()
	if err != nil {
		// No /dev/tty available (e.g. truly headless without --once). This is the
		// "truly unusable" interactive case: report it so the caller can exit
		// nonzero, rather than silently doing nothing.
		return fmt.Errorf("console requires a terminal; rerun with --once for a static board (could not open /dev/tty: %w)", err)
	}
	defer tty.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	opts := []tea.ProgramOption{
		tea.WithInput(tty),
		tea.WithOutput(tty),
		tea.WithAltScreen(),
		tea.WithContext(ctx),
	}
	p := tea.NewProgram(m, opts...)

	// Start the watcher only when there is a live team to watch. It feeds events
	// into the program via p.Send; its lifetime is bound to ctx so it tears down
	// with the program.
	if !m.noTeam {
		startWatcher(ctx, p, rebuild, cfg.refreshOrDefault(), cfg.debounceOrDefault())
	}

	_, err = p.Run()
	return err
}

func (c Config) refreshOrDefault() time.Duration {
	if c.Refresh <= 0 {
		return DefaultRefresh
	}
	return c.Refresh
}

func (c Config) debounceOrDefault() time.Duration {
	if c.Debounce <= 0 {
		return DefaultDebounce
	}
	return c.Debounce
}

// programSender is the minimal slice of *tea.Program the watcher needs, so the
// watcher loop can be exercised without a real program in tests.
type programSender interface {
	Send(msg tea.Msg)
}

// startWatcher launches the fsnotify watcher in a goroutine. It establishes
// watches on the session-agent dirs (not every leaf), debounces bursts into a
// single rebuild request, and treats EVERY watch error as a resync — it never
// crashes. On any setup failure it sends a watchErrMsg and returns, so the
// periodic ticker fallback still keeps the board current.
func startWatcher(ctx context.Context, p programSender, rebuild rebuildConfig, refresh, debounce time.Duration) {
	go watchLoop(ctx, p, rebuild, refresh, debounce)
}

// watchLoop is the long-lived watcher. It is split out so the lifecycle is
// readable: create the watcher, (re)establish watches from a fresh snapshot,
// then select over fsnotify events / errors / a debounce timer / a resync
// ticker / ctx.Done. Any error path funnels into a resync rather than a crash.
func watchLoop(ctx context.Context, p programSender, rebuild rebuildConfig, refresh, debounce time.Duration) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		// Cannot watch at all: tell the UI (it surfaces the warning) and rely on
		// the in-program ticker fallback to keep data fresh. Do not crash.
		p.Send(watchErrMsg{err: fmt.Errorf("fsnotify unavailable, falling back to periodic refresh: %w", err)})
		return
	}
	defer w.Close()

	deb := newDebouncer(debounce)
	resyncTicker := time.NewTicker(refresh)
	defer resyncTicker.Stop()
	// A short tick drives the debounce check without busy-waiting.
	debTick := time.NewTicker(debounce)
	defer debTick.Stop()

	// Establish the initial watch set from a fresh snapshot.
	rewatch(w, rebuild)

	for {
		select {
		case <-ctx.Done():
			return

		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			deb.Request(time.Now(), classifyEvent(ev))

		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			// EVERY watch error -> resync, never crash.
			deb.Request(time.Now(), classifyWatchError(err))

		case <-debTick.C:
			if deb.Ready(time.Now()) {
				resync := deb.WantsResync()
				deb.Reset()
				if resync {
					rewatch(w, rebuild)
				}
				p.Send(rebuildResult(rebuild, resync))
			}

		case <-resyncTicker.C:
			// Periodic full resync fallback for missed/overflow events.
			rewatch(w, rebuild)
			p.Send(rebuildResult(rebuild, true))
		}
	}
}

// rewatch re-walks the tree via a fresh snapshot and (re)adds the watch targets.
// Adding an already-watched path is a no-op in fsnotify, so this is safe to call
// repeatedly. Watch-add failures are tolerated (best effort): the ticker
// fallback still keeps the board fresh.
func rewatch(w *fsnotify.Watcher, rebuild rebuildConfig) {
	snap, err := state.BuildWithThresholds(rebuild.ProjectDir, rebuild.BaseRoot, rebuild.Probe, rebuild.Thresholds)
	if err != nil {
		return
	}
	targets := watchTargets(snap.BaseRoot, snapshotDirs(snap))
	for _, t := range targets {
		_ = w.Add(t)
	}
}

// rebuildResult runs state.Build and wraps it as a snapshotMsg. It runs on the
// watcher goroutine (off the UI loop) so the Bubble Tea reducer only ever
// receives an immutable value.
func rebuildResult(rebuild rebuildConfig, _ bool) tea.Msg {
	snap, err := state.BuildWithThresholds(rebuild.ProjectDir, rebuild.BaseRoot, rebuild.Probe, rebuild.Thresholds)
	return snapshotMsg{snapshot: snap, buildErr: err}
}

// snapshotDirs projects a state.Snapshot into the watcher's minimal sessionDirs
// shape, so watchTargets stays decoupled from the state package.
func snapshotDirs(snap state.Snapshot) []sessionDirs {
	out := make([]sessionDirs, 0, len(snap.Sessions))
	for _, s := range snap.Sessions {
		dirs := make([]string, 0, len(s.Agents))
		for _, a := range s.Agents {
			if a.AgentDir != "" {
				dirs = append(dirs, a.AgentDir)
			}
		}
		out = append(out, sessionDirs{Root: s.Root, AgentDirs: dirs})
	}
	return out
}

// openTTY opens /dev/tty for the interactive program's I/O. Routing the TUI to
// /dev/tty (instead of os.Stdout) keeps stdout pure for the other verbs whose
// tests assert a clean stdout.
func openTTY() (*os.File, error) {
	return os.OpenFile("/dev/tty", os.O_RDWR, 0)
}
