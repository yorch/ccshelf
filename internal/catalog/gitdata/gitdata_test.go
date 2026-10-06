package gitdata

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
		t.Error("cancelled context should fail")
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
