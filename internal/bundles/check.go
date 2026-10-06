package bundles

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// maxDiff caps Drift.Diff (bytes).
const maxDiff = 64 << 10

// Drift is the difference between the files on disk and the generated set.
type Drift struct {
	// Missing are generated files absent on disk.
	Missing []string
	// Modified are files whose bytes differ (including CRLF conversion, and
	// a path that is not a regular file).
	Modified []string
	// Stale are manifests of generated bundles that no profile produces.
	Stale []string
	// Diff is a unified-style diff, capped at 64 KiB.
	Diff string
}

// HasDrift reports whether anything differs.
func (d *Drift) HasDrift() bool {
	return len(d.Missing)+len(d.Modified)+len(d.Stale) > 0
}

// Check compares root/bundles with files without writing anything.
func Check(root string, files []File) (*Drift, error) {
	for _, f := range files {
		if err := validRel(f.Path); err != nil {
			return nil, err
		}
	}
	r, err := rootDir(root)
	if err != nil {
		return nil, err
	}
	sorted := append([]File(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	d := &Drift{}
	var diff strings.Builder
	for _, f := range sorted {
		p := filepath.Join(r, filepath.FromSlash(f.Path))
		if err := ensureParents(r, f.Path); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				d.Missing = append(d.Missing, f.Path)
				diff.WriteString(unified(f.Path, nil, f.Content, ""))
				continue
			}
			return nil, err
		}
		fi, err := os.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			d.Missing = append(d.Missing, f.Path)
			diff.WriteString(unified(f.Path, nil, f.Content, ""))
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect %s: %w", p, err)
		}
		if !fi.Mode().IsRegular() {
			d.Modified = append(d.Modified, f.Path)
			fmt.Fprintf(&diff, "--- a/%s\n+++ b/%s\n(not a regular file)\n", f.Path, f.Path)
			continue
		}
		cur, err := readSmall(p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		if bytes.Equal(cur, f.Content) {
			continue
		}
		d.Modified = append(d.Modified, f.Path)
		note := ""
		if bytes.Equal(bytes.ReplaceAll(cur, []byte("\r\n"), []byte("\n")), f.Content) {
			note = "line endings differ: the file on disk uses CRLF, generated output uses LF"
		}
		diff.WriteString(unified(f.Path, cur, f.Content, note))
	}
	stale, err := staleBundles(r, wantedDirs(files))
	if err != nil {
		return nil, err
	}
	for _, n := range stale {
		rel := Dir + "/" + n + "/.claude-plugin/plugin.json"
		d.Stale = append(d.Stale, rel)
		cur, _ := readSmall(filepath.Join(r, filepath.FromSlash(rel)))
		diff.WriteString(unified(rel, cur, nil, "stale: no profile generates this bundle"))
	}
	d.Diff = diff.String()
	if len(d.Diff) > maxDiff {
		d.Diff = d.Diff[:maxDiff] + "\n... diff truncated\n"
	}
	return d, nil
}

// ensureParents checks that the directories above a file are real
// directories (not symlinks). It returns fs.ErrNotExist when one is absent.
func ensureParents(root, rel string) error {
	i := strings.LastIndex(rel, "/")
	return ensureDirs(root, rel[:i], false)
}

// unified renders a whole-file diff (one hunk). old or new nil means the
// file is absent on that side.
func unified(p string, old, new []byte, note string) string {
	var b strings.Builder
	if old == nil {
		b.WriteString("--- /dev/null\n")
	} else {
		fmt.Fprintf(&b, "--- a/%s\n", p)
	}
	if new == nil {
		b.WriteString("+++ /dev/null\n")
	} else {
		fmt.Fprintf(&b, "+++ b/%s\n", p)
	}
	if note != "" {
		fmt.Fprintf(&b, "# %s\n", note)
	}
	a, c := lines(old), lines(new)
	fmt.Fprintf(&b, "@@ -%s +%s @@\n", rng(len(a)), rng(len(c)))
	for _, op := range lcsDiff(a, c) {
		b.WriteString(op)
		b.WriteByte('\n')
	}
	return b.String()
}

func rng(n int) string {
	if n == 0 {
		return "0,0"
	}
	return fmt.Sprintf("1,%d", n)
}

// lines splits on LF and keeps a trailing CR visible as the two characters
// "\r" so CRLF drift is readable.
func lines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	s := strings.TrimSuffix(string(b), "\n")
	out := strings.Split(s, "\n")
	for i, l := range out {
		if strings.HasSuffix(l, "\r") {
			out[i] = strings.TrimSuffix(l, "\r") + `\r`
		}
	}
	return out
}

// lcsDiff returns " ", "-" and "+" prefixed lines for a to c.
func lcsDiff(a, c []string) []string {
	const limit = 2000
	if len(a) > limit || len(c) > limit {
		out := make([]string, 0, len(a)+len(c))
		for _, l := range a {
			out = append(out, "-"+l)
		}
		for _, l := range c {
			out = append(out, "+"+l)
		}
		return out
	}
	n, m := len(a), len(c)
	t := make([][]int, n+1)
	for i := range t {
		t[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case a[i] == c[j]:
				t[i][j] = t[i+1][j+1] + 1
			case t[i+1][j] >= t[i][j+1]:
				t[i][j] = t[i+1][j]
			default:
				t[i][j] = t[i][j+1]
			}
		}
	}
	var out []string
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == c[j]:
			out = append(out, " "+a[i])
			i++
			j++
		case t[i+1][j] >= t[i][j+1]:
			out = append(out, "-"+a[i])
			i++
		default:
			out = append(out, "+"+c[j])
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, "-"+a[i])
	}
	for ; j < m; j++ {
		out = append(out, "+"+c[j])
	}
	return out
}
