package noc

import (
	"errors"
	"strings"
	"testing"

	"github.com/omriariav/amq-noc/internal/state"
)

func TestFetchProjectHealthChecksToolchainAndDoctor(t *testing.T) {
	run := func(dir string, env []string, name string, args ...string) ([]byte, error) {
		key := name + " " + strings.Join(args, " ")
		switch key {
		case "amq version":
			return []byte("0.51.1\n"), nil
		case "amq-squad version":
			return []byte("amq-squad v2.28.0\n"), nil
		case "amq-squad doctor --json":
			return []byte(`{"schema_version":1,"kind":"doctor","data":{"checks":[{"name":"amq","status":"ok"},{"name":"team-rules roster","status":"warn"}]}}`), nil
		default:
			t.Fatalf("unexpected command: %s", key)
			return nil, nil
		}
	}

	h := FetchProjectHealth(run, ProjectSnapshot{Dir: "/repo/app"})
	if h.Status != healthStatusWarn {
		t.Fatalf("status = %q, want warn: %+v", h.Status, h)
	}
	if len(h.Toolchain) != 2 || h.Toolchain[0].Version != "0.51.1" || h.Toolchain[1].Version != "2.28.0" {
		t.Fatalf("toolchain = %+v", h.Toolchain)
	}
	if h.Doctor.Status != healthStatusWarn || !strings.Contains(h.Doctor.Detail, "1 warnings") {
		t.Fatalf("doctor = %+v", h.Doctor)
	}
}

func TestFetchSessionHealthKeepsHumanOperatorOutOfAgentCount(t *testing.T) {
	run := func(dir string, env []string, name string, args ...string) ([]byte, error) {
		key := name + " " + strings.Join(args, " ")
		switch key {
		case "amq env --export":
			if len(env) != 1 || env[0] != "AM_ROOT=/repo/app/.agent-mail/s" {
				t.Fatalf("env = %+v", env)
			}
			return []byte("export AM_ROOT=/repo/app/.agent-mail/s\n"), nil
		case "amq doctor --ops":
			return []byte("Summary: 4 ok, 0 warnings\nOps:\n  Root: /repo/app/.agent-mail/s\n  operator gate: none\n"), nil
		case "amq presence list --root /repo/app/.agent-mail/s":
			return []byte("cto active 2026-06-22T20:00:00Z\nuser human\nqa offline 2026-06-22T19:59:00Z\n"), nil
		default:
			t.Fatalf("unexpected command: %s", key)
			return nil, nil
		}
	}

	h := FetchSessionHealth(run, "/repo/app", state.Session{
		Name: "s",
		Root: "/repo/app/.agent-mail/s",
		Agents: []state.Agent{{
			Handle:     "qa",
			WakeHealth: state.WakeHealthMissing,
		}},
	}, "user")
	if h.Status != healthStatusWarn {
		t.Fatalf("status = %q, want warn because wake health is missing: %+v", h.Status, h)
	}
	if !h.Presence.HumanOperator || h.Presence.HumanHandle != "user" || h.Presence.Agents != 2 {
		t.Fatalf("presence = %+v", h.Presence)
	}
	if len(h.Signals) != 3 || h.Signals[0].Name != "wake_health" {
		t.Fatalf("signals = %+v", h.Signals)
	}
	if !strings.Contains(h.Ops.Detail, "Summary: 4 ok") || !strings.Contains(h.Ops.Detail, "operator gate: none") {
		t.Fatalf("ops detail = %q", h.Ops.Detail)
	}
}

// TestFetchSquadDoctorParsesChecksOnNonZeroExit is the regression for
// addendum A5: amq-squad 2.28's `doctor --json` exits non-zero
// ("error: doctor: N check(s) failed") whenever any check fails, but still
// prints the full checks envelope on stdout (DefaultHealthCommandRunner
// returns stdout alongside the error, matching a real exec.Cmd). Before this
// fix, a non-zero exit short-circuited straight to an opaque error blob
// instead of parsing the real, available checks summary.
func TestFetchSquadDoctorParsesChecksOnNonZeroExit(t *testing.T) {
	doctorJSON := `{"schema_version":1,"kind":"doctor","data":{"checks":[` +
		`{"name":"amq version","status":"fail","detail":"amq env failed"},` +
		`{"name":"markers CLAUDE.md","status":"warn","detail":"CLAUDE.md not found"},` +
		`{"name":"tmux","status":"ok"}` +
		`]}}`
	run := func(dir string, env []string, name string, args ...string) ([]byte, error) {
		if name == "amq-squad" && len(args) == 2 && args[0] == "doctor" && args[1] == "--json" {
			return []byte(doctorJSON), errors.New("amq-squad: exit status 2: error: doctor: 1 check(s) failed")
		}
		t.Fatalf("unexpected command: %s %v", name, args)
		return nil, nil
	}

	ch := fetchSquadDoctor(run, "/repo/app")
	if ch.Status != healthStatusError {
		t.Fatalf("status = %q, want error (non-zero exit trusted over per-check tally)", ch.Status)
	}
	if !strings.Contains(ch.Detail, "3 checks, 1 warnings, 1 failures") {
		t.Fatalf("detail = %q, want the parsed checks summary, not a raw error blob", ch.Detail)
	}
	if ch.Error != "" {
		t.Fatalf("error = %q, want cleared once the checks envelope parsed", ch.Error)
	}
}

// TestFetchSquadDoctorFallsBackOnTransportFailure keeps the pre-A5 behavior
// for a genuine transport failure: no parseable doctor envelope on stdout at
// all (amq-squad missing, wrong PATH, etc), so the opaque error must survive.
func TestFetchSquadDoctorFallsBackOnTransportFailure(t *testing.T) {
	run := func(dir string, env []string, name string, args ...string) ([]byte, error) {
		return []byte("exec: \"amq-squad\": executable file not found in $PATH"), errors.New("amq-squad: executable file not found in $PATH")
	}
	ch := fetchSquadDoctor(run, "/repo/app")
	if ch.Status != healthStatusError {
		t.Fatalf("status = %q, want error", ch.Status)
	}
	if ch.Error == "" {
		t.Fatalf("error should be preserved for a genuine transport failure, got %+v", ch)
	}
}

func TestEnrichHealthIsBestEffort(t *testing.T) {
	run := func(dir string, env []string, name string, args ...string) ([]byte, error) {
		if name == "amq" && len(args) == 1 && args[0] == "version" {
			return []byte("0.36.0\n"), nil
		}
		return nil, errors.New("boom")
	}
	ms := MultiSnapshot{Projects: []ProjectSnapshot{{
		Dir:            "/repo/app",
		TeamConfigured: true,
		SessionStore:   true,
		Snap: state.Snapshot{Sessions: []state.Session{{
			Name: "s",
			Root: "/repo/app/.agent-mail/s",
		}}},
	}}}

	EnrichHealth(&ms, run)
	if got := ms.Projects[0].Health.Toolchain[0].Status; got != healthStatusError {
		t.Fatalf("amq status = %q, want error below floor", got)
	}
	if got := ms.Projects[0].Health.Toolchain[1].Status; got != healthStatusUnavailable {
		t.Fatalf("amq-squad status = %q, want unavailable", got)
	}
	if got := ms.Projects[0].SessionHealth["s"].AMQEnv.Status; got != healthStatusError {
		t.Fatalf("session amq_env status = %q, want command error", got)
	}
}
