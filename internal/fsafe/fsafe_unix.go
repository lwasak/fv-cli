//go:build !windows

package fsafe

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// isTransientRenameError: a rename on unix is not blocked by open handles, so nothing is retried.
func isTransientRenameError(error) bool { return false }

// syncDir fsyncs dir so a rename inside it survives a crash. Filesystems that cannot sync a
// directory (EINVAL, ENOTSUP) are not an error.
func syncDir(dir string) error {
	d, err := os.Open(dir) // #nosec G304 -- dir is the parent of a path built by fsafe callers
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	if err := d.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOTSUP) {
		return err
	}
	return nil
}

func mkdirAll(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	// Collect missing directories, then create each with an explicit chmod so the
	// result does not depend on the umask.
	var missing []string
	for p := abs; ; p = filepath.Dir(p) {
		if _, err := os.Stat(p); err == nil {
			break
		}
		missing = append(missing, p)
		if filepath.Dir(p) == p {
			break
		}
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err := os.Mkdir(missing[i], 0o700); err != nil && !os.IsExist(err) {
			return err
		}
		if err := os.Chmod(missing[i], 0o700); err != nil {
			return err
		}
	}
	return nil
}

func checkPrivate(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("fsafe: check %s: %w", path, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("fsafe: %s is a symbolic link; refusing to use it", path)
	}
	if err := privateMode(fi.Mode()); err != nil {
		return fmt.Errorf("fsafe: %s is readable by other users: %w", path, err)
	}
	return nil
}

func createExclusive(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- path is built by fsafe callers
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, err
	}
	return f, nil
}

func openAppend(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600) // #nosec G304 -- path is built by fsafe callers
	if err != nil {
		return nil, err
	}
	return f, nil
}
