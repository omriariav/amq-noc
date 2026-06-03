package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omriariav/amq-noc/internal/launch"
	"github.com/omriariav/amq-noc/internal/role"
	"github.com/omriariav/amq-noc/internal/team"
)

type restoreCandidate struct {
	entry launch.Entry
}

// runRestore holds the real replay/scan logic. The top-level `restore` verb
// was removed in 2.0; this body now backs `agent resume <role>` (via
// runAgentResume, which forwards `--exec --role <role>`). It is internal-only
// and carries no deprecation surface of its own.
func runRestore(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	projectDirs := fs.String("project", "", "comma-separated project directories to scan (default: cwd)")
	execRestore := fs.Bool("exec", false, "exec the selected launch in this terminal")
	roleFilter := fs.String("role", "", "only consider records with this role")
	handleFilter := fs.String("handle", "", "only consider records with this handle")
	sessionFilter := fs.String("session", "", "only consider records with this session")
	conversationFilter := fs.String("conversation", "", "only consider records with this conversation name/id")

	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad agent resume - re-launch a saved agent by role

Usage:
  amq-squad agent resume <role> [--handle h] [--session s] [--conversation ref] [--project dir1,dir2,...]

Scans each project for amq-squad extension launch records, nearby role.md
persona files, and older AMQ mailbox history when launch.json is missing and
the original binary can be inferred. Default scope is the current working
directory if --project is omitted. Exactly one record must match; amq-squad
changes to that record's cwd and execs the saved launch through
'amq coop exec'. Use 'amq-squad history' to list restorable records.

For records that look active, the metadata line includes wake-health:
  wake: pid:N    - wake.lock present and the wake process is alive
  wake: missing  - agent looks active but no wake.lock was found
  wake: stale    - wake.lock present but the PID is dead or unrelated

Examples:
  amq-squad agent resume cto
  amq-squad agent resume fullstack --session issue-96
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	dirs, err := historyProjectDirs(*projectDirs)
	if err != nil {
		return err
	}

	var records []restoreCandidate

	for _, dir := range dirs {
		baseRoot, err := scanBaseRootForProject(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: resolve amq env for %s: %v\n", dir, err)
			baseRoot = ""
		}
		var entries []launch.Entry
		if baseRoot != "" {
			entries, err = launch.ScanRestorableEntriesInRoot(dir, baseRoot)
		} else {
			entries, err = launch.ScanRestorableEntries(dir)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: scan %s: %v\n", dir, err)
			continue
		}
		for _, e := range entries {
			if !matchesRestoreFilters(e.Record, *roleFilter, *handleFilter, *sessionFilter, *conversationFilter) {
				continue
			}
			records = append(records, restoreCandidate{entry: e})
		}
	}

	if len(records) == 0 {
		return fmt.Errorf("no matching launch.json records found in %s", strings.Join(dirs, ", "))
	}

	sort.Slice(records, func(i, j int) bool {
		if records[i].entry.Record.Role != records[j].entry.Record.Role {
			return records[i].entry.Record.Role < records[j].entry.Record.Role
		}
		return records[i].entry.Record.Handle < records[j].entry.Record.Handle
	})

	if *execRestore {
		if len(records) != 1 {
			printRestoreCandidates(os.Stderr, records)
			return fmt.Errorf("--exec requires exactly one matching record; narrow with --role, --handle, --session, or --conversation")
		}
		rec := records[0].entry.Record
		fmt.Fprintf(os.Stderr, "Restoring %s via amq coop exec.\n", restoreLabel(rec))
		return execRestoreRecord(rec)
	}

	fmt.Println("# amq-squad restore - run each command in its own terminal tab")
	fmt.Println()
	for i, f := range records {
		rec := f.entry.Record
		fmt.Printf("# %d. %s - %s (%s)\n", i+1, restoreLabel(rec), rec.Binary, restoreMetadata(f.entry))
		fmt.Println(emitCommand(rec))
		fmt.Println()
	}
	return nil
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

func printRestoreCandidates(out *os.File, records []restoreCandidate) {
	fmt.Fprintln(out, "Matching restore candidates:")
	for i, f := range records {
		rec := f.entry.Record
		fmt.Fprintf(out, "  %d. %s - %s (%s)\n", i+1, restoreLabel(rec), rec.Binary, restoreMetadata(f.entry))
	}
}

func restoreLabel(rec launch.Record) string {
	if rec.Role != "" {
		return rec.Role
	}
	if rec.Handle != "" {
		return rec.Handle
	}
	return "(unknown)"
}

func restoreMetadata(entry launch.Entry) string {
	rec := entry.Record
	parts := []string{}
	if rec.Session != "" {
		parts = append(parts, "session: "+rec.Session)
	}
	if rec.Conversation != "" {
		parts = append(parts, "conversation: "+rec.Conversation)
	}
	if rec.Handle != "" {
		parts = append(parts, "handle: "+rec.Handle)
	}
	if role.Exists(entry.AgentDir) {
		parts = append(parts, "persona: role.md")
	} else {
		parts = append(parts, "persona: missing")
	}
	if entry.Source != "" {
		parts = append(parts, "source: "+sourceLabel(entry.Source))
	}
	if wake := wakeHealthForEntry(entry, defaultDuplicateLaunchProbe); wake != "" {
		parts = append(parts, "wake: "+wake)
	}
	if !rec.StartedAt.IsZero() {
		label := "started"
		if entry.Source != "" && entry.Source != launch.FileName {
			label = "last seen"
		}
		parts = append(parts, label+": "+rec.StartedAt.Format("2006-01-02 15:04"))
	}
	if rec.CWD != "" {
		parts = append(parts, "cwd: "+rec.CWD)
	}
	return strings.Join(parts, ", ")
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

func execRestoreRecord(rec launch.Record) error {
	if rec.CWD == "" {
		return fmt.Errorf("launch record has empty cwd")
	}
	if err := os.Chdir(rec.CWD); err != nil {
		return fmt.Errorf("chdir %s: %w", rec.CWD, err)
	}
	return runLaunch(launchArgsFromRecord(rec))
}

func launchArgsFromRecord(rec launch.Record) []string {
	var args []string
	// Only a record with a saved conversation is a true reattach that must
	// skip bootstrap; a seat with no saved conversation re-runs bootstrap so
	// the agent re-orients from its brief and drains AMQ history rather than
	// coming up blank. This path has no operator opts, so it gates on the
	// conversation alone.
	if rec.Conversation != "" {
		args = append(args, "--no-bootstrap")
	}
	if rec.Role != "" {
		args = append(args, "--role", rec.Role)
	}
	// amq treats --session NAME as shorthand for --root .agent-mail/<name>,
	// so passing both is a hard "mutually exclusive" rejection. Emit one or
	// the other, never both. The same rule applies in emitCommandWithOptions.
	if rec.Session != "" {
		args = append(args, "--session", rec.Session)
	} else if rec.Root != "" {
		args = append(args, "--root", rec.Root)
	}
	if rec.SharedWorkstream {
		args = append(args, "--team-workstream")
	}
	if rec.Conversation != "" {
		args = append(args, "--conversation", rec.Conversation)
	}
	if rec.NoDefaultArgs {
		args = append(args, "--no-default-args")
	}
	if trust := trustModeFromRecord(rec); trust != "" {
		args = append(args, "--trust", trust)
	}
	if model := strings.TrimSpace(rec.Model); model != "" {
		args = append(args, "--model", model)
	}
	// =VALUE form keeps the value glued to the flag so a literal "--" inside
	// the binary args never reaches splitDashDash on replay.
	if len(rec.CodexArgs) > 0 {
		args = append(args, "--codex-args="+joinedAgentArgs(rec.CodexArgs))
	}
	if len(rec.ClaudeArgs) > 0 {
		args = append(args, "--claude-args="+joinedAgentArgs(rec.ClaudeArgs))
	}
	if rec.Launcher != "" {
		args = append(args, "--launcher", rec.Launcher)
		if len(rec.LauncherArgs) > 0 {
			args = append(args, "--launcher-args="+joinedAgentArgs(rec.LauncherArgs))
		}
	}
	if rec.Handle != "" {
		args = append(args, "--me", rec.Handle)
	}
	args = append(args, rec.Binary)
	argv := restoreArgvFromRecord(rec)
	if len(argv) > 0 {
		args = append(args, "--")
		args = append(args, argv...)
	}
	return args
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

// emitCommand reconstructs the bash command for a launch record.
// It emits the modern `agent up <binary>` surface so role + metadata
// round-trip cleanly without recommending the deprecated `launch` verb;
// callers who want the raw amq invocation can run with --dry-run to see it.
func emitCommand(rec launch.Record) string {
	return emitCommandWithOptions(rec, emitCommandOptions{})
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
