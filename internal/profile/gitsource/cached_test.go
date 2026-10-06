package gitsource

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestCachedCommitsListsCheckoutsNewestFirst(t *testing.T) {
	cache := t.TempDir()
	s, err := New(Options{URL: "https://example.com/acme/data.git", Ref: "v1", CacheDir: cache})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.CachedCommits(); err != nil || len(got) != 0 {
		t.Fatalf("empty cache: %v, %v", got, err)
	}
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	now := time.Now()
	for sha, age := range map[string]time.Duration{a: 48 * time.Hour, b: time.Hour} {
		d := CheckoutDir(cache, s.URL(), sha)
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(d, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	// Names that are not commits, files, and other repositories are ignored.
	if err := os.MkdirAll(CheckoutDir(cache, s.URL(), "not-a-sha"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(CheckoutDir(cache, "https://example.com/other.git", strings.Repeat("c", 40)), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := s.CachedCommits()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != b || got[1] != a {
		t.Errorf("CachedCommits = %v, want [%s %s]", got, b, a)
	}
}
