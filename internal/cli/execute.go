package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/lwasak/fv-cli/internal/credentials"
	"github.com/lwasak/fv-cli/internal/exitcode"
	"github.com/lwasak/fv-cli/internal/ksef"
	"github.com/lwasak/fv-cli/internal/logging"
)

// errReported marks an error whose details were already printed by the command;
// Run only maps it to exit code 1.
var errReported = errors.New("reported")

// errCredential marks a refusal of the KSeF credential at login time (missing, expired, not
// yet valid, NIP mismatch, key readable by others, wrong or missing passphrase).
var errCredential = errors.New("KSeF credential refused")

// credentialError wraps a credential refusal: it prints as the cause and matches errCredential.
type credentialError struct{ err error }

func (e *credentialError) Error() string        { return e.err.Error() }
func (e *credentialError) Unwrap() error        { return e.err }
func (e *credentialError) Is(target error) bool { return target == errCredential }

// Exit codes.
const (
	exitError = 1
	exitAuth  = 3
)

// Run executes the command tree with args and returns the process exit code:
// 0 on success, 3 for a KSeF login or credential failure, 2/4/5/10 for errors tagged with
// an internal/exitcode sentinel, 1 for every other error.
func Run(deps Deps, args []string) int {
	return execute(deps, NewRootCmd(deps), args)
}

// execute runs root with args; printed errors pass through the secret redactor.
func execute(deps Deps, root *cobra.Command, args []string) int {
	root.SetArgs(args)
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if err := root.ExecuteContext(ctx); err != nil {
		if errors.Is(err, exitcode.ErrAborted) {
			fmt.Fprintln(deps.Stderr, "aborted, nothing was sent")
		} else if !errors.Is(err, errReported) {
			fmt.Fprintf(deps.Stderr, "fv: %s\n", logging.Default.Redact(err.Error()))
			var ke *ksef.Error
			if errors.As(err, &ke) {
				for _, d := range ke.Details {
					fmt.Fprintf(deps.Stderr, "  %s\n", logging.Default.Redact(d))
				}
			}
		}
		if errors.Is(err, ksef.ErrAuth) || errors.Is(err, errCredential) || errors.Is(err, credentials.ErrNoCredential) {
			return exitAuth
		}
		if c := exitcode.Code(err); c != 0 {
			return c
		}
		return exitError
	}
	return 0
}

// Execute runs fv with the real process arguments and returns the exit code.
func Execute() int {
	deps := DefaultDeps()
	ctx, stop := watchInterrupt(deps.Stderr)
	defer stop()
	deps.Ctx = ctx
	return Run(deps, os.Args[1:])
}
