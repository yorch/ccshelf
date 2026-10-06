package bundles

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// WriteOptions control Write.
type WriteOptions struct {
	// PruneStale removes everything below bundles/ that is not in the file
	// set: stale profile-* directories, extra files inside wanted bundles
	// and non-bundle entries. It refuses (removing nothing) when any of
	// them is a symbolic link or special file.
	PruneStale bool
}

// maxBundleFile is the largest manifest read back.
const maxBundleFile = 1 << 20

// validRel checks a File.Path: relative, forward slashes, below bundles/,
// no empty, dot or dot-dot segments, no backslashes or drive colons.
func validRel(p string) error {
	if p == "" || strings.ContainsAny(p, "\\:\x00") || strings.HasPrefix(p, "/") {
		return fmt.Errorf("%w: %q", errPath, p)
	}
	segs := strings.Split(p, "/")
	if len(segs) < 2 || segs[0] != Dir {
		return fmt.Errorf("%w: %q is not inside %s/", errPath, p, Dir)
	}
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			return fmt.Errorf("%w: %q", errPath, p)
		}
	}
	return nil
}

func rootDir(root string) (string, error) {
	r, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve root %q: %w", root, err)
	}
	fi, err := os.Stat(r)
	if err != nil || !fi.IsDir() {
		return "", fmt.Errorf("root %q is not a directory", root)
	}
	return r, nil
}

// ensureDirs makes every directory of rel (a slash path of directories)
// below root, refusing symlinks and non-directories.
func ensureDirs(root, rel string, create bool) error {
	cur := root
	for _, s := range strings.Split(rel, "/") {
		cur = filepath.Join(cur, s)
		fi, err := os.Lstat(cur)
		switch {
		case err == nil:
			if fi.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("%w: %s is a symbolic link", errPath, cur)
			}
			if !fi.IsDir() {
				return fmt.Errorf("%w: %s is not a directory", errPath, cur)
			}
		case errors.Is(err, fs.ErrNotExist):
			if !create {
				return err
			}
			if err := os.Mkdir(cur, 0o755); err != nil { //nolint:gosec // public repository content, not a secret
				return fmt.Errorf("create %s: %w", cur, err)
			}
		default:
			return fmt.Errorf("inspect %s: %w", cur, err)
		}
	}
	return nil
}

// Write stores the files below <root>/bundles. See the package comment for
// the guarantees.
func Write(root string, files []File, opt ...WriteOptions) error {
	var o WriteOptions
	if len(opt) > 0 {
		o = opt[0]
	}
	seen := map[string]bool{}
	for _, f := range files {
		if err := validRel(f.Path); err != nil {
			return err
		}
		if seen[f.Path] {
			return fmt.Errorf("duplicate file %s", f.Path)
		}
		seen[f.Path] = true
	}
	r, err := rootDir(root)
	if err != nil {
		return err
	}
	for _, f := range files {
		dir := path.Dir(f.Path)
		if err := ensureDirs(r, dir, true); err != nil {
			return err
		}
		if err := writeAtomic(filepath.Join(r, filepath.FromSlash(f.Path)), f.Content); err != nil {
			return err
		}
	}
	if o.PruneStale {
		return prune(r, files)
	}
	return nil
}

func writeAtomic(target string, content []byte) error {
	if fi, err := os.Lstat(target); err == nil {
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("%w: %s exists and is not a regular file", errPath, target)
		}
		if cur, err := readSmall(target); err == nil && bytes.Equal(cur, content) {
			return nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect %s: %w", target, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".ccshelf-bundle-*")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", target, err)
	}
	name := tmp.Name()
	cleanup := func() { _ = os.Remove(name) }
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("write %s: %w", target, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("sync %s: %w", target, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close %s: %w", target, err)
	}
	if err := os.Chmod(name, 0o644); err != nil { //nolint:gosec // public repository content, not a secret
		cleanup()
		return fmt.Errorf("chmod %s: %w", target, err)
	}
	if err := os.Rename(name, target); err != nil {
		cleanup()
		return fmt.Errorf("replace %s: %w", target, err)
	}
	return nil
}

func readSmall(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxBundleFile+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxBundleFile {
		return nil, errors.New("file too large")
	}
	return b, nil
}

// entryKind classifies an unexpected entry below bundles/.
type entryKind int

const (
	kindFile  entryKind = iota // regular file
	kindDir                    // directory
	kindOther                  // symlink, device, socket, FIFO...
)

// extra is one path below bundles/ that the generated set does not contain.
type extra struct {
	rel   string // slash path relative to the root
	kind  entryKind
	stale bool // inside (or is) a profile-* directory no profile produces
}

// maxExtras bounds the number of unexpected entries collected.
const maxExtras = 10000

// scanExtras lists every entry below root/bundles that is not one of the
// wanted files or a directory on the way to one. A directory that is itself
// unexpected is listed with everything below it. Symbolic links and other
// special files are listed (kindOther) and never followed. A missing bundles
// directory yields nothing; a symlinked one is an error.
func scanExtras(root string, files []File) ([]extra, error) {
	if err := ensureDirs(root, Dir, false); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	wantedFile := map[string]bool{}
	wantedDir := map[string]bool{Dir: true}
	wantedProfile := map[string]bool{}
	for _, f := range files {
		wantedFile[f.Path] = true
		segs := strings.Split(f.Path, "/")
		wantedProfile[segs[1]] = true
		for n := 1; n < len(segs); n++ {
			wantedDir[strings.Join(segs[:n], "/")] = true
		}
	}
	var out []extra
	var walk func(rel string, stale bool) error
	walk = func(rel string, stale bool) error {
		ents, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return fmt.Errorf("list %s: %w", rel, err)
		}
		for _, e := range ents {
			child := rel + "/" + e.Name()
			if rel == Dir && strings.HasPrefix(e.Name(), Prefix) && !wantedProfile[e.Name()] {
				stale = true
			} else if rel == Dir {
				stale = false
			}
			if len(out) >= maxExtras {
				return fmt.Errorf("%s holds more than %d unexpected entries", Dir, maxExtras)
			}
			switch {
			case wantedFile[child]:
				continue
			case e.Type()&fs.ModeSymlink != 0 || (!e.IsDir() && !e.Type().IsRegular()):
				out = append(out, extra{rel: child, kind: kindOther, stale: stale})
			case e.IsDir() && wantedDir[child]:
				if err := walk(child, stale); err != nil {
					return err
				}
			case e.IsDir():
				out = append(out, extra{rel: child, kind: kindDir, stale: stale})
				if err := walkAll(root, child, stale, &out); err != nil {
					return err
				}
			default:
				out = append(out, extra{rel: child, kind: kindFile, stale: stale})
			}
		}
		return nil
	}
	if err := walk(Dir, false); err != nil {
		return nil, err
	}
	return out, nil
}

// walkAll lists everything below rel (never following symlinks).
func walkAll(root, rel string, stale bool, out *[]extra) error {
	ents, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return fmt.Errorf("list %s: %w", rel, err)
	}
	for _, e := range ents {
		child := rel + "/" + e.Name()
		if len(*out) >= maxExtras {
			return fmt.Errorf("%s holds more than %d unexpected entries", Dir, maxExtras)
		}
		switch {
		case e.Type()&fs.ModeSymlink != 0 || (!e.IsDir() && !e.Type().IsRegular()):
			*out = append(*out, extra{rel: child, kind: kindOther, stale: stale})
		case e.IsDir():
			*out = append(*out, extra{rel: child, kind: kindDir, stale: stale})
			if err := walkAll(root, child, stale, out); err != nil {
				return err
			}
		default:
			*out = append(*out, extra{rel: child, kind: kindFile, stale: stale})
		}
	}
	return nil
}

// prune removes every unexpected entry below bundles/ (see scanExtras). It
// refuses, before removing anything, when one of them is a symbolic link or
// a special file, so a surprising tree is left intact for a human to look at.
func prune(root string, files []File) error {
	extras, err := scanExtras(root, files)
	if err != nil {
		return err
	}
	for _, x := range extras {
		if x.kind == kindOther {
			return fmt.Errorf("%w: refusing to prune: %s is a symbolic link or special file", errPath, x.rel)
		}
	}
	// Deepest first so directories are empty when removed.
	sort.Slice(extras, func(i, j int) bool {
		di, dj := strings.Count(extras[i].rel, "/"), strings.Count(extras[j].rel, "/")
		if di != dj {
			return di > dj
		}
		return extras[i].rel < extras[j].rel
	})
	for _, x := range extras {
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(x.rel))); err != nil {
			return fmt.Errorf("remove %s: %w", x.rel, err)
		}
	}
	return nil
}
