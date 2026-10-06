package e2e

import (
	"encoding/pem"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRemote is a real git repository served over HTTPS (a TLS test server in
// front of git http-backend) so that the real binary, which only accepts
// https and ssh remotes, can fetch it. The sandbox's ~/.gitconfig trusts the
// server's certificate.
type gitRemote struct {
	t    *testing.T
	work string // working repository
	bare string // what the server serves
	URL  string
	env  []string
	srv  *httptest.Server
}

func newGitRemote(t *testing.T, s *sandbox, src string) *gitRemote {
	t.Helper()
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	out, err := exec.Command(gitBin, "--exec-path").Output()
	if err != nil {
		t.Skip("git --exec-path: ", err)
	}
	backend := filepath.Join(strings.TrimSpace(string(out)), "git-http-backend")
	if runtimeIsWindows() {
		backend += ".exe"
	}
	if _, err := os.Stat(backend); err != nil {
		t.Skip("git http-backend is not available")
	}
	root := t.TempDir()
	r := &gitRemote{t: t, work: src, bare: filepath.Join(root, "org.git")}
	r.env = []string{
		"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_SYSTEM=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"HOME=" + root, "PATH=" + os.Getenv("PATH"),
	}
	r.git(src, "init", "--quiet")
	r.git(src, "config", "commit.gpgsign", "false")
	r.git(src, "config", "tag.gpgsign", "false")
	r.git(src, "add", "-A")
	r.git(src, "commit", "--quiet", "-m", "seed")
	r.git(root, "init", "--quiet", "--bare", r.bare)
	for k, v := range map[string]string{"uploadpack.allowAnySHA1InWant": "true", "uploadpack.allowFilter": "true", "http.receivepack": "false"} {
		r.git(r.bare, "config", k, v)
	}
	h := &cgi.Handler{
		Path:       backend,
		Env:        []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"},
		InheritEnv: []string{"PATH", "HOME"},
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(h.ServeHTTP))
	t.Cleanup(srv.Close)
	r.srv = srv
	r.URL = srv.URL + "/org.git"
	pemFile := filepath.Join(root, "ca.pem")
	write(t, pemFile, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})))
	write(t, filepath.Join(s.Home, ".gitconfig"), "[http]\n\tsslCAInfo = "+filepath.ToSlash(pemFile)+"\n")
	return r
}

// stop shuts the server down.
func (r *gitRemote) stop() { r.srv.Close() }

func runtimeIsWindows() bool { return os.PathSeparator == '\\' }

func (r *gitRemote) git(dir string, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = r.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// publish commits the working tree, tags it and pushes to the served repository.
func (r *gitRemote) publish(tag string) {
	r.t.Helper()
	r.git(r.work, "add", "-A")
	r.git(r.work, "commit", "--quiet", "--allow-empty", "-m", tag)
	r.git(r.work, "tag", tag)
	r.git(r.work, "push", "--quiet", r.bare, "HEAD:refs/heads/main", "refs/tags/"+tag)
}

func (s *sandbox) useGitSource(url, ref string) {
	s.t.Helper()
	write(s.t, filepath.Join(s.ConfigDir(), "config.toml"),
		"[[sources]]\ntype = \"git\"\nurl = \""+url+"\"\nref = \""+ref+"\"\n")
}

func TestGitSourceProtectedPluginAndProtectChange(t *testing.T) {
	s := newSandbox(t)
	s.Setenv("FAKE_CLAUDE_PLUGINS", pluginsFile(t, "audit-logger@acme", "sre-kit@acme", "design-kit@acme", "seo-tools@acme"))
	org := exampleOrg(t) // protects audit-logger@acme in ccshelf.toml
	rem := newGitRemote(t, s, org)
	rem.publish("v1")
	s.useGitSource(rem.URL, "v1")

	s.mustRun("trust", "seo", "--accept", s.closureHash("seo"))
	r := s.mustRun("run", "seo")
	contains(t, "stderr", r.Stderr, "audit-logger@acme is protected")
	if strings.Contains(r.Stderr, "has no ccshelf.toml") {
		t.Errorf("warned although the repository has ccshelf.toml:\n%s", r.Stderr)
	}
	ep := enabledPlugins(t, settingsOf(t, s.launches()[0]))
	if _, ok := ep["audit-logger@acme"]; ok {
		t.Errorf("a protected plugin from the git source's ccshelf.toml was masked: %v", ep)
	}

	// Editing the protect list at a new tag forces a review (exit 4).
	write(t, filepath.Join(org, "ccshelf.toml"), "[protect]\nplugins = []\n")
	rem.publish("v2")
	s.useGitSource(rem.URL, "v2")
	r = s.run("run", "seo")
	if r.Code != 4 {
		t.Fatalf("after the protect list changed: exit %d, want 4\n%s", r.Code, r.Stderr)
	}
	if n := len(s.launches()); n != 1 {
		t.Errorf("claude started %d times, want 1", n)
	}
}

func TestGitSourceWithoutOrgConfigWarnsAndOfflineRunUsesTheCache(t *testing.T) {
	s := newSandbox(t)
	org := exampleOrg(t)
	if err := os.Remove(filepath.Join(org, "ccshelf.toml")); err != nil {
		t.Fatal(err)
	}
	rem := newGitRemote(t, s, org)
	rem.publish("v1")
	s.useGitSource(rem.URL, "v1")
	r := s.mustRun("ls")
	contains(t, "stderr", r.Stderr, "has no ccshelf.toml")

	s.mustRun("trust", "seo", "--accept", s.closureHash("seo"))
	// The server goes away: the trusted, cached commit still runs.
	rem.stop()
	r = s.run("run", "seo")
	if r.Code != 0 {
		t.Fatalf("offline run: exit %d\n%s", r.Code, r.Stderr)
	}
	if strings.Contains(r.Stderr, "unavailable") {
		t.Errorf("a cached source is not unavailable:\n%s", r.Stderr)
	}
	// A personal profile always runs.
	s.writeProfile("mine", "name = \"mine\"\ndescription = \"d\"\n")
	if r = s.run("run", "mine"); r.Code != 0 {
		t.Errorf("personal profile: exit %d\n%s", r.Code, r.Stderr)
	}
	// --refresh re-resolves the tag, which now fails: reported, not fatal.
	r = s.run("ls", "--refresh")
	if r.Code != 0 {
		t.Errorf("ls --refresh with the server down: exit %d\n%s", r.Code, r.Stderr)
	}
	contains(t, "stderr", r.Stderr, "unavailable")
}

// A deprecated plugin of the org's catalog is a warning on run for a git
// source, whose sidecars the launcher extracts and verifies with the profiles.
func TestGitSourceDeprecatedPluginWarning(t *testing.T) {
	s := newSandbox(t)
	s.Setenv("FAKE_CLAUDE_PLUGINS", pluginsFile(t, "audit-logger@acme", "sre-kit@acme", "design-kit@acme", "seo-tools@acme", "docs-writer@acme"))
	org := exampleOrg(t)
	write(t, filepath.Join(org, "catalog", "plugins", "seo-tools.toml"),
		"owner = \"@acme/seo\"\nstatus = \"deprecated\"\nsuperseded_by = \"docs-writer\"\n")
	rem := newGitRemote(t, s, org)
	rem.publish("v1")
	s.useGitSource(rem.URL, "v1")

	s.mustRun("trust", "seo", "--accept", s.closureHash("seo"))
	r := s.mustRun("run", "seo")
	contains(t, "stderr", r.Stderr, "plugin seo-tools@acme is deprecated; use docs-writer")
	if n := strings.Count(r.Stderr, "is deprecated"); n != 1 {
		t.Errorf("deprecation warning shown %d times, want once:\n%s", n, r.Stderr)
	}
	if len(s.launches()) != 1 {
		t.Errorf("a deprecated plugin must not block the run")
	}
	// It is in the structured warnings too.
	j := s.mustRun("--json", "dry-run", "seo")
	contains(t, "dry-run --json", j.Stdout, "seo-tools@acme is deprecated")
}

// search, recommend and doctor --policy work for a developer who reaches the
// org only through a git source: no checkout of the org data repo, and no
// network after the commit is cached.
func TestGitSourceCatalogCommandsWithoutAnOrgRepo(t *testing.T) {
	s := newSandbox(t)
	org := exampleOrg(t)
	rem := newGitRemote(t, s, org)
	rem.publish("v1")

	// Nothing configured, nothing cached: a clear failure that names the way out.
	r := s.run("search", "seo")
	if r.Code != 1 {
		t.Fatalf("search without any source: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	contains(t, "stderr", r.Stderr, "not an org data repo")
	contains(t, "stderr", r.Stderr, "--root")

	s.useGitSource(rem.URL, "v1")
	// Configured but never fetched: still nothing, and still no network.
	if r = s.run("search", "seo"); r.Code != 1 {
		t.Fatalf("search before the source was fetched: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	s.mustRun("ls") // fetches and verifies the checkout into the cache
	// A fetched commit is not a trusted one: the catalog is read only from a
	// commit the user accepted.
	if r = s.run("search", "seo"); r.Code != 1 {
		t.Fatalf("search before any trust: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	s.mustRun("trust", "seo", "--accept", s.closureHash("seo"))
	rem.stop() // from here on the server is gone

	r = s.mustRun("search", "seo")
	contains(t, "search", r.Stdout, "seo-tools")
	contains(t, "stderr", r.Stderr, "reading the cached catalog of git "+rem.URL)
	j := s.mustRun("--json", "search", "seo")
	contains(t, "search --json", j.Stdout, `"source"`)

	proj := t.TempDir()
	write(t, filepath.Join(proj, "main.tf"), "terraform {}\n")
	r = s.mustRun("recommend", "--dir", proj)
	contains(t, "recommend", r.Stdout, "sre-kit@acme")

	// An explicit --root that is not an org repo does not fall back.
	if r = s.run("--root", t.TempDir(), "search", "seo"); r.Code != 1 {
		t.Errorf("explicit --root: exit %d, want 1\n%s", r.Code, r.Stderr)
	}

	// doctor --policy needs no org repo. The exit code depends on the policy of
	// the machine running the test (0, or 3 when it blocks something), never 1.
	r = s.run("doctor", "--policy")
	if r.Code != 0 && r.Code != 3 {
		t.Fatalf("doctor --policy outside an org repo: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	contains(t, "doctor --policy", r.Stdout, "capability matrix")
}
