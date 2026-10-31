//go:build windows

package fsafe

import (
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func assertProtectedCurrentUserOnly(t *testing.T, path string) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	ctrl, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if ctrl&windows.SE_DACL_PROTECTED == 0 {
		t.Errorf("%s: DACL is not protected (inherits)", path)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("%s: no DACL: %v", path, err)
	}
	if dacl.AceCount != 1 {
		t.Errorf("%s: expected 1 ACE, got %d", path, dacl.AceCount)
	}
	tok := windows.GetCurrentProcessToken()
	user, err := tok.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
		t.Errorf("%s: ACE type %d", path, ace.Header.AceType)
	}
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !sid.Equals(user.User.Sid) {
		t.Errorf("%s: ACE SID %s is not the current user %s", path, sid, user.User.Sid)
	}
}

// addReadACE adds an allow-read ACE for the SID string to the file's DACL (keeping the
// existing entries).
func addReadACE(t *testing.T, path, sidStr string) {
	t.Helper()
	sid, err := windows.StringToSid(sidStr)
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	old, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	entries := []windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_READ,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}}
	dacl, err := windows.ACLFromEntries(entries, old)
	if err != nil {
		t.Fatal(err)
	}
	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestCheckPrivate_FsafeFileAccepted(t *testing.T) {
	p := filepath.Join(t.TempDir(), "k.key")
	if err := WriteFile(p, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivate(p); err != nil {
		t.Fatalf("fsafe file refused: %v", err)
	}
}

func TestCheckPrivate_EveryoneReadRefused(t *testing.T) {
	p := filepath.Join(t.TempDir(), "k.key")
	if err := WriteFile(p, []byte("x")); err != nil {
		t.Fatal(err)
	}
	addReadACE(t, p, "S-1-1-0")
	if err := CheckPrivate(p); err == nil {
		t.Fatal("expected refusal with Everyone read ACE")
	}
}

func TestCheckPrivate_UsersGroupRefused(t *testing.T) {
	p := filepath.Join(t.TempDir(), "k.key")
	if err := WriteFile(p, []byte("x")); err != nil {
		t.Fatal(err)
	}
	addReadACE(t, p, "S-1-5-32-545")
	if err := CheckPrivate(p); err == nil {
		t.Fatal("expected refusal with Users read ACE")
	}
}

func TestACL_FileAndDirProtectedCurrentUserOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "d", "e")
	if err := MkdirAll(dir); err != nil {
		t.Fatal(err)
	}
	assertProtectedCurrentUserOnly(t, dir)
	assertProtectedCurrentUserOnly(t, filepath.Dir(dir))
	f := filepath.Join(dir, "c.toml")
	if err := WriteFile(f, []byte("k")); err != nil {
		t.Fatal(err)
	}
	assertProtectedCurrentUserOnly(t, f)
	l := filepath.Join(dir, "fv.log")
	h, err := OpenAppend(l)
	if err != nil {
		t.Fatal(err)
	}
	_ = h.Close()
	assertProtectedCurrentUserOnly(t, l)
}

func TestACL_TempFileProtectedBeforeRename(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "d")
	if err := MkdirAll(dir); err != nil {
		t.Fatal(err)
	}
	checked := false
	beforeRename = func() error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			assertProtectedCurrentUserOnly(t, filepath.Join(dir, e.Name()))
			checked = true
		}
		return nil
	}
	t.Cleanup(func() { beforeRename = nil })
	if err := WriteFile(filepath.Join(dir, "c.toml"), []byte("k")); err != nil {
		t.Fatal(err)
	}
	if !checked {
		t.Fatal("no temp file observed before rename")
	}
}

// S4 on the real thing: a reader holds meta.json open for a moment; the replace waits for it.
func TestWriteFile_WaitsForAnOpenReader(t *testing.T) {
	p := filepath.Join(t.TempDir(), "meta.json")
	if err := WriteFile(p, []byte("old")); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = f.Close()
	}()
	if err := WriteFile(p, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); string(got) != "new" {
		t.Errorf("got %q", got)
	}
}
