package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// appDirName is the directory below the user's config base that ccshelf owns.
const appDirName = "ccshelf"

// Dir returns the directory holding ccshelf's configuration files.
func Dir() (string, error) {
	home, _ := os.UserHomeDir()
	return configDirFor(runtime.GOOS, os.Getenv, home)
}

func configDirFor(goos string, getenv func(string) string, home string) (string, error) {
	if goos == "windows" {
		if v := getenv("APPDATA"); v != "" && filepath.IsAbs(v) {
			return filepath.Join(v, appDirName), nil
		}
		if home == "" {
			return "", errors.New("cannot locate the config directory: APPDATA and the home directory are unset")
		}
		return filepath.Join(home, "AppData", "Roaming", appDirName), nil
	}
	if v := getenv("XDG_CONFIG_HOME"); v != "" && filepath.IsAbs(v) {
		return filepath.Join(v, appDirName), nil
	}
	if home == "" {
		return "", errors.New("cannot locate the config directory: XDG_CONFIG_HOME and HOME are unset")
	}
	return filepath.Join(home, ".config", appDirName), nil
}

// Path returns the location of config.toml.
func Path() (string, error) { return inConfigDir("config.toml") }

// LockfilePath returns the location of the trust lockfile (lock.json).
// This package never reads or writes it. The function exists so that later
// packages agree on one place.
func LockfilePath() (string, error) { return inConfigDir("lock.json") }

// ProjectTrustPath returns the location of the per-repository trust records
// (project-trust.json). Like LockfilePath it only names the file.
func ProjectTrustPath() (string, error) { return inConfigDir("project-trust.json") }

func inConfigDir(name string) (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, name), nil
}

// DefaultClaudeDir returns Claude Code's default configuration directory
// (~/.claude). ccshelf uses it only for comparison and reads nothing in it.
func DefaultClaudeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating the home directory: %w", err)
	}
	return filepath.Join(home, ".claude"), nil
}

// ExpandPath expands a leading "~" (alone or followed by a path separator)
// and $VAR or ${VAR} references. ExpandPath returns an error, not an empty
// string, for an unset variable, an unterminated "${" or a "$" that does not
// start a reference. Thus a typo cannot silently turn a path into "/" or a
// relative path.
func ExpandPath(p string) (string, error) {
	home, _ := os.UserHomeDir()
	return expandPath(p, os.Getenv, home)
}

func expandPath(p string, getenv func(string) string, home string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home == "" {
			return "", fmt.Errorf("expanding %q: the home directory is unknown", p)
		}
		p = home + p[1:]
	}
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c != '$' {
			b.WriteByte(c)
			continue
		}
		rest := p[i+1:]
		var name string
		switch {
		case strings.HasPrefix(rest, "{"):
			end := strings.IndexByte(rest, '}')
			if end < 0 {
				return "", fmt.Errorf("expanding %q: unterminated ${", p)
			}
			name = rest[1:end]
			i += end + 1
		default:
			n := 0
			for n < len(rest) && isNameByte(rest[n], n == 0) {
				n++
			}
			name = rest[:n]
			i += n
		}
		if name == "" || !validVarName(name) {
			return "", fmt.Errorf("expanding %q: invalid variable reference", p)
		}
		v := getenv(name)
		if v == "" {
			return "", fmt.Errorf("expanding %q: environment variable %s is not set", p, name)
		}
		b.WriteString(v)
	}
	return b.String(), nil
}

func isNameByte(c byte, first bool) bool {
	if c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
		return true
	}
	return !first && c >= '0' && c <= '9'
}

func validVarName(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isNameByte(s[i], i == 0) {
			return false
		}
	}
	return s != ""
}
