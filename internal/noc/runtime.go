package noc

// runtime.go consumes amq-squad's v1.5.0 tmux runtime contract — the per-member
// `tmux` block, `pane_alive`, and `actions[]` exposed by `amq-squad status
// --json` — so the NOC renders the exact, runnable control commands amq-squad
// advertises instead of inferring tmux state itself. This is the consumer half
// of amq-noc#6: amq-squad owns the execution/control contract; the NOC consumes
// it. Pure data + READ-ONLY exec; no rendering.

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
)

// RuntimeAction is one runnable control command amq-squad advertises for a
// member (kind: focus/send/resume/status). Command is copy-ready (already
// scoped with --project/--session/--role). Available is amq-squad's own gate:
// focus/send are only available while the member's pane is alive.
type RuntimeAction struct {
	Kind      string
	Command   string
	Available bool
}

// RuntimeMember is one member's runtime view as reported by amq-squad.
type RuntimeMember struct {
	Role      string
	Handle    string
	PaneID    string
	PaneAlive bool
	Actions   []RuntimeAction
}

// RuntimeStatus is the consumed runtime contract for one session. Advertised
// reports whether amq-squad declared capabilities.runtime_actions (true on
// builds that set it; absent on v1.5.0, where Members still carry actions).
type RuntimeStatus struct {
	Advertised bool
	Members    []RuntimeMember
}

// HasActions reports whether any member carries runtime actions — the robust
// gate for showing runtime commands, since v1.5.0 ships actions[] without yet
// advertising the capability flag (that lands in v1.5.1).
func (rs RuntimeStatus) HasActions() bool {
	for _, m := range rs.Members {
		if len(m.Actions) > 0 {
			return true
		}
	}
	return false
}

// MemberByRole returns the member with the given role (case-insensitive).
func (rs RuntimeStatus) MemberByRole(role string) (RuntimeMember, bool) {
	role = strings.ToLower(strings.TrimSpace(role))
	for _, m := range rs.Members {
		if strings.ToLower(strings.TrimSpace(m.Role)) == role {
			return m, true
		}
	}
	return RuntimeMember{}, false
}

// SquadRunner runs the amq-squad binary in dir and returns its stdout. It is the
// seam tests inject; production uses DefaultSquadRunner. READ-ONLY: only ever
// called with `status ... --json`.
type SquadRunner func(dir string, args ...string) ([]byte, error)

// DefaultSquadRunner shells `amq-squad` on PATH, matching how the rest of the
// NOC delegates to amq-squad. stderr is discarded; only stdout JSON is parsed.
func DefaultSquadRunner(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("amq-squad", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}

// the wire shape of `amq-squad status --json` (only the fields the NOC needs).
type squadStatusEnvelope struct {
	Kind string `json:"kind"`
	Data struct {
		Capabilities struct {
			RuntimeActions bool `json:"runtime_actions"`
		} `json:"capabilities"`
		Records []struct {
			Role   string `json:"role"`
			Handle string `json:"handle"`
			Tmux   *struct {
				PaneID    string `json:"pane_id"`
				PaneAlive bool   `json:"pane_alive"`
			} `json:"tmux"`
			Actions []struct {
				Kind      string `json:"kind"`
				Command   string `json:"command"`
				Available bool   `json:"available"`
			} `json:"actions"`
		} `json:"records"`
	} `json:"data"`
}

// FetchRuntimeStatus runs `amq-squad status --project DIR [--profile P] --session
// S --json` and parses the runtime contract. It is total: any failure (missing
// amq-squad, an older build whose JSON lacks the fields, a non-status envelope,
// malformed output) yields a zero RuntimeStatus, so callers degrade gracefully
// to their static commands. dir/session are required; profile is optional
// (default profile passes "").
func FetchRuntimeStatus(run SquadRunner, dir, profile, session string) RuntimeStatus {
	if run == nil || strings.TrimSpace(dir) == "" || strings.TrimSpace(session) == "" {
		return RuntimeStatus{}
	}
	args := []string{"status", "--project", dir, "--session", session, "--json"}
	if p := strings.TrimSpace(profile); p != "" && p != "default" {
		args = append(args, "--profile", p)
	}
	out, err := run(dir, args...)
	if err != nil {
		return RuntimeStatus{}
	}
	return parseRuntimeStatus(out)
}

func parseRuntimeStatus(out []byte) RuntimeStatus {
	var env squadStatusEnvelope
	if err := json.Unmarshal(out, &env); err != nil {
		return RuntimeStatus{}
	}
	if env.Kind != "status" {
		return RuntimeStatus{}
	}
	rs := RuntimeStatus{Advertised: env.Data.Capabilities.RuntimeActions}
	for _, r := range env.Data.Records {
		m := RuntimeMember{Role: r.Role, Handle: r.Handle}
		if r.Tmux != nil {
			m.PaneID = r.Tmux.PaneID
			m.PaneAlive = r.Tmux.PaneAlive
		}
		for _, a := range r.Actions {
			if strings.TrimSpace(a.Command) == "" {
				continue
			}
			m.Actions = append(m.Actions, RuntimeAction{Kind: a.Kind, Command: a.Command, Available: a.Available})
		}
		rs.Members = append(rs.Members, m)
	}
	// A status response that advertises neither the capability nor any actions is
	// a pre-v1.5 amq-squad with no runtime contract. Stay a zero RuntimeStatus
	// (totality) rather than returning members with empty Actions.
	if !rs.Advertised && !rs.HasActions() {
		return RuntimeStatus{}
	}
	return rs
}
