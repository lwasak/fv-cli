//go:build !windows

package credentials_test

import (
	"os"
	"testing"
)

// makeReadableByOthers gives group and others read access to path.
func makeReadableByOthers(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
}
