package cli

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/omriariav/amq-noc/internal/launch"
	"github.com/omriariav/amq-noc/internal/role"
	"github.com/omriariav/amq-noc/internal/rules"
	"github.com/omriariav/amq-noc/internal/team"
)

//go:embed bootstrap.md
var defaultBootstrapTemplate string

type bootstrapContext struct {
	Role          string
	Handle        string
	Binary        string
	Session       string
	CWD           string
	Root          string
	AgentDir      string
	TeamHome      string
	TeamRulesPath string
	RolePath      string
	LaunchPath    string
	BriefPath     string
	CurrentTeam   []bootstrapTeamMember
	Workstreams   []bootstrapWorkstream
	Warnings      []string
}

type bootstrapTeamMember struct {
	Role       string
	Handle     string
	Binary     string
	Session    string
	Project    string
	CWD        string
	Route      string
	RouteError string
	You        bool
}

func buildBootstrapPrompt(ctx bootstrapContext) (string, error) {
	ctx = sanitizeBootstrapContext(ctx)
	tpl, err := template.New("bootstrap").Funcs(template.FuncMap{
		"orDefault": func(s, fallback string) string {
			if s == "" {
				return fallback
			}
			return s
		},
	}).Parse(defaultBootstrapTemplate)
	if err != nil {
		return "", fmt.Errorf("parse bootstrap template: %w", err)
	}
	var b bytes.Buffer
	if err := tpl.Execute(&b, ctx); err != nil {
		return "", fmt.Errorf("render bootstrap template: %w", err)
	}
	return b.String(), nil
}

func sanitizeBootstrapContext(ctx bootstrapContext) bootstrapContext {
	ctx.Role = promptText(ctx.Role)
	ctx.Handle = promptText(ctx.Handle)
	ctx.Binary = promptText(ctx.Binary)
	ctx.Session = promptText(ctx.Session)
	ctx.CWD = promptText(ctx.CWD)
	ctx.Root = promptText(ctx.Root)
	ctx.AgentDir = promptText(ctx.AgentDir)
	ctx.TeamHome = promptText(ctx.TeamHome)
	ctx.TeamRulesPath = promptText(ctx.TeamRulesPath)
	ctx.RolePath = promptText(ctx.RolePath)
	ctx.LaunchPath = promptText(ctx.LaunchPath)
	ctx.BriefPath = promptText(ctx.BriefPath)
	for i := range ctx.CurrentTeam {
		m := &ctx.CurrentTeam[i]
		m.Role = promptText(m.Role)
		m.Handle = promptText(m.Handle)
		m.Binary = promptText(m.Binary)
		m.Session = promptText(m.Session)
		m.Project = promptText(m.Project)
		m.CWD = promptText(m.CWD)
		m.Route = promptText(m.Route)
		m.RouteError = promptText(m.RouteError)
	}
	for i := range ctx.Workstreams {
		w := &ctx.Workstreams[i]
		w.Name = promptText(w.Name)
		w.Handles = promptText(w.Handles)
		w.LastTouched = promptText(w.LastTouched)
	}
	for i := range ctx.Warnings {
		ctx.Warnings[i] = promptText(ctx.Warnings[i])
	}
	return ctx
}

func promptText(s string) string {
	// Keep prompt data single-line and out of inline-code delimiters; team.Read
	// already rejects control characters in persisted team fields.
	var b strings.Builder
	lastSpace := false
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == '`' {
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		b.WriteRune(r)
		lastSpace = false
	}
	return strings.TrimSpace(b.String())
}

func bootstrapContextFor(rec launch.Record, agentDir, teamHome string) bootstrapContext {
	teamRulesPath := ""
	if teamHome != "" {
		teamRulesPath = rules.Path(teamHome)
	} else if _, err := os.Stat(rules.Path(rec.CWD)); err == nil {
		teamRulesPath = rules.Path(rec.CWD)
	}
	currentTeam, warnings := bootstrapCurrentTeam(rec, teamHome)
	return bootstrapContext{
		Role:          rec.Role,
		Handle:        rec.Handle,
		Binary:        rec.Binary,
		Session:       rec.Session,
		CWD:           rec.CWD,
		Root:          rec.Root,
		AgentDir:      agentDir,
		TeamHome:      teamHome,
		TeamRulesPath: teamRulesPath,
		RolePath:      role.ExistingPath(agentDir),
		LaunchPath:    launch.ExistingPath(agentDir),
		// Brief resolution uses the same rule as the live-launch ensure
		// step so bootstrap can never name a path that ensure skipped (or
		// vice versa).
		BriefPath:   briefPath(resolveBriefHome(teamHome, rec.CWD), rec.Session),
		CurrentTeam: currentTeam,
		Workstreams: siblingWorkstreamSummaries(rec.Root, rec.Session),
		Warnings:    warnings,
	}
}

func bootstrapCurrentTeam(rec launch.Record, teamHome string) ([]bootstrapTeamMember, []string) {
	home := teamHome
	if home == "" {
		home = rec.CWD
	}
	t, err := team.ReadProfile(home, rec.TeamProfile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []string{fmt.Sprintf("current team routing unavailable: %v", err)}
	}
	if len(t.Members) == 0 {
		return nil, nil
	}

	currentProject := projectIdentityForCWD(rec.CWD)
	out := make([]bootstrapTeamMember, 0, len(t.Members))
	for _, m := range t.Members {
		cwd := m.EffectiveCWD(t.Project)
		project := projectIdentityForCWD(cwd)
		handle := memberHandle(m)
		session := routingSessionForMember(rec, m)
		route, routeError := routeCommandFor(rec.Root, rec.Session, currentProject, project, samePath(rec.CWD, cwd), rec.Handle, handle, session)
		out = append(out, bootstrapTeamMember{
			Role:       m.Role,
			Handle:     handle,
			Binary:     m.Binary,
			Session:    session,
			Project:    project.DisplayName(),
			CWD:        cwd,
			Route:      route,
			RouteError: routeError,
			You:        sameLaunchTarget(rec, cwd, handle, m),
		})
	}
	return out, nil
}

func routingSessionForMember(rec launch.Record, m team.Member) string {
	if rec.SharedWorkstream || (rec.Session != "" && rec.Session != rec.Role && rec.Session != rec.Handle) {
		return rec.Session
	}
	return m.Session
}

func memberHandle(m team.Member) string {
	if m.Handle != "" {
		return m.Handle
	}
	if m.Role != "" {
		return m.Role
	}
	return m.Binary
}

func sameLaunchTarget(rec launch.Record, cwd, handle string, m team.Member) bool {
	return rec.Role == m.Role &&
		rec.Handle == handle &&
		rec.Session == routingSessionForMember(rec, m) &&
		rec.CWD == cwd
}

type projectIdentity struct {
	Name  string
	Dir   string
	Known bool
}

func (p projectIdentity) DisplayName() string {
	if p.Name != "" {
		return p.Name
	}
	return "(unknown)"
}

func routeCommandFor(sourceRoot, sourceSession string, currentProject, targetProject projectIdentity, sameCWD bool, fromHandle, handle, session string) (string, string) {
	if !sameCWD && (!currentProject.Known || !targetProject.Known) {
		return "", "AMQ project identity is missing; add .amqrc project names or use amq route manually"
	}
	if !sameCWD && currentProject.Name == targetProject.Name && !samePath(currentProject.Dir, targetProject.Dir) {
		return "", "AMQ project identity is ambiguous; matching project names come from different .amqrc roots"
	}
	if sourceRoot != "" && fromHandle != "" && handle != "" {
		if route, routeError, ok := routeExplainCommand(sourceRoot, sourceSession, currentProject, targetProject, sameCWD, fromHandle, handle, session); ok {
			return route, routeError
		}
	}
	args := []string{"amq", "send", "--to", handle}
	if !sameCWD && currentProject.Name != targetProject.Name {
		args = append(args, "--project", targetProject.Name)
	}
	if session != "" {
		args = append(args, "--session", session)
	}
	if fromHandle != "" && handle != "" && fromHandle != handle {
		args = append(args, "--thread", canonicalP2PThread(fromHandle, handle))
	}
	for i, arg := range args {
		args[i] = shellQuote(arg)
	}
	return strings.Join(args, " "), ""
}

type routeExplainResult struct {
	Routable       bool     `json:"routable"`
	Argv           []string `json:"argv"`
	DisplayCommand string   `json:"display_command"`
	Error          string   `json:"error"`
}

func routeExplainCommand(sourceRoot, sourceSession string, currentProject, targetProject projectIdentity, sameCWD bool, fromHandle, handle, session string) (string, string, bool) {
	args := []string{"route", "explain", "--from-root", sourceRoot, "--me", fromHandle, "--to", handle, "--json"}
	if !sameCWD && currentProject.Name != targetProject.Name && targetProject.Name != "" {
		args = append(args, "--project", targetProject.Name)
	}
	if session != "" && session != sourceSession {
		args = append(args, "--session", session)
	}
	cmd := exec.Command("amq", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", "", false
	}
	var parsed routeExplainResult
	if err := json.Unmarshal(out, &parsed); err != nil {
		return "", "", false
	}
	if !parsed.Routable {
		if parsed.Error == "" {
			parsed.Error = "AMQ route explain reported route is not routable"
		}
		return "", parsed.Error, true
	}
	if len(parsed.Argv) == 0 {
		return "", "AMQ route explain returned empty argv", true
	}
	argv := append([]string(nil), parsed.Argv...)
	if fromHandle != "" && handle != "" && fromHandle != handle {
		argv = append(argv, "--thread", canonicalP2PThread(fromHandle, handle))
	}
	return shellCommand(argv[0], argv[1:]...), "", true
}

func projectIdentityForCWD(cwd string) projectIdentity {
	if cwd == "" {
		return projectIdentity{}
	}
	if env, err := resolveAMQEnvInDir(cwd, "", "", "amq-squad"); err == nil && env.Project != "" {
		dir := env.BaseRoot
		if dir == "" {
			dir = env.Root
		}
		return projectIdentity{Name: env.Project, Dir: dir, Known: true}
	}
	if dir, name := findProjectName(cwd); name != "" {
		return projectIdentity{Name: name, Dir: dir, Known: true}
	}
	return projectIdentity{}
}

func samePath(a, b string) bool {
	aa, err := filepath.Abs(a)
	if err != nil {
		aa = filepath.Clean(a)
	}
	bb, err := filepath.Abs(b)
	if err != nil {
		bb = filepath.Clean(b)
	}
	return aa == bb
}

func findProjectName(start string) (string, string) {
	dir, err := filepath.Abs(start)
	if err != nil {
		dir = start
	}
	for {
		path := filepath.Join(dir, ".amqrc")
		if b, err := os.ReadFile(path); err == nil {
			var cfg struct {
				Project string `json:"project"`
			}
			if json.Unmarshal(b, &cfg) == nil && cfg.Project != "" {
				return dir, cfg.Project
			}
			return dir, ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ""
		}
		dir = parent
	}
}
