//go:build windows

package cache

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// This file is UNTESTED ON REAL WINDOWS: it compiles and vets for
// windows/amd64 and windows/arm64 only. It implements SR4 with the Win32
// security APIs: an owner-only protected DACL on the cache directory and on
// every file, an owner check, and opening with FILE_FLAG_OPEN_REPARSE_POINT
// followed by an attribute re-check on the open handle, which closes the
// window an Lstat-then-open check leaves.

// currentUser returns the SID of the user running the process.
func currentUser() (*windows.SID, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("read the current user: %w", err)
	}
	return u.User.Sid, nil
}

// trustedOwners returns the SIDs accepted as the owner of cache objects: the
// current user and the token's default owner (BUILTIN\Administrators in an
// elevated process, where new objects are owned by that group).
func trustedOwners() ([]*windows.SID, error) {
	user, err := currentUser()
	if err != nil {
		return nil, err
	}
	owners := []*windows.SID{user}
	if o := tokenDefaultOwner(); o != nil {
		owners = append(owners, o)
	}
	return owners, nil
}

// tokenOwnerInfo mirrors the Win32 TOKEN_OWNER structure.
type tokenOwnerInfo struct {
	Owner *windows.SID
}

// tokenDefaultOwner returns the default owner SID of new objects created by
// the process token, or nil when it cannot be read.
func tokenDefaultOwner() *windows.SID {
	t := windows.GetCurrentProcessToken()
	var n uint32
	_ = windows.GetTokenInformation(t, windows.TokenOwner, nil, 0, &n)
	if n == 0 {
		return nil
	}
	b := make([]byte, n)
	if err := windows.GetTokenInformation(t, windows.TokenOwner, &b[0], n, &n); err != nil {
		return nil
	}
	return (*tokenOwnerInfo)(unsafe.Pointer(&b[0])).Owner
}

func ownedByUs(owner *windows.SID) (bool, error) {
	owners, err := trustedOwners()
	if err != nil {
		return false, err
	}
	for _, o := range owners {
		if owner != nil && o.Equals(owner) {
			return true, nil
		}
	}
	return false, nil
}

// ownerOnlyDescriptor builds a protected security descriptor that grants full
// control to the current user and nobody else.
func ownerOnlyDescriptor() (*windows.SECURITY_DESCRIPTOR, error) {
	user, err := currentUser()
	if err != nil {
		return nil, err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.String() + ")")
	if err != nil {
		return nil, fmt.Errorf("build the owner-only security descriptor: %w", err)
	}
	return sd, nil
}

// openNoFollow opens path with FILE_FLAG_OPEN_REPARSE_POINT, so a symlink or
// junction is opened itself instead of being followed, then re-checks the
// attributes of the open handle and refuses a reparse point. New files get an
// owner-only protected DACL.
func openNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	var access uint32
	switch flag & (os.O_RDONLY | os.O_WRONLY | os.O_RDWR) {
	case os.O_RDONLY:
		access = windows.GENERIC_READ
	case os.O_WRONLY:
		access = windows.GENERIC_WRITE
	case os.O_RDWR:
		access = windows.GENERIC_READ | windows.GENERIC_WRITE
	}
	var disp uint32
	switch {
	case flag&os.O_CREATE != 0 && flag&os.O_EXCL != 0:
		disp = windows.CREATE_NEW
	case flag&os.O_CREATE != 0 && flag&os.O_TRUNC != 0:
		disp = windows.CREATE_ALWAYS
	case flag&os.O_CREATE != 0:
		disp = windows.OPEN_ALWAYS
	case flag&os.O_TRUNC != 0:
		disp = windows.TRUNCATE_EXISTING
	default:
		disp = windows.OPEN_EXISTING
	}
	var sa *windows.SecurityAttributes
	if flag&os.O_CREATE != 0 {
		sd, err := ownerOnlyDescriptor()
		if err != nil {
			return nil, &os.PathError{Op: "open", Path: path, Err: err}
		}
		sa = &windows.SecurityAttributes{SecurityDescriptor: sd}
		sa.Length = uint32(unsafe.Sizeof(*sa))
	}
	share := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE)
	h, err := windows.CreateFile(p, access, share, sa, disp, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_EXISTS) || errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			err = os.ErrExist
		}
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		_ = windows.CloseHandle(h)
		return nil, &os.PathError{Op: "stat", Path: path, Err: err}
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = windows.CloseHandle(h)
		return nil, &os.PathError{Op: "open", Path: path, Err: errors.New("is a symlink or reparse point")}
	}
	return os.NewFile(uintptr(h), path), nil
}

// checkOwner is a no-op for open files on Windows: the owner-only DACL set at
// creation and the directory owner check in secureDir cover them.
func checkOwner(os.FileInfo) error { return nil }

// secureDir verifies that dir is not a reparse point and is owned by the
// current user, then replaces its DACL with a protected, owner-only one
// (inherited by new files and subdirectories).
func secureDir(dir string, _ os.FileInfo) error {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	attrs, err := windows.GetFileAttributes(p)
	if err != nil {
		return fmt.Errorf("read attributes: %w", err)
	}
	if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("is a symlink or reparse point, so ccshelf refuses to use it")
	}
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read the owner: %w", err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return fmt.Errorf("read the owner: %w", err)
	}
	ok, err := ownedByUs(owner)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("owned by %s, not the current user", owner)
	}
	user, err := currentUser()
	if err != nil {
		return err
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(user),
		},
	}}, nil)
	if err != nil {
		return fmt.Errorf("build the owner-only ACL: %w", err)
	}
	err = windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
	if err != nil {
		return fmt.Errorf("restrict the directory to its owner: %w", err)
	}
	return nil
}

// chmodNoFollow clears or sets the read-only attribute with os.Chmod. Windows
// has no mode bits. Callers inspect the path with Lstat and skip links before
// they call it. What os.Chmod does with a link on Windows is {U}, so this
// function does not claim to refuse links.
func chmodNoFollow(path string, dir bool, mode os.FileMode) error {
	return os.Chmod(path, mode)
}
