package gitsource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
		return nil, fmt.Errorf("%w: %v", ErrNotPinned, err)
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

// materialize returns the verified checkout directory for sha, creating it if
// needed.
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
	if _, err := s.git(ctx, tmp, "", "init", "--quiet", "--", tmp); err != nil {
		return "", err
	}
	if _, err := s.git(ctx, tmp, tmp, "remote", "add", "--", "origin", s.opts.URL); err != nil {
		return "", err
	}
	if _, err := s.git(ctx, tmp, tmp, "fetch", "--quiet", "--depth", "1", "--no-tags", "--no-recurse-submodules", "origin", sha); err != nil {
		if fullSHA.MatchString(strings.ToLower(s.opts.Ref)) {
			return "", fmt.Errorf("fetching commit %s: %w", sha, err)
		}
		tag := s.opts.Ref
		if _, err2 := s.git(ctx, tmp, tmp, "fetch", "--quiet", "--depth", "1", "--no-tags", "--no-recurse-submodules", "origin", "+refs/tags/"+tag+":refs/tags/"+tag); err2 != nil {
			return "", fmt.Errorf("fetching commit %s: %w", sha, err)
		}
	}
	if _, err := s.git(ctx, tmp, tmp, "checkout", "--quiet", "--detach", sha, "--"); err != nil {
		return "", fmt.Errorf("checking out %s: %w", sha, err)
	}
	if _, err := s.verifyCheckout(ctx, tmp, sha); err != nil {
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
	entries, err := s.verifyCheckout(ctx, checkout, sha)
	if err != nil {
		return "", err
	}
	base := layoutBase(entries, s.subpath)
	root := filepath.Join(checkout, filepath.FromSlash(base))
	if err := checkDisk(checkout, root); err != nil {
		return "", err
	}
	return root, nil
}

// verifyCheckout checks HEAD, rebuilds the index so content is re-hashed,
// requires a clean tree and validates the committed content.
func (s *Source) verifyCheckout(ctx context.Context, dir, sha string) ([]treeEntry, error) {
	head, err := s.git(ctx, dir, dir, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrTampered, dir, err)
	}
	if strings.TrimSpace(head) != sha {
		return nil, fmt.Errorf("%w: %s is at %s, expected %s", ErrTampered, dir, strings.TrimSpace(head), sha)
	}
	// A private index outside the checkout: it has no stat data, so status
	// re-hashes every file, and concurrent verifications never contend for
	// the checkout's own index.lock.
	idxDir, err := os.MkdirTemp(filepath.Dir(dir), ".idx-")
	if err != nil {
		return nil, fmt.Errorf("creating a temporary index: %w", err)
	}
	defer func() { _ = os.RemoveAll(idxDir) }()
	idx := []string{"GIT_INDEX_FILE=" + filepath.Join(idxDir, "index")}
	if _, err := s.gitEnvRun(ctx, dir, dir, idx, "read-tree", "HEAD"); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTampered, err)
	}
	st, err := s.gitEnvRun(ctx, dir, dir, idx, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignored")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTampered, err)
	}
	if st != "" {
		first := strings.SplitN(st, "\x00", 2)[0]
		if len(first) > 3 {
			first = first[3:]
		}
		return nil, fmt.Errorf("%w: %s has local changes (%s); delete the directory to fetch it again", ErrTampered, dir, first)
	}
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
