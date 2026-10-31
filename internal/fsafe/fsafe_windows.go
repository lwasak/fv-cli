//go:build windows

package fsafe

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// isTransientRenameError: another process has the target open (sharing violation or access
// denied while a handle without FILE_SHARE_DELETE exists).
func isTransientRenameError(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED)
}

// syncDir is a no-op: NTFS journals the rename, and a directory cannot be flushed this way.
func syncDir(string) error { return nil }

// securityAttributes builds SECURITY_ATTRIBUTES whose DACL is protected (no inheritance
// from the parent) and holds one allow ACE for the current user. dir selects whether the
// ACE is inherited by children.
func sa(dir bool) (*windows.SecurityAttributes, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("fsafe: current user: %w", err)
	}
	flags := ""
	if dir {
		flags = "OICI"
	}
	sddl := fmt.Sprintf("D:P(A;%s;FA;;;%s)", flags, user.User.Sid.String())
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, fmt.Errorf("fsafe: security descriptor: %w", err)
	}
	s := &windows.SecurityAttributes{SecurityDescriptor: sd}
	s.Length = uint32(unsafe.Sizeof(*s))
	return s, nil
}

func mkdirAll(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
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
		s, err := sa(true)
		if err != nil {
			return err
		}
		p16, err := windows.UTF16PtrFromString(missing[i])
		if err != nil {
			return err
		}
		if err := windows.CreateDirectory(p16, s); err != nil && err != windows.ERROR_ALREADY_EXISTS {
			return &os.PathError{Op: "mkdir", Path: missing[i], Err: err}
		}
	}
	return nil
}

func createFile(path string, access, share, disposition uint32) (*os.File, error) {
	s, err := sa(false)
	if err != nil {
		return nil, err
	}
	p16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p16, access, share, s, disposition, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

const (
	fileReadData  = 0x0001
	fileAllAccess = 0x1F01FF
	inheritOnly   = 0x08
)

func checkPrivate(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("fsafe: current user: %w", err)
	}
	system, err := windows.StringToSid("S-1-5-18")
	if err != nil {
		return err
	}
	admins, err := windows.StringToSid("S-1-5-32-544")
	if err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("fsafe: check %s: %w", path, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return fmt.Errorf("fsafe: %s has no access control list (open to everyone)", path)
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return fmt.Errorf("fsafe: check %s: %w", path, err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags&inheritOnly != 0 {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)) // #nosec G103 -- ACE layout per the Windows ACCESS_ALLOWED_ACE definition
		if sid.Equals(user.User.Sid) || sid.Equals(system) || sid.Equals(admins) {
			continue
		}
		const readBits = fileReadData | windows.GENERIC_READ | windows.GENERIC_ALL | fileAllAccess
		if ace.Mask&readBits != 0 {
			return fmt.Errorf("fsafe: %s is readable by other users: %s has read access", path, sid.String())
		}
	}
	return nil
}

func createExclusive(path string) (*os.File, error) {
	return createFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ, windows.CREATE_NEW)
}

func openAppend(path string) (*os.File, error) {
	return createFile(path, windows.FILE_APPEND_DATA|windows.SYNCHRONIZE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, windows.OPEN_ALWAYS)
}
