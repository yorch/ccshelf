package recommend

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Limits of Collect.
const (
	// MaxFiles caps Signals.Files.
	MaxFiles = 2000
	// MaxManifestSize caps the bytes read from one manifest file.
	MaxManifestSize = 64 << 10
	// MaxManifests caps the number of manifest files read.
	MaxManifests = 24
	// MaxDepth is how many directory levels below the start directory the
	// walk enters.
	MaxDepth = 6
	// MaxDirs caps the number of directories read, so a tree of many empty
	// directories cannot keep the walk busy.
	MaxDirs = 10000

	maxEntriesPerDir = 5000
	maxNameLen       = 255
)

// Signals describe a directory. Collect fills it; tests and callers may also
// build one by hand.
type Signals struct {
	// Cwd is the absolute directory.
	Cwd string
	// RepoRoot is the nearest ancestor of Cwd (or Cwd itself) that holds a
	// .git entry, or "" when there is none.
	RepoRoot string
	// Files are slash-separated paths relative to Cwd, sorted, at most
	// MaxFiles of them.
	Files []string
	// Truncated is true when the walk stopped at a cap.
	Truncated bool
	// CLIs are names of executables the files suggest (make, docker,
	// terraform, ...), sorted and unique.
	CLIs []string
	// ManifestFiles holds small manifest files (package.json, go.mod, ...)
	// by relative path, each cut to MaxManifestSize bytes.
	ManifestFiles map[string][]byte
	// Hosts are hostnames the caller knows about. Collect leaves it empty:
	// reading git remotes or other configuration is out of scope.
	Hosts []string
}

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, ".terraform": true,
	".venv": true, "venv": true, "__pycache__": true, ".hg": true, ".svn": true,
}

var manifestNames = map[string]bool{
	"package.json": true, "go.mod": true, "pyproject.toml": true, "requirements.txt": true,
	"cargo.toml": true, "gemfile": true, "pom.xml": true, "build.gradle": true,
	"build.gradle.kts": true, "composer.json": true, "mix.exs": true, "pipfile": true,
	"chart.yaml": true, "pubspec.yaml": true,
}

// cliByName maps lower-cased file names to the CLIs they imply.
var cliByName = map[string][]string{
	"makefile": {"make"}, "gnumakefile": {"make"}, "dockerfile": {"docker"},
	"docker-compose.yml": {"docker"}, "docker-compose.yaml": {"docker"}, "compose.yaml": {"docker"}, "compose.yml": {"docker"},
	"go.mod": {"go"}, "package.json": {"npm", "node"}, "yarn.lock": {"yarn"}, "pnpm-lock.yaml": {"pnpm"},
	"package-lock.json": {"npm"}, "cargo.toml": {"cargo"}, "pyproject.toml": {"python", "pip"},
	"requirements.txt": {"python", "pip"}, "pipfile": {"pipenv", "python"}, "gemfile": {"bundle", "ruby"},
	"pom.xml": {"mvn"}, "build.gradle": {"gradle"}, "build.gradle.kts": {"gradle"},
	"chart.yaml": {"helm"}, "kustomization.yaml": {"kubectl"}, "pulumi.yaml": {"pulumi"},
	"justfile": {"just"}, "flake.nix": {"nix"}, "podfile": {"pod"}, "mix.exs": {"mix"},
	"composer.json": {"composer"}, "skaffold.yaml": {"skaffold"}, "ansible.cfg": {"ansible"},
	".terraform-version": {"terraform"}, "terragrunt.hcl": {"terragrunt", "terraform"},
}

var cliByExt = map[string][]string{
	".tf": {"terraform"}, ".tfvars": {"terraform"}, ".go": {"go"}, ".rs": {"cargo"},
	".sql": {"psql"}, ".sh": {"bash"}, ".bicep": {"az"},
}

// Collect inspects dir with a bounded, breadth-first walk. It never follows
// symlinks, skips .git, node_modules, vendor and similar directories, skips
// entries it cannot read, ignores names with control characters or invalid
// UTF-8, and stops at MaxFiles files, MaxDirs directories and MaxDepth levels. It reads no file
// outside dir and only manifest files at all.
func Collect(ctx context.Context, dir string) (*Signals, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", dir, err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", abs, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", abs)
	}
	sig := &Signals{Cwd: abs, RepoRoot: findRepoRoot(abs), ManifestFiles: map[string][]byte{}}

	type item struct {
		rel   string // slash-separated, "" for the root
		depth int
	}
	cliSet := map[string]bool{}
	queue := []item{{"", 0}}
	manifests, dirs := 0, 0
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(sig.Files) >= MaxFiles || dirs >= MaxDirs {
			sig.Truncated = true
			break
		}
		cur := queue[0]
		queue = queue[1:]
		dirs++
		entries, err := os.ReadDir(filepath.Join(abs, filepath.FromSlash(cur.rel)))
		if err != nil {
			continue // permission errors and races are skipped
		}
		if len(entries) > maxEntriesPerDir {
			entries = entries[:maxEntriesPerDir]
			sig.Truncated = true
		}
		for _, e := range entries {
			name := e.Name()
			if !safeName(name) {
				continue
			}
			rel := path.Join(cur.rel, name)
			switch t := e.Type(); {
			case t&os.ModeSymlink != 0:
				continue
			case t.IsDir():
				if skipDirs[name] {
					continue
				}
				if cur.depth+1 > MaxDepth {
					sig.Truncated = true
					continue
				}
				queue = append(queue, item{rel, cur.depth + 1})
			case t.IsRegular():
				if len(sig.Files) >= MaxFiles {
					sig.Truncated = true
					continue
				}
				sig.Files = append(sig.Files, rel)
				lower := strings.ToLower(name)
				for _, c := range cliByName[lower] {
					cliSet[c] = true
				}
				for _, c := range cliByExt[path.Ext(lower)] {
					cliSet[c] = true
				}
				if manifestNames[lower] && manifests < MaxManifests && cur.depth <= 2 {
					if data, ok := readCapped(filepath.Join(abs, filepath.FromSlash(rel))); ok {
						sig.ManifestFiles[rel] = data
						manifests++
					}
				}
			}
		}
	}
	sort.Strings(sig.Files)
	for c := range cliSet {
		sig.CLIs = append(sig.CLIs, c)
	}
	sort.Strings(sig.CLIs)
	return sig, nil
}

func safeName(name string) bool {
	if name == "" || len(name) > maxNameLen || !utf8.ValidString(name) || strings.Contains(name, "\\") {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			return false
		}
	}
	return true
}

// findRepoRoot walks up from dir looking for a .git entry (a directory, or
// the file of a worktree). It only stats; it never reads .git.
func findRepoRoot(dir string) string {
	for d := dir; ; {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

// readCapped reads at most MaxManifestSize bytes of a regular file.
func readCapped(p string) ([]byte, bool) {
	st, err := os.Lstat(p)
	if err != nil || !st.Mode().IsRegular() {
		return nil, false
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxManifestSize))
	if err != nil {
		return nil, false
	}
	return data, true
}
