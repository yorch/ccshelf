package gitsource

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func (f *fixture) branchSource(branch string) *Source {
	f.t.Helper()
	s, err := New(Options{URL: f.url(), Branch: branch, CacheDir: f.cache, RequirePin: true, AllowLocal: true})
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

func TestNewBranchValidation(t *testing.T) {
	for name, tt := range map[string]struct {
		opts Options
		want string
	}{
		"both":      {Options{URL: "https://h/r", Ref: "v1", Branch: "main"}, "not both"},
		"neither":   {Options{URL: "https://h/r"}, "pinned"},
		"dash":      {Options{URL: "https://h/r", Branch: "-x"}, "must not start"},
		"dotdot":    {Options{URL: "https://h/r", Branch: "a..b"}, ".."},
		"full ref":  {Options{URL: "https://h/r", Branch: "refs/heads/main"}, "full ref"},
		"HEAD":      {Options{URL: "https://h/r", Branch: "HEAD"}, "not a branch name"},
		"ref main":  {Options{URL: "https://h/r", Ref: "main"}, `branch = "main"`},
		"ref slash": {Options{URL: "https://h/r", Ref: "a/b"}, "use branch"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := New(tt.opts)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("New() error = %v; want containing %q", err, tt.want)
			}
			if !errors.Is(err, ErrNotPinned) && name != "ref main" && name != "ref slash" {
				t.Errorf("error does not wrap ErrNotPinned: %v", err)
			}
		})
	}
	s, err := New(Options{URL: "https://h/r", Branch: "release/2026"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Branch() != "release/2026" || s.Ref() != "branch:release/2026" || s.Locator() != "git:https://h/r" {
		t.Errorf("accessors: %q %q %q", s.Branch(), s.Ref(), s.Locator())
	}
	if s.ID() != "git:https://h/r@branch:release/2026" {
		t.Errorf("unprepared ID = %q", s.ID())
	}
}

func TestBranchResolvesToCommit(t *testing.T) {
	f := newFixture(t)
	first := f.seed()
	f.git("branch", "-M", "main")
	s := f.branchSource("main")
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Commit() != first || s.ID() != "git:"+f.url()+"@"+first {
		t.Fatalf("commit = %q, id = %q; want %q", s.Commit(), s.ID(), first)
	}
	if names, err := s.Names(); err != nil || len(names) != 1 || names[0] != "base" {
		t.Fatalf("names = %v, %v", names, err)
	}

	// A new commit on the branch is seen by the next Prepare, as a new commit.
	f.write("profiles/more.toml", "name = \"more\"\ndescription = \"d\"\n")
	second := f.commit("more")
	head, err := f.branchSource("main").ResolveHead(context.Background())
	if err != nil || head != second {
		t.Fatalf("ResolveHead = %q, %v; want %q", head, err, second)
	}
	s2 := f.branchSource("main")
	if err := s2.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s2.Commit() != second {
		t.Fatalf("commit = %q; want %q", s2.Commit(), second)
	}
	// The old commit is still usable from the cache, with no remote.
	s3 := f.branchSource("main")
	if err := s3.PrepareCached(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if s3.Commit() != first {
		t.Errorf("cached commit = %q", s3.Commit())
	}
}

func TestBranchIsNotATag(t *testing.T) {
	f := newFixture(t)
	f.seed()
	f.git("branch", "-M", "main")
	f.git("tag", "v1")
	f.git("branch", "feature-1")
	// A tag name is not a branch, and a branch name is not a tag.
	if _, err := f.branchSource("v1").ResolveHead(context.Background()); !errors.Is(err, ErrNotPinned) {
		t.Errorf("a tag resolved as a branch: %v", err)
	}
	if err := f.source("feature-1", "").Prepare(context.Background()); !errors.Is(err, ErrNotPinned) {
		t.Errorf("a branch resolved as a tag: %v", err)
	}
	if _, err := f.branchSource("missing").ResolveHead(context.Background()); !errors.Is(err, ErrNotPinned) {
		t.Errorf("a missing branch: %v", err)
	}
	// A tag and a branch with the same name resolve to their own commits.
	f.git("checkout", "--quiet", "-b", "same")
	f.write("profiles/x.toml", "name = \"x\"\ndescription = \"d\"\n")
	branchTip := f.commit("on branch")
	f.git("checkout", "--quiet", "main")
	f.git("tag", "same")
	tagSrc, brSrc := f.source("same", ""), f.branchSource("same")
	if tagSrc.Ref() == brSrc.Ref() || tagSrc.ID() == brSrc.ID() {
		t.Fatalf("tag and branch share an identity: %q %q", tagSrc.Ref(), brSrc.Ref())
	}
	if err := tagSrc.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := brSrc.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tagSrc.Commit() == brSrc.Commit() || brSrc.Commit() != branchTip {
		t.Errorf("tag %q branch %q want branch %q", tagSrc.Commit(), brSrc.Commit(), branchTip)
	}
}

func TestResolveHeadNeedsBranch(t *testing.T) {
	f := newFixture(t)
	f.seed()
	f.git("tag", "v1")
	if _, err := f.source("v1", "").ResolveHead(context.Background()); err == nil {
		t.Error("ResolveHead on a tag source should fail")
	}
}

func TestPrepareAtFetchesTheKnownCommitNotTheHead(t *testing.T) {
	f := newFixture(t)
	first := f.seed()
	f.git("branch", "-M", "main")
	s := f.branchSource("main")
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.write("profiles/more.toml", "name = \"more\"\ndescription = \"d\"\n")
	second := f.commit("more")
	// The checkout of the first commit is gone from the cache.
	if err := os.RemoveAll(CheckoutDir(f.cache, f.url(), first)); err != nil {
		t.Fatal(err)
	}
	s2 := f.branchSource("main")
	if err := s2.PrepareCached(context.Background(), first); !errors.Is(err, ErrNotCached) {
		t.Fatalf("PrepareCached = %v; want ErrNotCached", err)
	}
	if err := s2.PrepareAt(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if s2.Commit() != first {
		t.Fatalf("commit = %q; want the trusted %q, not the head %q", s2.Commit(), first, second)
	}
	if names, _ := s2.Names(); len(names) != 1 {
		t.Errorf("names = %v; the head's profile leaked in", names)
	}
	if err := s2.PrepareAt(context.Background(), "main"); err == nil {
		t.Error("PrepareAt accepted a name")
	}
}
