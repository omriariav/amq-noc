package state

import (
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ConversationTranscript scans both participants' mailboxes under sessionRoot
// and returns every message exchanged between them (either direction), oldest
// first, deduplicated by message id (one message can sit as a copy in both
// inboxes), capped to the NEWEST limit messages. Read-only filesystem scan
// over the same maildirs the snapshot already reads.
//
// This is conversation mode's data source (amq-noc#27): ThreadSummary carries
// only the latest body, but a dialogue needs every body. The filter is by
// PARTICIPANTS, not by thread - the operator's conversation with the lead is
// every message between them wherever it lives (the p2p thread plus
// lead-raised gate asks), and nothing more.
func ConversationTranscript(sessionRoot, a, b string, limit int) []Message {
	sessionRoot = strings.TrimSpace(sessionRoot)
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if sessionRoot == "" || a == "" || b == "" || strings.EqualFold(a, b) {
		return nil
	}
	seen := map[string]bool{}
	var out []Message
	for _, handle := range []string{a, b} {
		dir := filepath.Join(sessionRoot, "agents", handle)
		msgs, _ := scanMailbox(dir, handle, time.Now)
		for _, m := range msgs {
			if !messageBetween(m, a, b) {
				continue
			}
			key := strings.TrimSpace(m.ID)
			if key == "" {
				key = m.From + "|" + m.Thread + "|" + m.Created.Format(time.RFC3339Nano) + "|" + m.Subject
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Created.Equal(out[j].Created) {
			return out[i].Created.Before(out[j].Created)
		}
		return out[i].ID < out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// messageBetween reports whether m travels between a and b, either direction.
func messageBetween(m Message, a, b string) bool {
	from := strings.TrimSpace(m.From)
	if strings.EqualFold(from, a) {
		return messageAddressedTo(m, b)
	}
	if strings.EqualFold(from, b) {
		return messageAddressedTo(m, a)
	}
	return false
}

// messageAddressedTo reports whether handle is among m's recipients.
func messageAddressedTo(m Message, handle string) bool {
	for _, to := range m.To {
		if strings.EqualFold(strings.TrimSpace(to), handle) {
			return true
		}
	}
	return false
}
