package cli

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-noc/internal/launch"
)

func captureOutput(t *testing.T, fn func() error) (string, string, error) {
	t.Helper()
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	os.Stdout = stdoutW
	os.Stderr = stderrW
	type readResult struct {
		data []byte
		err  error
	}
	stdoutCh := make(chan readResult, 1)
	stderrCh := make(chan readResult, 1)
	go func() {
		data, err := io.ReadAll(stdoutR)
		stdoutCh <- readResult{data: data, err: err}
	}()
	go func() {
		data, err := io.ReadAll(stderrR)
		stderrCh <- readResult{data: data, err: err}
	}()
	runErr := fn()
	if err := stdoutW.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderrW.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = oldStdout
	os.Stderr = oldStderr

	stdout := <-stdoutCh
	if stdout.err != nil {
		t.Fatal(stdout.err)
	}
	stderr := <-stderrCh
	if stderr.err != nil {
		t.Fatal(stderr.err)
	}
	return string(stdout.data), string(stderr.data), runErr
}

func setupFakeAMQSessionRoots(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, ".agent-mail")
	script := `#!/bin/sh
if [ "$1" != "env" ]; then
  echo "unexpected amq command: $*" >&2
  exit 1
fi
session=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --session)
      shift
      session="$1"
      ;;
  esac
  shift
done
root="$AMQ_FAKE_BASE"
if [ "$session" != "" ]; then
  root="$root/$session"
fi
printf '{"root":"%s"}\n' "$root"
`
	if err := os.WriteFile(filepath.Join(binDir, "amq"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMQ_FAKE_BASE", base)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return base
}

func seedAgentRecord(t *testing.T, base, workstream, handle string, rec launch.Record) string {
	t.Helper()
	agentDir := filepath.Join(base, workstream, "agents", handle)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	return agentDir
}
