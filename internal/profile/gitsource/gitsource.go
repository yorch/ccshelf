package gitsource

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ccshelf/ccshelf/internal/cache"
	"github.com/ccshelf/ccshelf/internal/config"
	"github.com/ccshelf/ccshelf/internal/profile"
)

// DefaultTimeout bounds one Prepare when Options.Timeout is zero.
const DefaultTimeout = 2 * time.Minute

// Errors returned by this package, besides ErrBadURL and ErrHygiene.
var (
	// ErrNotPrepared is returned by Names, Open and Root before Prepare.
	ErrNotPrepared = errors.New("git source is not prepared; call Prepare first")
	// ErrNotPinned is returned when the ref is not an acceptable pin.
	ErrNotPinned = errors.New("git source is not pinned")
	// ErrTampered is returned when a cached checkout does not match the
	// commit it is supposed to hold.
	ErrTampered = errors.New("cached git checkout does not match its commit")
)

// Options configures a Source.
type Options struct {
	// URL is the repository: https://, ssh:// or user@host:path. With
	// AllowLocal also file:// and local paths.
	URL string
	// Ref is a tag or a full commit SHA. Branch-like names are rejected.
	Ref string
	// Subpath is the folder inside the repository that holds profiles/, or
	// is the profiles folder itself. Empty means the repository root.
	Subpath string
	// CacheDir holds the checkouts. Default: the "git" folder of the cache
	// directory (cache.Dir()).
	CacheDir string
	// RequirePin demands that Ref is a tag (or a SHA). The launcher sets it
	// from trust.require_pin.
	RequirePin bool
	// AllowLocal permits file:// URLs and plain paths. Tests only.
	AllowLocal bool
	// GitPath is the git executable. Default "git".
	GitPath string
	// Timeout bounds Prepare. Default DefaultTimeout.
	Timeout time.Duration
}

// Source is a profile source backed by a pinned git checkout. It satisfies
// profile.Source (kind KindOrg) once Prepare has succeeded.
type Source struct {
	opts    Options
	subpath string

	prepMu   sync.Mutex // serializes Prepare; guards hooksDir
	mu       sync.Mutex // guards the fields below
	hooksDir string
	sha      string
	root     string
	inner    profile.Source
}

var _ profile.Source = (*Source)(nil)

// New validates opts and returns an unprepared Source. It does no I/O.
func New(opts Options) (*Source, error) {
	if err := validateURL(opts.URL, opts.AllowLocal); err != nil {
		return nil, err
	}
	if err := config.ValidatePin(opts.Ref); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotPinned, err)
	}
	sub, err := cleanSubpath(opts.Subpath)
	if err != nil {
		return nil, err
	}
	if opts.Timeout < 0 {
		return nil, errors.New("timeout must not be negative")
	}
	return &Source{opts: opts, subpath: sub}, nil
}

// URL returns the repository URL.
func (s *Source) URL() string { return s.opts.URL }

// Ref returns the requested tag or SHA.
func (s *Source) Ref() string { return s.opts.Ref }

// Locator returns "git:<url>", the identity of the repository without the
// commit. The trust package keys lockfile entries on it.
func (s *Source) Locator() string { return "git:" + s.opts.URL }

// ID returns "git:<url>@<sha>" after Prepare and "git:<url>@<ref>" before.
func (s *Source) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sha != "" {
		return "git:" + s.opts.URL + "@" + s.sha
	}
	return "git:" + s.opts.URL + "@" + s.opts.Ref
}

// Kind returns profile.KindOrg.
func (s *Source) Kind() profile.Kind { return profile.KindOrg }

// Commit returns the resolved full commit SHA, or "" before Prepare.
func (s *Source) Commit() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sha
}

// Root returns the verified checkout folder that holds profiles/, or "" before
// Prepare.
func (s *Source) Root() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root
}

// Names lists the profiles in the checkout.
func (s *Source) Names() ([]string, error) {
	in, err := s.prepared()
	if err != nil {
		return nil, err
	}
	return in.Names()
}

// Open reads and validates one profile from the checkout.
func (s *Source) Open(name string) (*profile.File, error) {
	in, err := s.prepared()
	if err != nil {
		return nil, err
	}
	f, err := in.Open(name)
	if err != nil {
		return nil, err
	}
	f.Source = s
	return f, nil
}

func (s *Source) prepared() (profile.Source, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inner == nil {
		return nil, ErrNotPrepared
	}
	return s.inner, nil
}

// Prepare resolves the ref to a commit SHA, creates or re-verifies the
// checkout and validates its content. It is safe to call more than once and
// from several goroutines or processes at the same time; each call
// re-resolves a tag so a moved tag is noticed.
func (s *Source) Prepare(ctx context.Context) error {
	s.prepMu.Lock()
	defer s.prepMu.Unlock()
	timeout := s.opts.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	base, err := s.cacheBase()
	if err != nil {
		return err
	}
	if err := s.ensureHooksDir(base); err != nil {
		return err
	}
	sha, err := s.resolve(ctx, base)
	if err != nil {
		return err
	}
	checkout, err := s.materialize(ctx, base, sha)
	if err != nil {
		return err
	}
	root, err := s.verify(ctx, checkout, sha)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.sha = sha
	s.root = root
	s.inner = profile.DirSource(profile.KindOrg, filepath.Join(root, "profiles"))
	s.mu.Unlock()
	return nil
}

func (s *Source) cacheBase() (string, error) {
	if s.opts.CacheDir != "" {
		abs, err := filepath.Abs(s.opts.CacheDir)
		if err != nil {
			return "", fmt.Errorf("resolving cache directory: %w", err)
		}
		if err := cache.Ensure(abs); err != nil {
			return "", err
		}
		return abs, nil
	}
	d, err := cache.Dir()
	if err != nil {
		return "", err
	}
	g := filepath.Join(d, "git")
	if err := cache.Ensure(g); err != nil {
		return "", err
	}
	return g, nil
}

// ensureHooksDir creates the empty directory core.hooksPath points at.
func (s *Source) ensureHooksDir(base string) error {
	dir := filepath.Join(base, ".nohooks")
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("creating hooks directory: %w", err)
	}
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() {
		return fmt.Errorf("hooks directory %s is not a plain directory", dir)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading hooks directory: %w", err)
	}
	if len(ents) != 0 {
		return fmt.Errorf("%w: %s must stay empty", ErrTampered, dir)
	}
	s.hooksDir = dir
	return nil
}

// resolve turns the ref into a full commit SHA.
func (s *Source) resolve(ctx context.Context, base string) (string, error) {
	ref := strings.ToLower(s.opts.Ref)
	if fullSHA.MatchString(ref) {
		return ref, nil
	}
	ref = s.opts.Ref
	out, err := s.git(ctx, base, "", "ls-remote", "--", s.opts.URL, "refs/tags/"+ref, "refs/tags/"+ref+"^{}")
	if err != nil {
		return "", fmt.Errorf("resolving tag %q: %w", ref, err)
	}
	if sha := pickRef(out, "refs/tags/"+ref); sha != "" {
		return sha, nil
	}
	if s.opts.RequirePin {
		return "", fmt.Errorf("%w: %q is not a tag of %s and not a commit SHA", ErrNotPinned, ref, s.opts.URL)
	}
	out, err = s.git(ctx, base, "", "ls-remote", "--", s.opts.URL, "refs/heads/"+ref)
	if err != nil {
		return "", fmt.Errorf("resolving ref %q: %w", ref, err)
	}
	if sha := pickRef(out, "refs/heads/"+ref); sha != "" {
		return sha, nil
	}
	return "", fmt.Errorf("%w: ref %q was not found in %s", ErrNotPinned, ref, s.opts.URL)
}

// pickRef picks the commit for name from ls-remote output, preferring the
// peeled commit of an annotated tag.
func pickRef(out, name string) string {
	var direct, peeled string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || !fullSHA.MatchString(f[0]) {
			continue
		}
		switch f[1] {
		case name:
			direct = f[0]
		case name + "^{}":
			peeled = f[0]
		}
	}
	if peeled != "" {
		return peeled
	}
	return direct
}

func urlKey(u string) string {
	sum := sha256.Sum256([]byte(u))
	return hex.EncodeToString(sum[:8])
}

// fetchFilter makes the server leave out blobs bigger than the per-file limit
// (a server that does not support filters sends them, and they are then
// refused by the tree check before anything is written).
const fetchFilter = "--filter=blob:limit=1048577"

// materialize returns the checkout directory for sha, creating it if needed.
// A new checkout is built without git ever writing a working tree: the commit
// is fetched into an object store, the tree is listed and checked, and the
// files of the watched folders are read from the object store and written
// here, so no filter driver (git-lfs, git-crypt, a custom clean/smudge),
// attribute, hook or checkout-time rewrite can change a byte.
func (s *Source) materialize(ctx context.Context, base, sha string) (string, error) {
	urlDir := filepath.Join(base, urlKey(s.opts.URL))
	final := filepath.Join(urlDir, sha)
	if fi, err := os.Lstat(final); err == nil {
		if !fi.IsDir() {
			return "", fmt.Errorf("%w: %s is not a directory", ErrTampered, final)
		}
		return final, nil
	}
	if err := os.Mkdir(urlDir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", fmt.Errorf("creating %s: %w", urlDir, err)
	}
	if fi, err := os.Lstat(urlDir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("cache path %s is not a plain directory", urlDir)
	}
	tmp, err := os.MkdirTemp(urlDir, ".tmp-")
	if err != nil {
		return "", fmt.Errorf("creating a temporary checkout: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	initArgs := []string{"init", "--quiet"}
	if len(sha) == 64 {
		initArgs = append(initArgs, "--object-format=sha256")
	}
	if _, err := s.git(ctx, tmp, "", append(initArgs, "--", tmp)...); err != nil {
		return "", err
	}
	if _, err := s.git(ctx, tmp, tmp, "remote", "add", "--", "origin", s.opts.URL); err != nil {
		return "", err
	}
	fetch := []string{"fetch", "--quiet", "--depth", "1", "--no-tags", "--no-recurse-submodules", fetchFilter, "origin"}
	if _, err := s.git(ctx, tmp, tmp, append(fetch, sha)...); err != nil {
		if fullSHA.MatchString(strings.ToLower(s.opts.Ref)) {
			return "", fmt.Errorf("fetching commit %s: %w", sha, err)
		}
		tag := s.opts.Ref
		if _, err2 := s.git(ctx, tmp, tmp, append(fetch, "+refs/tags/"+tag+":refs/tags/"+tag)...); err2 != nil {
			return "", fmt.Errorf("fetching commit %s: %w", sha, err)
		}
	}
	if _, err := s.git(ctx, tmp, tmp, "update-ref", "--no-deref", "HEAD", sha); err != nil {
		return "", fmt.Errorf("pinning %s: %w", sha, err)
	}
	if err := s.verifyRepo(ctx, tmp, sha); err != nil {
		return "", err
	}
	entries, err := s.listTree(ctx, tmp, sha)
	if err != nil {
		return "", err
	}
	if err := s.extract(ctx, tmp, entries, layoutBase(entries, s.subpath)); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, final); err != nil {
		if fi, serr := os.Lstat(final); serr == nil && fi.IsDir() {
			return final, nil // another caller won the race; it is verified next
		}
		return "", fmt.Errorf("installing checkout: %w", err)
	}
	return final, nil
}

// verify re-verifies the checkout and returns the source root inside it.
func (s *Source) verify(ctx context.Context, checkout, sha string) (string, error) {
	if err := s.verifyRepo(ctx, checkout, sha); err != nil {
		return "", err
	}
	entries, err := s.listTree(ctx, checkout, sha)
	if err != nil {
		return "", err
	}
	base := layoutBase(entries, s.subpath)
	if err := verifyDisk(checkout, base, entries); err != nil {
		return "", err
	}
	return filepath.Join(checkout, filepath.FromSlash(base)), nil
}

// verifyRepo checks that the object store holds the commit the checkout is
// named for: HEAD is that commit, and the commit object hashes to its id.
func (s *Source) verifyRepo(ctx context.Context, dir, sha string) error {
	head, err := s.git(ctx, dir, dir, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrTampered, dir, err)
	}
	if strings.TrimSpace(head) != sha {
		return fmt.Errorf("%w: %s is at %s, expected %s", ErrTampered, dir, strings.TrimSpace(head), sha)
	}
	raw, err := s.git(ctx, dir, dir, "cat-file", "commit", sha)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrTampered, err)
	}
	id, err := gitObjectID(sha, "commit", []byte(raw))
	if err != nil {
		return err
	}
	if id != sha {
		return fmt.Errorf("%w: the commit object in %s does not hash to %s", ErrTampered, dir, sha)
	}
	return nil
}

// listTree lists the files of commit sha and validates the tree.
func (s *Source) listTree(ctx context.Context, dir, sha string) ([]treeEntry, error) {
	out, err := s.git(ctx, dir, dir, "ls-tree", "-r", "-z", "--long", sha)
	if err != nil {
		return nil, err
	}
	entries, err := parseLsTree(out)
	if err != nil {
		return nil, err
	}
	if err := checkTree(entries, layoutBase(entries, s.subpath)); err != nil {
		return nil, err
	}
	return entries, nil
}

// extract writes the files of the watched folders from the object store into
// dir. The tree has been checked, so every path is a clean relative path.
func (s *Source) extract(ctx context.Context, dir string, entries []treeEntry, base string) error {
	files, _ := expectedFiles(entries, base)
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return nil
	}
	var in strings.Builder
	want := 0
	for _, p := range paths {
		in.WriteString(files[p].oid + "\n")
		want += int(files[p].size) + 2*len(files[p].oid) + 64
	}
	out, err := s.run(ctx, dir, dir, strings.NewReader(in.String()), want+1024, "cat-file", "--batch")
	if err != nil {
		return err
	}
	rest := []byte(out)
	for _, p := range paths {
		e := files[p]
		nl := bytes.IndexByte(rest, '\n')
		if nl < 0 {
			return fmt.Errorf("%w: git cat-file ended early at %s", ErrTampered, p)
		}
		hdr := strings.Fields(string(rest[:nl]))
		rest = rest[nl+1:]
		if len(hdr) != 3 || hdr[0] != e.oid || hdr[1] != "blob" || hdr[2] != strconv.FormatInt(e.size, 10) {
			return fmt.Errorf("%w: git returned an unexpected object for %s", ErrTampered, p)
		}
		if int64(len(rest)) < e.size+1 || rest[e.size] != '\n' {
			return fmt.Errorf("%w: git cat-file output for %s is malformed", ErrTampered, p)
		}
		content := rest[:e.size]
		rest = rest[e.size+1:]
		id, err := gitObjectID(e.oid, "blob", content)
		if err != nil {
			return err
		}
		if id != e.oid {
			return fmt.Errorf("%w: %s does not hash to its tree entry", ErrTampered, p)
		}
		if err := writeNew(dir, p, content); err != nil {
			return err
		}
	}
	return nil
}

// writeNew creates rel (a slash separated path already checked by checkTree)
// below dir with the content, mode 0600, directories 0700, failing when the
// file already exists (which also refuses a symlink in its place).
func writeNew(dir, rel string, content []byte) error {
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("creating the folder of %s: %w", rel, err)
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("writing %s: %w", rel, err)
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		return fmt.Errorf("writing %s: %w", rel, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", rel, err)
	}
	return nil
}
