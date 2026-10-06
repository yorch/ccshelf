package profile

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/ccshelf/ccshelf/internal/envpolicy"
)

// MaxManifestSize is the largest manifest accepted.
const MaxManifestSize = 256 << 10

var (
	nameRe      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	pluginIDRe  = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9._-]+$`)
	skillNameRe = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)
	accountRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	serverRe    = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	modelRe     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:\[\]-]{0,127}$`)
)

// ValidName reports whether s is a legal profile name.
func ValidName(s string) bool { return nameRe.MatchString(s) }

// sr1Keys are top-level keys that earn the specific SR1 message.
var sr1Keys = map[string]bool{
	"permissions": true, "hooks": true, "apiKeyHelper": true, "allowedMcpServers": true,
	"deniedMcpServers": true, "disableAllHooks": true, "statusLine": true, "env": true,
}

// Parse decodes and validates a manifest. filename is the base file name
// ("frontend.toml"); when non-empty it must equal name + ".toml". Unknown keys
// are errors (SR1). The result is a *ValidationError when anything is wrong.
func Parse(raw []byte, filename string) (*Manifest, error) {
	if len(raw) > MaxManifestSize {
		return nil, &ValidationError{File: filename, Problems: []Problem{{Message: fmt.Sprintf("file is larger than %d bytes", MaxManifestSize)}}}
	}
	var m Manifest
	dec := toml.NewDecoder(bytes.NewReader(raw)).DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, &ValidationError{File: filename, Problems: decodeProblems(err)}
	}
	if probs := validate(&m, raw, filename); len(probs) > 0 {
		return nil, &ValidationError{File: filename, Problems: probs}
	}
	return &m, nil
}

func decodeProblems(err error) []Problem {
	var sm *toml.StrictMissingError
	if errors.As(err, &sm) {
		probs := make([]Problem, 0, len(sm.Errors))
		for i := range sm.Errors {
			e := &sm.Errors[i]
			row, _ := e.Position()
			key := e.Key()
			full := strings.Join(key, ".")
			msg := "unknown key (the profile schema is closed)"
			switch {
			case len(key) == 1 && sr1Keys[key[0]]:
				msg = "not allowed in a profile: SR1"
			case key[len(key)-1] == "command":
				msg = "not allowed in a profile: SR1 (MCP definitions live in the registry, not in a profile)"
			}
			probs = append(probs, Problem{Field: full, Line: row, Message: msg})
		}
		return probs
	}
	var de *toml.DecodeError
	if errors.As(err, &de) {
		row, _ := de.Position()
		msg := de.Error()
		field := strings.Join(de.Key(), ".")
		if strings.HasPrefix(field, "mcp.servers") {
			msg = "mcp.servers must be a list of registry names; MCP definitions are not allowed in a profile: SR1"
		}
		return []Problem{{Field: field, Line: row, Message: msg}}
	}
	return []Problem{{Message: err.Error()}}
}

type validator struct {
	raw   []byte
	probs []Problem
}

func (v *validator) add(field, format string, a ...any) {
	v.probs = append(v.probs, Problem{Field: field, Line: lineOf(v.raw, field), Message: fmt.Sprintf(format, a...)})
}

func validate(m *Manifest, raw []byte, filename string) []Problem {
	v := &validator{raw: raw}
	switch {
	case m.Name == "":
		v.add("name", "required")
	case !nameRe.MatchString(m.Name):
		v.add("name", "%q must match %s", m.Name, nameRe)
	case filename != "" && filename != m.Name+".toml":
		v.add("name", "%q does not match the file name %q (want %s.toml)", m.Name, filename, m.Name)
	}
	v.text("description", m.Description, true)
	v.text("owner", m.Owner, true)
	if m.Status != "" && !contains(Statuses(), m.Status) {
		v.add("status", "%q is not one of %v", m.Status, Statuses())
	}
	switch {
	case m.Status == StatusDeprecated && m.SupersededBy == "":
		v.add("superseded_by", "required when status is deprecated")
	case m.SupersededBy != "" && m.Status != StatusDeprecated:
		v.add("superseded_by", "only allowed when status is deprecated")
	case m.SupersededBy != "" && !nameRe.MatchString(m.SupersededBy):
		v.add("superseded_by", "%q must match %s", m.SupersededBy, nameRe)
	case m.SupersededBy == m.Name && m.SupersededBy != "":
		v.add("superseded_by", "a profile cannot supersede itself")
	}
	if m.Account != "" && !accountRe.MatchString(m.Account) {
		v.add("account", "%q must be an account name matching %s, never a path", m.Account, accountRe)
	}
	v.list("extends", m.Extends, nameRe)
	v.free("when_to_use", m.WhenToUse)
	v.free("avoid_when", m.AvoidWhen)

	if m.Plugins.Mode != "" && !contains(PluginModes(), m.Plugins.Mode) {
		v.add("plugins.mode", "%q is not one of %v", m.Plugins.Mode, PluginModes())
	}
	v.list("plugins.include", m.Plugins.Include, pluginIDRe)
	v.list("plugins.exclude", m.Plugins.Exclude, pluginIDRe)
	v.disjoint("plugins.include", m.Plugins.Include, "plugins.exclude", m.Plugins.Exclude)
	v.list("skills.off", m.Skills.Off, skillNameRe)
	v.list("skills.name_only", m.Skills.NameOnly, skillNameRe)
	v.disjoint("skills.off", m.Skills.Off, "skills.name_only", m.Skills.NameOnly)

	v.list("mcp.servers", m.MCP.Servers, serverRe)
	if m.MCP.ClaudeAIConnectors != "" && !contains(ConnectorModes(), m.MCP.ClaudeAIConnectors) {
		v.add("mcp.claudeai_connectors", "%q is not one of %v", m.MCP.ClaudeAIConnectors, ConnectorModes())
	}

	if m.Session.Model != "" && !modelRe.MatchString(m.Session.Model) {
		v.add("session.model", "%q must match %s", m.Session.Model, modelRe)
	}
	if m.Session.Effort != "" && !contains(Efforts(), m.Session.Effort) {
		v.add("session.effort", "%q is not one of %v", m.Session.Effort, Efforts())
	}
	if p := m.Session.AppendSystemPromptFile; p != "" {
		if err := CheckRelPath(p); err != nil {
			v.add("session.append_system_prompt_file", "%v", err)
		}
	}
	envNames := make([]string, 0, len(m.Session.Env))
	for k := range m.Session.Env {
		envNames = append(envNames, k)
	}
	sort.Strings(envNames)
	for _, k := range envNames {
		field := "session.env." + k
		if r := envpolicy.DeniedReason(k); r != "" {
			v.add(field, "environment variable %q is not allowed in a profile: %s", k, r)
		}
		val := m.Session.Env[k]
		if len(val) > 4096 || strings.ContainsAny(val, "\x00\r\n") {
			v.add(field, "value must be a single line of at most 4096 bytes")
		}
	}
	if m.Policy.OnBlocked != "" && !contains(OnBlockedModes(), m.Policy.OnBlocked) {
		v.add("policy.on_blocked", "%q is not one of %v", m.Policy.OnBlocked, OnBlockedModes())
	}
	return v.probs
}

// text validates a single-line free text field.
func (v *validator) text(field, s string, single bool) {
	for _, r := range s {
		if unicode.IsControl(r) && !(r == '\t' && single) {
			v.add(field, "must not contain control characters")
			return
		}
	}
}

func (v *validator) free(field string, items []string) {
	seen := map[string]bool{}
	for i, s := range items {
		f := fmt.Sprintf("%s[%d]", field, i)
		if strings.TrimSpace(s) == "" {
			v.add(f, "must not be empty")
		}
		v.text(f, s, true)
		if seen[s] {
			v.add(f, "duplicate entry %q", s)
		}
		seen[s] = true
	}
}

func (v *validator) list(field string, items []string, re *regexp.Regexp) {
	seen := map[string]bool{}
	for i, s := range items {
		f := fmt.Sprintf("%s[%d]", field, i)
		if !re.MatchString(s) {
			v.add(f, "%q must match %s", s, re)
		}
		if seen[s] {
			v.add(f, "duplicate entry %q", s)
		}
		seen[s] = true
	}
}

func (v *validator) disjoint(fa string, a []string, fb string, b []string) {
	inB := map[string]bool{}
	for _, s := range b {
		inB[s] = true
	}
	for i, s := range a {
		if inB[s] {
			v.add(fmt.Sprintf("%s[%d]", fa, i), "%q is in both %s and %s", s, fa, fb)
		}
	}
}

// CheckRelPath checks that p is a syntactically safe relative path with forward
// slashes: not absolute, no drive letter, no backslash, no "..", not empty.
func CheckRelPath(p string) error {
	switch {
	case p == "":
		return errors.New("path is empty")
	case strings.ContainsAny(p, "\x00\\"):
		return fmt.Errorf("path %q must use forward slashes and no control characters", p)
	case strings.HasPrefix(p, "/") || (len(p) >= 2 && p[1] == ':'):
		return fmt.Errorf("path %q must be relative to the source root", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return fmt.Errorf("path %q must not contain a \"..\" segment", p)
		}
		if seg == "" {
			return fmt.Errorf("path %q has an empty segment", p)
		}
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// lineOf finds the 1-based line of the key named by a dotted field such as
// "plugins.include[1]" or "session.env.FOO_REF". It tracks [table] headers and
// returns 0 when the key is not found (a heuristic used only for messages).
func lineOf(raw []byte, field string) int {
	if i := strings.IndexByte(field, '['); i >= 0 {
		field = field[:i]
	}
	section := ""
	for n, line := range strings.Split(string(raw), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			section = strings.Trim(strings.TrimSpace(strings.Trim(t, "[]")), " ")
			section = strings.ReplaceAll(section, `"`, "")
			if section == field {
				return n + 1
			}
			continue
		}
		eq := strings.IndexByte(t, '=')
		if eq <= 0 || strings.HasPrefix(t, "#") {
			continue
		}
		key := strings.ReplaceAll(strings.TrimSpace(t[:eq]), `"`, "")
		full := key
		if section != "" {
			full = section + "." + key
		}
		if full == field {
			return n + 1
		}
	}
	return 0
}
