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
	name, err := DirName(prefix, content)
	if err != nil {
		return "", err
	}
	if !dirFilePattern.MatchString(file) {
		return "", fmt.Errorf("invalid cache file name %q", file)
	}
	if len(content) > MaxFileSize {
		return "", fmt.Errorf("cache directory %q: content is too large", name)
	}
	final := filepath.Join(dir, name)
	// Other launches may build or repair the same directory at the same
	// time, so a step can fail because another process just changed the
	// target. Each round checks the target first and builds it only if the
	// check fails. A few rounds are enough for the processes to agree.
	var last error
	for round := 0; round < 8; round++ {
		if round > 0 {
			time.Sleep(time.Duration(round) * 5 * time.Millisecond)
		}
		if last = checkDir(final, file, content); last == nil {
			touch(final)
			return final, nil
		}
		if err := replaceDir(dir, final, file, content); err != nil {
			last = err
		}
	}
	return "", fmt.Errorf("cache directory %s: %w%s", name, last, deleteHint(final))
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
	// Make the tree read-only: file 0400, directory 0500. This stops an
	// accidental write by Claude Code, not a deliberate chmod by the same
	// user. checkDir still verifies the content at every launch. On Windows
	// the file gets the read-only attribute, and the directory stays
	// writable.
	if err := os.Chmod(filepath.Join(tmp, file), 0o400); err != nil {
		return fmt.Errorf("make the cache file read-only: %w", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmp, 0o500); err != nil {
			return fmt.Errorf("make the cache directory read-only: %w", err)
		}
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
		if err := os.Rename(final, aside); err == nil {
			defer removeTree(aside)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("move the old cache directory away: %w", err)
		}
	}
	if err := os.Rename(tmp, final); err != nil {
		return fmt.Errorf("publish cache directory: %w", err)
	}
	published = true
	return nil
}

// checkDir verifies the directory path as [WriteDir] describes. It returns an
// error wrapping [os.ErrNotExist] when the directory is absent and
// [ErrTampered] for any other difference.
func checkDir(path, file string, content []byte) error {
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
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o500 {
		return bad("has mode %v, and it must be 0500", fi.Mode().Perm())
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
	if runtime.GOOS != "windows" && ffi.Mode().Perm() != 0o400 {
		return bad("holds %s with mode %v, and it must be 0400", file, ffi.Mode().Perm())
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
			_ = os.Chmod(path, 0o600)
		}
		return
	}
	_ = os.Chmod(path, 0o700)
	entries, err := os.ReadDir(path)
	if err != nil {
		return
	}
	for _, e := range entries {
		makeWritable(filepath.Join(path, e.Name()))
	}
}
