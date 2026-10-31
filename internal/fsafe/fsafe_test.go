package fsafe

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPrivateMode(t *testing.T) {
	cases := []struct {
		mode    os.FileMode
		refused bool
	}{
		{0o600, false}, {0o400, false},
		{0o640, true}, {0o604, true}, {0o660, true}, {0o644, true}, {0o610, true},
	}
	for _, c := range cases {
		err := privateMode(c.mode)
		if (err != nil) != c.refused {
			t.Errorf("mode %o: err = %v, refused want %v", c.mode, err, c.refused)
		}
	}
}

func TestCheckPrivate_MissingFileIsError(t *testing.T) {
	if err := CheckPrivate(filepath.Join(t.TempDir(), "nope.key")); err == nil {
		t.Fatal("expected error for a missing file")
	}
}

func TestWriteFile_ReplacesContent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.toml")
	if err := WriteFile(p, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(p, []byte("two")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "two" {
		t.Fatalf("got %q", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("expected only the target file, got %d entries", len(entries))
	}
}

func TestWriteFile_FailureBeforeRenameKeepsOriginalAndNoTemp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.toml")
	if err := WriteFile(p, []byte("original")); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	beforeRename = func() error { return boom }
	t.Cleanup(func() { beforeRename = nil })

	err := WriteFile(p, []byte("new"))
	if !errors.Is(err, boom) {
		t.Fatalf("expected boom, got %v", err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "original" {
		t.Fatalf("original changed: %q", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %d entries", len(entries))
	}
}

func TestMkdirAll_CreatesNested(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a", "b", "c")
	if err := MkdirAll(p); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
		t.Fatalf("not a dir: %v", err)
	}
	if err := MkdirAll(p); err != nil {
		t.Fatalf("second call: %v", err)
	}
}

func TestOpenAppend_Appends(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fv.log")
	for _, s := range []string{"a\n", "b\n"} {
		f, err := OpenAppend(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(s); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := os.ReadFile(p)
	if string(got) != "a\nb\n" {
		t.Fatalf("got %q", got)
	}
}

var errBusy = errors.New("sharing violation (test)")

// S4: a rename that fails transiently (a reader holds the file open on Windows) is retried.
func TestWriteFile_RetriesTransientRenameFailure(t *testing.T) {
	var calls, sleeps int
	oldR, oldRetry, oldSleep := renameFile, retryableRename, sleepFor
	t.Cleanup(func() { renameFile, retryableRename, sleepFor = oldR, oldRetry, oldSleep })
	renameFile = func(from, to string) error {
		calls++
		if calls <= 3 {
			return errBusy
		}
		return os.Rename(from, to)
	}
	retryableRename = func(err error) bool { return errors.Is(err, errBusy) }
	sleepFor = func(time.Duration) { sleeps++ }

	p := filepath.Join(t.TempDir(), "meta.json")
	if err := WriteFile(p, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); string(got) != "new" || calls != 4 || sleeps != 3 {
		t.Errorf("content %q, rename calls %d, sleeps %d", got, calls, sleeps)
	}
}

func TestRenameWithRetry_GivesUpAndSkipsPermanentErrors(t *testing.T) {
	always := func(error) bool { return true }
	never := func(error) bool { return false }
	var calls int
	fail := func(string, string) error { calls++; return errBusy }
	noSleep := func(time.Duration) {}

	if err := renameWithRetry(fail, always, noSleep, 5, time.Millisecond, "a", "b"); !errors.Is(err, errBusy) || calls != 5 {
		t.Errorf("transient: err %v after %d calls, want errBusy after 5", err, calls)
	}
	calls = 0
	if err := renameWithRetry(fail, never, noSleep, 5, time.Millisecond, "a", "b"); !errors.Is(err, errBusy) || calls != 1 {
		t.Errorf("permanent: err %v after %d calls, want 1", err, calls)
	}
}

func TestWriteFile_TemporaryFileRemovedWhenRenameGivesUp(t *testing.T) {
	oldR, oldRetry, oldSleep := renameFile, retryableRename, sleepFor
	t.Cleanup(func() { renameFile, retryableRename, sleepFor = oldR, oldRetry, oldSleep })
	renameFile = func(string, string) error { return errBusy }
	retryableRename = func(error) bool { return true }
	sleepFor = func(time.Duration) {}
	dir := t.TempDir()
	if err := WriteFile(filepath.Join(dir, "a"), []byte("x")); !errors.Is(err, errBusy) {
		t.Fatalf("err = %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("leftovers: %v", entries)
	}
}

// S5: the parent directory is synced after the rename (and not when the rename failed).
func TestWriteFile_SyncsParentDirectoryAfterRename(t *testing.T) {
	var synced []string
	old := syncParent
	t.Cleanup(func() { syncParent = old })
	syncParent = func(dir string) error {
		synced = append(synced, dir)
		return nil
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "meta.json")
	if err := WriteFile(p, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if len(synced) != 1 || synced[0] != dir {
		t.Errorf("synced = %v, want [%s]", synced, dir)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
}

func TestSyncDir_Works(t *testing.T) {
	if err := syncDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}
