package ksef

import (
	"io"
	"log/slog"

	"github.com/lwasak/fv-cli/internal/logging"
)

// redactedLogger writes through the process-wide redactor like the production logger.
func redactedLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(redactWriter{w}, nil))
}

type redactWriter struct{ w io.Writer }

func (r redactWriter) Write(p []byte) (int, error) {
	if _, err := r.w.Write([]byte(logging.Default.Redact(string(p)))); err != nil {
		return 0, err
	}
	return len(p), nil
}
