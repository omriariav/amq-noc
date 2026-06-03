package state

import (
	"sort"
	"strings"
	"time"
)

// Coordination is the derived read-only coordination model for a session: its
// threads, edges, timeline, triage rollup, and any scan warnings. It is built
// purely from scanned mailbox messages plus the injected clock + thresholds.
type Coordination struct {
	Threads  []ThreadSummary
	Edges    []Edge
	Timeline []TimelineEvent
	Rollup   TriageRollup
	Warnings []Warning
}

// collapseInput is the raw material for building a session's coordination view:
// every message observed across every agent inbox in the session, deduplicated
// by (msgid, owner, state) at scan time, plus the agents' liveness for the
// heartbeat/at-risk signals.
type collapseInput struct {
	messages []Message
	agents   []Agent
	warnings []Warning
}

// buildCoordination collapses messages into threads, builds the edge list and a
// human-readable timeline, then computes triage per thread and rolls it up. now
// and thresholds are injected; the function reads no clock and no disk itself.
func buildCoordination(in collapseInput, now time.Time, th Thresholds) Coordination {
	th = withThresholdDefaults(th)

	threads := collapseThreads(in.messages, now, th, in.agents)
	edges := buildEdges(in.messages)
	timeline := buildTimeline(threads, in.messages)

	var rollup TriageRollup
	for _, t := range threads {
		rollup.countThread(t)
	}

	return Coordination{
		Threads:  threads,
		Edges:    edges,
		Timeline: timeline,
		Rollup:   rollup,
		Warnings: in.warnings,
	}
}

// threadAccumulator gathers everything about one canonical thread as messages
// stream in, so the final summary is a single pass.
type threadAccumulator struct {
	id           string
	participants map[string]bool
	subject      string
	lastKind     Kind
	labels       map[string]bool
	orchestrator string
	fromProject  string
	replyProject string
	lastEventAt  time.Time
	count        int
	// unreadBy: recipient handle -> still has an unread copy (inbox/new).
	unreadBy map[string]bool
	// readBy: recipient handle -> has a read copy (inbox/cur). Used so a later
	// read supersedes an earlier unread for the same recipient.
	readBy      map[string]bool
	latest      Message
	hasLatest   bool
	blockActive bool
	blockOwner  string
}

// collapseThreads groups messages by canonical thread id and derives a summary
// per thread: union participants, latest subject/kind, status, unread-by, and
// triage+freshness.
func collapseThreads(msgs []Message, now time.Time, th Thresholds, agents []Agent) []ThreadSummary {
	accs := map[string]*threadAccumulator{}
	var order []string

	for _, m := range msgs {
		acc, ok := accs[m.Thread]
		if !ok {
			acc = &threadAccumulator{
				id:           m.Thread,
				participants: map[string]bool{},
				labels:       map[string]bool{},
				unreadBy:     map[string]bool{},
				readBy:       map[string]bool{},
			}
			accs[m.Thread] = acc
			order = append(order, m.Thread)
		}
		acc.observe(m)
	}

	sort.Strings(order)
	out := make([]ThreadSummary, 0, len(order))
	for _, id := range order {
		out = append(out, accs[id].summarize(now, th, agents))
	}
	return out
}

func (a *threadAccumulator) observe(m Message) {
	a.count++
	if m.From != "" {
		a.participants[m.From] = true
	}
	for _, r := range m.To {
		a.participants[r] = true
	}
	for _, label := range m.Labels {
		if label != "" {
			a.labels[label] = true
		}
	}

	// unread-by is per RECIPIENT, decided by where the recipient's own copy
	// sits. A copy in inbox/new => that owner has it unread; a copy in inbox/cur
	// => read. Only the OWNER of an inbox is informative about that owner.
	if m.Owner != "" {
		switch m.State {
		case MailboxNew:
			if !a.readBy[m.Owner] {
				a.unreadBy[m.Owner] = true
			}
		case MailboxCur:
			a.readBy[m.Owner] = true
			delete(a.unreadBy, m.Owner)
		}
	}

	// Track latest by created time (messages arrive sorted, but be defensive).
	if !a.hasLatest || !m.Created.Before(a.lastEventAt) {
		a.latest = m
		a.hasLatest = true
		a.lastEventAt = m.Created
		a.lastKind = m.Kind
		if m.Subject != "" {
			a.subject = m.Subject
		}
		a.orchestrator = m.Orchestrator
		a.fromProject = m.FromProject
		a.replyProject = m.ReplyProject
	} else if a.subject == "" && m.Subject != "" {
		a.subject = m.Subject
	}

	// Block tracking: a declared block stays active until a later message
	// clears it.
	if declaresBlock(m) {
		a.blockActive = true
		a.blockOwner = primaryRecipient(m)
	} else if a.blockActive && clearsBlock(m) {
		a.blockActive = false
	}
}

func (a *threadAccumulator) summarize(now time.Time, th Thresholds, agents []Agent) ThreadSummary {
	parts := keysSorted(a.participants)
	labels := keysSorted(a.labels)
	unread := keysSorted(a.unreadBy)

	status := deriveStatus(a)
	fresh := computeFreshness(a.lastEventAt, a.latest, now, governingThreshold(status, th))
	triage := computeTriage(a, status, fresh, now, th, agents)
	stale := isStale(a.lastEventAt, now, th.StaleAfter, triage)
	reason := classifyAttnReason(a, triage)
	historical := isHistoricalNeedsYou(triage, fresh, agents, th.OperatorHandle)

	return ThreadSummary{
		ID:           a.id,
		LatestID:     a.latest.ID,
		Participants: parts,
		Subject:      a.subject,
		Kind:         a.lastKind,
		Labels:       labels,
		Orchestrator: a.orchestrator,
		FromProject:  a.fromProject,
		ReplyProject: a.replyProject,
		Status:       status,
		LastEventAt:  a.lastEventAt,
		MessageCount: a.count,
		UnreadBy:     unread,
		Triage:       triage,
		Freshness:    fresh,
		Stale:        stale,
		Historical:   historical,
		AttnReason:   reason,
	}
}

func isHistoricalNeedsYou(triage Triage, fresh Freshness, agents []Agent, operatorHandle string) bool {
	if triage != TriageNeedsYou {
		return false
	}
	seenAgent := false
	hasActiveAgent := false
	for _, ag := range agents {
		if ag.Handle == operatorHandle {
			continue
		}
		seenAgent = true
		if agentCanCurrentlyNeedOperator(ag.Liveness) {
			hasActiveAgent = true
		}
	}
	if seenAgent && !hasActiveAgent {
		return true
	}
	if fresh.Age <= DefaultNeedsYouHistoricalAfter {
		return false
	}
	for _, ag := range agents {
		if ag.Handle == operatorHandle {
			continue
		}
		if !agentCanCurrentlyNeedOperator(ag.Liveness) {
			return false
		}
	}
	return seenAgent
}

func agentCanCurrentlyNeedOperator(l Liveness) bool {
	switch l {
	case LivenessAlive, LivenessWakeLive, LivenessDeadMailboxLive:
		return true
	default:
		return false
	}
}

// approveMarkers are case-insensitive substrings in a needs-you thread's subject
// (or kind) that mean the agent is PAUSED awaiting a human approval / permission
// / confirmation before it can proceed. The act-now reason.
var approveMarkers = []string{
	"approve", "approval", "permission", "allow", "proceed",
	"confirm", "[y/n]", "(y/n)", "y/n", "run this", "ok to",
}

// goalMarkers are case-insensitive substrings that mean the team signalled the
// work is DONE / the goal is reached and the human should review and close.
// "done" is deliberately NOT a bare substring here: it false-positives on
// ordinary words that embed it ("abandoned", "condoned", "redone"). The "done"
// signal — including the taught `DONE:` subject prefix — is matched as a whole
// word by hasGoalWord instead, while the broader goal phrases below stay
// substring matches.
var goalMarkers = []string{
	"complete", "completed", "shipped", "goal reached",
	"finished", "ready to close", "✅",
}

// hasGoalWord reports whether a lowercased subject carries a "done" goal signal
// as a whole word, WITHOUT the substring false positives of bare "done" (e.g.
// "abandoned", "condoned", "redone"). It matches the taught `done:` subject
// prefix and a standalone "done" token (bounded by non-letters), so
// "DONE: epic complete" / "all done" / "we are done" classify goal-reached
// while "abandoned the retry" does not.
func hasGoalWord(subjLower string) bool {
	const w = "done"
	for i := 0; i+len(w) <= len(subjLower); i++ {
		if subjLower[i:i+len(w)] != w {
			continue
		}
		if i > 0 && isASCIILetter(subjLower[i-1]) {
			continue
		}
		if j := i + len(w); j < len(subjLower) && isASCIILetter(subjLower[j]) {
			continue
		}
		return true
	}
	return false
}

// isASCIILetter reports whether b is an ASCII letter, used for word-boundary
// checks in hasGoalWord.
func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// classifyAttnReason derives WHY a needs-you thread needs the human from the
// subject of the message addressed to the operator. It returns AttnNone for any
// non-needs-you thread (the field is meaningful only on needs-you).
//
// Precedence: APPROVE (paused awaiting permission, act now) wins over
// GOAL-REACHED (review + close); a plain question with neither marker is
// AttnGeneric. Agents are taught (bootstrap + team-rules) to emit the goal
// signal as a `DONE:` subject prefix; that prefix and a standalone "done" word
// are matched by hasGoalWord (word-bounded, so "abandoned" does not trip it),
// while the broader goal phrases stay substring matches in goalMarkers.
func classifyAttnReason(a *threadAccumulator, triage Triage) AttnReason {
	if triage != TriageNeedsYou {
		return AttnNone
	}
	subj := strings.ToLower(a.subject)
	for _, mk := range approveMarkers {
		if strings.Contains(subj, mk) {
			return AttnApprove
		}
	}
	if hasGoalWord(subj) {
		return AttnGoalReached
	}
	for _, mk := range goalMarkers {
		if strings.Contains(subj, mk) {
			return AttnGoalReached
		}
	}
	return AttnGeneric
}

// isStale reports whether a thread is age-decayed: its last event is older than
// staleAfter. Needs-you uses the Historical flag instead of Stale when it is not
// current operator action. A thread with no recorded last-event time (zero) is
// not considered stale because there is no age to decay against.
func isStale(lastEventAt, now time.Time, staleAfter time.Duration, triage Triage) bool {
	if triage == TriageNeedsYou {
		return false
	}
	if lastEventAt.IsZero() || staleAfter <= 0 {
		return false
	}
	return now.Sub(lastEventAt) > staleAfter
}

// deriveStatus computes the thread lifecycle status from the LATEST message
// kind plus the active-block flag.
func deriveStatus(a *threadAccumulator) ThreadStatus {
	if a.blockActive {
		return ThreadBlocked
	}
	switch a.lastKind {
	case KindQuestion, KindReviewRequest:
		return ThreadAwaitingReply
	case KindDecision:
		// A decision addressed to someone (awaiting their ack/action) reads as
		// awaiting-reply; otherwise it is an open record.
		if len(a.latest.To) > 0 {
			return ThreadAwaitingReply
		}
		return ThreadOpen
	case KindAnswer, KindReviewResponse:
		return ThreadResolved
	default:
		return ThreadOpen
	}
}

// governingThreshold picks which staleness threshold applies to a thread's
// freshness, by status.
func governingThreshold(status ThreadStatus, th Thresholds) time.Duration {
	switch status {
	case ThreadAwaitingReply:
		return th.ReviewAge
	case ThreadBlocked:
		return th.AtRiskWait
	default:
		return th.AtRiskWait
	}
}

// computeFreshness measures the thread's age from its last event and flags
// staleness against the governing threshold.
func computeFreshness(at time.Time, latest Message, now time.Time, threshold time.Duration) Freshness {
	src := SourceObserved
	if !at.IsZero() {
		src = createdSource(latestHeaderCreated(latest))
	}
	age := time.Duration(0)
	if !at.IsZero() {
		age = now.Sub(at)
		if age < 0 {
			age = 0
		}
	}
	return Freshness{
		Source:   src,
		Observed: at,
		Age:      age,
		Stale:    threshold > 0 && age > threshold,
	}
}

// latestHeaderCreated reconstructs whether the latest message had an embedded
// created time (vs a filesystem-mtime fallback). We re-derive from the parsed
// time being non-zero and matching an embedded format is not recoverable post
// hoc, so we conservatively report embedded when Created is set and the message
// was schema-OK; mtime otherwise. (The raw header string is not retained on the
// Message, so this is the available signal.)
func latestHeaderCreated(m Message) string {
	if !m.Created.IsZero() && m.SchemaOK {
		return m.Created.UTC().Format(time.RFC3339Nano)
	}
	return ""
}

// computeTriage classifies a thread into a triage tier. Severity order is
// enforced by checking NeedsYou first, then Blocked, then Gated, then AtRisk.
func computeTriage(a *threadAccumulator, status ThreadStatus, fresh Freshness, now time.Time, th Thresholds, agents []Agent) Triage {
	op := th.OperatorHandle

	// NeedsYou: an unanswered ask addressed TO the operator, a block awaiting
	// the human, or explicit prose that says the thread is waiting for the
	// operator/user.
	if status == ThreadAwaitingReply || status == ThreadBlocked {
		if addressedTo(a.latest, op) {
			switch a.latest.Kind {
			case KindQuestion, KindReviewRequest, KindDecision:
				if operatorMessageNeedsAction(a.latest) && (operatorStillUnread(a, op) || a.latest.From != op) {
					return TriageNeedsYou
				}
			}
		}
		if a.blockActive && a.blockOwner == op {
			return TriageNeedsYou
		}
	}
	if declaresUserWait(a.latest) {
		return TriageNeedsYou
	}

	// Blocked: an explicitly declared, still-active block (owner may be another
	// agent).
	if a.blockActive {
		return TriageBlocked
	}

	// Gated: intentionally paused on release, QA, policy, or approval gates. A
	// user-waiting gate has already been classified as NeedsYou above.
	if declaresGate(a.latest) {
		return TriageGated
	}

	// AtRisk: agent<->agent unanswered review/question aging past thresholds, or
	// a quiet heartbeat on a participant.
	if status == ThreadAwaitingReply && fresh.Stale {
		return TriageAtRisk
	}
	if heartbeatQuiet(a.participants, agents, now, th) && status == ThreadAwaitingReply {
		return TriageAtRisk
	}
	if deadMailboxLiveParticipant(a.participants, agents) {
		return TriageAtRisk
	}

	return TriageClear
}

// addressedTo reports whether handle is among the message recipients.
func addressedTo(m Message, handle string) bool {
	for _, r := range m.To {
		if r == handle {
			return true
		}
	}
	return false
}

var operatorActionMarkers = []string{
	"?", "please", "need ", "needs ", "scope-check", "scope check",
	"please review", "review this", "review needed", "needs review",
	"approve", "approval", "confirm", "decide", "need decision", "decision needed",
	"choose", "answer", "reply", "input", "permission", "ok to",
	"can you", "should we", "action needed",
}

var operatorNoticePrefixes = []string{
	"ack", "ack:", "re:", "fyi", "status", "qa confirms", "correction",
	"closeout", "main promotion complete",
}

func operatorMessageNeedsAction(m Message) bool {
	if declaresUserWait(m) {
		return true
	}
	text := messageSignalText(m)
	if looksLikeOperatorNotice(text) && !hasDirectOperatorActionMarker(text) {
		return false
	}
	if hasOperatorActionMarker(text) {
		return true
	}
	return m.Kind == KindReviewRequest
}

func hasOperatorActionMarker(text string) bool {
	if hasDirectOperatorActionMarker(text) {
		return true
	}
	for _, mk := range goalMarkers {
		if strings.Contains(text, mk) {
			return true
		}
	}
	return hasGoalWord(text)
}

func hasDirectOperatorActionMarker(text string) bool {
	for _, mk := range operatorActionMarkers {
		if strings.Contains(text, mk) {
			return true
		}
	}
	return false
}

func looksLikeOperatorNotice(text string) bool {
	text = strings.TrimSpace(text)
	for _, prefix := range operatorNoticePrefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

// operatorStillUnread reports whether the operator still holds the latest copy
// unread (true also when we have no inbox observation for the operator, since
// absence of a read receipt cannot be treated as "answered").
func operatorStillUnread(a *threadAccumulator, op string) bool {
	if a.readBy[op] {
		return false
	}
	return true
}

// primaryRecipient returns the first recipient of a message (the block owner),
// or "" when none.
func primaryRecipient(m Message) string {
	if len(m.To) > 0 {
		return m.To[0]
	}
	return ""
}

// heartbeatQuiet reports whether any thread participant that is a discovered
// agent has gone quiet past the heartbeat threshold (last_seen too old), which
// makes an outstanding ask at-risk.
func heartbeatQuiet(participants map[string]bool, agents []Agent, now time.Time, th Thresholds) bool {
	for _, ag := range agents {
		if !participants[ag.Handle] {
			continue
		}
		// A genuinely ALIVE agent (its process verified live by signal-0 + binary
		// match) is NOT "quiet" just because its presence heartbeat FILE is stale.
		// Presence and process liveness can disagree (e.g. an agent that does not
		// rewrite presence.json on every turn); trusting a stale presence file
		// here flips every awaiting-reply thread the live agent participates in to
		// at-risk, inflating the count. Only a NOT-alive participant can be quiet.
		if ag.Liveness == LivenessAlive {
			continue
		}
		if ag.LastSeen.IsZero() {
			continue
		}
		if now.Sub(ag.LastSeen) > th.Heartbeat {
			return true
		}
	}
	return false
}

// deadMailboxLiveParticipant reports whether any thread participant is in the
// dead-mailbox-live state from PR1's liveness — a concrete at-risk signal.
func deadMailboxLiveParticipant(participants map[string]bool, agents []Agent) bool {
	for _, ag := range agents {
		if participants[ag.Handle] && ag.Liveness == LivenessDeadMailboxLive {
			return true
		}
	}
	return false
}

// buildEdges tallies directed from->to message counts across the session.
// Self-edges (from==to) are dropped. Edges are sorted for determinism.
func buildEdges(msgs []Message) []Edge {
	type key struct{ from, to string }
	counts := map[key]int{}
	for _, m := range msgs {
		// Edges are computed from the SENT direction once per message. A message
		// is observed in multiple recipient inboxes; count it once per
		// (from,to) by only counting the copy in the recipient's own inbox.
		for _, to := range m.To {
			if m.From == "" || to == "" || m.From == to {
				continue
			}
			// Count once per recipient copy: only when this is the recipient's
			// own inbox copy (m.Owner == to) OR the sender's outbox is not
			// scanned. To avoid double counting across multiple inbox copies of
			// the same message, count only the recipient-owned copy.
			if m.Owner != "" && m.Owner != to {
				continue
			}
			counts[key{m.From, to}]++
		}
	}
	edges := make([]Edge, 0, len(counts))
	for k, c := range counts {
		edges = append(edges, Edge{From: k.from, To: k.to, Count: c})
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})
	return edges
}

// buildTimeline derives human-readable state-transition events. It emits one
// event per thread for its current derived state (not a raw message dump),
// sorted by time. The phrasing reads like "qa blocked on cto".
func buildTimeline(threads []ThreadSummary, msgs []Message) []TimelineEvent {
	// Index latest message per thread for actor/owner phrasing.
	latest := map[string]Message{}
	for _, m := range msgs {
		cur, ok := latest[m.Thread]
		if !ok || !m.Created.Before(cur.Created) {
			latest[m.Thread] = m
		}
	}

	var events []TimelineEvent
	for _, t := range threads {
		m := latest[t.ID]
		summary := phraseTransition(t, m)
		if summary == "" {
			continue
		}
		events = append(events, TimelineEvent{
			At:      t.LastEventAt,
			Kind:    t.Kind,
			Summary: summary,
			Source:  t.ID,
		})
	}
	sort.SliceStable(events, func(i, j int) bool {
		if !events[i].At.Equal(events[j].At) {
			return events[i].At.Before(events[j].At)
		}
		return events[i].Source < events[j].Source
	})
	return events
}

// phraseTransition renders a human sentence for a thread's current state.
func phraseTransition(t ThreadSummary, m Message) string {
	from := m.From
	owner := primaryRecipient(m)
	switch t.Status {
	case ThreadBlocked:
		if from != "" && owner != "" {
			return from + " blocked on " + owner
		}
		if from != "" {
			return from + " declared a block"
		}
		return "block declared in " + t.ID
	case ThreadAwaitingReply:
		verb := "awaiting reply"
		switch m.Kind {
		case KindReviewRequest:
			verb = "awaiting review"
		case KindQuestion:
			verb = "asked a question"
		case KindDecision:
			verb = "awaiting decision ack"
		}
		if from != "" && owner != "" {
			return from + " -> " + owner + ": " + verb
		}
		if from != "" {
			return from + " " + verb
		}
		return verb + " in " + t.ID
	case ThreadResolved:
		if from != "" {
			return from + " resolved " + shortThread(t.ID)
		}
		return shortThread(t.ID) + " resolved"
	default:
		return ""
	}
}

// shortThread trims a namespace prefix for compact timeline phrasing.
func shortThread(id string) string {
	if i := strings.IndexByte(id, '/'); i >= 0 && i+1 < len(id) {
		return id[i+1:]
	}
	return id
}

// keysSorted returns the sorted keys of a string-set map.
func keysSorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
