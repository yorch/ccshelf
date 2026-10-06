package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ccshelf/ccshelf/internal/config"
)

// Kind is the origin trust level of a profile.
type Kind int

// Kinds, from most to least trusted by the user.
const (
	// KindPersonal profiles come from the user's own directory.
	KindPersonal Kind = iota
	// KindProject profiles come from a repository's .ccshelf folder and are
	// only loaded after explicit per-repo trust (SR2).
	KindProject
	// KindOrg profiles come from any shared source: a git repo, a plugin or an
	// org directory.
	KindOrg
)

// String returns "personal", "project" or "org".
func (k Kind) String() string {
	switch k {
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
//   - Root is an absolute directory that contains profiles/ and optionally
//     mcp/registry.toml and prompts/. Every path a profile names (prompt
//     files, the registry) is resolved below Root and confined to it.
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

// PersonalDir returns the personal profiles directory: ConfigDir()/profiles
// (see package config for the base directory).
func PersonalDir() (string, error) {
	d, err := config.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "profiles"), nil
}

type dirSource struct {
	kind Kind
	dir  string // absolute profiles directory
}

// DirSource returns a Source for a local profiles folder. Root is the parent
// of profilesDir (so registry and prompts sit next to it). profilesDir is made
// absolute but need not exist yet. Profile files may be symlinks only when
// they resolve inside profilesDir.
func DirSource(kind Kind, profilesDir string) Source {
	abs, err := filepath.Abs(profilesDir)
	if err != nil {
		abs = filepath.Clean(profilesDir)
	}
	return &dirSource{kind: kind, dir: abs}
}

func (d *dirSource) ID() string     { return "dir:" + d.dir }
func (d *dirSource) Kind() Kind     { return d.kind }
func (d *dirSource) Root() string   { return filepath.Dir(d.dir) }
func (d *dirSource) Commit() string { return "" }

func (d *dirSource) Names() ([]string, error) {
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

func (d *dirSource) Open(name string) (*File, error) {
	if !nameRe.MatchString(name) {
		return nil, &ValidationError{File: name + ".toml", Problems: []Problem{{Field: "name", Message: fmt.Sprintf("%q must match %s", name, nameRe)}}}
	}
	raw, _, err := readConfined(d.dir, name+".toml", MaxManifestSize)
	if err != nil {
		return nil, fmt.Errorf("opening profile %q in %s: %w", name, d.ID(), err)
	}
	m, err := Parse(raw, name+".toml")
	if err != nil {
		return nil, fmt.Errorf("profile %q in %s: %w", name, d.ID(), err)
	}
	return &File{Name: name, Path: filepath.Join(d.dir, name+".toml"), Raw: raw, Manifest: m, Source: d}, nil
}
