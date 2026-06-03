// Package console — noc_watch.go: the NOC live data feeds — a refresh ticker
// (driven from Update via nocTickCmd), a periodic re-discovery (nocRediscover),
// and an fsnotify watcher over EVERY discovered project's .agent-mail tree.
//
// kqueue FD safety: we watch DIRECTORIES (not leaf files), add newly created
// subdirectories on the fly, and treat any watch error as a resync trigger
// rather than a fatal. No feed mutates the model; each delivers an immutable
// tea.Msg via p.Send. The ticker covers any events the watcher misses.
package console

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fsnotify/fsnotify"

	"github.com/omriariav/amq-noc/internal/noc"
)

// startNOCFeeds launches the filesystem watcher feed. The refresh + rediscover
// tickers are scheduled by Update (nocTickCmd / nocRediscoverTickCmd); this only
// owns the fsnotify watcher, which is rebuilt on each re-discovery so new/removed
// projects' .agent-mail trees are covered without a restart.
func startNOCFeeds(ctx context.Context, p *tea.Program, rebuild NOCRebuildConfig) {
	go watchNOCRoots(ctx, p, rebuild)
}

// watchNOCRoots maintains an fsnotify watcher over every discovered project's
// .agent-mail tree, re-walking the roots periodically so the watch set follows
// project churn. A change debounces into a single nocWatchMsg.
func watchNOCRoots(ctx context.Context, p *tea.Program, rebuild NOCRebuildConfig) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return // ticker still drives periodic rebuilds
	}
	defer w.Close()

	rewatch := func() {
		dirs, derr := noc.Discover(rebuild.Roots, rebuild.Depth)
		if derr != nil {
			return
		}
		for _, dir := range dirs {
			addNOCTree(w, filepath.Join(dir, noc.AgentMailDirName))
		}
	}
	rewatch()

	resyncTimer := time.NewTicker(NOCDefaultRediscover)
	defer resyncTimer.Stop()

	var debTimer *time.Timer
	debounce := func() {
		if debTimer != nil {
			debTimer.Stop()
		}
		debTimer = time.AfterFunc(150*time.Millisecond, func() {
			p.Send(nocWatchMsg{})
		})
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-resyncTimer.C:
			// Re-discover and extend the watch set (new projects/sessions).
			rewatch()
		case event, ok := <-w.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Create != 0 {
				if fi, statErr := os.Stat(event.Name); statErr == nil && fi.IsDir() {
					_ = w.Add(event.Name)
				}
			}
			debounce()
		case _, ok := <-w.Errors:
			if !ok {
				return
			}
			// Watch error: trigger a resync; the ticker also covers us.
			debounce()
		}
	}
}

// addNOCTree adds baseRoot and its immediate session subdirectories to the
// watcher. Watching directories (not leaves) keeps the FD count bounded and is
// kqueue-safe on macOS.
func addNOCTree(w *fsnotify.Watcher, baseRoot string) {
	if baseRoot == "" {
		return
	}
	if fi, err := os.Stat(baseRoot); err != nil || !fi.IsDir() {
		return
	}
	_ = w.Add(baseRoot)
	entries, err := os.ReadDir(baseRoot)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			_ = w.Add(filepath.Join(baseRoot, e.Name()))
		}
	}
}

// defaultPidTree returns the child PIDs of pid via a read-only `pgrep -P`. It is
// the production seam for the jump action's PID-subtree bonus; a missing pgrep or
// no children yields an empty slice (the bonus is simply not awarded).
func defaultPidTree(pid int) []int {
	if pid <= 0 {
		return nil
	}
	out, err := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output()
	if err != nil {
		return nil
	}
	var kids []int
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if n, perr := strconv.Atoi(line); perr == nil {
			kids = append(kids, n)
		}
	}
	return kids
}
