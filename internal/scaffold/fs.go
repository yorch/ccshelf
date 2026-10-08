package scaffold

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
)

// Modes of what the package writes. The output is meant to be committed and
// read by others, so files are 0644 and directories 0755; the private-cache
// rule (0600/0700) is for the tool's own cache, not for a repository.
const (
	fileMode fs.FileMode = 0o644
	dirMode  fs.FileMode = 0o755
)

// ErrSymlink is returned when a path passes through, or is, a symbolic link.
var ErrSymlink = errors.New("refusing to follow a symbolic link")

// ErrNotRegular is returned for a path that is not a regular file.
var ErrNotRegular = errors.New("not a regular file")

// ErrTooLarge is returned by ReadFile for a file over the limit.
var ErrTooLarge = errors.New("file is too large")

// FS is the file system the plan is built from and applied to. Names are
// slash-separated and relative to the target directory ("." is the target).
// An implementation refuses anything else, and it never follows or writes
// through a symbolic link.
type FS interface {
	// Lstat describes name without following a final symbolic link. A missing
	// name (or a missing intermediate directory) returns fs.ErrNotExist. An
	// intermediate symbolic link returns ErrSymlink.
	Lstat(name string) (fs.FileInfo, error)
	// ReadFile reads a regular file of at most limit bytes (ErrNotRegular,
	// ErrSymlink, ErrTooLarge otherwise).
	ReadFile(name string, limit int64) ([]byte, error)
	// ReadDir lists a directory, sorted by name.
	ReadDir(name string) ([]fs.DirEntry, error)
	// MkdirAll creates name and its missing parents (mode 0755), one level at
	// a time, refusing symbolic links.
	MkdirAll(name string) error
	// WriteNew creates name atomically and exclusively: it returns an error
	// wrapping fs.ErrExist when name exists, and never replaces anything.
	WriteNew(name string, data []byte, perm fs.FileMode) error
	// Replace atomically replaces the regular file name.
	Replace(name string, data []byte, perm fs.FileMode) error
	// Remove deletes the regular file or the empty directory name. It never
	// removes a symbolic link or a directory that has content.
	Remove(name string) error
}

// checkName refuses names that are not plain relative slash paths.
func checkName(name string) error {
	if name != "." && !fs.ValidPath(name) {
		return fmt.Errorf("invalid path %q", name)
	}
	if strings.ContainsAny(name, "\\\x00") || (len(name) >= 2 && name[1] == ':') {
		return fmt.Errorf("invalid path %q", name)
	}
	return nil
}

// emptyFS is the FS of a target directory that does not exist yet: every read
// finds nothing. It is used to plan; writing needs a real directory.
type emptyFS struct{}

// Empty returns the FS of a directory that does not exist yet.
func Empty() FS { return emptyFS{} }

// Lstat implements FS.
func (emptyFS) Lstat(name string) (fs.FileInfo, error) {
	if err := checkName(name); err != nil {
		return nil, err
	}
	return nil, fs.ErrNotExist
}

// ReadFile implements FS.
func (emptyFS) ReadFile(name string, _ int64) ([]byte, error) {
	if err := checkName(name); err != nil {
		return nil, err
	}
	return nil, fs.ErrNotExist
}

// ReadDir implements FS.
func (emptyFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := checkName(name); err != nil {
		return nil, err
	}
	return nil, fs.ErrNotExist
}

// MkdirAll implements FS.
func (emptyFS) MkdirAll(string) error { return errors.New("the target directory does not exist") }

// WriteNew implements FS.
func (emptyFS) WriteNew(string, []byte, fs.FileMode) error {
	return errors.New("the target directory does not exist")
}

// Replace implements FS.
func (emptyFS) Replace(string, []byte, fs.FileMode) error {
	return errors.New("the target directory does not exist")
}

// Remove implements FS.
func (emptyFS) Remove(string) error { return errors.New("the target directory does not exist") }

// osFS is an FS over a directory, confined with os.Root.
type osFS struct {
	root *os.Root
}

// OpenDir opens the existing directory dir as an FS and returns it with the
// function that closes it. Every operation is confined to dir: os.Root rejects
// escaping paths and symbolic links that leave it, and on top of that this FS
// refuses every symbolic link inside it.
func OpenDir(dir string) (FS, io.Closer, error) {
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("opening %s: %w", dir, err)
	}
	return &osFS{root: r}, r, nil
}

// walk checks that every ancestor directory of name exists as a real
// directory. It returns fs.ErrNotExist for a missing one.
func (o *osFS) walk(name string) error {
	if name == "." {
		return nil
	}
	parts := strings.Split(name, "/")
	for i := 1; i < len(parts); i++ {
		anc := strings.Join(parts[:i], "/")
		fi, err := o.root.Lstat(anc)
		switch {
		case err != nil:
			return err
		case fi.Mode()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s: %w", anc, ErrSymlink)
		case !fi.IsDir():
			return fmt.Errorf("%s is not a directory", anc)
		}
	}
	return nil
}

// Lstat implements FS.
func (o *osFS) Lstat(name string) (fs.FileInfo, error) {
	if err := checkName(name); err != nil {
		return nil, err
	}
	if err := o.walk(name); err != nil {
		return nil, err
	}
	return o.root.Lstat(name)
}

// ReadFile implements FS.
func (o *osFS) ReadFile(name string, limit int64) ([]byte, error) {
	fi, err := o.Lstat(name)
	if err != nil {
		return nil, err
	}
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		return nil, fmt.Errorf("%s: %w", name, ErrSymlink)
	case !fi.Mode().IsRegular():
		return nil, fmt.Errorf("%s: %w", name, ErrNotRegular)
	case fi.Size() > limit:
		return nil, fmt.Errorf("%s: %w (limit %d bytes)", name, ErrTooLarge, limit)
	}
	f, err := o.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	// Re-check on the open handle: the file may have been swapped since Lstat.
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: %w", name, ErrNotRegular)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s: %w (limit %d bytes)", name, ErrTooLarge, limit)
	}
	return data, nil
}

// ReadDir implements FS.
func (o *osFS) ReadDir(name string) ([]fs.DirEntry, error) {
	fi, err := o.Lstat(name)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s: %w", name, ErrSymlink)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", name)
	}
	d, err := o.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = d.Close() }()
	ents, err := d.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	sort.Slice(ents, func(i, j int) bool { return ents[i].Name() < ents[j].Name() })
	return ents, nil
}

// MkdirAll implements FS.
func (o *osFS) MkdirAll(name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	if name == "." {
		return nil
	}
	parts := strings.Split(name, "/")
	for i := range parts {
		cur := strings.Join(parts[:i+1], "/")
		fi, err := o.root.Lstat(cur)
		switch {
		case err == nil && fi.Mode()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s: %w", cur, ErrSymlink)
		case err == nil && !fi.IsDir():
			return fmt.Errorf("%s exists and is not a directory", cur)
		case err == nil:
			continue
		case !errors.Is(err, fs.ErrNotExist):
			return err
		}
		if err := o.root.Mkdir(cur, dirMode); err != nil && !errors.Is(err, fs.ErrExist) { //nolint:gosec // committed repository content, see dirMode
			return err
		}
		// Mkdir is subject to the umask; repository directories are 0755.
		if err := o.root.Chmod(cur, dirMode); err != nil { //nolint:gosec // see dirMode
			return err
		}
	}
	return nil
}

// tempIn creates an exclusive temporary file next to name.
func (o *osFS) tempIn(name string, perm fs.FileMode) (*os.File, string, error) {
	dir, base := path.Split(name)
	for range 8 {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, "", err
		}
		tmp := dir + "." + base + ".ccshelf-tmp-" + hex.EncodeToString(b[:])
		f, err := o.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm) //nolint:gosec // see fileMode
		if err == nil {
			return f, tmp, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("no free temporary name for %s", name)
}

// stage writes data to a temporary file next to name and returns its name.
func (o *osFS) stage(name string, data []byte, perm fs.FileMode) (string, error) {
	f, tmp, err := o.tempIn(name, perm)
	if err != nil {
		return "", fmt.Errorf("creating a temporary file for %s: %w", name, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = o.root.Remove(tmp)
		return "", fmt.Errorf("writing %s: %w", name, err)
	}
	if err := f.Close(); err != nil {
		_ = o.root.Remove(tmp)
		return "", fmt.Errorf("closing %s: %w", name, err)
	}
	// OpenFile is subject to the umask; generated files get the mode asked for.
	if err := o.root.Chmod(tmp, perm); err != nil { //nolint:gosec // see fileMode
		_ = o.root.Remove(tmp)
		return "", fmt.Errorf("setting the mode of %s: %w", name, err)
	}
	return tmp, nil
}

// WriteNew implements FS.
func (o *osFS) WriteNew(name string, data []byte, perm fs.FileMode) error {
	if err := checkName(name); err != nil {
		return err
	}
	if err := o.MkdirAll(path.Dir(name)); err != nil {
		return err
	}
	tmp, err := o.stage(name, data, perm)
	if err != nil {
		return err
	}
	// A hard link fails when name exists, which makes the creation exclusive
	// without a window between the check and the write. Where links are not
	// supported, fall back to a check and a rename.
	err = o.root.Link(tmp, name)
	if err == nil {
		_ = o.root.Remove(tmp)
		return nil
	}
	if errors.Is(err, fs.ErrExist) {
		_ = o.root.Remove(tmp)
		return fmt.Errorf("%s: %w", name, fs.ErrExist)
	}
	if _, serr := o.root.Lstat(name); serr == nil {
		_ = o.root.Remove(tmp)
		return fmt.Errorf("%s: %w", name, fs.ErrExist)
	}
	if rerr := o.root.Rename(tmp, name); rerr != nil {
		_ = o.root.Remove(tmp)
		return fmt.Errorf("creating %s: %w", name, rerr)
	}
	return nil
}

// Replace implements FS.
func (o *osFS) Replace(name string, data []byte, perm fs.FileMode) error {
	fi, err := o.Lstat(name)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s: %w", name, ErrNotRegular)
	}
	tmp, err := o.stage(name, data, perm)
	if err != nil {
		return err
	}
	if err := o.root.Rename(tmp, name); err != nil {
		_ = o.root.Remove(tmp)
		return fmt.Errorf("replacing %s: %w", name, err)
	}
	return nil
}

// Remove implements FS.
func (o *osFS) Remove(name string) error {
	if name == "." {
		return errors.New("refusing to remove the target directory")
	}
	fi, err := o.Lstat(name)
	if err != nil {
		return err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%s: %w", name, ErrSymlink)
	}
	if !fi.Mode().IsRegular() && !fi.IsDir() {
		return fmt.Errorf("%s: %w", name, ErrNotRegular)
	}
	return o.root.Remove(name)
}
