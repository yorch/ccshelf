package claude

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ccshelf/ccshelf/internal/cache"
)

// DefaultInstalledTTL is how long [InstalledCache] trusts a memoized list.
const DefaultInstalledTTL = 5 * time.Minute

// InstalledCache memoizes [ListInstalled] (about a second per call) in the
// cache directory. An entry is valid only while its fingerprint is
// unchanged: working directory, claude binary (path, size, mtime),
// CLAUDE_CONFIG_DIR, and the modification times of the installed-plugins
// registry, the user settings and the project settings files, plus
// ExtraFiles. It also expires after TTL. Entries carry a checksum, and any
// doubt (unreadable, corrupt, future-dated, mismatching) means a fresh call.
// Failing to write the cache is never an error.
type InstalledCache struct {
	// Dir is the cache directory, normally from cache.Dir.
	Dir string
	// TTL defaults to DefaultInstalledTTL.
	TTL time.Duration
	// Now defaults to time.Now; tests replace it.
	Now func() time.Time
	// ExtraFiles are further files (for example managed settings) whose
	// modification times join the fingerprint.
	ExtraFiles []string
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

func fileStamp(p string) string {
	fi, err := os.Stat(p)
	if err != nil {
		return p + "|absent"
	}
	return fmt.Sprintf("%s|%d|%d", p, fi.ModTime().UnixNano(), fi.Size())
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
		"v1", "cwd=" + abs, "cfg=" + cfg, "bin=" + fileStamp(bin),
		fileStamp(filepath.Join(cfg, "plugins", "installed_plugins.json")),
		fileStamp(filepath.Join(cfg, "settings.json")),
		fileStamp(filepath.Join(abs, ".claude", "settings.json")),
		fileStamp(filepath.Join(abs, ".claude", "settings.local.json")),
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
	name := "installed-" + key[:16] + ".json"
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
	var compact bytes.Buffer
	if json.Compact(&compact, out) == nil {
		raw := compact.Bytes()
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
	list, err := parseInstalled(e.Raw)
	if err != nil {
		return nil, false
	}
	sortPlugins(list)
	return list, true
}
