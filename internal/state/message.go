package state

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"time"
)

// MessageSchema is the on-disk schema version this parser understands. Every
// message frontmatter carries "schema":1; a mismatch is tolerated but recorded
// as a degraded read (see Warning) so the console can surface it instead of
// silently trusting fields it may not understand.
const MessageSchema = 1

// Priority is a message priority. It is OPTIONAL on disk (~absent in a small
// fraction of messages) and defaults to PriorityNormal when missing/unknown.
type Priority string

const (
	PriorityNormal Priority = "normal"
	PriorityUrgent Priority = "urgent"
	PriorityLow    Priority = "low"
)

// Kind is a message kind. It is OPTIONAL on disk; an absent or unknown value
// is preserved verbatim (KindUnknown) and never assumed to be any tier-bearing
// kind. Only the explicitly recognized kinds drive triage/status transitions.
type Kind string

const (
	KindUnknown        Kind = ""
	KindStatus         Kind = "status"
	KindTodo           Kind = "todo"
	KindAnswer         Kind = "answer"
	KindReviewRequest  Kind = "review_request"
	KindReviewResponse Kind = "review_response"
	KindDecision       Kind = "decision"
	KindQuestion       Kind = "question"
)

// MailboxState is where a message file was found in a maildir. new=unread,
// cur=read; tmp (in-flight) is deliberately IGNORED by the scanner and never
// produces a Message.
type MailboxState string

const (
	MailboxNew MailboxState = "new" // UNREAD by the owning handle
	MailboxCur MailboxState = "cur" // READ by the owning handle
)

// messageHeader mirrors the ---json frontmatter block of a message file. All
// fields are decoded leniently: `to` is always an array on disk but we tolerate
// a bare string too; priority/kind may be absent.
type messageHeader struct {
	Schema       int      `json:"schema"`
	ID           string   `json:"id"`
	From         string   `json:"from"`
	To           []string `json:"to"`
	Thread       string   `json:"thread"`
	Subject      string   `json:"subject"`
	Created      string   `json:"created"`
	Priority     string   `json:"priority"`
	Kind         string   `json:"kind"`
	ReplyTo      string   `json:"reply_to"`
	Labels       []string `json:"labels"`
	Orchestrator string   `json:"orchestrator"`
	FromProject  string   `json:"from_project"`
	ReplyProject string   `json:"reply_project"`
}

// Message is one parsed maildir message: its decoded header fields plus the
// physical facts the scanner observed (which mailbox state, which owning
// handle's inbox it sat in, the file path, and the body).
type Message struct {
	ID        string
	From      string
	To        []string
	Thread    string // canonicalized via canonicalThreadID
	RawThread string // the thread id exactly as written on disk
	Subject   string
	Created   time.Time
	Priority  Priority
	Kind      Kind
	ReplyTo   string
	Labels    []string
	// Integration/routing metadata is optional AMQ context for federated or
	// orchestrator-originated traffic.
	Orchestrator string
	FromProject  string
	ReplyProject string
	Body         string

	// Owner is the handle whose inbox this copy was found in.
	Owner string
	// State is new (unread by Owner) or cur (read by Owner).
	State MailboxState
	// Path is the absolute path of the message file on disk.
	Path string
	// SchemaOK reports whether the on-disk schema matched MessageSchema. When
	// false the message is still surfaced but treated as DEGRADED: callers
	// should not over-trust optional fields and a Warning is recorded.
	SchemaOK bool
}

// Warning records a non-fatal problem encountered while scanning a mailbox: a
// torn/partial/invalid file that was skipped, a schema mismatch that degraded a
// read, or a thread id that had to be repaired. Warnings never abort a scan.
type Warning struct {
	Path   string
	Reason string
}

// normalizePriority maps an on-disk priority string to a Priority, defaulting
// to PriorityNormal for absent/unknown values (never assume present).
func normalizePriority(s string) Priority {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "urgent":
		return PriorityUrgent
	case "low":
		return PriorityLow
	case "normal":
		return PriorityNormal
	default:
		return PriorityNormal
	}
}

// normalizeKind maps an on-disk kind string to a Kind. Unknown/absent values
// are preserved as KindUnknown and never coerced into a tier-bearing kind.
func normalizeKind(s string) Kind {
	switch Kind(strings.ToLower(strings.TrimSpace(s))) {
	case KindStatus:
		return KindStatus
	case KindTodo:
		return KindTodo
	case KindAnswer:
		return KindAnswer
	case KindReviewRequest:
		return KindReviewRequest
	case KindReviewResponse:
		return KindReviewResponse
	case KindDecision:
		return KindDecision
	case KindQuestion:
		return KindQuestion
	default:
		return KindUnknown
	}
}

// parseMessageFile reads and decodes one maildir message file. It splits the
// leading ---json ... --- fence, json.Unmarshal's the header, and returns the
// Message plus the body. A torn/partial/invalid file is NEVER fatal: the
// returned ok is false and the error carries a human reason so the caller can
// record a Warning and skip the file. It never panics.
//
// owner/state are the physical facts the scanner already knows (which inbox the
// file was found in); they are stamped onto the Message so downstream code can
// compute unread-by without re-walking the tree.
func parseMessageFile(path, owner string, state MailboxState, now func() time.Time) (Message, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Message{}, false, err
	}
	header, body, perr := splitFrontmatter(data)
	if perr != nil {
		return Message{}, false, perr
	}

	var h messageHeader
	if err := json.Unmarshal([]byte(header), &h); err != nil {
		// Tolerate a bare-string `to` by retrying with a lenient shape before
		// giving up: some malformed/older writers emit `"to":"cto"`.
		if alt, ok := tryLenientHeader(header); ok {
			h = alt
		} else {
			return Message{}, false, &parseError{reason: "invalid frontmatter json: " + err.Error()}
		}
	}

	m := Message{
		ID:           strings.TrimSpace(h.ID),
		From:         strings.TrimSpace(h.From),
		To:           cleanRecipients(h.To),
		RawThread:    h.Thread,
		Thread:       canonicalThreadID(h.Thread),
		Subject:      strings.TrimSpace(h.Subject),
		Priority:     normalizePriority(h.Priority),
		Kind:         normalizeKind(h.Kind),
		ReplyTo:      strings.TrimSpace(h.ReplyTo),
		Labels:       cleanLabels(h.Labels),
		Orchestrator: strings.TrimSpace(h.Orchestrator),
		FromProject:  strings.TrimSpace(h.FromProject),
		ReplyProject: strings.TrimSpace(h.ReplyProject),
		Body:         body,
		Owner:        owner,
		State:        state,
		Path:         path,
		SchemaOK:     h.Schema == MessageSchema,
	}
	m.Created = parseCreated(h.Created, path, now)

	// A message with neither an id nor a sender is structurally useless; treat
	// it as torn so it is skipped-with-warning rather than polluting threads.
	if m.ID == "" && m.From == "" {
		return Message{}, false, &parseError{reason: "frontmatter missing both id and from"}
	}
	return m, true, nil
}

// parseError is a small typed error so callers can phrase warnings uniformly.
type parseError struct{ reason string }

func (e *parseError) Error() string { return e.reason }

// splitFrontmatter separates the leading ---json fenced header from the body.
// The expected shape is:
//
//	---json
//	{ ...json... }
//	---
//	body...
//
// It tolerates a bare leading `---` fence (no `json` tag) and CRLF. It returns
// an error (never panics) when no closing fence is found.
func splitFrontmatter(data []byte) (header, body string, err error) {
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	if !sc.Scan() {
		return "", "", &parseError{reason: "empty file"}
	}
	first := strings.TrimSpace(strings.TrimRight(sc.Text(), "\r"))
	if first != "---json" && first != "---" {
		return "", "", &parseError{reason: "missing ---json frontmatter fence"}
	}

	var headerLines []string
	closed := false
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "---" {
			closed = true
			break
		}
		headerLines = append(headerLines, line)
	}
	if err := sc.Err(); err != nil {
		return "", "", err
	}
	if !closed {
		return "", "", &parseError{reason: "unterminated frontmatter (no closing ---)"}
	}

	var bodyLines []string
	for sc.Scan() {
		bodyLines = append(bodyLines, strings.TrimRight(sc.Text(), "\r"))
	}
	if err := sc.Err(); err != nil {
		return "", "", err
	}
	return strings.Join(headerLines, "\n"), strings.TrimSpace(strings.Join(bodyLines, "\n")), nil
}

// tryLenientHeader retries decoding with `to` as a free-form value so a bare
// string recipient (`"to":"cto"`) survives. Returns ok=false if it still fails.
func tryLenientHeader(header string) (messageHeader, bool) {
	var raw struct {
		Schema       int             `json:"schema"`
		ID           string          `json:"id"`
		From         string          `json:"from"`
		To           json.RawMessage `json:"to"`
		Thread       string          `json:"thread"`
		Subject      string          `json:"subject"`
		Created      string          `json:"created"`
		Priority     string          `json:"priority"`
		Kind         string          `json:"kind"`
		ReplyTo      string          `json:"reply_to"`
		Labels       []string        `json:"labels"`
		Orchestrator string          `json:"orchestrator"`
		FromProject  string          `json:"from_project"`
		ReplyProject string          `json:"reply_project"`
	}
	if err := json.Unmarshal([]byte(header), &raw); err != nil {
		return messageHeader{}, false
	}
	h := messageHeader{
		Schema: raw.Schema, ID: raw.ID, From: raw.From, Thread: raw.Thread,
		Subject: raw.Subject, Created: raw.Created, Priority: raw.Priority,
		Kind: raw.Kind, ReplyTo: raw.ReplyTo, Labels: raw.Labels,
		Orchestrator: raw.Orchestrator, FromProject: raw.FromProject, ReplyProject: raw.ReplyProject,
	}
	if len(raw.To) > 0 {
		var arr []string
		if err := json.Unmarshal(raw.To, &arr); err == nil {
			h.To = arr
		} else {
			var one string
			if err := json.Unmarshal(raw.To, &one); err == nil {
				h.To = []string{one}
			}
		}
	}
	return h, true
}

// cleanRecipients trims and drops empty recipients, preserving order.
func cleanRecipients(to []string) []string {
	out := make([]string, 0, len(to))
	for _, r := range to {
		r = strings.TrimSpace(r)
		if r != "" {
			out = append(out, r)
		}
	}
	return out
}

func cleanLabels(labels []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, label := range labels {
		label = strings.TrimSpace(label)
		if label == "" || seen[label] {
			continue
		}
		seen[label] = true
		out = append(out, label)
	}
	return out
}

// parseCreated parses the RFC3339(nano) created timestamp. When absent/invalid
// it falls back to the file mtime (so timeline ordering still works), and as a
// last resort to now(). The Freshness layer records which source was used.
func parseCreated(s, path string, now func() time.Time) time.Time {
	s = strings.TrimSpace(s)
	if s != "" {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t
		}
	}
	if fi, err := os.Stat(path); err == nil {
		return fi.ModTime()
	}
	if now != nil {
		return now()
	}
	return time.Time{}
}

// createdSource reports how a message's Created time was derived, for Freshness.
func createdSource(headerCreated string) FreshnessSource {
	if strings.TrimSpace(headerCreated) != "" {
		if _, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(headerCreated)); err == nil {
			return SourceEmbedded
		}
		if _, err := time.Parse(time.RFC3339, strings.TrimSpace(headerCreated)); err == nil {
			return SourceEmbedded
		}
	}
	return SourceMtime
}

// canonicalThreadID normalizes/repairs a thread id defensively. Real ids are
// namespaced ("obs-042/memory-union", "p2p/cto__qa", "decision/item-3a",
// "broadcast/<s>", "team/<x>") but some are MALFORMED. Rules:
//
//   - trim, collapse internal whitespace, lowercase the namespace segment only.
//   - strip a stray leading/trailing slash; collapse duplicate slashes.
//   - for p2p threads, canonicalize the participant pair so "cto__qa" and
//     "qa__cto" map to the same id, and strip ":role" decorations that some
//     writers append ("cto:cto__qa:qa" -> "p2p/cto__qa").
//   - an empty/garbage id degrades to a stable sentinel ("(unthreaded)") rather
//     than producing many singleton threads.
//
// It never panics and always returns a non-empty id.
func canonicalThreadID(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return unthreadedID
	}
	// Collapse internal whitespace runs to single spaces, then to nothing in
	// the structural parts (thread ids do not legitimately contain spaces).
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, " ", "-")

	// Normalize slashes: drop leading/trailing, collapse duplicates.
	parts := make([]string, 0, 4)
	for _, seg := range strings.Split(s, "/") {
		seg = strings.TrimSpace(seg)
		if seg != "" {
			parts = append(parts, seg)
		}
	}
	if len(parts) == 0 {
		return unthreadedID
	}

	ns := strings.ToLower(parts[0])
	rest := parts[1:]

	if ns == "p2p" {
		return "p2p/" + canonicalPairKey(strings.Join(rest, "/"))
	}
	// Other namespaces (decision/obs-NNN/broadcast/team/...): keep the rest as
	// written (only the namespace is lowercased) so ids stay human-meaningful.
	if len(rest) == 0 {
		return ns
	}
	return ns + "/" + strings.Join(rest, "/")
}

const unthreadedID = "(unthreaded)"

// canonicalPairKey turns a p2p participant body into an order-independent key,
// stripping ":role" decorations. "cto__qa" and "qa__cto" both yield "cto__qa";
// "fullstack:fullstack__cto:cto" yields "cto__fullstack".
func canonicalPairKey(body string) string {
	body = strings.Trim(body, "/")
	peers := strings.Split(body, "__")
	clean := make([]string, 0, len(peers))
	for _, p := range peers {
		p = strings.TrimSpace(p)
		if i := strings.IndexByte(p, ':'); i >= 0 {
			p = p[:i] // drop ":role" decoration
		}
		if p != "" {
			clean = append(clean, p)
		}
	}
	if len(clean) == 0 {
		return body
	}
	sort.Strings(clean)
	return strings.Join(clean, "__")
}
