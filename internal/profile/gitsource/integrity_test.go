package gitsource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ccshelf/ccshelf/internal/orgconfig"
)

// PoC: a replace ref inside the cached checkout's object store swaps the tree
// of the pinned commit; PrepareCached (and Prepare) accept it.
func TestPoCReplaceRefSwapsTreeOfPinnedCommit(t *testing.T) {
	f := newFixture(t)
	f.write("ccshelf.toml", "[protect]\nplugins = [\"audit@acme\"]\n")
	f.seed()
	sha1 := f.git("rev-parse", "HEAD")
	t1 := f.git("rev-parse", "HEAD^{tree}")
	f.write("ccshelf.toml", "")
	f.write("profiles/base.toml", "name = \"base\"\ndescription = \"evil\"\n")
	f.commit("evil")
	sha2 := f.git("rev-parse", "HEAD")
	t2 := f.git("rev-parse", "HEAD^{tree}")
	f.git("reset", "--hard", "--quiet", sha1)
	f.git("tag", "v1")

	s := f.source("v1", "")
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg, _ := s.OrgConfig()
	if len(cfg.Protect.Plugins) != 1 {
		t.Fatalf("setup: %v", cfg.Protect.Plugins)
	}
	co := s.Root()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"--git-dir=" + filepath.Join(co, ".git")}, args...)...)
		cmd.Dir = co
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("-c", "protocol.file.allow=always", "fetch", "--quiet", "--depth", "1", f.url(), sha2)
	run("replace", t1, t2)
	// make the disk match the replacement tree
	if err := os.WriteFile(filepath.Join(co, "ccshelf.toml"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(co, "profiles", "base.toml"), []byte("name = \"base\"\ndescription = \"evil\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s2 := f.source("v1", "")
	err := s2.PrepareCached(context.Background(), sha1)
	if err != nil {
		t.Logf("rejected (good): %v", err)
		return
	}
	cfg2, _ := s2.OrgConfig()
	file, _ := s2.Open("base")
	t.Errorf("VULN: PrepareCached accepted commit %s with a swapped tree: protect=%v profile=%q", sha1, cfg2.Protect.Plugins, strings.TrimSpace(string(file.Raw)))
	s3 := f.source("v1", "")
	if err := s3.Prepare(context.Background()); err == nil {
		cfg3, _ := s3.OrgConfig()
		t.Errorf("VULN: Prepare (online) also accepted: protect=%v", cfg3.Protect.Plugins)
	}
}

// PoC: overwrite the loose tree object of the pinned commit (no replace ref).
func TestPoCLooseTreeOverwrite(t *testing.T) {
	f := newFixture(t)
	f.write("ccshelf.toml", "[protect]\nplugins = [\"audit@acme\"]\n")
	f.seed()
	sha1 := f.git("rev-parse", "HEAD")
	t1 := f.git("rev-parse", "HEAD^{tree}")
	f.write("ccshelf.toml", "")
	f.commit("evil")
	t2 := f.git("rev-parse", "HEAD^{tree}")
	cmd := exec.Command("git", "cat-file", "tree", t2)
	cmd.Dir = f.origin
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	f.git("reset", "--hard", "--quiet", sha1)
	f.git("tag", "v1")
	s := f.source("v1", "")
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	co := s.Root()
	// The empty blob must exist in the store for ls-tree --long to size it.
	c2 := exec.Command("git", "--git-dir="+filepath.Join(co, ".git"), "hash-object", "-w", "--stdin")
	c2.Stdin = strings.NewReader("")
	if out, err := c2.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	rewriteLooseObject(t, filepath.Join(co, ".git"), t1, append([]byte("tree "+itoa(len(raw))+"\x00"), raw...))
	if err := os.WriteFile(filepath.Join(co, "ccshelf.toml"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s2 := f.source("v1", "")
	if err := s2.PrepareCached(context.Background(), sha1); err != nil {
		t.Logf("rejected (good): %v", err)
		return
	}
	cfg2, _ := s2.OrgConfig()
	t.Errorf("VULN: loose tree overwrite accepted; protect=%v", cfg2.Protect.Plugins)
}

func TestPrepareCachedRequiresAFullCommitSHA(t *testing.T) {
	f := newFixture(t)
	sha := f.seed()
	f.git("tag", "v1")
	s := f.source("v1", "")
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"v1", sha[:12], "", strings.Repeat("g", 40), sha + "0"} {
		err := f.source("v1", "").PrepareCached(context.Background(), bad)
		if !errors.Is(err, ErrNotPinned) {
			t.Errorf("PrepareCached(%q) = %v, want ErrNotPinned", bad, err)
		}
	}
	if err := f.source("v1", "").PrepareCached(context.Background(), strings.ToUpper(sha)); err != nil {
		t.Errorf("an upper-case full SHA must work: %v", err)
	}
}

func TestPrepareMarksTheCheckoutUsedBeforePruning(t *testing.T) {
	f := newFixture(t)
	sha := f.seed()
	s := f.source(sha, "")
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	co := s.Root()
	old := time.Now().Add(-90 * 24 * time.Hour)
	if err := os.Chtimes(co, old, old); err != nil {
		t.Fatal(err)
	}
	if err := f.source(sha, "").PrepareCached(context.Background(), sha); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(co)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(fi.ModTime()) > time.Hour {
		t.Errorf("a used checkout keeps its old mtime %v and could be pruned", fi.ModTime())
	}
}

func TestTamperedTreeIsRejectedEvenWhenTheDiskMatches(t *testing.T) {
	f := newFixture(t)
	sha := f.seed()
	s := f.source(sha, "")
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Join(s.Root(), ".git")
	// A subtree object rewritten to different content (an empty tree body).
	tree := f.git("rev-parse", "HEAD:profiles")
	rewriteLooseObject(t, gitDir, tree, []byte("tree 0\x00"))
	err := f.source(sha, "").PrepareCached(context.Background(), sha)
	if !errors.Is(err, ErrTampered) {
		t.Fatalf("err = %v, want ErrTampered", err)
	}
}

func TestReplaceRefsAreRemoved(t *testing.T) {
	f := newFixture(t)
	sha := f.seed()
	s := f.source(sha, "")
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Join(s.Root(), ".git")
	ref := filepath.Join(gitDir, "refs", "replace", sha)
	if err := os.MkdirAll(filepath.Dir(ref), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ref, []byte(sha+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.source(sha, "").PrepareCached(context.Background(), sha); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ref); err == nil {
		t.Error("the replace ref must be deleted")
	}
}

func TestOrgConfigHygieneFailuresAreFatalInvalid(t *testing.T) {
	t.Run("oversized", func(t *testing.T) {
		f := newFixture(t)
		f.write("ccshelf.toml", "# "+strings.Repeat("x", MaxFileSize+10)+"\n")
		f.seed()
		f.git("tag", "v1")
		err := f.source("v1", "").Prepare(context.Background())
		if !errors.Is(err, orgconfig.ErrInvalid) {
			t.Fatalf("err = %v, want orgconfig.ErrInvalid", err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlinks are not portable to Windows")
		}
		f := newFixture(t)
		f.seed()
		if err := os.Symlink("profiles/base.toml", filepath.Join(f.origin, "ccshelf.toml")); err != nil {
			t.Fatal(err)
		}
		f.commit("link")
		f.git("tag", "v1")
		err := f.source("v1", "").Prepare(context.Background())
		if !errors.Is(err, orgconfig.ErrInvalid) {
			t.Fatalf("err = %v, want orgconfig.ErrInvalid", err)
		}
	})
}

// oldUrlKey is the folder name an earlier build gave a repository (no layout
// version in the hash).
func oldURLKey(u string) string {
	sum := sha256.Sum256([]byte(u))
	return hex.EncodeToString(sum[:8])
}

func TestCheckoutOfAnOlderLayoutIsIgnoredNotTampered(t *testing.T) {
	f := newFixture(t)
	f.seed()
	f.write("catalog/plugins/x.toml", sidecarDeprecated)
	f.write(".claude-plugin/marketplace.json", `{"name":"acme","owner":{"name":"x"},"plugins":[]}`)
	sha := f.commit("catalog")
	f.git("tag", "v1")
	s := f.source("v1", "")
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Turn the fresh checkout into what the previous build left: under the old
	// folder name, without the catalog files.
	cur := CheckoutDir(f.cache, f.url(), sha)
	old := filepath.Join(f.cache, oldURLKey(f.url()), sha)
	if old == cur {
		t.Fatal("the layout version must change the checkout folder")
	}
	if err := os.MkdirAll(filepath.Dir(old), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(cur, old); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"catalog", ".claude-plugin"} {
		if err := os.RemoveAll(filepath.Join(old, rel)); err != nil {
			t.Fatal(err)
		}
	}
	s2 := f.source("v1", "")
	if err := s2.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare after an upgrade: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s2.Root(), "catalog", "plugins", "x.toml")); err != nil {
		t.Errorf("catalog data not extracted: %v", err)
	}
	if err := f.source("v1", "").PrepareCached(context.Background(), sha); err != nil {
		t.Errorf("PrepareCached after Prepare: %v", err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Errorf("the old checkout should be left for the pruning: %v", err)
	}
}

func TestMissingCatalogFilesInTheSameLayoutAreRestored(t *testing.T) {
	f := newFixture(t)
	f.seed()
	f.write("catalog/plugins/x.toml", sidecarDeprecated)
	f.write(".claude-plugin/marketplace.json", `{"name":"acme","owner":{"name":"x"},"plugins":[]}`)
	sha := f.commit("catalog")
	f.git("tag", "v1")
	if err := f.source("v1", "").Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	dir := CheckoutDir(f.cache, f.url(), sha)
	for _, rel := range []string{"catalog", ".claude-plugin"} {
		if err := os.RemoveAll(filepath.Join(dir, rel)); err != nil {
			t.Fatal(err)
		}
	}
	// Both entry points repair it from the verified object store.
	for name, prep := range map[string]func(*Source) error{
		"Prepare":       func(s *Source) error { return s.Prepare(context.Background()) },
		"PrepareCached": func(s *Source) error { return s.PrepareCached(context.Background(), sha) },
	} {
		for _, rel := range []string{"catalog", ".claude-plugin"} {
			_ = os.RemoveAll(filepath.Join(dir, rel))
		}
		if err := prep(f.source("v1", "")); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		b, err := os.ReadFile(filepath.Join(dir, "catalog", "plugins", "x.toml"))
		if err != nil || string(b) != sidecarDeprecated {
			t.Errorf("%s: restored sidecar = %q, %v", name, b, err)
		}
	}
	// Missing files of the older, always watched parts are still tampering.
	if err := os.Remove(filepath.Join(dir, "prompts", "p.md")); err != nil {
		t.Fatal(err)
	}
	if err := f.source("v1", "").Prepare(context.Background()); !errors.Is(err, ErrTampered) {
		t.Errorf("a deleted prompt: err = %v, want ErrTampered", err)
	}
}
