// Package console - noc_output.go: the read-only view-output affordance
// (#21). The operator presses v on an agent row to see what that agent is
// doing RIGHT NOW without leaving the NOC: the tail of its live pane when the
// published runtime contract carries an alive pane id, else the newest AMQ
// message the agent sent. Strictly diagnostic: capture is user-paced (never a
// background poll), reads screen text only, and never sends keys.
package console

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-noc/internal/state"
)

// agentOutputTailLines bounds the preview so the detail pane stays scannable.
const agentOutputTailLines = 12

// agentOutputView is the captured preview for ONE agent row. It renders only
// while that row stays selected; source names where the text came from and
// how fresh it is, so the operator never mistakes a stale tail for live work.
type agentOutputView struct {
	nodeID string
	source string
	lines  []string
}

// beginViewOutput captures the selected agent's latest output. Pane tail
// first: the pane id comes from amq-squad's published runtime contract
// (status --json), never from scraping, and only an alive pane is read.
// Fallback: the newest AMQ message FROM this agent. Both paths are read-only.
func (m *NOCModel) beginViewOutput() tea.Cmd {
	n, ok := m.selectedNode()
	if !ok || n.kind != nodeAgent {
		m.actNote = "view output applies to an agent row - select an agent first"
		return nil
	}
	projectDir := strings.TrimSpace(n.project.Dir)
	session := strings.TrimSpace(n.session.Name)
	profile := sessionCommandProfileForSession(n.project, n.session)
	if profile == "PROFILE" {
		profile = ""
	}

	if m.runtimeFetch != nil && m.paneCapture != nil && projectDir != "" && session != "" {
		rs := m.runtimeFetch(projectDir, profile, session)
		mem, found := rs.MemberByRole(n.agent.Role)
		if !found {
			mem, found = rs.MemberByRole(n.agent.Handle)
		}
		if found && mem.PaneAlive && strings.TrimSpace(mem.PaneID) != "" {
			lines, err := m.paneCapture(mem.PaneID, agentOutputTailLines)
			if err == nil {
				m.agentOutput = &agentOutputView{
					nodeID: n.id,
					source: "pane " + mem.PaneID + " tail " + m.dot() + " captured on demand, read-only",
					lines:  lines,
				}
				return nil
			}
			m.actNote = "view output: pane capture failed: " + err.Error()
			return nil
		}
	}

	if th, found := newestThreadFromAgent(n.session, n.agent.Handle); found {
		age := ""
		if !th.LastEventAt.IsZero() && !m.ms.ObservedAt.IsZero() {
			age = " " + m.dot() + " " + ageLabel(m.ms.ObservedAt.Sub(th.LastEventAt))
		}
		m.agentOutput = &agentOutputView{
			nodeID: n.id,
			source: "latest AMQ message (no live pane)" + age,
			lines:  splitBoundedLines(th.LatestBody, agentOutputTailLines),
		}
		return nil
	}

	m.actNote = "view output: no live pane and no AMQ messages from " + n.agent.Handle
	return nil
}

// newestThreadFromAgent returns the freshest thread whose LATEST message was
// sent by handle - "what did this agent say last", not merely participate in.
func newestThreadFromAgent(sess state.Session, handle string) (state.ThreadSummary, bool) {
	var best state.ThreadSummary
	found := false
	for _, th := range sess.Coordination.Threads {
		if !strings.EqualFold(strings.TrimSpace(th.LastFrom), strings.TrimSpace(handle)) {
			continue
		}
		if !found || th.LastEventAt.After(best.LastEventAt) {
			best = th
			found = true
		}
	}
	return best, found
}

// splitBoundedLines splits a message body into at most n display lines,
// keeping the head (a message reads top-down, unlike a pane tail).
func splitBoundedLines(body string, n int) []string {
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if n > 0 && len(lines) > n {
		lines = lines[:n]
	}
	return lines
}

// agentOutputSection renders the captured preview when it belongs to the
// selected node; cleared implicitly by selecting a different agent.
func (m NOCModel) agentOutputSection(nodeID string) string {
	out := m.agentOutput
	if out == nil || out.nodeID != nodeID {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.dim, "latest output "+m.dot()+" "+out.source) + "\n")
	if len(out.lines) == 0 {
		b.WriteString(m.th.paint(m.th.dim, "  (empty)") + "\n")
	}
	for _, line := range out.lines {
		b.WriteString(m.th.paint(m.th.dim, "  "+truncate(line, detailThreadTitleWidth)) + "\n")
	}
	b.WriteString(m.detailRule() + "\n")
	return b.String()
}
