// Package console — noc_alerts.go: NEEDS-YOU ALERTS (2.3 awareness+scale).
//
// When a session TRANSITIONS into needs-you — its needs-you count goes 0 → >0
// between two snapshots — the NOC emits a terminal BELL once and sets a visible
// banner ("🔔 <project>/<session> needs you"). The alert fires ONLY on the
// 0→N transition: while a session stays in needs-you across 2s refreshes it does
// NOT re-alert (no spam). When it drops back to 0 and rises again, it may alert
// again.
//
// Alerts are strictly READ-ONLY: the bell + the banner are the only effects;
// they never mutate squad state. The bell is written through an injected seam
// (m.bell) so tests assert it fired without writing to a real tty, and so a
// --no-bell flag / the 'A' mute toggle can suppress it.
package console

import (
	"sort"
	"strings"

	"github.com/omriariav/amq-noc/internal/noc"
)

// sessionKey uniquely identifies a session across snapshots for needs-you
// transition tracking: project dir + session name. It is stable across refreshes
// (the SAME keys the tree's node ids derive from), so a session's prior
// needs-you state is matched correctly even as the snapshot is replaced.
func sessionAlertKey(projectDir, session string) string {
	return projectDir + "|" + session
}

// detectNeedsYouTransitions compares the model's PRIOR per-session needs-you
// state against a fresh snapshot, returns the sessions that just TRANSITIONED
// into needs-you (0 → >0), and updates the model's prior-state map to the new
// snapshot. Returns nil when nothing transitioned.
//
// The VERY FIRST snapshot only establishes the baseline — it never alerts, even
// if it already carries needs-you. This is deliberate: a freshly-opened NOC over
// an already-needs-you board should not ring a bell for pre-existing state (there
// was no 0→N transition to observe), and — critically — a banner set on frame
// zero would shift the tree layout, masking the very first cursor move. After the
// baseline is seeded, every later 0→N delta alerts. m.priorSeeded tracks whether
// the baseline has been taken.
//
// It is pure-ish: it only mutates m.priorNeedsYou / m.priorSeeded (the transition
// bookkeeping), never squad state. The caller (Update on a fresh snapshot) decides
// whether to ring the bell + set the banner.
func (m *NOCModel) detectNeedsYouTransitions(ms noc.MultiSnapshot) []needsYouAlert {
	if m.priorNeedsYou == nil {
		m.priorNeedsYou = map[string]int{}
	}
	next := map[string]int{}
	var transitioned []needsYouAlert
	baseline := !m.priorSeeded

	for _, ps := range ms.Projects {
		if ps.Warning != "" {
			continue
		}
		for _, sess := range ps.Snap.Sessions {
			key := sessionAlertKey(ps.Dir, sess.Name)
			now := sess.Rollup.NeedsYou
			next[key] = now
			if baseline {
				// First snapshot: record state, never alert.
				continue
			}
			prior := m.priorNeedsYou[key]
			if prior == 0 && now > 0 {
				transitioned = append(transitioned, needsYouAlert{
					project: ps.Project,
					session: sessionLabel(sess),
				})
			}
		}
	}

	// Replace the prior map wholesale so a session that vanished (project removed
	// / collapsed away) resets to prior=0 and can alert again if it returns.
	m.priorNeedsYou = next
	m.priorSeeded = true

	// Deterministic order so the banner picks a stable "first" when several
	// sessions transition in the same refresh.
	sort.SliceStable(transitioned, func(i, j int) bool {
		if transitioned[i].project != transitioned[j].project {
			return transitioned[i].project < transitioned[j].project
		}
		return transitioned[i].session < transitioned[j].session
	})
	return transitioned
}

// needsYouAlert is one session that just transitioned into needs-you, carried to
// the banner + bell.
type needsYouAlert struct {
	project string
	session string
}

// fireNeedsYouAlerts rings the bell ONCE (regardless of how many sessions
// transitioned in this refresh — one refresh, one bell) and sets the banner to
// the transitioned sessions, unless alerts are muted. Muting (the 'A' toggle or
// --no-bell) suppresses BOTH the bell and the banner so a muted operator gets no
// interruption. Returns whether the bell rang (for tests / clarity).
//
// READ-ONLY: the only effects are the injected bell seam + the model's banner
// string; nothing here touches a squad.
func (m *NOCModel) fireNeedsYouAlerts(alerts []needsYouAlert) bool {
	if len(alerts) == 0 {
		return false
	}
	// Desktop notification (#28): opt-in and INDEPENDENT of the bell mute -
	// the NOC's terminal is usually buried exactly when this matters. Fires
	// once per refresh that carried a transition, like the bell.
	if m.notify != nil {
		body := alerts[0].project + "/" + alerts[0].session + " needs you"
		if len(alerts) > 1 {
			body += " (+" + itoaPalette(len(alerts)-1) + " more)"
		}
		m.notify("amq-noc", body)
	}
	if m.alertsMuted {
		return false
	}
	// Banner: lead with the first transitioned session; note the count if more.
	first := alerts[0]
	banner := "🔔 " + first.project + "/" + first.session + " needs you"
	if m.colorMode == ColorAscii {
		banner = "(!) " + first.project + "/" + first.session + " needs you"
	}
	if len(alerts) > 1 {
		banner += " (+" + itoaPalette(len(alerts)-1) + " more)"
	}
	m.alertBanner = banner

	// Bell: exactly once per refresh that carried ≥1 transition. The seam writes
	// "\a" to the tty in production; tests inject a counter.
	if m.bell != nil {
		m.bell()
	}
	return true
}

// alertBannerView renders the needs-you alert banner (when set), painted hot so
// it is unmissable. Empty string when there is no active banner. It survives
// NO_COLOR via the text label.
func (m NOCModel) alertBannerView() string {
	b := strings.TrimSpace(m.alertBanner)
	if b == "" {
		return ""
	}
	return m.th.paint(m.th.needsYou, b)
}
