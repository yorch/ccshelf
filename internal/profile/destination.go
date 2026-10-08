package profile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ProjectProfilesDir finds the nearest Git working-tree marker (a directory or
// a worktree's .git file). Outside Git, profiles belong to the current directory.
// This is destination discovery only; it grants neither activation nor trust.
func ProjectProfilesDir(cwd string) (string, error) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("working directory: %w", err)
	}
	root := abs
	for dir := abs; ; dir = filepath.Dir(dir) {
		fi, err := os.Lstat(filepath.Join(dir, ".git"))
		if err == nil {
			if fi.Mode()&refusedMode != 0 || (!fi.IsDir() && !fi.Mode().IsRegular()) {
				return "", fmt.Errorf("%w: unsafe .git marker in %s", ErrPath, dir)
			}
			root = dir
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return filepath.Join(root, ".ccshelf", "profiles"), nil
}

// CheckDestination refuses symlinks and non-directories at the source root and
// profiles directory, even when the namespace is disabled. Missing directories
// are valid destinations, and this check does not create them.
func CheckDestination(dir string) error {
	for _, p := range []string{filepath.Dir(dir), dir} {
		fi, err := os.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if fi.Mode()&refusedMode != 0 || !fi.IsDir() {
			return fmt.Errorf("%w: %s is not a plain directory (symlinks are refused)", ErrPath, p)
		}
	}
	return nil
}

// nearestDir accepts a path whose nearest existing component (following
// symlinks) is a directory; missing components are valid and not created.
func nearestDir(path string) error {
	for p := path; ; p = filepath.Dir(p) {
		fi, err := os.Stat(p)
		if err == nil {
			if !fi.IsDir() {
				return fmt.Errorf("%w: %s is not a directory", ErrPath, p)
			}
			return nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if filepath.Dir(p) == p {
			return nil
		}
	}
}

// CheckPersonalDestination validates the personal profiles directory. The
// personal config root is the user's own path and may be reached through
// symlinks (a dotfiles-managed directory), but the profiles directory itself
// must be a plain directory, exactly as the reader requires. Missing
// directories are valid, and this check does not create them.
func CheckPersonalDestination(dir string) error {
	if err := nearestDir(filepath.Dir(dir)); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode()&refusedMode != 0 || !fi.IsDir() {
		return fmt.Errorf("%w: %s is not a plain directory (symlinks are refused)", ErrPath, dir)
	}
	return nil
}

// openConfined opens the directory component below parent, creating it 0700
// when missing. It refuses symlinks, irregular files and a component that was
// replaced while it was opened, so callers write below a directory that cannot
// be swapped for a link.
func openConfined(parent *os.Root, component, display string) (*os.Root, error) {
	if err := parent.Mkdir(component, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("creating %s: %w", display, err)
	}
	fi, err := parent.Lstat(component)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&refusedMode != 0 || !fi.IsDir() {
		return nil, fmt.Errorf("%w: %s is not a plain directory", ErrPath, component)
	}
	next, err := parent.OpenRoot(component)
	if err != nil {
		return nil, err
	}
	actual, err := next.Stat(".")
	if err != nil {
		_ = next.Close()
		return nil, err
	}
	if !os.SameFile(fi, actual) {
		_ = next.Close()
		return nil, fmt.Errorf("%w: destination changed while opening", ErrPath)
	}
	return next, nil
}

// WriteNewProfile writes exclusively below an opened directory anchor. Newly
// created directories are private. It refuses symlinks at the source root or
// profiles directory. os.Root confines operations to opened directories,
// rather than following replacement path ancestors. Callers validate before writing.
func WriteNewProfile(target string, data []byte) error {
	dir := filepath.Dir(target)
	if err := CheckDestination(dir); err != nil {
		return err
	}
	sourceRoot := filepath.Dir(dir)
	anchor := filepath.Dir(sourceRoot)
	for {
		fi, err := os.Stat(anchor)
		if err == nil {
			if !fi.IsDir() {
				return fmt.Errorf("%w: %s is not a directory", ErrPath, anchor)
			}
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		parent := filepath.Dir(anchor)
		if parent == anchor {
			return err
		}
		anchor = parent
	}
	root, err := os.OpenRoot(anchor)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	rel, err := filepath.Rel(anchor, dir)
	if err != nil || !filepath.IsLocal(rel) {
		return fmt.Errorf("%w: destination is outside its directory", ErrPath)
	}
	current := root
	for _, component := range strings.Split(rel, string(filepath.Separator)) {
		next, err := openConfined(current, component, dir)
		if err != nil {
			return err
		}
		if current != root {
			_ = current.Close()
		}
		current = next
	}
	if current != root {
		defer func() { _ = current.Close() }()
	}
	name := filepath.Base(target)
	f, err := current.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%s already exists", target)
		}
		return fmt.Errorf("creating %s: %w", target, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = current.Remove(name)
		return fmt.Errorf("writing %s: %w", target, err)
	}
	if err := f.Close(); err != nil {
		_ = current.Remove(name)
		return fmt.Errorf("writing %s: %w", target, err)
	}
	return nil
}

// WriteNewPersonalProfile writes one personal profile exclusively. The personal
// config root may be reached through symlinks, matching the reader. The
// profiles directory below it must be a plain directory, also matching the
// reader. The file is created exclusively and a replaced directory is detected
// before writing. Callers validate before writing.
func WriteNewPersonalProfile(target string, data []byte) error {
	dir := filepath.Dir(target)
	if err := CheckPersonalDestination(dir); err != nil {
		return err
	}
	rootPath := filepath.Dir(dir)
	if err := os.MkdirAll(rootPath, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", rootPath, err)
	}
	resolved, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPath, err)
	}
	root, err := os.OpenRoot(resolved)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	current, err := openConfined(root, filepath.Base(dir), dir)
	if err != nil {
		return err
	}
	defer func() { _ = current.Close() }()
	name := filepath.Base(target)
	f, err := current.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%s already exists", target)
		}
		return fmt.Errorf("creating %s: %w", target, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = current.Remove(name)
		return fmt.Errorf("writing %s: %w", target, err)
	}
	if err := f.Close(); err != nil {
		_ = current.Remove(name)
		return fmt.Errorf("writing %s: %w", target, err)
	}
	return nil
}
