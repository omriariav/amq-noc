package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

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

func runBrief(args []string) error {
	if len(args) > 0 && args[0] == "seed" {
		return runBriefSeed(args[1:])
	}
	fs := flag.NewFlagSet("brief", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory to inspect (default: cwd)")
	sessionFlag := fs.String("session", "", "AMQ workstream session name")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned brief envelope instead of the human report")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad brief - print a workstream brief

Usage:
  amq-squad brief --session NAME [--project DIR] [--json]
  amq-squad brief seed --session NAME --seed-from REF [--project DIR] [--force] [--dry-run] [--json]

Reads .amq-squad/briefs/<session>.md from the selected team-home and reports
whether the brief is missing, still the generated stub, or filled in.

Examples:
  amq-squad brief --session issue-96
  amq-squad brief --project ~/Code/app --session issue-96
  amq-squad brief --session issue-96 --json | jq .
  amq-squad brief seed --session issue-96 --seed-from issue:31
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("brief takes no positional arguments; use --session NAME")
	}
	if !flagWasSet(fs, "session") || strings.TrimSpace(*sessionFlag) == "" {
		return usageErrorf("brief requires --session NAME")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	projectDir, err := resolveProjectDirFlag(cwd, *projectFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	data, err := readBriefData(projectDir, *sessionFlag)
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeJSONEnvelope(os.Stdout, "brief", data)
	}
	writeBriefReport(os.Stdout, data)
	return nil
}

func runBriefSeed(args []string) error {
	fs := flag.NewFlagSet("brief seed", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory to inspect (default: cwd)")
	sessionFlag := fs.String("session", "", "AMQ workstream session name")
	seedFrom := fs.String("seed-from", "", "brief seed source: file:<path>, issue:<n>, or gh:owner/repo#<n>")
	force := fs.Bool("force", false, "overwrite an existing brief")
	dryRun := fs.Bool("dry-run", false, "print the candidate brief without writing it")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned brief_seed envelope instead of the human report")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad brief seed - write a workstream brief from a deterministic source

Usage:
  amq-squad brief seed --session NAME --seed-from REF [--project DIR] [--force] [--dry-run] [--json]

Seed sources:
  file:<path>            literal file body
  issue:<n>              gh issue view <n> in the current repo
  gh:<owner>/<repo>#<n>  gh issue view <n> --repo owner/repo

Without --force, an existing brief is preserved and the command fails. With
--dry-run, the candidate brief is rendered but nothing is written.

Examples:
  amq-squad brief seed --session issue-96 --seed-from issue:31
  amq-squad brief seed --project ~/Code/app --session issue-96 --seed-from file:./brief.md
  amq-squad brief seed --session issue-96 --seed-from gh:owner/repo#31 --dry-run --json | jq .
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("brief seed takes no positional arguments; use --session NAME --seed-from REF")
	}
	if !flagWasSet(fs, "session") || strings.TrimSpace(*sessionFlag) == "" {
		return usageErrorf("brief seed requires --session NAME")
	}
	if !flagWasSet(fs, "seed-from") || strings.TrimSpace(*seedFrom) == "" {
		return usageErrorf("brief seed requires --seed-from REF")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	projectDir, err := resolveProjectDirFlag(cwd, *projectFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	data, err := seedBriefData(projectDir, *sessionFlag, *seedFrom, *force, *dryRun)
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeJSONEnvelope(os.Stdout, "brief_seed", data)
	}
	writeBriefSeedReport(os.Stdout, data)
	return nil
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

func writeBriefReport(out io.Writer, data briefEnvelopeData) {
	fmt.Fprintln(out, "# amq-squad brief")
	fmt.Fprintf(out, "# project: %s\n", data.ProjectDir)
	fmt.Fprintf(out, "# session: %s\n", data.Session)
	fmt.Fprintf(out, "# path: %s\n", data.Path)
	fmt.Fprintf(out, "# kind: %s\n", data.Kind)
	fmt.Fprintln(out)
	content := strings.TrimRight(data.Content, "\n")
	if content == "" {
		content = "(no brief)"
	}
	fmt.Fprintln(out, content)
}

func writeBriefSeedReport(out io.Writer, data briefSeedEnvelopeData) {
	fmt.Fprintln(out, "# amq-squad brief seed")
	fmt.Fprintf(out, "# project: %s\n", data.ProjectDir)
	fmt.Fprintf(out, "# session: %s\n", data.Session)
	fmt.Fprintf(out, "# path: %s\n", data.Path)
	fmt.Fprintf(out, "# source: %s\n", data.Source)
	fmt.Fprintf(out, "# generated_at: %s\n", data.GeneratedAt)
	if data.DryRun {
		fmt.Fprintln(out, "# mode: dry-run")
	} else {
		fmt.Fprintln(out, "# mode: written")
	}
	fmt.Fprintf(out, "# force: %t\n", data.Force)
	fmt.Fprintln(out)
	fmt.Fprint(out, data.Content)
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
