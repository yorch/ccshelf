package update

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// BackupSuffix is appended to the executable's name for the previous version.
const BackupSuffix = ".old"

// tempPrefix starts the name of every file this package creates beside the
// executable, so that stale ones can be recognized and removed.
const tempPrefix = ".ccshelf-update-"

// NotWritableError means the directory of the executable cannot be written by
// the current user.
type NotWritableError struct {
	Dir string
	Err error
}

// Error implements error.
func (e *NotWritableError) Error() string {
	return fmt.Sprintf("cannot write to %s: %v", e.Dir, e.Err)
}

// Unwrap returns the underlying error.
func (e *NotWritableError) Unwrap() error { return e.Err }

// CheckWritable fails with a *NotWritableError unless a file can be created in
// dir.
func CheckWritable(dir string) error {
	f, err := os.CreateTemp(dir, tempPrefix+"probe-*")
	if err != nil {
		if errors.Is(err, fs.ErrPermission) || errors.Is(err, fs.ErrNotExist) || isReadOnly(err) {
			return &NotWritableError{Dir: dir, Err: err}
		}
		return fmt.Errorf("checking that %s is writable: %w", dir, err)
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}

// ResolveExecutable returns the path the update will replace. A symlinked
// executable is followed to its target, but only when the target's directory is
// one the current user owns (so a link in a world-readable location can never
// be used to overwrite something that is not ours); that the directory can be
// written is checked later by [CheckWritable], right before a replacement,
// because this function must not write anything (it also serves --check and
// --dry-run). wasLink reports whether a link was followed. On Linux
// os.Executable already returns the resolved path, so a link is seen here
// mostly where the OS reports the path as invoked (macOS).
func ResolveExecutable(exe string) (resolved string, wasLink bool, err error) {
	if !filepath.IsAbs(exe) {
		return "", false, fmt.Errorf("the executable path %q is not absolute", exe)
	}
	fi, err := os.Lstat(exe)
	if err != nil {
		return "", false, fmt.Errorf("inspecting %s: %w", exe, err)
	}
	if fi.Mode()&fs.ModeSymlink == 0 {
		return exe, false, nil
	}
	target, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", true, fmt.Errorf("following the link %s: %w", exe, err)
	}
	dir := filepath.Dir(target)
	if err := dirOwnedByUser(dir); err != nil {
		return "", true, fmt.Errorf("%s is a link to %s, whose directory %s is not one you own: %w", exe, target, dir, err)
	}
	// Whether the directory can be written is not probed here: this runs for
	// --check, --dry-run and the cached notice, which must change nothing on
	// disk. Apply and Rollback do the probe just before they replace.
	return target, true, nil
}

// fileMode returns the mode the new binary gets: that of the file it replaces
// (permission bits only), or 0755 when that is not executable.
func fileMode(exe string) os.FileMode {
	fi, err := os.Stat(exe)
	if err != nil || fi.Mode().Perm()&0o100 == 0 {
		return 0o755
	}
	return fi.Mode().Perm()
}

// CreateTemp creates a private temporary file for the new binary in dir (the
// directory of the executable, so that the final rename stays on one file
// system). On Windows the name ends in ".exe" so that it can be run for the
// version check.
func CreateTemp(dir, goos string) (*os.File, error) {
	suffix := ".tmp"
	if goos == "windows" {
		suffix = ".exe"
	}
	f, err := os.CreateTemp(dir, tempPrefix+"*"+suffix)
	if err != nil {
		return nil, fmt.Errorf("creating a temporary file in %s: %w", dir, err)
	}
	return f, nil
}

// Finish makes the temporary binary ready to be moved into place: flushed to
// disk and given the executable mode of the file it replaces.
func Finish(f *os.File, exe string) error {
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("syncing %s: %w", f.Name(), err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", f.Name(), err)
	}
	if err := os.Chmod(f.Name(), fileMode(exe)); err != nil { //nolint:gosec // an executable must be executable; the mode is that of the file it replaces
		return fmt.Errorf("setting the mode of %s: %w", f.Name(), err)
	}
	return nil
}

// RemoveStale deletes leftovers of an interrupted update (temporary files not
// modified since cutoff) from dir. It never touches the .old backup.
func RemoveStale(dir string, cutoff time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), tempPrefix) {
			continue
		}
		fi, err := e.Info()
		if err != nil || !fi.Mode().IsRegular() || !fi.ModTime().Before(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}

// keepBackup makes old a second name for the current executable (a hard link,
// or a copy when links are not possible), replacing any previous backup.
func keepBackup(exe, old string) error {
	if err := os.Remove(old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing the previous backup %s: %w", old, err)
	}
	if err := os.Link(exe, old); err == nil {
		return nil
	}
	return copyFile(exe, old)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // the running executable, resolved by ResolveExecutable
	if err != nil {
		return fmt.Errorf("reading %s: %w", src, err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700) //nolint:gosec // an executable backup
	if err != nil {
		return fmt.Errorf("creating %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return fmt.Errorf("copying %s: %w", src, err)
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return fmt.Errorf("syncing %s: %w", dst, err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return fmt.Errorf("closing %s: %w", dst, err)
	}
	return os.Chmod(dst, fileMode(src)) //nolint:gosec // an executable backup keeps its mode
}

// installAtomic puts newPath in place of exe with one atomic rename, after
// keeping the previous binary as old. At every instant exe is a complete
// binary (the old one or the new one). Used where a running executable can be
// overwritten by rename (Unix).
func installAtomic(newPath, exe, old string) error {
	if err := keepBackup(exe, old); err != nil {
		return err
	}
	if err := os.Rename(newPath, exe); err != nil {
		return fmt.Errorf("replacing %s: %w", exe, err)
	}
	return nil
}

// installAside is installAtomic for systems where a running executable cannot
// be replaced or deleted but can be renamed (Windows): the running file is
// renamed to old, then the new one takes its name; if that fails the old one
// is renamed back.
func installAside(newPath, exe, old string) error {
	if err := os.Remove(old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing the previous backup %s (is the old version still running?): %w", old, err)
	}
	if err := os.Rename(exe, old); err != nil {
		return fmt.Errorf("moving %s aside: %w", exe, err)
	}
	if err := os.Rename(newPath, exe); err != nil {
		if rerr := os.Rename(old, exe); rerr != nil {
			return fmt.Errorf("replacing %s: %w (and putting the old binary back failed: %w; it is at %s)", exe, err, rerr, old)
		}
		return fmt.Errorf("replacing %s: %w", exe, err)
	}
	return nil
}

// swapAtomic exchanges exe and old (rollback): exe becomes the backup's
// content and the current content becomes the new backup, so a rollback can
// itself be rolled back.
func swapAtomic(exe, old string) error {
	tmp := exe + ".swap"
	if err := keepBackup(exe, tmp); err != nil {
		return err
	}
	if err := os.Rename(old, exe); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("restoring %s: %w", old, err)
	}
	if err := os.Rename(tmp, old); err != nil {
		return fmt.Errorf("keeping the replaced binary as %s: %w", old, err)
	}
	return nil
}

// swapAside is swapAtomic for Windows semantics.
func swapAside(exe, old string) error {
	tmp := exe + ".swap"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing %s: %w", tmp, err)
	}
	if err := os.Rename(exe, tmp); err != nil {
		return fmt.Errorf("moving %s aside: %w", exe, err)
	}
	if err := os.Rename(old, exe); err != nil {
		if rerr := os.Rename(tmp, exe); rerr != nil {
			return fmt.Errorf("restoring %s: %w (and putting the current binary back failed: %w; it is at %s)", old, err, rerr, tmp)
		}
		return fmt.Errorf("restoring %s: %w", old, err)
	}
	if err := os.Rename(tmp, old); err != nil {
		return fmt.Errorf("keeping the replaced binary as %s: %w", old, err)
	}
	return nil
}

// Install replaces exe with the binary at newPath using the strategy of the
// running OS, keeping the previous binary as exe+".old".
func Install(newPath, exe string) error { return platformInstall(newPath, exe, exe+BackupSuffix) }

// Swap exchanges exe with its ".old" backup (rollback).
func Swap(exe string) error { return platformSwap(exe, exe+BackupSuffix) }
