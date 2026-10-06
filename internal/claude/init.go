package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ErrNoInit is returned by [ParseInit] when the stream has no system/init
// event.
var ErrNoInit = errors.New("no system/init event in the stream")

// MCPStatus is one MCP server of the init event.
type MCPStatus struct {
	Name   string
	Status string
}

// InitInfo is what the system/init event of
// `claude -p ... --output-format stream-json --verbose` reports. Plugin
// names come without @marketplace, as Claude Code prints them.
type InitInfo struct {
	PermissionMode string
	Model          string
	Plugins        []string
	Skills         []string
	SlashCommands  []string
	Agents         []string
	Tools          []string
	MCPServers     []MCPStatus
}

// names decodes an array whose items are strings or objects with a "name".
func names(raw json.RawMessage) []string {
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	var out []string
	for _, it := range items {
		var s string
		if json.Unmarshal(it, &s) == nil {
			out = append(out, s)
			continue
		}
		var o struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(it, &o) == nil && o.Name != "" {
			out = append(out, o.Name)
		}
	}
	return out
}

func str(m map[string]json.RawMessage, keys ...string) string {
	for _, k := range keys {
		var s string
		if v, ok := m[k]; ok && json.Unmarshal(v, &s) == nil {
			return s
		}
	}
	return ""
}

// ParseInit reads stream-json output line by line and returns the first
// system/init event. Other events, blank lines, lines that are not JSON and
// unknown fields are ignored.
func ParseInit(r io.Reader) (*InitInfo, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 && line[0] == '{' {
			var ev map[string]json.RawMessage
			if json.Unmarshal(line, &ev) == nil && str(ev, "type") == "system" && str(ev, "subtype") == "init" {
				return initFrom(ev), nil
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, ErrNoInit
			}
			return nil, fmt.Errorf("read stream: %w", err)
		}
	}
}

func initFrom(ev map[string]json.RawMessage) *InitInfo {
	info := &InitInfo{
		PermissionMode: str(ev, "permissionMode", "permission_mode"),
		Model:          str(ev, "model"),
		Plugins:        names(ev["plugins"]),
		Skills:         names(ev["skills"]),
		SlashCommands:  names(ev["slash_commands"]),
		Agents:         names(ev["agents"]),
		Tools:          names(ev["tools"]),
	}
	var servers []map[string]json.RawMessage
	if json.Unmarshal(ev["mcp_servers"], &servers) == nil {
		for _, s := range servers {
			info.MCPServers = append(info.MCPServers, MCPStatus{Name: str(s, "name"), Status: str(s, "status")})
		}
	}
	return info
}
