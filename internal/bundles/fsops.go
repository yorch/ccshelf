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
	// PruneStale removes bundles/profile-* directories that are not in the
	// file set, but only if they are provably generated.
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

// staleBundles returns the profile-* directory names below root/bundles that
// are not wanted and are provably generated.
func staleBundles(root string, wanted map[string]bool) ([]string, error) {
	if err := ensureDirs(root, Dir, false); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(root, Dir))
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", Dir, err)
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, Prefix) || wanted[n] || !e.IsDir() {
			continue // e.IsDir is false for symlinks, which are never touched
		}
		if generatedDir(filepath.Join(root, Dir, n), n) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out, nil
}

// generatedDir reports whether dir holds nothing but a generated manifest.
func generatedDir(dir, name string) bool {
	top, err := os.ReadDir(dir)
	if err != nil || len(top) != 1 || top[0].Name() != ".claude-plugin" || !top[0].IsDir() {
		return false
	}
	inner, err := os.ReadDir(filepath.Join(dir, ".claude-plugin"))
	if err != nil || len(inner) != 1 || inner[0].Name() != "plugin.json" || !inner[0].Type().IsRegular() {
		return false
	}
	b, err := readSmall(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	return err == nil && isGenerated(name, b)
}

func wantedDirs(files []File) map[string]bool {
	w := map[string]bool{}
	for _, f := range files {
		if segs := strings.Split(f.Path, "/"); len(segs) > 1 {
			w[segs[1]] = true
		}
	}
	return w
}

func prune(root string, files []File) error {
	stale, err := staleBundles(root, wantedDirs(files))
	if err != nil {
		return err
	}
	for _, n := range stale {
		d := filepath.Join(root, Dir, n)
		for _, p := range []string{
			filepath.Join(d, ".claude-plugin", "plugin.json"),
			filepath.Join(d, ".claude-plugin"),
			d,
		} {
			if err := os.Remove(p); err != nil {
				return fmt.Errorf("remove stale bundle %s: %w", n, err)
			}
		}
	}
	return nil
}
