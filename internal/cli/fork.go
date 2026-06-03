package cli

import (
	"flag"
	"fmt"
	"os"

	"github.com/omriariav/amq-noc/internal/team"
)

func runFork(args []string) error {
	fs := flag.NewFlagSet("fork", flag.ContinueOnError)
	from := fs.String("from", "", "source AMQ workstream session to fork from")
	as := fs.String("as", "", "target AMQ workstream session for fresh launches")
	forceDuplicate := fs.Bool("force-duplicate", false, "fork into an existing target workstream by overwriting its launch plan")
	projectFlag := fs.String("project", "", "project/team-home directory to fork (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to fork (default: default profile)")
	noBootstrap := fs.Bool("no-bootstrap", false, "emit fresh launch commands that skip the generated bootstrap prompt")
	trustRaw := fs.String("trust", "", "Codex trust profile for fresh members: sandboxed (default) or trusted")
	modelFlag := fs.String("model", "", "per-persona model overrides for fresh members, e.g. cto=gpt-5,fullstack=sonnet")
	codexArgsRaw := fs.String("codex-args", "", "extra Codex args for fresh members, e.g. '--enable goals'")
	claudeArgsRaw := fs.String("claude-args", "", "extra Claude args for fresh members, e.g. '--chrome'")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad fork - plan a fresh team in a new workstream branched off an existing one

Usage:
  amq-squad fork --from SOURCE --as TARGET [--project DIR] [--profile NAME]
                 [--force-duplicate] [--no-bootstrap]
                 [--trust sandboxed|trusted] [--model role=model,...]
                 [--codex-args args] [--claude-args args]

SOURCE must have local workstream state or restorable records for this
team. TARGET must not already exist unless --force-duplicate is passed.
Fork does not copy launch records, briefs, conversations, or team.json;
it plans fresh launches into TARGET using the current team intent.
--project targets another team-home without changing directories.

Examples:
  amq-squad fork --from issue-96 --as issue-96-review
  amq-squad fork --project ~/Code/app --from issue-96 --as issue-96-review
  amq-squad fork --from main --as experiment --no-bootstrap --trust trusted
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	if *from == "" || *as == "" {
		return usageErrorf("fork requires both --from SOURCE and --as TARGET")
	}
	if err := validateWorkstreamName(*from); err != nil {
		return usageErrorf("invalid --from: %s", err)
	}
	if err := validateWorkstreamName(*as); err != nil {
		return usageErrorf("invalid --as: %s", err)
	}
	if *from == *as {
		return usageErrorf("--from and --as must differ; got %q for both", *from)
	}

	profile, err := resolveProfileFlag(*profileFlag)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	projectDir, err := resolveProjectDirFlag(cwd, *projectFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	if !team.ExistsProfile(projectDir, profile) {
		return fmt.Errorf("no team configured for profile %q. Run '%s' first.", profile, profileInitCommand(profile))
	}
	t, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	if len(t.Members) == 0 {
		return fmt.Errorf("team has no members")
	}
	if !forkSourceHasState(t, *from) {
		return fmt.Errorf("--from %q: no local workstream state or restorable launch records for this team; nothing to fork from", *from)
	}

	return executeResume(resumeExecution{
		ProjectDir:       projectDir,
		RequestedSession: *as,
		ExplicitSession:  true,
		Mode:             resumeModeFresh,
		Force:            *forceDuplicate,
		NoBootstrap:      *noBootstrap,
		TrustRaw:         *trustRaw,
		ExplicitTrust:    flagWasSet(fs, "trust"),
		ModelRaw:         *modelFlag,
		CodexArgsRaw:     *codexArgsRaw,
		ClaudeArgsRaw:    *claudeArgsRaw,
		Profile:          profile,
		Style: resumePrinterStyle{
			Label:      "fork",
			FooterVerb: "up",
			ForkFrom:   *from,
			ForkTo:     *as,
		},
	})
}

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
