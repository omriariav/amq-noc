package cli

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-noc/internal/state"
)

// archiveDirName is the base-root subdirectory that `archive` MOVES sessions
// into. It is dot-prefixed so the status board (which skips dot-dirs) and the
// AMQ session scanners never treat archived sessions as live workstreams.
const archiveDirName = ".archive"

// rmMode is the one bit that separates the only two destructive verbs:
//   - rmModeDelete (rm):     permanently remove the session root + brief.
//   - rmModeArchive (archive): MOVE the session root (and brief) aside into
//     <baseRoot>/.archive/<session>/ instead of deleting.
type rmMode int

const (
	rmModeDelete rmMode = iota
	rmModeArchive
)

func (m rmMode) verb() string {
	if m == rmModeArchive {
		return "archive"
	}
	return "rm"
}

// rmExecution carries everything the destructive path needs, with every
// dangerous seam injectable so tests drive it deterministically: the base-root
// resolver, the liveness probe, the confirmation reader, and the writer.
//
// SAFETY CONTRACT (the whole point of this verb):
//   - The session name is validated through validateWorkstreamName so a
//     traversal or absolute path can never escape the base root.
//   - The target is filepath.Join(BaseRoot, session) AND is re-checked to be a
//     direct child of BaseRoot before a single byte is touched.
//   - A live agent in the session refuses the operation unless Force.
//   - Without Yes, the operator must confirm an explicit preview; the default
//     answer is NO, and declining makes ZERO filesystem changes.
type rmExecution struct {
	ProjectDir string
	Session    string
	Mode       rmMode
	Yes        bool
	Force      bool

	// BaseRoot, when set, is used verbatim and ResolveBaseRoot is NOT called.
	// Tests seed this; production leaves it empty and resolves once.
	BaseRoot        string
	ResolveBaseRoot func(projectDir string) (string, error)

	// Probe drives liveness detection through internal/state. Tests inject a
	// deterministic probe; production uses state.DefaultProbe.
	Probe state.Probe

	// Confirm is the confirmation reader. Defaults to os.Stdin. Tests supply a
	// strings.Reader so y/n is deterministic without real stdin.
	Confirm io.Reader

	Out io.Writer
}

func runRm(args []string, mode rmMode) error {
	verb := mode.verb()
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	yes := fs.Bool("yes", false, "skip the confirmation prompt (for automation)")
	fs.BoolVar(yes, "y", false, "shorthand for --yes")
	force := fs.Bool("force", false, "proceed even when the session has live agents (does NOT stop them)")
	projectFlag := fs.String("project", "", "project/team-home directory to target (default: cwd)")
	fs.Usage = rmUsage(fs, mode)
	args = allowInterspersedFlags(fs, args)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return usageErrorf("%s requires a session name: %s <session>", verb, verb)
	}
	if fs.NArg() > 1 {
		return usageErrorf("%s takes exactly one session; got %d", verb, fs.NArg())
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	projectDir, err := resolveProjectDirFlag(cwd, *projectFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	return executeRm(rmExecution{
		ProjectDir: projectDir,
		Session:    fs.Arg(0),
		Mode:       mode,
		Yes:        *yes,
		Force:      *force,
		Probe:      state.DefaultProbe,
		Confirm:    os.Stdin,
		Out:        os.Stdout,
	})
}

func rmUsage(fs *flag.FlagSet, mode rmMode) func() {
	return func() {
		if mode == rmModeArchive {
			fmt.Fprint(os.Stderr, `amq-squad archive - move a finished session aside (non-destructive)

Usage:
  amq-squad archive <session> [--project DIR] [--yes|-y] [--force]

Moves the session's AMQ root dir to <baseRoot>/.archive/<session>/ and moves
its brief alongside it as .archive/<session>/<session>.md. Nothing is deleted.
The session leaves the board but its mailboxes and brief are recoverable.
--project targets another team-home without changing directories.

By default archive PREVIEWS exactly what will move and prompts for confirmation
(default: No). Declining makes zero filesystem changes. Pass --yes/-y to skip
the prompt for automation.

A session with any LIVE agent is refused unless --force; archive never stops
running agents. Stop the team first with 'amq-squad down --all --force'.

Examples:
  amq-squad archive issue-96
  amq-squad archive issue-96 --project ~/Code/app --yes
  amq-squad archive issue-96 --yes
  amq-squad archive issue-96 --force --yes
`)
			return
		}
		fmt.Fprint(os.Stderr, `amq-squad rm - permanently remove a finished session

Usage:
  amq-squad rm <session> [--project DIR] [--yes|-y] [--force]

Deletes the session's AMQ root dir (<baseRoot>/<session>/) and its brief
(.amq-squad/briefs/<session>.md). This session-destructive verb is confined to
the session: it never touches a sibling session or anything outside that one
root and brief.
--project targets another team-home without changing directories.

By default rm PREVIEWS exactly what will be removed (the resolved paths + agent
count) and prompts for confirmation (default: No). Declining makes zero
filesystem changes. Pass --yes/-y to skip the prompt for automation. To keep the
data recoverable, use 'amq-squad archive <session>' instead.

A session with any LIVE agent is refused unless --force; rm never stops running
agents. Stop the team first with 'amq-squad down --all --force'.

Examples:
  amq-squad rm issue-96
  amq-squad rm issue-96 --project ~/Code/app --yes
  amq-squad rm issue-96 --yes
  amq-squad rm issue-96 --force --yes
`)
	}
}

// allowInterspersedFlags moves flags before positional arguments so small
// imperative commands like `amq-squad rm issue-96 --yes` work the way operators
// naturally type them while still using the stdlib flag parser for validation.
func allowInterspersedFlags(fs *flag.FlagSet, args []string) []string {
	var flags []string
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positional = append(positional, arg)
			continue
		}
		flags = append(flags, arg)
		name := flagName(arg)
		if name == "" || strings.Contains(arg, "=") {
			continue
		}
		f := fs.Lookup(name)
		if f == nil || isBoolFlag(f) {
			continue
		}
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, positional...)
}

func flagName(arg string) string {
	arg = strings.TrimLeft(arg, "-")
	if arg == "" {
		return ""
	}
	if name, _, ok := strings.Cut(arg, "="); ok {
		return name
	}
	return arg
}

type boolFlag interface {
	IsBoolFlag() bool
}

func isBoolFlag(f *flag.Flag) bool {
	bf, ok := f.Value.(boolFlag)
	return ok && bf.IsBoolFlag()
}

// rmTarget is the fully resolved, safety-checked footprint of one session.
type rmTarget struct {
	Session    string
	BaseRoot   string
	Root       string // <baseRoot>/<session>
	RootExists bool
	Brief      string // brief path; "" when none could be resolved
	BriefHas   bool
	Agents     int // count of agent mailboxes under <root>/agents
}

func executeRm(e rmExecution) error {
	_, err := executeRmReportDeclined(e)
	return err
}

// executeRmReportDeclined is executeRm's body, additionally reporting whether
// the operator declined the confirmation gate (which, like executeRm, makes
// ZERO filesystem changes and returns a nil error). `up --reset` reuses this
// so it can cancel the whole launch on a decline rather than proceeding to
// launch into the session the operator just refused to clear.
func executeRmReportDeclined(e rmExecution) (bool, error) {
	verb := e.Mode.verb()
	out := e.Out
	if out == nil {
		out = os.Stdout
	}

	// SAFETY 1: validate the session name BEFORE it is ever joined into a path,
	// so a traversal ("../foo"), an absolute path, or a name with separators is
	// rejected outright and can never escape the base root.
	session := strings.TrimSpace(e.Session)
	if err := validateWorkstreamName(session); err != nil {
		return false, err
	}

	resolve := e.ResolveBaseRoot
	if resolve == nil {
		resolve = scanBaseRootForProject
	}
	baseRoot := e.BaseRoot
	if baseRoot == "" {
		var err error
		baseRoot, err = resolve(e.ProjectDir)
		if err != nil {
			return false, fmt.Errorf("resolve AMQ base root: %w", err)
		}
	}
	if strings.TrimSpace(baseRoot) == "" {
		return false, fmt.Errorf("resolved AMQ base root is empty; nothing to %s", verb)
	}
	baseRoot = filepath.Clean(baseRoot)

	root := filepath.Join(baseRoot, session)
	// SAFETY 2 (highest-risk property): the target MUST resolve to a direct
	// child of the base root. A validated session name already forbids
	// separators, but we re-derive and compare independently so a future change
	// to validation can never silently re-open an escape. Deleting session X
	// must be provably incapable of touching session Y or anything outside
	// <baseRoot>/<session>/.
	if filepath.Dir(root) != baseRoot || filepath.Base(root) != session {
		return false, fmt.Errorf("refusing to %s: resolved path %q is not a direct child of base root %q", verb, root, baseRoot)
	}

	target := rmTarget{
		Session:  session,
		BaseRoot: baseRoot,
		Root:     root,
		Brief:    briefPath(e.ProjectDir, session),
	}
	if fi, err := os.Stat(root); err == nil && fi.IsDir() {
		target.RootExists = true
		target.Agents = countAgentMailboxes(root)
	} else if err == nil && !fi.IsDir() {
		return false, fmt.Errorf("refusing to %s: %q exists but is not a directory", verb, root)
	}
	if target.Brief != "" {
		if _, err := os.Stat(target.Brief); err == nil {
			target.BriefHas = true
		}
	}

	// SAFETY 5: nothing to remove is a clean error, never a panic.
	if !target.RootExists && !target.BriefHas {
		return false, fmt.Errorf("%s: session %q has no AMQ root or brief under %s; nothing to remove", verb, session, baseRoot)
	}

	// SAFETY 3: refuse a running session unless --force. Reuse the repo's
	// liveness (internal/state) so this agrees with status/down about "live".
	if target.RootExists {
		live, err := liveAgentsInSession(e.ProjectDir, baseRoot, session, e.Probe)
		if err != nil {
			return false, fmt.Errorf("check liveness for session %q: %w", session, err)
		}
		if len(live) > 0 && !e.Force {
			return false, fmt.Errorf("session %q has live agents (%s); stop it first with 'amq-squad down --all --force', or pass --force to %s anyway",
				session, strings.Join(live, ", "), verb)
		}
	}

	// PREVIEW: list exactly what will be removed/moved, every time.
	renderRmPreview(out, e.Mode, target)

	// SAFETY 2 (confirm gate): default is NO. Declining makes ZERO changes.
	if !e.Yes {
		if !confirmRm(out, e.Confirm, session) {
			fmt.Fprintf(out, "%s: aborted; no changes made.\n", verb)
			return true, nil
		}
	}

	if e.Mode == rmModeArchive {
		return false, archiveSession(out, target)
	}
	return false, deleteSession(out, target)
}

// liveAgentsInSession returns the handles of agents the repo's liveness
// classifier considers operational (alive, wake-live, or dead-mailbox-live) in
// the named session. An empty slice means the session is safe to tear down.
func liveAgentsInSession(projectDir, baseRoot, session string, probe state.Probe) ([]string, error) {
	snap, err := state.Build(projectDir, baseRoot, probe)
	if err != nil {
		return nil, err
	}
	var live []string
	for _, sess := range snap.Sessions {
		if sess.Name != session {
			continue
		}
		for _, a := range sess.Agents {
			switch a.Liveness {
			case state.LivenessAlive, state.LivenessWakeLive, state.LivenessDeadMailboxLive:
				live = append(live, a.Handle)
			}
		}
	}
	return live, nil
}

// countAgentMailboxes counts the agent subdirectories under <root>/agents so
// the preview can report how many agents a session footprint covers, even when
// no launch record exists (e.g. a session that only has mailboxes + a brief).
func countAgentMailboxes(root string) int {
	entries, err := os.ReadDir(filepath.Join(root, "agents"))
	if err != nil {
		return 0
	}
	n := 0
	for _, ent := range entries {
		if ent.IsDir() {
			n++
		}
	}
	return n
}

func renderRmPreview(out io.Writer, mode rmMode, t rmTarget) {
	if mode == rmModeArchive {
		fmt.Fprintf(out, "# amq-squad archive — preview\n")
	} else {
		fmt.Fprintf(out, "# amq-squad rm — preview\n")
	}
	fmt.Fprintf(out, "# session:  %s\n", t.Session)
	fmt.Fprintf(out, "# agents:   %d\n", t.Agents)
	fmt.Fprintln(out)
	action := "DELETE"
	if mode == rmModeArchive {
		action = "MOVE"
		dest := filepath.Join(t.BaseRoot, archiveDirName, t.Session)
		if t.RootExists {
			fmt.Fprintf(out, "  %s  %s\n", action, t.Root)
			fmt.Fprintf(out, "      -> %s\n", dest)
		}
		if t.BriefHas {
			fmt.Fprintf(out, "  %s  %s\n", action, t.Brief)
			fmt.Fprintf(out, "      -> %s\n", filepath.Join(dest, t.Session+".md"))
		}
		return
	}
	if t.RootExists {
		fmt.Fprintf(out, "  %s  %s\n", action, t.Root)
	}
	if t.BriefHas {
		fmt.Fprintf(out, "  %s  %s\n", action, t.Brief)
	}
}

// confirmRm prompts and reads a single y/N answer. The default is NO: any
// answer that is not an explicit yes (y / yes, case-insensitive) declines, and
// EOF / empty input declines too. This is intentionally strict — an rm that
// proceeds on a stray keypress is a defect.
func confirmRm(out io.Writer, r io.Reader, session string) bool {
	if r == nil {
		r = os.Stdin
	}
	fmt.Fprintf(out, "Remove session %s? [y/N] ", session)
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "y" || answer == "yes"
}

func deleteSession(out io.Writer, t rmTarget) error {
	if t.RootExists {
		if err := os.RemoveAll(t.Root); err != nil {
			return fmt.Errorf("remove session root %q: %w", t.Root, err)
		}
		fmt.Fprintf(out, "removed %s\n", t.Root)
	}
	if t.BriefHas {
		if err := os.Remove(t.Brief); err != nil {
			return fmt.Errorf("remove brief %q: %w", t.Brief, err)
		}
		fmt.Fprintf(out, "removed %s\n", t.Brief)
	}
	fmt.Fprintf(out, "rm: session %s removed.\n", t.Session)
	return nil
}

func archiveSession(out io.Writer, t rmTarget) error {
	dest := filepath.Join(t.BaseRoot, archiveDirName, t.Session)
	if err := os.MkdirAll(filepath.Join(t.BaseRoot, archiveDirName), 0o755); err != nil {
		return fmt.Errorf("create archive dir: %w", err)
	}
	// Refuse to clobber an existing archive entry: silently overwriting a prior
	// archive of the same session would be its own data-loss defect.
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("archive: %q already exists; remove it first or pick a different session name", dest)
	}
	if t.RootExists {
		if err := os.Rename(t.Root, dest); err != nil {
			return fmt.Errorf("archive session root %q: %w", t.Root, err)
		}
		fmt.Fprintf(out, "moved %s -> %s\n", t.Root, dest)
	}
	if t.BriefHas {
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return fmt.Errorf("create archive dir: %w", err)
		}
		briefDest := filepath.Join(dest, t.Session+".md")
		if err := os.Rename(t.Brief, briefDest); err != nil {
			return fmt.Errorf("archive brief %q: %w", t.Brief, err)
		}
		fmt.Fprintf(out, "moved %s -> %s\n", t.Brief, briefDest)
	}
	fmt.Fprintf(out, "archive: session %s moved to %s.\n", t.Session, dest)
	return nil
}
