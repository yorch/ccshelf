package gitsource

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// MaxFileSize is the largest file allowed under profiles/, mcp/ and prompts/.
const MaxFileSize = 1 << 20

// ErrHygiene is wrapped by content hygiene failures.
var ErrHygiene = errors.New("unacceptable repository content")

// watched are the folders of a source root that are ever read.
var watched = []string{"profiles", "mcp", "prompts"}

type treeEntry struct {
	mode string
	size int64
	path string
}

// parseLsTree parses `git ls-tree -r -z --long` output.
func parseLsTree(out string) ([]treeEntry, error) {
	var res []treeEntry
	for _, rec := range strings.Split(out, "\x00") {
		if rec == "" {
			continue
		}
		tab := strings.IndexByte(rec, '\t')
		if tab < 0 {
			return nil, fmt.Errorf("unexpected git ls-tree output")
		}
		fields := strings.Fields(rec[:tab])
		if len(fields) != 4 {
			return nil, fmt.Errorf("unexpected git ls-tree output")
		}
		size := int64(0)
		if fields[3] != "-" {
			n, err := strconv.ParseInt(fields[3], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("unexpected git ls-tree size %q", fields[3])
			}
			size = n
		}
		res = append(res, treeEntry{mode: fields[0], size: size, path: rec[tab+1:]})
	}
	return res, nil
}

func badComponent(c string) bool {
	if c == "" || c == "." || c == ".." || strings.EqualFold(c, ".git") {
		return true
	}
	for _, r := range c {
		if r == '\\' || unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// isWatched reports whether p is inside (or is) one of the watched folders
// below base.
func isWatched(p, base string) bool {
	rel := p
	if base != "" {
		if !strings.HasPrefix(p, base+"/") {
			return false
		}
		rel = p[len(base)+1:]
	}
	for _, w := range watched {
		if rel == w || strings.HasPrefix(rel, w+"/") {
			return true
		}
	}
	return false
}

// layoutBase decides which folder of the tree is the source root: subpath
// itself when it holds profiles/ (or nothing identifies it), or its parent
// when subpath names the profiles folder.
func layoutBase(entries []treeEntry, subpath string) string {
	prefix := ""
	if subpath != "" {
		prefix = subpath + "/"
	}
	for _, e := range entries {
		if strings.HasPrefix(e.path, prefix+"profiles/") {
			return subpath
		}
	}
	if subpath != "" && path.Base(subpath) == "profiles" {
		d := path.Dir(subpath)
		if d == "." {
			return ""
		}
		return d
	}
	return subpath
}

// checkTree validates the committed tree.
func checkTree(entries []treeEntry, base string) error {
	for _, e := range entries {
		for _, c := range strings.Split(e.path, "/") {
			if badComponent(c) {
				return fmt.Errorf("%w: path %q has an unsafe component", ErrHygiene, e.path)
			}
		}
		if !isWatched(e.path, base) {
			continue
		}
		switch {
		case e.mode == "120000":
			return fmt.Errorf("%w: %s is a symlink", ErrHygiene, e.path)
		case e.mode == "160000":
			return fmt.Errorf("%w: %s is a submodule", ErrHygiene, e.path)
		case e.size > MaxFileSize:
			return fmt.Errorf("%w: %s is larger than %d bytes", ErrHygiene, e.path, MaxFileSize)
		}
	}
	return nil
}

// checkDisk validates the working tree under root (the source root).
func checkDisk(checkout, root string) error {
	rel, err := filepath.Rel(checkout, root)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrHygiene, err)
	}
	cur := checkout
	if rel != "." {
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			cur = filepath.Join(cur, part)
			fi, err := os.Lstat(cur)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return fmt.Errorf("inspecting %s: %w", cur, err)
			}
			if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
				return fmt.Errorf("%w: %s is not a plain directory", ErrHygiene, part)
			}
		}
	}
	for _, w := range watched {
		dir := filepath.Join(root, w)
		fi, err := os.Lstat(dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("inspecting %s: %w", dir, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
			return fmt.Errorf("%w: %s is not a plain directory", ErrHygiene, w)
		}
		err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("%w: %s is a symlink", ErrHygiene, shortPath(checkout, p))
			}
			if d.IsDir() {
				return nil
			}
			if !d.Type().IsRegular() {
				return fmt.Errorf("%w: %s is not a regular file", ErrHygiene, shortPath(checkout, p))
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if info.Size() > MaxFileSize {
				return fmt.Errorf("%w: %s is larger than %d bytes", ErrHygiene, shortPath(checkout, p), MaxFileSize)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func shortPath(root, p string) string {
	if r, err := filepath.Rel(root, p); err == nil {
		return filepath.ToSlash(r)
	}
	return p
}
