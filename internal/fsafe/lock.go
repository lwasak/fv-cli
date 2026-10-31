package fsafe

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrLockTimeout is returned (wrapped) by Lock when the lock stayed held by another
// process for the whole timeout.
var ErrLockTimeout = errors.New("fsafe: timed out waiting for lock")

// lockPoll is how often a held lock is retried.
const lockPoll = 50 * time.Millisecond

// Lock takes an exclusive OS lock on the file at path (created user-only when missing),
// waiting up to timeout. The lock is advisory between fv processes and is released by the
// returned unlock function, or by the OS when the process exits or crashes, so there are
// no stale lock files. unlock is safe to call more than once.
func Lock(path string, timeout time.Duration) (unlock func() error, err error) {
	return LockContext(context.Background(), path, timeout)
}

// LockContext is Lock that stops waiting when ctx ends (Ctrl-C) and returns ctx's error.
func LockContext(ctx context.Context, path string, timeout time.Duration) (unlock func() error, err error) {
	if timeout < 0 {
		timeout = 0
	}
	deadline := time.Now().Add(timeout)
	l, err := openLock(path)
	if err != nil {
		return nil, fmt.Errorf("fsafe: open lock file %s: %w", path, err)
	}
	for {
		ok, err := l.try()
		if err != nil {
			_ = l.close()
			return nil, fmt.Errorf("fsafe: lock %s: %w", path, err)
		}
		if ok {
			break
		}
		if !time.Now().Before(deadline) {
			_ = l.close()
			return nil, fmt.Errorf("%w: %s (held by another fv process)", ErrLockTimeout, path)
		}
		select {
		case <-ctx.Done():
			_ = l.close()
			return nil, fmt.Errorf("fsafe: waiting for lock %s: %w", path, ctx.Err())
		case <-time.After(lockPoll):
		}
	}
	done := false
	return func() error {
		if done {
			return nil
		}
		done = true
		return l.release()
	}, nil
}
