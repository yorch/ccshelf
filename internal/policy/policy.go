package policy

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
)

// SourceKind says where a managed document came from.
type SourceKind string

// Source kinds.
const (
	// KindFile is managed-settings.json or managed-mcp.json.
	KindFile SourceKind = "file"
	// KindDropIn is a file in managed-settings.d/.
	KindDropIn SourceKind = "dropin"
	// KindRegistry is a Windows registry value.
	KindRegistry SourceKind = "registry"
	// KindMDM is the macOS managed preferences plist.
	KindMDM SourceKind = "mdm"
)

// Hive names a Windows registry hive that can hold a policy value.
type Hive string

// Registry hives read by Detect.
const (
	HKLM Hive = "HKLM"
	HKCU Hive = "HKCU"
)

// Source is one place Detect looked for managed settings. Only key names are
// recorded, never values.
type Source struct {
	Kind SourceKind `json:"kind"`
	// Location is a file path, a registry location or a preferences domain.
	Location string `json:"location"`
	// Present is true when the source exists (even if it could not be read).
	Present bool `json:"present"`
	// Keys lists the top-level key names the source sets, sorted.
	Keys []string `json:"keys,omitempty"`
	// Used is true when the source contributes to the interpreted policy.
	Used bool `json:"used"`
}

// MarketplaceSource is a redacted marketplace source: the kind
// ("github", "git", "url", "directory", "hostPattern", ...) and a reference
// with credentials, query strings and fragments removed.
type MarketplaceSource struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
}

// ServerRule is a redacted MCP allow or deny entry. Kind is serverName,
// serverUrl or serverCommand. For serverCommand only the executable is kept,
// for serverUrl credentials and queries are removed.
type ServerRule struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// Policy is the interpreted managed policy of this machine. Pointer and
// slice fields are nil when the key is unset, which is different from false
// or an empty list (for example an empty strictKnownMarketplaces blocks every
// marketplace). Values that could be secrets are never stored.
type Policy struct {
	// Sources lists everything that was looked at, in precedence order.
	Sources []Source `json:"sources"`

	// ManagedMCPFile is true when a managed-mcp.json file exists, which
	// puts MCP under exclusive control.
	ManagedMCPFile bool `json:"managed_mcp_file"`
	// ManagedSourcesBehavior is "first-wins" (default) or "merge".
	ManagedSourcesBehavior string `json:"managed_sources_behavior"`

	DisableSideloadFlags            *bool                        `json:"disableSideloadFlags,omitempty"`
	EnabledPlugins                  map[string]bool              `json:"enabledPlugins,omitempty"`
	StrictKnownMarketplaces         []MarketplaceSource          `json:"strictKnownMarketplaces,omitempty"`
	BlockedMarketplaces             []MarketplaceSource          `json:"blockedMarketplaces,omitempty"`
	ExtraKnownMarketplaces          map[string]MarketplaceSource `json:"extraKnownMarketplaces,omitempty"`
	PluginSuggestionMarketplaces    []string                     `json:"pluginSuggestionMarketplaces,omitempty"`
	AllowManagedHooksOnly           *bool                        `json:"allowManagedHooksOnly,omitempty"`
	AllowManagedPermissionRulesOnly *bool                        `json:"allowManagedPermissionRulesOnly,omitempty"`
	AllowManagedMcpServersOnly      *bool                        `json:"allowManagedMcpServersOnly,omitempty"`
	// DisableBypassPermissionsMode is true when permissions.disableBypassPermissionsMode is "disable".
	DisableBypassPermissionsMode *bool        `json:"disableBypassPermissionsMode,omitempty"`
	DeniedMcpServers             []ServerRule `json:"deniedMcpServers,omitempty"`
	AllowedMcpServers            []ServerRule `json:"allowedMcpServers,omitempty"`
	// ManagedMcpServers holds the names of servers provided by managedMcpServers.
	ManagedMcpServers          []string `json:"managedMcpServers,omitempty"`
	DisableAllHooks            *bool    `json:"disableAllHooks,omitempty"`
	DisableClaudeAiConnectors  *bool    `json:"disableClaudeAiConnectors,omitempty"`
	AllowAllClaudeAiMcps       *bool    `json:"allowAllClaudeAiMcps,omitempty"`
	WSLInheritsWindowsSettings *bool    `json:"wslInheritsWindowsSettings,omitempty"`

	// Other lists the names of keys this version does not interpret (the
	// policy schema evolves). Values are not kept.
	Other []string `json:"other,omitempty"`
	// Warnings are user-visible problems: malformed files, typed keys with
	// the wrong type.
	Warnings []string `json:"warnings,omitempty"`
	// Unknown lists sources that could not be read, or cannot be read from
	// here at all, with the reason.
	Unknown []string `json:"unknown,omitempty"`
	// Unreadable is true when a source that exists could not be read or
	// parsed, so the effective policy is genuinely unknown (not "none").
	Unreadable bool `json:"unreadable"`
}

// Options controls Detect. The zero value reads the real, documented
// locations of the running OS. Every root is injectable for tests.
type Options struct {
	// GOOS overrides runtime.GOOS.
	GOOS string
	// ManagedDir overrides the OS directory that holds managed-settings.json,
	// managed-settings.d/ and managed-mcp.json.
	ManagedDir string
	// WindowsDir overrides where the Windows policy folder
	// (C:\Program Files\ClaudeCode) is visible from WSL
	// (default /mnt/c/Program Files/ClaudeCode).
	WindowsDir string
	// WSL overrides WSL detection on Linux.
	WSL *bool
	// PlistPath overrides the macOS managed preferences file.
	PlistPath string
	// ConvertPlist converts a plist to JSON. It must return an error
	// satisfying errors.Is(err, fs.ErrNotExist) when the file is absent. The
	// default runs /usr/bin/plutil with a 5 second timeout.
	ConvertPlist func(ctx context.Context, path string) ([]byte, error)
	// ReadRegistry returns the REG_SZ value "Settings" under
	// SOFTWARE\Policies\ClaudeCode in the hive, or an error satisfying
	// errors.Is(err, fs.ErrNotExist) when the key or value is absent. The
	// default reads the real registry on Windows and reports "unsupported"
	// elsewhere.
	ReadRegistry func(h Hive) (string, error)
}

// Documented locations. See doc.go for the verification sources.
const (
	macDir        = "/Library/Application Support/ClaudeCode"
	linuxDir      = "/etc/claude-code"
	windowsDir    = `C:\Program Files\ClaudeCode`
	wslWindowsDir = "/mnt/c/Program Files/ClaudeCode"
	macPlist      = "/Library/Managed Preferences/com.anthropic.claudecode.plist"
	plistDomain   = "com.anthropic.claudecode"
	regKey        = `SOFTWARE\Policies\ClaudeCode`
	regValue      = "Settings"
)

// controlKeys do not count as policy keys.
var controlKeys = map[string]bool{"wslInheritsWindowsSettings": true, "managedSourcesBehavior": true}

// tier is one ranked managed document (possibly several merged files).
type tier struct {
	name       string
	admin      bool
	m          map[string]any
	present    bool // exists
	unreadable bool
	srcs       []int // indexes into Policy.Sources
}

func (t *tier) hasPolicyKey() bool {
	for k, v := range t.m {
		if !controlKeys[k] && v != nil {
			return true
		}
	}
	return false
}

// adminPresent reports the "present admin document" rule: it sets a policy
// key, or it exists but cannot be read.
func (t *tier) adminPresent() bool { return t.hasPolicyKey() || t.unreadable }

func (o Options) goos() string {
	if o.GOOS != "" {
		return o.GOOS
	}
	return runtime.GOOS
}

func (o Options) isWSL(goos string) bool {
	if o.WSL != nil {
		return *o.WSL && goos == "linux"
	}
	if goos != "linux" || runtime.GOOS != "linux" {
		return false
	}
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	return err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft")
}

// Detect reads the documented managed-settings sources, read-only, and
// returns the interpreted policy. It never runs claude, never probes by
// trial and never fails because a source is unreadable: that is reported in
// Policy.Unknown and Policy.Warnings. The error is reserved for a canceled
// context.
func Detect(ctx context.Context, opt Options) (*Policy, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	goos := opt.goos()
	p := &Policy{ManagedSourcesBehavior: "first-wins"}
	d := &detector{ctx: ctx, opt: opt, p: p}

	var tiers []*tier
	wsl := opt.isWSL(goos)
	switch goos {
	case "darwin":
		tiers = append(tiers, d.plist(), d.files(orDefault(opt.ManagedDir, macDir), "managed settings file"))
	case "windows":
		tiers = append(tiers, d.registry(HKLM, true), d.files(orDefault(opt.ManagedDir, windowsDir), "managed settings file"), d.registry(HKCU, false))
	default:
		lin := d.files(orDefault(opt.ManagedDir, linuxDir), "managed settings file")
		tiers = append(tiers, lin)
		if wsl {
			tiers = d.wsl(lin)
		}
	}
	p.Unknown = append(p.Unknown, "server-managed settings (claude.ai admin console or a Claude apps gateway) cannot be read locally; if your organization uses them they rank above every source listed here")
	d.combine(tiers)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sort.Strings(p.Other)
	p.Other = dedupe(p.Other)
	return p, nil
}

func orDefault(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

type detector struct {
	ctx context.Context
	opt Options
	p   *Policy
}

func (d *detector) addSource(s Source) int {
	d.p.Sources = append(d.p.Sources, s)
	return len(d.p.Sources) - 1
}

// unreadable records a source that exists but cannot be used.
func (d *detector) unreadable(t *tier, what, reason string) {
	t.present, t.unreadable = true, true
	d.p.Unreadable = true
	d.p.Unknown = append(d.p.Unknown, what+": "+reason)
}

// wsl builds the WSL tier chain: the Windows policy folder first when
// wslInheritsWindowsSettings is on, then /etc/claude-code only when no
// Windows admin document is present.
func (d *detector) wsl(lin *tier) []*tier {
	dir := orDefault(d.opt.WindowsDir, wslWindowsDir)
	win := d.files(dir, "Windows managed settings file (via WSL)")
	d.p.Unknown = append(d.p.Unknown, "Windows registry (HKLM, HKCU) is not readable from WSL; a policy delivered only there is not visible")
	inherit := false
	if v, ok := win.m["wslInheritsWindowsSettings"]; ok {
		b, isBool := toBool(v)
		inherit = !isBool || b // any other value counts as the chain being on
		if isBool {
			d.p.WSLInheritsWindowsSettings = &b
		}
	}
	if !inherit {
		return []*tier{lin}
	}
	if win.adminPresent() {
		return []*tier{win}
	}
	return []*tier{win, lin}
}

// combine selects the contributing tiers by the documented precedence and
// interprets the combined document.
func (d *detector) combine(tiers []*tier) {
	var admins []*tier
	var hkcu *tier
	for _, t := range tiers {
		if t.admin {
			admins = append(admins, t)
		} else {
			hkcu = t
		}
	}
	// Highest-ranked admin tier carrying the key or a policy key decides.
	behavior := "first-wins"
	for _, t := range admins {
		if v, ok := t.m["managedSourcesBehavior"]; ok || t.hasPolicyKey() {
			if s, isStr := v.(string); isStr && s == "merge" {
				behavior = "merge"
			}
			break
		}
	}
	d.p.ManagedSourcesBehavior = behavior

	var using []*tier
	for _, t := range admins {
		if t.hasPolicyKey() {
			using = append(using, t)
			if behavior == "first-wins" {
				break
			}
		}
	}
	anyAdminPresent := false
	for _, t := range admins {
		anyAdminPresent = anyAdminPresent || t.adminPresent()
	}
	if len(using) == 0 && !anyAdminPresent && hkcu != nil && hkcu.hasPolicyKey() {
		using = []*tier{hkcu}
	}
	doc := effective(using, admins, behavior)
	for _, t := range using {
		for _, i := range t.srcs {
			d.p.Sources[i].Used = true
		}
	}
	d.interpret(doc)
}

func dedupe(s []string) []string { return slices.Compact(s) }

// isConfined reports whether target (after resolving symlinks) is inside dir.
func isConfined(dir, target string) bool {
	rd, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	rt, err := filepath.EvalSymlinks(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rd, rt)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func isNotExist(err error) bool { return errors.Is(err, fs.ErrNotExist) }
