package cli

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/omriariav/amq-noc/internal/catalog"
	"github.com/omriariav/amq-noc/internal/launch"
	"github.com/omriariav/amq-noc/internal/role"
)

// runLaunch is the real single-agent launcher. The top-level `launch` verb
// was removed in 2.0; this body now backs `agent up` (via runAgentUp ->
// translateAgentUpArgs) and the replay path (execRestoreRecord). It is
// internal-only and carries no deprecation surface of its own.
func runLaunch(args []string) error {
	// Split at "--" so launcher flags aren't consumed by amq-squad's parser.
	squadArgs, childArgs := splitDashDash(args)

	fs := flag.NewFlagSet("launch", flag.ContinueOnError)
	roleFlag := fs.String("role", "", "role label for this agent (e.g. cpo, cto, dev, qa)")
	session := fs.String("session", "", "AMQ session name (passed through to coop exec)")
	sharedWorkstream := fs.Bool("team-workstream", false, "mark --session as the shared amq-squad team workstream")
	me := fs.String("me", "", "override the agent handle (defaults to binary basename)")
	rootFlag := fs.String("root", "", "override AMQ root directory")
	_ = fs.String("project", "", "project/team-home directory to launch from (default: cwd)")
	teamHome := fs.String("team-home", "", "team-home directory used to find .amq-squad/team-rules.md for bootstrap")
	teamProfile := fs.String("team-profile", "", "team profile this launch belongs to (default: default profile)")
	conversation := fs.String("conversation", "", "resume and store a Codex or Claude conversation name/id")
	conversationID := fs.String("conversation-id", "", "alias for --conversation")
	noBootstrap := fs.Bool("no-bootstrap", false, "do not pass the generated bootstrap prompt to the agent")
	noDefaultArgs := fs.Bool("no-default-args", false, "do not prepend Codex or Claude default permission args")
	trustRaw := fs.String("trust", "", "Codex trust profile: sandboxed (default) or trusted (local power mode)")
	model := fs.String("model", "", "native model name to pass to the agent binary, e.g. 'gpt-5' or 'sonnet'")
	codexArgsRaw := fs.String("codex-args", "", "extra Codex args to treat as launch defaults, e.g. '--enable goals'")
	claudeArgsRaw := fs.String("claude-args", "", "extra Claude args to treat as launch defaults, e.g. '--chrome'")
	forceDuplicate := fs.Bool("force-duplicate", false, "launch even when a live agent for the same handle/workstream is detected")
	dryRun := fs.Bool("dry-run", false, "print the coop exec command without executing")
	launcherRaw := fs.String("launcher", "", "custom launcher to exec instead of <binary> (still receives AMQ env/identity, bootstrap, and a launch record)")
	launcherArgsRaw := fs.String("launcher-args", "", "args passed to --launcher before the agent's child args; the launcher must forward trailing args to <binary>")

	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad agent up - launch an agent with role metadata

Usage:
  amq-squad agent up <binary> [--project DIR] [options] [-- <binary-flags>]

Options:
`)
		fs.PrintDefaults()
		fmt.Fprint(os.Stderr, `
The <binary> is the agent launcher (claude, codex, etc). Flags after "--"
are passed through to that binary via 'amq coop exec'.

Side effects before exec:
  1. Resolves AMQ env via 'amq env --json' for the target session.
  2. Refuses by default when a live agent for the same handle/workstream is
     already running. Override with --force-duplicate.
  3. Writes launch.json under the amq-squad extension namespace.
  4. Writes a role.md stub if one does not already exist.
  5. Prepends Claude default permission args, and prepends Codex permission
     args only when --trust trusted is set. --no-default-args opts out of all
     built-in defaults.
  6. Inserts --model <name> for codex or claude when --model is provided.
  7. Prepends --codex-args or --claude-args for the matching binary.
  8. Translates --conversation for Codex or Claude resume when provided.
  9. Adds a generated bootstrap prompt unless --no-bootstrap is set or
     non-default binary args were provided.
 10. Execs 'amq coop exec --session <session> <binary> -- <binary-flags>'.

With --dry-run, the resolved coop exec command is printed and amq-squad exits.
Disk state is untouched and no exec occurs.
--project targets another team-home without changing directories; launch records
and relative AMQ config resolution behave as if the command ran from DIR.

When --conversation generates resume args, do not pass additional child args
after "--". Use --codex-args or --claude-args for native flags that should
still combine with --conversation.

Examples:
  amq-squad agent up codex --role cto --session issue-96
  amq-squad agent up codex --project ~/Code/app --session issue-96
  amq-squad agent up codex --dry-run --no-bootstrap
`)
	}

	if err := parseFlags(fs, squadArgs); err != nil {
		return err
	}
	if flagWasSet(fs, "project") {
		project, rest, err := peelProjectFlag(args)
		if err != nil {
			return err
		}
		return runInProject(project, func() error { return runLaunch(rest) })
	}
	trustExplicit := flagWasSet(fs, "trust")
	trustMode, err := normalizeTrustMode(*trustRaw)
	if err != nil {
		return err
	}
	conversationRef, err := conversationRefFromFlags(*conversation, *conversationID)
	if err != nil {
		return err
	}
	binaryArgs, err := parseBinaryArgFlags(*codexArgsRaw, *claudeArgsRaw)
	if err != nil {
		return err
	}
	if err := validateTrustCombination(trustMode, trustExplicit, *noDefaultArgs, binaryArgs); err != nil {
		return err
	}
	launcher := strings.TrimSpace(*launcherRaw)
	var launcherArgs []string
	if strings.TrimSpace(*launcherArgsRaw) != "" {
		launcherArgs, err = parseAgentArgs(*launcherArgsRaw)
		if err != nil {
			return fmt.Errorf("parse --launcher-args: %w", err)
		}
	}
	if launcher == "" && len(launcherArgs) > 0 {
		return usageErrorf("--launcher-args requires --launcher")
	}
	remaining := fs.Args()
	if len(remaining) == 0 {
		return usageErrorf("agent up requires a binary (e.g. 'amq-squad agent up codex --role cpo')")
	}
	binary := remaining[0]
	// Positional args before "--" get folded into childArgs.
	if len(remaining) > 1 {
		childArgs = append(remaining[1:], childArgs...)
	}
	extraDefaultArgs := binaryArgsFor(binary, binaryArgs)
	modelArgs := modelArgsForBinary(binary, *model)
	defaultArgs := launchDefaultChildArgsWithTrust(binary, !*noDefaultArgs, modelArgs, extraDefaultArgs, trustMode)
	childArgs = ensureLeadingChildArgs(defaultArgs, childArgs)
	if conversationRef != "" {
		var err error
		childArgs, err = applyConversationRestoreArgsWithDefaults(binary, childArgs, conversationRef, defaultArgs)
		if err != nil {
			return err
		}
	}

	handle := *me
	if handle == "" {
		handle = strings.ToLower(filepath.Base(binary))
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	// Resolve the AMQ env via the amq CLI so launch.json and the actual
	// mailbox agree. resolveAMQEnv respects .amqrc and AMQ's validated
	// sender identity exactly as coop exec will. If both --session and
	// --root were passed, the boundary policy in amq_env.go drops --root
	// (and warns), since amq treats --session NAME as shorthand for
	// --root .agent-mail/<name> and rejects the pair.
	env, err := resolveAMQEnv(*rootFlag, *session, handle)
	if err != nil {
		return fmt.Errorf("resolve amq env: %w", err)
	}
	root := env.Root
	if env.Me != "" {
		handle = env.Me
	}

	agentDir := filepath.Join(root, "agents", handle)
	rec := launch.Record{
		CWD:              cwd,
		Binary:           binary,
		Argv:             childArgs,
		Session:          env.SessionName,
		SharedWorkstream: *sharedWorkstream,
		Conversation:     conversationRef,
		Handle:           handle,
		Role:             *roleFlag,
		Root:             root,
		BaseRoot:         env.BaseRoot,
		RootSource:       env.RootSource,
		AMQVersion:       env.AMQVersion,
		CodexArgs:        binaryArgs["codex"],
		ClaudeArgs:       binaryArgs["claude"],
		Launcher:         launcher,
		LauncherArgs:     launcherArgs,
		Model:            strings.TrimSpace(*model),
		Trust:            trustMode,
		NoDefaultArgs:    *noDefaultArgs,
		AgentPID:         os.Getpid(),
		AgentTTY:         currentLaunchTTY(),
		StartedAt:        time.Now().UTC(),
		TeamProfile:      strings.TrimSpace(*teamProfile),
	}

	// Keep generated bootstrap out of launch.json so restore stays compact
	// and does not replay stale startup text.
	effectiveChildArgs := append([]string(nil), childArgs...)
	if !*noBootstrap && shouldAppendBootstrapWithDefaults(childArgs, defaultArgs) {
		prompt, err := buildBootstrapPrompt(bootstrapContextFor(rec, agentDir, *teamHome))
		if err != nil {
			return err
		}
		effectiveChildArgs = append(effectiveChildArgs, prompt)
	}

	// Build the coop exec invocation. Done before any disk writes so
	// --dry-run is a true preview with zero side effects. --session NAME
	// is amq shorthand for --root .agent-mail/<name>; passing both is
	// rejected by amq, so prefer --session when callers supplied both
	// (matching the resolveAMQEnvInDir boundary policy). The same warning
	// fires once at env resolution time, so this branch stays silent to
	// avoid duplicating the message.
	coopArgs := []string{"coop", "exec"}
	if *session != "" {
		coopArgs = append(coopArgs, "--session", *session)
	} else if *rootFlag != "" {
		coopArgs = append(coopArgs, "--root", *rootFlag)
	}
	if *me != "" {
		coopArgs = append(coopArgs, "--me", *me)
	}
	// A custom launcher is exec'd in place of the binary. Launcher args precede
	// the agent's normal child args; the launcher is expected to forward the
	// trailing args to the binary so bootstrap and default args still reach it.
	target := binary
	trailing := effectiveChildArgs
	if launcher != "" {
		target = launcher
		trailing = append(append([]string(nil), launcherArgs...), effectiveChildArgs...)
	}
	coopArgs = append(coopArgs, target)
	if len(trailing) > 0 {
		coopArgs = append(coopArgs, "--")
		coopArgs = append(coopArgs, trailing...)
	}

	if *dryRun {
		fmt.Println(shellCommand("amq", coopArgs...))
		quietNotice("(dry run - no files written, not execing)\n")
		verbosePolicyEcho()
		return nil
	}

	if launcher != "" {
		if err := ensureLauncherExecutable(launcher); err != nil {
			return err
		}
	}

	preflight := agentLaunchPreflight{
		AgentDir:   agentDir,
		Handle:     handle,
		Workstream: env.SessionName,
		Root:       root,
		Binary:     binary,
		Force:      *forceDuplicate,
	}
	if blocker, err := preflight.check(defaultDuplicateLaunchProbe); err != nil {
		return err
	} else if blocker != nil {
		return blocker
	}

	// Ensure the active brief stub exists for this workstream before the
	// launch record is written, so a brief-creation failure does not leave
	// a fresh launch.json for an agent that never started. resolveBriefHome
	// applies the same skip rule bootstrap uses (explicit --team-home or
	// cwd-with-team-rules-md only) so the two sources stay aligned.
	if briefHome := resolveBriefHome(*teamHome, cwd); briefHome != "" {
		if _, _, err := ensureBriefStub(briefHome, rec.Session); err != nil {
			return fmt.Errorf("ensure brief: %w", err)
		}
	}

	if err := launch.Write(agentDir, rec); err != nil {
		return fmt.Errorf("write launch record: %w", err)
	}

	// Seed role.md from the catalog when the role is known. Never
	// overwrites existing user edits.
	if *roleFlag != "" {
		if err := seedRoleStub(agentDir, *roleFlag); err != nil {
			fmt.Fprintf(os.Stderr, "warning: seed role.md: %v\n", err)
		}
	}

	amqBin, err := exec.LookPath("amq")
	if err != nil {
		return fmt.Errorf("amq not found in PATH: %w", err)
	}
	// Strip inherited AMQ identity vars before exec'ing coop exec. We already
	// resolved the right --root/--session/--me on the command line; passing a
	// stale AM_ROOT/AM_ME from the launching shell along to the agent would
	// re-create the identity-leak asymmetry #46 closed for env resolution.
	return syscall.Exec(amqBin, append([]string{"amq"}, coopArgs...), envWithoutAMQIdentity(os.Environ()))
}

// ensureLauncherExecutable verifies a custom --launcher path exists and is an
// executable file before exec, so a missing or non-executable wrapper fails
// with a clear message instead of an opaque coop exec error.
func ensureLauncherExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("launcher %q not found", path)
		}
		return fmt.Errorf("launcher %q: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("launcher %q is a directory, not an executable", path)
	}
	if info.Mode()&0o111 == 0 {
		return fmt.Errorf("launcher %q is not executable (chmod +x it)", path)
	}
	return nil
}

func conversationRefFromFlags(conversation, conversationID string) (string, error) {
	conversation = strings.TrimSpace(conversation)
	conversationID = strings.TrimSpace(conversationID)
	if conversation != "" && conversationID != "" && conversation != conversationID {
		return "", fmt.Errorf("use only one of --conversation or --conversation-id")
	}
	if conversation != "" {
		return conversation, nil
	}
	return conversationID, nil
}

func applyConversationRestoreArgs(binary string, childArgs []string, conversation string) ([]string, error) {
	return applyConversationRestoreArgsWithDefaults(binary, childArgs, conversation, defaultChildArgsForBinary(binary))
}

func applyConversationRestoreArgsWithDefaults(binary string, childArgs []string, conversation string, defaultArgs []string) ([]string, error) {
	conversation = strings.TrimSpace(conversation)
	if conversation == "" {
		return childArgs, nil
	}
	switch normalizedAgentBinary(binary) {
	case "codex":
		if ref, ok := codexResumeRef(childArgs); ok {
			if ref == "" {
				return nil, fmt.Errorf("--conversation cannot be combined with existing codex resume without a ref")
			}
			if ref != conversation {
				return nil, fmt.Errorf("--conversation %q conflicts with existing codex resume %q", conversation, ref)
			}
			if !hasNoExtraConversationArgs(stripCodexResumeRef(childArgs, conversation), defaultArgs) {
				return nil, fmt.Errorf("--conversation cannot be combined with extra codex args; omit --conversation and pass native resume args after --")
			}
			return childArgs, nil
		}
		if !hasNoExtraConversationArgs(childArgs, defaultArgs) {
			return nil, fmt.Errorf("--conversation cannot be combined with extra codex args; omit --conversation and pass native resume args after --")
		}
		out := append([]string(nil), childArgs...)
		return append(out, "resume", conversation), nil
	case "claude":
		if ref, ok := claudeResumeRef(childArgs); ok {
			if ref == "" {
				return nil, fmt.Errorf("--conversation cannot be combined with existing claude resume or continue without a ref")
			}
			if ref != conversation {
				return nil, fmt.Errorf("--conversation %q conflicts with existing claude resume %q", conversation, ref)
			}
			if !hasNoExtraConversationArgs(stripClaudeResumeRef(childArgs, conversation), defaultArgs) {
				return nil, fmt.Errorf("--conversation cannot be combined with extra claude args; omit --conversation and pass native resume args after --")
			}
			return childArgs, nil
		}
		if !hasNoExtraConversationArgs(childArgs, defaultArgs) {
			return nil, fmt.Errorf("--conversation cannot be combined with extra claude args; omit --conversation and pass native resume args after --")
		}
		out := append([]string(nil), childArgs...)
		return append(out, "--resume", conversation), nil
	default:
		return nil, fmt.Errorf("--conversation is supported for codex and claude, got %q", binary)
	}
}

func hasNoExtraConversationArgs(childArgs []string, defaultArgs []string) bool {
	if len(childArgs) == 0 {
		return true
	}
	if len(childArgs) != len(defaultArgs) {
		return false
	}
	for i := range childArgs {
		if childArgs[i] != defaultArgs[i] {
			return false
		}
	}
	return true
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

func codexResumeRef(args []string) (string, bool) {
	for i, arg := range args {
		if arg != "resume" {
			continue
		}
		if i+1 < len(args) {
			return args[i+1], true
		}
		return "", true
	}
	return "", false
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

func claudeResumeRef(args []string) (string, bool) {
	for i, arg := range args {
		switch {
		case arg == "--resume" || arg == "-r" || arg == "--session-id":
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", true
		case strings.HasPrefix(arg, "--resume="):
			return strings.TrimPrefix(arg, "--resume="), true
		case strings.HasPrefix(arg, "--session-id="):
			return strings.TrimPrefix(arg, "--session-id="), true
		case arg == "--continue" || arg == "-c":
			return "", true
		}
	}
	return "", false
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

// resolveAMQRoot shells out to `amq env --json` to discover the final root path.
func resolveAMQRoot(rootFlag, session, handle string) (string, error) {
	return resolveAMQRootInDir("", rootFlag, session, handle)
}

func resolveAMQRootInDir(cwd, rootFlag, session, handle string) (string, error) {
	env, err := resolveAMQEnvInDir(cwd, rootFlag, session, handle)
	if err != nil {
		return "", err
	}
	return env.Root, nil
}

// seedRoleStub writes a role.md stub for the given agent directory based on
// the catalog entry for roleID. If the role isn't in the catalog, it still
// writes a minimal stub with the label = roleID.
func seedRoleStub(agentDir, roleID string) error {
	stub := role.Stub{RoleID: roleID, Label: roleID}
	if r := catalog.Lookup(roleID); r != nil {
		stub.Label = r.Label
		stub.Description = r.Description
		stub.Skills = r.Skills
		stub.Peers = r.DefaultPeers
	}
	_, err := role.EnsureStub(agentDir, stub)
	return err
}

// splitDashDash splits argv at the first "--" separator.
func splitDashDash(args []string) ([]string, []string) {
	for i, a := range args {
		if a == "--" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}
