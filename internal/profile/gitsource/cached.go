package gitsource

import (
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
