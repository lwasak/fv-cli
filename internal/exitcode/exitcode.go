// Package exitcode maps sentinel errors to fv process exit codes, so packages can tag
// their errors without importing the cli package.
package exitcode

import "errors"

// Sentinel errors, one per exit code. Exit 3 (auth) keeps its mapping in the cli package.
var (
	ErrRegistry = errors.New("registry lookup failed") // exit 2
	ErrRejected = errors.New("rejected by KSeF")       // exit 4
	ErrUnknown  = errors.New("KSeF outcome unknown")   // exit 5
	ErrAborted  = errors.New("aborted")                // exit 10
)

// Exit codes.
const (
	CodeRegistry = 2
	CodeRejected = 4
	CodeUnknown  = 5
	CodeAborted  = 10
)

// Code returns the exit code for err: 0 for nil, 2/4/5/10 for the sentinels (matched with
// errors.Is), and 0 for any other error, meaning "not classified".
func Code(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrAborted):
		return CodeAborted
	case errors.Is(err, ErrUnknown):
		return CodeUnknown
	case errors.Is(err, ErrRejected):
		return CodeRejected
	case errors.Is(err, ErrRegistry):
		return CodeRegistry
	}
	return 0
}

type tagged struct {
	sentinel error
	err      error
}

func (e *tagged) Error() string   { return e.err.Error() }
func (e *tagged) Unwrap() []error { return []error{e.sentinel, e.err} }

// Wrap tags err with sentinel: errors.Is matches both the sentinel and the cause, and
// Error() is the cause text unchanged. A nil err gives nil.
func Wrap(sentinel, err error) error {
	if err == nil {
		return nil
	}
	return &tagged{sentinel: sentinel, err: err}
}
