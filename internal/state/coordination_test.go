package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-noc/internal/launch"
)

// coordNow is the deterministic clock for coordination tests. All seeded
// created/last_seen times are expressed relative to it so age/staleness math is
// stable and the sandbox never touches a real wall-clock.
var coordNow = time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)

// msgSpec describes a maildir message to seed in the REAL ---json frontmatter
// format. Empty Kind/Priority are written as ABSENT keys (the ~optional case).
// A torn flag writes a deliberately invalid file that must be skipped.
type msgSpec struct {
	id        string
	from      string
	to        []string
	thread    string
	subject   string
	kind      string // "" => key omitted on disk
	priority  string // "" => key omitted on disk
	body      string
	createdAt time.Time
	schema    int // 0 => default to 1

	torn        bool // write an invalid/partial file (must be skipped)
	badThreadID bool // write the thread id verbatim (caller supplies malformed)
}

// seedMessage writes one message file into <agentDir>/inbox/<state>/<id>.md in
// the real frontmatter format. state is "new" (unread) or "cur" (read).
func seedMessage(t *testing.T, agentDir, state string, s msgSpec) {
	t.Helper()
	dir := filepath.Join(agentDir, "inbox", state)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, s.id+".md")

	if s.torn {
		// A torn file: an opening fence and partial JSON, no closing fence/body.
		if err := os.WriteFile(path, []byte("---json\n{ \"schema\": 1, \"id\": \"trunc\", \"from\": \"cto\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}

	schema := s.schema
	if schema == 0 {
		schema = 1
	}
	var b strings.Builder
	b.WriteString("---json\n{\n")
	b.WriteString("  \"schema\": " + itoa(schema) + ",\n")
	b.WriteString("  \"id\": \"" + s.id + "\",\n")
	b.WriteString("  \"from\": \"" + s.from + "\",\n")
	b.WriteString("  \"to\": [")
	for i, r := range s.to {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("\"" + r + "\"")
	}
	b.WriteString("],\n")
	b.WriteString("  \"thread\": \"" + s.thread + "\",\n")
	b.WriteString("  \"subject\": \"" + s.subject + "\",\n")
	created := s.createdAt
	if created.IsZero() {
		created = coordNow
	}
	b.WriteString("  \"created\": \"" + created.UTC().Format(time.RFC3339Nano) + "\"")
	if s.priority != "" {
		b.WriteString(",\n  \"priority\": \"" + s.priority + "\"")
	}
	if s.kind != "" {
		b.WriteString(",\n  \"kind\": \"" + s.kind + "\"")
	}
	b.WriteString("\n}\n---\n")
	b.WriteString(s.body)
	b.WriteString("\n")

	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// findThread locates a thread summary by canonical id in a coordination model.
func findThread(t *testing.T, c Coordination, id string) ThreadSummary {
	t.Helper()
	for _, th := range c.Threads {
		if th.ID == id {
			return th
		}
	}
	t.Fatalf("thread %q not found; have %v", id, threadIDs(c))
	return ThreadSummary{}
}

func threadIDs(c Coordination) []string {
	var out []string
	for _, th := range c.Threads {
		out = append(out, th.ID)
	}
	return out
}

func coordProbe() Probe {
	return Probe{
		PIDAlive:     func(pid int) bool { return true },
		ProcessMatch: func(pid int, _ func(args string) bool) bool { return true },
		Now:          func() time.Time { return coordNow },
	}
}

// --- parseMessageFile + canonicalThreadID --------------------------------

func TestParseMessageFileRealFrontmatter(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agents", "cto")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	seedMessage(t, agentDir, "new", msgSpec{
		id: "m1", from: "senior-dev", to: []string{"cto"},
		thread: "p2p/cto__senior-dev", subject: "GO: spot re-review",
		kind: "review_response", priority: "normal", body: "Findings: none.",
		createdAt: coordNow.Add(-time.Minute),
	})
	path := filepath.Join(agentDir, "inbox", "new", "m1.md")
	m, ok, err := parseMessageFile(path, "cto", MailboxNew, func() time.Time { return coordNow })
	if err != nil || !ok {
		t.Fatalf("parseMessageFile: ok=%v err=%v", ok, err)
	}
	if m.From != "senior-dev" || len(m.To) != 1 || m.To[0] != "cto" {
		t.Fatalf("from/to wrong: %+v", m)
	}
	if m.Kind != KindReviewResponse {
		t.Fatalf("Kind = %q, want review_response", m.Kind)
	}
	if m.Priority != PriorityNormal {
		t.Fatalf("Priority = %q, want normal", m.Priority)
	}
	if !m.SchemaOK {
		t.Fatal("SchemaOK should be true for schema:1")
	}
	if m.Body != "Findings: none." {
		t.Fatalf("Body = %q", m.Body)
	}
	if m.State != MailboxNew || m.Owner != "cto" {
		t.Fatalf("owner/state wrong: %+v", m)
	}
}

func TestParseMessageFileAMQIntegrationMetadata(t *testing.T) {
	dir := t.TempDir()
	seedMessage(t, dir, "new", msgSpec{
		id: "m1", from: "cto", to: []string{"qa"},
		thread: "handoff/test", subject: "handoff", kind: "decision",
		createdAt: coordNow,
		body:      "body",
	})
	path := filepath.Join(dir, "inbox", "new", "m1.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(raw), "\"kind\": \"decision\"", "\"kind\": \"decision\",\n  \"labels\": [\"handoff\", \"blocking\", \"handoff\"],\n  \"orchestrator\": \"kanban\",\n  \"from_project\": \"api\",\n  \"reply_project\": \"web\"", 1)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}

	m, ok, err := parseMessageFile(path, "qa", MailboxNew, func() time.Time { return coordNow })
	if err != nil || !ok {
		t.Fatalf("parseMessageFile metadata: ok=%v err=%v", ok, err)
	}
	if strings.Join(m.Labels, ",") != "handoff,blocking" || m.Orchestrator != "kanban" || m.FromProject != "api" || m.ReplyProject != "web" {
		t.Fatalf("metadata mismatch: %+v", m)
	}
}

func TestParseMessageMissingKindAndPriorityDefault(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agents", "cto")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// kind and priority OMITTED (the ~optional case).
	seedMessage(t, agentDir, "new", msgSpec{
		id: "m2", from: "fullstack", to: []string{"cto"},
		thread: "p2p/cto__fullstack", subject: "no meta", body: "hi",
	})
	path := filepath.Join(agentDir, "inbox", "new", "m2.md")
	m, ok, err := parseMessageFile(path, "cto", MailboxNew, func() time.Time { return coordNow })
	if err != nil || !ok {
		t.Fatalf("parseMessageFile: ok=%v err=%v", ok, err)
	}
	if m.Kind != KindUnknown {
		t.Fatalf("absent kind must be KindUnknown, got %q", m.Kind)
	}
	if m.Priority != PriorityNormal {
		t.Fatalf("absent priority must default to normal, got %q", m.Priority)
	}
}

func TestParseTornFileSkipped(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agents", "cto")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	seedMessage(t, agentDir, "new", msgSpec{id: "torn", torn: true})
	path := filepath.Join(agentDir, "inbox", "new", "torn.md")
	_, ok, err := parseMessageFile(path, "cto", MailboxNew, func() time.Time { return coordNow })
	if ok {
		t.Fatal("torn file must NOT parse ok")
	}
	if err == nil {
		t.Fatal("torn file must return a recorded reason, not nil")
	}
}

func TestCanonicalThreadID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"p2p/cto__qa", "p2p/cto__qa"},
		{"p2p/qa__cto", "p2p/cto__qa"},                             // order-independent
		{"p2p/cto:cto__fullstack:fullstack", "p2p/cto__fullstack"}, // strip :role
		{"p2p/fullstack:fullstack__cto:cto", "p2p/cto__fullstack"}, // both decorated + reorder
		{"obs-042/memory-union", "obs-042/memory-union"},           // namespaced kept
		{"decision/item-3a", "decision/item-3a"},                   // namespaced kept
		{"  P2P//cto__qa  ", "p2p/cto__qa"},                        // trim + dup slash + case
		{"broadcast/sess1", "broadcast/sess1"},                     // broadcast kept
		{"team/x", "team/x"},                                       // team kept
		{"", unthreadedID},                                         // empty -> sentinel
		{"   ", unthreadedID},                                      // whitespace -> sentinel
		{"/leading/slash", "leading/slash"},                        // strip leading slash
		{"weird thread id", "weird-thread-id"},                     // spaces repaired (single segment)
	}
	for _, c := range cases {
		if got := canonicalThreadID(c.in); got != c.want {
			t.Errorf("canonicalThreadID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- thread collapse + participants + unread-by --------------------------

func TestThreadCollapseParticipantsAndUnread(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()

	ctoDir := seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})
	devDir := seedAgent(t, base, "s", "senior-dev", launch.Record{Binary: "codex", Handle: "senior-dev", Session: "s", AgentPID: 2})

	// senior-dev -> cto question; cto's copy is UNREAD (inbox/new).
	seedMessage(t, ctoDir, "new", msgSpec{
		id: "q1", from: "senior-dev", to: []string{"cto"},
		thread: "p2p/cto__senior-dev", subject: "need a call", kind: "question",
		createdAt: coordNow.Add(-time.Minute),
	})
	// The sender's own inbox does not carry it; but an earlier status that cto
	// already READ (inbox/cur) shares the canonical thread under the reordered id.
	seedMessage(t, devDir, "cur", msgSpec{
		id: "s0", from: "cto", to: []string{"senior-dev"},
		thread: "p2p/senior-dev__cto", subject: "status", kind: "status",
		createdAt: coordNow.Add(-2 * time.Minute),
	})

	snap, err := Build(proj, base, coordProbe())
	if err != nil {
		t.Fatal(err)
	}
	sess := snap.Sessions[0]
	th := findThread(t, sess.Coordination, "p2p/cto__senior-dev")

	if th.MessageCount != 2 {
		t.Fatalf("MessageCount = %d, want 2 (collapsed across reordered ids)", th.MessageCount)
	}
	wantParts := []string{"cto", "senior-dev"}
	if strings.Join(th.Participants, ",") != strings.Join(wantParts, ",") {
		t.Fatalf("Participants = %v, want %v", th.Participants, wantParts)
	}
	// cto has the latest question UNREAD in its own inbox.
	if len(th.UnreadBy) != 1 || th.UnreadBy[0] != "cto" {
		t.Fatalf("UnreadBy = %v, want [cto]", th.UnreadBy)
	}
	// Latest kind is question -> awaiting reply.
	if th.Status != ThreadAwaitingReply {
		t.Fatalf("Status = %q, want awaiting-reply", th.Status)
	}
}

// --- edge counts ---------------------------------------------------------

func TestEdgeCounts(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	ctoDir := seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})
	devDir := seedAgent(t, base, "s", "senior-dev", launch.Record{Binary: "codex", Handle: "senior-dev", Session: "s", AgentPID: 2})

	// senior-dev -> cto twice (both land in cto's inbox).
	seedMessage(t, ctoDir, "cur", msgSpec{id: "a", from: "senior-dev", to: []string{"cto"}, thread: "p2p/cto__senior-dev", kind: "status", createdAt: coordNow.Add(-3 * time.Minute)})
	seedMessage(t, ctoDir, "new", msgSpec{id: "b", from: "senior-dev", to: []string{"cto"}, thread: "p2p/cto__senior-dev", kind: "status", createdAt: coordNow.Add(-2 * time.Minute)})
	// cto -> senior-dev once (lands in senior-dev's inbox).
	seedMessage(t, devDir, "cur", msgSpec{id: "c", from: "cto", to: []string{"senior-dev"}, thread: "p2p/cto__senior-dev", kind: "status", createdAt: coordNow.Add(-time.Minute)})

	snap, err := Build(proj, base, coordProbe())
	if err != nil {
		t.Fatal(err)
	}
	edges := snap.Sessions[0].Coordination.Edges
	got := map[string]int{}
	for _, e := range edges {
		got[e.From+"->"+e.To] = e.Count
	}
	if got["senior-dev->cto"] != 2 {
		t.Fatalf("senior-dev->cto = %d, want 2 (edges=%v)", got["senior-dev->cto"], edges)
	}
	if got["cto->senior-dev"] != 1 {
		t.Fatalf("cto->senior-dev = %d, want 1 (edges=%v)", got["cto->senior-dev"], edges)
	}
}

// --- triage: needs-you via operator handle -------------------------------

func TestTriageNeedsYouViaOperatorHandle(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	userDir := seedAgent(t, base, "s", "user", launch.Record{Binary: "claude", Handle: "user", Session: "s", AgentPID: 9})
	_ = seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})

	// cto asks the operator (user) a review_request, UNREAD in user's inbox.
	seedMessage(t, userDir, "new", msgSpec{
		id: "rr1", from: "cto", to: []string{"user"},
		thread: "p2p/cto__user", subject: "please review the plan", kind: "review_request",
		createdAt: coordNow.Add(-2 * time.Minute),
	})

	snap, err := Build(proj, base, coordProbe())
	if err != nil {
		t.Fatal(err)
	}
	sess := snap.Sessions[0]
	th := findThread(t, sess.Coordination, "p2p/cto__user")
	if th.Triage != TriageNeedsYou {
		t.Fatalf("Triage = %q, want needs-you (review_request addressed to operator)", th.Triage)
	}
	if sess.Rollup.NeedsYou != 1 {
		t.Fatalf("session rollup NeedsYou = %d, want 1 (%+v)", sess.Rollup.NeedsYou, sess.Rollup)
	}
	if snap.Rollup.NeedsYou != 1 {
		t.Fatalf("snapshot rollup NeedsYou = %d, want 1", snap.Rollup.NeedsYou)
	}
}

func TestTriageOperatorAddressedAckOnRunningTeamDoesNotNeedUser(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	userDir := seedAgent(t, base, "s", "user", launch.Record{Binary: "claude", Handle: "user", Session: "s", AgentPID: 9})
	_ = seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})
	_ = seedAgent(t, base, "s", "qa", launch.Record{Binary: "claude", Handle: "qa", Session: "s", AgentPID: 2})

	seedMessage(t, userDir, "new", msgSpec{
		id: "ack1", from: "cto", to: []string{"user"},
		thread: "status/ack", subject: "Ack: Phase 2 accepted, no apply release", kind: "question",
		body:      "Acknowledged. No operator action requested.",
		createdAt: coordNow.Add(-2 * time.Minute),
	})

	snap, err := Build(proj, base, coordProbe())
	if err != nil {
		t.Fatal(err)
	}
	sess := snap.Sessions[0]
	th := findThread(t, sess.Coordination, "status/ack")
	if th.Triage == TriageNeedsYou {
		t.Fatalf("ack/status thread triage = needs-you, want non-actionable: %+v", th)
	}
	if sess.Rollup.NeedsYou != 0 || snap.Rollup.NeedsYou != 0 {
		t.Fatalf("needs-you rollups session/snapshot = %d/%d, want 0/0", sess.Rollup.NeedsYou, snap.Rollup.NeedsYou)
	}
}

func TestTriageOldNeedsYouOnLiveTeamCountsAsHistorical(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	userDir := filepath.Join(base, "s", "agents", "user")
	_ = seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})
	_ = seedAgent(t, base, "s", "qa", launch.Record{Binary: "claude", Handle: "qa", Session: "s", AgentPID: 2})

	seedMessage(t, userDir, "new", msgSpec{
		id: "old-ask", from: "cto", to: []string{"user"},
		thread: "decision/ship", subject: "APPROVAL: ship now?", kind: "question",
		createdAt: coordNow.Add(-26 * time.Hour),
	})

	snap, err := Build(proj, base, coordProbe())
	if err != nil {
		t.Fatal(err)
	}
	sess := snap.Sessions[0]
	th := findThread(t, sess.Coordination, "decision/ship")
	if th.Triage != TriageNeedsYou || !th.Historical {
		t.Fatalf("thread triage/historical = %q/%v, want needs-you historical", th.Triage, th.Historical)
	}
	if sess.Rollup.NeedsYou != 0 || sess.Rollup.NeedsYouHistorical != 1 {
		t.Fatalf("session rollup = %+v, want historical needs-you only", sess.Rollup)
	}
	if snap.Rollup.NeedsYou != 0 || snap.Rollup.NeedsYouHistorical != 1 {
		t.Fatalf("snapshot rollup = %+v, want historical needs-you only", snap.Rollup)
	}
}

func TestTriageOldNeedsYouOnDeadMailboxLiveTeamCountsAsHistorical(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	userDir := filepath.Join(base, "s", "agents", "user")
	ctoDir := seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})
	qaDir := seedAgent(t, base, "s", "qa", launch.Record{Binary: "claude", Handle: "qa", Session: "s", AgentPID: 2})
	seedPresence(t, ctoDir, "cto", "active", coordNow.Add(-5*time.Second))
	seedPresence(t, qaDir, "qa", "active", coordNow.Add(-5*time.Second))

	seedMessage(t, userDir, "new", msgSpec{
		id: "old-ask", from: "cto", to: []string{"user"},
		thread: "decision/ship", subject: "APPROVAL: ship now?", kind: "question",
		createdAt: coordNow.Add(-26 * time.Hour),
	})

	probe := coordProbe()
	probe.PIDAlive = func(pid int) bool { return false }
	probe.ProcessMatch = func(pid int, _ func(args string) bool) bool { return false }
	snap, err := Build(proj, base, probe)
	if err != nil {
		t.Fatal(err)
	}
	sess := snap.Sessions[0]
	th := findThread(t, sess.Coordination, "decision/ship")
	if th.Triage != TriageNeedsYou || !th.Historical {
		t.Fatalf("thread triage/historical = %q/%v, want needs-you historical", th.Triage, th.Historical)
	}
	if sess.Rollup.NeedsYou != 0 || sess.Rollup.NeedsYouHistorical != 1 {
		t.Fatalf("session rollup = %+v, want historical needs-you only", sess.Rollup)
	}
}

func TestTriageNeedsYouFromStoppedAgentCountsAsHistorical(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	userDir := filepath.Join(base, "s", "agents", "user")
	_ = seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})

	seedMessage(t, userDir, "new", msgSpec{
		id: "fresh-ask", from: "cto", to: []string{"user"},
		thread: "decision/scope", subject: "APPROVAL: scope check?", kind: "question",
		createdAt: coordNow.Add(-2 * time.Minute),
	})

	probe := coordProbe()
	probe.PIDAlive = func(pid int) bool { return false }
	probe.ProcessMatch = func(pid int, _ func(args string) bool) bool { return false }
	snap, err := Build(proj, base, probe)
	if err != nil {
		t.Fatal(err)
	}
	sess := snap.Sessions[0]
	if len(sess.Agents) != 1 || agentCanCurrentlyNeedOperator(sess.Agents[0].Liveness) {
		t.Fatalf("precondition agents = %+v, want one inactive agent", sess.Agents)
	}
	th := findThread(t, sess.Coordination, "decision/scope")
	if th.Triage != TriageNeedsYou || !th.Historical {
		t.Fatalf("thread triage/historical = %q/%v, want needs-you history", th.Triage, th.Historical)
	}
	if sess.Rollup.NeedsYou != 0 || sess.Rollup.NeedsYouHistorical != 1 {
		t.Fatalf("session rollup = %+v, want historical needs-you only", sess.Rollup)
	}
	if snap.Rollup.NeedsYou != 0 || snap.Rollup.NeedsYouHistorical != 1 {
		t.Fatalf("snapshot rollup = %+v, want historical needs-you only", snap.Rollup)
	}
}

func TestTriageNeedsYouScansOperatorMailboxWithoutLaunchRecord(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	_ = seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})
	userDir := filepath.Join(base, "s", "agents", "user")

	seedMessage(t, userDir, "new", msgSpec{
		id: "approval1", from: "cto", to: []string{"user"},
		thread: "decision/ship", subject: "APPROVAL: ship now?", kind: "question",
		createdAt: coordNow.Add(-2 * time.Minute),
	})

	snap, err := Build(proj, base, coordProbe())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(snap.Sessions))
	}
	sess := snap.Sessions[0]
	if len(sess.Agents) != 1 || sess.Agents[0].Handle != "cto" {
		t.Fatalf("operator mailbox should not become a squad agent: %+v", sess.Agents)
	}
	th := findThread(t, sess.Coordination, "decision/ship")
	if th.Triage != TriageNeedsYou || th.LatestID != "approval1" {
		t.Fatalf("thread triage/latest = %q/%q, want needs-you approval1", th.Triage, th.LatestID)
	}
	if len(th.UnreadBy) != 1 || th.UnreadBy[0] != "user" {
		t.Fatalf("UnreadBy = %v, want [user]", th.UnreadBy)
	}
	if sess.Rollup.NeedsYou != 1 || snap.Rollup.NeedsYou != 1 {
		t.Fatalf("needs-you rollups session/snapshot = %d/%d, want 1/1", sess.Rollup.NeedsYou, snap.Rollup.NeedsYou)
	}
}

func TestTriageNeedsYouCustomOperatorHandle(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	// Operator handle is configured as "operator", not the default "user".
	opDir := seedAgent(t, base, "s", "operator", launch.Record{Binary: "claude", Handle: "operator", Session: "s", AgentPID: 9})
	seedMessage(t, opDir, "new", msgSpec{
		id: "qq", from: "cto", to: []string{"operator"},
		thread: "p2p/cto__operator", subject: "decision needed", kind: "decision",
		createdAt: coordNow.Add(-time.Minute),
	})

	snap, err := BuildWithThresholds(proj, base, coordProbe(), Thresholds{OperatorHandle: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	th := findThread(t, snap.Sessions[0].Coordination, "p2p/cto__operator")
	if th.Triage != TriageNeedsYou {
		t.Fatalf("Triage = %q, want needs-you via custom operator handle", th.Triage)
	}
}

func TestThreadSummaryCarriesAMQIntegrationMetadata(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	qaDir := seedAgent(t, base, "s", "qa", launch.Record{Binary: "claude", Handle: "qa", Session: "s", AgentPID: 2})
	seedMessage(t, qaDir, "new", msgSpec{
		id: "handoff1", from: "cto", to: []string{"qa"},
		thread: "handoff/test", subject: "handoff", kind: "decision",
		createdAt: coordNow,
	})
	path := filepath.Join(qaDir, "inbox", "new", "handoff1.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(raw), "\"kind\": \"decision\"", "\"kind\": \"decision\",\n  \"labels\": [\"handoff\", \"blocking\"],\n  \"orchestrator\": \"kanban\",\n  \"from_project\": \"api\",\n  \"reply_project\": \"web\"", 1)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}

	snap, err := Build(proj, base, coordProbe())
	if err != nil {
		t.Fatal(err)
	}
	th := findThread(t, snap.Sessions[0].Coordination, "handoff/test")
	if strings.Join(th.Labels, ",") != "blocking,handoff" || th.Orchestrator != "kanban" || th.FromProject != "api" || th.ReplyProject != "web" {
		t.Fatalf("thread metadata mismatch: %+v", th)
	}
}

// --- triage: at-risk via threshold (deterministic clock) -----------------

func TestTriageAtRiskAgingReview(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	ctoDir := seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})
	_ = seedAgent(t, base, "s", "senior-dev", launch.Record{Binary: "codex", Handle: "senior-dev", Session: "s", AgentPID: 2})

	// senior-dev -> cto review_request created 60m ago; ReviewAge default 45m.
	// Agent<->agent (not the operator) and aging -> at-risk.
	seedMessage(t, ctoDir, "new", msgSpec{
		id: "old", from: "senior-dev", to: []string{"cto"},
		thread: "p2p/cto__senior-dev", subject: "review PR", kind: "review_request",
		createdAt: coordNow.Add(-60 * time.Minute),
	})

	snap, err := Build(proj, base, coordProbe())
	if err != nil {
		t.Fatal(err)
	}
	th := findThread(t, snap.Sessions[0].Coordination, "p2p/cto__senior-dev")
	if th.Triage != TriageAtRisk {
		t.Fatalf("Triage = %q, want at-risk (review aging past ReviewAge)", th.Triage)
	}
	if !th.Freshness.Stale {
		t.Fatal("Freshness.Stale should be true for an aged review")
	}
}

func TestTriageNotAtRiskWhenFresh(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	ctoDir := seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})
	_ = seedAgent(t, base, "s", "senior-dev", launch.Record{Binary: "codex", Handle: "senior-dev", Session: "s", AgentPID: 2})

	// Same review_request but created only 5m ago: under ReviewAge -> not at-risk.
	seedMessage(t, ctoDir, "new", msgSpec{
		id: "fresh", from: "senior-dev", to: []string{"cto"},
		thread: "p2p/cto__senior-dev", subject: "review PR", kind: "review_request",
		createdAt: coordNow.Add(-5 * time.Minute),
	})
	snap, err := Build(proj, base, coordProbe())
	if err != nil {
		t.Fatal(err)
	}
	th := findThread(t, snap.Sessions[0].Coordination, "p2p/cto__senior-dev")
	if th.Triage == TriageAtRisk {
		t.Fatal("fresh review should NOT be at-risk")
	}
	if th.Freshness.Stale {
		t.Fatal("fresh review Freshness.Stale should be false")
	}
}

// --- triage: declared block ----------------------------------------------

func TestTriageBlockedDeclared(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	ctoDir := seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})
	_ = seedAgent(t, base, "s", "senior-dev", launch.Record{Binary: "codex", Handle: "senior-dev", Session: "s", AgentPID: 2})

	// senior-dev declares a block on cto via a NO-GO review_response body marker.
	seedMessage(t, ctoDir, "new", msgSpec{
		id: "blk", from: "senior-dev", to: []string{"cto"},
		thread: "p2p/cto__senior-dev", subject: "PR NO-GO", kind: "review_response",
		body: "NO-GO. I am blocked on the missing migration.", createdAt: coordNow.Add(-time.Minute),
	})
	snap, err := Build(proj, base, coordProbe())
	if err != nil {
		t.Fatal(err)
	}
	th := findThread(t, snap.Sessions[0].Coordination, "p2p/cto__senior-dev")
	if th.Status != ThreadBlocked {
		t.Fatalf("Status = %q, want blocked", th.Status)
	}
	if th.Triage != TriageBlocked {
		t.Fatalf("Triage = %q, want blocked", th.Triage)
	}
	if snap.Sessions[0].Rollup.Blocked != 1 {
		t.Fatalf("rollup Blocked = %d, want 1", snap.Sessions[0].Rollup.Blocked)
	}
}

func TestTriageNeedsYouViaWaitingForInstructions(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	ctoDir := seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})

	seedMessage(t, ctoDir, "cur", msgSpec{
		id: "wait-user", from: "cto",
		thread: "status/cto", subject: "Waiting for instructions", kind: "status",
		body: "CTO is waiting for instructions before proceeding.", createdAt: coordNow.Add(-time.Minute),
	})
	snap, err := Build(proj, base, coordProbe())
	if err != nil {
		t.Fatal(err)
	}
	th := findThread(t, snap.Sessions[0].Coordination, "status/cto")
	if th.Triage != TriageNeedsYou {
		t.Fatalf("Triage = %q, want needs-you", th.Triage)
	}
	if snap.Sessions[0].Rollup.NeedsYou != 1 {
		t.Fatalf("rollup NeedsYou = %d, want 1", snap.Sessions[0].Rollup.NeedsYou)
	}
}

func TestTriageGatedDeclared(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	devDir := seedAgent(t, base, "s", "senior-dev", launch.Record{Binary: "codex", Handle: "senior-dev", Session: "s", AgentPID: 1})

	seedMessage(t, devDir, "cur", msgSpec{
		id: "gate", from: "senior-dev",
		thread: "status/senior-dev", subject: "Paused on OBS-048", kind: "status",
		body: "Paused on OBS-048 until CTO release and QA gates are authorized.", createdAt: coordNow.Add(-time.Minute),
	})
	snap, err := Build(proj, base, coordProbe())
	if err != nil {
		t.Fatal(err)
	}
	th := findThread(t, snap.Sessions[0].Coordination, "status/senior-dev")
	if th.Triage != TriageGated {
		t.Fatalf("Triage = %q, want gated", th.Triage)
	}
	if snap.Sessions[0].Rollup.Gated != 1 || snap.Sessions[0].Rollup.Blocked != 0 {
		t.Fatalf("rollup gated/blocked = %d/%d, want 1/0", snap.Sessions[0].Rollup.Gated, snap.Sessions[0].Rollup.Blocked)
	}
}

func TestBlockClearedByLaterGo(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	ctoDir := seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})

	seedMessage(t, ctoDir, "cur", msgSpec{
		id: "blk", from: "senior-dev", to: []string{"cto"},
		thread: "p2p/cto__senior-dev", subject: "NO-GO", kind: "review_response",
		body: "NO-GO. blocked on migration.", createdAt: coordNow.Add(-10 * time.Minute),
	})
	// A LATER GO clears the block.
	seedMessage(t, ctoDir, "cur", msgSpec{
		id: "go", from: "senior-dev", to: []string{"cto"},
		thread: "p2p/cto__senior-dev", subject: "GO", kind: "review_response",
		body: "GO for the fix. resolved.", createdAt: coordNow.Add(-time.Minute),
	})
	snap, err := Build(proj, base, coordProbe())
	if err != nil {
		t.Fatal(err)
	}
	th := findThread(t, snap.Sessions[0].Coordination, "p2p/cto__senior-dev")
	if th.Status == ThreadBlocked {
		t.Fatal("a later GO must clear the block")
	}
	if th.Triage == TriageBlocked {
		t.Fatal("cleared block must not stay triage=blocked")
	}
}

// --- freshness staleness + schema mismatch -------------------------------

func TestSchemaMismatchDegradedWithWarning(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	ctoDir := seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})

	// schema:2 -> degraded read, surfaced but warned.
	seedMessage(t, ctoDir, "new", msgSpec{
		id: "future", from: "cto", to: []string{"senior-dev"},
		thread: "p2p/cto__senior-dev", subject: "from the future", kind: "status",
		schema: 2, createdAt: coordNow.Add(-time.Minute),
	})
	snap, err := Build(proj, base, coordProbe())
	if err != nil {
		t.Fatal(err)
	}
	coord := snap.Sessions[0].Coordination
	// Thread still present (degraded, not dropped).
	findThread(t, coord, "p2p/cto__senior-dev")
	// A schema-mismatch warning was recorded.
	found := false
	for _, w := range coord.Warnings {
		if strings.Contains(w.Reason, "schema mismatch") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a schema-mismatch warning, got %v", coord.Warnings)
	}
}

func TestTornFileRecordedAsWarningNotCrash(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	ctoDir := seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})

	// One good message + one torn file in the same inbox.
	seedMessage(t, ctoDir, "new", msgSpec{
		id: "good", from: "cto", to: []string{"senior-dev"},
		thread: "p2p/cto__senior-dev", subject: "ok", kind: "status", createdAt: coordNow.Add(-time.Minute),
	})
	seedMessage(t, ctoDir, "new", msgSpec{id: "torn", torn: true})

	snap, err := Build(proj, base, coordProbe())
	if err != nil {
		t.Fatalf("Build must not crash on a torn file: %v", err)
	}
	coord := snap.Sessions[0].Coordination
	// The good message produced a thread; the torn one was skipped-with-warning.
	findThread(t, coord, "p2p/cto__senior-dev")
	skipped := false
	for _, w := range coord.Warnings {
		if strings.Contains(w.Reason, "skipped") {
			skipped = true
		}
	}
	if !skipped {
		t.Fatalf("torn file must produce a skip warning, got %v", coord.Warnings)
	}
}

// --- malformed thread id collapses correctly -----------------------------

func TestMalformedThreadIDCollapses(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	ctoDir := seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})

	// Two messages whose RAW thread ids differ (one decorated with :role, one
	// reordered) but canonicalize to the SAME id.
	seedMessage(t, ctoDir, "cur", msgSpec{
		id: "x1", from: "senior-dev", to: []string{"cto"},
		thread: "p2p/cto:cto__senior-dev:senior-dev", subject: "decorated", kind: "status",
		createdAt: coordNow.Add(-3 * time.Minute),
	})
	seedMessage(t, ctoDir, "cur", msgSpec{
		id: "x2", from: "cto", to: []string{"senior-dev"},
		thread: "p2p/senior-dev__cto", subject: "reordered", kind: "status",
		createdAt: coordNow.Add(-2 * time.Minute),
	})
	snap, err := Build(proj, base, coordProbe())
	if err != nil {
		t.Fatal(err)
	}
	coord := snap.Sessions[0].Coordination
	th := findThread(t, coord, "p2p/cto__senior-dev")
	if th.MessageCount != 2 {
		t.Fatalf("malformed ids must collapse to one thread of 2 msgs, got count=%d ids=%v", th.MessageCount, threadIDs(coord))
	}
}

// --- dead-mailbox-live participant -> at-risk ----------------------------

func TestAtRiskDeadMailboxLiveParticipant(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	// cto agent PID is dead but presence is fresh -> dead-mailbox-live (PR1).
	ctoDir := seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 4242})
	seedPresence(t, ctoDir, "cto", "active", coordNow.Add(-5*time.Second))
	_ = seedAgent(t, base, "s", "senior-dev", launch.Record{Binary: "codex", Handle: "senior-dev", Session: "s", AgentPID: 2})

	// An unanswered review request from senior-dev to cto (fresh), but cto is
	// dead-mailbox-live -> at-risk even though not yet past ReviewAge.
	seedMessage(t, ctoDir, "new", msgSpec{
		id: "rr", from: "senior-dev", to: []string{"cto"},
		thread: "p2p/cto__senior-dev", subject: "review", kind: "review_request",
		createdAt: coordNow.Add(-2 * time.Minute),
	})

	// cto PID dead; senior-dev alive.
	probe := Probe{
		PIDAlive:     func(pid int) bool { return pid == 2 },
		ProcessMatch: func(pid int, _ func(args string) bool) bool { return true },
		Now:          func() time.Time { return coordNow },
	}
	snap, err := Build(proj, base, probe)
	if err != nil {
		t.Fatal(err)
	}
	cto := findAgent(t, snap, "cto")
	if cto.Liveness != LivenessDeadMailboxLive {
		t.Fatalf("precondition: cto liveness = %q, want dead-mailbox-live", cto.Liveness)
	}
	th := findThread(t, snap.Sessions[0].Coordination, "p2p/cto__senior-dev")
	if th.Triage != TriageAtRisk {
		t.Fatalf("Triage = %q, want at-risk (dead-mailbox-live participant)", th.Triage)
	}
}

// --- timeline is derived + human-readable --------------------------------

func TestTimelineDerivedHumanReadable(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	ctoDir := seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})

	seedMessage(t, ctoDir, "new", msgSpec{
		id: "blk", from: "senior-dev", to: []string{"cto"},
		thread: "p2p/cto__senior-dev", subject: "NO-GO", kind: "review_response",
		body: "NO-GO. blocked on cto.", createdAt: coordNow.Add(-time.Minute),
	})
	snap, err := Build(proj, base, coordProbe())
	if err != nil {
		t.Fatal(err)
	}
	tl := snap.Sessions[0].Coordination.Timeline
	if len(tl) == 0 {
		t.Fatal("timeline should have at least one derived event")
	}
	// Human phrasing like "senior-dev blocked on cto".
	want := "senior-dev blocked on cto"
	found := false
	for _, ev := range tl {
		if ev.Summary == want {
			found = true
		}
	}
	if !found {
		var got []string
		for _, ev := range tl {
			got = append(got, ev.Summary)
		}
		t.Fatalf("timeline missing %q; got %v", want, got)
	}
}

// --- rollup string -------------------------------------------------------

func TestTriageRollupString(t *testing.T) {
	r := TriageRollup{NeedsYou: 2, AtRisk: 1, Blocked: 3, Gated: 4, Clear: 5}
	if got := r.String(); got != "2 needs-you, 1 at-risk, 3 blocked, 4 gated" {
		t.Fatalf("rollup string = %q", got)
	}
}

// --- multi-recipient to array --------------------------------------------

func TestMultiRecipientParticipantsUnion(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	ctoDir := seedAgent(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Session: "s", AgentPID: 1})
	qaDir := seedAgent(t, base, "s", "qa", launch.Record{Binary: "claude", Handle: "qa", Session: "s", AgentPID: 2})

	// cpo -> [cto, qa] on a namespaced thread. Both recipients hold a copy.
	seedMessage(t, ctoDir, "new", msgSpec{
		id: "b1", from: "cpo", to: []string{"cto", "qa"},
		thread: "obs-042/memory-union", subject: "broadcast", kind: "status",
		createdAt: coordNow.Add(-time.Minute),
	})
	seedMessage(t, qaDir, "cur", msgSpec{
		id: "b1", from: "cpo", to: []string{"cto", "qa"},
		thread: "obs-042/memory-union", subject: "broadcast", kind: "status",
		createdAt: coordNow.Add(-time.Minute),
	})
	snap, err := Build(proj, base, coordProbe())
	if err != nil {
		t.Fatal(err)
	}
	th := findThread(t, snap.Sessions[0].Coordination, "obs-042/memory-union")
	want := []string{"cpo", "cto", "qa"}
	if strings.Join(th.Participants, ",") != strings.Join(want, ",") {
		t.Fatalf("Participants = %v, want %v", th.Participants, want)
	}
	// cto's copy is unread (new), qa's is read (cur): UnreadBy = [cto].
	if len(th.UnreadBy) != 1 || th.UnreadBy[0] != "cto" {
		t.Fatalf("UnreadBy = %v, want [cto]", th.UnreadBy)
	}
}
