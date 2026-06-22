package state

import (
	"sort"
	"strings"
	"time"
)

// AttentionQueueItem is the first-class operator queue row derived from AMQ
// thread evidence. It is read-only: NextAction is an action name a caller can
// map to an existing action registry entry, not an executable command invented
// by state.
type AttentionQueueItem struct {
	Kind         string
	Urgency      int
	WhoActs      string
	Thread       string
	Since        time.Time
	AgeSource    FreshnessSource
	Why          string
	Requester    string
	Recipient    string
	Session      string
	Profile      string
	BodyPreview  string
	NextAction   string
	Answered     bool
	Acknowledged bool
	Conflict     bool
}

const (
	AttentionNeedsYouGate   = "needs-you-gate"
	AttentionBlockedAsk     = "blocker-question"
	AttentionStaleDirective = "stale-directive"
	AttentionReviewReady    = "review-ready"
	AttentionStaleWorker    = "stale-worker"
	AttentionProgress       = "progress"
)

func buildAttentionQueue(threads []ThreadSummary, agents []Agent, th Thresholds, session string) []AttentionQueueItem {
	var items []AttentionQueueItem
	openGate := false
	for _, t := range threads {
		if t.Gate && !t.GateAnswered {
			openGate = true
			break
		}
	}
	for _, t := range threads {
		switch {
		case t.Triage == TriageNeedsYou && !t.Historical:
			kind := AttentionNeedsYouGate
			if !t.Gate {
				kind = AttentionBlockedAsk
			}
			items = append(items, threadQueueItem(t, kind, 0, th.OperatorHandle, session))
		case t.Directive && !t.DirectiveAcked:
			item := threadQueueItem(t, AttentionStaleDirective, 2, th.OperatorHandle, session)
			item.WhoActs = nonOperatorDirectiveRecipient(t, th.OperatorHandle)
			item.Acknowledged = false
			item.Conflict = openGate
			if item.Conflict {
				item.Why = "directive overlaps open operator gate"
			}
			items = append(items, item)
		case t.Status == ThreadAwaitingReply && t.Kind == KindReviewRequest:
			items = append(items, threadQueueItem(t, AttentionReviewReady, 3, th.OperatorHandle, session))
		case t.Triage == TriageBlocked || t.Triage == TriageAtRisk || t.Triage == TriageGated:
			items = append(items, threadQueueItem(t, AttentionBlockedAsk, 1, th.OperatorHandle, session))
		case t.Status == ThreadOpen && !t.Stale && !t.Historical:
			items = append(items, threadQueueItem(t, AttentionProgress, 5, th.OperatorHandle, session))
		}
	}
	for _, ag := range agents {
		if agentOperational(ag) {
			continue
		}
		items = append(items, AttentionQueueItem{
			Kind:       AttentionStaleWorker,
			Urgency:    4,
			WhoActs:    strings.TrimSpace(ag.Handle),
			Session:    session,
			Profile:    normalizedProfile(ag.TeamProfile),
			Why:        "worker is not operational",
			NextAction: "agent_resume",
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Profile != items[j].Profile {
			return items[i].Profile < items[j].Profile
		}
		if items[i].Session != items[j].Session {
			return items[i].Session < items[j].Session
		}
		if items[i].Urgency != items[j].Urgency {
			return items[i].Urgency < items[j].Urgency
		}
		if !items[i].Since.Equal(items[j].Since) {
			return items[i].Since.Before(items[j].Since)
		}
		return items[i].Thread < items[j].Thread
	})
	return items
}

func threadQueueItem(t ThreadSummary, kind string, urgency int, operatorHandle, session string) AttentionQueueItem {
	requester := strings.TrimSpace(t.LastFrom)
	recipient := primaryThreadRecipient(t, requester)
	item := AttentionQueueItem{
		Kind:         kind,
		Urgency:      urgency,
		WhoActs:      queueActor(t, kind, operatorHandle),
		Thread:       t.ID,
		Since:        t.LastEventAt,
		AgeSource:    t.Freshness.Source,
		Why:          queueWhy(t, kind),
		Requester:    requester,
		Recipient:    recipient,
		Session:      session,
		Profile:      queueProfile(t),
		BodyPreview:  bodyPreview(t.LatestBody),
		NextAction:   queueNextAction(kind),
		Answered:     t.GateAnswered,
		Acknowledged: t.DirectiveAcked,
		Conflict:     t.DirectiveConflict,
	}
	return item
}

func queueActor(t ThreadSummary, kind string, operatorHandle string) string {
	switch kind {
	case AttentionNeedsYouGate:
		return operatorHandle
	case AttentionStaleDirective:
		return nonOperatorDirectiveRecipient(t, operatorHandle)
	default:
		if t.NeedsYouOwner != "" {
			return t.NeedsYouOwner
		}
		return primaryThreadRecipient(t, t.LastFrom)
	}
}

func queueWhy(t ThreadSummary, kind string) string {
	switch kind {
	case AttentionNeedsYouGate:
		if t.Gate {
			if t.AttnReason != "" {
				return "operator gate: " + string(t.AttnReason)
			}
			return "operator gate"
		}
		if t.AttnReason != "" {
			return "needs operator: " + string(t.AttnReason)
		}
		return "needs operator"
	case AttentionStaleDirective:
		return "operator directive awaiting lead ack"
	case AttentionReviewReady:
		return "review request awaiting response"
	case AttentionStaleWorker:
		return "worker is not operational"
	case AttentionProgress:
		return "latest progress"
	default:
		if t.Triage != "" && t.Triage != TriageClear {
			return string(t.Triage)
		}
		return string(t.Status)
	}
}

func queueNextAction(kind string) string {
	switch kind {
	case AttentionNeedsYouGate:
		return "reply"
	case AttentionStaleDirective:
		return "message_wait"
	case AttentionReviewReady:
		return "thread_context"
	case AttentionStaleWorker:
		return "agent_resume"
	default:
		return "thread_context"
	}
}

func nonOperatorDirectiveRecipient(t ThreadSummary, operatorHandle string) string {
	for _, p := range t.Participants {
		if p != "" && p != operatorHandle {
			return p
		}
	}
	return ""
}

func primaryThreadRecipient(t ThreadSummary, requester string) string {
	for _, p := range t.Participants {
		if p != "" && p != requester {
			return p
		}
	}
	return ""
}

func queueProfile(t ThreadSummary) string {
	return "default"
}

func normalizedProfile(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return "default"
	}
	return profile
}

func bodyPreview(body string) string {
	body = strings.Join(strings.Fields(strings.TrimSpace(body)), " ")
	const max = 160
	if len(body) <= max {
		return body
	}
	return strings.TrimSpace(body[:max-3]) + "..."
}
