package state

import (
	"sort"
	"strings"
	"time"
)

// DefaultOperatorHandle is the mailbox handle that represents the human
// operator. In real data the agents are cpo/cto/senior-dev/qa AND `user`;
// `user` is the concrete signal for the "needs-you" triage tier. It is
// configurable via Thresholds.OperatorHandle.
const DefaultOperatorHandle = "user"

// ThreadStatus is the derived lifecycle state of a thread, computed from the
// LATEST message kind plus unread/block markers — never read from disk.
type ThreadStatus string

const (
	// ThreadOpen: an ordinary in-progress thread with nothing outstanding.
	ThreadOpen ThreadStatus = "open"
	// ThreadAwaitingReply: the latest message is an unanswered question /
	// review_request / decision — someone owes a response.
	ThreadAwaitingReply ThreadStatus = "awaiting-reply"
	// ThreadBlocked: a block has been explicitly declared in the thread and not
	// yet cleared.
	ThreadBlocked ThreadStatus = "blocked"
	// ThreadResolved: the latest message is a terminal answer/review_response,
	// closing out the outstanding ask.
	ThreadResolved ThreadStatus = "resolved"
)

// Triage is one of the computed headline tiers (plus Clear). Order of
// severity: NeedsYou > Blocked > Gated > AtRisk > Clear.
type Triage string

const (
	// TriageNeedsYou: an actionable unanswered message addressed TO the operator
	// handle, explicit waiting-for-operator prose, or a declared block awaiting
	// the human. The concrete "the human must act" signal.
	TriageNeedsYou Triage = "needs-you"
	// TriageBlocked: an agent has explicitly declared a block (kind/marker); the
	// owner may be another agent.
	TriageBlocked Triage = "blocked"
	// TriageGated: work is intentionally paused by a policy, release, QA, or
	// authorization gate. It is visible attention, but not a peer blocker.
	TriageGated Triage = "gated"
	// TriageAtRisk: an agent<->agent unanswered review/question aging past
	// ReviewAge, or a heartbeat gone quiet past Heartbeat, or the
	// dead-mailbox-live case. Aging, not yet a hard block.
	TriageAtRisk Triage = "at-risk"
	// TriageClear: nothing outstanding.
	TriageClear Triage = "clear"
)

// AttnReason classifies WHY a needs-you thread needs the human, derived from the
// message addressed to the operator handle. It is meaningful ONLY on a needs-you
// thread (Triage == TriageNeedsYou); on every other thread it is AttnNone.
//
// Agents are taught (bootstrap + team-rules) the emit convention that makes
// these fire on real data: a thread to "user" with subject `APPROVAL: ...`
// classifies AttnApprove, and `DONE: ...` classifies AttnGoalReached. When no
// thread is addressed to "user" the board simply shows AttnNone — that is
// correct, not a bug. Tests seed user-addressed approve/done threads to exercise
// the classify + render path end-to-end against the taught prefixes.
type AttnReason string

const (
	// AttnNone: not a needs-you thread (or no reason classified).
	AttnNone AttnReason = ""
	// AttnApprove: the agent is paused awaiting a human to approve an action / a
	// command run / a permission grant. The hot, act-now reason — sorted first.
	AttnApprove AttnReason = "approve"
	// AttnGoalReached: the team signalled done / goal reached — the human is asked
	// to review and close. A distinct REVIEW reason; it must NOT read as a bare
	// "healthy / nothing to do" green check, so it stays inside NEEDS YOU below
	// approve.
	AttnGoalReached AttnReason = "goal-reached"
	// AttnGeneric: a plain question to the human with no approve/done markers.
	AttnGeneric AttnReason = "generic"
)

// Rank orders reasons for the NEEDS YOU block: approve (act now) above
// goal-reached (review) above generic above none. Lower sorts first. Exported so
// a render layer can sort needs-you items across sessions/projects by the SAME
// precedence the state layer uses.
func (a AttnReason) Rank() int {
	switch a {
	case AttnApprove:
		return 0
	case AttnGoalReached:
		return 1
	case AttnGeneric:
		return 2
	default:
		return 3
	}
}

// FreshnessSource records WHERE a derived time came from, so the console can be
// honest about how much to trust an age. embedded-time is most trustworthy;
// mtime is a filesystem fallback; observed is the snapshot clock.
type FreshnessSource string

const (
	SourceEmbedded FreshnessSource = "embedded-time"
	SourceMtime    FreshnessSource = "mtime"
	SourceObserved FreshnessSource = "observed"
)

// Freshness annotates a derived field with how old its underlying signal is and
// whether that age has crossed the relevant staleness threshold.
type Freshness struct {
	Source   FreshnessSource
	Observed time.Time     // the timestamp the age is measured from
	Age      time.Duration // now - Observed (>=0)
	Stale    bool          // Age exceeded the governing threshold
}

// Thresholds tune the time-based triage/freshness math. Zero values fall back
// to the documented defaults via withThresholdDefaults, so callers may set only
// the fields they care about.
type Thresholds struct {
	// AtRiskWait: an awaiting-reply thread older than this is at risk.
	AtRiskWait time.Duration
	// Heartbeat: presence/last-activity older than this is a quiet agent.
	Heartbeat time.Duration
	// ReviewAge: an unanswered review/question older than this is at risk.
	ReviewAge time.Duration
	// StaleAfter: a thread whose last event is older than this is STALE. Stale
	// threads (and the at-risk/blocked triage they carry) are age-decayed: they
	// weight ~0 for attention ranking and render dim/parenthesized rather than as
	// live attention. This is the window that separates "what is alive / what
	// needs me now" from ancient noise on long-stopped squads.
	StaleAfter time.Duration
	// OperatorHandle is the human's mailbox handle (default "user").
	OperatorHandle string
}

// Default threshold values.
const (
	DefaultAtRiskWait              = 30 * time.Minute
	DefaultHeartbeat               = 90 * time.Second
	DefaultReviewAge               = 45 * time.Minute
	DefaultNeedsYouHistoricalAfter = 24 * time.Hour
	// DefaultStaleAfter: 72h. A thread untouched for three days is treated as
	// stale — its triage no longer counts as LIVE attention.
	DefaultStaleAfter = 72 * time.Hour
)

func withThresholdDefaults(t Thresholds) Thresholds {
	if t.AtRiskWait <= 0 {
		t.AtRiskWait = DefaultAtRiskWait
	}
	if t.Heartbeat <= 0 {
		t.Heartbeat = DefaultHeartbeat
	}
	if t.ReviewAge <= 0 {
		t.ReviewAge = DefaultReviewAge
	}
	if t.StaleAfter <= 0 {
		t.StaleAfter = DefaultStaleAfter
	}
	if strings.TrimSpace(t.OperatorHandle) == "" {
		t.OperatorHandle = DefaultOperatorHandle
	}
	return t
}

// ThreadSummary collapses every message sharing a canonical thread id into one
// derived row. Participants are the union of from+to across the thread.
type ThreadSummary struct {
	ID           string
	LatestID     string
	Participants []string // union of from + to, sorted
	Subject      string   // latest non-empty subject
	Kind         Kind     // latest recognized kind
	Labels       []string // union of AMQ labels observed on messages in the thread
	Orchestrator string   // latest orchestrator metadata, when present
	FromProject  string   // latest cross-project sender metadata, when present
	ReplyProject string   // latest cross-project reply metadata, when present
	Status       ThreadStatus
	LastEventAt  time.Time
	MessageCount int
	UnreadBy     []string // recipients still holding a copy in inbox/new
	Triage       Triage
	Freshness    Freshness
	// Stale is true when now - LastEventAt exceeds Thresholds.StaleAfter. A stale
	// thread's triage is age-decayed: it is NOT counted as LIVE attention and is
	// rendered dim/parenthesized. Needs-you uses Historical instead of Stale when
	// the ask is old or no active agent is present to be waiting on the operator.
	Stale bool
	// Historical is true for a needs-you ask retained for context when it is not
	// current operator action, such as an old ask or a stopped session with no
	// active non-operator agent.
	Historical bool
	// AttnReason classifies WHY a needs-you thread needs the human (approve vs
	// goal-reached vs a plain question). It is AttnNone on every non-needs-you
	// thread. See AttnReason.
	AttnReason AttnReason
}

// Edge is a directed from->to message count across a session.
type Edge struct {
	From  string
	To    string
	Count int
}

// TimelineEvent is a DERIVED, human-readable state transition (not a raw
// message dump). Summary reads like "qa blocked on cto".
type TimelineEvent struct {
	At      time.Time
	Kind    Kind
	Summary string
	Source  string // thread id the transition came from
}

// TriageRollup is the per-session / per-snapshot headline count the console and
// board render: "N needs-you, M at-risk, K blocked". The at-risk/blocked counts
// are split into LIVE (recent, non-stale) and STALE (age-decayed) so a surface
// can lead with what is alive and demote ancient noise. NeedsYou is live only
// when it represents current operator action; historical needs-you is counted
// separately.
type TriageRollup struct {
	NeedsYou           int
	NeedsYouHistorical int // old retained needs-you asks, not current operator action
	AtRisk             int // LIVE at-risk (non-stale)
	Blocked            int // LIVE blocked (non-stale)
	Gated              int // LIVE intentionally gated (non-stale)
	AtRiskStale        int // age-decayed at-risk
	BlockedStale       int // age-decayed blocked
	GatedStale         int // age-decayed gated
	Clear              int
}

// Add folds another rollup into this one.
func (r *TriageRollup) Add(o TriageRollup) {
	r.NeedsYou += o.NeedsYou
	r.NeedsYouHistorical += o.NeedsYouHistorical
	r.AtRisk += o.AtRisk
	r.Blocked += o.Blocked
	r.Gated += o.Gated
	r.AtRiskStale += o.AtRiskStale
	r.BlockedStale += o.BlockedStale
	r.GatedStale += o.GatedStale
	r.Clear += o.Clear
}

// HasLiveAttention reports whether the rollup carries any LIVE (non-stale)
// outstanding item: needs-you, live at-risk, live blocked, or live gated.
func (r TriageRollup) HasLiveAttention() bool {
	return r.NeedsYou > 0 || r.AtRisk > 0 || r.Blocked > 0 || r.Gated > 0
}

// TopAttnReason returns the most urgent needs-you reason across a session's
// threads (approve > goal-reached > generic), or AttnNone when the session has
// no needs-you thread. Surfaces the single reason a session-level summary should
// lead with.
func (c Coordination) TopAttnReason() AttnReason {
	best := AttnNone
	for _, t := range c.Threads {
		if t.Triage != TriageNeedsYou || t.Historical || t.AttnReason == AttnNone {
			continue
		}
		if best == AttnNone || t.AttnReason.Rank() < best.Rank() {
			best = t.AttnReason
		}
	}
	return best
}

// NeedsYouThreads returns the needs-you threads carried by a coordination view,
// sorted for a NEEDS YOU listing: by reason rank (approve, then goal-reached,
// then generic), then oldest-first within a reason (the longest-waiting human
// ask leads), then by id for determinism.
func (c Coordination) NeedsYouThreads() []ThreadSummary {
	var out []ThreadSummary
	for _, t := range c.Threads {
		if t.Triage == TriageNeedsYou {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := out[i].AttnReason.Rank(), out[j].AttnReason.Rank()
		if ri != rj {
			return ri < rj
		}
		if !out[i].LastEventAt.Equal(out[j].LastEventAt) {
			return out[i].LastEventAt.Before(out[j].LastEventAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// countTriage tallies a bare triage tier into the rollup's LIVE buckets. It is
// retained for callers that recompute a rollup from a triage value alone (e.g.
// the status board's filtered re-rollup) and have no per-thread staleness in
// hand; such callers treat every counted item as live. Prefer countThread when
// a ThreadSummary (with its Stale flag) is available.
func (r *TriageRollup) countTriage(t Triage) {
	switch t {
	case TriageNeedsYou:
		r.NeedsYou++
	case TriageAtRisk:
		r.AtRisk++
	case TriageBlocked:
		r.Blocked++
	case TriageGated:
		r.Gated++
	default:
		r.Clear++
	}
}

// countThread tallies one thread's triage into the rollup, routing at-risk and
// blocked/gated into the LIVE or STALE bucket by the thread's Stale flag, and
// routing historical needs-you away from the live operator-action bucket.
func (r *TriageRollup) countThread(t ThreadSummary) {
	switch t.Triage {
	case TriageNeedsYou:
		if t.Historical {
			r.NeedsYouHistorical++
		} else {
			r.NeedsYou++
		}
	case TriageAtRisk:
		if t.Stale {
			r.AtRiskStale++
		} else {
			r.AtRisk++
		}
	case TriageBlocked:
		if t.Stale {
			r.BlockedStale++
		} else {
			r.Blocked++
		}
	case TriageGated:
		if t.Stale {
			r.GatedStale++
		} else {
			r.Gated++
		}
	default:
		r.Clear++
	}
}

// String renders the rollup the way the board/console label it. Stale counts are
// shown in parentheses only when present, keeping the live counts primary.
func (r TriageRollup) String() string {
	s := itoa(r.NeedsYou) + " needs-you, " + itoa(r.AtRisk) + " at-risk, " + itoa(r.Blocked) + " blocked"
	if r.Gated > 0 {
		s += ", " + itoa(r.Gated) + " gated"
	}
	if r.NeedsYouHistorical > 0 || r.AtRiskStale > 0 || r.BlockedStale > 0 || r.GatedStale > 0 {
		stale := itoa(r.AtRiskStale) + " at-risk, " + itoa(r.BlockedStale) + " blocked stale"
		if r.NeedsYouHistorical > 0 {
			stale = itoa(r.NeedsYouHistorical) + " needs-you historical, " + stale
		}
		if r.GatedStale > 0 {
			stale += ", " + itoa(r.GatedStale) + " gated stale"
		}
		s += " (" + stale + ")"
	}
	return s
}

// blockMarkers are case-insensitive substrings in a message body that declare a
// block when the kind itself is not block-bearing. Defensive: real data uses
// "NO-GO" / "blocked" / "blocker" prose to declare blocks.
var blockMarkers = []string{"no-go", "blocked on", "blocker:", "i am blocked", "we are blocked", "blocking:"}

// declaresBlock reports whether a message declares a block, via an explicit
// marker in the body. (There is no dedicated block kind on disk; blocks are
// declared in review_response/status prose, which is why this is defensive.)
func declaresBlock(m Message) bool {
	body := strings.ToLower(m.Body)
	subj := strings.ToLower(m.Subject)
	for _, mk := range blockMarkers {
		if strings.Contains(body, mk) || strings.Contains(subj, mk) {
			return true
		}
	}
	return false
}

// userWaitMarkers are conservative prose signals that the latest thread event
// is waiting for the human/operator, even when it was emitted as a status note
// instead of a message addressed to the operator mailbox.
var userWaitMarkers = []string{
	"waiting for instructions",
	"awaiting instructions",
	"need instructions",
	"needs instructions",
	"waiting for direction",
	"awaiting direction",
	"need direction",
	"needs direction",
	"waiting on user",
	"waiting for user",
	"awaiting user",
	"need user",
	"needs user",
	"user action",
	"user approval",
	"user decision",
	"waiting on operator",
	"waiting for operator",
	"awaiting operator",
	"operator action",
	"operator approval",
	"operator decision",
	"human action",
	"human approval",
	"human decision",
	"waiting for scope",
	"awaiting scope",
	"need scope",
	"needs scope",
	"release scope",
	"choose next action",
	"action needed",
}

var userWaitNegationMarkers = []string{
	"no operator action",
	"no user action",
	"no human action",
	"no action requested",
	"no action needed",
	"not waiting for operator",
	"not waiting for user",
	"not waiting for human",
}

func declaresUserWait(m Message) bool {
	text := messageSignalText(m)
	for _, mk := range userWaitNegationMarkers {
		if strings.Contains(text, mk) {
			return false
		}
	}
	for _, mk := range userWaitMarkers {
		if strings.Contains(text, mk) {
			return true
		}
	}
	return false
}

// gateMarkers are prose signals for intentional governance/approval pauses.
// They render as gated unless a more explicit user-wait marker above says the
// operator must act now.
var gateMarkers = []string{
	"approval gate",
	"policy gate",
	"qa gate",
	"qa gates",
	"release gate",
	"governance gate",
	"authorization gate",
	"waiting for approval",
	"awaiting approval",
	"pending approval",
	"paused on",
	"paused until",
	"until cto release",
	"cto release",
	"qa approval",
	"release authorized",
	"gates are authorized",
	"authorization required",
	"governance-gated",
	"approval-gated",
	"policy-gated",
}

func declaresGate(m Message) bool {
	text := messageSignalText(m)
	for _, mk := range gateMarkers {
		if strings.Contains(text, mk) {
			return true
		}
	}
	return false
}

func messageSignalText(m Message) string {
	return strings.ToLower(m.Subject + "\n" + m.Body)
}

// clearsBlock reports whether a message clears a previously-declared block.
// "GO" / "unblocked" / "resolved" signal forward progress.
func clearsBlock(m Message) bool {
	if m.Kind == KindReviewResponse || m.Kind == KindAnswer {
		body := strings.ToLower(m.Body)
		// A bare "GO" decision (not "NO-GO") clears.
		if (strings.Contains(body, "\ngo ") || strings.HasPrefix(body, "go ") ||
			strings.Contains(body, "go for") || strings.Contains(body, "unblocked") ||
			strings.Contains(body, "resolved")) && !declaresBlock(m) {
			return true
		}
	}
	return false
}
