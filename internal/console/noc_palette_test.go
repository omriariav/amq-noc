package console

import "testing"

func TestPalette_IncludesProjectAndCreationActions(t *testing.T) {
	m := newControlModel(t)
	addCandidateProject(m, "delta", "/fake/proj/delta")
	addConfiguredEmptyProject(m, "empty-team", "/fake/proj/empty-team")

	labels := map[string]bool{}
	for _, it := range buildPaletteItems(m.ms) {
		labels[it.label] = true
	}
	for _, want := range []string{
		"beta/project",
		"beta/action/status",
		"beta/action/amq-env",
		"beta/action/amq-who",
		"beta/action/doctor",
		"beta/action/resume-plan",
		"beta/action/roles",
		"beta/action/team-rules",
		"beta/action/team-profiles",
		"beta/action/sync-pointers",
		"beta/action/new-session",
		"beta/action/new-profile",
		"beta/beta/action/status",
		"beta/beta/action/threads",
		"beta/beta/action/thread-context-any",
		"beta/beta/action/brief",
		"beta/beta/action/brief-seed",
		"beta/beta/action/fork-plan",
		"beta/beta/action/stop",
		"beta/beta/action/resume",
		"beta/beta/action/restart",
		"beta/beta/action/thread-context",
		"beta/beta/action/read-needs-you",
		"beta/beta/action/reply",
		"beta/beta/action/approve",
		"beta/beta/action/deny",
		"beta/beta/action/broadcast",
		"beta/beta/action/amq-ops",
		"beta/beta/action/amq-cleanup",
		"beta/beta/action/presence",
		"beta/beta/action/archive",
		"beta/beta/action/remove",
		"beta/beta/qa/action/thread-context",
		"beta/beta/qa/action/read-needs-you",
		"beta/beta/qa/action/reply",
		"beta/beta/qa/action/approve",
		"beta/beta/qa/action/deny",
		"beta/beta/qa/action/inbox",
		"beta/beta/qa/action/drain",
		"beta/beta/qa/action/dlq",
		"beta/beta/qa/action/dlq-read",
		"beta/beta/qa/action/dlq-retry",
		"beta/beta/qa/action/dlq-purge",
		"beta/beta/qa/action/dlq-retry-all",
		"beta/beta/qa/action/receipts-wait",
		"beta/beta/qa/action/message",
		"beta/beta/qa/action/message-wait",
		"beta/beta/qa/action/agent-resume",
		"delta/project",
		"delta/action/doctor",
		"delta/action/roles",
		"delta/action/new-team",
		"empty-team/project",
		"empty-team/action/status",
		"empty-team/action/doctor",
		"empty-team/action/resume-plan",
		"empty-team/action/roles",
		"empty-team/action/team-rules",
		"empty-team/action/team-profiles",
		"empty-team/action/sync-pointers",
		"empty-team/action/new-session",
		"empty-team/action/new-profile",
	} {
		if !labels[want] {
			t.Errorf("palette missing %q", want)
		}
	}
}

func TestPalette_ActionAliasesFindCreationRows(t *testing.T) {
	m := newControlModel(t)
	addCandidateProject(m, "delta", "/fake/proj/delta")
	addConfiguredEmptyProject(m, "empty-team", "/fake/proj/empty-team")
	items := buildPaletteItems(m.ms)

	cases := []struct {
		query string
		label string
		tag   string
	}{
		{query: "beta doctor health", label: "beta/action/doctor", tag: "doctor"},
		{query: "beta resume plan", label: "beta/action/resume-plan", tag: "resume plan"},
		{query: "beta amq env", label: "beta/action/amq-env", tag: "AMQ env"},
		{query: "beta amq who", label: "beta/action/amq-who", tag: "AMQ who"},
		{query: "beta project status", label: "beta/action/status", tag: "project status"},
		{query: "beta session status", label: "beta/beta/action/status", tag: "session status"},
		{query: "empty project status", label: "empty-team/action/status", tag: "project status"},
		{query: "beta team rules", label: "beta/action/team-rules", tag: "team rules"},
		{query: "beta threads", label: "beta/beta/action/threads", tag: "threads"},
		{query: "beta thread id", label: "beta/beta/action/thread-context-any", tag: "thread context by id"},
		{query: "beta brief", label: "beta/beta/action/brief", tag: "brief"},
		{query: "beta seed brief", label: "beta/beta/action/brief-seed", tag: "seed brief"},
		{query: "beta fork plan", label: "beta/beta/action/fork-plan", tag: "fork plan"},
		{query: "beta stop session", label: "beta/beta/action/stop", tag: "stop session"},
		{query: "beta resume session", label: "beta/beta/action/resume", tag: "resume session"},
		{query: "beta restart session", label: "beta/beta/action/restart", tag: "restart session"},
		{query: "delta create team", label: "delta/action/new-team", tag: "create team"},
		{query: "empty start workstream", label: "empty-team/action/new-session", tag: "start session"},
		{query: "beta create profile", label: "beta/action/new-profile", tag: "create profile"},
		{query: "delta role market", label: "delta/action/roles", tag: "role market"},
		{query: "beta team profiles", label: "beta/action/team-profiles", tag: "team profiles"},
		{query: "beta qa thread context", label: "beta/beta/qa/action/thread-context", tag: "thread context"},
		{query: "beta qa read needs you", label: "beta/beta/qa/action/read-needs-you", tag: "read needs-you"},
		{query: "beta qa approve", label: "beta/beta/qa/action/approve", tag: "approve"},
		{query: "beta qa reply", label: "beta/beta/qa/action/reply", tag: "reply"},
		{query: "beta qa deny", label: "beta/beta/qa/action/deny", tag: "deny"},
		{query: "beta broadcast", label: "beta/beta/action/broadcast", tag: "broadcast"},
		{query: "beta amq ops", label: "beta/beta/action/amq-ops", tag: "AMQ ops"},
		{query: "beta amq cleanup", label: "beta/beta/action/amq-cleanup", tag: "AMQ cleanup"},
		{query: "beta presence", label: "beta/beta/action/presence", tag: "presence"},
		{query: "beta qa inbox", label: "beta/beta/qa/action/inbox", tag: "inbox"},
		{query: "beta qa drain", label: "beta/beta/qa/action/drain", tag: "drain"},
		{query: "beta qa dlq", label: "beta/beta/qa/action/dlq", tag: "DLQ"},
		{query: "beta qa read dlq", label: "beta/beta/qa/action/dlq-read", tag: "read DLQ"},
		{query: "beta qa retry dlq", label: "beta/beta/qa/action/dlq-retry", tag: "retry DLQ"},
		{query: "beta qa purge dlq", label: "beta/beta/qa/action/dlq-purge", tag: "purge DLQ"},
		{query: "beta qa retry all dlq", label: "beta/beta/qa/action/dlq-retry-all", tag: "retry all DLQ"},
		{query: "beta qa wait receipts", label: "beta/beta/qa/action/receipts-wait", tag: "wait receipts"},
		{query: "beta qa message", label: "beta/beta/qa/action/message", tag: "message"},
		{query: "beta qa wait message", label: "beta/beta/qa/action/message-wait", tag: "wait message"},
		{query: "beta qa resume agent", label: "beta/beta/qa/action/agent-resume", tag: "resume agent"},
		{query: "beta archive session", label: "beta/beta/action/archive", tag: "archive session"},
		{query: "beta remove session", label: "beta/beta/action/remove", tag: "remove session"},
	}
	for _, tc := range cases {
		p := paletteState{query: tc.query, items: items}
		got, ok := p.selected()
		if !ok {
			t.Fatalf("query %q should find %q", tc.query, tc.label)
		}
		if got.label != tc.label {
			t.Fatalf("query %q selected %q, want %q", tc.query, got.label, tc.label)
		}
		if tag := paletteActionLabel(got); tag != tc.tag {
			t.Fatalf("query %q tag = %q, want %q", tc.query, tag, tc.tag)
		}
	}
}
