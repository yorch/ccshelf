package scaffold

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/yorch/ccshelf/internal/ui"
)

// TargetError is a refused target directory. Usage is true when the argument
// itself is wrong (the command exits 2), false for a condition of the file
// system (exit 1).
type TargetError struct {
	Msg   string
	Usage bool
}

func (e *TargetError) Error() string { return e.Msg }

// TargetOptions describe the directory argument.
type TargetOptions struct {
	// Arg is the directory as typed. An empty Arg means the working directory.
	Arg string
	// Wd is the working directory.
	Wd string
	// Home is the user's home directory, or "" when unknown.
	Home string
	// Forbidden are directories that a target may be neither equal to nor
	// inside: Claude Code's configuration directories and ccshelf's own
	// configuration and cache directories. Relative entries are ignored.
	Forbidden []string
}

// Target is a validated target directory.
type Target struct {
	// Dir is the absolute, cleaned path.
	Dir string
	// Exists is false when the directory has to be created.
	Exists bool
	// explicit is true when the path was given as an argument.
	explicit bool
}

// toolModule is the module path of the tool repository. A directory that
// holds a go.mod for it (or for a fork, which keeps the last element) is the
// tool repository, never an org data repo.
const toolModule = "github.com/yorch/ccshelf"

// ResolveTarget validates the directory argument and returns the target. It
// refuses (without touching anything):
//   - an argument with a ".." component or a control character
//   - the file system root, the user's home directory and every directory
//     above it (an org data repo is never that broad)
//   - a directory that is, or lies inside, one of o.Forbidden (the Claude Code
//     and ccshelf configuration and cache directories: this tool never writes
//     into them)
//   - a directory that is, or lies inside, the ccshelf tool repository
//   - a target that is a symbolic link or not a directory, and, for a relative
//     argument, any component below the working directory that is a symbolic
//     link. Symbolic links above an absolute path are the caller's explicit
//     choice (macOS temporary directories are links, for one).
func ResolveTarget(o TargetOptions) (*Target, error) {
	arg := o.Arg
	if ui.HasControl(arg) {
		return nil, &TargetError{Msg: "the directory contains a control character", Usage: true}
	}
	for _, comp := range strings.FieldsFunc(arg, func(r rune) bool { return r == '/' || r == '\\' }) {
		if comp == ".." {
			return nil, &TargetError{Msg: fmt.Sprintf("the directory %q contains \"..\": pass a path without it", ui.SanitizeLine(arg)), Usage: true}
		}
	}
	dir := arg
	if dir == "" {
		dir = "."
	}
	if !filepath.IsAbs(dir) {
		if o.Wd == "" || !filepath.IsAbs(o.Wd) {
			return nil, &TargetError{Msg: "cannot find the working directory"}
		}
		dir = filepath.Join(o.Wd, dir)
	}
	dir = filepath.Clean(dir)
	t := &Target{Dir: dir, explicit: arg != "" && filepath.Clean(arg) != "."}
	if filepath.Dir(dir) == dir {
		return nil, &TargetError{Msg: "refusing to use the file system root as an org data repo", Usage: true}
	}
	if err := checkNotHome(dir, o.Home); err != nil {
		return nil, err
	}
	if err := checkNotForbidden(dir, o.Forbidden); err != nil {
		return nil, err
	}
	if err := checkNotToolRepo(dir); err != nil {
		return nil, err
	}
	if err := t.checkSymlinks(o); err != nil {
		return nil, err
	}
	return t, nil
}

func checkNotHome(dir, home string) error {
	if home == "" {
		return nil
	}
	if filepath.IsAbs(home) && within(resolveLoose(home), resolveLoose(dir)) {
		// The home directory is the target or lies below it: the target is an
		// ancestor of home, which holds everything of the user.
		return &TargetError{Msg: "refusing to use your home directory, or a directory above it, as an org data repo", Usage: true}
	}
	di, err := os.Stat(dir)
	if err != nil {
		if filepath.Clean(home) == dir {
			return &TargetError{Msg: "refusing to use your home directory as an org data repo", Usage: true}
		}
		return nil
	}
	if hi, err := os.Stat(home); err == nil && os.SameFile(di, hi) {
		return &TargetError{Msg: "refusing to use your home directory as an org data repo", Usage: true}
	}
	return nil
}

// resolveLoose resolves symbolic links in the existing part of p and appends
// the part that does not exist, so that a directory that is yet to be created
// compares with the real directories around it.
func resolveLoose(p string) string {
	p = filepath.Clean(p)
	var tail []string
	for cur := p; ; cur = filepath.Dir(cur) {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				r = filepath.Join(r, tail[i])
			}
			return r
		}
		if filepath.Dir(cur) == cur {
			return p
		}
		tail = append(tail, filepath.Base(cur))
	}
}

func samePath(a, b string) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// within reports whether p is dir or lies below it (both resolved).
func within(p, dir string) bool {
	if samePath(p, dir) {
		return true
	}
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// checkNotForbidden refuses a target equal to or inside one of the forbidden
// directories, and a target above the home directory (an ancestor of it).
func checkNotForbidden(dir string, forbidden []string) error {
	resolved := resolveLoose(dir)
	for _, f := range forbidden {
		if f == "" || !filepath.IsAbs(f) {
			continue
		}
		if within(resolved, resolveLoose(f)) {
			return &TargetError{Msg: fmt.Sprintf("refusing to use %s: it is, or is inside, %s, a directory of Claude Code or ccshelf that this command never writes to", ui.SanitizeLine(dir), ui.SanitizeLine(f)), Usage: true}
		}
	}
	return nil
}

// checkNotToolRepo looks for the tool repository's go.mod in dir and in each
// of its parents.
func checkNotToolRepo(dir string) error {
	for p := dir; ; p = filepath.Dir(p) {
		if isToolModule(filepath.Join(p, "go.mod")) {
			return &TargetError{Msg: fmt.Sprintf("%s is, or is inside, the ccshelf tool repository: an org data repo lives in its own repository", ui.SanitizeLine(p)), Usage: true}
		}
		if filepath.Dir(p) == p {
			return nil
		}
	}
}

// isToolModule reports whether the file is a go.mod whose module path is the
// tool's (or ends in /ccshelf, which a fork keeps).
func isToolModule(path string) bool {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > 64<<10 {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(io.LimitReader(f, 64<<10))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, "module"); ok && (rest == "" || rest[0] == ' ' || rest[0] == '\t') {
			rest, _, _ = strings.Cut(rest, "//")
			mod := strings.Trim(strings.TrimSpace(rest), `"`)
			return mod == toolModule || strings.HasSuffix(mod, "/ccshelf")
		}
	}
	return false
}

// checkSymlinks refuses a target that is a link or not a directory, and links
// below the working directory on a relative path.
func (t *Target) checkSymlinks(o TargetOptions) error {
	if o.Wd != "" && filepath.IsAbs(o.Wd) && !filepath.IsAbs(o.Arg) && o.Arg != "" {
		if rel, err := filepath.Rel(o.Wd, t.Dir); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			cur := filepath.Clean(o.Wd)
			for _, comp := range strings.Split(rel, string(filepath.Separator)) {
				cur = filepath.Join(cur, comp)
				fi, err := os.Lstat(cur)
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				if err != nil {
					return &TargetError{Msg: fmt.Sprintf("inspecting %s: %v", ui.SanitizeLine(cur), err)}
				}
				if fi.Mode()&fs.ModeSymlink != 0 {
					return &TargetError{Msg: fmt.Sprintf("%s is a symbolic link: refusing to write through it", ui.SanitizeLine(cur))}
				}
				if !fi.IsDir() {
					return &TargetError{Msg: fmt.Sprintf("%s is not a directory", ui.SanitizeLine(cur))}
				}
			}
		}
	}
	fi, err := os.Lstat(t.Dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return &TargetError{Msg: fmt.Sprintf("inspecting %s: %v", ui.SanitizeLine(t.Dir), err)}
	case t.explicit && fi.Mode()&fs.ModeSymlink != 0:
		return &TargetError{Msg: fmt.Sprintf("%s is a symbolic link: refusing to write through it (pass the real directory)", ui.SanitizeLine(t.Dir))}
	}
	if st, err := os.Stat(t.Dir); err != nil || !st.IsDir() {
		return &TargetError{Msg: fmt.Sprintf("%s is not a directory", ui.SanitizeLine(t.Dir))}
	}
	t.Exists = true
	return nil
}

// Open returns the FS of the target for planning: the real directory when it
// exists, the empty FS when it does not. The caller must call the returned closer.
func (t *Target) Open() (FS, io.Closer, error) {
	if !t.Exists {
		return Empty(), nopCloser{}, nil
	}
	return t.open()
}

func (t *Target) open() (FS, io.Closer, error) {
	f, c, err := OpenDir(t.Dir)
	if err != nil {
		return nil, nil, err
	}
	if t.explicit {
		// The target may have been swapped for a link since ResolveTarget.
		li, lerr := os.Lstat(t.Dir)
		si, serr := os.Stat(t.Dir)
		if lerr != nil || serr != nil || !os.SameFile(li, si) {
			_ = c.Close()
			return nil, nil, &TargetError{Msg: fmt.Sprintf("%s changed while ccshelf opened it: refusing to continue", ui.SanitizeLine(t.Dir))}
		}
	}
	return f, c, nil
}

// Create makes the target directory (and missing parents, mode 0755) when it
// does not exist and returns the writable FS. Call it only when there is
// something to write.
func (t *Target) Create() (FS, io.Closer, error) {
	if !t.Exists {
		if err := os.MkdirAll(t.Dir, dirMode); err != nil { //nolint:gosec // the org data repo is committed content, see dirMode
			return nil, nil, fmt.Errorf("creating %s: %w", t.Dir, err)
		}
		t.Exists = true
	}
	return t.open()
}

type nopCloser struct{}

// Close does nothing.
func (nopCloser) Close() error { return nil }
