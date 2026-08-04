package noc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/omriariav/amq-noc/internal/state"
)

const (
	amqMinimumVersion         = "0.51.1"
	amqPreferredVersion       = ""
	amqSquadMinimumVersion    = "2.28.0"
	healthStatusOK            = "ok"
	healthStatusWarn          = "warn"
	healthStatusError         = "error"
	healthStatusUnavailable   = "unavailable"
	healthStatusCapabilityGap = "capability_gap"
)

// HealthCommandRunner is the command seam for read-only health snapshots.
type HealthCommandRunner func(dir string, env []string, name string, args ...string) ([]byte, error)

// DefaultHealthCommandRunner runs bounded read-only AMQ/amq-squad commands.
func DefaultHealthCommandRunner(dir string, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return stdout.Bytes(), fmt.Errorf("%s: %s", name, detail)
	}
	return stdout.Bytes(), nil
}

// ProjectHealth is the read-only local capability snapshot for one project.
type ProjectHealth struct {
	Status    string            `json:"status,omitempty"`
	Summary   string            `json:"summary,omitempty"`
	Toolchain []CapabilityCheck `json:"toolchain,omitempty"`
	Doctor    CommandHealth     `json:"doctor,omitempty"`
}

// SessionHealth is the read-only AMQ health snapshot for one session.
type SessionHealth struct {
	Status   string          `json:"status,omitempty"`
	Summary  string          `json:"summary,omitempty"`
	AMQEnv   CommandHealth   `json:"amq_env,omitempty"`
	Ops      CommandHealth   `json:"ops,omitempty"`
	Presence PresenceHealth  `json:"presence,omitempty"`
	Signals  []DerivedSignal `json:"signals,omitempty"`
}

type CapabilityCheck struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	Version   string `json:"version,omitempty"`
	Minimum   string `json:"minimum,omitempty"`
	Preferred string `json:"preferred,omitempty"`
	Detail    string `json:"detail,omitempty"`
	Error     string `json:"error,omitempty"`
}

type CommandHealth struct {
	Status  string `json:"status,omitempty"`
	Command string `json:"command,omitempty"`
	Detail  string `json:"detail,omitempty"`
	Error   string `json:"error,omitempty"`
}

type PresenceHealth struct {
	Status        string          `json:"status,omitempty"`
	HumanOperator bool            `json:"human_operator,omitempty"`
	HumanHandle   string          `json:"human_handle,omitempty"`
	Agents        int             `json:"agents,omitempty"`
	Entries       []PresenceEntry `json:"entries,omitempty"`
	Error         string          `json:"error,omitempty"`
}

type PresenceEntry struct {
	Handle   string `json:"handle"`
	Status   string `json:"status"`
	SeenAt   string `json:"seen_at,omitempty"`
	IsHuman  bool   `json:"is_human,omitempty"`
	IsWorker bool   `json:"is_worker,omitempty"`
}

type DerivedSignal struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// EnrichHealth adds read-only capability and AMQ health snapshots to an already
// collected multi-project snapshot. It is best-effort: command failures are
// returned as health fields, not collection failures.
func EnrichHealth(ms *MultiSnapshot, run HealthCommandRunner) {
	if ms == nil {
		return
	}
	if run == nil {
		run = DefaultHealthCommandRunner
	}
	for i := range ms.Projects {
		ps := &ms.Projects[i]
		if !ps.TeamConfigured && !ps.SessionStore {
			continue
		}
		ps.Health = FetchProjectHealth(run, *ps)
		if len(ps.Snap.Sessions) == 0 {
			continue
		}
		ps.SessionHealth = map[string]SessionHealth{}
		for _, sess := range ps.Snap.Sessions {
			if strings.TrimSpace(sess.Root) == "" {
				continue
			}
			ps.SessionHealth[sess.Name] = FetchSessionHealth(run, ps.Dir, sess, ps.OperatorGateHandle())
		}
	}
}

func FetchProjectHealth(run HealthCommandRunner, ps ProjectSnapshot) ProjectHealth {
	h := ProjectHealth{Status: healthStatusOK}
	amq := fetchVersionCapability(run, ps.Dir, "amq", []string{"version"}, "amq", amqMinimumVersion, amqPreferredVersion)
	squad := fetchVersionCapability(run, ps.Dir, "amq-squad", []string{"version"}, "amq-squad", amqSquadMinimumVersion, "")
	h.Toolchain = []CapabilityCheck{amq, squad}
	h.Doctor = fetchSquadDoctor(run, ps.Dir)
	h.Status, h.Summary = summarizeProjectHealth(h.Toolchain, h.Doctor)
	return h
}

func FetchSessionHealth(run HealthCommandRunner, dir string, sess state.Session, operatorHandle string) SessionHealth {
	root := strings.TrimSpace(sess.Root)
	env := []string{"AM_ROOT=" + root}
	h := SessionHealth{
		AMQEnv: fetchCommandHealth(run, dir, env, "amq env --export", "amq", "env", "--export"),
		Ops:    fetchCommandHealth(run, dir, env, "env AM_ROOT="+root+" amq doctor --ops", "amq", "doctor", "--ops"),
	}
	if h.Ops.Status == healthStatusOK {
		h.Ops.Detail = summarizeDoctorOps(h.Ops.Detail)
	}
	h.Presence = fetchPresence(run, dir, root, operatorHandle)
	h.Signals = deriveSessionSignals(sess, h)
	h.Status, h.Summary = summarizeSessionHealth(h)
	return h
}

func fetchVersionCapability(run HealthCommandRunner, dir, name string, args []string, label, minimum, preferred string) CapabilityCheck {
	out, err := run(dir, nil, name, args...)
	check := CapabilityCheck{Name: label, Minimum: minimum, Preferred: preferred}
	if err != nil {
		check.Status = healthStatusUnavailable
		check.Error = err.Error()
		return check
	}
	version := parseVersionString(string(out))
	check.Version = version
	if version == "" {
		check.Status = healthStatusWarn
		check.Detail = "version output did not contain a semantic version"
		return check
	}
	if minimum != "" && compareVersions(version, minimum) < 0 {
		check.Status = healthStatusError
		check.Detail = "below required floor"
		return check
	}
	if preferred != "" && compareVersions(version, preferred) < 0 {
		check.Status = healthStatusWarn
		check.Detail = "below preferred release floor"
		return check
	}
	check.Status = healthStatusOK
	return check
}

func fetchSquadDoctor(run HealthCommandRunner, dir string) CommandHealth {
	ch := fetchCommandHealth(run, dir, nil, "amq-squad doctor --json", "amq-squad", "doctor", "--json")
	if ch.Status != healthStatusOK {
		return ch
	}
	var env struct {
		Kind string `json:"kind"`
		Data struct {
			Checks []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
				Detail string `json:"detail"`
			} `json:"checks"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(ch.Detail), &env); err != nil || env.Kind != "doctor" {
		ch.Status = healthStatusWarn
		ch.Detail = "doctor output was not the expected JSON envelope"
		return ch
	}
	var warn, fail int
	for _, c := range env.Data.Checks {
		switch strings.ToLower(strings.TrimSpace(c.Status)) {
		case "ok":
		case "warn":
			warn++
		default:
			fail++
		}
	}
	switch {
	case fail > 0:
		ch.Status = healthStatusError
	case warn > 0:
		ch.Status = healthStatusWarn
	default:
		ch.Status = healthStatusOK
	}
	ch.Detail = fmt.Sprintf("%d checks, %d warnings, %d failures", len(env.Data.Checks), warn, fail)
	return ch
}

func fetchCommandHealth(run HealthCommandRunner, dir string, env []string, command, name string, args ...string) CommandHealth {
	out, err := run(dir, env, name, args...)
	ch := CommandHealth{Command: command}
	if err != nil {
		ch.Status = healthStatusError
		ch.Error = err.Error()
		ch.Detail = strings.TrimSpace(string(out))
		return ch
	}
	ch.Status = healthStatusOK
	ch.Detail = strings.TrimSpace(string(out))
	return ch
}

func fetchPresence(run HealthCommandRunner, dir, root, operatorHandle string) PresenceHealth {
	out, err := run(dir, nil, "amq", "presence", "list", "--root", root)
	if err != nil {
		return PresenceHealth{Status: healthStatusError, Error: err.Error()}
	}
	entries := parsePresenceList(string(out), operatorHandle)
	ph := PresenceHealth{Status: healthStatusOK, Entries: entries}
	for _, e := range entries {
		if e.IsHuman {
			ph.HumanOperator = true
			ph.HumanHandle = e.Handle
			continue
		}
		if e.IsWorker {
			ph.Agents++
		}
	}
	if operatorHandle != "" && !ph.HumanOperator {
		ph.Status = healthStatusWarn
		ph.Error = "operator handle not reported as human presence"
	}
	return ph
}

func parsePresenceList(out, operatorHandle string) []PresenceEntry {
	lines := strings.Split(out, "\n")
	entries := []PresenceEntry{}
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		status := fields[1]
		entry := PresenceEntry{
			Handle:  fields[0],
			Status:  status,
			IsHuman: status == "human",
		}
		if len(fields) >= 3 {
			entry.SeenAt = fields[2]
		}
		if entry.IsHuman && operatorHandle != "" && entry.Handle != operatorHandle {
			entry.IsHuman = false
		}
		entry.IsWorker = !entry.IsHuman
		entries = append(entries, entry)
	}
	return entries
}

func deriveSessionSignals(sess state.Session, h SessionHealth) []DerivedSignal {
	signals := []DerivedSignal{}
	for _, ag := range sess.Agents {
		if ag.WakeHealth == state.WakeHealthMissing || ag.WakeHealth == state.WakeHealthStale {
			signals = append(signals, DerivedSignal{
				Name:   "wake_health",
				Status: healthStatusWarn,
				Detail: strings.TrimSpace(ag.Handle + " " + string(ag.WakeHealth)),
			})
		}
	}
	signals = append(signals,
		DerivedSignal{
			Name:   "queued_not_drained",
			Status: healthStatusCapabilityGap,
			Detail: "current snapshot exposes receipt/DLQ inspect actions but no explicit queue-age evidence",
		},
		DerivedSignal{
			Name:   "drained_without_progress",
			Status: healthStatusCapabilityGap,
			Detail: "current snapshot exposes receipt inspect actions but no progress acknowledgement contract",
		},
	)
	return signals
}

func summarizeProjectHealth(checks []CapabilityCheck, doctor CommandHealth) (string, string) {
	status := healthStatusOK
	for _, c := range checks {
		status = worseHealth(status, c.Status)
	}
	status = worseHealth(status, doctor.Status)
	return status, "local AMQ/amq-squad toolchain snapshot"
}

func summarizeSessionHealth(h SessionHealth) (string, string) {
	status := healthStatusOK
	status = worseHealth(status, h.AMQEnv.Status)
	status = worseHealth(status, h.Ops.Status)
	status = worseHealth(status, h.Presence.Status)
	for _, s := range h.Signals {
		status = worseHealth(status, s.Status)
	}
	return status, "session AMQ environment, ops, presence, and derived health"
}

func worseHealth(a, b string) string {
	rank := map[string]int{
		"":                        0,
		healthStatusOK:            0,
		healthStatusCapabilityGap: 1,
		healthStatusWarn:          2,
		healthStatusUnavailable:   3,
		healthStatusError:         4,
	}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

func summarizeDoctorOps(out string) string {
	lines := strings.Split(out, "\n")
	keep := []string{}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Summary:") || strings.HasPrefix(trimmed, "operator gate:") || strings.HasPrefix(trimmed, "Root:") {
			keep = append(keep, trimmed)
		}
	}
	if len(keep) == 0 {
		return "amq doctor --ops completed"
	}
	return strings.Join(keep, "; ")
}

var versionRE = regexp.MustCompile(`v?([0-9]+(?:\.[0-9]+){1,2})`)

func parseVersionString(out string) string {
	match := versionRE.FindStringSubmatch(out)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func compareVersions(a, b string) int {
	av := versionParts(a)
	bv := versionParts(b)
	for i := 0; i < 3; i++ {
		if av[i] < bv[i] {
			return -1
		}
		if av[i] > bv[i] {
			return 1
		}
	}
	return 0
}

func versionParts(v string) [3]int {
	var out [3]int
	parts := strings.Split(v, ".")
	for i := 0; i < len(parts) && i < 3; i++ {
		n, _ := strconv.Atoi(parts[i])
		out[i] = n
	}
	return out
}
