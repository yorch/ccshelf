package cache

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"time"
)

// DefaultMaxAge is the age after which [GC] callers normally remove files.
const DefaultMaxAge = 30 * 24 * time.Hour

// MaxFileSize bounds what [ReadFile] and the re-hash in [Write] will read.
const MaxFileSize = 16 << 20

// ErrTampered is returned when a file that should hold known content does
// not (or is not a regular file). Callers should refuse to use it.
var ErrTampered = errors.New("cache file does not match its content address")

var (
	partPattern = regexp.MustCompile(`^[A-Za-z0-9_]+(-[A-Za-z0-9_]+)*$`)
	extPattern  = regexp.MustCompile(`^[A-Za-z0-9]{1,16}$`)
	// namePattern matches names created by this package.
	namePattern = regexp.MustCompile(`^[A-Za-z0-9_]+(-[A-Za-z0-9_]+)*-[0-9a-f]{32}\.[A-Za-z0-9]{1,16}$`)
	tmpPattern  = regexp.MustCompile(`^\.ccshelf-tmp-[0-9a-f]{16}$`)
)

// Path returns the cache directory path without creating or checking it.
// It is ~/.cache/ccshelf on macOS and Linux (or $XDG_CACHE_HOME/ccshelf when
// that is an absolute path), and %LOCALAPPDATA%\ccshelf on Windows.
func Path() (string, error) {
	return pathFor(runtime.GOOS, os.Getenv, os.UserHomeDir, os.UserCacheDir)
}

func pathFor(goos string, getenv func(string) string, home, userCache func() (string, error)) (string, error) {
	if goos == "windows" {
		if d := getenv("LOCALAPPDATA"); d != "" && filepath.IsAbs(d) {
			return filepath.Join(d, "ccshelf"), nil
		}
		d, err := userCache()
		if err != nil {
			return "", fmt.Errorf("locate cache directory: %w", err)
		}
		return filepath.Join(d, "ccshelf"), nil
	}
	if d := getenv("XDG_CACHE_HOME"); d != "" && filepath.IsAbs(d) {
		return filepath.Join(d, "ccshelf"), nil
	}
	h, err := home()
	if err != nil {
		return "", fmt.Errorf("locate cache directory: %w", err)
	}
	return filepath.Join(h, ".cache", "ccshelf"), nil
}

// Dir returns the cache directory from [Path], creating it (mode 0700) and
// verifying it with [Ensure].
func Dir() (string, error) {
	p, err := Path()
	if err != nil {
		return "", err
	}
	if err := Ensure(p); err != nil {
		return "", err
	}
	return p, nil
}

// Ensure creates dir (and missing parents) with mode 0700 and verifies that
// the final component is a real directory owned by the current user. A
// directory the user owns whose mode is wider than 0700 is repaired with
// chmod 0700 (Unix) or given an owner-only protected DACL (Windows) and
// accepted. A directory owned by someone else, or a symlink, is refused.
// Parent components may be symlinks (for example /tmp on macOS). Only the
// cache directory itself may not be.
func Ensure(dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return fmt.Errorf("create cache parent: %w", err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create cache directory %s: %w", dir, err)
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect cache directory: %w", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("cache directory %s is a symlink, so ccshelf refuses to use it", dir)
	}
	if !fi.IsDir() {
		return fmt.Errorf("cache path %s is not a directory", dir)
	}
	if err := secureDir(dir, fi); err != nil {
		return fmt.Errorf("cache directory %s: %w", dir, err)
	}
	return nil
}

// Name returns the content-addressed file name for content.
func Name(prefix, ext string, content []byte) (string, error) {
	if !partPattern.MatchString(prefix) {
		return "", fmt.Errorf("invalid cache prefix %q", prefix)
	}
	if !extPattern.MatchString(ext) {
		return "", fmt.Errorf("invalid cache extension %q", ext)
	}
	sum := sha256.Sum256(content)
	return prefix + "-" + hex.EncodeToString(sum[:16]) + "." + ext, nil
}

// Write stores content in dir under its content-addressed name and returns
// the full path. If the file already exists its bytes are re-read and must be
// identical, otherwise [ErrTampered] is returned. Reuse refreshes the file's
// modification time so that [GC] keeps files that are still being used.
func Write(dir, prefix, ext string, content []byte) (string, error) {
	name, err := Name(prefix, ext, content)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	if err := verify(path, content); err == nil {
		touch(path)
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := publish(dir, path, content, false); err != nil {
		return "", err
	}
	return path, nil
}

// WriteReplace atomically writes content to the file called name in dir,
// replacing any previous regular file of that name. name must match the
// pattern <prefix>-<32 hex>.<ext> so that [GC] can age it out. Use it for
// small mutable records. Use [Write] for immutable content-addressed files.
func WriteReplace(dir, name string, content []byte) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("invalid cache file name %q", name)
	}
	if len(content) > MaxFileSize {
		return fmt.Errorf("cache file %q too large", name)
	}
	return publish(dir, filepath.Join(dir, name), content, true)
}

// ReadFile reads the file called name from dir without following symlinks.
// It returns an error wrapping [os.ErrNotExist] when the file is absent and
// [ErrTampered] when it is not a regular file owned by the current user.
func ReadFile(dir, name string) ([]byte, error) {
	if !namePattern.MatchString(name) {
		return nil, fmt.Errorf("invalid cache file name %q", name)
	}
	return readRegular(filepath.Join(dir, name), MaxFileSize)
}

// statePattern matches the fixed names of small mutable records (for example
// update-state.json) that are not content addressed and never aged out by GC.
var statePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,40}\.json$`)

// WriteState atomically writes content to the fixed-name record called name
// (lower-case letters, digits and "-", ending in .json) in dir with mode 0600,
// replacing any previous regular file of that name. Unlike [WriteReplace] the
// name carries no content address, so [GC] and [Prune] never remove it.
func WriteState(dir, name string, content []byte) error {
	if !statePattern.MatchString(name) {
		return fmt.Errorf("invalid cache state file name %q", name)
	}
	if len(content) > MaxFileSize {
		return fmt.Errorf("cache file %q too large", name)
	}
	return publish(dir, filepath.Join(dir, name), content, true)
}

// ReadState reads the record written by [WriteState] without following
// symlinks. It returns an error wrapping [os.ErrNotExist] when the file is
// absent and [ErrTampered] when it is not a regular file owned by the current
// user or is larger than limit bytes (0 means [MaxFileSize]).
func ReadState(dir, name string, limit int64) ([]byte, error) {
	if !statePattern.MatchString(name) {
		return nil, fmt.Errorf("invalid cache state file name %q", name)
	}
	if limit <= 0 || limit > MaxFileSize {
		limit = MaxFileSize
	}
	return readRegular(filepath.Join(dir, name), limit)
}

// deleteHint is appended to errors about a cache file that cannot be trusted:
// every cache file can be rebuilt, so removing it is always safe.
func deleteHint(path string) string {
	return ". Delete " + path + " and run again to rebuild it"
}

func readRegular(path string, limit int64) ([]byte, error) {
	f, err := openNoFollow(path, os.O_RDONLY, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: open %s: %w%s", ErrTampered, filepath.Base(path), err, deleteHint(path))
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat cache file: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s is not a regular file%s", ErrTampered, filepath.Base(path), deleteHint(path))
	}
	if err := checkOwner(fi); err != nil {
		return nil, fmt.Errorf("%w: %s: %w%s", ErrTampered, filepath.Base(path), err, deleteHint(path))
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read cache file: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: %s is larger than %d bytes%s", ErrTampered, filepath.Base(path), limit, deleteHint(path))
	}
	return data, nil
}

// verify re-hashes the file at path and compares it with content.
func verify(path string, content []byte) error {
	got, err := readRegular(path, int64(len(content))+1)
	if err != nil {
		return err
	}
	want := sha256.Sum256(content)
	have := sha256.Sum256(got)
	if want != have || !bytes.Equal(got, content) {
		return fmt.Errorf("%w: %s does not hold what its name says%s", ErrTampered, filepath.Base(path), deleteHint(path))
	}
	return nil
}

func randomHex() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("random: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// publish writes content to a temporary file and renames it to path. With
// replace false it never overwrites an existing target and verifies it
// instead.
func publish(dir, path string, content []byte, replace bool) error {
	var tmp string
	for i := 0; ; i++ {
		r, err := randomHex()
		if err != nil {
			return err
		}
		tmp = filepath.Join(dir, ".ccshelf-tmp-"+r)
		f, err := openNoFollow(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			if errors.Is(err, os.ErrExist) && i < 5 {
				continue
			}
			return fmt.Errorf("create temporary cache file: %w", err)
		}
		_, werr := f.Write(content)
		if werr == nil {
			werr = f.Sync()
		}
		cerr := f.Close()
		if werr != nil || cerr != nil {
			_ = os.Remove(tmp)
			if werr == nil {
				werr = cerr
			}
			return fmt.Errorf("write cache file: %w", werr)
		}
		break
	}
	if !replace {
		if _, err := os.Lstat(path); err == nil {
			_ = os.Remove(tmp)
			return verify(path, content)
		}
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		if !replace {
			// Windows cannot rename over a file another process holds
			// open; if the target is now correct, that is fine.
			if verr := verify(path, content); verr == nil {
				return nil
			}
		}
		return fmt.Errorf("publish cache file: %w", err)
	}
	if testAfterRename != nil {
		testAfterRename(path)
	}
	if !replace {
		return verify(path, content)
	}
	return nil
}

// testAfterRename is a test hook called after publish renamed the temporary
// file into place and before the final verification. Production code never
// sets it.
var testAfterRename func(path string)

func touch(path string) {
	now := time.Now()
	_ = os.Chtimes(path, now, now)
}

// GC removes files in dir that this package created (content-addressed names
// and leftover temporary files), whose modification time is older than
// maxAge and for which inUse (if non-nil) returns false. Anything else in dir
// is left alone, symlinks are never followed or removed, and a maxAge of zero
// or less means [DefaultMaxAge]. It returns the removed paths in sorted order.
func GC(dir string, maxAge time.Duration, inUse func(path string) bool) ([]string, error) {
	if maxAge <= 0 {
		maxAge = DefaultMaxAge
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read cache directory: %w", err)
	}
	cutoff := time.Now().Add(-maxAge)
	var removed []string
	var errs []error
	for _, e := range entries {
		name := e.Name()
		if !namePattern.MatchString(name) && !tmpPattern.MatchString(name) {
			continue
		}
		path := filepath.Join(dir, name)
		fi, err := os.Lstat(path)
		if err != nil || !fi.Mode().IsRegular() || !fi.ModTime().Before(cutoff) {
			continue
		}
		if inUse != nil && inUse(path) {
			continue
		}
		if err := os.Remove(path); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, fmt.Errorf("remove %s: %w", name, err))
			}
			continue
		}
		removed = append(removed, path)
	}
	sort.Strings(removed)
	return removed, errors.Join(errs...)
}

var (
	urlKeyPattern   = regexp.MustCompile(`^[0-9a-f]{16}$`)
	commitPattern   = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
	tmpCheckoutName = regexp.MustCompile(`^\.tmp-[0-9]+$`)
)

// gitFolder is the folder of the cache directory that holds git checkouts, as
// <url key>/<commit>.
const gitFolder = "git"

// Prune removes what has not been used for maxAge from the cache directory
// (see [PruneDir]). A maxAge of zero or less means [DefaultMaxAge]. keep, if
// non-nil, is asked about every candidate and protects the ones it reports.
func Prune(maxAge time.Duration, keep func(path string) bool) ([]string, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	return PruneDir(dir, maxAge, keep)
}

// PruneDir is Prune for the cache directory dir. It removes
//
//   - the files this package created in dir (see [GC]),
//   - the content-addressed directories of [WriteDir] and left-over temporary
//     directories,
//   - the git checkouts dir/git/<url key>/<commit>, and left-over temporary
//     checkouts, whose modification time (refreshed by every use) is older
//     than maxAge.
//
// Nothing newer than maxAge is removed, nothing keep reports is removed, and
// anything that does not look like what this tool creates (other names,
// symbolic links) is left alone. Removal is best effort: the removed paths are
// returned in sorted order together with the joined errors of the failures.
func PruneDir(dir string, maxAge time.Duration, keep func(path string) bool) ([]string, error) {
	if maxAge <= 0 {
		maxAge = DefaultMaxAge
	}
	removed, err := GC(dir, maxAge, keep)
	errs := []error{err}
	cutoff := time.Now().Add(-maxAge)
	gitDir := filepath.Join(dir, gitFolder)
	urlDirs, rerr := os.ReadDir(gitDir)
	if rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
		errs = append(errs, fmt.Errorf("read git cache: %w", rerr))
	}
	for _, ue := range urlDirs {
		if !urlKeyPattern.MatchString(ue.Name()) || !ue.IsDir() {
			continue
		}
		urlDir := filepath.Join(gitDir, ue.Name())
		entries, rerr := os.ReadDir(urlDir)
		if rerr != nil {
			errs = append(errs, fmt.Errorf("read %s: %w", urlDir, rerr))
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !commitPattern.MatchString(name) && !tmpCheckoutName.MatchString(name) {
				continue
			}
			p := filepath.Join(urlDir, name)
			fi, lerr := os.Lstat(p)
			if lerr != nil || !fi.IsDir() || !fi.ModTime().Before(cutoff) {
				continue
			}
			if keep != nil && keep(p) {
				continue
			}
			if err := os.RemoveAll(p); err != nil {
				errs = append(errs, fmt.Errorf("remove %s: %w", name, err))
				continue
			}
			removed = append(removed, p)
		}
		_ = os.Remove(urlDir) // only succeeds when it is empty
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		name := e.Name()
		if !dirNamePattern.MatchString(name) && !tmpPattern.MatchString(name) {
			continue
		}
		p := filepath.Join(dir, name)
		fi, lerr := os.Lstat(p)
		if lerr != nil || !fi.IsDir() || !fi.ModTime().Before(cutoff) {
			continue
		}
		if keep != nil && keep(p) {
			continue
		}
		makeWritable(p)
		if err := os.RemoveAll(p); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", name, err))
			continue
		}
		removed = append(removed, p)
	}
	sort.Strings(removed)
	return removed, errors.Join(errs...)
}
