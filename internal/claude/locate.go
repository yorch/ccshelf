package claude

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// EnvBinary is the environment variable that overrides where claude lives.
const EnvBinary = "CCSHELF_CLAUDE"

// ErrNotFound is matched (errors.Is) by the error Locate returns when no
// claude binary can be found.
var ErrNotFound = errors.New("claude binary not found")

// ErrShimOnly is matched (errors.Is) by [ShimOnlyError].
var ErrShimOnly = errors.New("only a .cmd or .bat shim of claude was found")

// ShimOnlyError is returned on Windows when the only claude found is a
// .cmd or .bat shim (for example from an npm install). Go refuses to pass
// arbitrary arguments through cmd.exe, and ccshelf never builds cmd.exe
// command lines, so the user must point CCSHELF_CLAUDE at claude.exe.
type ShimOnlyError struct {
	// Path is the shim that was found.
	Path string
}

// Error implements error.
func (e *ShimOnlyError) Error() string {
	return fmt.Sprintf("found %s, which is a .cmd/.bat shim; ccshelf cannot run those safely. Set %s to the full path of claude.exe (the native installer puts it in %%USERPROFILE%%\\.local\\bin)", e.Path, EnvBinary)
}

// Is makes errors.Is(err, ErrShimOnly) work.
func (e *ShimOnlyError) Is(target error) bool { return target == ErrShimOnly }

// locator holds the environment Locate depends on, so tests can fake it.
type locator struct {
	goos     string
	getenv   func(string) string
	lookPath func(string) (string, error)
	home     func() (string, error)
}

func systemLocator() locator {
	return locator{runtime.GOOS, os.Getenv, exec.LookPath, os.UserHomeDir}
}

// Locate finds the claude binary. Order: the explicit override, the
// CCSHELF_CLAUDE environment variable, a PATH lookup, then
// ~/.local/bin/claude (claude.exe on Windows). On Windows claude.exe is
// preferred, and a lone .cmd/.bat shim yields a [ShimOnlyError].
func Locate(override string) (string, error) {
	return systemLocator().locate(override)
}

func isShim(goos, p string) bool {
	if goos != "windows" {
		return false
	}
	switch strings.ToLower(filepath.Ext(p)) {
	case ".cmd", ".bat":
		return true
	}
	return false
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

func (l locator) explicit(src, p string) (string, error) {
	if !strings.ContainsAny(p, `/\`) {
		found, err := l.lookPath(p)
		if err != nil {
			return "", fmt.Errorf("%s %q: %w: %w", src, p, ErrNotFound, err)
		}
		p = found
	}
	if !isFile(p) {
		return "", fmt.Errorf("%s %q: %w: not a file", src, p, ErrNotFound)
	}
	if isShim(l.goos, p) {
		return "", &ShimOnlyError{Path: p}
	}
	return p, nil
}

func (l locator) locate(override string) (string, error) {
	if override != "" {
		return l.explicit("claude override", override)
	}
	if v := l.getenv(EnvBinary); v != "" {
		return l.explicit(EnvBinary, v)
	}
	var shim string
	names := []string{"claude"}
	if l.goos == "windows" {
		names = []string{"claude.exe", "claude"}
	}
	for _, n := range names {
		p, err := l.lookPath(n)
		if err != nil {
			continue
		}
		if isShim(l.goos, p) {
			if shim == "" {
				shim = p
			}
			continue
		}
		return p, nil
	}
	if home, err := l.home(); err == nil && home != "" {
		n := "claude"
		if l.goos == "windows" {
			n = "claude.exe"
		}
		p := filepath.Join(home, ".local", "bin", n)
		if isFile(p) {
			return p, nil
		}
	}
	if shim != "" {
		return "", &ShimOnlyError{Path: shim}
	}
	return "", fmt.Errorf("%w: install Claude Code or set %s", ErrNotFound, EnvBinary)
}
