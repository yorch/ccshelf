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
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/yorch/ccshelf/internal/catalog/sidecar"
	"github.com/yorch/ccshelf/internal/orgconfig"
	"github.com/yorch/ccshelf/internal/ui"
)

// MaxFileSize is the largest file allowed in a watched folder, and the largest
// ccshelf.toml.
const MaxFileSize = 1 << 20

// ErrHygiene is wrapped by content hygiene failures.
var ErrHygiene = errors.New("unacceptable repository content")

const (
	// MaxWatchedFiles is the most files read from the watched folders.
	MaxWatchedFiles = 5000
	// MaxWatchedBytes is the most content read from the watched folders.
	MaxWatchedBytes = 32 << 20
)

// promptsDir is the folder of append_system_prompt_file targets.
const promptsDir = "prompts"

// watchSet lists what is ever read from a source root: folders (read
// recursively) and single files, all slash separated and relative to the root.
// It always holds ccshelf.toml, the profiles folder, the prompts folder, the MCP
// registry, the catalog sidecar folder (catalog/plugins) and the marketplace
// files of catalog.marketplaces; the org config can move the profiles folder
// and the registry (profiles.dir, profiles.mcp_registry) and name the
// marketplace files, so the set is built from it. The catalog data is read for
// the deprecated-plugin warning of run and for search and recommend without an
// org data repo checkout; it is subject to the same hygiene and limits as the
// rest, so a marketplace file over MaxFileSize makes the source unusable.
type watchSet struct {
	dirs  []string
	files []string
	// catalog lists the watched paths that an older layout did not read (the
	// catalog sidecar folder and the marketplace files); a checkout made by an
	// older build lacks them (see restorable).
	catalog []string
}

// newWatch builds the watch set for an org config (nil means the defaults).
// The folder of the MCP registry is watched as a whole, as the "mcp" folder
// always was; a registry at the root of the source is a single file.
func newWatch(cfg *orgconfig.Config) watchSet {
	if cfg == nil {
		cfg = orgconfig.Default()
	}
	w := watchSet{dirs: []string{path.Clean(cfg.Profiles.Dir), promptsDir}, files: []string{orgconfig.FileName}}
	// A profiles-only repo ([catalog] enabled = false) has no catalog data: the
	// sidecars and marketplace files are not watched, so a stray one is neither
	// verified nor written to the cache.
	if cfg.Catalog.Enabled {
		w.dirs = append(w.dirs, sidecar.Dir)
		w.catalog = []string{sidecar.Dir}
		for _, m := range cfg.Catalog.Marketplaces {
			w.files = append(w.files, path.Clean(m))
			w.catalog = append(w.catalog, path.Clean(m))
		}
	}
	reg := path.Clean(cfg.Profiles.MCPRegistry)
	if d := path.Dir(reg); d != "." {
		w.dirs = append(w.dirs, d)
	} else {
		w.files = append(w.files, reg)
	}
	return w
}

// inDir reports whether rel is d or lies below it.
func inDir(rel, d string) bool { return rel == d || strings.HasPrefix(rel, d+"/") }

// has reports whether rel is a watched file or inside a watched folder.
func (w watchSet) has(rel string) bool {
	for _, d := range w.dirs {
		if inDir(rel, d) {
			return true
		}
	}
	for _, f := range w.files {
		if rel == f {
			return true
		}
	}
	return false
}

// restorable reports whether rel (relative to the source root) is part of what
// only the current layout watches, so that its absence is a checkout from an
// older build and not damage.
func (w watchSet) restorable(rel string) bool {
	for _, c := range w.catalog {
		if inDir(rel, c) {
			return true
		}
	}
	return false
}

// missingFilesError is the ErrTampered failure of verifyDisk when the only
// difference is files of the commit that are not on disk.
type missingFilesError struct{ paths []string }

func (e *missingFilesError) Error() string {
	return fmt.Sprintf("%v: %s is missing", ErrTampered, e.paths[0])
}

func (e *missingFilesError) Unwrap() error { return ErrTampered }

// loneFiles returns the watched files that are not inside a watched folder.
func (w watchSet) loneFiles() []string {
	var out []string
	for _, f := range w.files {
		inside := false
		for _, d := range w.dirs {
			if inDir(f, d) {
				inside = true
			}
		}
		if !inside {
			out = append(out, f)
		}
	}
	return out
}

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

// isWatched reports whether p is a watched file or inside (or is) a watched
// folder below base.
func isWatched(p, base string, w watchSet) bool {
	rel := p
	if base != "" {
		if !strings.HasPrefix(p, base+"/") {
			return false
		}
		rel = p[len(base)+1:]
	}
	return w.has(rel)
}

// layoutBase decides which folder of the tree is the source root: subpath
// itself when it holds ccshelf.toml or profiles/ (or nothing identifies it), or
// its parent when subpath names the profiles folder.
func layoutBase(entries []treeEntry, subpath string) string {
	prefix := ""
	if subpath != "" {
		prefix = subpath + "/"
	}
	for _, e := range entries {
		if e.path == prefix+orgconfig.FileName || strings.HasPrefix(e.path, prefix+"profiles/") {
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

// orgConfigEntry returns the tree entry of ccshelf.toml at the source root.
func orgConfigEntry(entries []treeEntry, base string) (treeEntry, bool) {
	want := orgconfig.FileName
	if base != "" {
		want = base + "/" + want
	}
	for _, e := range entries {
		if e.path == want {
			return e, true
		}
	}
	return treeEntry{}, false
}

// checkPaths rejects every path of the tree that has an unsafe component.
func checkPaths(entries []treeEntry) error {
	for _, e := range entries {
		for _, c := range strings.Split(e.path, "/") {
			if badComponent(c) {
				return fmt.Errorf("%w: path %q has an unsafe component", ErrHygiene, ui.SanitizeLine(e.path))
			}
		}
	}
	return nil
}

// parseOrgConfig parses the bytes of ccshelf.toml. A file that does not parse
// is refused: guessing what it meant could mask a protected control.
func parseOrgConfig(data []byte) (*orgconfig.Config, error) {
	cfg, err := orgconfig.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %w: %s: %w", ErrHygiene, orgconfig.ErrInvalid, orgconfig.FileName, err)
	}
	return cfg, nil
}

// checkTree validates the committed tree before anything is written: every
// path, and for the watched files and folders the entry type, the size and the
// totals.
func checkTree(entries []treeEntry, base string, w watchSet) error {
	if err := checkPaths(entries); err != nil {
		return err
	}
	files, total := 0, int64(0)
	for _, e := range entries {
		if !isWatched(e.path, base, w) {
			continue
		}
		if err := checkWatchedEntry(e); err != nil {
			return err
		}
		files++
		total += e.size
		if files > MaxWatchedFiles || total > MaxWatchedBytes {
			return fmt.Errorf("%w: the watched folders hold more than %d files or %d bytes", ErrHygiene, MaxWatchedFiles, MaxWatchedBytes)
		}
	}
	return nil
}

// checkWatchedEntry checks the type, mode and size of one entry that will be
// read: a regular file, not a symlink or a submodule, no bigger than
// MaxFileSize.
func checkWatchedEntry(e treeEntry) error {
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

// expectedFiles maps the path of every watched file to its tree entry, and
// lists their directories.
func expectedFiles(entries []treeEntry, base string, w watchSet) (files map[string]treeEntry, dirs map[string]bool) {
	files, dirs = map[string]treeEntry{}, map[string]bool{}
	for _, e := range entries {
		if !isWatched(e.path, base, w) {
			continue
		}
		files[e.path] = e
		for d := path.Dir(e.path); d != "." && d != "/"; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	return files, dirs
}

// verifyDisk checks that the watched folders and files under checkout hold
// exactly the committed files, byte for byte (the content is hashed the way
// git hashes a blob and compared with the id in the tree). Structural problems
// (symlinks, special files, a watched folder that is not a plain directory)
// are ErrHygiene; missing, extra or changed files are ErrTampered. Nothing
// outside the watched folders and files is ever read, so nothing there can
// matter.
func verifyDisk(checkout, base string, entries []treeEntry, w watchSet) error {
	root, err := checkBase(checkout, base)
	if err != nil {
		return err
	}
	files, dirs := expectedFiles(entries, base, w)
	seen := map[string]bool{}
	for _, wd := range w.dirs {
		if err := plainParents(root, wd); err != nil {
			return err
		}
		dir := filepath.Join(root, filepath.FromSlash(wd))
		fi, err := os.Lstat(dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("inspecting %s: %w", dir, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
			return fmt.Errorf("%w: %s is not a plain directory", ErrHygiene, wd)
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
	for _, f := range w.loneFiles() {
		if err := verifyLoneFile(root, base, f, files, seen); err != nil {
			return err
		}
	}
	var missing []string
	for p := range files {
		if !seen[p] {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return &missingFilesError{paths: missing}
	}
	return nil
}

// checkBase returns the source root inside checkout and checks that every
// folder on the way (the subpath) is a plain directory.
func checkBase(checkout, base string) (string, error) {
	if base == "" {
		return checkout, nil
	}
	cur := checkout
	for _, part := range strings.Split(base, "/") {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				break
			}
			return "", fmt.Errorf("inspecting %s: %w", cur, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
			return "", fmt.Errorf("%w: %s is not a plain directory", ErrHygiene, part)
		}
	}
	return filepath.Join(checkout, filepath.FromSlash(base)), nil
}

// plainParents checks that no folder above the last component of rel (below
// root) is a symlink or a file, so a watched path cannot be redirected.
func plainParents(root, rel string) error {
	cur := root
	parts := strings.Split(rel, "/")
	for _, part := range parts[:len(parts)-1] {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("inspecting %s: %w", part, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
			return fmt.Errorf("%w: %s is not a plain directory", ErrHygiene, part)
		}
	}
	return nil
}

// verifyLoneFile verifies one watched file that is not inside a watched
// folder (ccshelf.toml, a registry at the root). A file that does not exist
// is reported by the caller when the commit has it.
func verifyLoneFile(root, base, rel string, files map[string]treeEntry, seen map[string]bool) error {
	if err := plainParents(root, rel); err != nil {
		return err
	}
	full := rel
	if base != "" {
		full = base + "/" + rel
	}
	p := filepath.Join(root, filepath.FromSlash(rel))
	fi, err := os.Lstat(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspecting %s: %w", full, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s is a symlink", ErrHygiene, full)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%w: %s is not a regular file", ErrHygiene, full)
	}
	want, ok := files[full]
	if !ok {
		return fmt.Errorf("%w: %s is not in the commit", ErrTampered, full)
	}
	seen[full] = true
	return verifyFile(p, full, want)
}

// verifyFile compares one file on disk with its tree entry: size first, then
// content.
func verifyFile(p, rel string, want treeEntry) error {
	_, err := readVerified(p, rel, want)
	return err
}

// readVerified is verifyFile that also returns the content it verified.
func readVerified(p, rel string, want treeEntry) ([]byte, error) {
	info, err := os.Lstat(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s is missing", ErrTampered, rel)
		}
		return nil, fmt.Errorf("inspecting %s: %w", rel, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s is not a regular file", ErrHygiene, rel)
	}
	if info.Size() > MaxFileSize {
		return nil, fmt.Errorf("%w: %s is larger than %d bytes", ErrHygiene, rel, MaxFileSize)
	}
	if info.Size() != want.size {
		return nil, fmt.Errorf("%w: %s is %d bytes on disk, the commit has %d", ErrTampered, rel, info.Size(), want.size)
	}
	f, err := os.Open(p) //nolint:gosec // a path under the verified checkout
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", rel, err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", rel, err)
	}
	id, err := gitObjectID(want.oid, "blob", b)
	if err != nil {
		return nil, err
	}
	if id != want.oid {
		return nil, fmt.Errorf("%w: %s differs from the commit", ErrTampered, rel)
	}
	return b, nil
}

func shortPath(root, p string) string {
	if r, err := filepath.Rel(root, p); err == nil {
		return filepath.ToSlash(r)
	}
	return p
}
