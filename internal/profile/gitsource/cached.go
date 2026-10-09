package gitsource

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CachedCommits lists the commits of the repository of s that have a checkout
// in the cache, newest use first. It does no network access and does not
// verify anything: pass each commit to PrepareCached, which applies the full
// verification, to find one that can be used (the launcher does this to run
// without network access when nothing is pinned for the tag). A repository
// with no cached checkout gives an empty list and no error.
func (s *Source) CachedCommits() ([]string, error) {
	base, err := s.cacheBase()
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(CheckoutDir(base, s.opts.URL, strings.Repeat("0", 40)))
	ents, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("listing cached checkouts: %w", err)
	}
	type found struct {
		sha string
		at  int64
	}
	var list []found
	for _, e := range ents {
		if !e.IsDir() || !fullSHA.MatchString(e.Name()) {
			continue
		}
		fi, err := os.Lstat(filepath.Join(dir, e.Name()))
		if err != nil || !fi.IsDir() {
			continue
		}
		list = append(list, found{e.Name(), fi.ModTime().UnixNano()})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].at != list[j].at {
			return list[i].at > list[j].at
		}
		return list[i].sha < list[j].sha
	})
	out := make([]string, len(list))
	for i, f := range list {
		out[i] = f.sha
	}
	return out, nil
}

// IsAncestor reports whether the commit older is a strict ancestor of the
// commit newer. It works offline, on the verified cached checkout of newer
// only: it never contacts the remote and never resolves a ref. The answer is
// true only when it is proven. A missing checkout, a checkout that fails the
// verification, a history that the shallow checkout does not hold, and any
// error all give false, so a caller must treat false as "not known".
func (s *Source) IsAncestor(ctx context.Context, older, newer string) bool {
	older, newer = strings.ToLower(older), strings.ToLower(newer)
	if older == newer || !fullSHA.MatchString(older) || !fullSHA.MatchString(newer) {
		return false
	}
	base, err := s.cacheBase()
	if err != nil {
		return false
	}
	dir := CheckoutDir(base, s.opts.URL, newer)
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
		return false
	}
	timeout := s.opts.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := s.verifyRepo(ctx, dir, newer); err != nil {
		return false
	}
	// The commit object was verified against its id, so its parent lines are
	// authentic. A shallow checkout holds the parent id even when it does not
	// hold the parent object.
	raw, err := s.git(ctx, dir, dir, "cat-file", "commit", newer)
	if err != nil {
		return false
	}
	head, _, _ := strings.Cut(raw, "\n\n")
	for _, line := range strings.Split(head, "\n") {
		if p, ok := strings.CutPrefix(line, "parent "); ok && strings.TrimSpace(p) == older {
			return true
		}
	}
	_, err = s.git(ctx, dir, dir, "merge-base", "--is-ancestor", older, newer)
	return err == nil
}
