package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"time"
)

// chmodFn changes a mode in replaceDir. A test replaces it to imitate a file
// system that ignores modes.
var chmodFn = os.Chmod

// renameFn renames a path in replaceDir. A test replaces it to imitate a
// platform (older macOS) where renaming a directory needs write permission on
// the directory itself, because the rename updates its ".." entry.
var renameFn = os.Rename

var (
	dirFilePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	// dirNamePattern matches the directory names that [WriteDir] creates.
	dirNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]+(-[A-Za-z0-9_]+)*-[0-9a-f]{64}$`)
)

// DirName returns the content-addressed directory name for content:
// <prefix>-<SHA-256 of content as 64 hex digits>.
func DirName(prefix string, content []byte) (string, error) {
	if !partPattern.MatchString(prefix) {
		return "", fmt.Errorf("invalid cache prefix %q", prefix)
	}
	sum := sha256.Sum256(content)
	return prefix + "-" + hex.EncodeToString(sum[:]), nil
}

// WriteDir makes sure that the directory <dir>/<DirName(prefix, content)>
// exists and holds exactly one entry: the regular file called file with the
// bytes of content. It returns the path of the directory. The directory has
// mode 0500 and the file 0400 (on Windows, the file gets the read-only
// attribute and the directory stays writable). This stops accidental writes.
// It does not stop a process of the same user that changes the modes, so the
// check below still runs at every launch.
//
// An existing directory is reused only after a check: it is a real directory
// (not a link) owned by the current user, its only entry is file, and file is
// a regular file (not a link) whose bytes equal content. Anything else is
// replaced. The replacement is built in a new temporary directory next to the
// target, so that the target never appears half written. A directory that
// fails the check is first renamed to a unique temporary name, because
// Windows and Unix do not agree on renaming over a directory that is not
// empty. Processes that race to build the same directory all succeed: each
// one checks the target again after a failed step and retries a few times.
func WriteDir(dir, prefix, file string, content []byte) (string, error) {
	p, _, err := WriteDirChecked(dir, prefix, file, content)
	return p, err
}

// WriteDirChecked is [WriteDir]. It also reports modesIgnored: true when the
// cache file system does not keep the read-only modes (some network and FAT
// file systems ignore them). The content checks still passed in that case, so
// the directory is usable but not read-only. The function probes the file
// system with a temporary file. If the probe shows that modes are ignored, it
// accepts a directory with the right content and does not rebuild it.
func WriteDirChecked(dir, prefix, file string, content []byte) (path string, modesIgnored bool, err error) {
	name, err := DirName(prefix, content)
	if err != nil {
		return "", false, err
	}
	if !dirFilePattern.MatchString(file) {
		return "", false, fmt.Errorf("invalid cache file name %q", file)
	}
	if len(content) > MaxFileSize {
		return "", false, fmt.Errorf("cache directory %q: content is too large", name)
	}
	final := filepath.Join(dir, name)
	// Other launches may build or repair the same directory at the same
	// time, so a step can fail because another process just changed the
	// target. Each round checks the target first and builds it only if the
	// check fails. A few rounds are enough for the processes to agree.
	var last error
	windowRetries := 0
	for round := 0; round < 8; round++ {
		if round > 0 {
			time.Sleep(time.Duration(round) * 5 * time.Millisecond)
		}
		ok, ignored, why := settleDir(dir, final, file, content)
		if ok {
			touch(final)
			return final, ignored, nil
		}
		last = why
		// Another process may have just published the directory and not
		// yet made it read-only (mode 0700, file 0400). Wait a few rounds
		// for it instead of replacing a good directory.
		if errors.Is(why, errModes) && windowRetries < 4 && inPublishWindow(final, file) {
			windowRetries++
			continue
		}
		if err := replaceDir(dir, final, file, content); err != nil {
			last = err
		}
	}
	// The last replace has not been checked yet.
	if ok, ignored, why := settleDir(dir, final, file, content); ok {
		touch(final)
		return final, ignored, nil
	} else if last == nil {
		last = why
	}
	return "", false, fmt.Errorf("cache directory %s: %w%s", name, last, deleteHint(final))
}

// errModes is wrapped by the reason that settleDir gives when the content is
// right and the modes are not.
var errModes = fmt.Errorf("%w: the file modes of the directory are not 0500 and 0400", ErrTampered)

// settleDir checks the directory. ok is true when it can be used. ignored is
// true when the content is right and the cache file system ignores modes, so
// that a rebuild cannot help and would replace the directory under running
// sessions. When ok is false, why says what is wrong and is never nil.
func settleDir(dir, final, file string, content []byte) (ok, ignored bool, why error) {
	if err := checkContent(final, file, content); err != nil {
		return false, false, err
	}
	if checkModes(final, file) == nil {
		return true, false, nil
	}
	if !fsHonorsModes(dir) {
		return true, true, nil
	}
	return false, false, errModes
}

// replaceDir builds the directory in a temporary directory next to final and
// renames it to final. A final that exists is renamed to a unique temporary
// name first and removed after the new directory is in place.
func replaceDir(dir, final, file string, content []byte) error {
	tmp, err := newTempDir(dir)
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			removeTree(tmp)
		}
	}()
	f, err := openNoFollow(filepath.Join(tmp, file), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create file in temporary cache directory: %w", err)
	}
	_, werr := f.Write(content)
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return fmt.Errorf("write cache directory: %w", werr)
	}
	// The file becomes read-only (0400) now. The directory stays writable
	// (0700) until the rename: some systems need write permission on a
	// directory to move it. Then the published directory becomes 0500. This
	// stops an accidental write by Claude Code, not a deliberate chmod by the
	// same user. checkDir still verifies the content at every launch. On
	// Windows the directory stays writable.
	if err := chmodFn(filepath.Join(tmp, file), 0o400); err != nil {
		return fmt.Errorf("make the cache file read-only: %w", err)
	}
	// Check again just before the move, so that a directory that another
	// process published meanwhile is not moved away.
	if checkDir(final, file, content) == nil {
		return nil
	}
	if _, err := os.Lstat(final); err == nil {
		aside, err := tempName(dir)
		if err != nil {
			return err
		}
		// A directory of mode 0500 may need write permission to be moved.
		_ = chmodNoFollow(final, true, 0o700)
		if err := renameFn(final, aside); err == nil {
			defer removeTree(aside)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("move the old cache directory away: %w", err)
		}
	}
	if err := renameFn(tmp, final); err != nil {
		return fmt.Errorf("publish cache directory: %w", err)
	}
	published = true
	if runtime.GOOS != "windows" {
		if err := chmodFn(final, 0o500); err != nil {
			return fmt.Errorf("make the cache directory read-only: %w", err)
		}
	}
	return nil
}

// checkDir verifies the directory path as [WriteDir] describes, modes
// included. It returns an error wrapping [os.ErrNotExist] when the directory is
// absent and [ErrTampered] for any other difference.
func checkDir(path, file string, content []byte) error {
	if err := checkContent(path, file, content); err != nil {
		return err
	}
	if err := checkModes(path, file); err != nil {
		return fmt.Errorf("%w: %s %w%s", ErrTampered, filepath.Base(path), err, deleteHint(path))
	}
	return nil
}

// checkContent verifies everything but the modes: a real directory (no link,
// no junction) owned by the user, with exactly one regular file with the
// expected bytes.
func checkContent(path, file string, content []byte) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s %s%s", ErrTampered, filepath.Base(path), fmt.Sprintf(format, a...), deleteHint(path))
	}
	// ModeIrregular covers a Windows junction or another reparse point.
	if fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 || !fi.IsDir() {
		return bad("is not a directory")
	}
	if err := checkOwner(fi); err != nil {
		return bad("%v", err)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("read cache directory: %w", err)
	}
	if len(entries) != 1 || entries[0].Name() != file {
		return bad("does not hold exactly the file %s", file)
	}
	ffi, err := os.Lstat(filepath.Join(path, file))
	if err != nil {
		return fmt.Errorf("inspect cache file: %w", err)
	}
	if !ffi.Mode().IsRegular() || ffi.Mode()&os.ModeIrregular != 0 {
		return bad("holds %s, which is not a regular file", file)
	}
	got, err := readRegular(filepath.Join(path, file), int64(len(content))+1)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, content) {
		return bad("does not hold what its name says")
	}
	return nil
}

// checkModes verifies directory mode 0500 and file mode 0400. It checks
// nothing on Windows, which has no mode bits.
func checkModes(path, file string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode().Perm() != 0o500 {
		return fmt.Errorf("has mode %v, and it must be 0500", fi.Mode().Perm())
	}
	ffi, err := os.Lstat(filepath.Join(path, file))
	if err != nil {
		return err
	}
	if ffi.Mode().Perm() != 0o400 {
		return fmt.Errorf("holds %s with mode %v, and it must be 0400", file, ffi.Mode().Perm())
	}
	return nil
}

func tempName(dir string) (string, error) {
	r, err := randomHex()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ".ccshelf-tmp-"+r), nil
}

// newTempDir creates a new private directory with a name that [PruneDir]
// recognizes as left over.
func newTempDir(dir string) (string, error) {
	for i := 0; ; i++ {
		p, err := tempName(dir)
		if err != nil {
			return "", err
		}
		if err := os.Mkdir(p, 0o700); err != nil {
			if errors.Is(err, os.ErrExist) && i < 5 {
				continue
			}
			return "", fmt.Errorf("create temporary cache directory: %w", err)
		}
		return p, nil
	}
}

// removeTree removes path and what is below it. It first restores the owner
// write permission on directories and files, because a directory of mode 0500
// cannot be emptied, and Windows does not remove a read-only file. It does not
// follow links. Errors are ignored: the callers treat the removal as best
// effort and a left-over tree is pruned later.
func removeTree(path string) {
	makeWritable(path)
	_ = os.RemoveAll(path)
}

func makeWritable(path string) {
	fi, err := os.Lstat(path)
	if err != nil || fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return
	}
	if !fi.IsDir() {
		if fi.Mode().IsRegular() {
			_ = chmodNoFollow(path, false, 0o600)
		}
		return
	}
	_ = chmodNoFollow(path, true, 0o700)
	entries, err := os.ReadDir(path)
	if err != nil {
		return
	}
	for _, e := range entries {
		makeWritable(filepath.Join(path, e.Name()))
	}
}

// fsHonorsModes reports whether a mode set with chmod in dir stays. It
// creates a temporary file, sets mode 0400 and reads the mode back. When the
// probe fails for another reason it returns true, so that the caller keeps
// its strict behavior.
func fsHonorsModes(dir string) bool {
	if runtime.GOOS == "windows" {
		return true
	}
	tmp, err := tempName(dir)
	if err != nil {
		return true
	}
	f, err := openNoFollow(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return true
	}
	f.Close()
	defer os.Remove(tmp)
	if err := chmodFn(tmp, 0o400); err != nil {
		return true
	}
	fi, err := os.Lstat(tmp)
	if err != nil {
		return true
	}
	return fi.Mode().Perm() == 0o400
}

// inPublishWindow reports whether the directory has mode 0700 and the file
// 0400: the state between the rename of a new directory and its chmod to 0500.
func inPublishWindow(path, file string) bool {
	if runtime.GOOS == "windows" {
		return false
	}
	fi, err := os.Lstat(path)
	if err != nil || fi.Mode().Perm() != 0o700 {
		return false
	}
	ffi, err := os.Lstat(filepath.Join(path, file))
	return err == nil && ffi.Mode().Perm() == 0o400
}
