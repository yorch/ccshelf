package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
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
		{Key: "trust.branch_check_interval", Kind: KindDuration, Help: "how often a run checks a tracked branch for a new commit (default 24h, 1h to 1 year)"},
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
	case "trust.branch_check_interval":
		if c.Trust.BranchCheckInterval == "" {
			return FormatInterval(DefaultBranchCheckInterval), false
		}
		return c.Trust.BranchCheckInterval, true
	case "update.mode":
		return c.Update.EffectiveMode(), c.Update.Mode != ""
	case "update.interval":
		if c.Update.Interval == "" {
			return FormatInterval(DefaultUpdateInterval), false
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

// SetSetting changes one allowlisted key. It parses the value for the key's
// kind. The caller still runs Validate on the result, which adds the
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
			return fmt.Errorf("%s: looks like it embeds a credential (%s...). Never put tokens in the configuration", key, k)
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
	case "trust.branch_check_interval":
		c.Trust.BranchCheckInterval = value
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
	case "trust.branch_check_interval":
		c.Trust.BranchCheckInterval = ""
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

// FormatInterval prints d compactly: 24h, 1h30m, 90s.
func FormatInterval(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// SameSource reports whether a and b are the same source: the same type and
// the same repository (git), directory (dir) or plugin id (plugin). A git
// repository is compared in normalized form (see NormalizeGitURL) together with
// its folder, so two folders of one repository are two sources, while
// https://h/o/r, https://h/o/r/ and https://h/o/r.git are one. The ref and the
// marketplace are not part of the identity: to change them use "config source
// pin", remove the source, or edit the file.
func SameSource(a, b SourceConfig) bool {
	if a.Type != b.Type {
		return false
	}
	switch a.Type {
	case SourceGit:
		return NormalizeGitURL(a.URL) == NormalizeGitURL(b.URL) && path.Clean("/"+a.Path) == path.Clean("/"+b.Path)
	case SourceDir:
		return samePath(a.Path, b.Path)
	case SourcePlugin:
		return a.Plugin == b.Plugin
	}
	return false
}

// NormalizeGitURL returns a form in which spellings of one repository on one
// transport compare equal: the scheme (https only) and host in lower case, no
// trailing "/" and no ".git", and user@host:path equal to ssh://user@host/path.
// An https and an ssh address of one repository stay different.
func NormalizeGitURL(raw string) string {
	trim := func(p string) string {
		p = strings.TrimRight(p, "/")
		return strings.TrimSuffix(p, ".git")
	}
	switch {
	case strings.HasPrefix(raw, "https://"), strings.HasPrefix(raw, "ssh://"):
		u, err := url.Parse(raw)
		if err != nil {
			return raw
		}
		if u.Scheme == "ssh" {
			user := ""
			if u.User != nil {
				user = u.User.Username() + "@"
			}
			return "ssh:" + user + strings.ToLower(u.Hostname()) + "/" + strings.TrimLeft(trim(u.Path), "/")
		}
		return "https:" + strings.ToLower(u.Host) + "/" + strings.TrimLeft(trim(u.Path), "/")
	}
	if i := strings.Index(raw, ":"); i > 0 && strings.Contains(raw[:i], "@") {
		at := strings.LastIndex(raw[:i], "@")
		return "ssh:" + raw[:at+1] + strings.ToLower(raw[at+1:i]) + "/" + strings.TrimLeft(trim(raw[i+1:]), "/")
	}
	return raw
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
//   - turns trust.require_pin off
//   - turns trust.trust_project_profiles on
//   - sets update.mode to install (a new binary is installed without a
//     separate step)
//   - adds a git source (or changes the ref of one) so that its ref is not a
//     tag or full commit id, which can only happen while require_pin is off
//   - adds a git source that tracks a branch, or switches one to a branch (or
//     to another branch). The branch can move without notice, but every new
//     commit still needs trust before it runs (D-54)
//   - adds a dir source whose path starts with $ (ccshelf reads the variable
//     at every run, so what it names can change without a config change)
//   - changes what is trusted to supply code or releases: claude.path,
//     update.base_url, update.cosign_identity_repo, a new update.asset_hosts
//     entry, the marketplace of an existing plugin source, or the URL of an
//     existing git source. "Existing" for a URL means the same position when
//     the number of sources is unchanged (a removal shifts positions, and a
//     removal is not a weakening). This rule is conservative: Weakening also
//     reports an edit that swaps one source for another in one step.
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
	if before.Claude.Path != after.Claude.Path {
		out = append(out, "claude.path changed: ccshelf will start a different claude binary")
	}
	if before.Update.BaseURL != after.Update.BaseURL {
		out = append(out, "update.base_url changed: releases are fetched from a different server")
	}
	if before.Update.CosignIdentityRepo != after.Update.CosignIdentityRepo {
		out = append(out, "update.cosign_identity_repo changed: releases signed by a different repository are accepted")
	}
	for _, h := range after.Update.AssetHosts {
		if !contains(before.Update.AssetHosts, h) {
			out = append(out, fmt.Sprintf("update.asset_hosts gained %s: release downloads may be redirected to it", h))
		}
	}
	for _, s := range after.Sources {
		if s.Type == SourcePlugin {
			for _, b := range before.Sources {
				if SameSource(b, s) && b.Marketplace != s.Marketplace {
					out = append(out, fmt.Sprintf("plugin source %s now expects marketplace %q instead of %q", s.Plugin, s.Marketplace, b.Marketplace))
				}
			}
		}
		if s.Type == SourceDir && strings.HasPrefix(s.Path, "$") && !containsSource(before.Sources, s) {
			out = append(out, fmt.Sprintf("dir source %s starts with a variable, which is read at every run", s.Path))
		}
	}
	if len(before.Sources) == len(after.Sources) {
		for i, s := range after.Sources {
			if b := before.Sources[i]; s.Type == SourceGit && b.Type == SourceGit && NormalizeGitURL(b.URL) != NormalizeGitURL(s.URL) {
				out = append(out, fmt.Sprintf("git source %d now points at %s instead of %s", i+1, s.URL, b.URL))
			}
		}
	}
	for _, s := range after.Sources {
		if s.Type != SourceGit || (s.Branch == "" && ValidatePin(s.Ref) == nil) {
			continue
		}
		if containsSource(before.Sources, s) {
			continue
		}
		if s.Branch != "" {
			out = append(out, BranchWarning(s))
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

// BranchWarning is the warning for a git source that tracks a branch.
func BranchWarning(s SourceConfig) string {
	return fmt.Sprintf("git source %s tracks branch %s. The branch can move without notice, and every new commit still needs trust before it runs", s.URL, s.Branch)
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

// ErrChangedWhileEditing is the error that SaveChecked and SaveRawChecked
// return when the file no longer holds the bytes that the change was based on.
var ErrChangedWhileEditing = errors.New("the configuration file changed while it was being edited")

// BackupPath returns the name of the backup kept next to path.
func BackupPath(path string) string { return path + ".bak" }

// SaveChecked validates cfg and replaces the file at path with it, but only if
// the file still holds exactly loaded (the bytes cfg was derived from). First
// it copies the previous file to BackupPath(path) (0600, replaced
// atomically). It refuses a symlink or a non-regular file at either name.
// Encoding cfg drops comments and layout, but the backup keeps them.
// SaveChecked returns the backup path.
//
// The comparison and the rename are two steps, so SaveChecked does not detect
// another writer that wins the instant between them. This guards against a
// person who edits the file while a prompt is open. It does not guard against
// a hostile local process (which could rewrite the file anyway).
func SaveChecked(path string, cfg *Config, loaded []byte) (string, error) {
	data, err := Encode(cfg)
	if err != nil {
		return "", err
	}
	return replaceChecked(path, loaded, data)
}

// SaveRawChecked is SaveChecked for text that a person wrote. It parses and
// validates raw, and then writes it byte for byte, so it keeps comments.
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

// ValidateSource reports why cfg would refuse s as a source, or nil. It
// checks only s.
func (c *Config) ValidateSource(s SourceConfig) error {
	probe := *c
	probe.Sources = []SourceConfig{s}
	probe.Accounts = nil
	probe.DefaultAccount = ""
	return probe.Validate()
}
