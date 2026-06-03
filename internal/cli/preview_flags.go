package cli

import (
	"flag"
	"fmt"
	"time"
)

// previewFlags is the shared flag surface for printing a team's launch-command
// plan. Both `team show` and `up --dry-run` register it so the two preview
// entry points cannot drift.
type previewFlags struct {
	noBootstrap    *bool
	session        *string
	fresh          *bool
	trustRaw       *string
	model          *string
	codexArgsRaw   *string
	claudeArgsRaw  *string
	forceDuplicate *bool
}

func registerPreviewFlags(fs *flag.FlagSet) *previewFlags {
	return &previewFlags{
		noBootstrap:    fs.Bool("no-bootstrap", false, "emit launch commands that skip the generated bootstrap prompt"),
		session:        fs.String("session", "", "AMQ workstream session name (default: sanitized team-home directory name; lowercase a-z, 0-9, -, _)"),
		fresh:          fs.Bool("fresh", false, "fail if the selected workstream session already exists"),
		trustRaw:       fs.String("trust", "", "Codex trust profile for this run: sandboxed or trusted"),
		model:          fs.String("model", "", "per-persona model overrides for this run, e.g. cto=gpt-5,fullstack=sonnet"),
		codexArgsRaw:   fs.String("codex-args", "", "extra Codex args for this run, e.g. '--enable goals'"),
		claudeArgsRaw:  fs.String("claude-args", "", "extra Claude args for this run, e.g. '--chrome'"),
		forceDuplicate: fs.Bool("force-duplicate", false, "include --force-duplicate in emitted launch commands"),
	}
}

func (p *previewFlags) toEmitOptions(fs *flag.FlagSet) (emitTeamOptions, error) {
	trustMode, err := normalizeTrustMode(*p.trustRaw)
	if err != nil {
		return emitTeamOptions{}, err
	}
	modelOverrides, err := parseKV(*p.model)
	if err != nil {
		return emitTeamOptions{}, fmt.Errorf("parse --model: %w", err)
	}
	modelOverrides = lowercaseKeys(modelOverrides)
	binaryArgs, err := parseBinaryArgFlags(*p.codexArgsRaw, *p.claudeArgsRaw)
	if err != nil {
		return emitTeamOptions{}, err
	}
	return emitTeamOptions{
		NoBootstrap:      *p.noBootstrap,
		RequestedSession: *p.session,
		ExplicitSession:  flagWasSet(fs, "session"),
		Fresh:            *p.fresh,
		ExtraBinaryArgs:  binaryArgs,
		RequestedTrust:   trustMode,
		ExplicitTrust:    flagWasSet(fs, "trust"),
		ModelOverrides:   modelOverrides,
		ForceDuplicate:   *p.forceDuplicate,
	}, nil
}

// liveLaunchFlags is the backend-specific flag surface shared by `team launch`
// and live `up`. It does not include --dry-run: each command owns its own
// dry-run flag because the two semantics differ (terminal-plan dry-run for
// team launch, launch-command preview for up).
type liveLaunchFlags struct {
	terminal        *string
	target          *string
	layout          *string
	terminalSession *string
	stagger         *time.Duration
	// noAttach is parsed for compatibility but has no behavioral effect.
	noAttach *bool
}

func registerLiveLaunchFlags(fs *flag.FlagSet) *liveLaunchFlags {
	return &liveLaunchFlags{
		terminal:        fs.String("terminal", "tmux", "terminal backend to use"),
		target:          fs.String("target", "current-window", "terminal target, backend-specific"),
		layout:          fs.String("layout", "vertical", "terminal layout, backend-specific"),
		terminalSession: fs.String("terminal-session", "", "terminal session name when the backend creates one"),
		stagger:         fs.Duration("stagger", 750*time.Millisecond, "delay between starting agent panes"),
		noAttach:        fs.Bool("no-attach", false, "legacy no-op; new-session never attaches automatically"),
	}
}

// buildLiveLaunchOptions composes a teamLaunchOptions from the shared preview
// and live flag sets. The caller fills in DryRun separately so the two
// callers can keep distinct dry-run semantics.
func buildLiveLaunchOptions(fs *flag.FlagSet, pf *previewFlags, lf *liveLaunchFlags) (teamLaunchOptions, error) {
	emit, err := pf.toEmitOptions(fs)
	if err != nil {
		return teamLaunchOptions{}, err
	}
	return teamLaunchOptions{
		Terminal:        *lf.terminal,
		Target:          *lf.target,
		Layout:          *lf.layout,
		Workstream:      emit.RequestedSession,
		TerminalSession: *lf.terminalSession,
		Fresh:           emit.Fresh,
		NoBootstrap:     emit.NoBootstrap,
		Stagger:         *lf.stagger,
		SquadBin:        teamSquadBin(),
		BinaryArgs:      emit.ExtraBinaryArgs,
		Trust:           emit.RequestedTrust,
		ModelOverrides:  emit.ModelOverrides,
		ForceDuplicate:  emit.ForceDuplicate,
	}, nil
}
