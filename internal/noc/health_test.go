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
			return []byte("0.38.0\n"), nil
		case "amq-squad version":
			return []byte("amq-squad v2.5.0\n"), nil
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
	if len(h.Toolchain) != 2 || h.Toolchain[0].Version != "0.38.0" || h.Toolchain[1].Version != "2.5.0" {
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
