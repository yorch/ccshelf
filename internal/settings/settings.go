package settings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/ccshelf/ccshelf/internal/claude"
	"github.com/ccshelf/ccshelf/internal/envpolicy"
)

// Modes for [Spec.Mode].
const (
	ModeAllowOnly = "allow-only"
	ModeAdditive  = "additive"
)

// AllowedKeys is the closed set of top-level keys, sorted.
var AllowedKeys = []string{"deniedMcpServers", "disableClaudeAiConnectors", "enabledPlugins", "env", "model", "skillOverrides"}

// Skill override values Build produces.
const (
	SkillOff      = "off"
	SkillNameOnly = "name-only"
)

var (
	pluginIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9._-]+$`)
	skillPattern    = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)
	skillValues     = map[string]bool{"on": true, "name-only": true, "user-invocable-only": true, "off": true}
)

const (
	maxNameLen  = 256
	maxEnvValue = 4096
)

// Spec is everything Build needs.
type Spec struct {
	// Installed is the output of claude.ListInstalled.
	Installed []claude.Plugin
	// Mode is "allow-only" (default) or "additive".
	Mode string
	// Include lists plugin ids to enable; Exclude plugin ids to always mask.
	Include, Exclude []string
	// Protected lists plugin ids that must never be masked.
	Protected []string
	// OffSkills and NameOnlySkills list standalone skill names.
	OffSkills, NameOnlySkills []string
	// HideConnectors writes disableClaudeAiConnectors: true.
	HideConnectors bool
	// DenyMCP lists full MCP server labels to deny.
	DenyMCP []string
	// Model is written as "model" when non-empty.
	Model string
	// Env is written as "env"; names must pass envpolicy.
	Env map[string]string
	// Profile, when non-empty, adds CCSHELF_PROFILE to env.
	Profile string
}

// DeniedServer is one deniedMcpServers entry.
type DeniedServer struct {
	// ServerName is the full server label.
	ServerName string `json:"serverName"`
}

// Doc is the generated settings document. Field order is the key order of
// the output (alphabetical); empty fields are omitted.
type Doc struct {
	DeniedMcpServers          []DeniedServer    `json:"deniedMcpServers,omitempty"`
	DisableClaudeAiConnectors bool              `json:"disableClaudeAiConnectors,omitempty"`
	EnabledPlugins            map[string]bool   `json:"enabledPlugins,omitempty"`
	Env                       map[string]string `json:"env,omitempty"`
	Model                     string            `json:"model,omitempty"`
	SkillOverrides            map[string]string `json:"skillOverrides,omitempty"`
}

// Result is the outcome of [Build].
type Result struct {
	// Doc is the document to marshal with JSON.
	Doc Doc
	// Masked and Enabled are the plugin ids written false and true.
	Masked, Enabled []string
	// Locked lists installed plugins required by org policy (never written).
	Locked []string
	// Missing lists Include ids that are not installed (never written).
	Missing []string
	// Protected lists installed protected ids that would have been masked.
	Protected []string
	// Warnings are human-readable notes, in deterministic order.
	Warnings []string
}

// JSON marshals Doc deterministically (two-space indent, trailing newline)
// and runs [Validate] on the output.
func (r *Result) JSON() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r.Doc); err != nil {
		return nil, fmt.Errorf("marshal settings: %w", err)
	}
	if err := Validate(buf.Bytes()); err != nil {
		return nil, fmt.Errorf("generated settings are invalid: %w", err)
	}
	return buf.Bytes(), nil
}

func set(list []string) map[string]bool {
	m := make(map[string]bool, len(list))
	for _, s := range list {
		m[s] = true
	}
	return m
}

func sortedUnique(list []string) []string {
	m := set(list)
	out := make([]string, 0, len(m))
	for s := range m {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func checkIDs(what string, ids []string) error {
	for _, id := range ids {
		if !pluginIDPattern.MatchString(id) {
			return fmt.Errorf("%s: %q is not a plugin id of the form name@marketplace", what, id)
		}
	}
	return nil
}

func checkSkills(what string, names []string) error {
	for _, n := range names {
		if len(n) > maxNameLen || !skillPattern.MatchString(n) {
			return fmt.Errorf("%s: skill name %q must match %s", what, n, skillPattern)
		}
	}
	return nil
}

func plainString(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// Build compiles spec into a settings document. See the package comment for
// the semantics. It returns an error for invalid input (bad ids, skills in
// both lists, denied env names) and never produces a key outside the closed
// set.
func Build(spec Spec) (*Result, error) {
	mode := spec.Mode
	if mode == "" {
		mode = ModeAllowOnly
	}
	if mode != ModeAllowOnly && mode != ModeAdditive {
		return nil, fmt.Errorf("unknown mode %q (want %q or %q)", spec.Mode, ModeAllowOnly, ModeAdditive)
	}
	for _, c := range []struct {
		what string
		ids  []string
	}{{"include", spec.Include}, {"exclude", spec.Exclude}, {"protected", spec.Protected}} {
		if err := checkIDs(c.what, c.ids); err != nil {
			return nil, err
		}
	}
	include, exclude, protected := set(spec.Include), set(spec.Exclude), set(spec.Protected)
	for _, id := range sortedUnique(spec.Include) {
		if exclude[id] {
			return nil, fmt.Errorf("plugin %q is in both include and exclude", id)
		}
	}
	res := &Result{}
	doc := &res.Doc

	buildPlugins(spec, mode, include, exclude, protected, res)
	if err := buildSkills(spec, res); err != nil {
		return nil, err
	}
	for _, label := range sortedUnique(spec.DenyMCP) {
		if label == "" || label != strings.TrimSpace(label) || len(label) > maxNameLen || !plainString(label) {
			return nil, fmt.Errorf("deny MCP: invalid server label %q", label)
		}
		doc.DeniedMcpServers = append(doc.DeniedMcpServers, DeniedServer{ServerName: label})
	}
	doc.DisableClaudeAiConnectors = spec.HideConnectors
	if spec.Model != "" {
		if len(spec.Model) > maxNameLen || strings.ContainsAny(spec.Model, " \t") || !plainString(spec.Model) {
			return nil, fmt.Errorf("model %q is not a valid model name", spec.Model)
		}
		doc.Model = spec.Model
	}
	env := map[string]string{}
	for k, v := range spec.Env {
		env[k] = v
	}
	if spec.Profile != "" {
		if prev, ok := env[envpolicy.Profile]; ok && prev != spec.Profile {
			return nil, fmt.Errorf("env %s conflicts with the profile name", envpolicy.Profile)
		}
		if len(spec.Profile) > maxNameLen || !plainString(spec.Profile) {
			return nil, fmt.Errorf("invalid profile name %q", spec.Profile)
		}
		env[envpolicy.Profile] = spec.Profile
	}
	if len(env) > 0 {
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if err := envpolicy.Check(keys); err != nil {
			return nil, err
		}
		for _, k := range keys {
			if len(env[k]) > maxEnvValue || strings.ContainsRune(env[k], 0) {
				return nil, fmt.Errorf("env %s: value is too long or contains NUL", k)
			}
		}
		doc.Env = env
	}
	return res, nil
}

func buildPlugins(spec Spec, mode string, include, exclude, protected map[string]bool, res *Result) {
	installed := map[string]claude.Plugin{}
	locked := map[string]bool{}
	var ids []string
	for _, p := range spec.Installed {
		if !pluginIDPattern.MatchString(p.ID) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("installed plugin %q has an unusual id and was ignored", p.ID))
			continue
		}
		if _, dup := installed[p.ID]; !dup {
			ids = append(ids, p.ID)
		}
		installed[p.ID] = p
		if p.RequiredByOrg {
			locked[p.ID] = true
		}
	}
	sort.Strings(ids)
	plugins := map[string]bool{}
	var spared []string
	for _, id := range ids {
		if locked[id] {
			res.Locked = append(res.Locked, id)
			if exclude[id] || (mode == ModeAllowOnly && !include[id]) {
				res.Warnings = append(res.Warnings, fmt.Sprintf("plugin %s is required by org policy and cannot be masked", id))
			}
			continue
		}
		if include[id] {
			plugins[id] = true
			continue
		}
		wouldMask := exclude[id] || mode == ModeAllowOnly
		if !wouldMask {
			continue
		}
		if protected[id] {
			spared = append(spared, id)
			if exclude[id] {
				res.Warnings = append(res.Warnings, fmt.Sprintf("plugin %s is both excluded and protected; protection wins", id))
			}
			continue
		}
		plugins[id] = false
	}
	for _, id := range sortedUnique(spec.Include) {
		if _, ok := installed[id]; !ok {
			res.Missing = append(res.Missing, id)
			res.Warnings = append(res.Warnings, fmt.Sprintf("plugin %s is not installed and was not written", id))
		}
	}
	res.Protected = spared
	for _, id := range ids {
		v, ok := plugins[id]
		switch {
		case !ok:
		case v:
			res.Enabled = append(res.Enabled, id)
		default:
			res.Masked = append(res.Masked, id)
		}
	}
	if len(plugins) > 0 {
		res.Doc.EnabledPlugins = plugins
	}
}

func buildSkills(spec Spec, res *Result) error {
	if err := checkSkills("off skills", spec.OffSkills); err != nil {
		return err
	}
	if err := checkSkills("name-only skills", spec.NameOnlySkills); err != nil {
		return err
	}
	off, nameOnly := set(spec.OffSkills), set(spec.NameOnlySkills)
	for _, n := range sortedUnique(spec.OffSkills) {
		if nameOnly[n] {
			return fmt.Errorf("skill %q is in both the off and name-only lists", n)
		}
	}
	if len(off)+len(nameOnly) == 0 {
		return nil
	}
	namespaces := map[string]bool{}
	for _, p := range spec.Installed {
		if p.Name != "" {
			namespaces[p.Name] = true
		}
	}
	over := map[string]string{}
	for _, c := range []struct {
		names map[string]bool
		val   string
	}{{off, SkillOff}, {nameOnly, SkillNameOnly}} {
		for n := range c.names {
			over[n] = c.val
		}
	}
	keys := make([]string, 0, len(over))
	for k := range over {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if ns, _, ok := strings.Cut(k, ":"); ok && namespaces[ns] {
			res.Warnings = append(res.Warnings, fmt.Sprintf("skill override %q targets a skill of plugin %s; overrides do not apply to plugin skills", k, ns))
		}
	}
	res.Doc.SkillOverrides = over
	return nil
}
