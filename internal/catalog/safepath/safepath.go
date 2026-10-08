package safepath

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrEscape is returned when a path would leave the repository root.
var ErrEscape = errors.New("path escapes the repository root")

// ErrTooLarge is returned when a file exceeds the size cap.
var ErrTooLarge = errors.New("file is too large")

// ErrNotRegular is returned when a path is not a regular file.
var ErrNotRegular = errors.New("not a regular file")

// CheckRel checks that rel is a relative, slash-separated path that stays
// below its root lexically: not empty, no NUL, no backslash, not absolute, no
// drive letter, and no ".." segment. It does not touch the file system.
func CheckRel(rel string) error {
	if rel == "" {
		return errors.New("path is empty")
	}
	if strings.ContainsRune(rel, 0) {
		return errors.New("path contains a NUL byte")
	}
	if strings.ContainsRune(rel, '\\') {
		return errors.New("path contains a backslash (use forward slashes)")
	}
	if strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" || (len(rel) >= 2 && rel[1] == ':') {
		return fmt.Errorf("path %q is absolute", rel)
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return fmt.Errorf("path %q contains \"..\"", rel)
		}
	}
	return nil
}

// Resolve returns the real absolute path of rel below root. The path does not
// have to exist: Resolve follows the symlinks of the deepest existing ancestor
// and appends the remainder. It fails with ErrEscape when the result is
// outside root, including through a symlink or a dangling symlink.
func Resolve(root, rel string) (string, error) {
	if err := CheckRel(rel); err != nil {
		return "", fmt.Errorf("%w: %w", ErrEscape, err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve root %q: %w", root, err)
	}
	cur := filepath.Join(realRoot, filepath.FromSlash(rel))
	var tail []string
	resolved := ""
	for {
		real, err := filepath.EvalSymlinks(cur)
		if err == nil {
			resolved = real
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("resolve %q: %w", rel, err)
		}
		if _, lerr := os.Lstat(cur); lerr == nil {
			// The entry exists but its target does not: a dangling symlink.
			return "", fmt.Errorf("%w: %q is a dangling symlink", ErrEscape, rel)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("resolve %q: no existing ancestor", rel)
		}
		tail = append([]string{filepath.Base(cur)}, tail...)
		cur = parent
	}
	if !within(realRoot, resolved) {
		return "", fmt.Errorf("%w: %q", ErrEscape, rel)
	}
	out := resolved
	for _, t := range tail {
		out = filepath.Join(out, t)
	}
	return out, nil
}

func within(root, p string) bool {
	r, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) && !filepath.IsAbs(r)
}

// Stat is os.Stat on the confined path.
func Stat(root, rel string) (fs.FileInfo, error) {
	p, err := Resolve(root, rel)
	if err != nil {
		return nil, err
	}
	return os.Stat(p)
}

// Exists reports whether rel exists below root. A path that cannot be
// confined reports false with the confinement error.
func Exists(root, rel string) (bool, error) {
	_, err := Stat(root, rel)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// ReadFile reads the regular file rel below root, failing with ErrTooLarge
// above max bytes.
func ReadFile(root, rel string, max int64) ([]byte, error) {
	p, err := Resolve(root, rel)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %q", ErrNotRegular, rel)
	}
	if st.Size() > max {
		return nil, fmt.Errorf("%w: %q is %d bytes, the limit is %d", ErrTooLarge, rel, st.Size(), max)
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%w: %q exceeds %d bytes", ErrTooLarge, rel, max)
	}
	return data, nil
}

// ReadDir lists the directory rel below root, sorted by name.
func ReadDir(root, rel string) ([]fs.DirEntry, error) {
	p, err := Resolve(root, rel)
	if err != nil {
		return nil, err
	}
	return os.ReadDir(p)
}

// IsDir reports whether rel is a directory below root.
func IsDir(root, rel string) bool {
	st, err := Stat(root, rel)
	return err == nil && st.IsDir()
}

// IsFile reports whether rel is a regular file below root.
func IsFile(root, rel string) bool {
	st, err := Stat(root, rel)
	return err == nil && st.Mode().IsRegular()
}
