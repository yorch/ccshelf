package claude

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/ccshelf/ccshelf/internal/cache"
)

// DefaultInstalledTTL is how long [InstalledCache] trusts a memoized list.
const DefaultInstalledTTL = 5 * time.Minute

// maxHashedFile is the largest file whose content joins the fingerprint as a
// hash (settings files); larger files (the claude binary) are stamped by size
// and modification time only.
const maxHashedFile = 64 << 10

// Managed-settings locations. These constants mirror internal/policy
// (policy.go: macDir, linuxDir, windowsDir, wslWindowsDir, macPlist); this
// package does not import policy, so keep the two in sync.
const (
	managedMacDir     = "/Library/Application Support/ClaudeCode"
	managedLinuxDir   = "/etc/claude-code"
	managedWindowsDir = `C:\Program Files\ClaudeCode`
	managedWSLDir     = "/mnt/c/Program Files/ClaudeCode"
	managedMacPlist   = "/Library/Managed Preferences/com.anthropic.claudecode.plist"
)

// ManagedLocations returns the managed-settings files and drop-in
// directories that Claude Code reads on goos ("darwin", "linux" or
// "windows"): files are stamped individually, a directory contributes every
// *.json file in it. The Windows registry policy cannot be stamped; the TTL
// bounds how long a change there can go unnoticed.
func ManagedLocations(goos string) (files, dirs []string) {
	dirOf := func(base, sep string) {
		files = append(files, base+sep+"managed-settings.json", base+sep+"managed-mcp.json")
		dirs = append(dirs, base+sep+"managed-settings.d")
	}
	switch goos {
	case "darwin":
		dirOf(managedMacDir, "/")
		files = append(files, managedMacPlist)
	case "windows":
		dirOf(managedWindowsDir, `\`)
	default:
		dirOf(managedLinuxDir, "/")
		dirOf(managedWSLDir, "/")
	}
	return files, dirs
}

// InstalledCache memoizes [ListInstalled] (about a second per call) in the
// cache directory. An entry is valid only while its fingerprint is
// unchanged: working directory, claude binary (path, size, mtime),
// CLAUDE_CONFIG_DIR, the installed-plugins registry, the user settings, the
// project settings and settings.local files and the managed-settings files of
// this OS (see [ManagedLocations]), plus ExtraFiles. Every stamp is the path,
// size, nanosecond modification time and, for files under 64 KiB, a content
// hash, so an edit that keeps size and mtime is still noticed. An entry also
// expires after TTL. Entries carry a checksum, and any doubt (unreadable,
// corrupt, future-dated, mismatching) means a fresh call. Failing to write
// the cache is never an error.
//
// Only the fields the launcher needs are stored: the plugin identity, version,
// scope, paths, enabled flags, the required-by-org marker and the names of
// MCP servers. A plugin read back from the cache has MCPServers values of
// JSON null and no Extra; callers needing those must call [ListInstalled].
type InstalledCache struct {
	// Dir is the cache directory, normally from cache.Dir.
	Dir string
	// TTL defaults to DefaultInstalledTTL.
	TTL time.Duration
	// Now defaults to time.Now; tests replace it.
	Now func() time.Time
	// ExtraFiles are further files whose stamps join the fingerprint.
	ExtraFiles []string
	// ManagedFiles replaces the managed-settings files of the current OS
	// when non-nil (an empty non-nil slice disables them). Tests use it.
	ManagedFiles []string
}

// cachedPlugin is the stored subset of [Plugin].
type cachedPlugin struct {
	ID             string   `json:"id"`
	Version        string   `json:"version,omitempty"`
	Scope          string   `json:"scope,omitempty"`
	InstallPath    string   `json:"installPath,omitempty"`
	InstalledAt    string   `json:"installedAt,omitempty"`
	LastUpdated    string   `json:"lastUpdated,omitempty"`
	Enabled        bool     `json:"enabled,omitempty"`
	ProjectEnabled bool     `json:"projectEnabled,omitempty"`
	RequiredByOrg  bool     `json:"requiredByOrg,omitempty"`
	MCPServers     []string `json:"mcpServers,omitempty"`
}

func toCached(list []Plugin) []cachedPlugin {
	out := make([]cachedPlugin, 0, len(list))
	for _, p := range list {
		cp := cachedPlugin{p.ID, p.Version, p.Scope, p.InstallPath, p.InstalledAt, p.LastUpdated, p.Enabled, p.ProjectEnabled, p.RequiredByOrg, nil}
		for name := range p.MCPServers {
			cp.MCPServers = append(cp.MCPServers, name)
		}
		sort.Strings(cp.MCPServers)
		out = append(out, cp)
	}
	return out
}

func fromCached(list []cachedPlugin) ([]Plugin, bool) {
	out := make([]Plugin, 0, len(list))
	for _, cp := range list {
		if cp.ID == "" {
			return nil, false
		}
		p := Plugin{
			ID: cp.ID, Version: cp.Version, Scope: cp.Scope, InstallPath: cp.InstallPath,
			InstalledAt: cp.InstalledAt, LastUpdated: cp.LastUpdated, Enabled: cp.Enabled,
			ProjectEnabled: cp.ProjectEnabled, RequiredByOrg: cp.RequiredByOrg,
		}
		p.Name, p.Marketplace = SplitID(cp.ID)
		if len(cp.MCPServers) > 0 {
			p.MCPServers = make(map[string]json.RawMessage, len(cp.MCPServers))
			for _, n := range cp.MCPServers {
				p.MCPServers[n] = json.RawMessage("null")
			}
		}
		out = append(out, p)
	}
	return out, true
}

type cacheEntry struct {
	Key   string          `json:"key"`
	Saved int64           `json:"saved"`
	Sum   string          `json:"sum"`
	Raw   json.RawMessage `json:"raw"`
}

func envLookup(env []string, name string) string {
	if env == nil {
		return os.Getenv(name)
	}
	v := ""
	for _, e := range env {
		if k, val, ok := strings.Cut(e, "="); ok && k == name {
			v = val
		}
	}
	return v
}

// fileStamp describes a file for the fingerprint: path, size, nanosecond
// mtime and, for small files, a content hash.
func fileStamp(p string) string {
	fi, err := os.Stat(p)
	if err != nil {
		return p + "|absent"
	}
	stamp := fmt.Sprintf("%s|%d|%d", p, fi.ModTime().UnixNano(), fi.Size())
	if fi.Mode().IsRegular() && fi.Size() <= maxHashedFile {
		if b, err := os.ReadFile(p); err == nil && int64(len(b)) <= maxHashedFile {
			sum := sha256.Sum256(b)
			stamp += "|" + hex.EncodeToString(sum[:8])
		}
	}
	return stamp
}

// dirStamp stamps every *.json file of dir (managed-settings.d).
func dirStamp(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return dir + "|absent"
	}
	parts := []string{dir}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			parts = append(parts, fileStamp(filepath.Join(dir, e.Name())))
		}
	}
	return strings.Join(parts, ";")
}

func (c *InstalledCache) fingerprint(bin, dir string, env []string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	cfg := envLookup(env, "CLAUDE_CONFIG_DIR")
	if cfg == "" {
		home := envLookup(env, "HOME")
		if runtime.GOOS == "windows" {
			home = envLookup(env, "USERPROFILE")
		}
		if home == "" {
			var err error
			if home, err = os.UserHomeDir(); err != nil {
				return "", err
			}
		}
		cfg = filepath.Join(home, ".claude")
	}
	parts := []string{
		"v2", "cwd=" + abs, "cfg=" + cfg, "bin=" + fileStamp(bin),
		fileStamp(filepath.Join(cfg, "plugins", "installed_plugins.json")),
		fileStamp(filepath.Join(cfg, "settings.json")),
		fileStamp(filepath.Join(abs, ".claude", "settings.json")),
		fileStamp(filepath.Join(abs, ".claude", "settings.local.json")),
	}
	managed, managedDirs := c.ManagedFiles, []string(nil)
	if managed == nil {
		managed, managedDirs = ManagedLocations(runtime.GOOS)
	}
	for _, f := range managed {
		parts = append(parts, fileStamp(f))
	}
	for _, d := range managedDirs {
		parts = append(parts, dirStamp(d))
	}
	for _, f := range c.ExtraFiles {
		parts = append(parts, fileStamp(f))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:]), nil
}

func (c *InstalledCache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// List returns the installed plugins as [ListInstalled] would, from the
// cache when it is still valid (cached is then true).
func (c *InstalledCache) List(ctx context.Context, bin, dir string, env []string) (plugins []Plugin, cached bool, err error) {
	key, kerr := c.fingerprint(bin, dir, env)
	if kerr != nil || c.Dir == "" {
		p, err := ListInstalled(ctx, bin, dir, env)
		return p, false, err
	}
	name := "installed-" + key[:32] + ".json"
	if p, ok := c.load(name, key); ok {
		return p, true, nil
	}
	out, err := runList(ctx, bin, dir, env, false)
	if err != nil {
		return nil, false, err
	}
	list, err := parseInstalled(out)
	if err != nil {
		return nil, false, err
	}
	sortPlugins(list)
	if raw, merr := json.Marshal(toCached(list)); merr == nil {
		sum := sha256.Sum256(raw)
		data, merr := json.Marshal(cacheEntry{key, c.now().UnixNano(), hex.EncodeToString(sum[:]), raw})
		if merr == nil {
			_ = cache.WriteReplace(c.Dir, name, data)
		}
	}
	return list, false, nil
}

func (c *InstalledCache) load(name, key string) ([]Plugin, bool) {
	data, err := cache.ReadFile(c.Dir, name)
	if err != nil {
		return nil, false
	}
	var e cacheEntry
	if json.Unmarshal(data, &e) != nil || e.Key != key {
		return nil, false
	}
	ttl := c.TTL
	if ttl <= 0 {
		ttl = DefaultInstalledTTL
	}
	age := c.now().Sub(time.Unix(0, e.Saved))
	if age < 0 || age > ttl {
		return nil, false
	}
	sum := sha256.Sum256(e.Raw)
	if hex.EncodeToString(sum[:]) != e.Sum {
		return nil, false
	}
	var stored []cachedPlugin
	if json.Unmarshal(e.Raw, &stored) != nil || stored == nil {
		return nil, false
	}
	list, ok := fromCached(stored)
	if !ok {
		return nil, false
	}
	sortPlugins(list)
	return list, true
}
