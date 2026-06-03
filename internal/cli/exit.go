package cli

import (
	"errors"
	"flag"
)

// Exit-code taxonomy for amq-squad (epic #31, Step 11D):
//
//	0 ExitSuccess - success.
//	1 ExitUser    - user/usage error (UsageError, unknown flag, bad input).
//	2 ExitSystem  - system/runtime error (IO/process/env failure, panics
//	                surfaced as errors, anything the user cannot fix by
//	                changing arguments).
//	3 ExitPartial - partial success (PartialError: some targets succeeded,
//	                some failed; e.g. `stop` with mixed stopped + failed).
//
// Bump only on a breaking change; callers (CI scripts, dashboards) should
// be able to rely on these constants across 1.x.
const (
	ExitSuccess = 0
	ExitUser    = 1
	ExitSystem  = 2
	ExitPartial = 3
)

// PartialError signals partial success: the command made progress on some
// targets and explicitly reported per-target failure for the rest. main
// maps it to ExitPartial so wrapper scripts can tell "all-failed" (system
// error) from "mixed" (partial). Cause, when non-nil, lets callers attach
// the underlying error so errors.Is/As traversal keeps working.
type PartialError struct {
	Message string
	Cause   error
}

func (e *PartialError) Error() string { return e.Message }

// Unwrap returns the wrapped cause, if any, so errors.Is/As reach it.
func (e *PartialError) Unwrap() error { return e.Cause }

// ExitCode classifies err into the amq-squad exit-code taxonomy. nil is
// success; PartialError is partial success; UsageError is user error;
// anything else is a system/runtime error.
//
// PartialError is checked BEFORE UsageError so an outer PartialError that
// happens to wrap a UsageError (e.g. one target failed because of a usage
// problem on its row) still classifies as ExitPartial. The outer error
// type signals the operator's intent; wrapping a user-error cause must not
// flip the whole command to "user error".
func ExitCode(err error) int {
	if err == nil {
		return ExitSuccess
	}
	var pe *PartialError
	if errors.As(err, &pe) {
		return ExitPartial
	}
	var ue UsageError
	if errors.As(err, &ue) {
		return ExitUser
	}
	return ExitSystem
}

// parseFlags is the shared flag-parse helper. flag.ErrHelp bubbles up so
// Run can swallow it and exit 0; every other parse failure (unknown flag,
// malformed value) is wrapped as a UsageError so main exits via the user
// path.
func parseFlags(fs *flag.FlagSet, args []string) error {
	err := fs.Parse(args)
	if err == nil {
		return nil
	}
	if errors.Is(err, flag.ErrHelp) {
		return err
	}
	return UsageError(err.Error())
}
