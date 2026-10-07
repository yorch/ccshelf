package scaffold

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/yorch/ccshelf/internal/ui"
)

// Paths of the files this package knows about.
const (
	pathMarketplace = ".claude-plugin/marketplace.json"
	pathConfig      = "ccshelf.toml"
	pathCodeowners  = ".github/CODEOWNERS"
	pathReadme      = "README.md"
	pathAttributes  = ".gitattributes"
	pathIgnore      = ".gitignore"
	sidecarDir      = "catalog/plugins"
	pluginsDir      = "plugins"
	bundlesDir      = "bundles"
	bundlePrefix    = "profile-"
	// SuggestionSuffix is appended to the name of a needs-merge file when
	// suggestions are written.
	SuggestionSuffix = ".ccshelf-suggested"
	// BackupSuffix is appended to a file that --force replaces.
	BackupSuffix = ".bak"
)

// Size limits of the files read from an existing repository.
const (
	maxReadSize     = 1 << 20
	maxManifestSize = 256 << 10
)

// pluginNameRe is the shape of a plugin name this package accepts as a
// sidecar file name and as a directory under plugins/. It is the sidecar
// loader's rule, tightened to a length.
var pluginNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// foundPlugin is a plugin directory discovered under plugins/.
type foundPlugin struct {
	// Name is the marketplace name: plugin.json's name when valid, else the
	// directory name.
	Name string
	// Dir is the directory below the repository root, "plugins/<dir>".
	Dir         string
	Description string
	Author      string
}

// manifest is the part of plugin.json read here.
type manifest struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Author      json.RawMessage `json:"author"`
}

// authorName returns the author's name from the string or object form, or "".
func authorName(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return cleanText(s, 100)
	}
	var o struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &o) == nil {
		return cleanText(o.Name, 100)
	}
	return ""
}

// cleanText returns s trimmed when it is usable as one line of at most max
// characters, else "".
func cleanText(s string, limit int) string {
	s = strings.TrimSpace(s)
	if s == "" || utf8.RuneCountInString(s) > limit || ui.HasControl(s) {
		return ""
	}
	return s
}

// discoverPlugins lists the plugin directories under plugins/ that hold a
// .claude-plugin/plugin.json. Anything else (files, symbolic links, names that
// are not plugin names, unreadable or malformed manifests) is skipped with a
// note. The result is sorted by directory.
func discoverPlugins(fsys FS) (found []foundPlugin, notes []string) {
	ents, err := fsys.ReadDir(pluginsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, []string{fmt.Sprintf("%s could not be read, so no plugins were discovered: %s", pluginsDir, ui.SanitizeLine(err.Error()))}
	}
	names := map[string]string{}
	for _, e := range ents {
		n := e.Name()
		switch {
		case e.Type()&fs.ModeSymlink != 0:
			notes = append(notes, fmt.Sprintf("%s/%s is a symbolic link and was skipped", pluginsDir, ui.SanitizeLine(n)))
			continue
		case !e.IsDir():
			continue
		case !pluginNameRe.MatchString(n):
			notes = append(notes, fmt.Sprintf("%s/%s was skipped: its name is not a plugin name", pluginsDir, ui.SanitizeLine(n)))
			continue
		}
		mf := pluginsDir + "/" + n + "/.claude-plugin/plugin.json"
		data, err := fsys.ReadFile(mf, maxManifestSize)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s was skipped: %s", mf, ui.SanitizeLine(err.Error())))
			continue
		}
		var m manifest
		if err := json.Unmarshal(data, &m); err != nil {
			notes = append(notes, fmt.Sprintf("%s was skipped: it is not valid JSON for a plugin manifest", mf))
			continue
		}
		fp := foundPlugin{Name: n, Dir: pluginsDir + "/" + n, Description: cleanText(m.Description, 500), Author: authorName(m.Author)}
		if m.Name != "" && pluginNameRe.MatchString(m.Name) {
			fp.Name = m.Name
		}
		if other, dup := names[fp.Name]; dup {
			notes = append(notes, fmt.Sprintf("%s was skipped: the plugin name %q is already used by %s", fp.Dir, fp.Name, other))
			continue
		}
		names[fp.Name] = fp.Dir
		found = append(found, fp)
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Dir < found[j].Dir })
	return found, notes
}
