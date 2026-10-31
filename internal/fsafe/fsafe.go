// Package fsafe is the one place fv creates files and directories. Everything it creates
// is readable and writable by the current user only, with the protection applied at
// creation time (POSIX modes 0700/0600; a protected, single-ACE DACL on Windows).
package fsafe

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Renaming over a file that another process holds open fails on Windows with a sharing
// violation or "access denied" (Go opens files without FILE_SHARE_DELETE; virus scanners and
// the search indexer open files too). Such a failure is transient, so the rename is retried
// a few times with a short pause before it is reported.
const (
	renameAttempts = 20
	renameBackoff  = 50 * time.Millisecond
)

// Seams for tests.
var (
	renameFile = os.Rename
	// sleepFor is replaced in tests so retries do not wait.
	sleepFor = time.Sleep
	// retryableRename reports whether a rename error may be transient (platform specific).
	retryableRename = isTransientRenameError
	// syncParent makes the new directory entry durable (a no-op on Windows).
	syncParent = syncDir
)

// renameWithRetry runs rename, retrying while retryable says the error is transient, at
// most attempts times in total, pausing backoff between tries. It returns the last error.
func renameWithRetry(rename func(from, to string) error, retryable func(error) bool, sleep func(time.Duration),
	attempts int, backoff time.Duration, from, to string) error {
	var err error
	for i := 0; i < attempts; i++ {
		if err = rename(from, to); err == nil || !retryable(err) {
			return err
		}
		if i < attempts-1 {
			sleep(backoff)
		}
	}
	return err
}

// beforeRename is a test hook run after the temp file is synced and closed, just before
// the rename. A non-nil error aborts the write.
var beforeRename func() error

// CheckPrivate returns an error when path is readable by anyone but the current user (and,
// on Windows, SYSTEM and Administrators). Use it before reading key material. The error
// names the path and the offending mode or principal, never content.
func CheckPrivate(path string) error {
	return checkPrivate(path)
}

// privateMode refuses any group or other permission bit.
func privateMode(mode os.FileMode) error {
	if mode.Perm()&0o077 != 0 {
		return fmt.Errorf("mode %04o grants access to group or others; run chmod 600", mode.Perm())
	}
	return nil
}

// MkdirAll creates path and any missing parents, each accessible to the current user only.
func MkdirAll(path string) error {
	return mkdirAll(path)
}

// WriteFile atomically replaces path with data: it writes a user-only temp file in the same
// directory, fsyncs it, then renames it over the target. The temp file is removed on any
// failure and the previous content stays untouched.
func WriteFile(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Errorf("fsafe: temp name: %w", err)
	}
	tmp := filepath.Join(dir, "."+filepath.Base(path)+"."+hex.EncodeToString(suffix[:])+".tmp")
	f, err := createExclusive(tmp)
	if err != nil {
		return fmt.Errorf("fsafe: create temp file: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = f.Close()
		}
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return fmt.Errorf("fsafe: write %s: %w", tmp, err)
	}
	if err = f.Sync(); err != nil {
		return fmt.Errorf("fsafe: sync %s: %w", tmp, err)
	}
	closed = true
	if err = f.Close(); err != nil {
		return fmt.Errorf("fsafe: close %s: %w", tmp, err)
	}
	if beforeRename != nil {
		if err = beforeRename(); err != nil {
			return err
		}
	}
	if err = renameWithRetry(renameFile, retryableRename, sleepFor, renameAttempts, renameBackoff, tmp, path); err != nil {
		return fmt.Errorf("fsafe: replace %s: %w", path, err)
	}
	// The data is durable (synced above) but the new directory entry is not until the parent
	// directory is synced: without it a power loss can bring back the old file, for example
	// "draft" after a "sending" that was already POSTed.
	if err = syncParent(dir); err != nil {
		return fmt.Errorf("fsafe: sync directory of %s: %w", path, err)
	}
	return nil
}

// Rename renames from to to like os.Rename, retrying the transient sharing failures of
// Windows (see renameWithRetry).
func Rename(from, to string) error {
	return renameWithRetry(renameFile, retryableRename, sleepFor, renameAttempts, renameBackoff, from, to)
}

// OpenAppend opens path for appending, creating it user-only when missing. Existing files
// keep the protections they have.
func OpenAppend(path string) (*os.File, error) {
	return openAppend(path)
}
