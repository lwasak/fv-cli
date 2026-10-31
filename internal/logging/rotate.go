package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/lwasak/fv-cli/internal/fsafe"
)

const (
	// DefaultLimit is the size at which fv.log is rotated.
	DefaultLimit = 5 << 20
	// keep is how many rotated files (fv.log.1..keep) are retained.
	keep = 3
)

// rotator is an io.WriteCloser appending to path. The file (and its directory) is
// created on the first write; at limit bytes it is rotated to path.1..path.3.
type rotator struct {
	path  string
	limit int64

	mu   sync.Mutex
	f    *os.File
	size int64
}

func newRotator(path string, limit int64) *rotator {
	return &rotator{path: path, limit: limit}
}

func (r *rotator) open() error {
	if err := fsafe.MkdirAll(filepath.Dir(r.path)); err != nil {
		return err
	}
	f, err := fsafe.OpenAppend(r.path)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	r.f, r.size = f, st.Size()
	return nil
}

func (r *rotator) rotate() error {
	if r.f != nil {
		if err := r.f.Close(); err != nil {
			return err
		}
		r.f = nil
	}
	_ = os.Remove(fmt.Sprintf("%s.%d", r.path, keep))
	for i := keep - 1; i >= 1; i-- {
		if err := os.Rename(fmt.Sprintf("%s.%d", r.path, i), fmt.Sprintf("%s.%d", r.path, i+1)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Rename(r.path, r.path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Write appends p, rotating first when it would push the file past the limit.
func (r *rotator) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		if err := r.open(); err != nil {
			return 0, err
		}
	}
	if r.size > 0 && r.size+int64(len(p)) > r.limit {
		if err := r.rotate(); err != nil {
			return 0, err
		}
		if err := r.open(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

// Close closes the file if it was opened.
func (r *rotator) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}
