package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/omriariav/amq-noc/internal/state"
)

const defaultThreadTranscriptLimit = 20

type threadEnvelopeData struct {
	ProjectDir  string          `json:"project_dir"`
	BaseRoot    string          `json:"base_root"`
	Session     string          `json:"session"`
	Root        string          `json:"root"`
	Thread      string          `json:"thread"`
	IncludeBody bool            `json:"include_body"`
	Limit       int             `json:"limit,omitempty"`
	Entries     json.RawMessage `json:"entries,omitempty"`
	Output      string          `json:"output,omitempty"`
}

type threadExecution struct {
	ProjectDir      string
	Session         string
	Thread          string
	IncludeBody     bool
	Limit           int
	BaseRoot        string
	ResolveBaseRoot func(projectDir string) (string, error)
	Probe           state.Probe
	Out             io.Writer
	JSON            bool
	RunAMQThread    func(threadAMQRequest) ([]byte, error)
}

type threadAMQRequest struct {
	Root        string
	Thread      string
	IncludeBody bool
	Limit       int
	JSON        bool
}

func runThread(args []string) error {
	fs := flag.NewFlagSet("thread", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory to inspect (default: cwd)")
	sessionFlag := fs.String("session", "", "AMQ workstream session name to inspect")
	threadFlag := fs.String("id", "", "thread id to read")
	includeBody := fs.Bool("include-body", true, "include message bodies in the transcript")
	limitFlag := fs.Int("limit", defaultThreadTranscriptLimit, "maximum messages to show (0 = all)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned thread envelope instead of the human transcript")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad thread - read one AMQ thread by project and session

Usage:
  amq-squad thread --session NAME --id THREAD [--project DIR] [--limit N] [--include-body=false] [--json]

Resolves the selected amq-squad workstream to its AMQ root, then reads the
thread transcript without moving unread mail. This is a project/session wrapper
around the read-only AMQ thread inspection, so operators do not need to know
the .agent-mail path.

Examples:
  amq-squad thread --session issue-96 --id p2p/cto__fullstack
  amq-squad thread --project ~/Code/app --session issue-96 --id decision/ship --limit 50
  amq-squad thread --session issue-96 --id decision/ship --json
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if strings.TrimSpace(*sessionFlag) == "" {
		return usageErrorf("thread requires --session")
	}
	if strings.TrimSpace(*threadFlag) == "" {
		return usageErrorf("thread requires --id")
	}
	if *limitFlag < 0 {
		return usageErrorf("--limit must be >= 0")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	projectDir, err := resolveProjectDirFlag(cwd, *projectFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	return executeThread(threadExecution{
		ProjectDir:  projectDir,
		Session:     *sessionFlag,
		Thread:      *threadFlag,
		IncludeBody: *includeBody,
		Limit:       *limitFlag,
		Out:         os.Stdout,
		JSON:        *jsonOut,
	})
}

func executeThread(s threadExecution) error {
	out := s.Out
	if out == nil {
		out = os.Stdout
	}
	session := strings.TrimSpace(s.Session)
	if session == "" {
		return usageErrorf("thread requires --session")
	}
	threadID := strings.TrimSpace(s.Thread)
	if threadID == "" {
		return usageErrorf("thread requires --id")
	}
	if s.Limit < 0 {
		return usageErrorf("--limit must be >= 0")
	}
	resolve := s.ResolveBaseRoot
	if resolve == nil {
		resolve = scanBaseRootForProject
	}
	baseRoot := strings.TrimSpace(s.BaseRoot)
	var err error
	if baseRoot == "" {
		baseRoot, err = resolve(s.ProjectDir)
		if err != nil {
			return fmt.Errorf("resolve AMQ base root: %w", err)
		}
	}
	if baseRoot == "" {
		return fmt.Errorf("resolve AMQ base root: empty root")
	}
	snap, err := state.Build(s.ProjectDir, baseRoot, s.Probe)
	if err != nil {
		return fmt.Errorf("scan AMQ base root: %w", err)
	}
	sess, ok := findThreadsSession(snap.Sessions, session)
	if !ok {
		return fmt.Errorf("session %q not found under %s", session, baseRoot)
	}
	run := s.RunAMQThread
	if run == nil {
		run = runAMQThreadDefault
	}
	req := threadAMQRequest{
		Root:        sess.Root,
		Thread:      threadID,
		IncludeBody: s.IncludeBody,
		Limit:       s.Limit,
		JSON:        s.JSON,
	}
	output, err := run(req)
	if err != nil {
		return err
	}
	env := threadEnvelopeData{
		ProjectDir:  s.ProjectDir,
		BaseRoot:    snap.BaseRoot,
		Session:     sess.Name,
		Root:        sess.Root,
		Thread:      threadID,
		IncludeBody: s.IncludeBody,
		Limit:       s.Limit,
	}
	if s.JSON {
		entries := json.RawMessage(strings.TrimSpace(string(output)))
		if len(entries) == 0 {
			entries = json.RawMessage("[]")
		}
		if !json.Valid(entries) {
			return fmt.Errorf("amq thread returned invalid JSON")
		}
		env.Entries = entries
		return writeJSONEnvelope(out, "thread", env)
	}
	env.Output = string(output)
	return renderThreadTranscript(out, env)
}

func runAMQThreadDefault(req threadAMQRequest) ([]byte, error) {
	args := []string{"thread", "--root", req.Root, "--id", req.Thread}
	if req.IncludeBody {
		args = append(args, "--include-body")
	}
	if req.Limit > 0 {
		args = append(args, "--limit", fmt.Sprint(req.Limit))
	}
	if req.JSON {
		args = append(args, "--json")
	}
	cmd := exec.Command("amq", args...)
	cmd.Env = envWithoutAMQIdentity(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return nil, fmt.Errorf("%w: %s", err, detail)
		}
		return nil, err
	}
	return out, nil
}

func renderThreadTranscript(out io.Writer, data threadEnvelopeData) error {
	fmt.Fprintln(out, "# amq-squad thread")
	fmt.Fprintf(out, "# project: %s\n", data.ProjectDir)
	fmt.Fprintf(out, "# session: %s\n", data.Session)
	fmt.Fprintf(out, "# root: %s\n", data.Root)
	fmt.Fprintf(out, "# thread: %s\n", data.Thread)
	fmt.Fprintln(out)
	output := strings.TrimRight(data.Output, "\n")
	if output == "" {
		output = "(no thread messages)"
	}
	_, err := fmt.Fprintln(out, output)
	return err
}
