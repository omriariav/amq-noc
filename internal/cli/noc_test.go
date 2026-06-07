package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-noc/internal/console"
	"github.com/omriariav/amq-noc/internal/launch"
	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/rules"
	"github.com/omriariav/amq-noc/internal/state"
	"github.com/omriariav/amq-noc/internal/team"
)

// captureNOC records the NOCConfig executeNOC assembled, so tests can assert the
// wiring without starting a Bubble Tea program.
type captureNOC struct {
	cfg    console.NOCConfig
	called bool
}

func (c *captureNOC) run(cfg console.NOCConfig) error {
	c.cfg = cfg
	c.called = true
	return nil
}

func TestExecuteNOC_PassesRootsAndThresholds(t *testing.T) {
	var cap captureNOC
	exec := nocExecution{
		Cwd:         "/tmp/proj",
		Version:     "v-test",
		Roots:       []string{"/tmp/a", "/tmp/b"},
		Depth:       5,
		Filter:      "needs-you",
		Once:        true,
		Out:         &bytes.Buffer{},
		StdoutIsTTY: false,
		RunNOC:      cap.run,
	}
	if err := executeNOC(exec); err != nil {
		t.Fatalf("executeNOC: %v", err)
	}
	if !cap.called {
		t.Fatal("RunNOC was not called")
	}
	if len(cap.cfg.Roots) != 2 || cap.cfg.Roots[0] != "/tmp/a" {
		t.Errorf("Roots not propagated: %v", cap.cfg.Roots)
	}
	if cap.cfg.Depth != 5 {
		t.Errorf("Depth = %d, want 5", cap.cfg.Depth)
	}
	if !cap.cfg.Once {
		t.Error("Once should propagate")
	}
	if cap.cfg.InitialFilter != "needs-you" {
		t.Errorf("InitialFilter = %q, want needs-you", cap.cfg.InitialFilter)
	}
	if cap.cfg.Version != "v-test" {
		t.Errorf("Version = %q, want v-test", cap.cfg.Version)
	}
	if cap.cfg.Lifecycle == nil {
		t.Error("Lifecycle seam should be wired")
	}
	if cap.cfg.SessionCleanup == nil {
		t.Error("SessionCleanup seam should be wired")
	}
	if cap.cfg.NewSession == nil {
		t.Error("NewSession seam should be wired")
	}
	if cap.cfg.NewTeam == nil {
		t.Error("NewTeam seam should be wired")
	}
	if cap.cfg.ReadNeedsYou == nil {
		t.Error("ReadNeedsYou seam should be wired")
	}
	if cap.cfg.DrainAgent == nil {
		t.Error("DrainAgent seam should be wired")
	}
	if cap.cfg.InboxAgent == nil {
		t.Error("InboxAgent seam should be wired")
	}
	if cap.cfg.DLQAgent == nil {
		t.Error("DLQAgent seam should be wired")
	}
	if cap.cfg.DLQRead == nil {
		t.Error("DLQRead seam should be wired")
	}
	if cap.cfg.DLQRetry == nil {
		t.Error("DLQRetry seam should be wired")
	}
	if cap.cfg.DLQPurge == nil {
		t.Error("DLQPurge seam should be wired")
	}
	if cap.cfg.DLQRetryAll == nil {
		t.Error("DLQRetryAll seam should be wired")
	}
	if cap.cfg.ReceiptsAgent == nil {
		t.Error("ReceiptsAgent seam should be wired")
	}
	if cap.cfg.ReceiptsWait == nil {
		t.Error("ReceiptsWait seam should be wired")
	}
	if cap.cfg.MessageWait == nil {
		t.Error("MessageWait seam should be wired")
	}
	if cap.cfg.AMQCleanup == nil {
		t.Error("AMQCleanup seam should be wired")
	}
	if cap.cfg.ThreadContext == nil {
		t.Error("ThreadContext seam should be wired")
	}
	if cap.cfg.AMQOps == nil {
		t.Error("AMQOps seam should be wired")
	}
	if cap.cfg.AMQWho == nil {
		t.Error("AMQWho seam should be wired")
	}
	if cap.cfg.AMQEnv == nil {
		t.Error("AMQEnv seam should be wired")
	}
	if cap.cfg.Presence == nil {
		t.Error("Presence seam should be wired")
	}
	if cap.cfg.ProjectDoctor == nil {
		t.Error("ProjectDoctor seam should be wired")
	}
	if cap.cfg.ProjectHistory == nil {
		t.Error("ProjectHistory seam should be wired")
	}
	if cap.cfg.TeamRules == nil {
		t.Error("TeamRules seam should be wired")
	}
	if cap.cfg.ProjectResumePlan == nil {
		t.Error("ProjectResumePlan seam should be wired")
	}
	if cap.cfg.ForkPlan == nil {
		t.Error("ForkPlan seam should be wired")
	}
	if cap.cfg.Brief == nil {
		t.Error("Brief seam should be wired")
	}
	if cap.cfg.BriefSeed == nil {
		t.Error("BriefSeed seam should be wired")
	}
	if cap.cfg.Status == nil {
		t.Error("Status seam should be wired")
	}
}

func TestNOCStartHideStaleDefaultsToFocusedLiveTUI(t *testing.T) {
	cases := []struct {
		name         string
		once         bool
		jsonOut      bool
		actionsOut   bool
		runActionSet bool
		hideFlag     bool
		showFlag     bool
		want         bool
	}{
		{name: "live TUI hides by default", want: true},
		{name: "show stale opts live TUI out", showFlag: true, want: false},
		{name: "once remains explicit", once: true, want: false},
		{name: "json remains explicit", jsonOut: true, want: false},
		{name: "actions remains explicit", actionsOut: true, want: false},
		{name: "run action remains explicit", runActionSet: true, want: false},
		{name: "hide flag still scopes static paths", once: true, hideFlag: true, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := nocStartHideStale(tc.once, tc.jsonOut, tc.actionsOut, tc.runActionSet, tc.hideFlag, tc.showFlag)
			if got != tc.want {
				t.Fatalf("nocStartHideStale() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNOCTerminalSessionNameScopesProjectAndWorkstream(t *testing.T) {
	got := nocTerminalSessionName("/Users/me/My Project:API", "issue-200")
	want := "amq-squad-my-project-api-issue-200"
	if got != want {
		t.Fatalf("nocTerminalSessionName = %q, want %q", got, want)
	}
}

func TestConsoleProjectHistoryRejectsEmptyProject(t *testing.T) {
	_, err := consoleProjectHistory(console.ProjectHistoryRequest{})
	if err == nil {
		t.Fatal("empty project history request should fail")
	}
	if !strings.Contains(err.Error(), "project dir cannot be empty") {
		t.Fatalf("empty project history error = %v", err)
	}
}

func TestConsoleTeamRulesReadsScopedRules(t *testing.T) {
	dir := t.TempDir()
	body := "# Team Rules\n\ncustom\n"
	if err := rules.Write(dir, body); err != nil {
		t.Fatal(err)
	}

	got, err := consoleTeamRules(console.TeamRulesRequest{ProjectDir: dir})
	if err != nil {
		t.Fatalf("consoleTeamRules: %v", err)
	}
	if got.ProjectDir != dir || got.Path != rules.Path(dir) || got.Content != body {
		t.Fatalf("consoleTeamRules result = %+v, want dir/path/content", got)
	}
}

func TestConsoleTeamRulesRejectsEmptyProject(t *testing.T) {
	_, err := consoleTeamRules(console.TeamRulesRequest{})
	if err == nil {
		t.Fatal("empty team rules request should fail")
	}
	if !strings.Contains(err.Error(), "project dir cannot be empty") {
		t.Fatalf("empty team rules error = %v", err)
	}
}

func TestConsoleProjectResumePlanRejectsEmptyProject(t *testing.T) {
	_, err := consoleProjectResumePlan(console.ProjectResumePlanRequest{})
	if err == nil {
		t.Fatal("empty project resume plan request should fail")
	}
	if !strings.Contains(err.Error(), "project dir cannot be empty") {
		t.Fatalf("empty project resume plan error = %v", err)
	}
}

func TestConsoleForkPlanRejectsMissingScope(t *testing.T) {
	_, err := consoleForkPlan(console.ForkPlanRequest{})
	if err == nil {
		t.Fatal("empty fork plan request should fail")
	}
	if !strings.Contains(err.Error(), "project dir cannot be empty") {
		t.Fatalf("empty fork plan error = %v", err)
	}
	_, err = consoleForkPlan(console.ForkPlanRequest{ProjectDir: "/tmp/team"})
	if err == nil || !strings.Contains(err.Error(), "source session cannot be empty") {
		t.Fatalf("missing source fork plan error = %v", err)
	}
	_, err = consoleForkPlan(console.ForkPlanRequest{ProjectDir: "/tmp/team", FromSession: "issue-1"})
	if err == nil || !strings.Contains(err.Error(), "target session cannot be empty") {
		t.Fatalf("missing target fork plan error = %v", err)
	}
}

func TestNOCTerminalSessionNameAvoidsDuplicateSuffix(t *testing.T) {
	got := nocTerminalSessionName("/Users/me/beta", "beta")
	want := "amq-squad-beta"
	if got != want {
		t.Fatalf("nocTerminalSessionName = %q, want %q", got, want)
	}
}

func TestConsoleResumeArgsProjectScoped(t *testing.T) {
	got := consoleResumeArgs("/tmp/team home", "review", "issue-1")
	want := []string{
		"resume",
		"--project", "/tmp/team home",
		"--profile", "review",
		"--exec",
		"--target", "new-session",
		"--terminal-session", "amq-squad-team-home-issue-1",
		"--session", "issue-1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("consoleResumeArgs = %#v, want %#v", got, want)
	}
}

func TestConsoleNewSessionArgsDefaultProfile(t *testing.T) {
	got, err := consoleNewSessionArgs(console.NewSessionRequest{
		ProjectDir: "/tmp/team home",
		Session:    "issue-1",
	})
	if err != nil {
		t.Fatalf("consoleNewSessionArgs: %v", err)
	}
	want := []string{
		"new", "session",
		"--project", "/tmp/team home",
		"--target", "new-session",
		"--terminal-session", "amq-squad-team-home-issue-1",
		"issue-1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("consoleNewSessionArgs = %#v, want %#v", got, want)
	}
}

func TestConsoleNewSessionArgsNamedProfileAndSeed(t *testing.T) {
	got, err := consoleNewSessionArgs(console.NewSessionRequest{
		ProjectDir: "/tmp/team home",
		Profile:    "review",
		Session:    "issue-2",
		SeedFrom:   "issue:31",
	})
	if err != nil {
		t.Fatalf("consoleNewSessionArgs: %v", err)
	}
	want := []string{
		"new", "session",
		"--project", "/tmp/team home",
		"--profile", "review",
		"--seed-from", "issue:31",
		"--target", "new-session",
		"--terminal-session", "amq-squad-team-home-issue-2",
		"issue-2",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("consoleNewSessionArgs = %#v, want %#v", got, want)
	}
}

func TestConsoleNewSessionArgsRejectsEmptySession(t *testing.T) {
	_, err := consoleNewSessionArgs(console.NewSessionRequest{Session: " "})
	if err == nil {
		t.Fatal("consoleNewSessionArgs should reject empty session")
	}
	if !strings.Contains(err.Error(), "session name cannot be empty") {
		t.Fatalf("consoleNewSessionArgs error = %v", err)
	}
}

func TestConsoleStatusArgs(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  console.StatusRequest
		want []string
	}{
		{
			name: "project",
			req:  console.StatusRequest{ProjectDir: "/tmp/team home"},
			want: []string{"status", "--project", "/tmp/team home"},
		},
		{
			name: "session default profile",
			req:  console.StatusRequest{ProjectDir: "/tmp/team home", Session: "issue-1"},
			want: []string{"status", "--project", "/tmp/team home", "--session", "issue-1"},
		},
		{
			name: "session named profile",
			req:  console.StatusRequest{ProjectDir: "/tmp/team home", Session: "issue-1", Profile: "review"},
			want: []string{"status", "--project", "/tmp/team home", "--profile", "review", "--session", "issue-1"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := consoleStatusArgs(tc.req)
			if err != nil {
				t.Fatalf("consoleStatusArgs: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("consoleStatusArgs = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestConsoleStatusArgsRejectsEmptyProject(t *testing.T) {
	_, err := consoleStatusArgs(console.StatusRequest{ProjectDir: " "})
	if err == nil {
		t.Fatal("consoleStatusArgs should reject empty project")
	}
	if !strings.Contains(err.Error(), "project dir cannot be empty") {
		t.Fatalf("consoleStatusArgs error = %v", err)
	}
}

func TestConsoleAgentResumeArgsProjectScoped(t *testing.T) {
	got, err := consoleAgentResumeArgs(console.AgentResumeRequest{
		ProjectDir: "/tmp/team home",
		Role:       "qa",
		Session:    "issue-1",
	})
	if err != nil {
		t.Fatalf("consoleAgentResumeArgs: %v", err)
	}
	want := []string{
		"agent", "resume", "qa",
		"--project", "/tmp/team home",
		"--session", "issue-1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("consoleAgentResumeArgs = %#v, want %#v", got, want)
	}
}

func TestConsoleAgentResumeArgsRejectsEmptyRole(t *testing.T) {
	_, err := consoleAgentResumeArgs(console.AgentResumeRequest{Role: " "})
	if err == nil {
		t.Fatal("consoleAgentResumeArgs should reject empty role")
	}
	if !strings.Contains(err.Error(), "role cannot be empty") {
		t.Fatalf("consoleAgentResumeArgs error = %v", err)
	}
}

func TestConsoleSessionCleanupArgs(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  console.SessionCleanupRequest
		want []string
	}{
		{
			name: "archive",
			req:  console.SessionCleanupRequest{ProjectDir: "/tmp/team home", Session: "issue-1", Archive: true},
			want: []string{"archive", "--project", "/tmp/team home", "--yes", "issue-1"},
		},
		{
			name: "remove",
			req:  console.SessionCleanupRequest{ProjectDir: "/tmp/team home", Session: "issue-1"},
			want: []string{"rm", "--project", "/tmp/team home", "--yes", "issue-1"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := consoleSessionCleanupArgs(tc.req)
			if err != nil {
				t.Fatalf("consoleSessionCleanupArgs: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("consoleSessionCleanupArgs = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestConsoleSessionCleanupArgsRejectsEmptySession(t *testing.T) {
	_, err := consoleSessionCleanupArgs(console.SessionCleanupRequest{Session: " "})
	if err == nil {
		t.Fatal("consoleSessionCleanupArgs should reject empty session")
	}
	if !strings.Contains(err.Error(), "session name cannot be empty") {
		t.Fatalf("consoleSessionCleanupArgs error = %v", err)
	}
}

func TestConsoleDLQReadArgs(t *testing.T) {
	got, err := consoleDLQReadArgs(console.DLQReadRequest{
		Root:   "/tmp/team/.agent-mail",
		Handle: "qa",
		ID:     "dlq_123",
	})
	if err != nil {
		t.Fatalf("consoleDLQReadArgs: %v", err)
	}
	want := []string{"dlq", "read", "--root", "/tmp/team/.agent-mail", "--me", "qa", "--id", "dlq_123"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("consoleDLQReadArgs = %#v, want %#v", got, want)
	}
}

func TestConsoleDLQReadArgsRejectsEmptyID(t *testing.T) {
	_, err := consoleDLQReadArgs(console.DLQReadRequest{Root: "/tmp/root", Handle: "qa", ID: " "})
	if err == nil {
		t.Fatal("consoleDLQReadArgs should reject empty ID")
	}
	if !strings.Contains(err.Error(), "DLQ id cannot be empty") {
		t.Fatalf("consoleDLQReadArgs error = %v", err)
	}
}

func TestConsoleDLQRetryArgs(t *testing.T) {
	got, err := consoleDLQRetryArgs(console.DLQRetryRequest{
		Root:   "/tmp/team/.agent-mail",
		Handle: "qa",
		ID:     "dlq_123",
	})
	if err != nil {
		t.Fatalf("consoleDLQRetryArgs: %v", err)
	}
	want := []string{"dlq", "retry", "--root", "/tmp/team/.agent-mail", "--me", "qa", "--id", "dlq_123"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("consoleDLQRetryArgs = %#v, want %#v", got, want)
	}
}

func TestConsoleDLQRetryArgsRejectsEmptyID(t *testing.T) {
	_, err := consoleDLQRetryArgs(console.DLQRetryRequest{Root: "/tmp/root", Handle: "qa", ID: " "})
	if err == nil {
		t.Fatal("consoleDLQRetryArgs should reject empty ID")
	}
	if !strings.Contains(err.Error(), "DLQ id cannot be empty") {
		t.Fatalf("consoleDLQRetryArgs error = %v", err)
	}
}

func TestConsoleDLQPurgeArgs(t *testing.T) {
	got, err := consoleDLQPurgeArgs(console.DLQPurgeRequest{
		Root:      "/tmp/team/.agent-mail",
		Handle:    "qa",
		OlderThan: "168h",
	})
	if err != nil {
		t.Fatalf("consoleDLQPurgeArgs: %v", err)
	}
	want := []string{"dlq", "purge", "--root", "/tmp/team/.agent-mail", "--me", "qa", "--older-than", "168h", "--yes"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("consoleDLQPurgeArgs = %#v, want %#v", got, want)
	}
}

func TestConsoleDLQPurgeArgsRejectsInvalidAge(t *testing.T) {
	_, err := consoleDLQPurgeArgs(console.DLQPurgeRequest{Root: "/tmp/root", Handle: "qa", OlderThan: "0s"})
	if err == nil {
		t.Fatal("consoleDLQPurgeArgs should reject invalid age")
	}
	if !strings.Contains(err.Error(), "positive duration") {
		t.Fatalf("consoleDLQPurgeArgs error = %v", err)
	}
}

func TestConsoleDLQRetryAllArgs(t *testing.T) {
	got, err := consoleDLQRetryAllArgs(console.DLQRetryAllRequest{
		Root:   "/tmp/team/.agent-mail",
		Handle: "qa",
	})
	if err != nil {
		t.Fatalf("consoleDLQRetryAllArgs: %v", err)
	}
	want := []string{"dlq", "retry", "--root", "/tmp/team/.agent-mail", "--me", "qa", "--all"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("consoleDLQRetryAllArgs = %#v, want %#v", got, want)
	}
}

func TestConsoleDLQRetryAllArgsRejectsEmptyHandle(t *testing.T) {
	_, err := consoleDLQRetryAllArgs(console.DLQRetryAllRequest{Root: "/tmp/root", Handle: " "})
	if err == nil {
		t.Fatal("consoleDLQRetryAllArgs should reject empty handle")
	}
	if !strings.Contains(err.Error(), "agent handle cannot be empty") {
		t.Fatalf("consoleDLQRetryAllArgs error = %v", err)
	}
}

func TestConsoleReceiptsWaitArgs(t *testing.T) {
	got, err := consoleReceiptsWaitArgs(console.ReceiptsWaitRequest{
		Root:    "/tmp/team/.agent-mail",
		Handle:  "qa",
		MsgID:   "msg_123",
		Stage:   "drained",
		Timeout: "60s",
	})
	if err != nil {
		t.Fatalf("consoleReceiptsWaitArgs: %v", err)
	}
	want := []string{"receipts", "wait", "--root", "/tmp/team/.agent-mail", "--me", "qa", "--msg-id", "msg_123", "--stage", "drained", "--timeout", "60s"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("consoleReceiptsWaitArgs = %#v, want %#v", got, want)
	}
}

func TestConsoleReceiptsWaitArgsRejectsInvalidStage(t *testing.T) {
	_, err := consoleReceiptsWaitArgs(console.ReceiptsWaitRequest{
		Root:    "/tmp/root",
		Handle:  "qa",
		MsgID:   "msg_123",
		Stage:   "sent",
		Timeout: "60s",
	})
	if err == nil {
		t.Fatal("consoleReceiptsWaitArgs should reject invalid stage")
	}
	if !strings.Contains(err.Error(), "drained or dlq") {
		t.Fatalf("consoleReceiptsWaitArgs error = %v", err)
	}
}

func TestConsoleReceiptsWaitArgsRejectsNegativeTimeout(t *testing.T) {
	_, err := consoleReceiptsWaitArgs(console.ReceiptsWaitRequest{
		Root:    "/tmp/root",
		Handle:  "qa",
		MsgID:   "msg_123",
		Stage:   "drained",
		Timeout: "-1s",
	})
	if err == nil {
		t.Fatal("consoleReceiptsWaitArgs should reject negative timeout")
	}
	if !strings.Contains(err.Error(), "non-negative duration") {
		t.Fatalf("consoleReceiptsWaitArgs error = %v", err)
	}
}

func TestConsoleMessageWaitArgs(t *testing.T) {
	got, err := consoleMessageWaitArgs(console.MessageWaitRequest{
		Root:    "/tmp/team/.agent-mail",
		Handle:  "qa",
		Body:    "Please check logs",
		Timeout: "60s",
	})
	if err != nil {
		t.Fatalf("consoleMessageWaitArgs: %v", err)
	}
	want := []string{
		"send",
		"--root", "/tmp/team/.agent-mail",
		"--me", "user",
		"--to", "qa",
		"--subject", "Message from operator",
		"--body", "Please check logs",
		"--kind", "status",
		"--wait-for", "drained",
		"--wait-timeout", "60s",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("consoleMessageWaitArgs = %#v, want %#v", got, want)
	}
}

func TestConsoleMessageWaitArgsRejectsNegativeTimeout(t *testing.T) {
	_, err := consoleMessageWaitArgs(console.MessageWaitRequest{
		Root:    "/tmp/root",
		Handle:  "qa",
		Body:    "Please check logs",
		Timeout: "-1s",
	})
	if err == nil {
		t.Fatal("consoleMessageWaitArgs should reject negative timeout")
	}
	if !strings.Contains(err.Error(), "non-negative duration") {
		t.Fatalf("consoleMessageWaitArgs error = %v", err)
	}
}

func TestConsoleAMQCleanupArgs(t *testing.T) {
	got, err := consoleAMQCleanupArgs(console.AMQCleanupRequest{
		Root:         "/tmp/team/.agent-mail",
		TmpOlderThan: "36h",
	})
	if err != nil {
		t.Fatalf("consoleAMQCleanupArgs: %v", err)
	}
	want := []string{"cleanup", "--root", "/tmp/team/.agent-mail", "--tmp-older-than", "36h", "--yes"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("consoleAMQCleanupArgs = %#v, want %#v", got, want)
	}
}

func TestConsoleAMQCleanupArgsRejectsInvalidAge(t *testing.T) {
	_, err := consoleAMQCleanupArgs(console.AMQCleanupRequest{Root: "/tmp/root", TmpOlderThan: "0s"})
	if err == nil {
		t.Fatal("consoleAMQCleanupArgs should reject invalid age")
	}
	if !strings.Contains(err.Error(), "positive duration") {
		t.Fatalf("consoleAMQCleanupArgs error = %v", err)
	}
}

func TestConsoleNewTeamArgsDefaultProfile(t *testing.T) {
	got, err := consoleNewTeamArgs(console.NewTeamRequest{
		ProjectDir: "/tmp/team home",
		Roles:      "cto,qa",
		Binary:     "qa=codex",
		Session:    "issue-96",
	})
	if err != nil {
		t.Fatalf("consoleNewTeamArgs: %v", err)
	}
	want := []string{
		"new", "team",
		"--project", "/tmp/team home",
		"--roles", "cto,qa",
		"--binary", "qa=codex",
		"--session", "issue-96",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("consoleNewTeamArgs = %#v, want %#v", got, want)
	}
}

func TestConsoleNewTeamArgsNamedProfile(t *testing.T) {
	got, err := consoleNewTeamArgs(console.NewTeamRequest{
		ProjectDir: "/tmp/team home",
		Profile:    "review",
		Roles:      "cto,qa",
		Binary:     "qa=codex",
		Session:    "issue-96",
	})
	if err != nil {
		t.Fatalf("consoleNewTeamArgs: %v", err)
	}
	want := []string{
		"new", "profile", "review",
		"--project", "/tmp/team home",
		"--roles", "cto,qa",
		"--binary", "qa=codex",
		"--session", "issue-96",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("consoleNewTeamArgs = %#v, want %#v", got, want)
	}
}

func TestConsoleNewTeamArgsRejectsEmptyRoles(t *testing.T) {
	_, err := consoleNewTeamArgs(console.NewTeamRequest{Roles: " "})
	if err == nil {
		t.Fatal("consoleNewTeamArgs should reject empty roles")
	}
	if !strings.Contains(err.Error(), "roles cannot be empty") {
		t.Fatalf("consoleNewTeamArgs error = %v", err)
	}
}

func TestNOCNewTeamTemplateCommandNamedProfile(t *testing.T) {
	got := nocNewTeamTemplateCommand("/tmp/team home", "review")
	for _, want := range []string{
		"amq-squad new profile review",
		"--project '/tmp/team home'",
		"--roles '<roles>'",
		"--binary '<binary>'",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("named profile template missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "new team --profile") {
		t.Fatalf("named profile template should use new profile command: %s", got)
	}
	if strings.Contains(got, "--sync") {
		t.Fatalf("new profile template should not run pointer sync implicitly: %s", got)
	}
}

func TestNOCGeneratedSquadCommandsDelegateToAMQSquad(t *testing.T) {
	// Default: every delegated squad lifecycle/config command renders as the
	// installed amq-squad CLI, never the running amq-noc executable (whose
	// public surface is only noc/version, so an amq-noc path would be unrunnable).
	defaults := map[string]string{
		"new team":    nocNewTeamTemplateCommand("/repo/app", team.DefaultProfile),
		"new session": nocNewSessionTemplateCommand("/repo/app", team.DefaultProfile),
		"resume":      shellCommand("amq-squad", "resume", "--project", "/repo/app"),
		"status":      shellCommand("amq-squad", "status", "--project", "/repo/app"),
		"team rm":     shellCommand("amq-squad", "team", "rm", "--project", "/repo/app", "--yes"),
	}
	for name, got := range defaults {
		if !strings.HasPrefix(got, "amq-squad ") {
			t.Fatalf("%s command should delegate to amq-squad, got %q", name, got)
		}
		if strings.HasPrefix(got, "/") {
			t.Fatalf("%s command leaked an executable path instead of amq-squad: %q", name, got)
		}
	}

	// The generatedSquadCommandOverride hook still redirects generated commands
	// (used by tests) to a stub binary.
	old := generatedSquadCommandOverride
	generatedSquadCommandOverride = "/tmp/amq2"
	t.Cleanup(func() { generatedSquadCommandOverride = old })

	if got := nocNewTeamTemplateCommand("/repo/app", team.DefaultProfile); !strings.HasPrefix(got, "/tmp/amq2 new team ") {
		t.Fatalf("override should redirect new-team command, got %q", got)
	}
	if got := nocNewSessionTemplateCommand("/repo/app", team.DefaultProfile); !strings.HasPrefix(got, "/tmp/amq2 new session ") {
		t.Fatalf("override should redirect new-session command, got %q", got)
	}
}

func TestExecuteNOC_NoRootDefaultsToCwd(t *testing.T) {
	var cap captureNOC
	cwd := t.TempDir() // not an amq project (no .agent-mail)
	exec := nocExecution{
		Cwd:         cwd,
		Once:        true,
		Out:         &bytes.Buffer{},
		StdoutIsTTY: false,
		RunNOC:      cap.run,
	}
	if err := executeNOC(exec); err != nil {
		t.Fatalf("executeNOC: %v", err)
	}
	if len(cap.cfg.Roots) != 1 || cap.cfg.Roots[0] != cwd {
		t.Errorf("default root should be cwd %q, got %v", cwd, cap.cfg.Roots)
	}
}

func TestExecuteNOC_ProjectCwdDefaultsToParent(t *testing.T) {
	var cap captureNOC
	parent := t.TempDir()
	proj := filepath.Join(parent, "myproj")
	if err := mkAgentMail(proj); err != nil {
		t.Fatal(err)
	}
	exec := nocExecution{
		Cwd:         proj,
		Once:        true,
		Out:         &bytes.Buffer{},
		StdoutIsTTY: false,
		RunNOC:      cap.run,
	}
	if err := executeNOC(exec); err != nil {
		t.Fatalf("executeNOC: %v", err)
	}
	// A cwd that IS an amq project defaults its scan root to the PARENT so sibling
	// squads appear.
	if len(cap.cfg.Roots) != 1 || cap.cfg.Roots[0] != parent {
		t.Errorf("project cwd should default root to parent %q, got %v", parent, cap.cfg.Roots)
	}
}

func TestExecuteNOC_NoTTYForcesOnce(t *testing.T) {
	var cap captureNOC
	exec := nocExecution{
		Cwd:         "/tmp/proj",
		Roots:       []string{"/tmp/a"},
		Once:        false, // interactive requested
		Out:         &bytes.Buffer{},
		StdoutIsTTY: false, // but no TTY
		RunNOC:      cap.run,
	}
	if err := executeNOC(exec); err != nil {
		t.Fatalf("executeNOC: %v", err)
	}
	if !cap.cfg.Once {
		t.Error("no TTY should force Once=true so a piped invocation still works")
	}
}

func TestNOCSessionEnvelopeCapsThreadSummaries(t *testing.T) {
	threads := make([]state.ThreadSummary, 0, defaultThreadsLimit+5)
	for i := 0; i < defaultThreadsLimit+5; i++ {
		threads = append(threads, state.ThreadSummary{
			ID:           fmt.Sprintf("thread/%02d", i),
			Subject:      "thread summary",
			Status:       state.ThreadOpen,
			Triage:       state.TriageClear,
			MessageCount: 1,
		})
	}
	row := nocSessionEnvelope(noc.ProjectSnapshot{Dir: "/root/api"}, state.Session{
		Name:         "issue-96",
		Coordination: state.Coordination{Threads: threads},
	})
	if row.ThreadCount != defaultThreadsLimit+5 {
		t.Fatalf("thread_count = %d, want %d", row.ThreadCount, defaultThreadsLimit+5)
	}
	if row.ThreadsReturned != defaultThreadsLimit || len(row.Threads) != defaultThreadsLimit {
		t.Fatalf("threads returned = %d/%d, want %d", row.ThreadsReturned, len(row.Threads), defaultThreadsLimit)
	}
}

func TestNOCSessionEnvelopeExposesAttention(t *testing.T) {
	row := nocSessionEnvelope(noc.ProjectSnapshot{Dir: "/root/api"}, state.Session{
		Name:             "issue-96",
		Attention:        state.Attention{State: state.TriageNeedsYou, Reason: state.AttnApprove},
		UnownedAttention: state.Attention{State: state.TriageBlocked},
		Agents: []state.Agent{
			{Handle: "cto", Liveness: state.LivenessAlive, Attention: state.Attention{State: state.TriageNeedsYou, Reason: state.AttnApprove}},
			{Handle: "qa", Liveness: state.LivenessAlive, Attention: state.Attention{State: state.TriageClear}},
		},
	})
	if row.Attention != "needs-you" || row.AttentionReason != "approve" {
		t.Fatalf("session attention = %q/%q, want needs-you/approve", row.Attention, row.AttentionReason)
	}
	if row.UnownedEvidence != "blocked" {
		t.Fatalf("unowned_evidence = %q, want blocked", row.UnownedEvidence)
	}
	byHandle := map[string]nocAgentJSONData{}
	for _, a := range row.Agents {
		byHandle[a.Handle] = a
	}
	if byHandle["cto"].Attention != "needs-you" || byHandle["cto"].AttentionReason != "approve" {
		t.Fatalf("cto attention = %q/%q, want needs-you/approve", byHandle["cto"].Attention, byHandle["cto"].AttentionReason)
	}
	if byHandle["qa"].Attention != "clear" || byHandle["qa"].AttentionReason != "" {
		t.Fatalf("qa attention = %q/%q, want clear/empty", byHandle["qa"].Attention, byHandle["qa"].AttentionReason)
	}

	// A clear session omits unowned_evidence but still reports its headline.
	clearRow := nocSessionEnvelope(noc.ProjectSnapshot{Dir: "/root/api"}, state.Session{
		Name:      "issue-97",
		Attention: state.Attention{State: state.TriageClear},
	})
	if clearRow.UnownedEvidence != "" {
		t.Fatalf("unowned_evidence = %q, want empty when clear", clearRow.UnownedEvidence)
	}
	if clearRow.Attention != "clear" {
		t.Fatalf("session attention = %q, want clear", clearRow.Attention)
	}
}

func TestNOCProjectEnvelopeLabelsHistoricalNeedsYouAsNonLive(t *testing.T) {
	ps := noc.ProjectSnapshot{
		Project: "api",
		Dir:     "/root/api",
		Snap: state.Snapshot{Rollup: state.TriageRollup{NeedsYouHistorical: 1}, Sessions: []state.Session{{
			Name: "issue-96",
			Agents: []state.Agent{
				{Handle: "cto", Liveness: state.LivenessAlive},
				{Handle: "qa", Liveness: state.LivenessAlive},
			},
			Coordination: state.Coordination{Threads: []state.ThreadSummary{{
				ID:         "ask/old",
				Subject:    "APPROVAL: deploy?",
				Triage:     state.TriageNeedsYou,
				AttnReason: state.AttnApprove,
				Historical: true,
			}}},
			Rollup: state.TriageRollup{NeedsYouHistorical: 1},
		}}},
	}
	project := nocProjectEnvelope(ps)
	if project.State == "needs-you" || project.Rollup.NeedsYou != 0 || project.Rollup.NeedsYouHistorical != 1 {
		t.Fatalf("project state/rollup = %s/%+v, want non-live historical needs-you", project.State, project.Rollup)
	}
	if len(project.Sessions) != 1 || project.Sessions[0].Rollup.NeedsYouHistorical != 1 {
		t.Fatalf("session historical rollup missing: %+v", project.Sessions)
	}
	if len(project.Sessions[0].Threads) != 1 || !project.Sessions[0].Threads[0].Historical {
		t.Fatalf("thread historical marker missing: %+v", project.Sessions[0].Threads)
	}
}

func TestNOCSessionEnvelopeIncludesThreadIntegrationMetadata(t *testing.T) {
	row := nocSessionEnvelope(noc.ProjectSnapshot{Dir: "/root/api"}, state.Session{
		Name: "issue-96",
		Coordination: state.Coordination{Threads: []state.ThreadSummary{{
			ID:           "handoff/test",
			Subject:      "handoff",
			Status:       state.ThreadAwaitingReply,
			Triage:       state.TriageBlocked,
			Labels:       []string{"blocking", "handoff"},
			Orchestrator: "kanban",
			FromProject:  "web",
			ReplyProject: "api",
		}}},
	})
	if len(row.Threads) != 1 {
		t.Fatalf("threads = %+v, want one", row.Threads)
	}
	th := row.Threads[0]
	if strings.Join(th.Labels, ",") != "blocking,handoff" || th.Orchestrator != "kanban" || th.FromProject != "web" || th.ReplyProject != "api" {
		t.Fatalf("thread metadata mismatch: %+v", th)
	}
}

func TestRunNOCJSONEnvelope(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	seedNOCOperatorTeam(t, proj)
	base := filepath.Join(proj, noc.AgentMailDirName)
	sessionRoot := filepath.Join(base, "issue-96")
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    sessionRoot,
	})

	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--json", "--root", root})
	})
	if err != nil {
		t.Fatalf("runNOC --json: %v", err)
	}
	env := decodeJSONEnvelope[nocSnapshotEnvelopeData](t, stdout)
	if env.Kind != "noc_snapshot" {
		t.Errorf("kind = %q, want noc_snapshot", env.Kind)
	}
	if env.Data.ProjectCount != 1 || len(env.Data.Projects) != 1 {
		t.Fatalf("project count/list = %d/%d, want 1/1", env.Data.ProjectCount, len(env.Data.Projects))
	}
	project := env.Data.Projects[0]
	if project.Project != "p" {
		t.Errorf("project = %q, want p", project.Project)
	}
	if project.State != "stopped" {
		t.Errorf("project state = %q, want stopped", project.State)
	}
	if project.SessionCount != 1 || len(project.Sessions) != 1 {
		t.Fatalf("session count/list = %d/%d, want 1/1", project.SessionCount, len(project.Sessions))
	}
	session := project.Sessions[0]
	if session.Name != "issue-96" || session.AgentsTotal != 1 || len(session.Agents) != 1 {
		t.Fatalf("session row = %+v, want issue-96 with one agent", session)
	}
	agent := session.Agents[0]
	if agent.Handle != "cto" || agent.TeamProfile != "default" {
		t.Errorf("agent = %+v, want cto default profile", agent)
	}
	if containsCLI(stdout, "amq-noc NOC") {
		t.Fatalf("noc --json leaked human NOC render:\n%s", stdout)
	}
}

func TestRunNOCActionsHumanTable(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	seedNOCOperatorTeam(t, proj)
	base := filepath.Join(proj, noc.AgentMailDirName)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    filepath.Join(base, "issue-96"),
	})
	seedAgentRecord(t, base, "drive-fix", "qa", launch.Record{
		Binary:  "claude",
		Role:    "qa",
		Handle:  "qa",
		Session: "drive-fix",
		Root:    filepath.Join(base, "drive-fix"),
	})

	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--actions", "--root", root, "--filter", "session:issue-96"})
	})
	if err != nil {
		t.Fatalf("runNOC --actions: %v", err)
	}
	for _, want := range []string{"ID", "SCOPE", "VARS", "COMMAND", "session|", "roles", "amq-squad resume"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("actions table missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "drive-fix") {
		t.Fatalf("filtered actions table leaked drive-fix:\n%s", stdout)
	}
}

func TestRunNOCActionsHumanTableShowsProfileChoices(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	seedNOCOperatorTeam(t, proj)
	base := filepath.Join(proj, noc.AgentMailDirName)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:      "codex",
		Role:        "cto",
		Handle:      "cto",
		Session:     "issue-96",
		Root:        filepath.Join(base, "issue-96"),
		TeamProfile: team.DefaultProfile,
	})
	seedAgentRecord(t, base, "issue-96", "qa", launch.Record{
		Binary:      "claude",
		Role:        "qa",
		Handle:      "qa",
		Session:     "issue-96",
		Root:        filepath.Join(base, "issue-96"),
		TeamProfile: "review",
	})

	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--actions", "--root", root, "--filter", "session:issue-96", "--action", "resume"})
	})
	if err != nil {
		t.Fatalf("runNOC --actions mixed profile: %v", err)
	}
	if !strings.Contains(stdout, "profile[default|review]") {
		t.Fatalf("actions table should show profile choices:\n%s", stdout)
	}
}

func TestRunNOCActionsHumanTableShowsRoleExamples(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--actions", "--root", root, "--filter", "project:p", "--action", "new_team"})
	})
	if err != nil {
		t.Fatalf("runNOC --actions new_team: %v", err)
	}
	if !strings.Contains(stdout, "roles(ex:cto,qa)") {
		t.Fatalf("actions table should show role examples:\n%s", stdout)
	}
	if !strings.Contains(stdout, "binary(optional)") {
		t.Fatalf("actions table should show binary as optional:\n%s", stdout)
	}
}

func TestRunNOCActionsJSONEnvelope(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	seedNOCOperatorTeam(t, proj)
	base := filepath.Join(proj, noc.AgentMailDirName)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    filepath.Join(base, "issue-96"),
	})

	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--actions", "--json", "--root", root, "--filter", "session:issue-96"})
	})
	if err != nil {
		t.Fatalf("runNOC --actions --json: %v", err)
	}
	env := decodeJSONEnvelope[nocActionsEnvelopeData](t, stdout)
	if env.Kind != "noc_actions" {
		t.Fatalf("kind = %q, want noc_actions", env.Kind)
	}
	if env.Data.Filter != "session:issue-96" {
		t.Fatalf("filter = %q, want session:issue-96", env.Data.Filter)
	}
	if env.Data.ActionCount != len(env.Data.Actions) || env.Data.ActionCount == 0 {
		t.Fatalf("action_count = %d len(actions) = %d, want nonzero and consistent",
			env.Data.ActionCount, len(env.Data.Actions))
	}
	if !hasNOCActionID(env.Data.Actions, "session|"+proj+"|issue-96|action|resume") {
		t.Fatalf("noc_actions missing session resume action: %+v", env.Data.Actions)
	}
	if !hasNOCActionID(env.Data.Actions, "agent|"+proj+"|issue-96|cto|action|dlq") {
		t.Fatalf("noc_actions missing agent DLQ action: %+v", env.Data.Actions)
	}
	if !hasNOCActionID(env.Data.Actions, "agent|"+proj+"|issue-96|cto|action|receipts") {
		t.Fatalf("noc_actions missing agent receipts action: %+v", env.Data.Actions)
	}
	if !hasNOCActionID(env.Data.Actions, "agent|"+proj+"|issue-96|cto|action|receipts_wait") {
		t.Fatalf("noc_actions missing agent receipts_wait action: %+v", env.Data.Actions)
	}
	if !hasNOCActionID(env.Data.Actions, "agent|"+proj+"|issue-96|cto|action|message_wait") {
		t.Fatalf("noc_actions missing agent message_wait action: %+v", env.Data.Actions)
	}
	if !hasNOCActionID(env.Data.Actions, "session|"+proj+"|issue-96|action|amq_cleanup") {
		t.Fatalf("noc_actions missing session amq_cleanup action: %+v", env.Data.Actions)
	}
}

func TestRunNOCActionsSelectors(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    filepath.Join(base, "issue-96"),
	})

	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--actions", "--json", "--root", root, "--action", "resume", "--mutating"})
	})
	if err != nil {
		t.Fatalf("runNOC --actions --action resume --mutating: %v", err)
	}
	env := decodeJSONEnvelope[nocActionsEnvelopeData](t, stdout)
	if env.Data.ActionCount == 0 {
		t.Fatal("expected at least one selected action")
	}
	for _, action := range env.Data.Actions {
		if action.Name != "resume" {
			t.Fatalf("--action resume leaked %q action: %+v", action.Name, action)
		}
		if !action.Mutates {
			t.Fatalf("--mutating leaked read-only action: %+v", action)
		}
	}
	if !hasNOCActionID(env.Data.Actions, "session|"+proj+"|issue-96|action|resume") {
		t.Fatalf("selected actions missing resume action: %+v", env.Data.Actions)
	}
}

func TestRunNOCActionsExactSelectors(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    filepath.Join(base, "issue-96"),
	})
	seedAgentRecord(t, base, "drive-fix", "qa", launch.Record{
		Binary:  "claude",
		Role:    "qa",
		Handle:  "qa",
		Session: "drive-fix",
		Root:    filepath.Join(base, "drive-fix"),
	})

	actionID := "session|" + proj + "|issue-96|action|resume"
	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--actions", "--root", root, "--action-id", actionID, "--commands"})
	})
	if err != nil {
		t.Fatalf("runNOC --actions --action-id --commands: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 1 {
		t.Fatalf("commands-only output lines = %d, want 1:\n%s", len(lines), stdout)
	}
	if strings.Contains(stdout, "ID") || strings.Contains(stdout, "drive-fix") {
		t.Fatalf("exact action-id selector output mismatch:\n%s", stdout)
	}
	if !strings.Contains(lines[0], "amq-squad resume --project "+proj) ||
		!strings.Contains(lines[0], "--session issue-96") {
		t.Fatalf("exact action-id selector should print issue-96 resume command:\n%s", stdout)
	}
}

func TestRunNOCActionsTargetAndScopeSelectors(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    filepath.Join(base, "issue-96"),
	})
	seedAgentRecord(t, base, "drive-fix", "qa", launch.Record{
		Binary:  "claude",
		Role:    "qa",
		Handle:  "qa",
		Session: "drive-fix",
		Root:    filepath.Join(base, "drive-fix"),
	})

	targetID := "session|" + proj + "|issue-96"
	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--actions", "--json", "--root", root, "--target-id", targetID, "--scope", "session", "--mutating"})
	})
	if err != nil {
		t.Fatalf("runNOC --actions --target-id --scope --mutating: %v", err)
	}
	env := decodeJSONEnvelope[nocActionsEnvelopeData](t, stdout)
	if env.Data.ActionCount == 0 {
		t.Fatal("expected selected session actions")
	}
	for _, action := range env.Data.Actions {
		if action.Scope != "session" || action.TargetID != targetID || !action.Mutates {
			t.Fatalf("target/scope selector leaked action: %+v", action)
		}
		if strings.Contains(action.ID, "drive-fix") || strings.Contains(action.Command, "drive-fix") {
			t.Fatalf("target/scope selector leaked drive-fix action: %+v", action)
		}
	}
	if !hasNOCActionID(env.Data.Actions, "session|"+proj+"|issue-96|action|resume") {
		t.Fatalf("selected actions missing issue-96 resume: %+v", env.Data.Actions)
	}
}

func TestExecuteNOCRunActionExecutesReadOnlyAction(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := mkAgentMail(proj); err != nil {
		t.Fatal(err)
	}
	var ran string
	var out bytes.Buffer
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|status",
		Out:         &out,
		RunActionCommand: func(command string) error {
			ran = command
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeNOC --run-action status: %v", err)
	}
	if !strings.Contains(ran, "amq-squad status --project "+proj) {
		t.Fatalf("run-action executed %q, want project status", ran)
	}
	if !strings.Contains(out.String(), "NOC action: project|"+proj+"|action|status") {
		t.Fatalf("run-action preview missing action id:\n%s", out.String())
	}
}

func TestExecuteNOCRunActionRequiresConfirmationForMutatingAction(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    filepath.Join(base, "issue-96"),
	})
	var ran bool
	var out bytes.Buffer
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "session|" + proj + "|issue-96|action|resume",
		Out:         &out,
		Confirm:     strings.NewReader("n\n"),
		RunActionCommand: func(string) error {
			ran = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeNOC declined mutating action: %v", err)
	}
	if ran {
		t.Fatal("declined mutating action should not run command")
	}
	if !strings.Contains(out.String(), "no command executed") {
		t.Fatalf("declined mutating action should report abort:\n%s", out.String())
	}
}

func TestExecuteNOCRunActionFillsSessionCleanupCommands(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    filepath.Join(base, "issue-96"),
	})

	for _, tc := range []struct {
		name string
		want string
	}{
		{name: "archive", want: "amq-squad archive --project " + proj + " --yes issue-96"},
		{name: "remove", want: "amq-squad rm --project " + proj + " --yes issue-96"},
	} {
		var ran string
		err := executeNOC(nocExecution{
			Cwd:         root,
			Roots:       []string{root},
			Depth:       noc.DefaultDepth,
			RunActionID: "session|" + proj + "|issue-96|action|" + tc.name,
			Yes:         true,
			Out:         &bytes.Buffer{},
			RunActionCommand: func(command string) error {
				ran = command
				return nil
			},
		})
		if err != nil {
			t.Fatalf("executeNOC --run-action %s: %v", tc.name, err)
		}
		if ran != tc.want {
			t.Fatalf("%s command = %q, want %q", tc.name, ran, tc.want)
		}
	}
}

func TestExecuteNOCRunActionRejectsProfileOutsideChoices(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:      "codex",
		Role:        "cto",
		Handle:      "cto",
		Session:     "issue-96",
		Root:        filepath.Join(base, "issue-96"),
		TeamProfile: team.DefaultProfile,
	})
	seedAgentRecord(t, base, "issue-96", "qa", launch.Record{
		Binary:      "claude",
		Role:        "qa",
		Handle:      "qa",
		Session:     "issue-96",
		Root:        filepath.Join(base, "issue-96"),
		TeamProfile: "review",
	})

	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "session|" + proj + "|issue-96|action|resume",
		ActionVars:  map[string]string{"profile": "ghost"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(string) error {
			t.Fatal("invalid profile choice should not run")
			return nil
		},
	})
	if err == nil {
		t.Fatal("invalid profile choice should fail")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	for _, want := range []string{`profile="ghost"`, "choose one of: default, review"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("invalid profile choice error missing %q: %v", want, err)
		}
	}
}

func TestExecuteNOCRunActionAcceptsProfileChoice(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:      "codex",
		Role:        "cto",
		Handle:      "cto",
		Session:     "issue-96",
		Root:        filepath.Join(base, "issue-96"),
		TeamProfile: team.DefaultProfile,
	})
	seedAgentRecord(t, base, "issue-96", "qa", launch.Record{
		Binary:      "claude",
		Role:        "qa",
		Handle:      "qa",
		Session:     "issue-96",
		Root:        filepath.Join(base, "issue-96"),
		TeamProfile: "review",
	})
	var out bytes.Buffer
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "session|" + proj + "|issue-96|action|resume",
		ActionVars:  map[string]string{"profile": "review"},
		DryRun:      true,
		Out:         &out,
		RunActionCommand: func(string) error {
			t.Fatal("dry-run action should not execute")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("valid profile choice should render: %v", err)
	}
	if !strings.Contains(out.String(), "--profile review") {
		t.Fatalf("valid profile choice should be rendered in command:\n%s", out.String())
	}
}

func TestExecuteNOCRunActionMissingProfileNamesChoices(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:      "codex",
		Role:        "cto",
		Handle:      "cto",
		Session:     "issue-96",
		Root:        filepath.Join(base, "issue-96"),
		TeamProfile: team.DefaultProfile,
	})
	seedAgentRecord(t, base, "issue-96", "qa", launch.Record{
		Binary:      "claude",
		Role:        "qa",
		Handle:      "qa",
		Session:     "issue-96",
		Root:        filepath.Join(base, "issue-96"),
		TeamProfile: "review",
	})
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "session|" + proj + "|issue-96|action|resume",
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(string) error {
			t.Fatal("missing profile value should not run")
			return nil
		},
	})
	if err == nil {
		t.Fatal("missing profile value should fail")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	for _, want := range []string{"--set profile=<value>", "choices: default, review"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("missing profile error missing %q: %v", want, err)
		}
	}
}

func TestExecuteNOCRunActionDryRunDoesNotExecute(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := mkAgentMail(proj); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|status",
		DryRun:      true,
		Out:         &out,
		RunActionCommand: func(string) error {
			t.Fatal("dry-run action should not execute")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeNOC --run-action --dry-run: %v", err)
	}
	if !strings.Contains(out.String(), "Dry run: no command executed.") {
		t.Fatalf("dry-run output missing no-execute notice:\n%s", out.String())
	}
}

func TestRunNOCActionDryRunJSONEnvelope(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := team.Write(proj, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto"}},
	}); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{
			"--root", root,
			"--filter", "project:p",
			"--run-action", "new_session",
			"--set", "session=issue-99",
			"--dry-run",
			"--json",
		})
	})
	if err != nil {
		t.Fatalf("runNOC --run-action --dry-run --json: %v", err)
	}
	env := decodeJSONEnvelope[nocActionPlanEnvelopeData](t, stdout)
	if env.Kind != "noc_action_plan" {
		t.Fatalf("kind = %q, want noc_action_plan", env.Kind)
	}
	if !env.Data.DryRun || env.Data.WouldExecute {
		t.Fatalf("dry_run/would_execute = %v/%v, want true/false", env.Data.DryRun, env.Data.WouldExecute)
	}
	if env.Data.Selector != "new_session" || env.Data.Action.Name != "new_session" {
		t.Fatalf("selector/action = %q/%q, want new_session", env.Data.Selector, env.Data.Action.Name)
	}
	if !strings.Contains(env.Data.Command, "amq-squad new session") ||
		!strings.Contains(env.Data.Command, "issue-99") ||
		strings.Contains(env.Data.Command, "<session>") {
		t.Fatalf("dry-run command not rendered correctly: %s", env.Data.Command)
	}
	if env.Data.TemplateValues["session"] != "issue-99" ||
		env.Data.TemplateValues["tmux-session"] != "amq-squad-p-issue-99" {
		t.Fatalf("template values = %+v, want session and derived tmux-session", env.Data.TemplateValues)
	}
	requirePreflight(t, env.Data.Preflight, "session_valid", "ok")
	requirePreflight(t, env.Data.Preflight, "session_available", "ok")
}

func requirePreflight(t *testing.T, checks []nocPreflightData, name, status string) nocPreflightData {
	t.Helper()
	for _, check := range checks {
		if check.Check != name {
			continue
		}
		if check.Status != status {
			t.Fatalf("preflight %s status = %q, want %q; checks = %+v", name, check.Status, status, checks)
		}
		return check
	}
	t.Fatalf("preflight missing %s/%s; checks = %+v", name, status, checks)
	return nocPreflightData{}
}

func TestExecuteNOCRunActionYesFillsNewSessionTemplate(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := team.Write(proj, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto"}},
	}); err != nil {
		t.Fatal(err)
	}
	var ran string
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|new_session",
		ActionVars:  map[string]string{"session": "issue-97"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(command string) error {
			ran = command
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeNOC --run-action new_session: %v", err)
	}
	for _, want := range []string{"amq-squad new session", "--project " + proj, "--terminal-session amq-squad-p-issue-97", "issue-97"} {
		if !strings.Contains(ran, want) {
			t.Fatalf("new_session command missing %q: %s", want, ran)
		}
	}
	if strings.Contains(ran, "<session>") || strings.Contains(ran, "<tmux-session>") {
		t.Fatalf("new_session command still has placeholders: %s", ran)
	}
	if strings.Contains(ran, "--seed-from") || strings.Contains(ran, "<seed-from>") {
		t.Fatalf("new_session without seed-from should omit seed flag: %s", ran)
	}
}

func TestExecuteNOCRunActionFillsNewSessionSeedFromTemplate(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := team.Write(proj, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto"}},
	}); err != nil {
		t.Fatal(err)
	}
	var ran string
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|new_session",
		ActionVars:  map[string]string{"session": "issue-97", "seed-from": "issue:31"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(command string) error {
			ran = command
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeNOC --run-action new_session --set seed-from: %v", err)
	}
	for _, want := range []string{"amq-squad new session", "--project " + proj, "--seed-from 'issue:31'", "--terminal-session amq-squad-p-issue-97", "issue-97"} {
		if !strings.Contains(ran, want) {
			t.Fatalf("new_session seeded command missing %q: %s", want, ran)
		}
	}
	if strings.Contains(ran, "<seed-from>") {
		t.Fatalf("new_session seeded command still has seed placeholder: %s", ran)
	}
}

func TestExecuteNOCRunActionFillsMessageTemplate(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	sessionRoot := filepath.Join(base, "issue-96")
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    sessionRoot,
	})

	var ran string
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "agent|" + proj + "|issue-96|cto|action|message",
		ActionVars:  map[string]string{"body": "Please check logs"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(command string) error {
			ran = command
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeNOC --run-action message: %v", err)
	}
	for _, want := range []string{"amq send", "--root " + sessionRoot, "--me user", "--to cto", "--subject 'Message from operator'", "--body 'Please check logs'", "--kind status"} {
		if !strings.Contains(ran, want) {
			t.Fatalf("message command missing %q: %s", want, ran)
		}
	}
	if strings.Contains(ran, "<body>") {
		t.Fatalf("message command still has placeholder: %s", ran)
	}
}

func TestExecuteNOCRunActionFillsMessageWaitTemplate(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	sessionRoot := filepath.Join(base, "issue-96")
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    sessionRoot,
	})

	var ran string
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "agent|" + proj + "|issue-96|cto|action|message_wait",
		ActionVars:  map[string]string{"body": "Please check logs", "timeout": "60s"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(command string) error {
			ran = command
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeNOC --run-action message_wait: %v", err)
	}
	for _, want := range []string{"amq send", "--root " + sessionRoot, "--me user", "--to cto", "--subject 'Message from operator'", "--body 'Please check logs'", "--kind status", "--wait-for drained", "--wait-timeout 60s"} {
		if !strings.Contains(ran, want) {
			t.Fatalf("message_wait command missing %q: %s", want, ran)
		}
	}
	if strings.Contains(ran, "<body>") || strings.Contains(ran, "<timeout>") {
		t.Fatalf("message_wait command still has placeholder: %s", ran)
	}
}

func TestExecuteNOCRunActionFillsReceiptsWaitTemplate(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	sessionRoot := filepath.Join(base, "issue-96")
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    sessionRoot,
	})

	var ran string
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "agent|" + proj + "|issue-96|cto|action|receipts_wait",
		ActionVars:  map[string]string{"msg-id": "msg_123", "stage": "drained", "timeout": "60s"},
		Out:         &bytes.Buffer{},
		RunActionCommand: func(command string) error {
			ran = command
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeNOC --run-action receipts_wait: %v", err)
	}
	want := "amq receipts wait --root " + sessionRoot + " --me cto --msg-id msg_123 --stage drained --timeout 60s"
	if ran != want {
		t.Fatalf("receipts_wait command = %q, want %q", ran, want)
	}
}

func TestExecuteNOCRunActionFillsAMQCleanupTemplate(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	sessionRoot := filepath.Join(base, "issue-96")
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    sessionRoot,
	})

	var ran string
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "session|" + proj + "|issue-96|action|amq_cleanup",
		ActionVars:  map[string]string{"tmp-older-than": "36h"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(command string) error {
			ran = command
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeNOC --run-action amq_cleanup: %v", err)
	}
	want := "amq cleanup --root " + sessionRoot + " --tmp-older-than 36h --yes"
	if ran != want {
		t.Fatalf("amq_cleanup command = %q, want %q", ran, want)
	}
}

func TestExecuteNOCRunActionFillsBroadcastTemplate(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	sessionRoot := filepath.Join(base, "issue-96")
	seedAgentRecord(t, base, "issue-96", "qa", launch.Record{
		Binary:  "claude",
		Role:    "qa",
		Handle:  "qa",
		Session: "issue-96",
		Root:    sessionRoot,
	})
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    sessionRoot,
	})

	var ran string
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "session|" + proj + "|issue-96|action|broadcast",
		ActionVars:  map[string]string{"subject": "Heads up", "body": "Deploying now"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(command string) error {
			ran = command
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeNOC --run-action broadcast: %v", err)
	}
	for _, want := range []string{"amq send", "--root " + sessionRoot, "--me user", "--to 'cto,qa'", "--subject 'Heads up'", "--body 'Deploying now'", "--thread broadcast/operator", "--kind status"} {
		if !strings.Contains(ran, want) {
			t.Fatalf("broadcast command missing %q: %s", want, ran)
		}
	}
	if strings.Contains(ran, "<subject>") || strings.Contains(ran, "<body>") {
		t.Fatalf("broadcast command still has placeholders: %s", ran)
	}
}

func TestExecuteNOCRunActionFillsApproveCommand(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	seedNOCOperatorTeam(t, proj)
	base := filepath.Join(proj, noc.AgentMailDirName)
	sessionRoot := filepath.Join(base, "issue-96")
	agentDir := seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    sessionRoot,
	})
	seedNOCNeedsYouMessage(t, agentDir, "cto", "ask/approve", "APPROVAL: Ship it?")

	var ran string
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "agent|" + proj + "|issue-96|cto|action|approve",
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(command string) error {
			ran = command
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeNOC --run-action approve: %v", err)
	}
	for _, want := range []string{"amq send", "--root " + sessionRoot, "--me user", "--to cto", "--subject 'Re: APPROVAL: Ship it?'", "--body APPROVED", "--thread ask/approve", "--kind answer"} {
		if !strings.Contains(ran, want) {
			t.Fatalf("approve command missing %q: %s", want, ran)
		}
	}
}

func TestExecuteNOCRunActionFillsReadNeedsYouCommand(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	seedNOCOperatorTeam(t, proj)
	base := filepath.Join(proj, noc.AgentMailDirName)
	sessionRoot := filepath.Join(base, "issue-96")
	agentDir := seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    sessionRoot,
	})
	seedNOCNeedsYouMessage(t, agentDir, "cto", "ask/approve", "APPROVAL: Ship it?")

	var ran string
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "session|" + proj + "|issue-96|action|read_needs_you",
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(command string) error {
			ran = command
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeNOC --run-action read_needs_you: %v", err)
	}
	for _, want := range []string{"amq read", "--root " + sessionRoot, "--me user", "--id ask_approve", "--json"} {
		if !strings.Contains(ran, want) {
			t.Fatalf("read_needs_you command missing %q: %s", want, ran)
		}
	}
}

func TestExecuteNOCRunActionFillsReplyTemplate(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	seedNOCOperatorTeam(t, proj)
	base := filepath.Join(proj, noc.AgentMailDirName)
	sessionRoot := filepath.Join(base, "issue-96")
	agentDir := seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    sessionRoot,
	})
	seedNOCNeedsYouMessage(t, agentDir, "cto", "ask/approve", "APPROVAL: Ship it?")

	var ran string
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "session|" + proj + "|issue-96|action|reply",
		ActionVars:  map[string]string{"body": "I need one more check"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(command string) error {
			ran = command
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeNOC --run-action reply: %v", err)
	}
	for _, want := range []string{"amq send", "--root " + sessionRoot, "--me user", "--to cto", "--subject 'Re: APPROVAL: Ship it?'", "--body 'I need one more check'", "--thread ask/approve", "--kind answer"} {
		if !strings.Contains(ran, want) {
			t.Fatalf("reply command missing %q: %s", want, ran)
		}
	}
	if strings.Contains(ran, "<body>") {
		t.Fatalf("reply command still has placeholder: %s", ran)
	}
}

func TestExecuteNOCRunActionFillsDenyTemplate(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	seedNOCOperatorTeam(t, proj)
	base := filepath.Join(proj, noc.AgentMailDirName)
	sessionRoot := filepath.Join(base, "issue-96")
	agentDir := seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    sessionRoot,
	})
	seedNOCNeedsYouMessage(t, agentDir, "cto", "ask/approve", "APPROVAL: Ship it?")

	var ran string
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "session|" + proj + "|issue-96|action|deny",
		ActionVars:  map[string]string{"reason": "too risky"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(command string) error {
			ran = command
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeNOC --run-action deny: %v", err)
	}
	for _, want := range []string{"amq send", "--root " + sessionRoot, "--me user", "--to cto", "--subject 'Re: APPROVAL: Ship it?'", "--body 'DENIED: too risky'", "--thread ask/approve", "--kind answer"} {
		if !strings.Contains(ran, want) {
			t.Fatalf("deny command missing %q: %s", want, ran)
		}
	}
	if strings.Contains(ran, "<reason>") {
		t.Fatalf("deny command still has placeholder: %s", ran)
	}
}

func TestExecuteNOCRunActionFillsDLQRetryTemplate(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	sessionRoot := filepath.Join(base, "issue-96")
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    sessionRoot,
	})

	var ran string
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "agent|" + proj + "|issue-96|cto|action|dlq_retry",
		ActionVars:  map[string]string{"dlq-id": "dlq_123"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(command string) error {
			ran = command
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeNOC --run-action dlq_retry: %v", err)
	}
	want := "amq dlq retry --root " + sessionRoot + " --me cto --id dlq_123"
	if ran != want {
		t.Fatalf("DLQ retry command = %q, want %q", ran, want)
	}
}

func TestExecuteNOCRunActionFillsDLQPurgeTemplate(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	sessionRoot := filepath.Join(base, "issue-96")
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    sessionRoot,
	})

	var ran string
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "agent|" + proj + "|issue-96|cto|action|dlq_purge",
		ActionVars:  map[string]string{"older-than": "168h"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(command string) error {
			ran = command
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeNOC --run-action dlq_purge: %v", err)
	}
	want := "amq dlq purge --root " + sessionRoot + " --me cto --older-than 168h --yes"
	if ran != want {
		t.Fatalf("DLQ purge command = %q, want %q", ran, want)
	}
}

func TestExecuteNOCRunActionRejectsInvalidDLQID(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    filepath.Join(base, "issue-96"),
	})

	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "agent|" + proj + "|issue-96|cto|action|dlq_retry",
		ActionVars:  map[string]string{"dlq-id": "../bad"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(string) error {
			t.Fatal("invalid DLQ id action should not run")
			return nil
		},
	})
	if err == nil {
		t.Fatal("invalid DLQ id should fail preflight")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "invalid DLQ id") || !strings.Contains(err.Error(), "not a path") {
		t.Fatalf("invalid DLQ id error = %v", err)
	}
}

func TestExecuteNOCRunActionRejectsInvalidDLQPurgeDuration(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    filepath.Join(base, "issue-96"),
	})

	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "agent|" + proj + "|issue-96|cto|action|dlq_purge",
		ActionVars:  map[string]string{"older-than": "0s"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(string) error {
			t.Fatal("invalid DLQ purge duration action should not run")
			return nil
		},
	})
	if err == nil {
		t.Fatal("invalid DLQ purge duration should fail preflight")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "invalid DLQ purge age") || !strings.Contains(err.Error(), "positive duration") {
		t.Fatalf("invalid DLQ purge duration error = %v", err)
	}
}

func TestExecuteNOCRunActionRejectsInvalidMessageWaitTimeout(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    filepath.Join(base, "issue-96"),
	})

	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "agent|" + proj + "|issue-96|cto|action|message_wait",
		ActionVars:  map[string]string{"body": "Please check logs", "timeout": "-1s"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(string) error {
			t.Fatal("invalid message_wait timeout action should not run")
			return nil
		},
	})
	if err == nil {
		t.Fatal("invalid message wait timeout should fail preflight")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "invalid message wait timeout") || !strings.Contains(err.Error(), "non-negative duration") {
		t.Fatalf("invalid message wait timeout error = %v", err)
	}
}

func TestExecuteNOCRunActionRejectsInvalidReceiptsWaitTimeout(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    filepath.Join(base, "issue-96"),
	})

	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "agent|" + proj + "|issue-96|cto|action|receipts_wait",
		ActionVars:  map[string]string{"msg-id": "msg_123", "stage": "drained", "timeout": "-1s"},
		Out:         &bytes.Buffer{},
		RunActionCommand: func(string) error {
			t.Fatal("invalid receipts_wait timeout action should not run")
			return nil
		},
	})
	if err == nil {
		t.Fatal("invalid receipts wait timeout should fail preflight")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "invalid receipts wait timeout") || !strings.Contains(err.Error(), "non-negative duration") {
		t.Fatalf("invalid receipts wait timeout error = %v", err)
	}
}

func TestExecuteNOCRunActionRejectsInvalidAMQCleanupAge(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    filepath.Join(base, "issue-96"),
	})

	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "session|" + proj + "|issue-96|action|amq_cleanup",
		ActionVars:  map[string]string{"tmp-older-than": "0s"},
		Out:         &bytes.Buffer{},
		RunActionCommand: func(string) error {
			t.Fatal("invalid amq_cleanup age action should not run")
			return nil
		},
	})
	if err == nil {
		t.Fatal("invalid AMQ cleanup age should fail preflight")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "invalid AMQ cleanup tmp age") || !strings.Contains(err.Error(), "positive duration") {
		t.Fatalf("invalid AMQ cleanup age error = %v", err)
	}
}

func TestExecuteNOCRunActionRejectsInvalidReceiptsWaitStage(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    filepath.Join(base, "issue-96"),
	})

	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "agent|" + proj + "|issue-96|cto|action|receipts_wait",
		ActionVars:  map[string]string{"msg-id": "msg_123", "stage": "sent", "timeout": "60s"},
		Out:         &bytes.Buffer{},
		RunActionCommand: func(string) error {
			t.Fatal("invalid receipts_wait stage action should not run")
			return nil
		},
	})
	if err == nil {
		t.Fatal("invalid receipts wait stage should fail")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "stage=\"sent\"") || !strings.Contains(err.Error(), "dlq, drained") {
		t.Fatalf("invalid receipts wait stage error = %v", err)
	}
}

func TestExecuteNOCRunActionRejectsInvalidSeedFrom(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := team.Write(proj, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto"}},
	}); err != nil {
		t.Fatal(err)
	}
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|new_session",
		ActionVars:  map[string]string{"session": "issue-97", "seed-from": "issue:not-a-number"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(string) error {
			t.Fatal("invalid seed-from action should not run")
			return nil
		},
	})
	if err == nil {
		t.Fatal("invalid seed-from should fail preflight")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "invalid seed-from") || !strings.Contains(err.Error(), "issue:<n>") {
		t.Fatalf("invalid seed-from error = %v", err)
	}
}

func TestExecuteNOCRunActionRejectsInvalidSessionName(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := team.Write(proj, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto"}},
	}); err != nil {
		t.Fatal(err)
	}
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|new_session",
		ActionVars:  map[string]string{"session": "v0.5.0"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(string) error {
			t.Fatal("invalid session action should not run")
			return nil
		},
	})
	if err == nil {
		t.Fatal("invalid session should fail preflight")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	for _, want := range []string{"invalid session name", "replace dots"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("invalid session error missing %q: %v", want, err)
		}
	}
}

func TestExecuteNOCRunActionRejectsInvalidSessionProfile(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	cfg := team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto"}}}
	if err := team.Write(proj, cfg); err != nil {
		t.Fatal(err)
	}
	if err := team.WriteProfile(proj, "review", cfg); err != nil {
		t.Fatal(err)
	}
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|new_session",
		ActionVars:  map[string]string{"session": "issue-101", "profile": "Bad/Name"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(string) error {
			t.Fatal("invalid profile session action should not run")
			return nil
		},
	})
	if err == nil {
		t.Fatal("invalid session profile should fail preflight")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "invalid profile name") {
		t.Fatalf("invalid profile error = %v", err)
	}
}

func TestExecuteNOCRunActionRejectsUnknownSessionProfile(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	cfg := team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto"}}}
	if err := team.Write(proj, cfg); err != nil {
		t.Fatal(err)
	}
	if err := team.WriteProfile(proj, "review", cfg); err != nil {
		t.Fatal(err)
	}
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|new_session",
		ActionVars:  map[string]string{"session": "issue-101", "profile": "ghost"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(string) error {
			t.Fatal("unknown profile session action should not run")
			return nil
		},
	})
	if err == nil {
		t.Fatal("unknown session profile should fail preflight")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), `profile "ghost" is not configured`) {
		t.Fatalf("unknown profile error = %v", err)
	}
}

func TestExecuteNOCRunActionRejectsDuplicateSession(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := team.Write(proj, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(proj, noc.AgentMailDirName, "issue-97", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|new_session",
		ActionVars:  map[string]string{"session": "issue-97"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(string) error {
			t.Fatal("duplicate session action should not run")
			return nil
		},
	})
	if err == nil {
		t.Fatal("duplicate session should fail preflight")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "session \"issue-97\" already exists") {
		t.Fatalf("duplicate session error = %v", err)
	}
}

func TestExecuteNOCRunActionRejectsMemberRootDuplicateSession(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	memberDir := t.TempDir()
	if err := team.Write(proj, team.Team{
		Members: []team.Member{{
			Role:    "cto",
			Binary:  "codex",
			Handle:  "cto",
			CWD:     memberDir,
			Session: "issue-96",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "issue-102", "agents", "cto"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|new_session",
		ActionVars:  map[string]string{"session": "issue-102"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(string) error {
			t.Fatal("member-root duplicate session action should not run")
			return nil
		},
	})
	if err == nil {
		t.Fatal("member-root duplicate session should fail preflight")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	for _, want := range []string{"session \"issue-102\" already exists", "profile \"default\""} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("member-root duplicate error missing %q: %v", want, err)
		}
	}
}

func TestExecuteNOCRunActionRejectsDuplicateProfile(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	cfg := team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto"}}}
	if err := team.Write(proj, cfg); err != nil {
		t.Fatal(err)
	}
	if err := team.WriteProfile(proj, "review", cfg); err != nil {
		t.Fatal(err)
	}
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|new_profile",
		ActionVars:  map[string]string{"profile": "review", "roles": "cto,qa"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(string) error {
			t.Fatal("duplicate profile action should not run")
			return nil
		},
	})
	if err == nil {
		t.Fatal("duplicate profile should fail preflight")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "profile \"review\" already exists") {
		t.Fatalf("duplicate profile error = %v", err)
	}
}

func TestExecuteNOCRunActionRejectsInvalidProfileName(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := team.Write(proj, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto"}},
	}); err != nil {
		t.Fatal(err)
	}
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|new_profile",
		ActionVars:  map[string]string{"profile": "Bad/Name", "roles": "cto,qa"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(string) error {
			t.Fatal("invalid profile action should not run")
			return nil
		},
	})
	if err == nil {
		t.Fatal("invalid profile should fail preflight")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "invalid profile name") {
		t.Fatalf("invalid profile error = %v", err)
	}
}

func TestExecuteNOCRunActionFillsNewProfileTemplate(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := team.Write(proj, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto"}},
	}); err != nil {
		t.Fatal(err)
	}
	var ran string
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|new_profile",
		ActionVars:  map[string]string{"profile": "review", "roles": "cto,qa"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(command string) error {
			ran = command
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeNOC --run-action new_profile: %v", err)
	}
	for _, want := range []string{"amq-squad new profile review", "--project " + proj, "--roles 'cto,qa'"} {
		if !strings.Contains(ran, want) {
			t.Fatalf("new_profile command missing %q: %s", want, ran)
		}
	}
	if strings.Contains(ran, "--sync") {
		t.Fatalf("new_profile should not run pointer sync implicitly: %s", ran)
	}
	if strings.Contains(ran, "--binary") {
		t.Fatalf("new_profile without binary overrides should not include --binary: %s", ran)
	}
	if strings.Contains(ran, "--session") || strings.Contains(ran, "<session>") {
		t.Fatalf("new_profile without session should omit session flag: %s", ran)
	}
}

func TestExecuteNOCRunActionFillsNewProfileSession(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := team.Write(proj, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto"}},
	}); err != nil {
		t.Fatal(err)
	}
	var ran string
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|new_profile",
		ActionVars:  map[string]string{"profile": "review", "roles": "cto,qa", "session": "review-99"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(command string) error {
			ran = command
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeNOC --run-action new_profile --set session: %v", err)
	}
	for _, want := range []string{"amq-squad new profile review", "--roles 'cto,qa'", "--session review-99"} {
		if !strings.Contains(ran, want) {
			t.Fatalf("new_profile command missing %q: %s", want, ran)
		}
	}
	if strings.Contains(ran, "--sync") {
		t.Fatalf("new_profile should not run pointer sync implicitly: %s", ran)
	}
}

func TestExecuteNOCRunActionFillsSyncPointersDefaultProfile(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := team.Write(proj, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := rules.Write(proj, "# Team Rules\n"); err != nil {
		t.Fatal(err)
	}
	var ran string
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|sync_pointers",
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(command string) error {
			ran = command
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeNOC --run-action sync_pointers: %v", err)
	}
	for _, want := range []string{"amq-squad team sync", "--project " + proj, "--apply"} {
		if !strings.Contains(ran, want) {
			t.Fatalf("sync_pointers command missing %q: %s", want, ran)
		}
	}
	if strings.Contains(ran, "--profile") || strings.Contains(ran, "allow-outside") || strings.Contains(ran, "<allow-outside>") {
		t.Fatalf("sync_pointers default command should omit optional flags: %s", ran)
	}
}

func TestExecuteNOCRunActionFillsSyncPointersProfileAllowOutside(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	memberDir := t.TempDir()
	if err := team.Write(proj, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := team.WriteProfile(proj, "review", team.Team{
		Members: []team.Member{{Role: "qa", Binary: "claude", Handle: "qa", CWD: memberDir}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := rules.Write(proj, "# Team Rules\n"); err != nil {
		t.Fatal(err)
	}
	var ran string
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|sync_pointers",
		ActionVars:  map[string]string{"profile": "review", "allow-outside": "true"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(command string) error {
			ran = command
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeNOC --run-action sync_pointers profile allow-outside: %v", err)
	}
	for _, want := range []string{"amq-squad team sync", "--project " + proj, "--profile review", "--allow-outside", "--apply"} {
		if !strings.Contains(ran, want) {
			t.Fatalf("sync_pointers command missing %q: %s", want, ran)
		}
	}
}

func TestExecuteNOCRunActionFillsDeleteTeamProfile(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := team.Write(proj, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := team.WriteProfile(proj, "review", team.Team{
		Members: []team.Member{{Role: "qa", Binary: "claude", Handle: "qa"}},
	}); err != nil {
		t.Fatal(err)
	}
	var ran string
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|delete_team",
		ActionVars:  map[string]string{"profile": "review"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(command string) error {
			ran = command
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeNOC --run-action delete_team: %v", err)
	}
	for _, want := range []string{"amq-squad team rm", "--project " + proj, "--profile review", "--yes"} {
		if !strings.Contains(ran, want) {
			t.Fatalf("delete_team command missing %q: %s", want, ran)
		}
	}
}

func TestExecuteNOCRunActionRejectsSyncPointersOutsideWithoutOptIn(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	memberDir := t.TempDir()
	if err := team.WriteProfile(proj, "review", team.Team{
		Members: []team.Member{{Role: "qa", Binary: "claude", Handle: "qa", CWD: memberDir}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := rules.Write(proj, "# Team Rules\n"); err != nil {
		t.Fatal(err)
	}
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|sync_pointers",
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(string) error {
			t.Fatal("unsafe pointer sync action should not run")
			return nil
		},
	})
	if err == nil {
		t.Fatal("sync_pointers outside cwd without allow-outside should fail preflight")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "allow-outside=true") {
		t.Fatalf("outside cwd error should mention allow-outside=true: %v", err)
	}
}

func TestExecuteNOCRunActionResolvesUniqueName(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := team.Write(proj, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto"}},
	}); err != nil {
		t.Fatal(err)
	}
	var ran string
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		Filter:      "project:p",
		RunActionID: "new_session",
		ActionVars:  map[string]string{"session": "issue-98"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(command string) error {
			ran = command
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeNOC --run-action new_session: %v", err)
	}
	if !strings.Contains(ran, "amq-squad new session") || !strings.Contains(ran, "issue-98") {
		t.Fatalf("unique action name should execute new_session command, got: %s", ran)
	}
}

func TestExecuteNOCRunActionRejectsAmbiguousName(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b"} {
		if err := os.MkdirAll(filepath.Join(root, name, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "new_team",
		ActionVars:  map[string]string{"roles": "cto,qa"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(string) error {
			t.Fatal("ambiguous action name should not run")
			return nil
		},
	})
	if err == nil {
		t.Fatal("ambiguous action name should fail")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	for _, want := range []string{"matches multiple actions", "project|" + filepath.Join(root, "a") + "|action|new_team", "project|" + filepath.Join(root, "b") + "|action|new_team"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ambiguous action error missing %q: %v", want, err)
		}
	}
}

func TestExecuteNOCRunActionYesFillsNewTeamTemplate(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	var ran string
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|new_team",
		ActionVars:  map[string]string{"roles": "cto,qa"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(command string) error {
			ran = command
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeNOC --run-action new_team: %v", err)
	}
	for _, want := range []string{"amq-squad new team", "--project " + proj, "--roles 'cto,qa'"} {
		if !strings.Contains(ran, want) {
			t.Fatalf("new_team command missing %q: %s", want, ran)
		}
	}
	if strings.Contains(ran, "--sync") {
		t.Fatalf("new_team should not run pointer sync implicitly: %s", ran)
	}
	if strings.Contains(ran, "--binary") {
		t.Fatalf("new_team without binary overrides should not include --binary: %s", ran)
	}
	if strings.Contains(ran, "--session") || strings.Contains(ran, "<session>") {
		t.Fatalf("new_team without session should omit session flag: %s", ran)
	}
}

func TestExecuteNOCRunActionFillsNewTeamBinaryOverride(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	var ran string
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|new_team",
		ActionVars:  map[string]string{"roles": "cto,qa", "binary": "qa=codex"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(command string) error {
			ran = command
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeNOC --run-action new_team --set binary: %v", err)
	}
	for _, want := range []string{"amq-squad new team", "--roles 'cto,qa'", "--binary qa=codex"} {
		if !strings.Contains(ran, want) {
			t.Fatalf("new_team command missing %q: %s", want, ran)
		}
	}
	if strings.Contains(ran, "--sync") {
		t.Fatalf("new_team should not run pointer sync implicitly: %s", ran)
	}
}

func TestExecuteNOCRunActionFillsNewTeamSession(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	var ran string
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|new_team",
		ActionVars:  map[string]string{"roles": "cto,qa", "session": "issue-99"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(command string) error {
			ran = command
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeNOC --run-action new_team --set session: %v", err)
	}
	for _, want := range []string{"amq-squad new team", "--roles 'cto,qa'", "--session issue-99"} {
		if !strings.Contains(ran, want) {
			t.Fatalf("new_team command missing %q: %s", want, ran)
		}
	}
	if strings.Contains(ran, "--sync") {
		t.Fatalf("new_team should not run pointer sync implicitly: %s", ran)
	}
}

func TestExecuteNOCRunActionFillsInlineTeamSpecBinaryOverride(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	var ran string
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|new_team",
		ActionVars:  map[string]string{"roles": "cto=codex,qa"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(command string) error {
			ran = command
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeNOC --run-action new_team inline binary: %v", err)
	}
	for _, want := range []string{"amq-squad new team", "--roles 'cto,qa'", "--binary cto=codex"} {
		if !strings.Contains(ran, want) {
			t.Fatalf("new_team command missing %q: %s", want, ran)
		}
	}
	if strings.Contains(ran, "--sync") {
		t.Fatalf("new_team should not run pointer sync implicitly: %s", ran)
	}
}

func TestRunNOCActionDryRunJSONWithBinaryOverride(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{
			"--root", root,
			"--filter", "project:p",
			"--run-action", "new_team",
			"--set", "roles=cto,qa",
			"--set", "binary=qa=codex",
			"--dry-run",
			"--json",
		})
	})
	if err != nil {
		t.Fatalf("runNOC --run-action new_team --set binary --dry-run --json: %v", err)
	}
	env := decodeJSONEnvelope[nocActionPlanEnvelopeData](t, stdout)
	if env.Kind != "noc_action_plan" || env.Data.Action.Name != "new_team" {
		t.Fatalf("action plan = kind:%q action:%q", env.Kind, env.Data.Action.Name)
	}
	if env.Data.TemplateValues["binary"] != "qa=codex" {
		t.Fatalf("template binary value = %+v, want qa=codex", env.Data.TemplateValues)
	}
	if _, ok := env.Data.TemplateValues["binary-flag"]; ok {
		t.Fatalf("template values should not expose internal binary-flag: %+v", env.Data.TemplateValues)
	}
	if !strings.Contains(env.Data.Command, "--binary qa=codex") {
		t.Fatalf("action command missing binary override: %s", env.Data.Command)
	}
}

func TestRunNOCActionDryRunJSONNormalizesInlineTeamSpec(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{
			"--root", root,
			"--filter", "project:p",
			"--run-action", "new_team",
			"--set", "roles=cto=codex,qa",
			"--dry-run",
			"--json",
		})
	})
	if err != nil {
		t.Fatalf("runNOC inline team spec --dry-run --json: %v", err)
	}
	env := decodeJSONEnvelope[nocActionPlanEnvelopeData](t, stdout)
	if env.Data.TemplateValues["roles"] != "cto,qa" || env.Data.TemplateValues["binary"] != "cto=codex" {
		t.Fatalf("template values = %+v, want normalized roles and binary", env.Data.TemplateValues)
	}
	if !strings.Contains(env.Data.Command, "--roles 'cto,qa'") || !strings.Contains(env.Data.Command, "--binary cto=codex") {
		t.Fatalf("action command should carry normalized roles and binary: %s", env.Data.Command)
	}
}

func TestExecuteNOCRunActionRejectsInvalidRoles(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|new_team",
		ActionVars:  map[string]string{"roles": "ghost"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(string) error {
			t.Fatal("invalid roles action should not run")
			return nil
		},
	})
	if err == nil {
		t.Fatal("invalid roles should fail preflight")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "unknown persona/role") {
		t.Fatalf("invalid roles error = %v", err)
	}
}

func TestExecuteNOCRunActionRejectsInvalidBinaryOverride(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|new_team",
		ActionVars:  map[string]string{"roles": "cto,qa", "binary": "ghost=codex"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(string) error {
			t.Fatal("invalid binary action should not run")
			return nil
		},
	})
	if err == nil {
		t.Fatal("invalid binary override should fail preflight")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "binary override has unknown role") {
		t.Fatalf("invalid binary error = %v", err)
	}
}

func TestExecuteNOCRunActionRejectsConflictingBinaryOverrides(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|new_team",
		ActionVars:  map[string]string{"roles": "cto=codex", "binary": "cto=claude"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(string) error {
			t.Fatal("conflicting binary action should not run")
			return nil
		},
	})
	if err == nil {
		t.Fatal("conflicting binary overrides should fail")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "specified more than once") {
		t.Fatalf("conflicting binary error = %v", err)
	}
}

func TestExecuteNOCRunActionRejectsInvalidTeamSession(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|new_team",
		ActionVars:  map[string]string{"roles": "cto,qa", "session": "Bad/Session"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(string) error {
			t.Fatal("invalid team session action should not run")
			return nil
		},
	})
	if err == nil {
		t.Fatal("invalid team session should fail preflight")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "invalid session name") {
		t.Fatalf("invalid team session error = %v", err)
	}
}

func TestExecuteNOCRunActionRejectsUnknownTemplateValue(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := team.Write(proj, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto"}},
	}); err != nil {
		t.Fatal(err)
	}
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|new_session",
		ActionVars:  map[string]string{"session": "issue-98", "sesion": "typo"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(string) error {
			t.Fatal("action with unknown template value should not run")
			return nil
		},
	})
	if err == nil {
		t.Fatal("unknown template value should fail")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "sesion") || !strings.Contains(err.Error(), "Accepted values") {
		t.Fatalf("unknown template value error = %v", err)
	}
}

func TestExecuteNOCRunActionRejectsSetOnNonTemplateAction(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := mkAgentMail(proj); err != nil {
		t.Fatal(err)
	}
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|status",
		ActionVars:  map[string]string{"session": "issue-98"},
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(string) error {
			t.Fatal("non-template action with --set should not run")
			return nil
		},
	})
	if err == nil {
		t.Fatal("--set on non-template action should fail")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "does not accept template values") {
		t.Fatalf("non-template --set error = %v", err)
	}
}

func TestNOCTemplateVarsAllowCommaValues(t *testing.T) {
	var vars nocTemplateVars
	if err := vars.Set("roles=cto,qa"); err != nil {
		t.Fatalf("set roles with comma: %v", err)
	}
	if got := vars["roles"]; got != "cto,qa" {
		t.Fatalf("roles value = %q, want cto,qa", got)
	}
}

func TestRunNOCActionSetFlagFillsCommaValue(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	prevRunner := nocActionRunnerOverride
	var ran string
	nocActionRunnerOverride = func(command string) error {
		ran = command
		return nil
	}
	t.Cleanup(func() { nocActionRunnerOverride = prevRunner })

	_, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--root", root, "--run-action", "project|" + proj + "|action|new_team", "--set", "roles=cto,qa", "--yes"})
	})
	if err != nil {
		t.Fatalf("runNOC --run-action --set roles=cto,qa: %v", err)
	}
	if !strings.Contains(ran, "--roles 'cto,qa'") {
		t.Fatalf("run-action --set should preserve comma value, got: %s", ran)
	}
}

func TestExecuteNOCRunActionRejectsMissingTemplateValues(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := team.Write(proj, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto"}},
	}); err != nil {
		t.Fatal(err)
	}
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|new_session",
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(string) error {
			t.Fatal("template action with missing values should not run")
			return nil
		},
	})
	if err == nil {
		t.Fatal("missing template values should fail")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "--set session=<value>") {
		t.Fatalf("missing template error should name --set session, got %v", err)
	}
	if strings.Contains(err.Error(), "tmux-session") {
		t.Fatalf("tmux-session is derived from session and should not be requested: %v", err)
	}
}

func TestExecuteNOCRunActionMissingRolesNamesExamples(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := executeNOC(nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		Depth:       noc.DefaultDepth,
		RunActionID: "project|" + proj + "|action|new_team",
		Yes:         true,
		Out:         &bytes.Buffer{},
		RunActionCommand: func(string) error {
			t.Fatal("new_team with missing roles should not run")
			return nil
		},
	})
	if err == nil {
		t.Fatal("missing roles should fail")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	for _, want := range []string{"--set roles=<value>", "examples: cto,qa; 2,9; all; cto=codex,qa"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("missing roles error missing %q: %v", want, err)
		}
	}
}

func TestRunNOCActionsCommandsOnly(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    filepath.Join(base, "issue-96"),
	})

	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--actions", "--root", root, "--action", "resume", "--commands"})
	})
	if err != nil {
		t.Fatalf("runNOC --actions --commands: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 1 {
		t.Fatalf("commands-only output lines = %d, want 1:\n%s", len(lines), stdout)
	}
	if strings.Contains(stdout, "ID") || strings.Contains(stdout, "SCOPE") {
		t.Fatalf("commands-only output should not include table headers:\n%s", stdout)
	}
	if !strings.Contains(lines[0], "amq-squad resume --project "+proj) ||
		!strings.Contains(lines[0], "--session issue-96") {
		t.Fatalf("commands-only output should include the selected resume command:\n%s", stdout)
	}
}

func TestRunNOCActionsCommandsOnlyConfiguredProjectActions(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := team.Write(proj, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto"}},
	}); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{
			"--actions",
			"--root", root,
			"--filter", "project:p",
			"--action", "doctor,roles,team_profiles,new_session,new_profile,sync_pointers",
			"--commands",
		})
	})
	if err != nil {
		t.Fatalf("runNOC --actions project commands: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 6 {
		t.Fatalf("project action command lines = %d, want 6:\n%s", len(lines), stdout)
	}
	for _, want := range []string{
		"amq-squad doctor --project " + proj + " --all-profiles",
		"amq-squad roles",
		"amq-squad team profiles --project " + proj,
		"amq-squad new session --project " + proj + " --seed-from '<seed-from>' --target new-session --terminal-session '<tmux-session>' '<session>'",
		"amq-squad new profile '<profile>' --project " + proj + " --roles '<roles>' --binary '<binary>' --session '<session>'",
		"amq-squad team sync --project " + proj + " '<allow-outside>' --apply",
	} {
		if !stringInSlice(lines, want) {
			t.Fatalf("project action commands missing %q in:\n%s", want, stdout)
		}
	}
}

func TestRunNOCActionsCommandsOnlyCandidateProjectActions(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "candidate")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{
			"--actions",
			"--root", root,
			"--filter", "project:candidate",
			"--action", "doctor,roles,new_team",
			"--commands",
		})
	})
	if err != nil {
		t.Fatalf("runNOC --actions candidate project commands: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 3 {
		t.Fatalf("candidate project command lines = %d, want 3:\n%s", len(lines), stdout)
	}
	for _, want := range []string{
		"amq-squad doctor --project " + proj + " --all-profiles",
		"amq-squad roles",
		"amq-squad new team --project " + proj + " --roles '<roles>' --binary '<binary>' --session '<session>'",
	} {
		if !stringInSlice(lines, want) {
			t.Fatalf("candidate project action commands missing %q in:\n%s", want, stdout)
		}
	}
}

func TestRunNOCActionsCommandsOnlySessionCleanupActions(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    filepath.Join(base, "issue-96"),
	})

	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--actions", "--root", root, "--filter", "session:issue-96", "--action", "archive,remove", "--commands"})
	})
	if err != nil {
		t.Fatalf("runNOC --actions archive,remove --commands: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("commands-only cleanup lines = %d, want 2:\n%s", len(lines), stdout)
	}
	for _, want := range []string{
		"amq-squad archive --project " + proj + " --yes issue-96",
		"amq-squad rm --project " + proj + " --yes issue-96",
	} {
		if !stringInSlice(lines, want) {
			t.Fatalf("cleanup commands missing %q in:\n%s", want, stdout)
		}
	}
}

func TestRunNOCActionsCommandsOnlyInboxAction(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	sessionRoot := filepath.Join(base, "issue-96")
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    sessionRoot,
	})

	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--actions", "--root", root, "--action", "inbox", "--commands"})
	})
	if err != nil {
		t.Fatalf("runNOC --actions inbox --commands: %v", err)
	}
	want := "amq list --root " + sessionRoot + " --me cto --new"
	if got := strings.TrimSpace(stdout); got != want {
		t.Fatalf("inbox command = %q, want %q", got, want)
	}
}

func TestRunNOCActionsCommandsOnlyPresenceAction(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	sessionRoot := filepath.Join(base, "issue-96")
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    sessionRoot,
	})

	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--actions", "--root", root, "--action", "presence", "--commands"})
	})
	if err != nil {
		t.Fatalf("runNOC --actions presence --commands: %v", err)
	}
	want := "amq presence list --root " + sessionRoot
	if got := strings.TrimSpace(stdout); got != want {
		t.Fatalf("presence command = %q, want %q", got, want)
	}
}

func TestRunNOCActionsCommandsOnlyAMQWhoAction(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    filepath.Join(base, "issue-96"),
	})

	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--actions", "--root", root, "--action", "amq_who", "--commands"})
	})
	if err != nil {
		t.Fatalf("runNOC --actions amq_who --commands: %v", err)
	}
	want := "amq who --root " + base
	if got := strings.TrimSpace(stdout); got != want {
		t.Fatalf("amq_who command = %q, want %q", got, want)
	}
}

func TestRunNOCActionsCommandsOnlyAMQEnvAction(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    filepath.Join(base, "issue-96"),
	})

	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--actions", "--root", root, "--action", "amq_env", "--commands"})
	})
	if err != nil {
		t.Fatalf("runNOC --actions amq_env --commands: %v", err)
	}
	want := "amq env --root " + base + " --json"
	if got := strings.TrimSpace(stdout); got != want {
		t.Fatalf("amq_env command = %q, want %q", got, want)
	}
}

func TestRunNOCActionsCommandsOnlyTeamRulesAction(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := team.Write(proj, team.Team{
		Project: proj,
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--actions", "--root", root, "--action", "team_rules", "--commands"})
	})
	if err != nil {
		t.Fatalf("runNOC --actions team_rules --commands: %v", err)
	}
	want := "amq-squad team rules show --project " + proj
	if got := strings.TrimSpace(stdout); got != want {
		t.Fatalf("team_rules command = %q, want %q", got, want)
	}
}

func TestRunNOCActionsCommandsOnlyDLQAction(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	sessionRoot := filepath.Join(base, "issue-96")
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    sessionRoot,
	})

	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--actions", "--root", root, "--action", "dlq", "--commands"})
	})
	if err != nil {
		t.Fatalf("runNOC --actions dlq --commands: %v", err)
	}
	want := "amq dlq list --root " + sessionRoot + " --me cto"
	if got := strings.TrimSpace(stdout); got != want {
		t.Fatalf("DLQ command = %q, want %q", got, want)
	}
}

func TestRunNOCActionsCommandsOnlyReceiptsAction(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	sessionRoot := filepath.Join(base, "issue-96")
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    sessionRoot,
	})

	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--actions", "--root", root, "--action", "receipts", "--commands"})
	})
	if err != nil {
		t.Fatalf("runNOC --actions receipts --commands: %v", err)
	}
	want := "amq receipts list --root " + sessionRoot + " --me cto"
	if got := strings.TrimSpace(stdout); got != want {
		t.Fatalf("receipts command = %q, want %q", got, want)
	}
}

func TestRunNOCActionsCommandsOnlyReceiptsWaitAction(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	sessionRoot := filepath.Join(base, "issue-96")
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    sessionRoot,
	})

	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--actions", "--root", root, "--action", "receipts_wait", "--commands"})
	})
	if err != nil {
		t.Fatalf("runNOC --actions receipts_wait --commands: %v", err)
	}
	want := "amq receipts wait --root " + sessionRoot + " --me cto --msg-id '<msg-id>' --stage '<stage>' --timeout '<timeout>'"
	if got := strings.TrimSpace(stdout); got != want {
		t.Fatalf("receipts_wait command = %q, want %q", got, want)
	}
}

func TestRunNOCActionsCommandsOnlyDLQRetryAllAction(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	sessionRoot := filepath.Join(base, "issue-96")
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    sessionRoot,
	})

	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--actions", "--root", root, "--action", "dlq_retry_all", "--commands"})
	})
	if err != nil {
		t.Fatalf("runNOC --actions dlq_retry_all --commands: %v", err)
	}
	want := "amq dlq retry --root " + sessionRoot + " --me cto --all"
	if got := strings.TrimSpace(stdout); got != want {
		t.Fatalf("DLQ retry-all command = %q, want %q", got, want)
	}
}

func TestRunNOCActionsCommandsOnlyTemplateDoesNotExposeInternalBinaryFlag(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--actions", "--root", root, "--action", "new_team", "--commands"})
	})
	if err != nil {
		t.Fatalf("runNOC --actions new_team --commands: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 1 {
		t.Fatalf("commands-only output lines = %d, want 1:\n%s", len(lines), stdout)
	}
	if strings.Contains(lines[0], "binary-flag") {
		t.Fatalf("commands-only output leaked internal binary-flag placeholder:\n%s", stdout)
	}
	if !strings.Contains(lines[0], "--binary '<binary>'") {
		t.Fatalf("commands-only output should show optional binary placeholder:\n%s", stdout)
	}
}

func TestRunNOCActionsCommandsOnlyRolesAction(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--actions", "--root", root, "--filter", "project:p", "--action", "roles", "--commands"})
	})
	if err != nil {
		t.Fatalf("runNOC --actions roles --commands: %v", err)
	}
	if got := strings.TrimSpace(stdout); got != "amq-squad roles" {
		t.Fatalf("roles command output = %q, want amq-squad roles", got)
	}
}

func TestRunNOCActionsCommandsRejectJSON(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--actions", "--commands", "--json"})
	})
	if err == nil {
		t.Fatal("--commands with --json should fail")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "--commands cannot be used with --json") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunNOCActionsSelectorsRequireActions(t *testing.T) {
	for _, args := range [][]string{
		{"--json", "--commands"},
		{"--json", "--action-id", "session|/repo|issue-96|action|resume"},
		{"--json", "--target-id", "session|/repo|issue-96"},
		{"--json", "--scope", "session"},
	} {
		_, _, err := captureOutput(t, func() error {
			return runNOC(args)
		})
		if err == nil {
			t.Fatalf("%v without --actions should fail", args)
		}
		if _, ok := err.(UsageError); !ok {
			t.Fatalf("%v: want UsageError, got %T: %v", args, err, err)
		}
		if !strings.Contains(err.Error(), "--action, --action-id, --target-id, --scope, --mutating, and --commands require --actions") {
			t.Fatalf("%v: unexpected error: %v", args, err)
		}
	}
}

func TestRunNOCActionFlagValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "json", args: []string{"--run-action", "project|/repo|action|status", "--json"}, want: "--run-action --json requires --dry-run"},
		{name: "actions", args: []string{"--run-action", "project|/repo|action|status", "--actions"}, want: "--run-action cannot be combined with --actions"},
		{name: "selector", args: []string{"--run-action", "project|/repo|action|status", "--scope", "project"}, want: "--run-action cannot be combined"},
		{name: "dry-run", args: []string{"--dry-run", "--once"}, want: "--dry-run requires --run-action"},
		{name: "set", args: []string{"--set", "session=issue-97", "--once"}, want: "--set requires --run-action"},
		{name: "yes", args: []string{"--yes", "--once"}, want: "--yes/-y requires --run-action"},
	} {
		_, _, err := captureOutput(t, func() error {
			return runNOC(tc.args)
		})
		if err == nil {
			t.Fatalf("%s: expected usage error", tc.name)
		}
		if _, ok := err.(UsageError); !ok {
			t.Fatalf("%s: want UsageError, got %T: %v", tc.name, err, err)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: error missing %q: %v", tc.name, tc.want, err)
		}
	}
}

func TestRunNOCJSONFilterScopesSessions(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    filepath.Join(base, "issue-96"),
	})
	seedAgentRecord(t, base, "drive-fix", "qa", launch.Record{
		Binary:  "claude",
		Role:    "qa",
		Handle:  "qa",
		Session: "drive-fix",
		Root:    filepath.Join(base, "drive-fix"),
	})

	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--json", "--root", root, "--filter", "session:issue-96"})
	})
	if err != nil {
		t.Fatalf("runNOC --json --filter: %v", err)
	}
	env := decodeJSONEnvelope[nocSnapshotEnvelopeData](t, stdout)
	if env.Data.Filter != "session:issue-96" {
		t.Errorf("filter = %q, want session:issue-96", env.Data.Filter)
	}
	if env.Data.ProjectCount != 1 || len(env.Data.Projects) != 1 {
		t.Fatalf("project count/list = %d/%d, want 1/1", env.Data.ProjectCount, len(env.Data.Projects))
	}
	if env.Data.Projects[0].SessionCount != 1 {
		t.Fatalf("filtered session_count = %d, want 1", env.Data.Projects[0].SessionCount)
	}
	if env.Data.Projects[0].AgentsTotal != 1 {
		t.Fatalf("filtered agents_total = %d, want 1", env.Data.Projects[0].AgentsTotal)
	}
	sessions := env.Data.Projects[0].Sessions
	if len(sessions) != 1 || sessions[0].Name != "issue-96" {
		t.Fatalf("filtered sessions = %+v, want only issue-96", sessions)
	}
}

func TestNOCJSONFilterRecomputesRollups(t *testing.T) {
	ms := noc.MultiSnapshot{
		Roots: []string{"/root"},
		Projects: []noc.ProjectSnapshot{{
			Project: "p",
			Dir:     "/root/p",
			Snap: state.Snapshot{
				Sessions: []state.Session{
					{
						Name:   "issue-96",
						Agents: []state.Agent{{Handle: "cto", Role: "cto", Engine: "codex", Liveness: state.LivenessAlive}},
						Rollup: state.TriageRollup{NeedsYou: 1},
					},
					{
						Name:   "drive-fix",
						Agents: []state.Agent{{Handle: "qa", Role: "qa", Engine: "claude", Liveness: state.LivenessDead}},
						Rollup: state.TriageRollup{Blocked: 2},
					},
				},
				Rollup: state.TriageRollup{NeedsYou: 1, Blocked: 2},
			},
		}},
		Rollup:       state.TriageRollup{NeedsYou: 1, Blocked: 2},
		LiveProjects: 1,
	}

	scoped := filterNOCSnapshot(ms, "session:issue-96", false)
	env := nocSnapshotEnvelope(scoped, "session:issue-96", false)
	if env.Rollup.NeedsYou != 1 || env.Rollup.Blocked != 0 {
		t.Fatalf("global rollup = %+v, want needs-you only", env.Rollup)
	}
	if env.LiveProjects != 1 {
		t.Fatalf("live_projects = %d, want 1", env.LiveProjects)
	}
	if env.ProjectCount != 1 || len(env.Projects) != 1 {
		t.Fatalf("project count/list = %d/%d, want 1/1", env.ProjectCount, len(env.Projects))
	}
	project := env.Projects[0]
	if project.Rollup.NeedsYou != 1 || project.Rollup.Blocked != 0 {
		t.Fatalf("project rollup = %+v, want needs-you only", project.Rollup)
	}
	if project.SessionCount != 1 || project.AgentsTotal != 1 || project.AgentsAlive != 1 {
		t.Fatalf("project counts = sessions:%d agents:%d alive:%d, want 1/1/1",
			project.SessionCount, project.AgentsTotal, project.AgentsAlive)
	}
}

func TestNOCJSONReasonCodesDistinguishRollupStates(t *testing.T) {
	cases := []struct {
		name       string
		rollup     state.TriageRollup
		live       int
		total      int
		wantState  string
		wantReason string
	}{
		{name: "needs-user", rollup: state.TriageRollup{NeedsYou: 1}, total: 1, wantState: "needs-you", wantReason: "needs_user"},
		// S4c source-backed contract: with NO operational agent (live==0), unowned or
		// outstanding blocked/gated/at-risk evidence is NOT a live wait - it collapses
		// to the stale primary state. The granular counts survive in the rollup detail.
		{name: "unowned-blocked-is-stale", rollup: state.TriageRollup{Blocked: 1}, total: 1, wantState: "stale-blocked", wantReason: "stale_blocked"},
		{name: "unowned-gated-is-stale", rollup: state.TriageRollup{Gated: 1}, total: 1, wantState: "stale-blocked", wantReason: "stale_blocked"},
		{name: "unowned-at-risk-is-stale", rollup: state.TriageRollup{AtRisk: 1}, total: 1, wantState: "stale-blocked", wantReason: "stale_blocked"},
		{name: "online", live: 1, total: 1, wantState: "online", wantReason: "online"},
		{name: "online-with-unowned-rollup-history", rollup: state.TriageRollup{Blocked: 3, Gated: 1, AtRisk: 1}, live: 1, total: 1, wantState: "online", wantReason: "online"},
		{name: "stale-blocked", rollup: state.TriageRollup{BlockedStale: 1}, total: 1, wantState: "stale-blocked", wantReason: "stale_blocked"},
		{name: "stopped", total: 1, wantState: "stopped", wantReason: "stopped"},
		{name: "empty", wantState: "empty", wantReason: "empty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sess := state.Session{Rollup: c.rollup}
			if got := nocSessionJSONState(sess, c.live, c.total); got != c.wantState {
				t.Fatalf("state = %q, want %q", got, c.wantState)
			}
			if got := nocSessionJSONReasonCode(sess, c.live, c.total); got != c.wantReason {
				t.Fatalf("reason_code = %q, want %q", got, c.wantReason)
			}
		})
	}

	env := nocRollupEnvelope(state.TriageRollup{Gated: 2, GatedStale: 3})
	if env.Gated != 2 || env.GatedStale != 3 {
		t.Fatalf("rollup gated fields = %d/%d, want 2/3", env.Gated, env.GatedStale)
	}
}

// S4c regression: the JSON/API path must obey the same source-backed status
// contract as the TUI. A session whose only agents are dead-mailbox-live (process
// gone, only AMQ presence fresh) plus an aged peer review (unowned at-risk) must
// report agents_alive=0 and a STALE primary state, never online or a live at-risk.
// The raw evidence is retained in the rollup + unowned_evidence detail.
func TestNOCSessionJSONAllDmblUnownedAtRiskIsStale(t *testing.T) {
	row := nocSessionEnvelope(noc.ProjectSnapshot{Dir: "/root/api"}, state.Session{
		Name: "issue-1",
		Agents: []state.Agent{
			{Handle: "cto", Liveness: state.LivenessDeadMailboxLive},
			{Handle: "fullstack", Liveness: state.LivenessDeadMailboxLive},
		},
		Attention:        state.Attention{State: state.TriageClear},
		UnownedAttention: state.Attention{State: state.TriageAtRisk},
		Rollup:           state.TriageRollup{AtRisk: 1},
	})
	if row.AgentsAlive != 0 {
		t.Fatalf("agents_alive = %d, want 0 (dead-mailbox-live is not live)", row.AgentsAlive)
	}
	if row.State != "stale-blocked" || row.ReasonCode != "stale_blocked" {
		t.Fatalf("state/reason = %q/%q, want stale-blocked/stale_blocked (unowned at-risk is not a live wait)", row.State, row.ReasonCode)
	}
	if row.UnownedEvidence != "at-risk" {
		t.Fatalf("unowned_evidence = %q, want at-risk (evidence retained as detail)", row.UnownedEvidence)
	}
}

// S4c companion: one operational (alive) agent that OWNS the aged peer review keeps
// the session in a live waiting/online-with-attention state, not stale. agents_alive
// counts the live agent, and the owned attention drives the primary state.
func TestNOCSessionJSONOneLiveAgentOwnsAtRiskIsWaiting(t *testing.T) {
	row := nocSessionEnvelope(noc.ProjectSnapshot{Dir: "/root/api"}, state.Session{
		Name: "issue-2",
		Agents: []state.Agent{
			{Handle: "cto", Liveness: state.LivenessAlive, Attention: state.Attention{State: state.TriageAtRisk}},
		},
		Attention: state.Attention{State: state.TriageAtRisk},
		Rollup:    state.TriageRollup{AtRisk: 1},
	})
	if row.AgentsAlive != 1 {
		t.Fatalf("agents_alive = %d, want 1", row.AgentsAlive)
	}
	if row.State != "at-risk" || row.ReasonCode != "at_risk" {
		t.Fatalf("state/reason = %q/%q, want at-risk/at_risk (owned live wait)", row.State, row.ReasonCode)
	}
}

func TestNOCSessionJSONNewerClearActivityBeatsOldAtRisk(t *testing.T) {
	old := time.Now().Add(-24 * time.Hour)
	fresh := time.Now()
	row := nocSessionEnvelope(noc.ProjectSnapshot{Dir: "/root/api"}, state.Session{
		Name: "issue-3",
		Agents: []state.Agent{
			{Handle: "cto", Liveness: state.LivenessAlive, Attention: state.Attention{State: state.TriageAtRisk}},
			{Handle: "fullstack", Liveness: state.LivenessAlive, Attention: state.Attention{State: state.TriageAtRisk}},
		},
		Attention: state.Attention{State: state.TriageAtRisk},
		Rollup:    state.TriageRollup{AtRisk: 1, Clear: 1},
		Coordination: state.Coordination{Threads: []state.ThreadSummary{
			{ID: "decision/status-model", Triage: state.TriageAtRisk, LastEventAt: old, Participants: []string{"cto", "fullstack"}},
			{ID: "p2p/cto__fullstack", Triage: state.TriageClear, LastEventAt: fresh, Participants: []string{"cto", "fullstack"}},
		}},
	})
	if row.AgentsAlive != 2 {
		t.Fatalf("agents_alive = %d, want 2", row.AgentsAlive)
	}
	if row.State != "online" || row.ReasonCode != "online" {
		t.Fatalf("state/reason = %q/%q, want online/online (newer clear activity supersedes old at-risk)", row.State, row.ReasonCode)
	}
	if row.Attention != "clear" || row.AttentionReason != "" {
		t.Fatalf("attention = %q/%q, want clear/empty", row.Attention, row.AttentionReason)
	}
	for _, ag := range row.Agents {
		if ag.Attention != "clear" || ag.AttentionReason != "" {
			t.Fatalf("%s attention = %q/%q, want clear/empty", ag.Handle, ag.Attention, ag.AttentionReason)
		}
	}
}

// S4c project-level: a project whose only session is all dead-mailbox-live with
// unowned at-risk is STALE, not online, and contributes zero live agents.
func TestNOCProjectJSONAllDmblIsStaleNotRunning(t *testing.T) {
	project := nocProjectEnvelope(noc.ProjectSnapshot{
		Project: "api",
		Dir:     "/root/api",
		Snap: state.Snapshot{
			Rollup: state.TriageRollup{AtRisk: 1},
			Sessions: []state.Session{{
				Name: "issue-1",
				Agents: []state.Agent{
					{Handle: "cto", Liveness: state.LivenessDeadMailboxLive},
					{Handle: "fullstack", Liveness: state.LivenessDeadMailboxLive},
				},
				Attention:        state.Attention{State: state.TriageClear},
				UnownedAttention: state.Attention{State: state.TriageAtRisk},
				Rollup:           state.TriageRollup{AtRisk: 1},
			}},
		},
	})
	if project.AgentsAlive != 0 {
		t.Fatalf("project agents_alive = %d, want 0", project.AgentsAlive)
	}
	if project.State != "stale-blocked" || project.ReasonCode != "stale_blocked" {
		t.Fatalf("project state/reason = %q/%q, want stale-blocked/stale_blocked", project.State, project.ReasonCode)
	}
}

func TestNOCProjectJSONNewerClearActivityBeatsOldAtRisk(t *testing.T) {
	old := time.Now().Add(-24 * time.Hour)
	fresh := time.Now()
	project := nocProjectEnvelope(noc.ProjectSnapshot{
		Project: "api",
		Dir:     "/root/api",
		Snap: state.Snapshot{
			Rollup: state.TriageRollup{AtRisk: 1, Clear: 1},
			Sessions: []state.Session{{
				Name: "issue-3",
				Agents: []state.Agent{
					{Handle: "cto", Liveness: state.LivenessAlive, Attention: state.Attention{State: state.TriageAtRisk}},
					{Handle: "fullstack", Liveness: state.LivenessAlive, Attention: state.Attention{State: state.TriageAtRisk}},
				},
				Attention: state.Attention{State: state.TriageAtRisk},
				Rollup:    state.TriageRollup{AtRisk: 1, Clear: 1},
				Coordination: state.Coordination{Threads: []state.ThreadSummary{
					{ID: "decision/status-model", Triage: state.TriageAtRisk, LastEventAt: old, Participants: []string{"cto", "fullstack"}},
					{ID: "p2p/cto__fullstack", Triage: state.TriageClear, LastEventAt: fresh, Participants: []string{"cto", "fullstack"}},
				}},
			}},
		},
	})
	if project.AgentsAlive != 2 {
		t.Fatalf("project agents_alive = %d, want 2", project.AgentsAlive)
	}
	if project.State != "online" || project.ReasonCode != "online" {
		t.Fatalf("project state/reason = %q/%q, want online/online", project.State, project.ReasonCode)
	}
	if len(project.Sessions) != 1 || project.Sessions[0].State != "online" {
		t.Fatalf("session state = %#v, want one online child", project.Sessions)
	}
}

func TestNOCJSONBareProjectFilterIncludesChildren(t *testing.T) {
	ms := noc.MultiSnapshot{
		Roots: []string{"/root"},
		Projects: []noc.ProjectSnapshot{{
			Project: "api-service",
			Dir:     "/root/api-service",
			Snap: state.Snapshot{
				Sessions: []state.Session{
					{
						Name:   "issue-96",
						Agents: []state.Agent{{Handle: "cto", Role: "cto", Engine: "codex"}},
						Rollup: state.TriageRollup{NeedsYou: 1},
					},
					{
						Name:   "drive-fix",
						Agents: []state.Agent{{Handle: "qa", Role: "qa", Engine: "claude"}},
						Rollup: state.TriageRollup{Blocked: 2},
					},
				},
				Rollup: state.TriageRollup{NeedsYou: 1, Blocked: 2},
			},
		}},
		Rollup: state.TriageRollup{NeedsYou: 1, Blocked: 2},
	}

	scoped := filterNOCSnapshot(ms, "api-service", false)
	env := nocSnapshotEnvelope(scoped, "api-service", false)
	if env.ProjectCount != 1 || len(env.Projects) != 1 {
		t.Fatalf("project count/list = %d/%d, want 1/1", env.ProjectCount, len(env.Projects))
	}
	project := env.Projects[0]
	if project.SessionCount != 2 || project.AgentsTotal != 2 {
		t.Fatalf("bare project filter should keep child sessions/agents, got sessions:%d agents:%d",
			project.SessionCount, project.AgentsTotal)
	}
	if project.Rollup.NeedsYou != 1 || project.Rollup.Blocked != 2 {
		t.Fatalf("project rollup = %+v, want full project rollup", project.Rollup)
	}
}

func TestNOCJSONActionsExposeControlCommands(t *testing.T) {
	ps := noc.ProjectSnapshot{
		Project:        "api-service",
		Dir:            "/root/api service",
		TeamConfigured: true,
		DefaultTeam:    true,
		Profiles:       []string{team.DefaultProfile},
		SessionStore:   true,
		Snap: state.Snapshot{
			Sessions: []state.Session{{
				Name: "issue-96",
				Root: "/root/api service/.agent-mail/issue-96",
				Agents: []state.Agent{{
					Handle:      "cto",
					Role:        "cto",
					Engine:      "codex",
					Liveness:    state.LivenessAlive,
					TeamProfile: team.DefaultProfile,
				}},
				Coordination: state.Coordination{Threads: []state.ThreadSummary{{
					ID:           "ask/approve",
					LatestID:     "ask_approve",
					Participants: []string{"cto", "user"},
					Subject:      "APPROVAL: Ship it?",
					Triage:       state.TriageNeedsYou,
					AttnReason:   state.AttnApprove,
				}}},
				Rollup: state.TriageRollup{NeedsYou: 1},
			}},
		},
	}

	project := nocProjectEnvelope(ps)
	if project.ID != "project|/root/api service" {
		t.Fatalf("project id = %q", project.ID)
	}
	if project.BaseRoot != "/root/api service/.agent-mail" {
		t.Fatalf("project base_root = %q, want project AMQ base root", project.BaseRoot)
	}
	env := nocSnapshotEnvelope(noc.MultiSnapshot{Projects: []noc.ProjectSnapshot{ps}}, "", false)
	if env.ActionCount != len(env.Actions) {
		t.Fatalf("action_count = %d, len(actions) = %d", env.ActionCount, len(env.Actions))
	}
	if env.ActionCount == 0 || env.MutatingActionCount == 0 {
		t.Fatalf("flat action index counts = %d/%d, want nonzero", env.ActionCount, env.MutatingActionCount)
	}
	if !hasNOCActionID(env.Actions, "project|/root/api service|action|new_session") ||
		!hasNOCActionID(env.Actions, "project|/root/api service|action|history") ||
		!hasNOCActionID(env.Actions, "project|/root/api service|action|resume_plan") ||
		!hasNOCActionID(env.Actions, "project|/root/api service|action|team_rules") ||
		!hasNOCActionID(env.Actions, "project|/root/api service|action|delete_team") ||
		!hasNOCActionID(env.Actions, "project|/root/api service|action|amq_env") ||
		!hasNOCActionID(env.Actions, "project|/root/api service|action|amq_who") ||
		!hasNOCActionID(env.Actions, "project|/root/api service|action|sync_pointers") ||
		!hasNOCActionID(env.Actions, "session|/root/api service|issue-96|action|resume") ||
		!hasNOCActionID(env.Actions, "session|/root/api service|issue-96|action|threads") ||
		!hasNOCActionID(env.Actions, "session|/root/api service|issue-96|action|thread_context_any") ||
		!hasNOCActionID(env.Actions, "session|/root/api service|issue-96|action|brief") ||
		!hasNOCActionID(env.Actions, "session|/root/api service|issue-96|action|brief_seed") ||
		!hasNOCActionID(env.Actions, "session|/root/api service|issue-96|action|fork_plan") ||
		!hasNOCActionID(env.Actions, "session|/root/api service|issue-96|action|amq_ops") ||
		!hasNOCActionID(env.Actions, "session|/root/api service|issue-96|action|amq_cleanup") ||
		!hasNOCActionID(env.Actions, "session|/root/api service|issue-96|action|presence") ||
		!hasNOCActionID(env.Actions, "session|/root/api service|issue-96|action|thread_context") ||
		!hasNOCActionID(env.Actions, "session|/root/api service|issue-96|action|read_needs_you") ||
		!hasNOCActionID(env.Actions, "session|/root/api service|issue-96|action|reply") ||
		!hasNOCActionID(env.Actions, "session|/root/api service|issue-96|action|approve") ||
		!hasNOCActionID(env.Actions, "session|/root/api service|issue-96|action|deny") ||
		!hasNOCActionID(env.Actions, "session|/root/api service|issue-96|action|broadcast") ||
		!hasNOCActionID(env.Actions, "session|/root/api service|issue-96|action|archive") ||
		!hasNOCActionID(env.Actions, "session|/root/api service|issue-96|action|remove") ||
		!hasNOCActionID(env.Actions, "agent|/root/api service|issue-96|cto|action|inbox") ||
		!hasNOCActionID(env.Actions, "agent|/root/api service|issue-96|cto|action|dlq") ||
		!hasNOCActionID(env.Actions, "agent|/root/api service|issue-96|cto|action|receipts") ||
		!hasNOCActionID(env.Actions, "agent|/root/api service|issue-96|cto|action|receipts_wait") ||
		!hasNOCActionID(env.Actions, "agent|/root/api service|issue-96|cto|action|dlq_read") ||
		!hasNOCActionID(env.Actions, "agent|/root/api service|issue-96|cto|action|dlq_retry") ||
		!hasNOCActionID(env.Actions, "agent|/root/api service|issue-96|cto|action|dlq_retry_all") ||
		!hasNOCActionID(env.Actions, "agent|/root/api service|issue-96|cto|action|dlq_purge") ||
		!hasNOCActionID(env.Actions, "agent|/root/api service|issue-96|cto|action|thread_context") ||
		!hasNOCActionID(env.Actions, "agent|/root/api service|issue-96|cto|action|read_needs_you") ||
		!hasNOCActionID(env.Actions, "agent|/root/api service|issue-96|cto|action|reply") ||
		!hasNOCActionID(env.Actions, "agent|/root/api service|issue-96|cto|action|approve") ||
		!hasNOCActionID(env.Actions, "agent|/root/api service|issue-96|cto|action|deny") ||
		!hasNOCActionID(env.Actions, "agent|/root/api service|issue-96|cto|action|message") ||
		!hasNOCActionID(env.Actions, "agent|/root/api service|issue-96|cto|action|message_wait") ||
		!hasNOCActionID(env.Actions, "agent|/root/api service|issue-96|cto|action|drain") ||
		!hasNOCActionID(env.Actions, "agent|/root/api service|issue-96|cto|action|agent_resume") {
		t.Fatalf("flat action index missing project/session/agent actions: %+v", env.Actions)
	}

	newSession := requireNOCAction(t, project.Actions, "new_session")
	if !newSession.Mutates || !newSession.RequiresConfirmation || !newSession.Template {
		t.Fatalf("new_session action flags = %+v, want mutating confirmed template", newSession)
	}
	if newSession.Scope != "project" || newSession.TargetID != project.ID || newSession.ID != project.ID+"|action|new_session" {
		t.Fatalf("new_session action identity = %+v, project id %q", newSession, project.ID)
	}
	for _, want := range []string{"amq-squad new session", "--project '/root/api service'", "--target new-session", "'<session>'"} {
		if !strings.Contains(newSession.Command, want) {
			t.Fatalf("new_session command missing %q: %s", want, newSession.Command)
		}
	}
	sessionVar := requireNOCActionVar(t, newSession.Vars, "session")
	if !sessionVar.Required {
		t.Fatalf("session var should be required: %+v", sessionVar)
	}
	seedFromVar := requireNOCActionVar(t, newSession.Vars, "seed-from")
	if seedFromVar.Required || !reflect.DeepEqual(seedFromVar.Examples, []string{"issue:31", "file:./brief.md", "gh:owner/repo#31"}) {
		t.Fatalf("seed-from var should be optional with examples: %+v", seedFromVar)
	}
	tmuxVar := requireNOCActionVar(t, newSession.Vars, "tmux-session")
	if tmuxVar.Required || tmuxVar.DerivedFrom != "session" {
		t.Fatalf("tmux-session var should derive from session: %+v", tmuxVar)
	}
	teamProfiles := requireNOCAction(t, project.Actions, "team_profiles")
	if teamProfiles.Mutates {
		t.Fatalf("team_profiles should be read-only: %+v", teamProfiles)
	}
	teamRules := requireNOCAction(t, project.Actions, "team_rules")
	if teamRules.Mutates || teamRules.RequiresConfirmation || teamRules.Template {
		t.Fatalf("team_rules should be read-only concrete action: %+v", teamRules)
	}
	if !strings.Contains(teamRules.Command, "amq-squad team rules show --project '/root/api service'") {
		t.Fatalf("team_rules command should inspect durable rules: %q", teamRules.Command)
	}
	deleteTeam := requireNOCAction(t, project.Actions, "delete_team")
	if !deleteTeam.Mutates || !deleteTeam.RequiresConfirmation || !deleteTeam.Template {
		t.Fatalf("delete_team should be confirm-required template mutation: %+v", deleteTeam)
	}
	if !strings.Contains(deleteTeam.Command, "amq-squad team rm --project '/root/api service' --profile '<profile>' --yes") {
		t.Fatalf("delete_team command should remove one profile config: %q", deleteTeam.Command)
	}
	deleteProfileVar := requireNOCActionVar(t, deleteTeam.Vars, "profile")
	if !deleteProfileVar.Required || !reflect.DeepEqual(deleteProfileVar.Choices, []string{team.DefaultProfile}) {
		t.Fatalf("delete_team profile var should be required with profile choices: %+v", deleteProfileVar)
	}
	syncPointers := requireNOCAction(t, project.Actions, "sync_pointers")
	if !syncPointers.Mutates || !syncPointers.RequiresConfirmation || !syncPointers.Template {
		t.Fatalf("sync_pointers should be confirm-required template mutation: %+v", syncPointers)
	}
	for _, want := range []string{"amq-squad team sync", "--project '/root/api service'", "'<allow-outside>'", "--apply"} {
		if !strings.Contains(syncPointers.Command, want) {
			t.Fatalf("sync_pointers command missing %q: %s", want, syncPointers.Command)
		}
	}
	allowOutsideVar := requireNOCActionVar(t, syncPointers.Vars, "allow-outside")
	if allowOutsideVar.Required || !reflect.DeepEqual(allowOutsideVar.Choices, []string{"false", "true"}) {
		t.Fatalf("allow-outside var should be optional with false/true choices: %+v", allowOutsideVar)
	}
	doctor := requireNOCAction(t, project.Actions, "doctor")
	if doctor.Mutates || doctor.RequiresConfirmation {
		t.Fatalf("doctor should be read-only: %+v", doctor)
	}
	if !strings.Contains(doctor.Command, "amq-squad doctor --project '/root/api service' --all-profiles") {
		t.Fatalf("doctor command should check every profile: %q", doctor.Command)
	}
	history := requireNOCAction(t, project.Actions, "history")
	if history.Mutates || history.RequiresConfirmation {
		t.Fatalf("history should be read-only: %+v", history)
	}
	if !strings.Contains(history.Command, "amq-squad history --project '/root/api service'") {
		t.Fatalf("history command should read launch records: %q", history.Command)
	}
	amqWho := requireNOCAction(t, project.Actions, "amq_who")
	if amqWho.Mutates || amqWho.RequiresConfirmation || amqWho.Template {
		t.Fatalf("amq_who should be read-only concrete action: %+v", amqWho)
	}
	if !strings.Contains(amqWho.Command, "amq who --root '/root/api service/.agent-mail'") {
		t.Fatalf("amq_who command should inspect the project AMQ base root: %q", amqWho.Command)
	}
	amqEnv := requireNOCAction(t, project.Actions, "amq_env")
	if amqEnv.Mutates || amqEnv.RequiresConfirmation || amqEnv.Template {
		t.Fatalf("amq_env should be read-only concrete action: %+v", amqEnv)
	}
	if !strings.Contains(amqEnv.Command, "amq env --root '/root/api service/.agent-mail' --json") {
		t.Fatalf("amq_env command should inspect the project AMQ env: %q", amqEnv.Command)
	}
	resumePlan := requireNOCAction(t, project.Actions, "resume_plan")
	if resumePlan.Mutates || resumePlan.RequiresConfirmation || resumePlan.Template {
		t.Fatalf("resume_plan should be read-only and concrete for one profile: %+v", resumePlan)
	}
	if !strings.Contains(resumePlan.Command, "amq-squad resume --project '/root/api service'") {
		t.Fatalf("resume_plan command should print a recovery plan: %q", resumePlan.Command)
	}
	roles := requireNOCAction(t, project.Actions, "roles")
	if roles.Mutates || roles.Command != "amq-squad roles" {
		t.Fatalf("roles should be read-only role-market action: %+v", roles)
	}
	newProfile := requireNOCAction(t, project.Actions, "new_profile")
	for _, want := range []string{"amq-squad new profile '<profile>'", "--project '/root/api service'", "--roles '<roles>'", "--binary '<binary>'", "--session '<session>'"} {
		if !strings.Contains(newProfile.Command, want) {
			t.Fatalf("new_profile command missing %q: %s", want, newProfile.Command)
		}
	}
	if strings.Contains(newProfile.Command, "--sync") {
		t.Fatalf("new_profile action should not run pointer sync implicitly: %s", newProfile.Command)
	}
	profileSessionVar := requireNOCActionVar(t, newProfile.Vars, "session")
	if profileSessionVar.Required {
		t.Fatalf("new_profile session var should be optional: %+v", profileSessionVar)
	}

	if len(project.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(project.Sessions))
	}
	session := project.Sessions[0]
	if session.ID != "session|/root/api service|issue-96" {
		t.Fatalf("session id = %q", session.ID)
	}
	if session.ThreadCount != 1 || session.ThreadsReturned != 1 || len(session.Threads) != 1 {
		t.Fatalf("session thread counts = %d/%d/%d, want 1/1/1", session.ThreadCount, session.ThreadsReturned, len(session.Threads))
	}
	if session.Threads[0].ID != "ask/approve" || session.Threads[0].LatestID != "ask_approve" || session.Threads[0].Triage != string(state.TriageNeedsYou) {
		t.Fatalf("session thread summary mismatch: %+v", session.Threads[0])
	}
	resume := requireNOCAction(t, session.Actions, "resume")
	if resume.Scope != "session" || resume.TargetID != session.ID || resume.ID != session.ID+"|action|resume" {
		t.Fatalf("resume action identity = %+v, session id %q", resume, session.ID)
	}
	if !strings.Contains(resume.Command, "amq-squad resume --project '/root/api service' --exec --target new-session") {
		t.Fatalf("resume command = %q, want project-scoped detached resume", resume.Command)
	}
	forkPlan := requireNOCAction(t, session.Actions, "fork_plan")
	if forkPlan.Mutates || forkPlan.RequiresConfirmation || !forkPlan.Template {
		t.Fatalf("fork_plan should be read-only template action: %+v", forkPlan)
	}
	for _, want := range []string{"amq-squad fork", "--project '/root/api service'", "--from issue-96", "--as '<session>'"} {
		if !strings.Contains(forkPlan.Command, want) {
			t.Fatalf("fork_plan command missing %q: %s", want, forkPlan.Command)
		}
	}
	brief := requireNOCAction(t, session.Actions, "brief")
	if brief.Mutates || brief.RequiresConfirmation || brief.Template {
		t.Fatalf("brief should be read-only concrete action: %+v", brief)
	}
	if !strings.Contains(brief.Command, "amq-squad brief --project '/root/api service' --session issue-96") {
		t.Fatalf("brief command should read the workstream brief: %q", brief.Command)
	}
	threads := requireNOCAction(t, session.Actions, "threads")
	if threads.Mutates || threads.RequiresConfirmation || threads.Template {
		t.Fatalf("threads should be read-only concrete action: %+v", threads)
	}
	if !strings.Contains(threads.Command, "amq-squad threads --project '/root/api service' --session issue-96 --limit 20") {
		t.Fatalf("threads command should read session thread summaries: %q", threads.Command)
	}
	threadContextAny := requireNOCAction(t, session.Actions, "thread_context_any")
	if threadContextAny.Mutates || threadContextAny.RequiresConfirmation || !threadContextAny.Template {
		t.Fatalf("thread_context_any should be read-only template action: %+v", threadContextAny)
	}
	for _, want := range []string{"amq-squad thread", "--project '/root/api service'", "--session issue-96", "--id '<thread-id>'", "--include-body", "--limit 20"} {
		if !strings.Contains(threadContextAny.Command, want) {
			t.Fatalf("thread_context_any command missing %q: %s", want, threadContextAny.Command)
		}
	}
	threadIDVar := requireNOCActionVar(t, threadContextAny.Vars, "thread-id")
	if !threadIDVar.Required || len(threadIDVar.Examples) == 0 {
		t.Fatalf("thread_context_any thread-id var should be required with examples: %+v", threadIDVar)
	}
	briefSeed := requireNOCAction(t, session.Actions, "brief_seed")
	if !briefSeed.Mutates || !briefSeed.RequiresConfirmation || !briefSeed.Template {
		t.Fatalf("brief_seed should be confirm-required template mutation: %+v", briefSeed)
	}
	for _, want := range []string{"amq-squad brief seed", "--project '/root/api service'", "--session issue-96", "--seed-from '<seed-from>'", "'<force>'"} {
		if !strings.Contains(briefSeed.Command, want) {
			t.Fatalf("brief_seed command missing %q: %s", want, briefSeed.Command)
		}
	}
	briefSeedFromVar := requireNOCActionVar(t, briefSeed.Vars, "seed-from")
	if !briefSeedFromVar.Required || !reflect.DeepEqual(briefSeedFromVar.Examples, []string{"issue:31", "file:./brief.md", "gh:owner/repo#31"}) {
		t.Fatalf("brief_seed seed-from var should be required with examples: %+v", briefSeedFromVar)
	}
	forceVar := requireNOCActionVar(t, briefSeed.Vars, "force")
	if forceVar.Required || !reflect.DeepEqual(forceVar.Choices, []string{"false", "true"}) {
		t.Fatalf("brief_seed force var should be optional with choices: %+v", forceVar)
	}
	stop := requireNOCAction(t, session.Actions, "stop")
	if !strings.Contains(stop.Command, "amq-squad stop --project '/root/api service' --all --session issue-96") {
		t.Fatalf("stop command = %q, want project-scoped stop", stop.Command)
	}
	restart := requireNOCAction(t, session.Actions, "restart")
	if !strings.Contains(restart.Command, " && ") {
		t.Fatalf("restart command should compose stop and resume: %s", restart.Command)
	}
	archive := requireNOCAction(t, session.Actions, "archive")
	if !archive.Mutates || !archive.RequiresConfirmation || archive.Template {
		t.Fatalf("archive should be confirm-required non-template mutation: %+v", archive)
	}
	if !strings.Contains(archive.Command, "amq-squad archive --project '/root/api service' --yes issue-96") {
		t.Fatalf("archive command = %q", archive.Command)
	}
	remove := requireNOCAction(t, session.Actions, "remove")
	if !remove.Mutates || !remove.RequiresConfirmation || remove.Template {
		t.Fatalf("remove should be confirm-required non-template mutation: %+v", remove)
	}
	if !strings.Contains(remove.Command, "amq-squad rm --project '/root/api service' --yes issue-96") {
		t.Fatalf("remove command = %q", remove.Command)
	}
	amqOps := requireNOCAction(t, session.Actions, "amq_ops")
	if amqOps.Mutates || amqOps.RequiresConfirmation {
		t.Fatalf("amq_ops should be read-only: %+v", amqOps)
	}
	if !strings.Contains(amqOps.Command, "env 'AM_ROOT=/root/api service/.agent-mail/issue-96' amq doctor --ops") {
		t.Fatalf("amq_ops command = %q", amqOps.Command)
	}
	presence := requireNOCAction(t, session.Actions, "presence")
	if presence.Mutates || presence.RequiresConfirmation || presence.Template {
		t.Fatalf("presence should be read-only concrete action: %+v", presence)
	}
	if !strings.Contains(presence.Command, "amq presence list --root '/root/api service/.agent-mail/issue-96'") {
		t.Fatalf("presence command = %q", presence.Command)
	}
	routeExplain := requireNOCAction(t, session.Actions, "route_explain")
	if routeExplain.Mutates || routeExplain.RequiresConfirmation || !routeExplain.Template {
		t.Fatalf("route_explain should be read-only template action: %+v", routeExplain)
	}
	if !strings.Contains(routeExplain.Command, "amq route explain --from-root '/root/api service/.agent-mail/issue-96' --me user --to '<to>' --json --session issue-96") {
		t.Fatalf("route_explain command = %q", routeExplain.Command)
	}
	toVar := requireNOCActionVar(t, routeExplain.Vars, "to")
	if !toVar.Required || len(toVar.Examples) == 0 {
		t.Fatalf("route_explain to var should be required with examples: %+v", toVar)
	}
	amqCleanup := requireNOCAction(t, session.Actions, "amq_cleanup")
	if !amqCleanup.Mutates || !amqCleanup.RequiresConfirmation || !amqCleanup.Template {
		t.Fatalf("amq_cleanup should be confirm-required template mutation: %+v", amqCleanup)
	}
	if !strings.Contains(amqCleanup.Command, "amq cleanup --root '/root/api service/.agent-mail/issue-96' --tmp-older-than '<tmp-older-than>' --yes") {
		t.Fatalf("amq_cleanup command = %q", amqCleanup.Command)
	}
	tmpOlderThanVar := requireNOCActionVar(t, amqCleanup.Vars, "tmp-older-than")
	if !tmpOlderThanVar.Required || !reflect.DeepEqual(tmpOlderThanVar.Examples, []string{"36h", "168h"}) {
		t.Fatalf("amq_cleanup tmp-older-than var should be required with examples: %+v", tmpOlderThanVar)
	}
	if strings.Contains(amqCleanup.Command, "extensions") {
		t.Fatalf("amq_cleanup must not target extension metadata paths directly: %q", amqCleanup.Command)
	}
	threadContext := requireNOCAction(t, session.Actions, "thread_context")
	if threadContext.Mutates || threadContext.RequiresConfirmation || threadContext.Template {
		t.Fatalf("thread_context should be read-only: %+v", threadContext)
	}
	if !strings.Contains(threadContext.Command, "amq thread --root '/root/api service/.agent-mail/issue-96' --id ask/approve --include-body --limit 20") {
		t.Fatalf("thread_context command = %q", threadContext.Command)
	}
	readNeedsYou := requireNOCAction(t, session.Actions, "read_needs_you")
	if !readNeedsYou.Mutates || !readNeedsYou.RequiresConfirmation || readNeedsYou.Template {
		t.Fatalf("read_needs_you should be confirm-required mutation: %+v", readNeedsYou)
	}
	if !strings.Contains(readNeedsYou.Command, "amq read --root '/root/api service/.agent-mail/issue-96' --me user --id ask_approve --json") {
		t.Fatalf("read_needs_you command = %q", readNeedsYou.Command)
	}
	reply := requireNOCAction(t, session.Actions, "reply")
	if !reply.Mutates || !reply.RequiresConfirmation || !reply.Template {
		t.Fatalf("reply should be confirm-required template mutation: %+v", reply)
	}
	for _, want := range []string{"amq send", "--root '/root/api service/.agent-mail/issue-96'", "--me user", "--to cto", "--subject 'Re: APPROVAL: Ship it?'", "--body '<body>'", "--thread ask/approve", "--kind answer"} {
		if !strings.Contains(reply.Command, want) {
			t.Fatalf("reply command missing %q: %s", want, reply.Command)
		}
	}
	if !requireNOCActionVar(t, reply.Vars, "body").Required {
		t.Fatalf("reply should require body var: %+v", reply.Vars)
	}
	approve := requireNOCAction(t, session.Actions, "approve")
	if !approve.Mutates || !approve.RequiresConfirmation || approve.Template {
		t.Fatalf("approve should be confirm-required mutation: %+v", approve)
	}
	for _, want := range []string{"amq send", "--root '/root/api service/.agent-mail/issue-96'", "--me user", "--to cto", "--subject 'Re: APPROVAL: Ship it?'", "--body APPROVED", "--thread ask/approve", "--kind answer"} {
		if !strings.Contains(approve.Command, want) {
			t.Fatalf("approve command missing %q: %s", want, approve.Command)
		}
	}
	deny := requireNOCAction(t, session.Actions, "deny")
	if !deny.Mutates || !deny.RequiresConfirmation || !deny.Template {
		t.Fatalf("deny should be confirm-required template mutation: %+v", deny)
	}
	if !requireNOCActionVar(t, deny.Vars, "reason").Required {
		t.Fatalf("deny should require reason var: %+v", deny.Vars)
	}
	broadcast := requireNOCAction(t, session.Actions, "broadcast")
	if !broadcast.Mutates || !broadcast.RequiresConfirmation || !broadcast.Template {
		t.Fatalf("broadcast should be confirm-required template mutation: %+v", broadcast)
	}
	for _, want := range []string{"amq send", "--root '/root/api service/.agent-mail/issue-96'", "--me user", "--to cto", "--subject '<subject>'", "--body '<body>'", "--thread broadcast/operator", "--kind status"} {
		if !strings.Contains(broadcast.Command, want) {
			t.Fatalf("broadcast command missing %q: %s", want, broadcast.Command)
		}
	}
	if !requireNOCActionVar(t, broadcast.Vars, "subject").Required ||
		!requireNOCActionVar(t, broadcast.Vars, "body").Required {
		t.Fatalf("broadcast should require subject and body vars: %+v", broadcast.Vars)
	}

	if len(session.Agents) != 1 {
		t.Fatalf("agents = %d, want 1", len(session.Agents))
	}
	if session.Agents[0].ID != "agent|/root/api service|issue-96|cto" {
		t.Fatalf("agent id = %q", session.Agents[0].ID)
	}
	inbox := requireNOCAction(t, session.Agents[0].Actions, "inbox")
	if inbox.Mutates || inbox.RequiresConfirmation {
		t.Fatalf("inbox should be read-only: %+v", inbox)
	}
	if !strings.Contains(inbox.Command, "amq list --root '/root/api service/.agent-mail/issue-96' --me cto --new") {
		t.Fatalf("inbox command = %q", inbox.Command)
	}
	dlq := requireNOCAction(t, session.Agents[0].Actions, "dlq")
	if dlq.Mutates || dlq.RequiresConfirmation || dlq.Template {
		t.Fatalf("DLQ should be read-only: %+v", dlq)
	}
	if !strings.Contains(dlq.Command, "amq dlq list --root '/root/api service/.agent-mail/issue-96' --me cto") {
		t.Fatalf("DLQ command = %q", dlq.Command)
	}
	receipts := requireNOCAction(t, session.Agents[0].Actions, "receipts")
	if receipts.Mutates || receipts.RequiresConfirmation || receipts.Template {
		t.Fatalf("receipts should be read-only: %+v", receipts)
	}
	if !strings.Contains(receipts.Command, "amq receipts list --root '/root/api service/.agent-mail/issue-96' --me cto") {
		t.Fatalf("receipts command = %q", receipts.Command)
	}
	receiptsWait := requireNOCAction(t, session.Agents[0].Actions, "receipts_wait")
	if receiptsWait.Mutates || receiptsWait.RequiresConfirmation || !receiptsWait.Template {
		t.Fatalf("receipts_wait should be read-only template action: %+v", receiptsWait)
	}
	for _, want := range []string{"amq receipts wait", "--root '/root/api service/.agent-mail/issue-96'", "--me cto", "--msg-id '<msg-id>'", "--stage '<stage>'", "--timeout '<timeout>'"} {
		if !strings.Contains(receiptsWait.Command, want) {
			t.Fatalf("receipts_wait command missing %q: %s", want, receiptsWait.Command)
		}
	}
	msgIDVar := requireNOCActionVar(t, receiptsWait.Vars, "msg-id")
	if !msgIDVar.Required || !reflect.DeepEqual(msgIDVar.Examples, []string{"msg_123", "20260601T090000Z_abc123"}) {
		t.Fatalf("receipts_wait msg-id var mismatch: %+v", msgIDVar)
	}
	stageVar := requireNOCActionVar(t, receiptsWait.Vars, "stage")
	if !stageVar.Required || !reflect.DeepEqual(stageVar.Choices, []string{"dlq", "drained"}) {
		t.Fatalf("receipts_wait stage var mismatch: %+v", stageVar)
	}
	receiptsTimeoutVar := requireNOCActionVar(t, receiptsWait.Vars, "timeout")
	if !receiptsTimeoutVar.Required || !reflect.DeepEqual(receiptsTimeoutVar.Examples, []string{"60s", "5m"}) {
		t.Fatalf("receipts_wait timeout var mismatch: %+v", receiptsTimeoutVar)
	}
	dlqRead := requireNOCAction(t, session.Agents[0].Actions, "dlq_read")
	if !dlqRead.Mutates || !dlqRead.RequiresConfirmation || !dlqRead.Template {
		t.Fatalf("DLQ read should be confirm-required template mutation: %+v", dlqRead)
	}
	if !strings.Contains(dlqRead.Command, "amq dlq read --root '/root/api service/.agent-mail/issue-96' --me cto --id '<dlq-id>'") {
		t.Fatalf("DLQ read command = %q", dlqRead.Command)
	}
	dlqIDVar := requireNOCActionVar(t, dlqRead.Vars, "dlq-id")
	if !dlqIDVar.Required || !reflect.DeepEqual(dlqIDVar.Examples, []string{"dlq_123", "dlq_123.md"}) {
		t.Fatalf("DLQ id var mismatch: %+v", dlqIDVar)
	}
	dlqRetry := requireNOCAction(t, session.Agents[0].Actions, "dlq_retry")
	if !dlqRetry.Mutates || !dlqRetry.RequiresConfirmation || !dlqRetry.Template {
		t.Fatalf("DLQ retry should be confirm-required template mutation: %+v", dlqRetry)
	}
	if !strings.Contains(dlqRetry.Command, "amq dlq retry --root '/root/api service/.agent-mail/issue-96' --me cto --id '<dlq-id>'") {
		t.Fatalf("DLQ retry command = %q", dlqRetry.Command)
	}
	dlqRetryAll := requireNOCAction(t, session.Agents[0].Actions, "dlq_retry_all")
	if !dlqRetryAll.Mutates || !dlqRetryAll.RequiresConfirmation || dlqRetryAll.Template {
		t.Fatalf("DLQ retry all should be confirm-required non-template mutation: %+v", dlqRetryAll)
	}
	if !strings.Contains(dlqRetryAll.Command, "amq dlq retry --root '/root/api service/.agent-mail/issue-96' --me cto --all") {
		t.Fatalf("DLQ retry all command = %q", dlqRetryAll.Command)
	}
	dlqPurge := requireNOCAction(t, session.Agents[0].Actions, "dlq_purge")
	if !dlqPurge.Mutates || !dlqPurge.RequiresConfirmation || !dlqPurge.Template {
		t.Fatalf("DLQ purge should be confirm-required template mutation: %+v", dlqPurge)
	}
	if !strings.Contains(dlqPurge.Command, "amq dlq purge --root '/root/api service/.agent-mail/issue-96' --me cto --older-than '<older-than>' --yes") {
		t.Fatalf("DLQ purge command = %q", dlqPurge.Command)
	}
	olderThanVar := requireNOCActionVar(t, dlqPurge.Vars, "older-than")
	if !olderThanVar.Required || !reflect.DeepEqual(olderThanVar.Examples, []string{"24h", "168h"}) {
		t.Fatalf("DLQ purge older-than var mismatch: %+v", olderThanVar)
	}
	agentApprove := requireNOCAction(t, session.Agents[0].Actions, "approve")
	if agentApprove.Command != approve.Command {
		t.Fatalf("agent approve command = %q, want session top needs-you command %q", agentApprove.Command, approve.Command)
	}
	agentReply := requireNOCAction(t, session.Agents[0].Actions, "reply")
	if agentReply.Command != reply.Command {
		t.Fatalf("agent reply command = %q, want session top needs-you command %q", agentReply.Command, reply.Command)
	}
	message := requireNOCAction(t, session.Agents[0].Actions, "message")
	if !message.Mutates || !message.RequiresConfirmation || !message.Template {
		t.Fatalf("message should be confirm-required template mutation: %+v", message)
	}
	for _, want := range []string{"amq send", "--root '/root/api service/.agent-mail/issue-96'", "--me user", "--to cto", "--subject 'Message from operator'", "--body '<body>'", "--kind status"} {
		if !strings.Contains(message.Command, want) {
			t.Fatalf("message command missing %q: %s", want, message.Command)
		}
	}
	if !requireNOCActionVar(t, message.Vars, "body").Required {
		t.Fatalf("message should require body var: %+v", message.Vars)
	}
	messageWait := requireNOCAction(t, session.Agents[0].Actions, "message_wait")
	if !messageWait.Mutates || !messageWait.RequiresConfirmation || !messageWait.Template {
		t.Fatalf("message_wait should be confirm-required template mutation: %+v", messageWait)
	}
	for _, want := range []string{"amq send", "--root '/root/api service/.agent-mail/issue-96'", "--me user", "--to cto", "--subject 'Message from operator'", "--body '<body>'", "--kind status", "--wait-for drained", "--wait-timeout '<timeout>'"} {
		if !strings.Contains(messageWait.Command, want) {
			t.Fatalf("message_wait command missing %q: %s", want, messageWait.Command)
		}
	}
	if !requireNOCActionVar(t, messageWait.Vars, "body").Required {
		t.Fatalf("message_wait should require body var: %+v", messageWait.Vars)
	}
	timeoutVar := requireNOCActionVar(t, messageWait.Vars, "timeout")
	if !timeoutVar.Required || !reflect.DeepEqual(timeoutVar.Examples, []string{"60s", "5m"}) {
		t.Fatalf("message_wait timeout var mismatch: %+v", timeoutVar)
	}
	drain := requireNOCAction(t, session.Agents[0].Actions, "drain")
	if !drain.Mutates || !drain.RequiresConfirmation {
		t.Fatalf("drain should be confirm-required mutation: %+v", drain)
	}
	if !strings.Contains(drain.Command, "amq drain --root '/root/api service/.agent-mail/issue-96' --me cto --include-body") {
		t.Fatalf("drain command = %q", drain.Command)
	}
	agentResume := requireNOCAction(t, session.Agents[0].Actions, "agent_resume")
	if agentResume.Scope != "agent" || agentResume.TargetID != session.Agents[0].ID ||
		agentResume.ID != session.Agents[0].ID+"|action|agent_resume" {
		t.Fatalf("agent_resume action identity = %+v, agent id %q", agentResume, session.Agents[0].ID)
	}
	if !strings.Contains(agentResume.Command, "amq-squad agent resume cto --project '/root/api service' --session issue-96") {
		t.Fatalf("agent_resume command = %q", agentResume.Command)
	}
}

func TestNOCJSONNewSessionActionExposesProfileChoices(t *testing.T) {
	project := nocProjectEnvelope(noc.ProjectSnapshot{
		Project:        "api-service",
		Dir:            "/root/api",
		TeamConfigured: true,
		DefaultTeam:    true,
		Profiles:       []string{"review", team.DefaultProfile, "review"},
	})

	newSession := requireNOCAction(t, project.Actions, "new_session")
	profileVar := requireNOCActionVar(t, newSession.Vars, "profile")
	if !profileVar.Required {
		t.Fatalf("profile var should be required: %+v", profileVar)
	}
	if want := []string{team.DefaultProfile, "review"}; !reflect.DeepEqual(profileVar.Choices, want) {
		t.Fatalf("profile choices = %+v, want %+v", profileVar.Choices, want)
	}
	syncPointers := requireNOCAction(t, project.Actions, "sync_pointers")
	syncProfileVar := requireNOCActionVar(t, syncPointers.Vars, "profile")
	if !syncProfileVar.Required {
		t.Fatalf("sync_pointers profile var should be required: %+v", syncProfileVar)
	}
	if want := []string{team.DefaultProfile, "review"}; !reflect.DeepEqual(syncProfileVar.Choices, want) {
		t.Fatalf("sync_pointers profile choices = %+v, want %+v", syncProfileVar.Choices, want)
	}
	deleteTeam := requireNOCAction(t, project.Actions, "delete_team")
	deleteProfileVar := requireNOCActionVar(t, deleteTeam.Vars, "profile")
	if !deleteProfileVar.Required {
		t.Fatalf("delete_team profile var should be required: %+v", deleteProfileVar)
	}
	if want := []string{team.DefaultProfile, "review"}; !reflect.DeepEqual(deleteProfileVar.Choices, want) {
		t.Fatalf("delete_team profile choices = %+v, want %+v", deleteProfileVar.Choices, want)
	}
	resumePlan := requireNOCAction(t, project.Actions, "resume_plan")
	if resumePlan.Mutates || resumePlan.RequiresConfirmation || !resumePlan.Template {
		t.Fatalf("resume_plan should be read-only template for multiple profiles: %+v", resumePlan)
	}
	resumeProfileVar := requireNOCActionVar(t, resumePlan.Vars, "profile")
	if !resumeProfileVar.Required {
		t.Fatalf("resume_plan profile var should be required: %+v", resumeProfileVar)
	}
	if want := []string{team.DefaultProfile, "review"}; !reflect.DeepEqual(resumeProfileVar.Choices, want) {
		t.Fatalf("resume_plan profile choices = %+v, want %+v", resumeProfileVar.Choices, want)
	}
}

func TestNOCJSONConfiguredEmptyProjectExposesStatusAction(t *testing.T) {
	project := nocProjectEnvelope(noc.ProjectSnapshot{
		Project:        "api-service",
		Dir:            "/root/api",
		TeamConfigured: true,
		DefaultTeam:    true,
		Profiles:       []string{team.DefaultProfile},
	})

	status := requireNOCAction(t, project.Actions, "status")
	if status.Mutates || status.RequiresConfirmation || status.Template {
		t.Fatalf("status should be read-only concrete action: %+v", status)
	}
	if status.Command != "amq-squad status --project /root/api" {
		t.Fatalf("status command = %q", status.Command)
	}
	teamRules := requireNOCAction(t, project.Actions, "team_rules")
	if teamRules.Mutates || teamRules.RequiresConfirmation || teamRules.Template {
		t.Fatalf("team_rules should be read-only concrete action: %+v", teamRules)
	}
	if teamRules.Command != "amq-squad team rules show --project /root/api" {
		t.Fatalf("team_rules command = %q", teamRules.Command)
	}
	for _, action := range project.Actions {
		switch action.Name {
		case "amq_env", "amq_who":
			t.Fatalf("configured empty project without AMQ store should not expose %s: %+v", action.Name, project.Actions)
		}
	}
}

func TestNOCJSONCandidateActionsSuggestNewTeam(t *testing.T) {
	project := nocProjectEnvelope(noc.ProjectSnapshot{
		Project:   "candidate",
		Dir:       "/root/candidate",
		Candidate: true,
	})
	if project.BaseRoot != "" {
		t.Fatalf("candidate base_root = %q, want empty until a session store exists", project.BaseRoot)
	}

	newTeam := requireNOCAction(t, project.Actions, "new_team")
	if !newTeam.Mutates || !newTeam.RequiresConfirmation || !newTeam.Template {
		t.Fatalf("new_team action flags = %+v, want mutating confirmed template", newTeam)
	}
	if newTeam.Scope != "project" || newTeam.TargetID != project.ID {
		t.Fatalf("new_team action identity = %+v, project id %q", newTeam, project.ID)
	}
	roles := requireNOCAction(t, project.Actions, "roles")
	if roles.Mutates || roles.Command != "amq-squad roles" {
		t.Fatalf("roles should be read-only role-market action: %+v", roles)
	}
	for _, want := range []string{"amq-squad new team", "--project /root/candidate", "--roles '<roles>'", "--binary '<binary>'", "--session '<session>'"} {
		if !strings.Contains(newTeam.Command, want) {
			t.Fatalf("new_team command missing %q: %s", want, newTeam.Command)
		}
	}
	if strings.Contains(newTeam.Command, "--sync") {
		t.Fatalf("new_team action should not run pointer sync implicitly: %s", newTeam.Command)
	}
	if strings.Contains(newTeam.Command, "binary-flag") {
		t.Fatalf("new_team command should not expose internal binary-flag placeholder: %s", newTeam.Command)
	}
	rolesVar := requireNOCActionVar(t, newTeam.Vars, "roles")
	if !rolesVar.Required {
		t.Fatalf("roles var should be required: %+v", rolesVar)
	}
	if want := []string{"cto,qa", "2,9", "all", "cto=codex,qa"}; !reflect.DeepEqual(rolesVar.Examples, want) {
		t.Fatalf("roles examples = %+v, want %+v", rolesVar.Examples, want)
	}
	binaryVar := requireNOCActionVar(t, newTeam.Vars, "binary")
	if binaryVar.Required || binaryVar.DerivedFrom != "" {
		t.Fatalf("binary var should be optional: %+v", binaryVar)
	}
	if want := []string{"qa=codex", "cto=claude,qa=codex"}; !reflect.DeepEqual(binaryVar.Examples, want) {
		t.Fatalf("binary examples = %+v, want %+v", binaryVar.Examples, want)
	}
	sessionVar := requireNOCActionVar(t, newTeam.Vars, "session")
	if sessionVar.Required {
		t.Fatalf("session var should be optional: %+v", sessionVar)
	}
}

func TestNOCJSONMixedProfileSessionActionsExposeProfileVar(t *testing.T) {
	ps := noc.ProjectSnapshot{
		Project:        "api-service",
		Dir:            "/root/api",
		TeamConfigured: true,
		DefaultTeam:    true,
		Profiles:       []string{team.DefaultProfile, "review"},
		SessionStore:   true,
		Snap: state.Snapshot{
			Sessions: []state.Session{{
				Name: "issue-96",
				Agents: []state.Agent{
					{Handle: "cto", Role: "cto", Engine: "codex", TeamProfile: team.DefaultProfile},
					{Handle: "qa", Role: "qa", Engine: "claude", TeamProfile: "review"},
				},
			}},
		},
	}

	project := nocProjectEnvelope(ps)
	if len(project.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(project.Sessions))
	}
	resume := requireNOCAction(t, project.Sessions[0].Actions, "resume")
	if !resume.Template {
		t.Fatalf("mixed-profile resume should be a template action: %+v", resume)
	}
	profileVar := requireNOCActionVar(t, resume.Vars, "profile")
	if !profileVar.Required {
		t.Fatalf("profile var should be required: %+v", profileVar)
	}
	if want := []string{team.DefaultProfile, "review"}; !reflect.DeepEqual(profileVar.Choices, want) {
		t.Fatalf("profile choices = %+v, want %+v", profileVar.Choices, want)
	}
	forkPlan := requireNOCAction(t, project.Sessions[0].Actions, "fork_plan")
	if !forkPlan.Template || forkPlan.Mutates || forkPlan.RequiresConfirmation {
		t.Fatalf("mixed-profile fork_plan should be a read-only template action: %+v", forkPlan)
	}
	forkProfileVar := requireNOCActionVar(t, forkPlan.Vars, "profile")
	if !forkProfileVar.Required {
		t.Fatalf("fork_plan profile var should be required: %+v", forkProfileVar)
	}
	if want := []string{team.DefaultProfile, "review"}; !reflect.DeepEqual(forkProfileVar.Choices, want) {
		t.Fatalf("fork_plan profile choices = %+v, want %+v", forkProfileVar.Choices, want)
	}
}

func TestNOCJSONFlatActionsHonorFilter(t *testing.T) {
	ms := noc.MultiSnapshot{
		Projects: []noc.ProjectSnapshot{{
			Project:        "api",
			Dir:            "/root/api",
			TeamConfigured: true,
			DefaultTeam:    true,
			Profiles:       []string{team.DefaultProfile},
			Snap: state.Snapshot{Sessions: []state.Session{
				{
					Name:   "issue-96",
					Agents: []state.Agent{{Handle: "cto", Role: "cto", Engine: "codex"}},
				},
				{
					Name:   "drive-fix",
					Agents: []state.Agent{{Handle: "qa", Role: "qa", Engine: "claude"}},
				},
			}},
		}},
	}

	scoped := filterNOCSnapshot(ms, "session:issue-96", false)
	env := nocSnapshotEnvelope(scoped, "session:issue-96", false)
	if env.ActionCount != len(env.Actions) || env.ActionCount == 0 {
		t.Fatalf("flat actions count = %d/%d, want nonzero and consistent", env.ActionCount, len(env.Actions))
	}
	for _, action := range env.Actions {
		if strings.Contains(action.ID, "drive-fix") || strings.Contains(action.Command, "drive-fix") {
			t.Fatalf("filtered flat actions leaked drive-fix action: %+v", action)
		}
	}
	if !hasNOCActionID(env.Actions, "session|/root/api|issue-96|action|resume") {
		t.Fatalf("filtered flat actions missing issue-96 resume: %+v", env.Actions)
	}
}

func TestRunNOCJSONHideStaleScopesProjects(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    filepath.Join(base, "issue-96"),
	})

	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--json", "--hide-stale", "--root", root})
	})
	if err != nil {
		t.Fatalf("runNOC --json --hide-stale: %v", err)
	}
	env := decodeJSONEnvelope[nocSnapshotEnvelopeData](t, stdout)
	if !env.Data.HideStale {
		t.Fatal("hide_stale = false, want true")
	}
	if env.Data.ProjectCount != 0 || len(env.Data.Projects) != 0 {
		t.Fatalf("hide-stale project count/list = %d/%d, want 0/0", env.Data.ProjectCount, len(env.Data.Projects))
	}
}

func TestExecuteNOCJSONDoesNotCallRunNOC(t *testing.T) {
	var cap captureNOC
	var out bytes.Buffer
	root := t.TempDir()
	exec := nocExecution{
		Cwd:         root,
		Roots:       []string{root},
		JSON:        true,
		Out:         &out,
		StdoutIsTTY: true,
		RunNOC:      cap.run,
	}
	if err := executeNOC(exec); err != nil {
		t.Fatalf("executeNOC json: %v", err)
	}
	if cap.called {
		t.Fatal("json mode should collect and write directly, not start the TUI runner")
	}
	env := decodeJSONEnvelope[nocSnapshotEnvelopeData](t, out.String())
	if env.Kind != "noc_snapshot" {
		t.Errorf("kind = %q, want noc_snapshot", env.Kind)
	}
}

func TestRunNOC_DispatchedFromCLI(t *testing.T) {
	// `amq-squad noc` is a recognized verb (not unknown-command). We drive it via
	// --once over a seeded fixture so it does not open /dev/tty.
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := mkAgentMail(proj); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--once", "--root", root})
	})
	if err != nil {
		t.Fatalf("runNOC --once: %v", err)
	}
	if !containsCLI(stdout, "NOC") {
		t.Errorf("noc --once board should render the NOC header, got:\n%s", stdout)
	}
}

func TestRunNOCRootExpandsHome(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "Code")
	proj := filepath.Join(root, "p")
	if err := mkAgentMail(proj); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--once", "--root", "~/Code"})
	})
	if err != nil {
		t.Fatalf("runNOC --root ~/Code: %v", err)
	}
	if !containsCLI(stdout, "NOC") {
		t.Errorf("noc --root ~/Code should render the NOC header, got:\n%s", stdout)
	}
}

func TestRunNOCRootValidatesDirectory(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--once", "--root", filepath.Join(t.TempDir(), "missing")})
	})
	if err == nil {
		t.Fatal("runNOC --root missing should fail")
	}
	if !containsCLI(err.Error(), "--root") {
		t.Fatalf("error should reference --root, got %v", err)
	}
}

func TestConsoleRootRoutesToNOC(t *testing.T) {
	// `console --root DIR` reaches the same multi-root NOC surface.
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := mkAgentMail(proj); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := captureOutput(t, func() error {
		return runConsole([]string{"--once", "--root", root})
	})
	if err != nil {
		t.Fatalf("console --root --once: %v", err)
	}
	if !containsCLI(stdout, "NOC") {
		t.Errorf("console --root should reach the NOC surface, got:\n%s", stdout)
	}
}

func TestConsoleRootForwardsNOCJSONFilter(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    filepath.Join(base, "issue-96"),
	})
	seedAgentRecord(t, base, "drive-fix", "qa", launch.Record{
		Binary:  "claude",
		Role:    "qa",
		Handle:  "qa",
		Session: "drive-fix",
		Root:    filepath.Join(base, "drive-fix"),
	})

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "filter", args: []string{"--root", root, "--json", "--filter", "session:issue-96"}},
		{name: "session", args: []string{"--root", root, "--json", "--session", "issue-96"}},
	} {
		stdout, _, err := captureOutput(t, func() error {
			return runConsole(tc.args)
		})
		if err != nil {
			t.Fatalf("console --root --json %s: %v", tc.name, err)
		}
		env := decodeJSONEnvelope[nocSnapshotEnvelopeData](t, stdout)
		if env.Kind != "noc_snapshot" {
			t.Errorf("kind = %q, want noc_snapshot", env.Kind)
		}
		if env.Data.Filter != "session:issue-96" {
			t.Errorf("filter = %q, want session:issue-96", env.Data.Filter)
		}
		if env.Data.ProjectCount != 1 || len(env.Data.Projects) != 1 {
			t.Fatalf("project count/list = %d/%d, want 1/1", env.Data.ProjectCount, len(env.Data.Projects))
		}
		sessions := env.Data.Projects[0].Sessions
		if len(sessions) != 1 || sessions[0].Name != "issue-96" {
			t.Fatalf("filtered sessions = %+v, want only issue-96", sessions)
		}
	}
}

func TestConsoleRootForwardsNOCActions(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    filepath.Join(base, "issue-96"),
	})

	stdout, _, err := captureOutput(t, func() error {
		return runConsole([]string{"--root", root, "--actions", "--json", "--session", "issue-96", "--action", "resume", "--mutating"})
	})
	if err != nil {
		t.Fatalf("console --root --actions --json: %v", err)
	}
	env := decodeJSONEnvelope[nocActionsEnvelopeData](t, stdout)
	if env.Kind != "noc_actions" {
		t.Fatalf("kind = %q, want noc_actions", env.Kind)
	}
	if !hasNOCActionID(env.Data.Actions, "session|"+proj+"|issue-96|action|resume") {
		t.Fatalf("console-root noc_actions missing session resume action: %+v", env.Data.Actions)
	}
	for _, action := range env.Data.Actions {
		if action.Name != "resume" || !action.Mutates {
			t.Fatalf("console-root action selectors leaked %+v", action)
		}
	}
}

func TestConsoleRootForwardsNOCActionsCommands(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    filepath.Join(base, "issue-96"),
	})

	stdout, _, err := captureOutput(t, func() error {
		return runConsole([]string{"--root", root, "--actions", "--session", "issue-96", "--action", "resume", "--commands"})
	})
	if err != nil {
		t.Fatalf("console --root --actions --commands: %v", err)
	}
	if strings.Contains(stdout, "ID") || !strings.Contains(stdout, "amq-squad resume --project "+proj) {
		t.Fatalf("console-root commands-only output mismatch:\n%s", stdout)
	}
}

func TestConsoleRootForwardsNOCActionsExactSelectors(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	base := filepath.Join(proj, noc.AgentMailDirName)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:  "codex",
		Role:    "cto",
		Handle:  "cto",
		Session: "issue-96",
		Root:    filepath.Join(base, "issue-96"),
	})
	seedAgentRecord(t, base, "drive-fix", "qa", launch.Record{
		Binary:  "claude",
		Role:    "qa",
		Handle:  "qa",
		Session: "drive-fix",
		Root:    filepath.Join(base, "drive-fix"),
	})

	targetID := "session|" + proj + "|issue-96"
	stdout, _, err := captureOutput(t, func() error {
		return runConsole([]string{"--root", root, "--actions", "--json", "--target-id", targetID, "--scope", "session", "--mutating"})
	})
	if err != nil {
		t.Fatalf("console --root --actions exact selectors: %v", err)
	}
	env := decodeJSONEnvelope[nocActionsEnvelopeData](t, stdout)
	if env.Data.ActionCount == 0 {
		t.Fatal("expected selected console-root actions")
	}
	for _, action := range env.Data.Actions {
		if action.TargetID != targetID || action.Scope != "session" || !action.Mutates {
			t.Fatalf("console-root exact selectors leaked action: %+v", action)
		}
	}
}

func TestConsoleRootForwardsNOCRunAction(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := mkAgentMail(proj); err != nil {
		t.Fatal(err)
	}
	prevRunner := nocActionRunnerOverride
	var ran string
	nocActionRunnerOverride = func(command string) error {
		ran = command
		return nil
	}
	t.Cleanup(func() { nocActionRunnerOverride = prevRunner })

	stdout, _, err := captureOutput(t, func() error {
		return runConsole([]string{"--root", root, "--run-action", "project|" + proj + "|action|status"})
	})
	if err != nil {
		t.Fatalf("console --root --run-action: %v", err)
	}
	if !strings.Contains(ran, "amq-squad status --project "+proj) {
		t.Fatalf("console-root run-action executed %q, want project status", ran)
	}
	if !strings.Contains(stdout, "NOC action: project|"+proj+"|action|status") {
		t.Fatalf("console-root run-action preview missing action id:\n%s", stdout)
	}
}

func TestConsoleRootForwardsNOCRunActionDryRunJSON(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := team.Write(proj, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto"}},
	}); err != nil {
		t.Fatal(err)
	}
	prevRunner := nocActionRunnerOverride
	nocActionRunnerOverride = func(string) error {
		t.Fatal("console-root dry-run action should not execute")
		return nil
	}
	t.Cleanup(func() { nocActionRunnerOverride = prevRunner })

	stdout, _, err := captureOutput(t, func() error {
		return runConsole([]string{
			"--root", root,
			"--filter", "project:p",
			"--run-action", "new_session",
			"--set", "session=issue-100",
			"--dry-run",
			"--json",
		})
	})
	if err != nil {
		t.Fatalf("console --root --run-action --dry-run --json: %v", err)
	}
	env := decodeJSONEnvelope[nocActionPlanEnvelopeData](t, stdout)
	if env.Kind != "noc_action_plan" || env.Data.Action.Name != "new_session" || !env.Data.DryRun {
		t.Fatalf("console-root action plan = kind:%q data:%+v", env.Kind, env.Data)
	}
}

func TestConsoleRootExpandsHome(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "Code")
	proj := filepath.Join(root, "p")
	if err := mkAgentMail(proj); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	stdout, _, err := captureOutput(t, func() error {
		return runConsole([]string{"--once", "--root", "~/Code"})
	})
	if err != nil {
		t.Fatalf("console --root ~/Code --once: %v", err)
	}
	if !containsCLI(stdout, "NOC") {
		t.Errorf("console --root ~/Code should reach the NOC surface, got:\n%s", stdout)
	}
}

func mkAgentMail(projectDir string) error {
	return os.MkdirAll(filepath.Join(projectDir, noc.AgentMailDirName, "main", "agents"), 0o755)
}

func containsCLI(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func requireNOCAction(t *testing.T, actions []nocActionJSONData, name string) nocActionJSONData {
	t.Helper()
	for _, a := range actions {
		if a.Name == name {
			return a
		}
	}
	t.Fatalf("missing action %q in %+v", name, actions)
	return nocActionJSONData{}
}

func requireNOCActionVar(t *testing.T, vars []nocActionVariableData, name string) nocActionVariableData {
	t.Helper()
	for _, v := range vars {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("missing action var %q in %+v", name, vars)
	return nocActionVariableData{}
}

func hasNOCActionID(actions []nocActionJSONData, id string) bool {
	for _, action := range actions {
		if action.ID == id {
			return true
		}
	}
	return false
}

func seedNOCOperatorTeam(t *testing.T, projectDir string) {
	t.Helper()
	dir := filepath.Join(projectDir, noc.SquadDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"schema":3,"operator":{"enabled":true,"handle":"user","runnable":false},"capabilities":{"operator_gates":true}}`
	if err := os.WriteFile(filepath.Join(dir, "team.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func seedNOCNeedsYouMessage(t *testing.T, agentDir, from, thread, subject string) {
	t.Helper()
	dir := filepath.Join(agentDir, "inbox", "new")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	id := strings.ReplaceAll(thread, "/", "_")
	msg := "---json\n" +
		`{"schema":1,"id":"` + id + `","thread":"` + thread + `","from":"` + from + `","to":["user"],"kind":"question","subject":"` + subject + `","created":"2026-05-29T12:00:00Z"}` + "\n" +
		"---\n" +
		"body\n"
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(msg), 0o600); err != nil {
		t.Fatal(err)
	}
}
