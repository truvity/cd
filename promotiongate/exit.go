package promotiongate

import (
	"context"
	"errors"
	"time"
)

// Exit statuses the scripts ended with, kept so a caller that reads the code
// (Argo Rollouts reads only zero or not) sees what it saw before.
const (
	// ExitFail is a verdict: the check failed.
	ExitFail = 1
	// ExitUsage is a missing or malformed input, the status a POSIX shell
	// exits with on `${VAR:?}`, `set -u` or a bad arithmetic expansion.
	ExitUsage = 2
	// ExitJQ is a response jq could not process (not JSON, or an index into
	// the wrong type), which ended the script under `set -e`.
	ExitJQ = 5
)

// ExitError is a failed check with the exit status the script gave it. Its
// message has already been logged by the check.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }

func (e *ExitError) Unwrap() error { return e.Err }

// ExitCode is the process exit status for the result of a check: 0 for nil,
// the script's own status for an [ExitError], [ExitFail] for anything else.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}

	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}

	return ExitFail
}

func exitf(code int, err error) error { return &ExitError{Code: code, Err: err} }

// sleepContext is the default sleeper: the wall clock, cut short by ctx.
func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
