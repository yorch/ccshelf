package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ccshelf/ccshelf/internal/config"
)

// Kind is the origin trust level of a profile. The zero value is KindInvalid
// on purpose: a Source that forgets to set its kind must not be treated as
// personal, the most trusted kind (B8).
type Kind int

// Kinds, from most to least trusted by the user.
const (
	// KindInvalid is the zero value. Resolve and List reject sources of this
	// kind (and of any kind not listed here).
	KindInvalid Kind = iota
	// KindPersonal profiles come from the user's own directory.
	KindPersonal
	// KindProject profiles come from a repository's .ccshelf folder and are
	// only loaded after explicit per-repo trust (SR2).
	KindProject
	// KindOrg profiles come from any shared source: a git repo, a plugin or an
	// org directory.
	KindOrg
)

// Valid reports whether k is one of the three real kinds.
func (k Kind) Valid() bool { return k >= KindPersonal && k <= KindOrg }

// String returns "personal", "project", "org" or "invalid".
func (k Kind) String() string {
	switch k {
	case KindInvalid:
		return "invalid"
	case KindPersonal:
		return "personal"
	case KindProject:
		return "project"
	case KindOrg:
		return "org"
	}
	return fmt.Sprintf("kind(%d)", int(k))
}

// Source is a place profiles come from. Implementations for git and plugin
// sources live elsewhere; they must keep these contracts:
//
//   - ID is stable and unique per source: "dir:/abs/path" for directories,
//     "git:<url>@<ref>" for git. Directory ids contain machine paths, so the
//     closure never uses them verbatim (see Closure).
//   - Root is an explicit, absolute directory that the source owns (never the
//     user's home directory or the file system root). It contains profiles/
//     and optionally mcp/registry.toml and prompts/. The only things read
//     below Root, besides profiles, are prompts/<file> and mcp/registry.toml;
//     no component of those paths may start with "." or be a symlink. "" means
//     the source has no usable root (not prepared, or refused).
//   - Names lists the profile names present (base names without ".toml"),
//     sorted; a missing directory is not an error. It need not validate them.
//   - Open reads and fully validates one profile and returns an error that
//     wraps *ValidationError when it is invalid. It must reject names that are
//     not valid profile names before touching the file system.
//   - Commit is the resolved commit SHA for git-backed sources, "" otherwise.
//   - Implementations must be safe for sequential use; no concurrency needed.
type Source interface {
	ID() string
	Kind() Kind
	Root() string
	Names() ([]string, error)
	Open(name string) (*File, error)
	Commit() string
}

// File is one loaded profile.
type File struct {
	Name     string
	Path     string
	Raw      []byte
	Manifest *Manifest
	Source   Source
}

// PersonalDir returns the personal profiles directory: config.Dir()/profiles
// (see package config for the base directory).
func PersonalDir() (string, error) {
	d, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "profiles"), nil
}

type dirSource struct {
	kind Kind
	dir  string // absolute profiles directory
	root string // absolute source root; "" when refused
	rel  string // profiles directory below root, slash separated; "" when it is root
	aux  bool   // prompts/ and mcp/registry.toml are available
	err  error  // why the source is unusable, if it is
}

// DirSource returns a Source for a local profiles folder. profilesDir is made
// absolute but need not exist yet.
//
// Root is the parent of profilesDir only when profilesDir is named "profiles"
// (so registry and prompts sit next to it, as in an org data repository).
// Otherwise Root is profilesDir itself: profiles are read from it directly and
// the source has no prompts and no MCP registry, because there is no way to
// tell which sibling folders belong to the source. Every use of a source whose
// root is the user's home directory, the file system root or (for a folder
// not named "profiles") the ccshelf config directory fails with an error
// (B2). Symlinks are never followed below the root (B7).
func DirSource(kind Kind, profilesDir string) Source {
	abs, err := filepath.Abs(profilesDir)
	if err != nil {
		abs = filepath.Clean(profilesDir)
	}
	d := &dirSource{kind: kind, dir: abs, root: abs}
	if filepath.Base(abs) == "profiles" {
		d.root, d.rel, d.aux = filepath.Dir(abs), "profiles", true
	}
	if err := checkRootDir(d.root); err != nil {
		d.err = err
	} else if !d.aux {
		if cfg, cerr := config.Dir(); cerr == nil && sameDir(cfg, d.root) {
			d.err = fmt.Errorf("%w: %s is the ccshelf config directory and cannot be used as a bare profiles folder", ErrPath, d.root)
		}
	}
	if d.err != nil {
		d.root = ""
	}
	return d
}

// checkRootDir refuses roots that are too broad to own: relative paths, the
// file system root and the user's home directory.
func checkRootDir(root string) error {
	switch {
	case root == "":
		return fmt.Errorf("%w: the source has no root directory", ErrPath)
	case !filepath.IsAbs(root):
		return fmt.Errorf("%w: source root %q is not absolute", ErrPath, root)
	case filepath.Dir(root) == root:
		return fmt.Errorf("%w: the file system root cannot be a source root", ErrPath)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && sameDir(home, root) {
		return fmt.Errorf("%w: the home directory cannot be a source root; keep profiles in a folder of their own", ErrPath)
	}
	return nil
}

// sameDir reports whether a and b name the same directory. It compares the
// directories themselves when both exist (which handles symlinks and
// case-insensitive file systems) and falls back to comparing cleaned paths.
func sameDir(a, b string) bool {
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	if errA == nil && errB == nil {
		return os.SameFile(fa, fb)
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// ID returns "dir:" followed by the absolute profiles directory.
func (d *dirSource) ID() string { return "dir:" + d.dir }

// Kind returns the kind the source was created with.
func (d *dirSource) Kind() Kind { return d.kind }

// Root returns the source root, or "" when the source was refused.
func (d *dirSource) Root() string { return d.root }

// Commit returns "": directory sources have no commit.
func (d *dirSource) Commit() string { return "" }

// auxAllowed reports whether prompts/ and mcp/registry.toml exist for this
// source (see DirSource).
func (d *dirSource) auxAllowed() bool { return d.aux }

// Names lists the profile files, or fails when the source was refused.
func (d *dirSource) Names() ([]string, error) {
	if d.err != nil {
		return nil, fmt.Errorf("source %s: %w", d.ID(), d.err)
	}
	ents, err := os.ReadDir(d.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("listing profiles in %s: %w", d.dir, err)
	}
	var names []string
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".toml") || strings.HasPrefix(n, ".") {
			continue
		}
		names = append(names, strings.TrimSuffix(n, ".toml"))
	}
	sort.Strings(names)
	return names, nil
}

// Open reads and validates one profile; symlinks are refused.
func (d *dirSource) Open(name string) (*File, error) {
	if !nameRe.MatchString(name) {
		return nil, &ValidationError{File: name + ".toml", Problems: []Problem{{Field: "name", Message: fmt.Sprintf("%q must match %s", name, nameRe)}}}
	}
	if d.err != nil {
		return nil, fmt.Errorf("opening profile %q: source %s: %w", name, d.ID(), d.err)
	}
	rel := name + ".toml"
	if d.rel != "" {
		rel = d.rel + "/" + rel
	}
	raw, _, err := readConfined(d.root, rel, MaxManifestSize)
	if err != nil {
		return nil, fmt.Errorf("opening profile %q in %s: %w", name, d.ID(), err)
	}
	m, err := Parse(raw, name+".toml")
	if err != nil {
		return nil, fmt.Errorf("profile %q in %s: %w", name, d.ID(), err)
	}
	return &File{Name: name, Path: filepath.Join(d.dir, name+".toml"), Raw: raw, Manifest: m, Source: d}, nil
}
