package gitsource

import (
	"crypto/sha1" //nolint:gosec // git object ids of SHA-1 repositories
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/ccshelf/ccshelf/internal/ui"
)

// MaxFileSize is the largest file allowed under profiles/, mcp/ and prompts/.
const MaxFileSize = 1 << 20

// ErrHygiene is wrapped by content hygiene failures.
var ErrHygiene = errors.New("unacceptable repository content")

const (
	// MaxWatchedFiles is the most files read from the watched folders.
	MaxWatchedFiles = 5000
	// MaxWatchedBytes is the most content read from the watched folders.
	MaxWatchedBytes = 32 << 20
)

// watched are the folders of a source root that are ever read.
var watched = []string{"profiles", "mcp", "prompts"}

type treeEntry struct {
	mode string
	typ  string
	oid  string
	// size is the blob size; sizeUnknown is set when git could not tell (a
	// blob the server left out because it is larger than the fetch limit).
	size        int64
	sizeUnknown bool
	path        string
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
		e := treeEntry{mode: fields[0], typ: fields[1], oid: fields[2], path: rec[tab+1:]}
		switch fields[3] {
		case "-":
		case "BAD":
			e.sizeUnknown = true
		default:
			n, err := strconv.ParseInt(fields[3], 10, 64)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("unexpected git ls-tree size %q", fields[3])
			}
			e.size = n
		}
		res = append(res, e)
	}
	return res, nil
}

func badComponent(c string) bool {
	if c == "" || c == "." || c == ".." || strings.EqualFold(c, ".git") {
		return true
	}
	for _, r := range c {
		if r == '\\' || r == ':' || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
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

// checkTree validates the committed tree before anything is written: every
// path, and for the watched folders the entry type, the size and the totals.
func checkTree(entries []treeEntry, base string) error {
	files, total := 0, int64(0)
	for _, e := range entries {
		for _, c := range strings.Split(e.path, "/") {
			if badComponent(c) {
				return fmt.Errorf("%w: path %q has an unsafe component", ErrHygiene, ui.SanitizeLine(e.path))
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
		case e.mode != "100644" && e.mode != "100755":
			return fmt.Errorf("%w: %s has the unsupported mode %s", ErrHygiene, e.path, ui.SanitizeLine(e.mode))
		case e.sizeUnknown:
			return fmt.Errorf("%w: %s is missing or larger than %d bytes", ErrHygiene, e.path, MaxFileSize)
		case e.size > MaxFileSize:
			return fmt.Errorf("%w: %s is larger than %d bytes", ErrHygiene, e.path, MaxFileSize)
		}
		files++
		total += e.size
		if files > MaxWatchedFiles || total > MaxWatchedBytes {
			return fmt.Errorf("%w: the profiles, mcp and prompts folders hold more than %d files or %d bytes", ErrHygiene, MaxWatchedFiles, MaxWatchedBytes)
		}
	}
	return nil
}

// blobHasher returns the hash git uses for objects of the repository whose
// object ids look like oid.
func blobHasher(oid string) (hash.Hash, error) {
	switch len(oid) {
	case 40:
		return sha1.New(), nil //nolint:gosec // the object format of the repository
	case 64:
		return sha256.New(), nil
	}
	return nil, fmt.Errorf("%w: object id %q has an unexpected length", ErrTampered, oid)
}

// gitObjectID returns the object id git gives to content of the given type in
// a repository whose ids look like like.
func gitObjectID(like, typ string, content []byte) (string, error) {
	h, err := blobHasher(like)
	if err != nil {
		return "", err
	}
	_, _ = fmt.Fprintf(h, "%s %d\x00", typ, len(content))
	_, _ = h.Write(content)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// expectedFiles maps the path of every file of the watched folders to its
// tree entry, and lists their directories.
func expectedFiles(entries []treeEntry, base string) (files map[string]treeEntry, dirs map[string]bool) {
	files, dirs = map[string]treeEntry{}, map[string]bool{}
	for _, e := range entries {
		if !isWatched(e.path, base) {
			continue
		}
		files[e.path] = e
		for d := path.Dir(e.path); d != "." && d != "/"; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	return files, dirs
}

// verifyDisk checks that the watched folders under checkout hold exactly the
// committed files, byte for byte (the content is hashed the way git hashes a
// blob and compared with the id in the tree). Structural problems (symlinks,
// special files, a watched folder that is not a plain directory) are
// ErrHygiene; missing, extra or changed files are ErrTampered. Nothing outside
// the watched folders is ever read, so nothing there can matter.
func verifyDisk(checkout, base string, entries []treeEntry) error {
	root := checkout
	if base != "" {
		root = filepath.Join(checkout, filepath.FromSlash(base))
		cur := checkout
		for _, part := range strings.Split(base, "/") {
			cur = filepath.Join(cur, part)
			fi, err := os.Lstat(cur)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					break
				}
				return fmt.Errorf("inspecting %s: %w", cur, err)
			}
			if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
				return fmt.Errorf("%w: %s is not a plain directory", ErrHygiene, part)
			}
		}
	}
	files, dirs := expectedFiles(entries, base)
	seen := map[string]bool{}
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
			rel := shortPath(checkout, p)
			if d.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("%w: %s is a symlink", ErrHygiene, rel)
			}
			if d.IsDir() {
				if p != dir && !dirs[rel] {
					return fmt.Errorf("%w: %s is not in commit", ErrTampered, rel)
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return fmt.Errorf("%w: %s is not a regular file", ErrHygiene, rel)
			}
			want, ok := files[rel]
			if !ok {
				return fmt.Errorf("%w: %s is not in the commit", ErrTampered, rel)
			}
			seen[rel] = true
			return verifyFile(p, rel, want)
		})
		if err != nil {
			return err
		}
	}
	for p := range files {
		if !seen[p] {
			return fmt.Errorf("%w: %s is missing", ErrTampered, p)
		}
	}
	return nil
}

// verifyFile compares one file on disk with its tree entry: size first, then
// content.
func verifyFile(p, rel string, want treeEntry) error {
	info, err := os.Lstat(p)
	if err != nil {
		return fmt.Errorf("inspecting %s: %w", rel, err)
	}
	if info.Size() > MaxFileSize {
		return fmt.Errorf("%w: %s is larger than %d bytes", ErrHygiene, rel, MaxFileSize)
	}
	if info.Size() != want.size {
		return fmt.Errorf("%w: %s is %d bytes on disk, the commit has %d", ErrTampered, rel, info.Size(), want.size)
	}
	f, err := os.Open(p) //nolint:gosec // a path under the verified checkout
	if err != nil {
		return fmt.Errorf("reading %s: %w", rel, err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxFileSize+1))
	if err != nil {
		return fmt.Errorf("reading %s: %w", rel, err)
	}
	id, err := gitObjectID(want.oid, "blob", b)
	if err != nil {
		return err
	}
	if id != want.oid {
		return fmt.Errorf("%w: %s differs from the commit", ErrTampered, rel)
	}
	return nil
}

func shortPath(root, p string) string {
	if r, err := filepath.Rel(root, p); err == nil {
		return filepath.ToSlash(r)
	}
	return p
}
