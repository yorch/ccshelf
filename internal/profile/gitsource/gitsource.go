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
	"github.com/ccshelf/ccshelf/internal/orgconfig"
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
	// ErrNotCached is returned by PrepareCached when no checkout of the
	// requested commit is in the cache.
	ErrNotCached = errors.New("no checkout of that commit is cached")
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
	cfg      *orgconfig.Config
	cfgFound bool
}

var (
	_ profile.Source          = (*Source)(nil)
	_ profile.RegistryLocator = (*Source)(nil)
)

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

// OrgConfig returns the org config (ccshelf.toml) read from the pinned
// checkout, and whether the repository has one. Without one the defaults are
// returned and found is false; the launcher uses that to warn that no
// protected controls are declared. It returns (nil, false) before Prepare.
func (s *Source) OrgConfig() (cfg *orgconfig.Config, found bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg, s.cfgFound
}

// RegistryPath returns the MCP registry file below Root (profile.RegistryLocator).
func (s *Source) RegistryPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg != nil {
		return s.cfg.Profiles.MCPRegistry
	}
	return profile.DefaultRegistryPath
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
func (s *Source) Prepare(ctx context.Context) error { return s.prepare(ctx, "") }

// PrepareCached is Prepare for a commit that is already known (the launcher
// takes it from the trust lockfile): it never contacts the remote. The
// checkout of that commit must already be in the cache and passes the same
// verification as in Prepare; when it is not there, ErrNotCached is returned
// and the caller can fall back to Prepare. A tag that moved on the remote is
// not noticed, but nothing but the pinned commit is ever read.
func (s *Source) PrepareCached(ctx context.Context, commit string) error {
	commit = strings.ToLower(commit)
	if !fullSHA.MatchString(commit) {
		return fmt.Errorf("%w: %q is not a full commit SHA", ErrNotPinned, commit)
	}
	return s.prepare(ctx, commit)
}

// prepare is Prepare and PrepareCached; a non-empty cached is the commit to
// use without any network access.
func (s *Source) prepare(ctx context.Context, cached string) error {
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
	var sha, checkout string
	if cached != "" {
		sha = cached
		checkout = CheckoutDir(base, s.opts.URL, sha)
		if _, err := os.Lstat(checkout); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("%w: %s", ErrNotCached, sha)
			}
			return fmt.Errorf("inspecting %s: %w", checkout, err)
		}
	} else {
		if sha, err = s.resolve(ctx, base); err != nil {
			return err
		}
		if checkout, err = s.materialize(ctx, base, sha); err != nil {
			return err
		}
	}
	// Mark the checkout as used before verifying it, so a concurrent
	// cache.Prune does not remove a checkout that is being used.
	now := time.Now()
	_ = os.Chtimes(checkout, now, now)
	root, cfg, found, err := s.verify(ctx, checkout, sha)
	if err != nil {
		if errors.Is(err, ErrTampered) {
			return fmt.Errorf("%w; delete %s to fetch the commit again", err, checkout)
		}
		return err
	}
	inner := profile.DirSourceAt(profile.KindOrg, root, profile.Layout{Profiles: cfg.Profiles.Dir, Registry: cfg.Profiles.MCPRegistry})
	s.mu.Lock()
	s.sha = sha
	s.root = root
	s.inner = inner
	s.cfg, s.cfgFound = cfg, found
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
	return CacheBase()
}

// CacheBase returns the folder that holds the checkouts when Options.CacheDir
// is empty (the "git" folder of the cache directory), creating it.
func CacheBase() (string, error) {
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

// CheckoutDir returns the folder that holds the checkout of commit sha of the
// repository at url below the cache base (see CacheBase). It does no I/O; the
// cache pruning uses it to know which checkouts the trust lockfile pins.
func CheckoutDir(base, url, sha string) string {
	return filepath.Join(base, urlKey(url), sha)
}

// layoutVersion is part of the folder name of every checkout (see urlKey). It
// changes when the set of files a checkout holds changes, so a checkout made
// by an older build is never reused (it is pruned by age like any other).
const layoutVersion = "layout2"

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
	sum := sha256.Sum256([]byte(layoutVersion + "\x00" + u))
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
	final := CheckoutDir(base, s.opts.URL, sha)
	urlDir := filepath.Dir(final)
	if fi, err := os.Lstat(final); err == nil {
		if !fi.IsDir() {
			return "", fmt.Errorf("%w: %s is not a directory", ErrTampered, final)
		}
		return final, nil // verified by the caller (verify) before any use
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
	pl, err := s.plan(entries, func(e treeEntry) ([]byte, error) { return s.readBlob(ctx, tmp, e) })
	if err != nil {
		return "", err
	}
	if err := s.extract(ctx, tmp, pl, nil); err != nil {
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

// verify re-verifies the checkout and returns the source root inside it and
// the org config (the defaults, with found false, when the commit has no
// ccshelf.toml).
func (s *Source) verify(ctx context.Context, checkout, sha string) (root string, cfg *orgconfig.Config, found bool, err error) {
	if err := s.verifyRepo(ctx, checkout, sha); err != nil {
		return "", nil, false, err
	}
	entries, err := s.listTree(ctx, checkout, sha)
	if err != nil {
		return "", nil, false, err
	}
	base := layoutBase(entries, s.subpath)
	root, err = checkBase(checkout, base)
	if err != nil {
		return "", nil, false, err
	}
	pl, err := s.plan(entries, func(e treeEntry) ([]byte, error) {
		return readVerified(filepath.Join(root, orgconfig.FileName), shortPath(checkout, filepath.Join(root, orgconfig.FileName)), e)
	})
	if err != nil {
		return "", nil, false, err
	}
	if err := verifyDisk(checkout, base, entries, pl.watch); err != nil {
		var mf *missingFilesError
		if !errors.As(err, &mf) || !restorable(mf.paths, base, pl.watch) {
			return "", nil, false, err
		}
		// A checkout from an older layout lacks only files the current layout
		// added. The object store has just been verified, so the files are
		// written again from it and the checkout is verified once more.
		if err := s.extract(ctx, checkout, pl, mf.paths); err != nil {
			return "", nil, false, err
		}
		if err := verifyDisk(checkout, base, entries, pl.watch); err != nil {
			return "", nil, false, err
		}
	}
	return root, pl.cfg, pl.found, nil
}

// restorable reports whether every missing path (a tree path) belongs to the
// files only the current layout watches.
func restorable(missing []string, base string, w watchSet) bool {
	for _, p := range missing {
		rel := p
		if base != "" {
			rel = strings.TrimPrefix(p, base+"/")
		}
		if !w.restorable(rel) {
			return false
		}
	}
	return true
}

// plan is what the committed tree says before anything is read from it: where
// the source root is, the org config and what is watched.
type plan struct {
	entries []treeEntry
	base    string
	cfg     *orgconfig.Config
	found   bool
	watch   watchSet
}

// plan checks the paths of the tree, finds the source root and the org config
// (read through read, which also verifies it) and checks the watched part of
// the tree.
func (s *Source) plan(entries []treeEntry, read func(treeEntry) ([]byte, error)) (*plan, error) {
	if err := checkPaths(entries); err != nil {
		return nil, err
	}
	pl := &plan{entries: entries, base: layoutBase(entries, s.subpath), cfg: orgconfig.Default()}
	if e, ok := orgConfigEntry(entries, pl.base); ok {
		if err := checkWatchedEntry(e); err != nil {
			return nil, orgConfigFatal(err)
		}
		data, err := read(e)
		if err != nil {
			return nil, orgConfigFatal(err)
		}
		if pl.cfg, err = parseOrgConfig(data); err != nil {
			return nil, err
		}
		pl.found = true
	}
	pl.watch = newWatch(pl.cfg)
	if err := checkTree(entries, pl.base, pl.watch); err != nil {
		return nil, err
	}
	return pl, nil
}

// orgConfigFatal marks a hygiene failure of the org config file itself as
// orgconfig.ErrInvalid, as a directory source does, so the launcher treats it
// as fatal (a config that cannot be read must not be skipped: it may hold the
// protected plugins).
func orgConfigFatal(err error) error {
	if errors.Is(err, ErrHygiene) && !errors.Is(err, orgconfig.ErrInvalid) {
		return fmt.Errorf("%w: %w", orgconfig.ErrInvalid, err)
	}
	return err
}

// readBlob reads the content of tree entry e from the object store of dir and
// checks that it hashes to its id.
func (s *Source) readBlob(ctx context.Context, dir string, e treeEntry) ([]byte, error) {
	out, err := s.run(ctx, dir, dir, nil, int(e.size)+1024, "cat-file", "blob", e.oid)
	if err != nil {
		return nil, err
	}
	content := []byte(out)
	id, err := gitObjectID(e.oid, "blob", content)
	if err != nil {
		return nil, err
	}
	if id != e.oid {
		return nil, fmt.Errorf("%w: %s does not hash to its tree entry", ErrTampered, e.path)
	}
	return content, nil
}

// verifyRepo checks that the object store holds the commit the checkout is
// named for: HEAD is that commit, and the commit object hashes to its id.
func (s *Source) verifyRepo(ctx context.Context, dir, sha string) error {
	head, err := s.git(ctx, dir, dir, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		// TEMPDEBUG
		ct, cerr := s.git(ctx, dir, dir, "cat-file", "-t", sha)
		hb, _ := os.ReadFile(filepath.Join(dir, ".git", "HEAD"))
		ents, _ := os.ReadDir(filepath.Join(dir, ".git"))
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		sh, _ := os.ReadFile(filepath.Join(dir, ".git", "shallow"))
		return fmt.Errorf("%w: %s: %w [DEBUG cat-file -t=%q err=%v HEAD=%q entries=%v shallow=%q]", ErrTampered, dir, err, ct, cerr, hb, names, sh)
	}
	if strings.TrimSpace(head) != sha {
		return fmt.Errorf("%w: %s is at %s, expected %s", ErrTampered, dir, strings.TrimSpace(head), sha)
	}
	raw, err := s.git(ctx, dir, dir, "cat-file", "commit", sha)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrTampered, err)
	}
	if err := checkCommitObject(dir, sha, raw); err != nil {
		return err
	}
	if err := s.dropReplaceRefs(ctx, dir); err != nil {
		return err
	}
	return s.verifyTrees(ctx, dir, sha, raw)
}

// checkCommitObject checks that the text of the commit object hashes to sha.
func checkCommitObject(dir, sha, raw string) error {
	id, err := gitObjectID(sha, "commit", []byte(raw))
	if err != nil {
		return err
	}
	if id != sha {
		return fmt.Errorf("%w: the commit object in %s does not hash to %s", ErrTampered, dir, sha)
	}
	return nil
}

// dropReplaceRefs deletes every refs/replace/* ref of the object store. They
// are ignored by every git call of this package, and a store has no reason to
// hold them.
func (s *Source) dropReplaceRefs(ctx context.Context, dir string) error {
	out, err := s.git(ctx, dir, dir, "for-each-ref", "--format=%(refname)", "refs/replace/")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrTampered, err)
	}
	for _, ref := range strings.Fields(out) {
		if _, err := s.git(ctx, dir, dir, "update-ref", "-d", ref); err != nil {
			return fmt.Errorf("%w: removing %s: %w", ErrTampered, ref, err)
		}
	}
	return nil
}

// maxTrees bounds how many tree objects verifyTrees reads.
const maxTrees = 200000

// checkTreeObject checks that the body of tree object want hashes to its id
// and returns the ids of its subtrees; sha is the commit id, which fixes the
// object format.
func checkTreeObject(dir, sha, want string, body []byte) ([]string, error) {
	id, err := gitObjectID(sha, "tree", body)
	if err != nil {
		return nil, err
	}
	if id != want {
		return nil, fmt.Errorf("%w: tree %s in %s does not hash to its id", ErrTampered, want, dir)
	}
	rawLen := len(sha) / 2
	var subs []string
	for len(body) > 0 {
		sp := bytes.IndexByte(body, ' ')
		if sp < 0 {
			return nil, fmt.Errorf("%w: tree %s is malformed", ErrTampered, want)
		}
		mode := string(body[:sp])
		z := bytes.IndexByte(body[sp:], 0)
		if z < 0 || len(body) < sp+z+1+rawLen {
			return nil, fmt.Errorf("%w: tree %s is malformed", ErrTampered, want)
		}
		oid := hex.EncodeToString(body[sp+z+1 : sp+z+1+rawLen])
		body = body[sp+z+1+rawLen:]
		if mode == "40000" {
			subs = append(subs, oid)
		}
	}
	return subs, nil
}

// verifyTrees re-hashes every tree object reachable from the root tree of the
// commit (raw is its text) and checks that each hashes to the id it is listed
// under, so the object store holds exactly what the pinned commit names.
func (s *Source) verifyTrees(ctx context.Context, dir, sha, raw string) error {
	root, _, _ := strings.Cut(raw, "\n")
	root = strings.TrimPrefix(root, "tree ")
	if root == raw || len(root) != len(sha) || !fullSHA.MatchString(root) {
		return fmt.Errorf("%w: the commit object in %s has no valid tree line", ErrTampered, dir)
	}
	seen := map[string]bool{root: true}
	level := []string{root}
	for len(level) > 0 {
		var next []string
		out, err := s.run(ctx, dir, dir, strings.NewReader(strings.Join(level, "\n")+"\n"), maxStdout, "cat-file", "--batch")
		if err != nil {
			return fmt.Errorf("%w: %w", ErrTampered, err)
		}
		rest := []byte(out)
		for _, want := range level {
			nl := bytes.IndexByte(rest, '\n')
			if nl < 0 {
				return fmt.Errorf("%w: git cat-file ended early at tree %s", ErrTampered, want)
			}
			hdr := strings.Fields(string(rest[:nl]))
			rest = rest[nl+1:]
			if len(hdr) != 3 || hdr[0] != want || hdr[1] != "tree" {
				return fmt.Errorf("%w: tree %s is missing or not a tree", ErrTampered, want)
			}
			size, err := strconv.Atoi(hdr[2])
			if err != nil || size < 0 || len(rest) < size+1 || rest[size] != '\n' {
				return fmt.Errorf("%w: git cat-file output for tree %s is malformed", ErrTampered, want)
			}
			body := rest[:size]
			rest = rest[size+1:]
			children, err := checkTreeObject(dir, sha, want, body)
			if err != nil {
				return err
			}
			for _, oid := range children {
				if !seen[oid] {
					if len(seen) >= maxTrees {
						return fmt.Errorf("%w: more than %d trees", ErrHygiene, maxTrees)
					}
					seen[oid] = true
					next = append(next, oid)
				}
			}
		}
		level = next
	}
	return nil
}

// listTree lists the files of commit sha. The tree is checked by plan.
func (s *Source) listTree(ctx context.Context, dir, sha string) ([]treeEntry, error) {
	out, err := s.git(ctx, dir, dir, "ls-tree", "-r", "-z", "--long", sha)
	if err != nil {
		return nil, err
	}
	return parseLsTree(out)
}

// extract writes the watched files from the object store into dir, or only
// the tree paths in only when it is not empty. The tree has been checked, so
// every path is a clean relative path.
func (s *Source) extract(ctx context.Context, dir string, pl *plan, only []string) error {
	files, _ := expectedFiles(pl.entries, pl.base, pl.watch)
	paths := make([]string, 0, len(files))
	if len(only) > 0 {
		for _, p := range only {
			if _, ok := files[p]; ok {
				paths = append(paths, p)
			}
		}
	} else {
		for p := range files {
			paths = append(paths, p)
		}
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
