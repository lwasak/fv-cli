//go:build windows

package fsafe

import (
	"os"

	"golang.org/x/sys/windows"
)

type osLock struct{ f *os.File }

func openLock(path string) (*osLock, error) {
	// LockFileEx needs a handle with GENERIC_READ or GENERIC_WRITE access.
	f, err := createFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, windows.OPEN_ALWAYS)
	if err != nil {
		return nil, err
	}
	return &osLock{f: f}, nil
}

// try attempts a non-blocking exclusive lock of byte 0; false means another holder has it.
func (l *osLock) try() (bool, error) {
	ol := new(windows.Overlapped)
	err := windows.LockFileEx(windows.Handle(l.f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	switch err {
	case nil:
		return true, nil
	case windows.ERROR_LOCK_VIOLATION, windows.ERROR_IO_PENDING:
		return false, nil
	default:
		return false, err
	}
}

func (l *osLock) release() error {
	ol := new(windows.Overlapped)
	err := windows.UnlockFileEx(windows.Handle(l.f.Fd()), 0, 1, 0, ol)
	if cerr := l.f.Close(); err == nil {
		err = cerr
	}
	return err
}

func (l *osLock) close() error { return l.f.Close() }
