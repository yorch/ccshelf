package gitsource

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ccshelf/ccshelf/internal/profile"
	"github.com/ccshelf/ccshelf/internal/testutil"
)

// fixture is a local origin repository plus an isolated environment.
type fixture struct {
	t      *testing.T
	env    map[string]string
	origin string
	cache  string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	env := testutil.IsolatedEnv(t)
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.com")
	f := &fixture{t: t, env: env, origin: filepath.Join(t.TempDir(), "origin"), cache: filepath.Join(t.TempDir(), "gitcache")}
	if err := os.MkdirAll(f.origin, 0o700); err != nil {
		t.Fatal(err)
	}
	f.git("init", "--quiet")
	f.git("config", "commit.gpgsign", "false")
	f.git("config", "tag.gpgsign", "false")
	return f
}

func (f *fixture) git(args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = f.origin
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *fixture) write(rel, content string) {
	f.t.Helper()
	p := filepath.Join(f.origin, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) commit(msg string) string {
	f.t.Helper()
	f.git("add", "-A")
	f.git("commit", "--quiet", "-m", msg)
	return f.git("rev-parse", "HEAD")
}

func (f *fixture) url() string {
	p := filepath.ToSlash(f.origin)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "file://" + p
}

func (f *fixture) source(ref, sub string) *Source {
	f.t.Helper()
	s, err := New(Options{URL: f.url(), Ref: ref, Subpath: sub, CacheDir: f.cache, RequirePin: true, AllowLocal: true})
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

const baseProfile = "name = \"base\"\ndescription = \"d\"\n"

func (f *fixture) seed() string {
	f.write("profiles/base.toml", baseProfile)
	f.write("mcp/registry.toml", "[servers.docs]\ntype = \"http\"\nurl = \"https://mcp.example.com/docs\"\n")
	f.write("prompts/p.md", "hello\n")
	return f.commit("seed")
}

func TestValidateURL(t *testing.T) {
	tests := []struct {
		url   string
		local bool
		ok    bool
	}{
		{"https://github.com/acme/data.git", false, true},
		{"https://user@github.com/acme/data.git", false, false},
		{"https://ghp_token@github.com/acme/data.git", false, false},
		{"https://github.com/acme/data.git?token=x", false, false},
		{"https://github.com/acme/data.git#frag", false, false},
		{"https://github.com/acme/da\u202eta.git", false, false},
		{"https://github.com/acme/da\u200bta.git", false, false},
		{"ssh://git:pw@github.com/acme/data.git", false, false},
		{"ssh://-oProxyCommand=x/repo", false, false},
		{"file:///srv/repo?x=1", true, false},
		{"https://tok@host/r", true, false},
		{"https://user:pw@github.com/acme/data.git", false, false},
		{"ssh://git@github.com/acme/data.git", false, true},
		{"git@github.com:acme/data.git", false, true},
		{"", false, false},
		{"-uupload-pack=evil", false, false},
		{"--upload-pack=x", true, false},
		{"ext::sh -c touch% /tmp/x", false, false},
		{"ext::sh", true, false},
		{"fd::17/foo", true, false},
		{"https://host/a b", false, false},
		{"https://host/a\nb", false, false},
		{"http://github.com/acme/data.git", false, false},
		{"git://github.com/acme/data.git", false, false},
		{"ftp://host/x", false, false},
		{"file:///srv/repo", false, false},
		{"file:///srv/repo", true, true},
		{"/srv/repo", false, false},
		{"/srv/repo", true, true},
		{"https://-bad/repo", false, false},
		{"https:///nohost", false, false},
		{"-x@host:path", false, false},
		{"https://%zz", false, false},
	}
	for _, tt := range tests {
		err := validateURL(tt.url, tt.local)
		if (err == nil) != tt.ok {
			t.Errorf("validateURL(%q, %v) = %v; want ok=%v", tt.url, tt.local, err, tt.ok)
		}
		if err != nil && !errors.Is(err, ErrBadURL) {
			t.Errorf("error for %q does not wrap ErrBadURL: %v", tt.url, err)
		}
	}
}

func TestNewValidation(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		want string
	}{
		{"branch main", Options{URL: "https://h/r", Ref: "main"}, "branch"},
		{"branch HEAD", Options{URL: "https://h/r", Ref: "HEAD"}, "branch"},
		{"origin branch", Options{URL: "https://h/r", Ref: "origin/dev"}, "branch"},
		{"empty ref", Options{URL: "https://h/r"}, "pinned"},
		{"dash ref", Options{URL: "https://h/r", Ref: "-x"}, "not allowed"},
		{"hostile url", Options{URL: "ext::sh", Ref: "v1"}, "URL"},
		{"abs subpath", Options{URL: "https://h/r", Ref: "v1", Subpath: "/etc"}, "subpath"},
		{"dotdot subpath", Options{URL: "https://h/r", Ref: "v1", Subpath: "a/../.."}, "subpath"},
		{"backslash subpath", Options{URL: "https://h/r", Ref: "v1", Subpath: `a\b`}, "subpath"},
		{"negative timeout", Options{URL: "https://h/r", Ref: "v1", Timeout: -1}, "timeout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.opts)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("New() error = %v; want containing %q", err, tt.want)
			}
		})
	}
	s, err := New(Options{URL: "https://h/r", Ref: "v1", Subpath: "./org//data/"})
	if err != nil {
		t.Fatal(err)
	}
	if s.subpath != "org/data" {
		t.Errorf("subpath = %q", s.subpath)
	}
	if s.Kind() != profile.KindOrg || s.Locator() != "git:https://h/r" || s.Ref() != "v1" || s.URL() != "https://h/r" {
		t.Errorf("accessors wrong: %v %q", s.Kind(), s.Locator())
	}
	if s.ID() != "git:https://h/r@v1" || s.Commit() != "" || s.Root() != "" {
		t.Errorf("unprepared ID/Commit/Root = %q %q %q", s.ID(), s.Commit(), s.Root())
	}
	if _, err := s.Names(); !errors.Is(err, ErrNotPrepared) {
		t.Errorf("Names before Prepare: %v", err)
	}
	if _, err := s.Open("base"); !errors.Is(err, ErrNotPrepared) {
		t.Errorf("Open before Prepare: %v", err)
	}
}

func TestTagPin(t *testing.T) {
	f := newFixture(t)
	sha := f.seed()
	f.git("tag", "v1")
	s := f.source("v1", "")
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Commit() != sha {
		t.Errorf("Commit = %q, want %q", s.Commit(), sha)
	}
	if s.ID() != "git:"+f.url()+"@"+sha {
		t.Errorf("ID = %q", s.ID())
	}
	names, err := s.Names()
	if err != nil || len(names) != 1 || names[0] != "base" {
		t.Fatalf("Names = %v, %v", names, err)
	}
	file, err := s.Open("base")
	if err != nil {
		t.Fatal(err)
	}
	if file.Source != profile.Source(s) {
		t.Error("File.Source must be the git source so the closure sees the commit")
	}
	if _, err := os.Stat(filepath.Join(s.Root(), "mcp", "registry.toml")); err != nil {
		t.Error(err)
	}
	// the checkout is mode 0700 on Unix
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(s.Root())
		if err != nil || fi.Mode().Perm() != 0o700 {
			t.Errorf("checkout mode = %v, %v", fi.Mode().Perm(), err)
		}
	}
	// second Prepare reuses the checkout
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestResolveThroughProfilePackage(t *testing.T) {
	f := newFixture(t)
	f.seed()
	f.write("profiles/dev.toml", "name = \"dev\"\ndescription = \"d\"\nextends = [\"base\"]\n[mcp]\nservers = [\"docs\"]\n")
	sha := f.commit("dev")
	f.git("tag", "v1")
	s := f.source("v1", "")
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	r, err := profile.Resolve("dev", []profile.Source{s}, profile.ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range r.Closure.Items {
		if it.Kind == profile.ItemSource && it.Name == "git:"+f.url()+"@"+sha {
			found = true
		}
	}
	if !found {
		t.Errorf("closure lacks the git source with its SHA: %+v", r.Closure.Items)
	}
}

func TestAnnotatedTagIsPeeled(t *testing.T) {
	f := newFixture(t)
	sha := f.seed()
	f.git("tag", "-a", "v2", "-m", "release")
	if tagObj := f.git("rev-parse", "v2"); tagObj == sha {
		t.Fatal("test setup: tag should be annotated")
	}
	s := f.source("v2", "")
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Commit() != sha {
		t.Errorf("Commit = %q, want the peeled commit %q", s.Commit(), sha)
	}
}

func TestSHAPin(t *testing.T) {
	f := newFixture(t)
	sha := f.seed()
	f.write("profiles/later.toml", "name = \"later\"\ndescription = \"d\"\n")
	f.commit("later")
	s := f.source(strings.ToUpper(sha), "")
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Commit() != sha {
		t.Errorf("Commit = %q, want %q", s.Commit(), sha)
	}
	names, _ := s.Names()
	if len(names) != 1 {
		t.Errorf("an older commit must not show later profiles: %v", names)
	}
	// the SHA pin needs no network once cached: break the origin
	if err := os.RemoveAll(filepath.Join(f.origin, ".git")); err != nil {
		t.Fatal(err)
	}
	s2 := f.source(sha, "")
	if err := s2.Prepare(context.Background()); err != nil {
		t.Fatalf("cached SHA pin should not need the remote: %v", err)
	}
}

func TestUnknownSHAFails(t *testing.T) {
	f := newFixture(t)
	f.seed()
	s := f.source(strings.Repeat("a", 40), "")
	err := s.Prepare(context.Background())
	if err == nil || !strings.Contains(err.Error(), "fetching commit") {
		t.Fatalf("err = %v", err)
	}
}

func TestMovedTagIsDifferentCommit(t *testing.T) {
	f := newFixture(t)
	first := f.seed()
	f.git("tag", "v1")
	s1 := f.source("v1", "")
	if err := s1.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.write("profiles/evil.toml", "name = \"evil\"\ndescription = \"d\"\n")
	second := f.commit("evil")
	f.git("tag", "-f", "v1")
	s2 := f.source("v1", "")
	if err := s2.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s1.Commit() != first || s2.Commit() != second || first == second {
		t.Fatalf("commits: %s %s", s1.Commit(), s2.Commit())
	}
	if s1.ID() == s2.ID() {
		t.Error("IDs must differ when the tag moves")
	}
	if s1.Locator() != s2.Locator() || s1.Ref() != s2.Ref() {
		t.Error("locator and ref are independent of the commit")
	}
	// Re-preparing the first source notices the move too.
	if err := s1.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s1.Commit() != second {
		t.Errorf("a repeated Prepare must re-resolve the tag: %s", s1.Commit())
	}
}

func TestBranchAndNonTagRefs(t *testing.T) {
	f := newFixture(t)
	f.seed()
	f.git("branch", "-M", "main")
	f.git("branch", "release-x")
	// branch-like names never reach git
	if _, err := New(Options{URL: f.url(), Ref: "main", AllowLocal: true}); err == nil {
		t.Fatal("main must be rejected")
	}
	// a branch that is not branch-like fails with RequirePin
	s := f.source("release-x", "")
	err := s.Prepare(context.Background())
	if !errors.Is(err, ErrNotPinned) {
		t.Fatalf("err = %v, want ErrNotPinned", err)
	}
	// and resolves without it, still stored as a SHA
	s2, err := New(Options{URL: f.url(), Ref: "release-x", CacheDir: f.cache, AllowLocal: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s2.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !fullSHA.MatchString(s2.Commit()) {
		t.Errorf("Commit = %q", s2.Commit())
	}
	s3, _ := New(Options{URL: f.url(), Ref: "missing-ref", CacheDir: f.cache, AllowLocal: true})
	if err := s3.Prepare(context.Background()); !errors.Is(err, ErrNotPinned) {
		t.Errorf("missing ref: %v", err)
	}
}

func TestSubpathLayouts(t *testing.T) {
	f := newFixture(t)
	f.write("org/profiles/base.toml", baseProfile)
	f.write("org/mcp/registry.toml", "[servers.docs]\ntype = \"http\"\nurl = \"https://mcp.example.com/docs\"\n")
	f.write("README.md", "x")
	f.commit("seed")
	f.git("tag", "v1")
	for _, sub := range []string{"org", "org/profiles"} {
		s := f.source("v1", sub)
		if err := s.Prepare(context.Background()); err != nil {
			t.Fatalf("%s: %v", sub, err)
		}
		if filepath.Base(s.Root()) != "org" {
			t.Errorf("%s: Root = %s", sub, s.Root())
		}
		if names, _ := s.Names(); len(names) != 1 {
			t.Errorf("%s: names = %v", sub, names)
		}
	}
	// a subpath that does not exist yields no profiles, not an error
	s := f.source("v1", "nope")
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if names, err := s.Names(); err != nil || len(names) != 0 {
		t.Errorf("names = %v, %v", names, err)
	}
}

func TestSymlinkRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs privileges on Windows")
	}
	for _, link := range []string{"profiles/evil.toml", "prompts/secret.md", "mcp/registry.toml"} {
		t.Run(link, func(t *testing.T) {
			f := newFixture(t)
			f.seed()
			if err := os.RemoveAll(filepath.Join(f.origin, filepath.FromSlash(link))); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("/etc/passwd", filepath.Join(f.origin, filepath.FromSlash(link))); err != nil {
				t.Fatal(err)
			}
			f.commit("link")
			f.git("tag", "v1")
			err := f.source("v1", "").Prepare(context.Background())
			if !errors.Is(err, ErrHygiene) || !strings.Contains(err.Error(), link) || !strings.Contains(err.Error(), "symlink") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestSymlinkOutsideWatchedFoldersIsIgnored(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs privileges on Windows")
	}
	f := newFixture(t)
	f.seed()
	if err := os.Symlink("/etc/passwd", filepath.Join(f.origin, "docs-link")); err != nil {
		t.Fatal(err)
	}
	f.commit("link")
	f.git("tag", "v1")
	if err := f.source("v1", "").Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOversizedFileRejected(t *testing.T) {
	f := newFixture(t)
	f.seed()
	f.write("prompts/big.md", strings.Repeat("a", MaxFileSize+1))
	f.commit("big")
	f.git("tag", "v1")
	err := f.source("v1", "").Prepare(context.Background())
	if !errors.Is(err, ErrHygiene) || !strings.Contains(err.Error(), "prompts/big.md") {
		t.Fatalf("err = %v", err)
	}
}

func TestSubmoduleRejected(t *testing.T) {
	f := newFixture(t)
	f.seed()
	f.git("update-index", "--add", "--cacheinfo", "160000,"+f.git("rev-parse", "HEAD")+",profiles/sub")
	f.git("commit", "--quiet", "-m", "gitlink")
	f.git("tag", "v1")
	err := f.source("v1", "").Prepare(context.Background())
	if !errors.Is(err, ErrHygiene) || !strings.Contains(err.Error(), "submodule") {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckTreeUnsafeComponents(t *testing.T) {
	for _, p := range []string{"a/../b", ".git/config", "x/.GIT/y", `profiles/a\b.toml`, "a//b", "profiles/\x01"} {
		err := checkTree([]treeEntry{{mode: "100644", path: p}}, "", newWatch(nil))
		if !errors.Is(err, ErrHygiene) {
			t.Errorf("%q: %v", p, err)
		}
	}
	if err := checkTree([]treeEntry{{mode: "100644", path: "profiles/ok.toml"}, {mode: "120000", path: "other/link"}}, "", newWatch(nil)); err != nil {
		t.Errorf("unexpected: %v", err)
	}
	if err := checkTree([]treeEntry{{mode: "120000", path: "org/profiles/x"}}, "org", newWatch(nil)); err == nil {
		t.Error("symlink under base must fail")
	}
	if err := checkTree([]treeEntry{{mode: "120000", path: "orgs/profiles/x"}}, "org", newWatch(nil)); err != nil {
		t.Errorf("sibling folder is out of scope: %v", err)
	}
}

func TestParseLsTreeErrors(t *testing.T) {
	for _, in := range []string{"garbage", "100644 blob abc\tx\x00", "100644 blob abc xx\tx\x00"} {
		if _, err := parseLsTree(in); err == nil {
			t.Errorf("%q should fail", in)
		}
	}
	e, err := parseLsTree("160000 commit abc       -\tsub\x00100644 blob def      12\ta b\x00")
	if err != nil || len(e) != 2 || e[1].size != 12 || e[1].path != "a b" {
		t.Errorf("%v %v", e, err)
	}
}

func TestHooksAndFiltersNeverRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hook and filter scripts are not portable to Windows")
	}
	f := newFixture(t)
	marker := filepath.Join(t.TempDir(), "marker")
	filterMarker := filepath.Join(t.TempDir(), "filter-marker")
	hookBody := "#!/bin/sh\necho ran $0 >> " + marker + "\n"
	// hooks inside the repository content, in the origin's .git and in the
	// user's own global configuration (core.hooksPath)
	for _, h := range []string{"post-checkout", "post-merge", "reference-transaction", "post-commit"} {
		f.write(".githooks/"+h, hookBody)
		if err := os.WriteFile(filepath.Join(f.origin, ".git", "hooks", h), []byte(hookBody), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	userHooks := t.TempDir()
	for _, h := range []string{"post-checkout", "reference-transaction", "post-merge", "pre-auto-gc"} {
		if err := os.WriteFile(filepath.Join(userHooks, h), []byte(hookBody), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// A real filter driver that would corrupt every file it touches, in the
	// user's global configuration, selected by a hostile .gitattributes.
	smudge := filepath.Join(t.TempDir(), "smudge.sh")
	if err := os.WriteFile(smudge, []byte("#!/bin/sh\necho ran >> "+filterMarker+"\ncat >/dev/null\necho CORRUPTED\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfgText := "[core]\n\thooksPath = " + filepath.ToSlash(userHooks) + "\n" +
		"[filter \"evil\"]\n\tsmudge = " + filepath.ToSlash(smudge) + "\n\tclean = cat\n\trequired = true\n" +
		"[filter \"lfs\"]\n\tsmudge = " + filepath.ToSlash(smudge) + "\n\tclean = cat\n\trequired = true\n"
	// the plain git of this test reads GIT_CONFIG_GLOBAL; the code under test
	// must not honor that variable, so the same text is also the user's
	// ~/.gitconfig, which it does read
	for _, p := range []string{os.Getenv("GIT_CONFIG_GLOBAL"), filepath.Join(f.env["HOME"], ".gitconfig")} {
		if err := os.WriteFile(p, []byte(cfgText), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f.seed()
	f.write(".gitattributes", "*.md filter=evil\n*.toml filter=lfs\n")
	f.commit("attr")
	f.git("tag", "v1")
	// the setup is only meaningful if plain git would have run both
	f.git("checkout", "--quiet", "-b", "other")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("test setup: the user hook should run for plain git: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(t.TempDir(), "plain")
	if out, err := exec.Command("git", "clone", "--quiet", f.origin, plain).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	if _, err := os.Stat(filterMarker); err != nil {
		t.Fatalf("test setup: the filter driver should run for a plain clone: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(plain, "profiles", "base.toml")); !strings.Contains(string(b), "CORRUPTED") {
		t.Fatalf("test setup: the filter should corrupt a plain checkout, got %q", b)
	}
	if err := os.Remove(filterMarker); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(marker) // the plain clone ran hooks too
	s := f.source("v1", "")
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		b, _ := os.ReadFile(marker)
		t.Fatalf("a git hook ran during fetch and extraction: %s", b)
	}
	if _, err := os.Stat(filterMarker); err == nil {
		t.Fatal("a filter driver ran")
	}
	for rel, want := range map[string]string{"profiles/base.toml": baseProfile, "prompts/p.md": "hello\n"} {
		got, err := os.ReadFile(filepath.Join(s.Root(), filepath.FromSlash(rel)))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want the committed bytes %q", rel, got, err, want)
		}
	}
	// and a second Prepare (the verification path) leaves them alone as well
	if err := f.source("v1", "").Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filterMarker); err == nil {
		t.Fatal("a filter driver ran during verification")
	}
}

func TestTamperedCheckoutIsRejected(t *testing.T) {
	cases := []struct {
		name   string
		tamper func(t *testing.T, root string)
	}{
		{"edited file, same size and mtime", func(t *testing.T, root string) {
			p := filepath.Join(root, "profiles", "base.toml")
			fi, err := os.Stat(p)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(p)
			b[len(b)-3] = 'X'
			if err := os.WriteFile(p, b, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(p, fi.ModTime(), fi.ModTime()); err != nil {
				t.Fatal(err)
			}
		}},
		{"untracked file", func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "profiles", "extra.toml"), []byte("name = \"extra\"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"deleted file", func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "prompts", "p.md")); err != nil {
				t.Fatal(err)
			}
		}},
		{"ignored file", func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("*.sneaky\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "profiles", "a.sneaky"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.seed()
			f.git("tag", "v1")
			s := f.source("v1", "")
			if err := s.Prepare(context.Background()); err != nil {
				t.Fatal(err)
			}
			tt.tamper(t, s.Root())
			err := f.source("v1", "").Prepare(context.Background())
			if !errors.Is(err, ErrTampered) {
				t.Fatalf("err = %v, want ErrTampered", err)
			}
		})
	}
}

func TestTamperedHEAD(t *testing.T) {
	f := newFixture(t)
	first := f.seed()
	f.write("profiles/more.toml", "name = \"more\"\ndescription = \"d\"\n")
	f.commit("more")
	f.git("tag", "v1", first)
	s := f.source("v1", "")
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	// point the cached checkout at a different (unfetched) place by editing HEAD
	if err := os.WriteFile(filepath.Join(s.Root(), ".git", "HEAD"), []byte(strings.Repeat("b", 40)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.source("v1", "").Prepare(context.Background()); !errors.Is(err, ErrTampered) {
		t.Fatalf("err = %v", err)
	}
	// and removing .git must not make git look at a parent repository
	root := s.Root()
	if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
		t.Fatal(err)
	}
	if err := f.source("v1", "").Prepare(context.Background()); !errors.Is(err, ErrTampered) {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckoutIsFilePathNotDir(t *testing.T) {
	f := newFixture(t)
	sha := f.seed()
	s := f.source(sha, "")
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	co := filepath.Dir(s.Root())
	_ = co
	final := s.Root()
	if err := os.RemoveAll(final); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(final, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.source(sha, "").Prepare(context.Background()); !errors.Is(err, ErrTampered) {
		t.Fatalf("err = %v", err)
	}
}

func TestConcurrentPrepare(t *testing.T) {
	f := newFixture(t)
	sha := f.seed()
	f.git("tag", "v1")
	var wg sync.WaitGroup
	errs := make([]error, 6)
	srcs := make([]*Source, 6)
	for i := range srcs {
		srcs[i] = f.source("v1", "")
	}
	for i := range srcs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = srcs[i].Prepare(context.Background())
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("prepare %d: %v", i, err)
		}
		if srcs[i].Commit() != sha {
			t.Errorf("commit %d = %s", i, srcs[i].Commit())
		}
	}
	// no temporary checkouts are left behind
	ents, _ := os.ReadDir(filepath.Dir(srcs[0].Root()))
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("leftover %s", e.Name())
		}
	}
	// the same Source may be shared by goroutines
	var wg2 sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			if err := srcs[0].Prepare(context.Background()); err != nil {
				t.Error(err)
			}
			_, _ = srcs[0].Names()
			_ = srcs[0].ID()
		}()
	}
	wg2.Wait()
}

func TestDefaultCacheDir(t *testing.T) {
	f := newFixture(t)
	f.seed()
	f.git("tag", "v1")
	s, err := New(Options{URL: f.url(), Ref: "v1", RequirePin: true, AllowLocal: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.Root(), filepath.Join("ccshelf", "git")) && !strings.Contains(s.Root(), filepath.Join("Local", "ccshelf")) {
		// the default cache location is platform specific; it must be below the isolated HOME
		if !strings.HasPrefix(s.Root(), f.env["HOME"]) && !strings.HasPrefix(s.Root(), f.env["LOCALAPPDATA"]) && !strings.HasPrefix(s.Root(), f.env["XDG_CACHE_HOME"]) {
			t.Errorf("root %s is outside the isolated environment", s.Root())
		}
	}
}

func TestLocalURLRefusedWithoutAllowLocal(t *testing.T) {
	f := newFixture(t)
	f.seed()
	if _, err := New(Options{URL: f.url(), Ref: "v1"}); !errors.Is(err, ErrBadURL) {
		t.Fatalf("err = %v", err)
	}
}

func TestGitFailures(t *testing.T) {
	f := newFixture(t)
	f.seed()
	f.git("tag", "v1")
	// missing git binary
	s, _ := New(Options{URL: f.url(), Ref: "v1", CacheDir: f.cache, AllowLocal: true, GitPath: filepath.Join(t.TempDir(), "nogit")})
	if err := s.Prepare(context.Background()); err == nil {
		t.Error("missing git binary must fail")
	}
	// a repository that does not exist
	s, _ = New(Options{URL: "file:///definitely/not/here", Ref: "v1", CacheDir: f.cache, AllowLocal: true})
	if err := s.Prepare(context.Background()); err == nil {
		t.Error("missing repository must fail")
	}
	// canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.source("v1", "").Prepare(ctx); err == nil {
		t.Error("canceled context must fail")
	}
	// timeout
	s, _ = New(Options{URL: f.url(), Ref: "v1", CacheDir: f.cache, AllowLocal: true, Timeout: time.Nanosecond})
	if err := s.Prepare(context.Background()); err == nil {
		t.Error("an expired timeout must fail")
	}
	// unreadable cache dir (a file)
	bad := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(bad, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ = New(Options{URL: f.url(), Ref: "v1", CacheDir: bad, AllowLocal: true})
	if err := s.Prepare(context.Background()); err == nil {
		t.Error("a file as cache dir must fail")
	}
}

func TestHooksDirMustStayEmpty(t *testing.T) {
	f := newFixture(t)
	f.seed()
	f.git("tag", "v1")
	if err := f.source("v1", "").Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.cache, ".nohooks", "post-checkout"), []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := f.source("v1", "").Prepare(context.Background()); !errors.Is(err, ErrTampered) {
		t.Fatalf("err = %v", err)
	}
}

func TestPickRef(t *testing.T) {
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	tests := []struct{ out, want string }{
		{"", ""},
		{a + "\trefs/tags/v1\n", a},
		{a + "\trefs/tags/v1\n" + b + "\trefs/tags/v1^{}\n", b},
		{a + "\trefs/tags/v10\n", ""},
		{"zz\trefs/tags/v1\n", ""},
		{"garbage line\n", ""},
	}
	for _, tt := range tests {
		if got := pickRef(tt.out, "refs/tags/v1"); got != tt.want {
			t.Errorf("pickRef(%q) = %q, want %q", tt.out, got, tt.want)
		}
	}
}

func TestCappedBuffer(t *testing.T) {
	c := cappedBuffer{max: 4}
	if _, err := c.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write([]byte("de")); !errors.Is(err, errTooMuch) {
		t.Errorf("err = %v", err)
	}
}

func TestLayoutBase(t *testing.T) {
	e := func(p string) []treeEntry { return []treeEntry{{path: p}} }
	tests := []struct {
		entries []treeEntry
		sub     string
		want    string
	}{
		{e("profiles/a.toml"), "", ""},
		{e("org/profiles/a.toml"), "org", "org"},
		{e("org/profiles/a.toml"), "org/profiles", "org"},
		{e("profiles/a.toml"), "profiles", ""},
		{nil, "x/y", "x/y"},
	}
	for _, tt := range tests {
		if got := layoutBase(tt.entries, tt.sub); got != tt.want {
			t.Errorf("layoutBase(%v, %q) = %q, want %q", tt.entries, tt.sub, got, tt.want)
		}
	}
}
