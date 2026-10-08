package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// This file holds what "ccshelf config" needs to change an existing
// configuration safely: an allowlist of settings, source identity, the
// definition of a weakening change, and a write that checks the file did not
// change and keeps the previous one as config.toml.bak.

// Setting kinds.
const (
	KindBool     = "bool"
	KindEnum     = "enum"
	KindDuration = "duration"
	KindURL      = "url"
	KindAccount  = "account"
)

// Setting describes one key "ccshelf config set" may change.
type Setting struct {
	// Key is the dotted name, for example trust.on_change.
	Key string
	// Kind is one of the Kind constants.
	Kind string
	// Values lists the accepted values of a bool or enum.
	Values []string
	// Default is shown when the key is unset ("" means no value).
	Default string
	// Help is a one-line description.
	Help string
}

// Settings returns the allowlist of keys "config set" and "config unset"
// accept. Everything else (claude.path, update.base_url,
// update.cosign_identity_repo, update.asset_hosts, accounts, sources) has a
// dedicated command or needs "config edit", because it widens what ccshelf
// trusts or runs.
func Settings() []Setting {
	return []Setting{
		{Key: "trust.on_change", Kind: KindEnum, Values: OnChangeModes(), Default: OnChangePrompt, Help: "what to do when the closure of an accepted profile changes"},
		{Key: "trust.require_pin", Kind: KindBool, Values: []string{"true", "false"}, Default: "true", Help: "refuse git sources that are not pinned to a tag or full commit"},
		{Key: "trust.trust_project_profiles", Kind: KindBool, Values: []string{"true", "false"}, Default: "false", Help: "load project .ccshelf/ profiles once trusted per repository"},
		{Key: "update.mode", Kind: KindEnum, Values: UpdateModes(), Default: UpdateOff, Help: "automatic update behavior"},
		{Key: "update.interval", Kind: KindDuration, Help: "how often an automatic update check may run (default 24h, 1h to 1 year)"},
		{Key: "catalog.remote_url", Kind: KindURL, Help: "published catalog.json for search outside an org repo (https)"},
		{Key: "default_account", Kind: KindAccount, Help: "account used when --account is not given"},
		{Key: "ui.color", Kind: KindEnum, Values: ColorModes(), Default: ColorAuto, Help: "color output"},
		{Key: "ui.interactive", Kind: KindEnum, Values: InteractiveModes(), Default: InteractiveAuto, Help: "prompts and pickers"},
	}
}

// LookupSetting returns the allowlisted setting named key.
func LookupSetting(key string) (Setting, bool) {
	for _, s := range Settings() {
		if s.Key == key {
			return s, true
		}
	}
	return Setting{}, false
}

// SettingKeys returns the allowlisted keys in display order.
func SettingKeys() []string {
	var out []string
	for _, s := range Settings() {
		out = append(out, s.Key)
	}
	return out
}

// GetSetting returns the value of an allowlisted key as text and whether the
// file sets it (an unset value shows the default).
func (c *Config) GetSetting(key string) (value string, set bool) {
	switch key {
	case "trust.on_change":
		return c.Trust.OnChange, c.Trust.OnChange != OnChangePrompt
	case "trust.require_pin":
		return strconv.FormatBool(c.Trust.RequirePin), !c.Trust.RequirePin
	case "trust.trust_project_profiles":
		return strconv.FormatBool(c.Trust.TrustProjectProfiles), c.Trust.TrustProjectProfiles
	case "update.mode":
		return c.Update.EffectiveMode(), c.Update.Mode != ""
	case "update.interval":
		if c.Update.Interval == "" {
			return DefaultUpdateInterval.String(), false
		}
		return c.Update.Interval, true
	case "catalog.remote_url":
		return c.Catalog.RemoteURL, c.Catalog.RemoteURL != ""
	case "default_account":
		return c.DefaultAccount, c.DefaultAccount != ""
	case "ui.color":
		return c.UI.Color, c.UI.Color != ColorAuto
	case "ui.interactive":
		return c.UI.Interactive, c.UI.Interactive != InteractiveAuto
	}
	return "", false
}

// SetSetting changes one allowlisted key. The value is parsed for the key's
// kind; the caller still runs Validate on the result, which adds the
// cross-field rules (for example that default_account names an account).
func (c *Config) SetSetting(key, value string) error {
	st, ok := LookupSetting(key)
	if !ok {
		return fmt.Errorf("%q cannot be changed with config set", key)
	}
	if value == "" {
		return fmt.Errorf("%s: the value is empty (to go back to the default use: ccshelf config unset %s)", key, key)
	}
	switch st.Kind {
	case KindBool, KindEnum:
		if !contains(st.Values, value) {
			return fmt.Errorf("%s: %q is not one of %s", key, value, strings.Join(st.Values, ", "))
		}
	case KindDuration:
		d, err := ParseUpdateInterval(value)
		switch {
		case err != nil:
			return fmt.Errorf("%s: %w", key, err)
		case d < MinUpdateInterval:
			return fmt.Errorf("%s: %q is shorter than the minimum %s", key, value, MinUpdateInterval)
		case d > MaxUpdateInterval:
			return fmt.Errorf("%s: %q is longer than the maximum %s", key, value, MaxUpdateInterval)
		}
	case KindURL:
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return fmt.Errorf("%s: must be an absolute HTTPS URL", key)
		}
		if k := credentialMarker(value); k != "" {
			return fmt.Errorf("%s: looks like it embeds a credential (%s...); never put tokens in the configuration", key, k)
		}
	case KindAccount:
		if !ValidAccountName(value) {
			return fmt.Errorf("%s: %q is not a valid account name", key, value)
		}
	}
	switch key {
	case "trust.on_change":
		c.Trust.OnChange = value
	case "trust.require_pin":
		c.Trust.RequirePin = value == "true"
	case "trust.trust_project_profiles":
		c.Trust.TrustProjectProfiles = value == "true"
	case "update.mode":
		c.Update.Mode = value
	case "update.interval":
		c.Update.Interval = value
	case "catalog.remote_url":
		c.Catalog.RemoteURL = value
	case "default_account":
		c.DefaultAccount = value
	case "ui.color":
		c.UI.Color = value
	case "ui.interactive":
		c.UI.Interactive = value
	}
	return nil
}

// UnsetSetting puts an allowlisted key back to its default.
func (c *Config) UnsetSetting(key string) error {
	st, ok := LookupSetting(key)
	if !ok {
		return fmt.Errorf("%q cannot be changed with config unset", key)
	}
	switch key {
	case "trust.on_change":
		c.Trust.OnChange = OnChangePrompt
	case "trust.require_pin":
		c.Trust.RequirePin = true
	case "trust.trust_project_profiles":
		c.Trust.TrustProjectProfiles = false
	case "update.mode":
		c.Update.Mode = ""
	case "update.interval":
		c.Update.Interval = ""
	case "catalog.remote_url":
		c.Catalog.RemoteURL = ""
	case "default_account":
		c.DefaultAccount = ""
	case "ui.color":
		c.UI.Color = ColorAuto
	case "ui.interactive":
		c.UI.Interactive = InteractiveAuto
	default:
		return fmt.Errorf("%s: no default known", st.Key)
	}
	return nil
}

// SameSource reports whether a and b are the same source: the same type and
// the same repository URL (git), directory (dir) or plugin id (plugin). The
// ref, folder and marketplace are not part of the identity: to change them
// use "config source pin", remove the source, or edit the file.
func SameSource(a, b SourceConfig) bool {
	if a.Type != b.Type {
		return false
	}
	switch a.Type {
	case SourceGit:
		return a.URL == b.URL
	case SourceDir:
		return samePath(a.Path, b.Path)
	case SourcePlugin:
		return a.Plugin == b.Plugin
	}
	return false
}

// SourceLocation is the address of a source for display: the repository URL,
// the directory or the plugin id.
func (s SourceConfig) SourceLocation() string {
	switch s.Type {
	case SourceGit:
		return s.URL
	case SourcePlugin:
		return s.Plugin
	}
	return s.Path
}

// Weakening lists, in words, the security-relevant ways after is weaker than
// before. A change is weakening when it:
//   - turns trust.require_pin off;
//   - turns trust.trust_project_profiles on;
//   - sets update.mode to install (a new binary is installed without a
//     separate step);
//   - adds a git source (or changes the ref of one) so that its ref is not a
//     tag or full commit id, which can only happen while require_pin is off.
//
// Not weakening: going back to a stricter or default value, trust.on_change,
// removing a source, and adding a pinned source (a new source still needs
// trust before any of its profiles run).
func Weakening(before, after *Config) []string {
	var out []string
	if before.Trust.RequirePin && !after.Trust.RequirePin {
		out = append(out, "trust.require_pin is off: git sources no longer have to be pinned to a tag or full commit")
	}
	if !before.Trust.TrustProjectProfiles && after.Trust.TrustProjectProfiles {
		out = append(out, "trust.trust_project_profiles is on: project .ccshelf/ profiles can be loaded once trusted per repository")
	}
	if before.Update.Mode != UpdateInstall && after.Update.Mode == UpdateInstall {
		out = append(out, "update.mode is install: new releases are installed automatically")
	}
	for _, s := range after.Sources {
		if s.Type != SourceGit || ValidatePin(s.Ref) == nil {
			continue
		}
		if containsSource(before.Sources, s) {
			continue
		}
		ref := s.Ref
		if ref == "" {
			ref = "(none)"
		}
		out = append(out, fmt.Sprintf("git source %s has ref %s, which is not a tag or full commit id: it can move without notice", s.URL, ref))
	}
	return out
}

func containsSource(list []SourceConfig, s SourceConfig) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// HasComment reports whether raw contains a TOML comment: a # outside a
// quoted string. Encoding a Config again drops comments and layout.
func HasComment(raw []byte) bool {
	for _, line := range strings.Split(string(raw), "\n") {
		var quote byte
		for i := 0; i < len(line); i++ {
			ch := line[i]
			switch {
			case quote == '"' && ch == '\\':
				i++
			case quote != 0:
				if ch == quote {
					quote = 0
				}
			case ch == '"' || ch == '\'':
				quote = ch
			case ch == '#':
				return true
			}
		}
	}
	return false
}

// ErrChangedWhileEditing is returned by SaveChecked and SaveRawChecked when
// the file no longer holds the bytes the change was based on.
var ErrChangedWhileEditing = errors.New("the configuration file changed while it was being edited")

// BackupPath returns the name of the backup kept next to path.
func BackupPath(path string) string { return path + ".bak" }

// SaveChecked validates cfg and replaces the file at path with it, but only if
// the file still holds exactly loaded (the bytes cfg was derived from). The
// previous file is first copied to BackupPath(path) (0600, replaced
// atomically; a symlink or a non-regular file at either name is refused).
// Encoding cfg drops comments and layout; the backup keeps them. It returns
// the backup path.
//
// The comparison and the rename are two steps, so another writer that wins
// the instant between them is not detected: this guards against a person
// editing the file while a prompt is open, not against a hostile local
// process (which could rewrite the file anyway).
func SaveChecked(path string, cfg *Config, loaded []byte) (string, error) {
	data, err := Encode(cfg)
	if err != nil {
		return "", err
	}
	return replaceChecked(path, loaded, data)
}

// SaveRawChecked is SaveChecked for text a person wrote: raw is parsed and
// validated, and then written byte for byte, so comments are kept.
func SaveRawChecked(path string, raw, loaded []byte) (string, error) {
	if _, err := Parse(raw, path); err != nil {
		return "", err
	}
	return replaceChecked(path, loaded, raw)
}

func replaceChecked(path string, loaded, data []byte) (string, error) {
	fi, err := os.Lstat(path)
	switch {
	case err != nil:
		return "", fmt.Errorf("checking %s: %w", path, err)
	case fi.Mode()&fs.ModeSymlink != 0:
		return "", fmt.Errorf("refusing to replace symlink %s", path)
	case !fi.Mode().IsRegular():
		return "", fmt.Errorf("%s is not a regular file", path)
	}
	current, err := ReadFile(path)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(current, loaded) {
		return "", ErrChangedWhileEditing
	}
	bak := BackupPath(path)
	if fi, err := os.Lstat(bak); err == nil && !fi.Mode().IsRegular() {
		return "", fmt.Errorf("refusing to replace %s: it is not a regular file", bak)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("checking %s: %w", bak, err)
	}
	if err := writeAtomic(bak, current); err != nil {
		return "", fmt.Errorf("keeping the previous configuration as %s: %w", bak, err)
	}
	if err := writeAtomic(path, data); err != nil {
		return "", err
	}
	return bak, nil
}

// CreateExclusive creates a new file next to path (same directory, mode 0600,
// created exclusively so an existing file or symlink is never followed or
// reused) holding data, and returns its name. The name ends in .toml so an
// editor picks the right syntax.
func CreateExclusive(path string, data []byte) (string, error) {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "config-edit-*.toml")
	if err != nil {
		return "", fmt.Errorf("creating a copy to edit in %s: %w", dir, err)
	}
	name := f.Name()
	fail := func(err error) (string, error) {
		f.Close()
		_ = os.Remove(name)
		return "", err
	}
	if err := f.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		return fail(fmt.Errorf("setting permissions on %s: %w", name, err))
	}
	if _, err := f.Write(data); err != nil {
		return fail(fmt.Errorf("writing %s: %w", name, err))
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("closing %s: %w", name, err)
	}
	return name, nil
}

// ValidateSourceFolder reports why p is not an acceptable folder inside a
// repository (the path of a git or plugin source), or nil.
func ValidateSourceFolder(p string) error { return relInside(p) }

// ValidateSource reports why s would be refused as a source of cfg, or nil.
// Only s is checked.
func (c *Config) ValidateSource(s SourceConfig) error {
	probe := *c
	probe.Sources = []SourceConfig{s}
	probe.Accounts = nil
	probe.DefaultAccount = ""
	return probe.Validate()
}
