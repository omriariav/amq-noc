package noc

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-noc/internal/state"
)

func TestFetchSessionCorrelationParsesTasksReportsAndEvidence(t *testing.T) {
	now := time.Date(2026, 6, 22, 20, 0, 0, 0, time.UTC)
	run := func(dir string, name string, args ...string) ([]byte, error) {
		key := name + " " + strings.Join(args, " ")
		switch key {
		case "amq-squad task list --project /repo/app --session issue-96 --json":
			return []byte(`{"schema_version":1,"kind":"tasks","data":{"session":"issue-96","tasks":[
				{"id":"t1","title":"Implementation","status":"completed","assigned_to":"backend-dev","evidence":"https://github.com/o/r/pull/12 #40 head abcdef1 go test ./..."},
				{"id":"t2","title":"Review","status":"pending","assigned_to":"qa","depends_on":["t1"]}
			]}}`), nil
		case "amq-squad status --project /repo/app --session issue-96 --json":
			return []byte(sampleCorrelationRuntimeStatusJSON), nil
		default:
			t.Fatalf("unexpected command: %s", key)
			return nil, nil
		}
	}

	c := FetchSessionCorrelation(run, ProjectSnapshot{Dir: "/repo/app"}, state.Session{
		Name: "issue-96",
		Agents: []state.Agent{
			{Handle: "backend-dev", Role: "backend-dev", Liveness: state.LivenessAlive},
			{Handle: "qa", Role: "qa", Liveness: state.LivenessStale},
		},
		Coordination: state.Coordination{Threads: []state.ThreadSummary{
			{
				ID:          "p2p/backend-dev__cto",
				Kind:        state.KindReviewRequest,
				LastFrom:    "backend-dev",
				Subject:     "Review PR https://github.com/o/r/pull/12",
				LastEventAt: now,
				LatestBody:  "go test ./... passed for abcdef1",
				Status:      state.ThreadAwaitingReply,
			},
			{
				ID:          "gate/release",
				Kind:        state.KindQuestion,
				LastFrom:    "cto",
				Subject:     "APPROVAL: release?",
				LastEventAt: now,
				LatestBody:  "Human release gate for #40",
				Gate:        true,
			},
		}, Messages: []state.Message{{
			ID:      "r1",
			From:    "backend-dev",
			To:      []string{"cto"},
			Thread:  "p2p/backend-dev__cto",
			Subject: "Review PR https://github.com/o/r/pull/12",
			Kind:    state.KindReviewRequest,
			Created: now,
			Body:    "go test ./... passed for abcdef1",
		}}},
	})

	if c.Tasks.Status != healthStatusOK || len(c.Tasks.Tasks) != 2 {
		t.Fatalf("tasks = %+v", c.Tasks)
	}
	if len(c.WorkerReports) != 1 || c.WorkerReports[0].Type != "review_request" || !c.WorkerReports[0].Review {
		t.Fatalf("worker reports = %+v", c.WorkerReports)
	}
	if c.Runtime.Status != healthStatusOK || c.Runtime.Source != "amq-squad status --json" || c.Runtime.WorkersTotal != 2 || c.Runtime.WorkersLive != 1 || c.Runtime.WorkersAtRisk != 1 {
		t.Fatalf("runtime = %+v", c.Runtime)
	}
	if len(c.Evidence.PRURLs) != 1 || len(c.Evidence.IssueRefs) != 1 || len(c.Evidence.HeadSHAs) != 1 || !c.Evidence.HumanGateRequired {
		t.Fatalf("evidence = %+v", c.Evidence)
	}
}

func TestFetchSessionCorrelationTaskStoreGapAndMismatches(t *testing.T) {
	run := func(dir string, name string, args ...string) ([]byte, error) {
		return nil, errors.New("task list unavailable")
	}
	c := FetchSessionCorrelation(run, ProjectSnapshot{Dir: "/repo/app"}, state.Session{Name: "issue-96"})
	if c.Tasks.Status != healthStatusCapabilityGap || len(c.Mismatches) != 1 || c.Mismatches[0].Name != "task_store_unavailable" {
		t.Fatalf("gap correlation = %+v", c)
	}

	run = func(dir string, name string, args ...string) ([]byte, error) {
		key := name + " " + strings.Join(args, " ")
		if key == "amq-squad status --project /repo/app --session issue-96 --json" {
			return []byte(sampleCorrelationRuntimeStatusJSON), nil
		}
		return []byte(`{"schema_version":1,"kind":"tasks","data":{"tasks":[
			{"id":"t1","title":"Implementation","status":"pending","assigned_to":"backend-dev"},
			{"id":"t2","title":"Review","status":"pending","assigned_to":"qa","depends_on":["t1"]},
			{"id":"t3","title":"Blocked thing","status":"blocked","assigned_to":"frontend-dev"}
		]}}`), nil
	}
	c = FetchSessionCorrelation(run, ProjectSnapshot{Dir: "/repo/app"}, state.Session{
		Name: "issue-96",
		Coordination: state.Coordination{Threads: []state.ThreadSummary{{
			ID:       "p2p/backend-dev__cto",
			Kind:     state.KindStatus,
			LastFrom: "backend-dev",
			Subject:  "progress",
		}, {
			ID:       "p2p/qa__cto",
			Kind:     state.KindReviewRequest,
			LastFrom: "qa",
			Subject:  "review please",
		}}, Messages: []state.Message{{
			ID:      "p1",
			From:    "backend-dev",
			To:      []string{"cto"},
			Thread:  "p2p/backend-dev__cto",
			Subject: "progress",
			Kind:    state.KindStatus,
		}, {
			ID:      "r1",
			From:    "qa",
			To:      []string{"cto"},
			Thread:  "p2p/qa__cto",
			Subject: "review please",
			Kind:    state.KindReviewRequest,
		}}},
	})
	names := map[string]bool{}
	for _, sig := range c.Mismatches {
		names[sig.Name] = true
	}
	for _, want := range []string{"pending_task_has_worker_report", "dependency_gated_task", "blocked_task_without_report", "review_request_before_task_done"} {
		if !names[want] {
			t.Fatalf("missing mismatch %q in %+v", want, c.Mismatches)
		}
	}
}

func TestFetchSessionCorrelationPreservesWorkerReportBeforeLeadAck(t *testing.T) {
	now := time.Date(2026, 6, 22, 20, 0, 0, 0, time.UTC)
	run := func(dir string, name string, args ...string) ([]byte, error) {
		key := name + " " + strings.Join(args, " ")
		switch key {
		case "amq-squad task list --project /repo/app --session issue-96 --json":
			return []byte(`{"schema_version":1,"kind":"tasks","data":{"tasks":[]}}`), nil
		case "amq-squad status --project /repo/app --session issue-96 --json":
			return []byte(sampleCorrelationRuntimeStatusJSON), nil
		default:
			t.Fatalf("unexpected command: %s", key)
			return nil, nil
		}
	}
	c := FetchSessionCorrelation(run, ProjectSnapshot{Dir: "/repo/app"}, state.Session{
		Name:   "issue-96",
		Agents: []state.Agent{{Handle: "backend-dev", Role: "backend-dev"}},
		Coordination: state.Coordination{
			Threads: []state.ThreadSummary{{
				ID:          "p2p/backend-dev__cto",
				Kind:        state.KindStatus,
				LastFrom:    "cto",
				Subject:     "ACK: accepted",
				LastEventAt: now.Add(time.Minute),
			}},
			Messages: []state.Message{{
				ID:      "review",
				From:    "backend-dev",
				To:      []string{"cto"},
				Thread:  "p2p/backend-dev__cto",
				Subject: "DONE: implementation ready",
				Kind:    state.KindReviewRequest,
				Created: now,
				Body:    "please review",
			}, {
				ID:      "ack",
				From:    "cto",
				To:      []string{"backend-dev"},
				Thread:  "p2p/backend-dev__cto",
				Subject: "ACK: accepted",
				Kind:    state.KindStatus,
				Created: now.Add(time.Minute),
				Body:    "accepted",
			}},
		},
	})
	if len(c.WorkerReports) != 1 {
		t.Fatalf("worker reports = %+v, want only worker report history", c.WorkerReports)
	}
	report := c.WorkerReports[0]
	if report.From != "backend-dev" || report.Type != "done" || !report.Done || !report.Review {
		t.Fatalf("worker report did not preserve pre-ACK review/DONE evidence: %+v", report)
	}
	if len(c.Evidence.ReviewThreads) != 1 || c.Evidence.ReviewThreads[0] != "p2p/backend-dev__cto" {
		t.Fatalf("review worker report should count as review evidence: %+v", c.Evidence)
	}
	for _, blocker := range c.Evidence.Blockers {
		if blocker == "review report exists without review thread evidence" {
			t.Fatalf("false review blocker present: %+v", c.Evidence)
		}
	}
	for _, sig := range c.Mismatches {
		if sig.Name == "worker_report_history_unavailable" {
			t.Fatalf("history should be available from messages: %+v", c.Mismatches)
		}
	}
}

func TestFetchSessionCorrelationRuntimeStatusOverridesStaleSnapshot(t *testing.T) {
	run := func(dir string, name string, args ...string) ([]byte, error) {
		key := name + " " + strings.Join(args, " ")
		switch key {
		case "amq-squad task list --project /repo/app --session issue-96 --json":
			return []byte(`{"schema_version":1,"kind":"tasks","data":{"tasks":[]}}`), nil
		case "amq-squad status --project /repo/app --session issue-96 --json":
			return []byte(sampleCorrelationRuntimeStatusJSON), nil
		default:
			t.Fatalf("unexpected command: %s", key)
			return nil, nil
		}
	}
	c := FetchSessionCorrelation(run, ProjectSnapshot{Dir: "/repo/app"}, state.Session{
		Name: "issue-96",
		Agents: []state.Agent{
			{Handle: "backend-dev", Role: "backend-dev", Liveness: state.LivenessStale},
			{Handle: "qa", Role: "qa", Liveness: state.LivenessStale},
		},
	})
	if c.Runtime.Source != "amq-squad status --json" || c.Runtime.WorkersLive != 1 || c.Runtime.WorkersAtRisk != 1 {
		t.Fatalf("runtime status did not override stale snapshot: %+v", c.Runtime)
	}
	for _, row := range c.Runtime.Rows {
		if row.Handle == "backend-dev" {
			if row.Liveness != "live" || !row.PaneAlive || row.NextAction != "focus" || row.Source != "amq-squad status --json" {
				t.Fatalf("backend-dev row did not use runtime status: %+v", row)
			}
			return
		}
	}
	t.Fatalf("backend-dev row missing: %+v", c.Runtime.Rows)
}

func TestFetchSessionCorrelationUsesNamedProfileCommands(t *testing.T) {
	var commands []string
	run := func(dir string, name string, args ...string) ([]byte, error) {
		key := name + " " + strings.Join(args, " ")
		commands = append(commands, key)
		switch key {
		case "amq-squad task list --project /repo/app --profile review --session issue-96 --json":
			return []byte(`{"schema_version":1,"kind":"tasks","data":{"tasks":[]}}`), nil
		case "amq-squad status --project /repo/app --profile review --session issue-96 --json":
			return []byte(sampleCorrelationRuntimeStatusJSON), nil
		default:
			t.Fatalf("unexpected command: %s", key)
			return nil, nil
		}
	}
	FetchSessionCorrelation(run, ProjectSnapshot{Dir: "/repo/app"}, state.Session{
		Name:        "issue-96",
		TeamProfile: "review",
	})
	if len(commands) != 2 {
		t.Fatalf("commands = %+v", commands)
	}
}

func TestFetchSessionCorrelationDoesNotTreatGenericReviewAsReleaseEvidence(t *testing.T) {
	run := func(dir string, name string, args ...string) ([]byte, error) {
		key := name + " " + strings.Join(args, " ")
		switch key {
		case "amq-squad task list --project /repo/app --session issue-96 --json":
			return []byte(`{"schema_version":1,"kind":"tasks","data":{"tasks":[
				{"id":"t1","title":"Review UI copy","status":"pending","assigned_to":"qa"}
			]}}`), nil
		case "amq-squad status --project /repo/app --session issue-96 --json":
			return []byte(sampleCorrelationRuntimeStatusJSON), nil
		default:
			t.Fatalf("unexpected command: %s", key)
			return nil, nil
		}
	}
	c := FetchSessionCorrelation(run, ProjectSnapshot{Dir: "/repo/app"}, state.Session{
		Name: "issue-96",
		Coordination: state.Coordination{Messages: []state.Message{{
			ID:      "r1",
			From:    "qa",
			To:      []string{"cto"},
			Thread:  "p2p/cto__qa",
			Subject: "Review ready",
			Kind:    state.KindReviewRequest,
		}}},
	})
	if len(c.Evidence.Blockers) != 0 || c.Evidence.Status == healthStatusWarn {
		t.Fatalf("generic review should not produce release blockers: %+v", c.Evidence)
	}
}

func TestFetchSessionCorrelationDoesNotTreatDecisionTextAsCIEvidence(t *testing.T) {
	run := func(dir string, name string, args ...string) ([]byte, error) {
		key := name + " " + strings.Join(args, " ")
		switch key {
		case "amq-squad task list --project /repo/app --session issue-96 --json":
			return []byte(`{"schema_version":1,"kind":"tasks","data":{"tasks":[
				{"id":"t1","title":"Decision queue cleanup","status":"pending","assigned_to":"qa"}
			]}}`), nil
		case "amq-squad status --project /repo/app --session issue-96 --json":
			return []byte(sampleCorrelationRuntimeStatusJSON), nil
		default:
			t.Fatalf("unexpected command: %s", key)
			return nil, nil
		}
	}
	c := FetchSessionCorrelation(run, ProjectSnapshot{Dir: "/repo/app"}, state.Session{Name: "issue-96"})
	if len(c.Evidence.CITests) != 0 || len(c.Evidence.Blockers) != 0 || c.Evidence.Status == healthStatusWarn {
		t.Fatalf("decision text should not produce CI evidence or release blockers: %+v", c.Evidence)
	}
}

const sampleCorrelationRuntimeStatusJSON = `{"schema_version":1,"kind":"status","data":{"records":[
	{"role":"backend-dev","handle":"backend-dev","status":"live","tmux":{"session":"noc","window_id":"@1","window_name":"shell","pane_id":"%1","pane_alive":true},"actions":[
		{"kind":"focus","scope":"agent","command":"amq-squad focus --session issue-96 --role backend-dev","available":true},
		{"kind":"resume","scope":"session","command":"amq-squad resume --session issue-96 --exec","available":true}
	]},
	{"role":"qa","handle":"qa","status":"missing","actions":[
		{"kind":"resume","scope":"session","command":"amq-squad resume --session issue-96 --exec","available":true}
	]}
]}}`
