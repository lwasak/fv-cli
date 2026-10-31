//go:build !windows

package fsafe

import (
	"errors"
	"os"
	"syscall"
)

type osLock struct{ f *os.File }

func openLock(path string) (*osLock, error) {
	f, err := openAppend(path)
	if err != nil {
		return nil, err
	}
	return &osLock{f: f}, nil
}

// try attempts a non-blocking exclusive flock; false means another holder has it.
func (l *osLock) try() (bool, error) {
	for {
		err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, syscall.EINTR):
			continue
		case errors.Is(err, syscall.EWOULDBLOCK):
			return false, nil
		default:
			return false, err
		}
	}
}

func (l *osLock) release() error {
	err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	if cerr := l.f.Close(); err == nil {
		err = cerr
	}
	return err
}

func (l *osLock) close() error { return l.f.Close() }
