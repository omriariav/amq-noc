// Package console - noc_orchestration.go: read-only rendering for lead-agent
// orchestrated squads (amq-squad v1.7 team.json orchestrated/lead). The NOC
// surfaces who the lead is, what the workstream is for (the brief's Goal),
// and the newest per-child lead exchange, all projected from data the
// collector already read: no new I/O happens at render time.
package console

import (
	"sort"
	"strconv"
	"strings"

	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/state"
)

// leadExchangePreviewLimit bounds the per-child digest so a wide squad cannot
// flood the session detail pane.
const leadExchangePreviewLimit = 5

// orchestrationHeader renders the session-detail header lines: the lead
// identity (with a lead-down warning when the driver is gone while children
// continue) for orchestrated sessions, and the brief Goal line whenever the
// workstream has a readable brief. Empty when there is nothing to say.
func (m NOCModel) orchestrationHeader(ps noc.ProjectSnapshot, sess state.Session) string {
	var b strings.Builder
	if sess.Orchestrated {
		label := "orchestrated; lead: " + sess.LeadHandle
		if sess.LeadRole != "" && !strings.EqualFold(sess.LeadRole, sess.LeadHandle) {
			label += " (" + sess.LeadRole + ")"
		}
		if state.SessionLeadDown(sess) {
			b.WriteString(m.th.paint(m.th.nocStateStyle(nocAtRisk), label+" - lead is DOWN; children continue") + "\n")
		} else {
			b.WriteString(m.th.paint(m.th.dim, label) + "\n")
		}
	}
	if goal := strings.TrimSpace(m.sessionBriefGoal(ps, sess)); goal != "" {
		b.WriteString(m.th.paint(m.th.dim, truncate("goal: "+goal, detailThreadTitleWidth)) + "\n")
	}
	return b.String()
}

// sessionBriefGoal resolves the collected brief Goal line for a session.
func (m NOCModel) sessionBriefGoal(ps noc.ProjectSnapshot, sess state.Session) string {
	name := strings.TrimSpace(sess.Name)
	if name == "" || len(ps.SessionBriefGoals) == 0 {
		return ""
	}
	return ps.SessionBriefGoals[name]
}

// leadReportsSection renders the per-child digest of the newest p2p exchange
// with the lead, newest first. This surfaces the orchestrator reporting
// protocol (children push status / question / review_request to the lead over
// AMQ) without leaving the NOC; it is a pure projection of the collapsed
// threads. Empty when the session has no lead exchanges yet.
func (m NOCModel) leadReportsSection(sess state.Session) string {
	exchanges := leadExchanges(sess)
	if len(exchanges) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.dim, "lead exchanges: newest per child") + "\n")
	shown := exchanges
	if len(shown) > leadExchangePreviewLimit {
		shown = shown[:leadExchangePreviewLimit]
	}
	for _, ex := range shown {
		line := "  " + ex.child + ": "
		if ex.thread.Kind != "" {
			line += string(ex.thread.Kind) + " "
		}
		title := strings.TrimSpace(ex.thread.Subject)
		if title == "" {
			title = ex.thread.ID
		}
		line += title
		meta := ""
		if !ex.thread.LastEventAt.IsZero() && !m.ms.ObservedAt.IsZero() {
			meta = ageLabel(m.ms.ObservedAt.Sub(ex.thread.LastEventAt))
		}
		if strings.EqualFold(ex.thread.LastFrom, sess.LeadHandle) {
			if meta != "" {
				meta += ", "
			}
			meta += "lead replied last"
		}
		if meta != "" {
			line += " (" + meta + ")"
		}
		b.WriteString(m.th.paint(m.th.dim, truncate(line, detailThreadTitleWidth)) + "\n")
	}
	if len(exchanges) > len(shown) {
		b.WriteString(m.th.paint(m.th.dim, "  +"+strconv.Itoa(len(exchanges)-len(shown))+" more children") + "\n")
	}
	b.WriteString(m.detailRule() + "\n")
	return b.String()
}

// leadExchange pairs a child handle with its newest p2p thread with the lead.
type leadExchange struct {
	child  string
	thread state.ThreadSummary
}

// leadExchanges projects the newest p2p thread between the lead and each
// child, newest first (ties by child handle). Only threads on the canonical
// p2p/ prefix with the lead as a participant count; gate/decision threads
// keep their existing surfaces.
func leadExchanges(sess state.Session) []leadExchange {
	if !sess.Orchestrated || strings.TrimSpace(sess.LeadHandle) == "" {
		return nil
	}
	newest := map[string]state.ThreadSummary{}
	for _, th := range sess.Coordination.Threads {
		if !strings.HasPrefix(strings.ToLower(th.ID), "p2p/") {
			continue
		}
		leadIn := false
		child := ""
		for _, p := range th.Participants {
			if strings.EqualFold(p, sess.LeadHandle) {
				leadIn = true
				continue
			}
			if child == "" {
				child = p
			}
		}
		if !leadIn || child == "" {
			continue
		}
		prev, ok := newest[child]
		if !ok || th.LastEventAt.After(prev.LastEventAt) {
			newest[child] = th
		}
	}
	out := make([]leadExchange, 0, len(newest))
	for child, th := range newest {
		out = append(out, leadExchange{child: child, thread: th})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].thread.LastEventAt.Equal(out[j].thread.LastEventAt) {
			return out[i].thread.LastEventAt.After(out[j].thread.LastEventAt)
		}
		return out[i].child < out[j].child
	})
	return out
}
