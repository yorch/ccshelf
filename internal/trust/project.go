package trust

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yorch/ccshelf/internal/config"
)

// ProjectFolder is the folder inside a repository that holds project profiles.
const ProjectFolder = ".ccshelf"

// ProjectVersion is the project trust file format version.
const ProjectVersion = 1

const (
	maxProjects       = 2000
	maxProjectFiles   = 2000
	maxProjectFile    = 4 << 20
	maxProjectBytes   = 32 << 20
	projectHashDomain = "ccshelf-project-v1"
)

// Trust returns ErrNoProjectFolder when root has no .ccshelf folder.
var ErrNoProjectFolder = errors.New("the repository has no .ccshelf folder")

// ProjectRecord is one trusted repository.
type ProjectRecord struct {
	// Path is the real (symlink-resolved) repository root.
	Path string
	// ContentHash covers every file under <Path>/.ccshelf.
	ContentHash string
	// TrustedAt is when the user trusted the folder (UTC).
	TrustedAt time.Time
}

type projectJSON struct {
	Path        string    `json:"path"`
	ContentHash string    `json:"contentHash"`
	TrustedAt   time.Time `json:"trustedAt"`
}

type projectFile struct {
	Version  int           `json:"version"`
	Projects []projectJSON `json:"projects"`
}

// ProjectStore records which repositories' .ccshelf folders the user trusts,
// like direnv's "allow": by path and content hash, so editing the folder
// revokes trust automatically (SR2). Here, trust only lets ccshelf load
// project profiles. A project profile still needs a lockfile entry for its
// closure.
// Methods are safe for concurrent use by goroutines.
type ProjectStore struct {
	path string
	now  func() time.Time

	mu      sync.Mutex
	records []ProjectRecord
}

// OpenProjects reads the project trust file at path (see
// config.ProjectTrustPath). A missing file is an empty store. OpenProjects
// never follows the file through a symlink. The file has a closed schema.
func OpenProjects(path string) (*ProjectStore, error) {
	s := &ProjectStore{path: path, now: func() time.Time { return time.Now().UTC() }}
	if err := s.reload(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *ProjectStore) reload() error {
	b, err := readStateFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		s.records = nil
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading project trust file: %w", err)
	}
	var pf projectFile
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&pf); err != nil {
		return fmt.Errorf("parsing project trust file %s: %w", s.path, err)
	}
	if pf.Version != ProjectVersion {
		return fmt.Errorf("project trust file %s has version %d. This ccshelf understands version %d", s.path, pf.Version, ProjectVersion)
	}
	if len(pf.Projects) > maxProjects {
		return fmt.Errorf("project trust file %s has too many entries", s.path)
	}
	s.records = s.records[:0]
	seen := map[string]bool{}
	for _, p := range pf.Projects {
		if p.Path == "" || !filepath.IsAbs(p.Path) || !hexHash.MatchString(p.ContentHash) {
			return fmt.Errorf("project trust file %s has an invalid entry", s.path)
		}
		if seen[p.Path] {
			return fmt.Errorf("project trust file %s has a duplicate entry for %s", s.path, p.Path)
		}
		seen[p.Path] = true
		s.records = append(s.records, ProjectRecord(p))
	}
	return nil
}

func (s *ProjectStore) save() error {
	sort.Slice(s.records, func(i, j int) bool { return s.records[i].Path < s.records[j].Path })
	pf := projectFile{Version: ProjectVersion, Projects: []projectJSON{}}
	for _, r := range s.records {
		pf.Projects = append(pf.Projects, projectJSON(r))
	}
	b, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding project trust file: %w", err)
	}
	if err := writeStateFile(s.path, append(b, '\n')); err != nil {
		return fmt.Errorf("writing project trust file: %w", err)
	}
	return nil
}

// realRoot returns the absolute, symlink-resolved form of root.
func realRoot(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", root, err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", abs, err)
	}
	return filepath.Clean(real), nil
}

// HashProjectFolder returns the content hash of <root>/.ccshelf: a SHA-256
// over every entry below it in sorted order, names and bytes, length-prefixed.
// Symlinks and other special files are errors, and the folder is bounded in
// size. HashProjectFolder uses root as given (callers pass the real path).
func HashProjectFolder(root string) (string, error) {
	dir := filepath.Join(root, ProjectFolder)
	fi, err := os.Lstat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", ErrNoProjectFolder
		}
		return "", fmt.Errorf("inspecting %s: %w", dir, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return "", fmt.Errorf("%s must be a plain directory, not a symlink", dir)
	}
	ents, err := readProjectTree(dir)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	put := func(b []byte) {
		var n [binary.MaxVarintLen64]byte
		h.Write(n[:binary.PutUvarint(n[:], uint64(len(b)))])
		h.Write(b)
	}
	put([]byte(projectHashDomain))
	for _, e := range ents {
		if e.isDir {
			put([]byte("d"))
			put([]byte(e.rel))
			continue
		}
		put([]byte("f"))
		put([]byte(e.rel))
		put(e.data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// projectEntry is one directory or file below <root>/.ccshelf.
type projectEntry struct {
	rel   string // slash separated, relative to the folder
	isDir bool
	data  []byte
}

// projectLimits bounds the size of a project folder while it is read.
type projectLimits struct {
	dir     string
	entries int
	total   int64
}

// addEntry counts one file or directory.
func (l *projectLimits) addEntry() error {
	l.entries++
	if l.entries > maxProjectFiles {
		return fmt.Errorf("%s has too many files", l.dir)
	}
	return nil
}

// addBytes counts n bytes of file content.
func (l *projectLimits) addBytes(n int) error {
	l.total += int64(n)
	if l.total > maxProjectBytes {
		return fmt.Errorf("%s is larger than %d bytes", l.dir, maxProjectBytes)
	}
	return nil
}

func sortProjectEntries(ents []projectEntry) {
	sort.Slice(ents, func(i, j int) bool { return ents[i].rel < ents[j].rel })
}

func errProjectSymlink(rel string) error {
	return fmt.Errorf("%s/%s is a symlink. Symlinks are not allowed in a project folder", ProjectFolder, rel)
}

func errProjectSpecial(rel string) error {
	return fmt.Errorf("%s/%s is not a regular file", ProjectFolder, rel)
}

// readProjectFile reads one regular file, bounded by maxProjectFile.
func readProjectFile(f io.Reader, rel string) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(f, maxProjectFile+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s/%s: %w", ProjectFolder, rel, err)
	}
	if len(b) > maxProjectFile {
		return nil, fmt.Errorf("%s/%s is larger than %d bytes", ProjectFolder, rel, maxProjectFile)
	}
	return b, nil
}

// Trust records the current content of <root>/.ccshelf as trusted. Call it
// only after the user has reviewed the folder.
func (s *ProjectStore) Trust(root string) error {
	real, err := realRoot(root)
	if err != nil {
		return err
	}
	hash, err := HashProjectFolder(real)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := lockState(s.path)
	if err != nil {
		return err
	}
	defer unlock()
	if err := s.reload(); err != nil {
		return err
	}
	rec := ProjectRecord{Path: real, ContentHash: hash, TrustedAt: s.now()}
	replaced := false
	for i := range s.records {
		if s.records[i].Path == real {
			s.records[i] = rec
			replaced = true
		}
	}
	if !replaced {
		if len(s.records) >= maxProjects {
			return errors.New("the project trust file is full")
		}
		s.records = append(s.records, rec)
	}
	return s.save()
}

// IsTrusted reports whether root's .ccshelf folder is trusted and unchanged
// since. A missing folder or record is (false, nil). A folder with symlinks or
// other problems is (false, err). The result is a point-in-time check: the
// closure hash in the lockfile covers the bytes that are finally used.
func (s *ProjectStore) IsTrusted(root string) (bool, error) {
	real, err := realRoot(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	s.mu.Lock()
	var rec *ProjectRecord
	for i := range s.records {
		if s.records[i].Path == real {
			r := s.records[i]
			rec = &r
		}
	}
	s.mu.Unlock()
	if rec == nil {
		return false, nil
	}
	hash, err := HashProjectFolder(real)
	if errors.Is(err, ErrNoProjectFolder) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return hash == rec.ContentHash, nil
}

// Revoke removes trust for root. Revoke matches a root that no longer exists
// by its cleaned absolute path. It returns ErrNotFound when there was no record.
func (s *ProjectStore) Revoke(root string) error {
	keys := map[string]bool{}
	if real, err := realRoot(root); err == nil {
		keys[real] = true
	}
	if abs, err := filepath.Abs(root); err == nil {
		keys[filepath.Clean(abs)] = true
		keys[resolveLoose(abs)] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := lockState(s.path)
	if err != nil {
		return err
	}
	defer unlock()
	if err := s.reload(); err != nil {
		return err
	}
	kept := s.records[:0:0]
	for _, r := range s.records {
		if !keys[r.Path] {
			kept = append(kept, r)
		}
	}
	if len(kept) == len(s.records) {
		return fmt.Errorf("%w for %s", ErrNotFound, root)
	}
	s.records = kept
	return s.save()
}

// List returns the trusted repositories sorted by path.
func (s *ProjectStore) List() []ProjectRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]ProjectRecord(nil), s.records...)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// ProjectAllowed reports whether project profiles under root may be loaded:
// the configuration must set trust.trust_project_profiles and the folder must
// be trusted and unchanged. Callers pass the result as
// profile.ResolveOptions.AllowProject and as the projectTrusted argument of
// Store.CheckWithProject.
func ProjectAllowed(cfg *config.Config, ps *ProjectStore, root string) (bool, error) {
	if cfg == nil || !cfg.Trust.TrustProjectProfiles || ps == nil || strings.TrimSpace(root) == "" {
		return false, nil
	}
	return ps.IsTrusted(root)
}

// resolveLoose resolves symlinks in the longest existing prefix of abs, so a
// repository that has been deleted can still be matched to its record.
func resolveLoose(abs string) string {
	rest := ""
	cur := filepath.Clean(abs)
	for {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return filepath.Clean(abs)
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}
