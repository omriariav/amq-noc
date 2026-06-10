// Package cli: retained helpers from the removed copied amq-squad lifecycle
// commands (the 0.1.0 split). The command surfaces themselves (team, new, up,
// agent, launch, restore, brief, fork, rm, down, and friends) were deleted as
// dead code; the symbols below are the pieces the live NOC path (noc.go,
// noc_runtime_actions.go, status/doctor/threads/resume support files) still
// uses. Names, signatures, and bodies are unchanged from their source files.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-noc/internal/catalog"
	"github.com/omriariav/amq-noc/internal/launch"
	"github.com/omriariav/amq-noc/internal/team"
)

const defaultThreadTranscriptLimit = 20

type teamLaunchOptions struct {
	Terminal        string
	Target          string
	Layout          string
	Workstream      string
	TerminalSession string
	Fresh           bool
	NoBootstrap     bool
	Stagger         time.Duration
	DryRun          bool
	SquadBin        string
	BinaryArgs      map[string][]string
	Trust           string
	ModelOverrides  map[string]string
	ForceDuplicate  bool
	// SeedBriefContent, when non-empty, is the rendered active brief that
	// the live launch path should write to .amq-squad/briefs/<workstream>.md
	// AFTER all team-launch validations and preflight pass. Empty means no
	// seeded brief was requested for this run. SeedBriefForce permits
	// overwriting an existing brief.
	SeedBriefContent string
	SeedBriefForce   bool
	// Profile is the named team profile this launch represents. Empty means
	// the implicit default profile. Propagated to emitted launch commands
	// via --team-profile so each agent's launch record carries the same
	// profile identity for bootstrap routing and status display.
	Profile string
	// WarnStubBrief, when true, makes the live launch emit a warn-if-stub
	// notice on stderr (silenced by --quiet) after a successful launch when
	// the brief is an untouched generated stub. `up` sets this when no brief
	// source (--seed-from) was supplied so CI / send-keys flows keep working
	// without a hard error, while nudging the operator to fill in the goal.
	WarnStubBrief bool
}

type teamLaunchPane struct {
	Role    string
	CWD     string
	Command string
}

type teamLaunchBackend interface {
	Name() string
	Validate(teamLaunchOptions) error
	DryRun(team.Team, teamLaunchOptions) error
	Launch(team.Team, teamLaunchOptions) error
}

// Terminal support is intentionally backend-based. A new terminal integration
// should live in its own team_launch_<name>.go file and call
// registerTeamLaunchBackend from init.
var teamLaunchBackends = map[string]teamLaunchBackend{}

func registerTeamLaunchBackend(backend teamLaunchBackend) {
	name := backend.Name()
	if name == "" {
		panic("team launch backend has empty name")
	}
	if _, exists := teamLaunchBackends[name]; exists {
		panic("duplicate team launch backend: " + name)
	}
	teamLaunchBackends[name] = backend
}

func registeredTeamLaunchTerminals() []string {
	names := make([]string, 0, len(teamLaunchBackends))
	for name := range teamLaunchBackends {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func buildTeamLaunchPanes(t team.Team, opts teamLaunchOptions) []teamLaunchPane {
	members := orderedTeamMembers(t.Members)
	binaryArgs := mergeBinaryArgs(t.BinaryArgs, opts.BinaryArgs)
	panes := make([]teamLaunchPane, 0, len(members))
	for _, m := range members {
		cwd := m.EffectiveCWD(t.Project)
		panes = append(panes, teamLaunchPane{
			Role: m.Role,
			CWD:  cwd,
			Command: emitTeamCommand(emitTeamCommandInput{
				CWD:            cwd,
				SquadBin:       opts.SquadBin,
				TeamHome:       t.Project,
				Member:         m,
				NoBootstrap:    opts.NoBootstrap,
				Workstream:     opts.Workstream,
				BinaryArgs:     binaryArgs,
				TrustMode:      opts.Trust,
				Model:          memberEffectiveModel(m, opts.ModelOverrides),
				ForceDuplicate: opts.ForceDuplicate,
				Profile:        opts.Profile,
			}),
		})
	}
	return panes
}

func teamSquadBin() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return "amq-squad"
}

func resolveTeamTrustMode(t team.Team, requested string, explicit bool) (string, error) {
	if explicit {
		return normalizeTrustMode(requested)
	}
	if strings.TrimSpace(t.Trust) != "" {
		return normalizeTrustMode(t.Trust)
	}
	return trustModeSandboxed, nil
}

func memberEffectiveModel(m team.Member, overrides map[string]string) string {
	if v, ok := overrides[strings.ToLower(m.Role)]; ok {
		return strings.TrimSpace(v)
	}
	return strings.TrimSpace(m.Model)
}

// validateModelOverrideKeys rejects --model role=model entries whose role is
// not one of the known roles. Silent drops on typos are a DX trap; an error
// makes the mistake visible.
func validateModelOverrideKeys(overrides map[string]string, known map[string]bool) error {
	if len(overrides) == 0 {
		return nil
	}
	var unknown []string
	for k := range overrides {
		if !known[strings.ToLower(strings.TrimSpace(k))] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("--model has unknown role(s): %s", strings.Join(unknown, ", "))
}

func lowercaseKeys(m map[string]string) map[string]string {
	if len(m) == 0 {
		return m
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[strings.ToLower(strings.TrimSpace(k))] = v
	}
	return out
}

func orderedTeamMembers(members []team.Member) []team.Member {
	idx := make(map[string]int, len(catalog.IDs()))
	for i, id := range catalog.IDs() {
		idx[id] = i
	}
	out := append([]team.Member(nil), members...)
	sort.SliceStable(out, func(i, j int) bool {
		left, lok := idx[out[i].Role]
		right, rok := idx[out[j].Role]
		if !lok && !rok {
			return out[i].Role < out[j].Role
		}
		if !lok {
			return false
		}
		if !rok {
			return true
		}
		return left < right
	})
	return out
}

func uniqueMemberCWDs(projectDir string, members []team.Member) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, m := range members {
		cwd := m.EffectiveCWD(projectDir)
		if seen[cwd] {
			continue
		}
		seen[cwd] = true
		out = append(out, cwd)
	}
	sort.Strings(out)
	return out
}

type emitTeamCommandInput struct {
	CWD            string
	SquadBin       string
	TeamHome       string
	Member         team.Member
	NoBootstrap    bool
	Workstream     string
	BinaryArgs     map[string][]string
	TrustMode      string
	Model          string
	ForceDuplicate bool
	Profile        string
}

func emitTeamCommand(in emitTeamCommandInput) string {
	m := in.Member
	var b strings.Builder
	b.WriteString("cd ")
	b.WriteString(shellQuote(in.CWD))
	b.WriteString(" && ")
	b.WriteString(shellQuote(in.SquadBin))
	// Emit the modern single-agent surface: `agent up <binary> [flags] [-- child]`.
	// Legacy `launch <binary>` still works with a deprecation warning, but
	// generated team commands recommend the 1.0 shape.
	b.WriteString(" agent up ")
	b.WriteString(shellQuote(m.Binary))
	b.WriteString(" --role ")
	b.WriteString(shellQuote(m.Role))
	b.WriteString(" --session ")
	b.WriteString(shellQuote(in.Workstream))
	b.WriteString(" --team-workstream")
	if in.TrustMode != "" {
		b.WriteString(" --trust ")
		b.WriteString(shellQuote(in.TrustMode))
	}
	if in.Model != "" {
		b.WriteString(" --model ")
		b.WriteString(shellQuote(in.Model))
	}
	if in.TeamHome != "" {
		b.WriteString(" --team-home ")
		b.WriteString(shellQuote(in.TeamHome))
	}
	if in.Profile != "" && in.Profile != team.DefaultProfile {
		b.WriteString(" --team-profile ")
		b.WriteString(shellQuote(in.Profile))
	}
	if in.NoBootstrap {
		b.WriteString(" --no-bootstrap")
	}
	if in.ForceDuplicate {
		b.WriteString(" --force-duplicate")
	}
	if m.Handle != "" {
		// Always explicit: a role-named handle avoids collisions when the
		// same binary (e.g. codex) hosts multiple roles in one project.
		b.WriteString(" --me ")
		b.WriteString(shellQuote(m.Handle))
	}
	if m.Launcher != "" {
		b.WriteString(" --launcher ")
		b.WriteString(shellQuote(m.Launcher))
		if len(m.LauncherArgs) > 0 {
			b.WriteString(" --launcher-args=")
			b.WriteString(shellQuote(joinedAgentArgs(m.LauncherArgs)))
		}
	}
	extraDefaultArgs := binaryArgsFor(m.Binary, in.BinaryArgs)
	if len(extraDefaultArgs) > 0 {
		switch normalizedAgentBinary(m.Binary) {
		case "codex":
			b.WriteString(" --codex-args=")
			b.WriteString(shellQuote(joinedAgentArgs(extraDefaultArgs)))
		case "claude":
			b.WriteString(" --claude-args=")
			b.WriteString(shellQuote(joinedAgentArgs(extraDefaultArgs)))
		}
	}
	modelArgs := modelArgsForBinary(m.Binary, in.Model)
	if defaultArgs := launchDefaultChildArgsWithTrust(m.Binary, true, modelArgs, extraDefaultArgs, in.TrustMode); len(defaultArgs) > 0 {
		b.WriteString(" --")
		for _, arg := range defaultArgs {
			b.WriteString(" ")
			b.WriteString(shellQuote(arg))
		}
	}
	return b.String()
}

// expandPath resolves a user-supplied path: expands a leading "~" or "~/"
// to the user's home directory, then makes the result absolute.
func expandPath(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("home dir: %w", err)
		}
		if p == "~" {
			p = home
		} else {
			p = filepath.Join(home, p[2:])
		}
	}
	return filepath.Abs(p)
}

func parseKV(s string) (map[string]string, error) {
	out := map[string]string{}
	if s == "" {
		return out, nil
	}
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		eq := strings.IndexByte(pair, '=')
		if eq <= 0 || eq == len(pair)-1 {
			return nil, fmt.Errorf("expected key=value, got %q", pair)
		}
		k := strings.TrimSpace(pair[:eq])
		v := strings.TrimSpace(pair[eq+1:])
		out[k] = v
	}
	return out, nil
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func syncTargetDirs(projectDir string, members []team.Member, allowOutside bool) ([]string, error) {
	home, err := canonicalDir(projectDir)
	if err != nil {
		return nil, fmt.Errorf("resolve team-home: %w", err)
	}
	targets := uniqueMemberCWDs(home, members)
	if len(targets) == 0 {
		targets = []string{home}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(targets))
	for _, raw := range targets {
		dir, err := canonicalDir(raw)
		if err != nil {
			return nil, fmt.Errorf("resolve sync target %s: %w", raw, err)
		}
		if !allowOutside && !pathWithin(home, dir) {
			return nil, fmt.Errorf("sync target %s is outside team-home %s; pass --allow-outside to write there", dir, home)
		}
		if !seen[dir] {
			seen[dir] = true
			out = append(out, dir)
		}
	}
	sort.Strings(out)
	return out, nil
}

func ensureTeamHomeSyncTarget(targetDirs []string, projectDir string) ([]string, error) {
	homeDir, err := canonicalDir(projectDir)
	if err != nil {
		return nil, fmt.Errorf("resolve team-home: %w", err)
	}
	if containsString(targetDirs, homeDir) {
		return targetDirs, nil
	}
	out := append([]string(nil), targetDirs...)
	out = append(out, homeDir)
	sort.Strings(out)
	return out, nil
}

func canonicalDir(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", abs)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func parseNewBoolFlag(name, raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "t", "true", "yes", "y", "on":
		return true, nil
	case "0", "f", "false", "no", "n", "off":
		return false, nil
	default:
		return false, usageErrorf("%s expects a boolean value", name)
	}
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

func stripConversationRestoreArgs(binary string, childArgs []string, conversation string) []string {
	conversation = strings.TrimSpace(conversation)
	if conversation == "" {
		return append([]string(nil), childArgs...)
	}
	switch normalizedAgentBinary(binary) {
	case "codex":
		return stripCodexResumeRef(childArgs, conversation)
	case "claude":
		return stripClaudeResumeRef(childArgs, conversation)
	default:
		return append([]string(nil), childArgs...)
	}
}

func normalizedAgentBinary(binary string) string {
	return strings.ToLower(filepath.Base(binary))
}

func stripCodexResumeRef(args []string, conversation string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "resume" && i+1 < len(args) && args[i+1] == conversation {
			i++
			continue
		}
		out = append(out, args[i])
	}
	return out
}

func stripClaudeResumeRef(args []string, conversation string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if (arg == "--resume" || arg == "-r" || arg == "--session-id") && i+1 < len(args) && args[i+1] == conversation {
			i++
			continue
		}
		if arg == "--resume="+conversation || arg == "--session-id="+conversation {
			continue
		}
		out = append(out, arg)
	}
	return out
}

func resolveAMQRootInDir(cwd, rootFlag, session, handle string) (string, error) {
	env, err := resolveAMQEnvInDir(cwd, rootFlag, session, handle)
	if err != nil {
		return "", err
	}
	return env.Root, nil
}

// profileInitCommand returns the creation command to suggest when reporting a
// missing team profile.
func profileInitCommand(profile string) string {
	if profile == "" || profile == team.DefaultProfile {
		return "amq-squad new team"
	}
	return "amq-squad new profile " + profile
}

// briefEnvelopeData is the kind="brief" payload.
type briefEnvelopeData struct {
	ProjectDir string `json:"project_dir"`
	Session    string `json:"session"`
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	Exists     bool   `json:"exists"`
	Content    string `json:"content,omitempty"`
}

// briefSeedEnvelopeData is the kind="brief_seed" payload.
type briefSeedEnvelopeData struct {
	ProjectDir  string `json:"project_dir"`
	Session     string `json:"session"`
	Path        string `json:"path"`
	Source      string `json:"source"`
	GeneratedAt string `json:"generated_at"`
	Generator   string `json:"generator"`
	Force       bool   `json:"force,omitempty"`
	DryRun      bool   `json:"dry_run,omitempty"`
	Written     bool   `json:"written,omitempty"`
	Content     string `json:"content"`
}

func readBriefData(projectDir, session string) (briefEnvelopeData, error) {
	projectDir = strings.TrimSpace(projectDir)
	session = strings.TrimSpace(session)
	if projectDir == "" {
		return briefEnvelopeData{}, fmt.Errorf("project dir cannot be empty")
	}
	if session == "" {
		return briefEnvelopeData{}, fmt.Errorf("session cannot be empty")
	}
	if err := validateWorkstreamName(session); err != nil {
		return briefEnvelopeData{}, fmt.Errorf("invalid session: %w", err)
	}
	path := briefPath(projectDir, session)
	data := briefEnvelopeData{
		ProjectDir: projectDir,
		Session:    session,
		Path:       path,
		Kind:       briefKindString(briefNone),
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return data, nil
		}
		return briefEnvelopeData{}, fmt.Errorf("read brief %s: %w", path, err)
	}
	_, kind := classifyBrief(projectDir, session)
	data.Kind = briefKindString(kind)
	data.Exists = true
	data.Content = string(content)
	return data, nil
}

func seedBriefData(projectDir, session, source string, force, dryRun bool) (briefSeedEnvelopeData, error) {
	projectDir = strings.TrimSpace(projectDir)
	session = strings.TrimSpace(session)
	source = strings.TrimSpace(source)
	if projectDir == "" {
		return briefSeedEnvelopeData{}, fmt.Errorf("project dir cannot be empty")
	}
	if session == "" {
		return briefSeedEnvelopeData{}, fmt.Errorf("session cannot be empty")
	}
	if source == "" {
		return briefSeedEnvelopeData{}, fmt.Errorf("seed source cannot be empty")
	}
	if err := validateWorkstreamName(session); err != nil {
		return briefSeedEnvelopeData{}, fmt.Errorf("invalid session: %w", err)
	}
	body, err := resolveSeed(source)
	if err != nil {
		return briefSeedEnvelopeData{}, err
	}
	now := seedNow()
	content := buildSeedBrief(source, body, now)
	path := briefPath(projectDir, session)
	data := briefSeedEnvelopeData{
		ProjectDir:  projectDir,
		Session:     session,
		Path:        path,
		Source:      source,
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Generator:   "deterministic",
		Force:       force,
		DryRun:      dryRun,
		Content:     content,
	}
	if dryRun {
		return data, nil
	}
	writtenPath, err := writeSeedBrief(projectDir, session, content, force)
	if err != nil {
		return briefSeedEnvelopeData{}, err
	}
	data.Path = writtenPath
	data.Written = true
	return data, nil
}

func briefKindString(kind briefKind) string {
	switch kind {
	case briefStub:
		return "stub"
	case briefReal:
		return "real"
	default:
		return "none"
	}
}

// briefsDirName is the per-team-home directory holding workstream briefs.
const briefsDirName = "briefs"

// briefPath returns the absolute path to the brief for a (teamHome, session)
// pair. teamHome is normalized to an absolute path so callers passing a
// relative argument (e.g. "." from a current-cwd fallback) still produce
// the absolute path bootstrap names in the priming prompt. Returns "" when
// either input is empty.
func briefPath(teamHome, session string) string {
	teamHome = strings.TrimSpace(teamHome)
	session = strings.TrimSpace(session)
	if teamHome == "" || session == "" {
		return ""
	}
	abs, err := filepath.Abs(teamHome)
	if err != nil {
		abs = filepath.Clean(teamHome)
	}
	return filepath.Join(abs, ".amq-squad", briefsDirName, session+".md")
}

// briefStubFirstLine is the first meaningful (non-heading, non-blank) line of
// the generated stub template. It is session-independent prose, so the status
// board can recognize an untouched stub by matching the brief's first
// meaningful line against it. Kept beside briefStubContent so the two never
// drift: briefStubContent emits this exact line right after the "# <session>"
// heading.
const briefStubFirstLine = "Use this brief to capture the active workstream's goal, scope, and"

// forkSourceHasState reports whether SOURCE looks like a workstream worth
// forking from. Either the AMQ root for SOURCE already exists, or at least
// one configured member has a restorable launch record matching SOURCE. No
// message bodies are inspected. This is the same "exists-or-restorable"
// condition `up` refuses by default; both share
// teamWorkstreamExistsOrRestorable so the two can never drift.
func forkSourceHasState(t team.Team, source string) bool {
	exists, _, err := teamWorkstreamExistsOrRestorable(t, source)
	return err == nil && exists
}

// resolveProfileFlag normalizes a --profile value: empty or "default" maps
// to the implicit default profile; non-default names are validated against
// the slug rules. Returns the canonical profile name and any error.
func resolveProfileFlag(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == team.DefaultProfile {
		return team.DefaultProfile, nil
	}
	if err := team.ValidateProfileName(name); err != nil {
		return "", fmt.Errorf("--profile: %w", err)
	}
	return name, nil
}

const (
	trustModeSandboxed = "sandboxed"
	trustModeTrusted   = "trusted"
)

var codexTrustedArgs = []string{"--dangerously-bypass-approvals-and-sandbox"}

func normalizeTrustMode(mode string) (string, error) {
	switch mode {
	case "", trustModeSandboxed:
		return trustModeSandboxed, nil
	case trustModeTrusted:
		return trustModeTrusted, nil
	default:
		return "", usageErrorf("invalid trust mode %q: use sandboxed or trusted", mode)
	}
}

func defaultChildArgsForBinaryWithTrust(binary, trustMode string) []string {
	switch defaultHandleFor(binary) {
	case "codex":
		if trustMode == trustModeTrusted {
			return append([]string(nil), codexTrustedArgs...)
		}
		return nil
	case "claude":
		return []string{"--permission-mode", "auto"}
	default:
		return nil
	}
}

func launchDefaultChildArgsWithTrust(binary string, includeBuiltIn bool, modelArgs, extraArgs []string, trustMode string) []string {
	out := []string{}
	if includeBuiltIn {
		out = append(out, defaultChildArgsForBinaryWithTrust(binary, trustMode)...)
	}
	out = append(out, modelArgs...)
	out = append(out, extraArgs...)
	return out
}

// validateTrustCombination rejects user input that contradicts the trust mode.
// trusted plus --no-default-args is incoherent: trust would prepend the bypass
// flag while no-default-args asks to omit defaults. sandboxed plus a manually
// supplied bypass via --codex-args is also rejected to keep the trust boundary
// the single, visible source of truth.
func validateTrustCombination(trustMode string, trustExplicit, noDefaultArgs bool, binaryArgs map[string][]string) error {
	if trustMode == trustModeTrusted && noDefaultArgs {
		return usageErrorf("--trust trusted cannot be combined with --no-default-args; trusted prepends the Codex permission flag, --no-default-args opts out of defaults")
	}
	if trustMode != trustModeTrusted {
		for _, arg := range binaryArgs["codex"] {
			if arg == "--dangerously-bypass-approvals-and-sandbox" {
				if trustExplicit {
					return usageErrorf("--trust sandboxed cannot be combined with --codex-args containing --dangerously-bypass-approvals-and-sandbox; pass --trust trusted instead")
				}
				return usageErrorf("--codex-args contains --dangerously-bypass-approvals-and-sandbox; pass --trust trusted instead so the trust boundary is explicit")
			}
		}
	}
	return nil
}

// modelArgsForBinary returns the native model-selection flag for the binary.
// codex and claude both accept --model <name>; unknown binaries get nothing.
func modelArgsForBinary(binary, model string) []string {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil
	}
	switch normalizedAgentBinary(binary) {
	case "codex", "claude":
		return []string{"--model", model}
	default:
		return nil
	}
}

func matchesRestoreFilters(rec launch.Record, roleFilter, handleFilter, sessionFilter, conversationFilter string) bool {
	if roleFilter != "" && rec.Role != roleFilter {
		return false
	}
	if handleFilter != "" && rec.Handle != handleFilter {
		return false
	}
	if sessionFilter != "" && rec.Session != sessionFilter {
		return false
	}
	if conversationFilter != "" && rec.Conversation != conversationFilter {
		return false
	}
	return true
}

func sourceLabel(source string) string {
	switch source {
	case launch.FileName:
		return "amq-squad"
	case "amq history":
		return "amq"
	case "":
		return "(unknown)"
	default:
		return source
	}
}

func restoreArgvFromRecord(rec launch.Record) []string {
	argv := append([]string(nil), rec.Argv...)
	if rec.Conversation != "" {
		argv = stripConversationRestoreArgs(rec.Binary, argv, rec.Conversation)
	}
	if extras := launchExtraBinaryArgs(rec); len(extras) > 0 {
		argv = removeContiguousSubsequence(argv, extras)
	}
	if model := strings.TrimSpace(rec.Model); model != "" {
		argv = removeContiguousSubsequence(argv, []string{"--model", model})
	}
	if !rec.NoDefaultArgs {
		trust := trustModeFromRecord(rec)
		if defaults := defaultChildArgsForBinaryWithTrust(rec.Binary, trust); len(defaults) > 0 {
			argv = removeContiguousSubsequence(argv, defaults)
		}
	}
	return argv
}

func launchExtraBinaryArgs(rec launch.Record) []string {
	switch normalizedAgentBinary(rec.Binary) {
	case "codex":
		return rec.CodexArgs
	case "claude":
		return rec.ClaudeArgs
	}
	return nil
}

// trustModeFromRecord returns the trust mode to re-emit on restore. If the
// record has Trust set, it wins. Otherwise legacy codex records that contain
// the bypass arg in argv (and did not opt out of defaults) are restored as
// trusted; everything else is sandboxed.
func trustModeFromRecord(rec launch.Record) string {
	if t, err := normalizeTrustMode(rec.Trust); err == nil && rec.Trust != "" {
		return t
	}
	if normalizedAgentBinary(rec.Binary) != "codex" {
		return ""
	}
	if !rec.NoDefaultArgs && argvContainsBypass(rec.Argv) {
		return trustModeTrusted
	}
	return trustModeSandboxed
}

func argvContainsBypass(argv []string) bool {
	for _, a := range argv {
		if a == "--dangerously-bypass-approvals-and-sandbox" {
			return true
		}
	}
	return false
}

func removeContiguousSubsequence(args, sub []string) []string {
	if len(sub) == 0 || len(args) < len(sub) {
		return args
	}
	for i := 0; i+len(sub) <= len(args); i++ {
		match := true
		for j := range sub {
			if args[i+j] != sub[j] {
				match = false
				break
			}
		}
		if match {
			out := make([]string, 0, len(args)-len(sub))
			out = append(out, args[:i]...)
			out = append(out, args[i+len(sub):]...)
			return out
		}
	}
	return args
}

// emitCommandOptions controls extra flags injected into the emitted
// 'amq-squad agent up' invocation. Force adds --force-duplicate so a
// planner (e.g. resume) can emit a command that matches the plan when a
// live agent has been overridden. NoBootstrap lets an operator force the
// emitted command to skip bootstrap even for a seat that would otherwise
// re-orient (a record with no saved conversation).
type emitCommandOptions struct {
	Force       bool
	NoBootstrap bool
}

func emitCommandWithOptions(rec launch.Record, opts emitCommandOptions) string {
	var b strings.Builder
	b.WriteString("cd ")
	b.WriteString(shellQuote(rec.CWD))
	// Modern surface: `agent up <binary> [launch flags] [-- child args]`.
	// Binary positional sits immediately after `agent up` so the printed
	// command reads as the documented 1.0 shape.
	b.WriteString(" && ")
	b.WriteString(shellQuote(generatedSquadCommand()))
	b.WriteString(" agent up ")
	b.WriteString(shellQuote(rec.Binary))
	// --no-bootstrap is emitted only for a true reattach (a record carries a
	// saved conversation, so re-running bootstrap would clobber the resumed
	// thread) or when the operator explicitly asked to skip bootstrap. A seat
	// with no saved conversation -- the common resume case -- must RE-RUN
	// bootstrap so the agent re-orients from its brief and drains AMQ history
	// instead of coming up blank.
	if opts.NoBootstrap || rec.Conversation != "" {
		b.WriteString(" --no-bootstrap")
	}
	if opts.Force {
		b.WriteString(" --force-duplicate")
	}
	if rec.Role != "" {
		b.WriteString(" --role ")
		b.WriteString(shellQuote(rec.Role))
	}
	// amq treats --session NAME as shorthand for --root .agent-mail/<name>,
	// so passing both is rejected by `amq env`. Emit one or the other.
	if rec.Session != "" {
		b.WriteString(" --session ")
		b.WriteString(shellQuote(rec.Session))
	} else if rec.Root != "" {
		b.WriteString(" --root ")
		b.WriteString(shellQuote(rec.Root))
	}
	if rec.SharedWorkstream {
		b.WriteString(" --team-workstream")
	}
	if rec.Conversation != "" {
		b.WriteString(" --conversation ")
		b.WriteString(shellQuote(rec.Conversation))
	}
	if rec.NoDefaultArgs {
		b.WriteString(" --no-default-args")
	}
	if trust := trustModeFromRecord(rec); trust != "" {
		b.WriteString(" --trust ")
		b.WriteString(shellQuote(trust))
	}
	if model := strings.TrimSpace(rec.Model); model != "" {
		b.WriteString(" --model ")
		b.WriteString(shellQuote(model))
	}
	if len(rec.CodexArgs) > 0 {
		b.WriteString(" --codex-args=")
		b.WriteString(shellQuote(joinedAgentArgs(rec.CodexArgs)))
	}
	if len(rec.ClaudeArgs) > 0 {
		b.WriteString(" --claude-args=")
		b.WriteString(shellQuote(joinedAgentArgs(rec.ClaudeArgs)))
	}
	if rec.Launcher != "" {
		b.WriteString(" --launcher ")
		b.WriteString(shellQuote(rec.Launcher))
		if len(rec.LauncherArgs) > 0 {
			b.WriteString(" --launcher-args=")
			b.WriteString(shellQuote(joinedAgentArgs(rec.LauncherArgs)))
		}
	}
	if rec.Handle != "" && rec.Handle != defaultHandleFor(rec.Binary) {
		b.WriteString(" --me ")
		b.WriteString(shellQuote(rec.Handle))
	}
	if profile := strings.TrimSpace(rec.TeamProfile); profile != "" && profile != team.DefaultProfile {
		b.WriteString(" --team-profile ")
		b.WriteString(shellQuote(profile))
	}
	argv := restoreArgvFromRecord(rec)
	if len(argv) > 0 {
		b.WriteString(" --")
		for _, a := range argv {
			b.WriteString(" ")
			b.WriteString(shellQuote(a))
		}
	}
	return b.String()
}

func defaultHandleFor(binary string) string {
	return strings.ToLower(filepath.Base(binary))
}

// shellQuote wraps a string in single quotes for safe shell pasting.
// If the string has no special chars, returns it as-is.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !(r == '/' || r == '.' || r == '-' || r == '_' || r == '=' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellCommand(bin string, args ...string) string {
	if bin == "amq-squad" {
		bin = generatedSquadCommand()
	}
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shellQuote(bin))
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

var generatedSquadCommandOverride string

// generatedSquadCommand resolves the binary that delegated squad
// lifecycle/config commands render and execute as. amq-noc owns visibility and
// orchestration but delegates squad mutations to the installed amq-squad CLI,
// so this is "amq-squad" (resolved on PATH at exec time), never the running
// amq-noc executable, whose public surface is only noc/version. Tests redirect
// both previews and execution through generatedSquadCommandOverride.
func generatedSquadCommand() string {
	if generatedSquadCommandOverride != "" {
		return generatedSquadCommandOverride
	}
	return "amq-squad"
}
