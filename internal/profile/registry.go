package profile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/yorch/ccshelf/internal/envpolicy"
)

// MCP server transport types.
const (
	MCPStdio = "stdio"
	MCPHTTP  = "http"
	MCPSSE   = "sse"
)

// MCPTypes returns the accepted values of servers.<name>.type.
func MCPTypes() []string { return []string{MCPStdio, MCPHTTP, MCPSSE} }

// MaxRegistrySize is the largest registry file accepted.
const MaxRegistrySize = 256 << 10

// MCPServer is one registry entry. EnvRefs are the names of variables the
// server needs. This package passes them on as ${NAME} references and never
// resolves them (SR1).
type MCPServer struct {
	Name    string       `toml:"-" json:"name"`
	Type    string       `toml:"type,omitempty" json:"type"`
	Command string       `toml:"command,omitempty" json:"command,omitempty"`
	Args    []string     `toml:"args,omitempty" json:"args,omitempty"`
	URL     string       `toml:"url,omitempty" json:"url,omitempty"`
	EnvRefs []string     `toml:"env_refs,omitempty" json:"env_refs,omitempty"`
	Windows *MCPOverride `toml:"windows,omitempty" json:"windows,omitempty"`
	MacOS   *MCPOverride `toml:"macos,omitempty" json:"macos,omitempty"`
	Linux   *MCPOverride `toml:"linux,omitempty" json:"linux,omitempty"`
}

// MCPOverride replaces a stdio server's command and arguments on one OS (for
// example "cmd", ["/c", "npx", ...] on Windows).
type MCPOverride struct {
	Command string   `toml:"command" json:"command"`
	Args    []string `toml:"args,omitempty" json:"args,omitempty"`
}

type registryFile struct {
	Servers map[string]MCPServer `toml:"servers"`
}

// LoadRegistry reads and validates an MCP registry file. The schema is closed.
// The file itself must not be a symlink (B7).
func LoadRegistry(path string) (map[string]MCPServer, error) {
	raw, err := readFileNoFollow(path, MaxRegistrySize)
	if err != nil {
		return nil, fmt.Errorf("reading MCP registry: %w", err)
	}
	return ParseRegistry(raw, path)
}

// ParseRegistry validates registry bytes. name is only for messages.
func ParseRegistry(raw []byte, name string) (map[string]MCPServer, error) {
	var rf registryFile
	dec := toml.NewDecoder(bytes.NewReader(raw)).DisallowUnknownFields()
	if err := dec.Decode(&rf); err != nil {
		return nil, &ValidationError{File: name, Problems: decodeProblems(err)}
	}
	v := &validator{raw: raw}
	spell, err := spellingProblems(raw, registryFile{}, "")
	if err != nil {
		return nil, &ValidationError{File: name, Problems: []Problem{{Message: err.Error()}}}
	}
	v.probs = append(v.probs, spell...)
	names := make([]string, 0, len(rf.Servers))
	for n := range rf.Servers {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make(map[string]MCPServer, len(names))
	for _, n := range names {
		s := rf.Servers[n]
		s.Name = n
		if s.Type == "" {
			s.Type = MCPStdio
		}
		validateServer(v, n, &s)
		out[n] = s
	}
	if len(v.probs) > 0 {
		return nil, &ValidationError{File: name, Problems: v.probs}
	}
	return out, nil
}

func validateServer(v *validator, name string, s *MCPServer) {
	f := "servers." + name
	if !serverRe.MatchString(name) || len(name) > 64 {
		v.add(f, "server name %q must match %s (at most 64 characters)", name, serverRe)
	}
	if !contains(MCPTypes(), s.Type) {
		v.add(f+".type", "%q is not one of %v", s.Type, MCPTypes())
		return
	}
	checkText := func(field, val string) {
		if p := textProblem(val, false); p != "" {
			v.add(field, "%s", p)
		}
		if strings.Contains(val, "${") {
			v.add(field, "must not contain ${...}: pass secrets through env_refs, never through command, args or url (SR1)")
		}
	}
	for _, r := range []struct {
		field string
		o     *MCPOverride
	}{{f + ".windows", s.Windows}, {f + ".macos", s.MacOS}, {f + ".linux", s.Linux}} {
		if r.o == nil {
			continue
		}
		if s.Type != MCPStdio {
			v.add(r.field, "per-OS overrides apply to stdio servers only")
			continue
		}
		if r.o.Command == "" {
			v.add(r.field+".command", "required in an override")
		}
		checkText(r.field+".command", r.o.Command)
		for i, a := range r.o.Args {
			checkText(fmt.Sprintf("%s.args[%d]", r.field, i), a)
		}
	}
	switch s.Type {
	case MCPStdio:
		if s.Command == "" {
			v.add(f+".command", "stdio servers need a command")
		}
		if s.URL != "" {
			v.add(f+".url", "not allowed for a stdio server")
		}
		checkText(f+".command", s.Command)
		for i, a := range s.Args {
			checkText(fmt.Sprintf("%s.args[%d]", f, i), a)
		}
		if err := envpolicy.Check(s.EnvRefs); err != nil {
			v.add(f+".env_refs", "%v", err)
		}
		seen := map[string]bool{}
		for i, e := range s.EnvRefs {
			if seen[e] {
				v.add(fmt.Sprintf("%s.env_refs[%d]", f, i), "duplicate entry %q", e)
			}
			seen[e] = true
		}
	default:
		if s.Command != "" || len(s.Args) > 0 {
			v.add(f, "%s servers take url, not command or args", s.Type)
		}
		if len(s.EnvRefs) > 0 {
			v.add(f+".env_refs", "only supported for stdio servers")
		}
		checkText(f+".url", s.URL)
		validateURL(v, f+".url", s.URL)
	}
}

// credentialQueryKeys are query keys that look like they carry a secret.
var credentialQueryKeys = []string{"token", "key", "secret", "password", "passwd", "auth", "sig", "signature", "api_key", "apikey", "access_token", "credential"}

func looksLikeCredentialKey(k string) bool {
	k = strings.ToLower(k)
	for _, c := range credentialQueryKeys {
		if k == c || strings.Contains(k, c) {
			return true
		}
	}
	return false
}

// validateURL checks the url of an http or sse server: lowercase https:// (Go
// would accept HTTPS:// but the schema does not, so both reject it), a host,
// no userinfo, whitespace or fragment, and no query string at all (B6): secrets in a URL end up in
// logs, process lists and the closure digest.
func validateURL(v *validator, field, raw string) {
	u, err := url.Parse(raw)
	switch {
	case err != nil || !strings.HasPrefix(raw, "https://") || u.Scheme != "https" || u.Host == "":
		v.add(field, "%q must be an https:// URL (lowercase scheme)", raw)
		return
	case u.User != nil:
		v.add(field, "must not embed credentials")
		return
	case strings.IndexFunc(raw, unicode.IsSpace) >= 0:
		v.add(field, "must not contain whitespace")
		return
	case u.Fragment != "" || strings.Contains(raw, "#"):
		v.add(field, "must not contain a fragment")
		return
	}
	if u.RawQuery == "" && !u.ForceQuery {
		return
	}
	for k := range u.Query() {
		if looksLikeCredentialKey(k) {
			v.add(field, "query key %q looks like a credential: never put secrets in a URL. Use env_refs (SR1)", k)
			return
		}
	}
	v.add(field, "must not contain a query string. Pass anything secret through env_refs, never through the URL (SR1)")
}

// ForOS returns the server with the per-OS override for goos ("windows",
// "darwin" or "linux") applied and the override fields cleared. Other values
// of goos return the base definition.
func (s MCPServer) ForOS(goos string) MCPServer {
	var o *MCPOverride
	switch goos {
	case "windows":
		o = s.Windows
	case "darwin":
		o = s.MacOS
	case "linux":
		o = s.Linux
	}
	out := s
	out.Windows, out.MacOS, out.Linux = nil, nil, nil
	out.Args = append([]string(nil), s.Args...)
	out.EnvRefs = append([]string(nil), s.EnvRefs...)
	if o != nil && s.Type == MCPStdio {
		out.Command = o.Command
		out.Args = append([]string(nil), o.Args...)
	}
	return out
}

// claudeServer is the object Claude Code expects in an --mcp-config file.
type claudeServer struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	URL     string            `json:"url,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// ClaudeJSON returns the --mcp-config object for the server on goos. Variables
// named in EnvRefs appear as "${NAME}" references, never as values.
func (s MCPServer) ClaudeJSON(goos string) (json.RawMessage, error) {
	o := s.ForOS(goos)
	var c claudeServer
	switch o.Type {
	case MCPStdio:
		if o.Command == "" {
			return nil, fmt.Errorf("MCP server %q has no command", s.Name)
		}
		c.Command, c.Args = o.Command, o.Args
		if c.Args == nil {
			c.Args = []string{}
		}
		if len(o.EnvRefs) > 0 {
			c.Env = make(map[string]string, len(o.EnvRefs))
			for _, n := range o.EnvRefs {
				c.Env[n] = "${" + n + "}"
			}
		}
	case MCPHTTP, MCPSSE:
		if o.URL == "" {
			return nil, fmt.Errorf("MCP server %q has no url", s.Name)
		}
		c.Type, c.URL = o.Type, o.URL
	default:
		return nil, fmt.Errorf("MCP server %q has unknown type %q", s.Name, o.Type)
	}
	b, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("encoding MCP server %q: %w", s.Name, err)
	}
	return b, nil
}

// MCPConfigJSON returns {"mcpServers": {...}} for servers on goos, with
// deterministic key order and two-space indentation.
func MCPConfigJSON(servers map[string]MCPServer, goos string) ([]byte, error) {
	names := make([]string, 0, len(servers))
	for n := range servers {
		names = append(names, n)
	}
	sort.Strings(names)
	m := make(map[string]json.RawMessage, len(names))
	for _, n := range names {
		s := servers[n]
		if s.Name == "" {
			s.Name = n
		}
		j, err := s.ClaudeJSON(goos)
		if err != nil {
			return nil, err
		}
		m[n] = j
	}
	b, err := json.MarshalIndent(map[string]any{"mcpServers": m}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding MCP config: %w", err)
	}
	return append(b, '\n'), nil
}

// canonicalJSON is the deterministic encoding used for closure digests. It
// includes every per-OS override and sorts env_refs.
func (s MCPServer) canonicalJSON() ([]byte, error) {
	c := s
	c.EnvRefs = append([]string(nil), s.EnvRefs...)
	sort.Strings(c.EnvRefs)
	b, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("encoding MCP server %q: %w", s.Name, err)
	}
	return b, nil
}
