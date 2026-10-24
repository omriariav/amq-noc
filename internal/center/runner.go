package center

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Runner is the only way the command center touches the outside world. It
// runs a program from a PATH lookup with an argv array, never a shell.
type Runner interface {
	Run(ctx context.Context, argv []string) (stdout []byte, err error)
}

// ExecRunner runs argv with os/exec after checking it against the allowlist.
type ExecRunner struct {
	Timeout time.Duration
}

// Run implements Runner.
func (r ExecRunner) Run(ctx context.Context, argv []string) ([]byte, error) {
	if err := checkProgram(argv); err != nil {
		return nil, err
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.Bytes(), fmt.Errorf("%s: %s", filepath.Base(argv[0]), firstLine(msg))
	}
	return out.Bytes(), nil
}

// allowedPrograms are the only programs the command center starts.
var allowedPrograms = map[string]bool{"amq-squad": true, "amq": true}

// checkProgram admits only a bare "amq-squad" or "amq" in argv[0]. A path
// form, a relative form or any other name is refused before exec.
func checkProgram(argv []string) error {
	if len(argv) == 0 || argv[0] == "" {
		return errors.New("runner: empty argv")
	}
	if !allowedPrograms[argv[0]] {
		return fmt.Errorf("runner: program not allowed: %s", argv[0])
	}
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// StatusArgv is the renderer's single input call. Fleet selects the fleet
// form (plan 16.3), which comes from amq-squad 4-3.
func StatusArgv(fleet bool) []string {
	if fleet {
		return []string{"amq-squad", "status", "--fleet", "--json"}
	}
	return []string{"amq-squad", "status", "--json"}
}

// NewTaskArgv composes the argv of the contract's "new task" action: the
// action's own argv plus, for each input the user gave, its flag and value.
// It does not know what the command is; it refuses an action whose argv is
// not a bare program name followed by arguments (plan 4.6), a required input
// that is empty, and a value outside the input's choices.
func NewTaskArgv(a Action, values map[string]string) ([]string, error) {
	if err := validateAction(a); err != nil {
		return nil, err
	}
	argv := append([]string(nil), a.Argv...)
	for _, in := range a.Inputs {
		v := strings.TrimSpace(values[in.Name])
		if v == "" {
			if in.Required {
				return nil, fmt.Errorf("%s is required", in.Name)
			}
			continue
		}
		if len(in.Choices) > 0 && !contains(in.Choices, v) {
			return nil, fmt.Errorf("%s must be one of %s", in.Name, strings.Join(in.Choices, ", "))
		}
		argv = append(argv, in.Flag+"="+v)
	}
	return argv, nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// validateAction is the argv rule of plan 4.6 as the renderer enforces it:
// a stable id, a bare program name in argv[0], and no shell interpreter.
func validateAction(a Action) error {
	if strings.TrimSpace(a.ID) == "" {
		return errors.New("the status has no new-task action")
	}
	if err := checkProgram(a.Argv); err != nil {
		return err
	}
	if len(a.Argv) == 0 || a.Argv[0] == "" || strings.ContainsAny(a.Argv[0], " \t\n;|&$`'\"<>()/\\") {
		return fmt.Errorf("action %s: argv[0] is not a bare program name", a.ID)
	}
	switch a.Argv[0] {
	case "sh", "bash", "zsh", "dash", "fish", "env", "eval", "cmd", "powershell":
		return fmt.Errorf("action %s: argv runs a shell", a.ID)
	}
	for _, in := range a.Inputs {
		if in.Name == "" || !strings.HasPrefix(in.Flag, "--") {
			return fmt.Errorf("action %s: input %q needs a name and a --flag", a.ID, in.Name)
		}
	}
	return nil
}
