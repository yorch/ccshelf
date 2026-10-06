package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// maxDocSize is the largest managed document that is read.
const maxDocSize = 1 << 20

const plutilTimeout = 5 * time.Second

// parseDoc parses a managed document: a JSON object (BOM tolerated). An
// empty document counts as {}.
func parseDoc(data []byte) (map[string]any, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("not valid JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("not valid JSON: trailing data after the top-level value")
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("top level is not a JSON object")
	}
	return m, nil
}

// readFile reads one regular file with the size limit. A symbolic link is
// followed when its target stays inside dir, or, on Unix, when the target is
// a regular file owned by root that no group or other user can write (the
// way configuration management tools usually link policy into place);
// otherwise the source is reported as unreadable with the reason. The
// resolved target must be a regular file (checked before opening, so a FIFO
// or device can never block), the file is opened non-blocking, and the read
// honors the context deadline. The error satisfies fs.ErrNotExist when the
// file is absent.
func (d *detector) readFile(dir, path string) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		target, err := os.Stat(path)
		if err != nil {
			// Not wrapped: a dangling link must not look like an absent file.
			return nil, fmt.Errorf("symbolic link cannot be resolved: %s", err.Error())
		}
		if !target.Mode().IsRegular() {
			return nil, errors.New("symbolic link does not point to a regular file")
		}
		if !isConfined(dir, path) && !d.rootOwned(target) {
			return nil, errors.New("symbolic link leaves the managed directory and its target is not a regular file owned by root that only root can write")
		}
	} else if !fi.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|nonblock, 0) //nolint:gosec // managed policy path, fixed or caller-chosen root
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	type result struct {
		data []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		data, err := io.ReadAll(io.LimitReader(f, maxDocSize+1))
		ch <- result{data, err}
	}()
	select {
	case <-d.ctx.Done():
		return nil, d.ctx.Err() // the deferred Close unblocks the reader
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		if len(r.data) > maxDocSize {
			return nil, fmt.Errorf("larger than the %d byte limit", maxDocSize)
		}
		return r.data, nil
	}
}

// rootOwned reports whether fi is owned by root and not writable by group or
// others. It is false where ownership is unknown (Windows).
func (d *detector) rootOwned(fi fs.FileInfo) bool {
	owner := d.opt.FileOwner
	if owner == nil {
		owner = fileOwner
	}
	uid, ok := owner(fi)
	return ok && uid == 0 && fi.Mode().Perm()&0o022 == 0
}

func keysOf(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func reasonOf(err error) string {
	if errors.Is(err, fs.ErrPermission) {
		return "permission denied"
	}
	return err.Error()
}

// files reads managed-settings.json then managed-settings.d/*.json from dir
// and merges them by the documented rules. It also notes managed-mcp.json.
func (d *detector) files(dir, label string) *tier {
	t := &tier{name: label, admin: true, m: map[string]any{}}
	main := filepath.Join(dir, "managed-settings.json")
	d.fileInto(t, dir, main, KindFile, label)

	dropDir := filepath.Join(dir, "managed-settings.d")
	entries, err := os.ReadDir(dropDir)
	switch {
	case err == nil:
		for _, e := range entries { // sorted by name
			n := e.Name()
			if strings.HasPrefix(n, ".") || !strings.HasSuffix(n, ".json") || e.IsDir() {
				continue
			}
			d.fileInto(t, dir, filepath.Join(dropDir, n), KindDropIn, label+" drop-in")
		}
	case isNotExist(err):
	default:
		i := d.addSource(Source{Kind: KindDropIn, Location: dropDir, Present: true})
		t.srcs = append(t.srcs, i)
		d.unreadable(t, label+" directory "+dropDir, reasonOf(err))
	}

	mcp := filepath.Join(dir, "managed-mcp.json")
	if fi, err := os.Lstat(mcp); err == nil && (fi.Mode().IsRegular() || fi.Mode()&os.ModeSymlink != 0) {
		d.p.ManagedMCPFile = true
		d.addSource(Source{Kind: KindFile, Location: mcp, Present: true, Used: true})
	}
	return t
}

func (d *detector) fileInto(t *tier, dir, path string, kind SourceKind, what string) {
	data, err := d.readFile(dir, path)
	if err != nil {
		if isNotExist(err) {
			if kind == KindFile {
				d.addSource(Source{Kind: kind, Location: path})
			}
			return
		}
		i := d.addSource(Source{Kind: kind, Location: path, Present: true})
		t.srcs = append(t.srcs, i)
		d.unreadable(t, what+" "+path, reasonOf(err))
		if !errors.Is(err, fs.ErrPermission) {
			d.p.Warnings = append(d.p.Warnings, fmt.Sprintf("%s %s could not be read: %s", what, path, reasonOf(err)))
		}
		return
	}
	m, err := parseDoc(data)
	i := d.addSource(Source{Kind: kind, Location: path, Present: true})
	t.srcs = append(t.srcs, i)
	if err != nil {
		d.unreadable(t, what+" "+path, "malformed: "+err.Error())
		d.p.Warnings = append(d.p.Warnings, fmt.Sprintf("%s %s is malformed (%v); the effective policy is unknown, it is NOT treated as no policy", what, path, err))
		return
	}
	t.present = true
	d.p.Sources[i].Keys = keysOf(m)
	d.normalizeDoc(m, what+" "+path)
	mergeInto(t.m, m, true)
}

// plist reads the macOS managed preferences through plutil.
func (d *detector) plist() *tier {
	t := &tier{name: "managed preferences", admin: true, m: map[string]any{}}
	path := orDefault(d.opt.PlistPath, macPlist)
	conv := d.opt.ConvertPlist
	if conv == nil {
		conv = runPlutil
	}
	loc := plistDomain + " (" + path + ")"
	out, err := conv(d.ctx, path)
	if err != nil {
		if isNotExist(err) {
			d.addSource(Source{Kind: KindMDM, Location: loc})
			return t
		}
		i := d.addSource(Source{Kind: KindMDM, Location: loc, Present: true})
		t.srcs = append(t.srcs, i)
		d.unreadable(t, "managed preferences "+plistDomain, reasonOf(err))
		return t
	}
	i := d.addSource(Source{Kind: KindMDM, Location: loc, Present: true})
	t.srcs = append(t.srcs, i)
	if len(out) > maxDocSize {
		d.unreadable(t, "managed preferences "+plistDomain, fmt.Sprintf("larger than the %d byte limit", maxDocSize))
		return t
	}
	m, perr := parseDoc(out)
	if perr != nil {
		d.unreadable(t, "managed preferences "+plistDomain, "malformed: "+perr.Error())
		d.p.Warnings = append(d.p.Warnings, fmt.Sprintf("managed preferences %s are malformed (%v); the effective policy is unknown", plistDomain, perr))
		return t
	}
	t.present, t.m = true, m
	d.p.Sources[i].Keys = keysOf(m)
	d.normalizeDoc(m, "managed preferences "+plistDomain)
	return t
}

// runPlutil converts a plist to JSON with the system plutil.
func runPlutil(ctx context.Context, path string) ([]byte, error) {
	if _, err := os.Lstat(path); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, plutilTimeout)
	defer cancel()
	var out, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "/usr/bin/plutil", "-convert", "json", "-o", "-", path) //nolint:gosec // fixed binary; the path is the managed preferences file
	cmd.Stdout = &limitWriter{w: &out, n: maxDocSize + 1}
	cmd.Stderr = &limitWriter{w: &stderr, n: 4096}
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("plutil failed: %w", err)
	}
	return out.Bytes(), nil
}

// limitWriter discards what exceeds n bytes but reports success, so the
// caller can detect truncation by size.
type limitWriter struct {
	w io.Writer
	n int
}

func (l *limitWriter) Write(p []byte) (int, error) {
	n := len(p)
	if l.n <= 0 {
		return n, nil
	}
	if len(p) > l.n {
		p = p[:l.n]
	}
	l.n -= len(p)
	_, err := l.w.Write(p)
	return n, err
}

// registry reads the Settings value of a hive.
func (d *detector) registry(h Hive, admin bool) *tier {
	t := &tier{name: string(h) + " registry", admin: admin, m: map[string]any{}}
	loc := string(h) + `\` + regKey + `\` + regValue
	read := d.opt.ReadRegistry
	if read == nil {
		read = readRegistry
	}
	val, err := read(h)
	if err != nil {
		if isNotExist(err) {
			d.addSource(Source{Kind: KindRegistry, Location: loc})
			return t
		}
		i := d.addSource(Source{Kind: KindRegistry, Location: loc, Present: true})
		t.srcs = append(t.srcs, i)
		if h == HKCU {
			// A broken user-writable value never blocks Claude Code.
			t.present = true
			d.p.Unknown = append(d.p.Unknown, loc+": "+reasonOf(err))
			return t
		}
		d.unreadable(t, loc, reasonOf(err))
		return t
	}
	i := d.addSource(Source{Kind: KindRegistry, Location: loc, Present: true})
	t.srcs = append(t.srcs, i)
	if strings.TrimSpace(val) == "" {
		if h == HKLM {
			d.unreadable(t, loc, "empty value")
			d.p.Warnings = append(d.p.Warnings, loc+" is empty; the effective policy is unknown")
		}
		t.present = true
		return t
	}
	m, perr := parseDoc([]byte(val))
	if perr != nil {
		if h == HKLM {
			d.unreadable(t, loc, "malformed: "+perr.Error())
		} else {
			d.p.Unknown = append(d.p.Unknown, loc+": malformed: "+perr.Error())
		}
		d.p.Warnings = append(d.p.Warnings, fmt.Sprintf("%s is malformed (%v)", loc, perr))
		t.present = true
		return t
	}
	t.present, t.m = true, m
	d.p.Sources[i].Keys = keysOf(m)
	d.normalizeDoc(m, loc)
	return t
}

// mergeInto merges src into dst by the file and drop-in rules: single values
// are replaced, lists combine without duplicates, nested blocks merge key by
// key, and entries of extraKnownMarketplaces and managedMcpServers replace
// whole.
func mergeInto(dst, src map[string]any, top bool) {
	for k, sv := range src {
		dv, ok := dst[k]
		if !ok || sv == nil {
			dst[k] = sv
			continue
		}
		if top && (k == "extraKnownMarketplaces" || k == "managedMcpServers") {
			dm, dok := dv.(map[string]any)
			sm, sok := sv.(map[string]any)
			if dok && sok {
				for n, e := range sm {
					dm[n] = e
				}
				continue
			}
			dst[k] = sv
			continue
		}
		switch s := sv.(type) {
		case map[string]any:
			if dm, ok := dv.(map[string]any); ok {
				mergeInto(dm, s, false)
				continue
			}
		case []any:
			if dl, ok := dv.([]any); ok {
				dst[k] = unionLists(dl, s)
				continue
			}
		}
		dst[k] = sv
	}
}

func unionLists(a, b []any) []any {
	out := append([]any(nil), a...)
	seen := map[string]bool{}
	for _, v := range a {
		seen[canon(v)] = true
	}
	for _, v := range b {
		if c := canon(v); !seen[c] {
			seen[c] = true
			out = append(out, v)
		}
	}
	return out
}

func canon(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
