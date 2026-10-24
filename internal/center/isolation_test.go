package center

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeBin writes fake `amq-squad` and `amq` programs that log their argv, one
// line per call, and print canned output, then makes them the whole PATH.
// Acceptance 1: the renderer runs no amq-squad command other than the status
// call (and the contract's new-task argv).
func fakeBin(t *testing.T, statusJSON string) (logPath string) {
	t.Helper()
	dir := t.TempDir()
	logPath = filepath.Join(dir, "calls.log")
	data := filepath.Join(dir, "status.json")
	if err := os.WriteFile(data, []byte(statusJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	thread := filepath.Join(dir, "thread.json")
	if err := os.WriteFile(thread, []byte(`[{"id":"m1","from":"lead","to":["user"],"thread":"p2p/lead__user","created":"2026-10-24T11:00:00Z","body":"hi"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	script := func(name, canned string) {
		body := "#!/bin/sh\necho \"" + name + " $*\" >> '" + logPath + "'\n"
		if canned != "" {
			body += "/bin/cat '" + canned + "'\n"
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	script("amq-squad", data)
	script("amq", thread)
	t.Setenv("PATH", dir)
	return logPath
}

func readLog(t *testing.T, p string) []string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestClientRunsOnlyContractArgv(t *testing.T) {
	logPath := fakeBin(t, string(goldenFixture))
	c := Client{Runner: ExecRunner{}}
	ctx := context.Background()

	f, err := c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.NewTask(ctx, f.Squads[0].Actions.NewTask, map[string]string{"title": "Fix it", "class": "implement"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Conversation(ctx, "/root", "user", "lead", 10); err != nil {
		t.Fatal(err)
	}
	if err := c.Send(ctx, "/root", "user", "lead", "ok"); err != nil {
		t.Fatal(err)
	}
	got := readLog(t, logPath)
	want := []string{
		"amq-squad status --json",
		"amq-squad task add --root /Users/owner/.agent-mail/squad --title Fix it --class implement",
		"amq thread --id p2p/lead__user --me user --root /root --json --include-body --limit 10",
		"amq send --root /root --me user --to lead --thread p2p/lead__user --body ok",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestFleetStatusArgv(t *testing.T) {
	logPath := fakeBin(t, string(goldenFixture))
	if _, err := (Client{Runner: ExecRunner{}, Fleet: true}).Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := readLog(t, logPath); len(got) != 1 || got[0] != "amq-squad status --fleet --json" {
		t.Fatalf("calls = %v", got)
	}
}

func TestNewTaskArgvRefusals(t *testing.T) {
	a := testSquad("/p").Actions.NewTask
	if _, err := NewTaskArgv(a, map[string]string{"class": "implement"}); err == nil {
		t.Error("missing required title must fail")
	}
	if _, err := NewTaskArgv(a, map[string]string{"title": "x", "class": "bogus"}); err == nil {
		t.Error("class outside choices must fail")
	}
	bad := a
	bad.Argv = []string{"sh", "-c", "rm -rf /"}
	if _, err := NewTaskArgv(bad, map[string]string{"title": "x", "class": "review"}); err == nil {
		t.Error("a shell argv must be refused")
	}
	bad.Argv = []string{"amq-squad task add"}
	if _, err := NewTaskArgv(bad, map[string]string{"title": "x", "class": "review"}); err == nil {
		t.Error("a shell string must be refused")
	}
}

// The file-access trace of acceptance 1, done statically: the package opens
// no file, lists no directory, names no `.amq-squad` path, and starts
// processes in one place only (runner.go).
func TestPackageTouchesNoFiles(t *testing.T) {
	forbidden := map[string]bool{
		"Open": true, "OpenFile": true, "ReadFile": true, "ReadDir": true, "Stat": true, "Lstat": true,
		"Create": true, "CreateTemp": true, "WriteFile": true, "Mkdir": true, "MkdirAll": true,
		"Remove": true, "RemoveAll": true, "Rename": true, "Symlink": true, "Readlink": true,
		"Walk": true, "WalkDir": true, "Glob": true, "DirFS": true, "OpenRoot": true,
	}
	fset := token.NewFileSet()
	// The whole binary: this package, the CLI package and the main package.
	var matches []string
	for _, pat := range []string{"*.go", "../cli/*.go", "../../cmd/amq-noc/*.go"} {
		m, _ := filepath.Glob(pat)
		matches = append(matches, m...)
	}
	checked := 0
	for _, name := range matches {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if p == "io/ioutil" || p == "io/fs" || p == "os/exec" && !strings.HasSuffix(name, "runner.go") {
				t.Errorf("%s imports %s", name, p)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.BasicLit:
				if x.Kind == token.STRING && (strings.Contains(x.Value, ".amq-squad") || strings.Contains(strings.ToLower(x.Value), "tmux")) {
					t.Errorf("%s names a .amq-squad path or tmux: %s", name, x.Value)
				}
			case *ast.SelectorExpr:
				id, ok := x.X.(*ast.Ident)
				if !ok {
					return true
				}
				if (id.Name == "os" || id.Name == "filepath") && forbidden[x.Sel.Name] {
					t.Errorf("%s: %s.%s touches the file system", name, id.Name, x.Sel.Name)
				}
				if id.Name == "exec" && name != "runner.go" {
					t.Errorf("%s starts a process outside runner.go", name)
				}
			}
			return true
		})
	}
	if checked < 8 {
		t.Fatalf("only %d source files checked", checked)
	}
}
