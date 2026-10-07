package gitdata

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed; skipping git-backed tests")
	}
}

// git runs git in dir with a hermetic configuration.
func git(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+nullDevice(), "GIT_CONFIG_SYSTEM="+nullDevice(), "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=T", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com")
	cmd.Env = append(cmd.Env, env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func commit(t *testing.T, root, file, content, email, date string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(file))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, root, nil, "add", "-A")
	git(t, root, []string{"GIT_AUTHOR_EMAIL=" + email, "GIT_AUTHOR_DATE=" + date, "GIT_COMMITTER_DATE=" + date},
		"-c", "commit.gpgsign=false", "commit", "-q", "-m", "c "+file)
}

func TestCollectAndTags(t *testing.T) {
	needGit(t)
	root := t.TempDir()
	ctx := context.Background()
	if IsRepo(ctx, root) {
		t.Skip("temp dir is inside a git repository")
	}
	git(t, root, nil, "init", "-q", "-b", "main")
	commit(t, root, "plugins/a/x.txt", "1", "alice@example.com", "2026-01-10T12:00:00Z")
	commit(t, root, "plugins/b/x.txt", "1", "bob@example.com", "2026-02-10T12:00:00Z")
	if tag, err := LatestTag(ctx, root); err != nil || tag != "" {
		t.Errorf("no tag yet: %q %v", tag, err)
	}
	git(t, root, nil, "tag", "v1.0.0")
	commit(t, root, "plugins/a/y.txt", "2", "Bob@example.com", "2026-03-10T12:00:00Z")
	commit(t, root, "README.md", "r", "alice@example.com", "2026-03-11T12:00:00Z")

	if !IsRepo(ctx, root) {
		t.Fatal("IsRepo false")
	}
	info, err := Collect(ctx, root, []string{"plugins/a", "plugins/b", "plugins/none"})
	if err != nil {
		t.Fatal(err)
	}
	if info["plugins/a"] != (Info{LastCommit: "2026-03-10", Authors: 2}) {
		t.Errorf("a = %+v", info["plugins/a"])
	}
	if info["plugins/b"] != (Info{LastCommit: "2026-02-10", Authors: 1}) {
		t.Errorf("b = %+v", info["plugins/b"])
	}
	if info["plugins/none"] != (Info{}) {
		t.Errorf("none = %+v", info["plugins/none"])
	}
	tag, err := LatestTag(ctx, root)
	if err != nil || tag != "v1.0.0" {
		t.Fatalf("LatestTag = %q %v", tag, err)
	}
	ch, err := ChangedSince(ctx, root, tag, []string{"plugins/b", "plugins/a", "plugins/none"})
	if err != nil || !reflect.DeepEqual(ch, []string{"plugins/a"}) {
		t.Errorf("ChangedSince = %v %v", ch, err)
	}
	if _, err := ChangedSince(ctx, root, "nope", []string{"plugins/a"}); err == nil {
		t.Error("unknown tag should fail")
	}
}

func TestEmptyRepoAndErrors(t *testing.T) {
	needGit(t)
	root := t.TempDir()
	ctx := context.Background()
	git(t, root, nil, "init", "-q", "-b", "main")
	if tag, err := LatestTag(ctx, root); err != nil || tag != "" {
		t.Errorf("empty repo tag = %q %v", tag, err)
	}
	if _, err := Collect(ctx, t.TempDir(), []string{"a"}); err == nil {
		// Not a repo (unless the temp dir lives inside one).
		t.Log("temp dir is inside a repository")
	}
	for _, bad := range []string{"", "-rf", "/abs", "a\x00b"} {
		if _, err := Collect(ctx, root, []string{bad}); err == nil {
			t.Errorf("dir %q accepted", bad)
		}
		if _, err := ChangedSince(ctx, root, "v1", []string{bad}); err == nil {
			t.Errorf("ChangedSince dir %q accepted", bad)
		}
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := LatestTag(cctx, root); err == nil {
		t.Error("canceled context should fail")
	}
}

func TestValidTag(t *testing.T) {
	good := []string{"v1.0.0", "release/2026.10.1", "sre-kit--v1.2.0", "1"}
	bad := []string{"", "-x", "--upload-pack=x", "a..b", "a b", "x/", "a\nb", "x.lock", ".hidden"}
	for _, g := range good {
		if !ValidTag(g) {
			t.Errorf("%q should be valid", g)
		}
	}
	for _, b := range bad {
		if ValidTag(b) {
			t.Errorf("%q should be invalid", b)
		}
	}
}

func TestEnvDropsGitVars(t *testing.T) {
	t.Setenv("GIT_DIR", "/evil")
	t.Setenv("git_ssh_command", "evil")
	e := env()
	joined := strings.Join(e, "\n")
	if strings.Contains(joined, "GIT_DIR=/evil") || strings.Contains(strings.ToLower(joined), "git_ssh_command=evil") {
		t.Error("inherited GIT_ variable leaked")
	}
	for _, want := range []string{"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + nullDevice()} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %s", want)
		}
	}
}

func TestNoGit(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := LatestTag(context.Background(), t.TempDir()); err == nil || !strings.Contains(err.Error(), "git was not found") {
		t.Errorf("err = %v", err)
	}
}

func TestGitArgsAreHardened(t *testing.T) {
	got := gitArgs("/r", []string{"-c", "safe.directory=/r"}, "log", "--", "x")
	want := []string{"-C", "/r", "-c", "core.fsmonitor=false", "-c", "core.quotepath=false", "-c", "log.showSignature=false", "-c", "safe.directory=/r", "log", "--", "x"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("gitArgs = %v\nwant %v", got, want)
	}
}

func TestFsmonitorHookIsNotRun(t *testing.T) {
	needGit(t)
	if runtime.GOOS == "windows" {
		t.Skip("the hook is a POSIX script")
	}
	root := t.TempDir()
	ctx := context.Background()
	git(t, root, nil, "init", "-q", "-b", "main")
	commit(t, root, "plugins/a/x.txt", "1", "a@example.com", "2026-01-10T12:00:00Z")
	marker := filepath.Join(t.TempDir(), "ran")
	hook := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch '"+marker+"'\nprintf '\\0'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	git(t, root, nil, "config", "core.fsmonitor", hook)
	git(t, root, nil, "tag", "v1.0.0")
	if _, err := run(ctx, root, "status", "--porcelain"); err != nil {
		t.Fatal(err)
	}
	if _, err := ChangedSince(ctx, root, "v1.0.0", []string{"plugins/a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("core.fsmonitor from the repository configuration was executed")
	}
}

func TestCheckDir(t *testing.T) {
	for _, bad := range []string{"", "-", "--", "-x", "-x/y", "/abs", "a\x00b"} {
		if checkDir(bad) == nil {
			t.Errorf("checkDir(%q) accepted", bad)
		}
	}
	for _, ok := range []string{"a", "a-b", "plugins/-x", "plugins/a/", "a.b", "."} {
		if err := checkDir(ok); err != nil {
			t.Errorf("checkDir(%q) = %v", ok, err)
		}
	}
}

func TestChangedSinceOnlyResolvesTags(t *testing.T) {
	needGit(t)
	root := t.TempDir()
	ctx := context.Background()
	git(t, root, nil, "init", "-q", "-b", "main")
	commit(t, root, "plugins/a/x.txt", "1", "a@example.com", "2026-01-10T12:00:00Z")
	git(t, root, nil, "branch", "feature")
	commit(t, root, "plugins/a/y.txt", "2", "a@example.com", "2026-01-11T12:00:00Z")
	// "feature" and "main" are branches, not tags: they must not be accepted.
	for _, name := range []string{"feature", "main", "HEAD"} {
		if _, err := ChangedSince(ctx, root, name, []string{"plugins/a"}); err == nil {
			t.Errorf("branch or ref %q resolved as a tag", name)
		}
	}
}

func TestPluginTagsAreNotReleaseTags(t *testing.T) {
	needGit(t)
	root := t.TempDir()
	ctx := context.Background()
	git(t, root, nil, "init", "-q", "-b", "main")
	commit(t, root, "plugins/a/x.txt", "1", "a@example.com", "2026-01-10T12:00:00Z")
	git(t, root, nil, "tag", "v1.0.0")
	commit(t, root, "plugins/a/y.txt", "2", "a@example.com", "2026-01-11T12:00:00Z")
	git(t, root, nil, "tag", "sre-kit--v9.9.9")
	git(t, root, nil, "tag", "rel-2026.1")
	if tag, err := LatestTag(ctx, root); err != nil || tag != "v1.0.0" {
		t.Errorf("LatestTag = %q %v, want v1.0.0", tag, err)
	}
	if tag, err := LatestReleaseTag(ctx, root, "rel-*"); err != nil || tag != "rel-2026.1" {
		t.Errorf("custom pattern = %q %v", tag, err)
	}
	// Even a broad pattern never returns a plugin tag.
	if tag, err := LatestReleaseTag(ctx, root, "*"); err != nil || tag == "sre-kit--v9.9.9" || tag == "" {
		t.Errorf("broad pattern = %q %v", tag, err)
	}
	if tag, err := LatestReleaseTag(ctx, root, "nomatch*"); err != nil || tag != "" {
		t.Errorf("no match = %q %v", tag, err)
	}
	for _, bad := range []string{"-x", "a b", "a;b", "a..b", strings.Repeat("a", 101), "$(x)"} {
		if _, err := LatestReleaseTag(ctx, root, bad); err == nil {
			t.Errorf("pattern %q accepted", bad)
		}
	}
}

func TestIsShallow(t *testing.T) {
	needGit(t)
	src := t.TempDir()
	ctx := context.Background()
	git(t, src, nil, "init", "-q", "-b", "main")
	commit(t, src, "a.txt", "1", "a@example.com", "2026-01-10T12:00:00Z")
	commit(t, src, "b.txt", "2", "a@example.com", "2026-01-11T12:00:00Z")
	if sh, err := IsShallow(ctx, src); err != nil || sh {
		t.Fatalf("full repo: %v %v", sh, err)
	}
	dst := filepath.Join(t.TempDir(), "clone")
	git(t, filepath.Dir(dst), nil, "clone", "-q", "--depth", "1", "file://"+filepath.ToSlash(src), dst)
	if sh, err := IsShallow(ctx, dst); err != nil || !sh {
		t.Fatalf("shallow clone: %v %v", sh, err)
	}
	if _, err := IsShallow(ctx, t.TempDir()); err == nil {
		t.Log("temp dir is inside a repository")
	}
}

func TestDubiousOwnershipDetection(t *testing.T) {
	err := errors.New("git log: exit status 128: fatal: detected dubious ownership in repository at '/w'")
	if !dubiousOwnership(err) || dubiousOwnership(nil) || dubiousOwnership(errors.New("other")) {
		t.Error("dubiousOwnership misclassifies")
	}
}

func TestInitAndRemoteURL(t *testing.T) {
	needGit(t)
	dir := t.TempDir()
	if IsRepo(context.Background(), dir) {
		t.Skip("the temporary directory is inside a git work tree")
	}
	if err := Init(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(dir, ".git")); err != nil || !st.IsDir() {
		t.Fatalf(".git: %v", err)
	}
	head, err := os.ReadFile(filepath.Join(dir, ".git", "HEAD"))
	if err != nil || strings.TrimSpace(string(head)) != "ref: refs/heads/main" {
		t.Errorf("HEAD = %q, %v", head, err)
	}
	// Init makes no commit and no other file.
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Errorf("init wrote more than .git: %v", ents)
	}
	if got := RemoteURL(context.Background(), dir); got != "" {
		t.Errorf("RemoteURL of a repo without a remote = %q", got)
	}
	git(t, dir, nil, "remote", "add", "origin", "git@ghe.example.com:acme/data.git")
	if got := RemoteURL(context.Background(), dir); got != "git@ghe.example.com:acme/data.git" {
		t.Errorf("RemoteURL = %q", got)
	}
	if got := RemoteURL(context.Background(), t.TempDir()); got != "" {
		t.Errorf("RemoteURL outside a repository = %q", got)
	}
}

func TestInitFailsForAMissingDirectory(t *testing.T) {
	needGit(t)
	if err := Init(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("Init of a missing directory succeeded")
	}
}

func TestInitSkipsTheTemplateDirectory(t *testing.T) {
	needGit(t)
	ctx := context.Background()
	plain := t.TempDir()
	if IsRepo(ctx, plain) {
		t.Skip("the temporary directory is inside a git work tree")
	}
	git(t, plain, nil, "init", "-q")
	samples, _ := os.ReadDir(filepath.Join(plain, ".git", "hooks"))
	if len(samples) == 0 {
		t.Skip("this git installation has no template hooks, so there is nothing to skip")
	}
	dir := t.TempDir()
	if err := Init(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadDir(filepath.Join(dir, ".git", "hooks")); len(got) != 0 {
		t.Errorf("Init copied the template hooks: %v", got)
	}
}

func TestEnclosingWorkTree(t *testing.T) {
	needGit(t)
	ctx := context.Background()
	outer := t.TempDir()
	if IsRepo(ctx, outer) {
		t.Skip("the temporary directory is inside a git work tree")
	}
	if got := EnclosingWorkTree(ctx, filepath.Join(outer, "not", "yet")); got != "" {
		t.Errorf("no repository above: %q", got)
	}
	git(t, outer, nil, "init", "-q")
	want, _ := filepath.EvalSymlinks(outer)
	for _, sub := range []string{"", "sub", filepath.Join("a", "b", "missing")} {
		got := EnclosingWorkTree(ctx, filepath.Join(outer, sub))
		if g, _ := filepath.EvalSymlinks(got); g != want {
			t.Errorf("EnclosingWorkTree(%q) = %q, want %q", sub, got, want)
		}
	}
}

func TestDefaultBranch(t *testing.T) {
	needGit(t)
	ctx := context.Background()
	dir := t.TempDir()
	if IsRepo(ctx, dir) {
		t.Skip("the temporary directory is inside a git work tree")
	}
	if b, src := DefaultBranch(ctx, dir); b != "" || src != "" {
		t.Errorf("no repository: %q %q", b, src)
	}
	git(t, dir, nil, "init", "-q", "-b", "trunk")
	if b, src := DefaultBranch(ctx, dir); b != "trunk" || src != "HEAD" {
		t.Errorf("unborn HEAD: %q %q", b, src)
	}
	// A clone-style origin/HEAD wins over the current branch.
	commit(t, dir, "x.txt", "1", "a@example.com", "2026-01-10T12:00:00Z")
	git(t, dir, nil, "update-ref", "refs/remotes/origin/master", "HEAD")
	git(t, dir, nil, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/master")
	git(t, dir, nil, "checkout", "-q", "-b", "feature")
	if b, src := DefaultBranch(ctx, dir); b != "master" || src != "origin/HEAD" {
		t.Errorf("origin/HEAD: %q %q", b, src)
	}
}
