package scaffold

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"
)

// memFS is an in-memory FS for tests. Directories are implicit: a file
// "a/b/c" makes "a" and "a/b" exist. Symbolic links are represented as entries in
// links; nothing follows them.
type memFS struct {
	files map[string][]byte
	dirs  map[string]bool
	links map[string]bool
	modes map[string]fs.FileMode
	// root says whether the target exists at all.
	missing bool
	// failWrite makes WriteNew fail for names with this suffix.
	failWrite string
	writes    []string
}

func newMem(files map[string]string) *memFS {
	m := &memFS{files: map[string][]byte{}, dirs: map[string]bool{}, links: map[string]bool{}, modes: map[string]fs.FileMode{}}
	for n, c := range files {
		m.put(n, c)
	}
	return m
}

func (m *memFS) put(name, content string) {
	m.files[name] = []byte(content)
	m.modes[name] = 0o644
	for d := path.Dir(name); d != "."; d = path.Dir(d) {
		m.dirs[d] = true
	}
}

func (m *memFS) mkdir(name string) {
	m.dirs[name] = true
	for d := path.Dir(name); d != "."; d = path.Dir(d) {
		m.dirs[d] = true
	}
}

func (m *memFS) symlink(name string) {
	m.links[name] = true
	for d := path.Dir(name); d != "."; d = path.Dir(d) {
		m.dirs[d] = true
	}
}

type memInfo struct {
	name string
	mode fs.FileMode
	size int64
}

func (i memInfo) Name() string       { return path.Base(i.name) }
func (i memInfo) Size() int64        { return i.size }
func (i memInfo) Mode() fs.FileMode  { return i.mode }
func (i memInfo) ModTime() time.Time { return time.Time{} }
func (i memInfo) IsDir() bool        { return i.mode.IsDir() }
func (i memInfo) Sys() any           { return nil }

func (m *memFS) ancestors(name string) error {
	for d := path.Dir(name); d != "."; d = path.Dir(d) {
		if m.links[d] {
			return fmt.Errorf("%s: %w", d, ErrSymlink)
		}
	}
	return nil
}

func (m *memFS) Lstat(name string) (fs.FileInfo, error) {
	if err := checkName(name); err != nil {
		return nil, err
	}
	if m.missing {
		return nil, fs.ErrNotExist
	}
	if err := m.ancestors(name); err != nil {
		return nil, err
	}
	switch {
	case name == ".":
		return memInfo{name, fs.ModeDir | 0o755, 0}, nil
	case m.links[name]:
		return memInfo{name, fs.ModeSymlink | 0o777, 0}, nil
	case m.dirs[name]:
		return memInfo{name, fs.ModeDir | 0o755, 0}, nil
	}
	if c, ok := m.files[name]; ok {
		return memInfo{name, m.modes[name], int64(len(c))}, nil
	}
	return nil, fs.ErrNotExist
}

func (m *memFS) ReadFile(name string, limit int64) ([]byte, error) {
	fi, err := m.Lstat(name)
	if err != nil {
		return nil, err
	}
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		return nil, ErrSymlink
	case !fi.Mode().IsRegular():
		return nil, ErrNotRegular
	case fi.Size() > limit:
		return nil, ErrTooLarge
	}
	return append([]byte(nil), m.files[name]...), nil
}

type memEntry struct{ memInfo }

func (e memEntry) Type() fs.FileMode          { return e.mode.Type() }
func (e memEntry) Info() (fs.FileInfo, error) { return e.memInfo, nil }

func (m *memFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if _, err := m.Lstat(name); err != nil {
		return nil, err
	}
	prefix := ""
	if name != "." {
		prefix = name + "/"
	}
	seen := map[string]fs.FileMode{}
	add := func(full string, mode fs.FileMode) {
		rest, ok := strings.CutPrefix(full, prefix)
		if !ok || rest == "" {
			return
		}
		first, more, _ := strings.Cut(rest, "/")
		if more != "" {
			seen[first] = fs.ModeDir | 0o755
			return
		}
		seen[first] = mode
	}
	for f := range m.files {
		add(f, m.modes[f])
	}
	for d := range m.dirs {
		add(d, fs.ModeDir|0o755)
	}
	for l := range m.links {
		add(l, fs.ModeSymlink|0o777)
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]fs.DirEntry, 0, len(names))
	for _, n := range names {
		out = append(out, memEntry{memInfo{prefix + n, seen[n], 0}})
	}
	return out, nil
}

func (m *memFS) MkdirAll(name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	if err := m.ancestors(name + "/x"); err != nil {
		return err
	}
	m.mkdir(name)
	return nil
}

func (m *memFS) WriteNew(name string, data []byte, perm fs.FileMode) error {
	if err := checkName(name); err != nil {
		return err
	}
	if m.failWrite != "" && strings.HasSuffix(name, m.failWrite) {
		return errors.New("disk full")
	}
	if err := m.ancestors(name); err != nil {
		return err
	}
	if _, ok := m.files[name]; ok || m.links[name] || m.dirs[name] {
		return fmt.Errorf("%s: %w", name, fs.ErrExist)
	}
	m.put(name, string(data))
	m.modes[name] = perm
	m.writes = append(m.writes, name)
	return nil
}

func (m *memFS) Replace(name string, data []byte, perm fs.FileMode) error {
	if _, ok := m.files[name]; !ok {
		return fs.ErrNotExist
	}
	m.files[name] = append([]byte(nil), data...)
	m.modes[name] = perm
	m.writes = append(m.writes, name)
	return nil
}

func (m *memFS) Remove(name string) error {
	if _, err := m.Lstat(name); err != nil {
		return err
	}
	if m.links[name] {
		return ErrSymlink
	}
	if _, ok := m.files[name]; ok {
		delete(m.files, name)
		delete(m.modes, name)
		return nil
	}
	prefix := name + "/"
	for f := range m.files {
		if strings.HasPrefix(f, prefix) {
			return errors.New("directory not empty")
		}
	}
	for d := range m.dirs {
		if strings.HasPrefix(d, prefix) {
			return errors.New("directory not empty")
		}
	}
	delete(m.dirs, name)
	return nil
}

func (m *memFS) read(name string) string { return string(m.files[name]) }

func (m *memFS) names() []string {
	var out []string
	for n := range m.files {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (m *memFS) has(name string) bool { _, ok := m.files[name]; return ok }
