//go:build !windows

package fsafe

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestCheckPrivate_0644Refused(t *testing.T) {
	p := filepath.Join(t.TempDir(), "k.key")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivate(p); err == nil {
		t.Fatal("expected refusal for mode 0644")
	}
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivate(p); err != nil {
		t.Fatalf("0600 refused: %v", err)
	}
}

func TestCheckPrivate_SymlinkRefused(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.key")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.key")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivate(link); err == nil {
		t.Fatal("expected refusal for a symlink")
	}
}

func TestPermissions_DirIs0700FileIs0600UnderUmask022(t *testing.T) {
	old := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(old) })

	root := filepath.Join(t.TempDir(), "x", "y")
	if err := MkdirAll(root); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(root, "c.toml")
	if err := WriteFile(f, []byte("k")); err != nil {
		t.Fatal(err)
	}
	l := filepath.Join(root, "fv.log")
	h, err := OpenAppend(l)
	if err != nil {
		t.Fatal(err)
	}
	_ = h.Close()

	for _, d := range []string{root, filepath.Dir(root)} {
		fi, _ := os.Stat(d)
		if fi.Mode().Perm() != 0o700 {
			t.Errorf("%s mode %o", d, fi.Mode().Perm())
		}
	}
	for _, p := range []string{f, l} {
		fi, _ := os.Stat(p)
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %o", p, fi.Mode().Perm())
		}
	}
}
