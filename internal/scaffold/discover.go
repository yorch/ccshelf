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

	"github.com/yorch/ccshelf/internal/catalog/safepath"
	"github.com/yorch/ccshelf/internal/marketplace"
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

// windowsReserved are the device names Windows will not use as a file name,
// with or without an extension, in any letter case.
var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// sourceSegRe is a path segment of a plugin source that is written into a
// CODEOWNERS pattern: no space, no #, no glob or escape character, no leading
// ! or control character can match.
var sourceSegRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// safeSourceDir reports whether dir, the cleaned local source of a
// marketplace entry, may be written into a CODEOWNERS pattern: a relative
// path (safepath) of plain segments that stays below the repository.
func safeSourceDir(dir string) bool {
	if dir == "" || len(dir) > 300 || safepath.CheckRel(dir) != nil || strings.HasPrefix(dir, "!") {
		return false
	}
	for _, seg := range strings.Split(dir, "/") {
		if !sourceSegRe.MatchString(seg) || strings.Trim(seg, ".") == "" || strings.EqualFold(seg, ".git") {
			return false
		}
	}
	return true
}

// portableName reports whether name works as a file name on every platform
// this tool supports: a plugin name (pluginNameRe), not a Windows device
// name (CON, NUL, COM1 and so on, with or without an extension) and without a
// trailing dot. why says what is wrong when ok is false.
func portableName(name string) (ok bool, why string) {
	switch {
	case !pluginNameRe.MatchString(name):
		return false, "its name is not a plugin name"
	case strings.HasSuffix(name, "."):
		return false, "a name ending in a dot is not a valid file name on Windows"
	}
	stem, _, _ := strings.Cut(name, ".")
	if windowsReserved[strings.ToUpper(stem)] {
		return false, "its name is a reserved device name on Windows"
	}
	return true, ""
}

// pathCollision returns the first pair of paths that differ only in letter
// case (they would be one file on Windows and on the default macOS file
// system), or "" when there is none.
func pathCollision(paths []string) string {
	seen := map[string]string{}
	for _, p := range paths {
		k := strings.ToLower(p)
		if prev, ok := seen[k]; ok {
			return fmt.Sprintf("%s and %s", prev, p)
		}
		seen[k] = p
	}
	return ""
}

// manifest is the part of plugin.json read here.
type manifest struct {
	Name        string
	Description string
	Author      json.RawMessage
}

// stringField decodes a JSON string value; ok is false for anything else.
func stringField(raw json.RawMessage) (string, bool) {
	var s string
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
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
		return nil, []string{fmt.Sprintf("ccshelf could not read %s, so it found no plugins: %s", pluginsDir, ui.SanitizeLine(err.Error()))}
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
		}
		if ok, why := portableName(n); !ok {
			notes = append(notes, fmt.Sprintf("%s/%s was skipped: %s", pluginsDir, ui.SanitizeLine(n), why))
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
		fields, err := marketplace.ExactKeys(data)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s was skipped: %s", mf, ui.SanitizeLine(err.Error())))
			continue
		}
		var m manifest
		var nameOK, descOK bool
		m.Name, nameOK = stringField(fields["name"])
		m.Description, descOK = stringField(fields["description"])
		if _, has := fields["name"]; has && !nameOK {
			notes = append(notes, fmt.Sprintf("%s was skipped: name is not a string", mf))
			continue
		}
		if _, has := fields["description"]; has && !descOK {
			notes = append(notes, fmt.Sprintf("%s was skipped: description is not a string", mf))
			continue
		}
		m.Author = fields["author"]
		fp := foundPlugin{Name: n, Dir: pluginsDir + "/" + n, Description: cleanText(m.Description, 500), Author: authorName(m.Author)}
		if ok, _ := portableName(m.Name); ok {
			fp.Name = m.Name
		}
		if other, dup := names[strings.ToLower(fp.Name)]; dup {
			notes = append(notes, fmt.Sprintf("%s was skipped: the plugin name %q is already used by %s (names that differ only in letter case collide on Windows and macOS)", fp.Dir, fp.Name, other))
			continue
		}
		names[strings.ToLower(fp.Name)] = fp.Dir
		found = append(found, fp)
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Dir < found[j].Dir })
	return found, notes
}
