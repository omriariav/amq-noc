package noc

// runtime.go consumes amq-squad's status JSON contract: the per-member tmux
// runtime/actions introduced in v1.5 and the namespace/visible-lead metadata
// added for v2.9. The NOC renders the exact commands and identities amq-squad
// advertises instead of inferring tmux or profile/session state itself.
// Pure data + READ-ONLY exec; no rendering.

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
)

// RuntimeAction is one runnable control command amq-squad advertises for a
// session or member. Command is copy-ready (already scoped with
// --project/--session/--role). Available is amq-squad's own gate: focus/send
// are only available while the member's pane is alive.
type RuntimeAction struct {
	Kind              string
	Label             string
	Scope             string
	NamespaceID       string
	Command           string
	Mutates           bool
	NeedsConfirmation bool
	Available         bool
	Reason            string
}

// RuntimeMember is one member's runtime view as reported by amq-squad.
type RuntimeMember struct {
	Role        string
	Handle      string
	Status      string
	RecordState string
	IsLead      bool
	External    bool
	Namespace   NamespaceRef
	Root        string
	AgentDir    string
	Session     string // tmux session hosting the pane (#{session_name})
	WindowID    string // tmux window id (@N)
	WindowName  string // tmux window name (#{window_name})
	PaneID      string // tmux pane id (%N) — the authoritative control address
	PaneAlive   bool
	Actions     []RuntimeAction
}

// JumpTarget builds the focus target for this member from the runtime contract:
// the authoritative pane id, the tmux session, and a reconstructed iTerm2 title
// token (amq:<workstream>:<role>) matching what amq-squad stamps — so the
// cross-session focus path can raise the right native tab without scraping.
// ok=false when the member has no live pane (caller falls back to scraping).
func (m RuntimeMember) JumpTarget(workstream, role string) (TmuxTarget, bool) {
	if strings.TrimSpace(m.PaneID) == "" || !m.PaneAlive {
		return TmuxTarget{}, false
	}
	key := strings.TrimSpace(role)
	if key == "" {
		key = strings.TrimSpace(m.Role)
	}
	if key == "" {
		key = strings.TrimSpace(m.Handle)
	}
	title := ""
	if w := strings.TrimSpace(workstream); w != "" && key != "" {
		title = "amq:" + w + ":" + key
	}
	return TmuxTarget{
		Session:    m.Session,
		PaneID:     m.PaneID,
		WindowID:   m.WindowID,
		Title:      title,
		WindowName: m.WindowName,
	}, true
}

// RuntimeStatus is the consumed runtime contract for one session. Advertised
// reports whether amq-squad declared capabilities.runtime_actions (true on
// builds that set it; absent on v1.5.0, where Members still carry actions).
type RuntimeStatus struct {
	Advertised     bool
	TeamHome       string
	Workstream     string
	Profile        string
	Namespace      NamespaceRef
	GoalBinding    GoalBinding
	Orchestrated   bool
	Lead           string
	LeadHandle     string
	Topology       *RuntimeTopology
	SessionActions []RuntimeAction
	Members        []RuntimeMember
}

type RuntimeTopology struct {
	Mode           string   `json:"mode,omitempty"`
	TmuxSessions   []string `json:"tmux_sessions,omitempty"`
	LivePanes      int      `json:"live_panes,omitempty"`
	LiveWindows    int      `json:"live_windows,omitempty"`
	VisibleProblem bool     `json:"visible_problem,omitempty"`
	ProblemFor     string   `json:"problem_for,omitempty"`
	Detail         string   `json:"detail,omitempty"`
}

// HasActions reports whether any member carries runtime actions — the robust
// gate for showing runtime commands, since v1.5.0 ships actions[] without yet
// advertising the capability flag (that lands in v1.5.1).
func (rs RuntimeStatus) HasActions() bool {
	if len(rs.SessionActions) > 0 {
		return true
	}
	for _, m := range rs.Members {
		if len(m.Actions) > 0 {
			return true
		}
	}
	return false
}

func (rs RuntimeStatus) HasStatusMetadata() bool {
	return strings.TrimSpace(rs.Namespace.ID) != "" ||
		strings.TrimSpace(rs.TeamHome) != "" ||
		strings.TrimSpace(rs.GoalBinding.Mode) != "" ||
		rs.Orchestrated
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
		TeamHome     string       `json:"team_home"`
		Workstream   string       `json:"workstream"`
		Profile      string       `json:"profile"`
		Namespace    NamespaceRef `json:"namespace"`
		GoalBinding  GoalBinding  `json:"goal_binding"`
		Orchestrated bool         `json:"orchestrated"`
		Lead         string       `json:"lead"`
		LeadHandle   string       `json:"lead_handle"`
		Capabilities struct {
			RuntimeActions bool `json:"runtime_actions"`
		} `json:"capabilities"`
		Topology *RuntimeTopology        `json:"topology"`
		Actions  []runtimeActionEnvelope `json:"actions"`
		Records  []struct {
			Role        string       `json:"role"`
			Handle      string       `json:"handle"`
			Status      string       `json:"status"`
			RecordState string       `json:"record_state"`
			IsLead      bool         `json:"is_lead"`
			External    bool         `json:"external"`
			Namespace   NamespaceRef `json:"namespace"`
			Root        string       `json:"root"`
			AgentDir    string       `json:"agent_dir"`
			Tmux        *struct {
				Session    string `json:"session"`
				WindowID   string `json:"window_id"`
				WindowName string `json:"window_name"`
				PaneID     string `json:"pane_id"`
				PaneAlive  bool   `json:"pane_alive"`
			} `json:"tmux"`
			Actions []runtimeActionEnvelope `json:"actions"`
		} `json:"records"`
	} `json:"data"`
}

type runtimeActionEnvelope struct {
	Kind              string `json:"kind"`
	Label             string `json:"label"`
	Scope             string `json:"scope"`
	NamespaceID       string `json:"namespace_id"`
	Command           string `json:"command"`
	Mutates           bool   `json:"mutates"`
	NeedsConfirmation bool   `json:"needs_confirmation"`
	Available         bool   `json:"available"`
	Reason            string `json:"reason"`
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
	rs.TeamHome = strings.TrimSpace(env.Data.TeamHome)
	rs.Workstream = strings.TrimSpace(env.Data.Workstream)
	rs.Profile = strings.TrimSpace(env.Data.Profile)
	rs.Namespace = env.Data.Namespace
	rs.GoalBinding = env.Data.GoalBinding
	rs.Orchestrated = env.Data.Orchestrated
	rs.Lead = strings.TrimSpace(env.Data.Lead)
	rs.LeadHandle = strings.TrimSpace(env.Data.LeadHandle)
	rs.Topology = env.Data.Topology
	for _, a := range env.Data.Actions {
		if action, ok := parseRuntimeAction(a); ok {
			rs.SessionActions = append(rs.SessionActions, action)
		}
	}
	for _, r := range env.Data.Records {
		m := RuntimeMember{
			Role:        r.Role,
			Handle:      r.Handle,
			Status:      r.Status,
			RecordState: r.RecordState,
			IsLead:      r.IsLead,
			External:    r.External,
			Namespace:   r.Namespace,
			Root:        r.Root,
			AgentDir:    r.AgentDir,
		}
		if r.Tmux != nil {
			m.Session = r.Tmux.Session
			m.WindowID = r.Tmux.WindowID
			m.WindowName = r.Tmux.WindowName
			m.PaneID = r.Tmux.PaneID
			m.PaneAlive = r.Tmux.PaneAlive
		}
		for _, a := range r.Actions {
			if action, ok := parseRuntimeAction(a); ok {
				m.Actions = append(m.Actions, action)
			}
		}
		rs.Members = append(rs.Members, m)
	}
	// A status response that advertises neither the capability nor any actions is
	// a pre-v1.5 amq-squad with no runtime contract. Stay a zero RuntimeStatus
	// (totality) rather than returning members with empty Actions.
	if !rs.Advertised && !rs.HasActions() && !rs.HasStatusMetadata() {
		return RuntimeStatus{}
	}
	return rs
}

func parseRuntimeAction(a runtimeActionEnvelope) (RuntimeAction, bool) {
	if strings.TrimSpace(a.Command) == "" {
		return RuntimeAction{}, false
	}
	return RuntimeAction{
		Kind:              strings.TrimSpace(a.Kind),
		Label:             strings.TrimSpace(a.Label),
		Scope:             strings.TrimSpace(a.Scope),
		NamespaceID:       strings.TrimSpace(a.NamespaceID),
		Command:           a.Command,
		Mutates:           a.Mutates,
		NeedsConfirmation: a.NeedsConfirmation,
		Available:         a.Available,
		Reason:            strings.TrimSpace(a.Reason),
	}, true
}
