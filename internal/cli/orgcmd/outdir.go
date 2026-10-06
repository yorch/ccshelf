package orgcmd

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ccshelf/ccshelf/internal/bundles"
	"github.com/ccshelf/ccshelf/internal/cli/clicore"
	"github.com/ccshelf/ccshelf/internal/ui"
)

// Modes of the published catalog output. The catalog is meant to be served by
// a web server or a Pages action, so it is world-readable; the private-cache
// rule (0700/0600) applies to the tool's own cache, not to this output.
const (
	publishDirMode  fs.FileMode = 0o755
	publishFileMode fs.FileMode = 0o644
)

// staleSiteFiles are the static site files that --no-site removes from an
// output directory that an earlier build filled, so a stale page is never
// served next to a fresh catalog.json.
var staleSiteFiles = []string{"index.html", "app.js", "style.css"}

// outTarget is a validated --out destination.
type outTarget struct {
	// dest is the absolute, cleaned destination.
	dest string
	// anchor is the directory the components of rel are checked and created
	// from: the working directory or the repo root when dest lies below one
	// of them (a symbolic link inside the repo checkout is then refused at
	// every level), else the parent of dest.
	anchor string
	// rel are the components of dest below anchor.
	rel []string
}

// resolveOut makes --out absolute and refuses every destination that could
// damage the repo: the repo root or a directory that contains it, the
// directories that hold sources (profiles, bundles, catalog sidecars, plugins
// and so on), and any path that passes through a symbolic link below the
// working directory or the repo root. Directory identity is decided with
// os.SameFile on the real directories, never by comparing path strings, so
// case-insensitive file systems and symbolic links cannot get around it.
func resolveOut(c *clicore.Context, r *repo, outDir string) (*outTarget, error) {
	if outDir == "" {
		outDir = defaultOut
	}
	wd, err := c.Getwd()
	if err != nil {
		return nil, fmt.Errorf("finding the current directory: %w", err)
	}
	dest := outDir
	if !filepath.IsAbs(dest) {
		dest = filepath.Join(wd, dest)
	}
	dest = filepath.Clean(dest)
	if err := guardRepo(r, dest); err != nil {
		return nil, ui.Usage(err)
	}
	t := &outTarget{dest: dest}
	t.anchor, t.rel = anchorFor(dest, wd, r.root)
	if err := t.checkNoSymlinks(); err != nil {
		return nil, err
	}
	return t, nil
}

// anchorFor picks the deepest of the working directory and the repo root
// (each also with symbolic links resolved) that dest lies below.
func anchorFor(dest string, dirs ...string) (anchor string, rel []string) {
	var cands []string
	for _, d := range dirs {
		if d == "" {
			continue
		}
		cands = append(cands, filepath.Clean(d))
		if real, err := filepath.EvalSymlinks(d); err == nil {
			cands = append(cands, filepath.Clean(real))
		}
	}
	for _, cand := range cands {
		sub, err := filepath.Rel(cand, dest)
		if err != nil || sub == "." || sub == ".." || strings.HasPrefix(sub, ".."+string(filepath.Separator)) || filepath.IsAbs(sub) {
			continue
		}
		if parts := strings.Split(sub, string(filepath.Separator)); anchor == "" || len(parts) < len(rel) {
			anchor, rel = cand, parts
		}
	}
	if anchor == "" {
		return filepath.Dir(dest), []string{filepath.Base(dest)}
	}
	return anchor, rel
}

// protectedDirs are the directories of the repo that hold sources or CI
// configuration: catalog output never goes in or below them.
func protectedDirs(r *repo) []string {
	rels := []string{
		r.cfg.Profiles.Dir, bundles.Dir, "plugins", "prompts", ".github", ".claude-plugin",
		filepath.Dir(r.cfg.Profiles.MCPRegistry), filepath.Dir(r.cfg.Lint.Taxonomy), "catalog",
	}
	for _, m := range r.cfg.Catalog.Marketplaces {
		rels = append(rels, filepath.Dir(filepath.FromSlash(m)))
	}
	var out []string
	for _, rel := range rels {
		rel = filepath.Clean(filepath.FromSlash(rel))
		if rel == "." || rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
			continue
		}
		out = append(out, filepath.Join(r.root, rel))
	}
	return out
}

// guardRepo refuses a destination that equals or contains the repo root, or
// that is, or lies inside, one of its source directories.
func guardRepo(r *repo, dest string) error {
	if di, err := os.Stat(dest); err == nil {
		for p := r.root; ; p = filepath.Dir(p) {
			if pi, err := os.Stat(p); err == nil && os.SameFile(di, pi) {
				return errors.New("--out must not be the repo root or a directory that contains it: pick a separate output directory")
			}
			if filepath.Dir(p) == p {
				break
			}
		}
	}
	prot := protectedDirs(r)
	for a := dest; ; a = filepath.Dir(a) {
		if ai, err := os.Stat(a); err == nil {
			for _, p := range prot {
				if pi, err := os.Stat(p); err == nil && os.SameFile(ai, pi) {
					return fmt.Errorf("--out must not be inside %s, which holds the repo's sources: pick a separate output directory", filepath.Base(p))
				}
			}
		}
		if filepath.Dir(a) == a {
			break
		}
	}
	return nil
}

// checkNoSymlinks looks at every existing component of dest below the anchor
// and refuses a symbolic link or a file that is not a directory.
func (t *outTarget) checkNoSymlinks() error {
	cur := t.anchor
	for _, comp := range t.rel {
		cur = filepath.Join(cur, comp)
		fi, err := os.Lstat(cur)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil // the rest is created by the writer, one level at a time
		case err != nil:
			return fmt.Errorf("inspecting %s: %w", cur, err)
		case fi.Mode()&os.ModeSymlink != 0:
			return fmt.Errorf("--out %s passes through the symbolic link %s; refusing to write through it", t.dest, cur)
		case !fi.IsDir():
			return fmt.Errorf("--out %s: %s is not a directory", t.dest, cur)
		}
	}
	return nil
}

// open creates the missing directories of dest (mode 0755, one level at a
// time, each checked with Lstat) and returns a root confined to dest. The
// returned root cannot be used to reach anything outside dest.
func (t *outTarget) open() (*os.Root, error) {
	if len(t.rel) == 1 && t.anchor == filepath.Dir(t.dest) {
		// A destination outside the working directory and the repo: its
		// parent is the user's explicit choice and is created if missing.
		if err := os.MkdirAll(t.anchor, publishDirMode); err != nil { //nolint:gosec // published output, see publishDirMode
			return nil, fmt.Errorf("creating %s: %w", t.anchor, err)
		}
	}
	cur, err := os.OpenRoot(t.anchor)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", t.anchor, err)
	}
	for _, comp := range t.rel {
		fi, err := cur.Lstat(comp)
		switch {
		case err == nil && fi.Mode()&os.ModeSymlink != 0:
			_ = cur.Close()
			return nil, fmt.Errorf("--out %s passes through a symbolic link at %s; refusing to write through it", t.dest, comp)
		case err == nil && !fi.IsDir():
			_ = cur.Close()
			return nil, fmt.Errorf("%s is not a directory", filepath.Join(t.anchor, comp))
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			_ = cur.Close()
			return nil, fmt.Errorf("inspecting %s: %w", comp, err)
		case err != nil:
			if err := cur.Mkdir(comp, publishDirMode); err != nil {
				_ = cur.Close()
				return nil, fmt.Errorf("creating %s: %w", filepath.Join(t.anchor, comp), err)
			}
			// Mkdir is subject to the umask; a new publish directory is 0755.
			if err := cur.Chmod(comp, publishDirMode); err != nil {
				_ = cur.Close()
				return nil, fmt.Errorf("setting the mode of %s: %w", comp, err)
			}
		}
		next, err := cur.OpenRoot(comp)
		_ = cur.Close()
		if err != nil {
			return nil, fmt.Errorf("opening %s: %w", filepath.Join(t.anchor, comp), err)
		}
		cur = next
	}
	return cur, nil
}

// writeOutput writes files atomically (exclusive temporary file, then rename)
// into the target, with mode 0644 in directories of mode 0755, and removes the
// listed stale files when they are regular files. It refuses to replace
// anything that is not a regular file. It returns the names it removed.
func writeOutput(t *outTarget, files []outFile, prune []string) ([]string, error) {
	dir, err := t.open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()
	for _, f := range files {
		if fi, err := dir.Lstat(f.name); err == nil {
			if !fi.Mode().IsRegular() {
				return nil, fmt.Errorf("refusing to overwrite %s: not a regular file", filepath.Join(t.dest, f.name))
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("inspecting %s: %w", filepath.Join(t.dest, f.name), err)
		}
		if err := writeAtomic(dir, f.name, f.data); err != nil {
			return nil, err
		}
	}
	var removed []string
	for _, name := range prune {
		fi, err := dir.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return removed, fmt.Errorf("inspecting %s: %w", filepath.Join(t.dest, name), err)
		}
		if !fi.Mode().IsRegular() {
			return removed, fmt.Errorf("refusing to remove %s: not a regular file", filepath.Join(t.dest, name))
		}
		if err := dir.Remove(name); err != nil {
			return removed, fmt.Errorf("removing the stale %s: %w", filepath.Join(t.dest, name), err)
		}
		removed = append(removed, name)
	}
	return removed, nil
}

func writeAtomic(dir *os.Root, name string, data []byte) (err error) {
	var tmpName string
	var tmp *os.File
	for range 8 {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return fmt.Errorf("choosing a temporary name for %s: %w", name, err)
		}
		tmpName = "." + name + ".tmp-" + hex.EncodeToString(b[:])
		tmp, err = dir.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, publishFileMode) //nolint:gosec // published output, see publishFileMode
		if err == nil || !errors.Is(err, fs.ErrExist) {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("creating a temporary file for %s: %w", name, err)
	}
	fail := func(err error) error {
		_ = tmp.Close()
		_ = dir.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(fmt.Errorf("writing %s: %w", name, err))
	}
	if err := tmp.Close(); err != nil {
		_ = dir.Remove(tmpName)
		return fmt.Errorf("closing %s: %w", name, err)
	}
	// OpenFile is subject to the umask; published files are 0644.
	if err := dir.Chmod(tmpName, publishFileMode); err != nil {
		_ = dir.Remove(tmpName)
		return fmt.Errorf("setting the mode of %s: %w", name, err)
	}
	if err := dir.Rename(tmpName, name); err != nil {
		_ = dir.Remove(tmpName)
		return fmt.Errorf("replacing %s: %w", name, err)
	}
	return nil
}
