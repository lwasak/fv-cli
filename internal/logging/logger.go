package logging

import (
	"io"
	"log/slog"
	"path/filepath"
)

// FileName is the log file name inside the data directory.
const FileName = "fv.log"

// redactingWriter passes each Write (one slog record) through a Redactor.
type redactingWriter struct {
	w io.Writer
	r *Redactor
}

func (rw redactingWriter) Write(p []byte) (int, error) {
	if _, err := rw.w.Write([]byte(rw.r.Redact(string(p)))); err != nil {
		return 0, err
	}
	return len(p), nil
}

// New returns an info-level logger writing to dataDir/fv.log through Default. Nothing is
// created until the first record is written. The returned func closes the file.
func New(dataDir string) (*slog.Logger, func()) {
	return newWith(dataDir, DefaultLimit, Default)
}

func newWith(dataDir string, limit int64, r *Redactor) (*slog.Logger, func()) {
	rot := newRotator(filepath.Join(dataDir, FileName), limit)
	h := slog.NewTextHandler(redactingWriter{w: rot, r: r}, &slog.HandlerOptions{Level: slog.LevelInfo})
	return slog.New(redactingHandler{next: h, r: r}), func() { _ = rot.Close() }
}
