// Command fakeclaude is a test double of the claude binary, driven by
// environment variables and files, for ccshelf's tests. It emulates what the
// launcher depends on, as verified in Stage 0 (docs/research/stage0.md):
// --version, `plugin list --json [--available]`, `agents --json --all`, and a
// `-p ... --output-format stream-json --verbose` run whose system/init event
// reflects settings layering, masking and MCP filtering.
//
// Environment variables
//
//	FAKE_CLAUDE_VERSION           version printed by --version (default 2.1.291)
//	FAKE_CLAUDE_PLUGINS           file with the installed-plugin JSON array
//	                              (default: a built-in set of five). Entries may
//	                              carry extra "skills": [names] used by init.
//	                              A file holding valid JSON that is not an
//	                              array (null, {}, a string, ...) is printed
//	                              verbatim by `plugin list`, to test callers
//	                              against malformed output.
//	FAKE_CLAUDE_AVAILABLE         file with the "available" array
//	FAKE_CLAUDE_PLUGIN_LIST_FAIL  1: plugin list exits 1 with a stderr message
//	FAKE_CLAUDE_MARKETPLACES      file with the JSON printed by `plugin marketplace
//	                              list --json` (default: acme from github
//	                              acme/plugins and claude-plugins-official from
//	                              github anthropics/claude-plugins-official). The
//	                              shape (name, source, repo|url|path,
//	                              installLocation) was verified against the real
//	                              claude read-only. Valid JSON that is not an
//	                              array is printed verbatim, to test callers.
//	FAKE_CLAUDE_MARKETPLACES_FAIL 1: marketplace list exits 1 with a stderr message
//	FAKE_CLAUDE_AGENTS_JSON       stdout of `agents --json --all` (default [])
//	FAKE_CLAUDE_USER_ENABLED      user-layer settings (inline JSON or file);
//	                              a plain {"id": bool} map is accepted HERE
//	                              ONLY, never inside a --settings value or any
//	                              other layer (real Claude ignores unknown keys)
//	FAKE_CLAUDE_PROJECT_SETTINGS  project-layer settings (inline JSON or file)
//	FAKE_CLAUDE_PROJECT_DIR       when set, the project layer applies only when
//	                              the working directory is this directory, and
//	                              `plugin list` reflects the layer's
//	                              enabledPlugins in "enabled" and
//	                              "projectEnabled" (output depends on cwd)
//	FAKE_CLAUDE_MANAGED           managed settings JSON: enabledPlugins forces
//	                              (a plugin forced true is listed with
//	                              "requiredByOrg": true), disableSideloadFlags
//	FAKE_CLAUDE_SKILLS            JSON array (inline or file) of standalone
//	                              skill names (default ["pdf","legacy-helper"])
//	FAKE_CLAUDE_CONNECTORS        JSON array of claude.ai connector labels
//	                              (default ["claude.ai Shopify","claude.ai Slack"])
//	FAKE_CLAUDE_LOG               file; every invocation is appended as one JSON line
//	FAKE_CLAUDE_EXIT              exit code of generic invocations
//	FAKE_CLAUDE_SLEEP             milliseconds to sleep in generic invocations
//
// An invalid --settings file (bad JSON or failing basic validation) is
// ignored with exit 0 and empty stderr, as real Claude Code does; a missing
// file exits 1 with "Settings file not found". Unknown options exit 1 with
// "error: unknown option '<x>'" (an explicit allowlist, see knownFlags), and
// a missing or invalid --mcp-config or --append-system-prompt-file exits 1.
// The init event always lists the three harness plugins cc-plugin-agents-md,
// cc-plugin-plugin-authoring and cc-plugin-telemetry. The fake is meant to be
// stricter than real Claude Code, never more forgiving.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const defaultVersion = "2.1.291"

var builtinPlugins = `[
 {"id":"design-kit@acme","version":"1.0.0","scope":"user","enabled":true,"installPath":"/fake/design-kit","installedAt":"2026-01-01T00:00:00.000Z","lastUpdated":"2026-01-02T00:00:00.000Z","projectEnabled":false},
 {"id":"sre-kit@acme","version":"2.3.0","scope":"user","enabled":true,"installPath":"/fake/sre-kit","installedAt":"2026-01-01T00:00:00.000Z","lastUpdated":"2026-01-02T00:00:00.000Z","projectEnabled":false,"skills":["runbook","oncall"]},
 {"id":"seo-tools@acme","version":"0.4.1","scope":"user","enabled":true,"installPath":"/fake/seo-tools","installedAt":"2026-01-01T00:00:00.000Z","lastUpdated":"2026-01-02T00:00:00.000Z","projectEnabled":false},
 {"id":"context7@claude-plugins-official","version":"1.0.0","scope":"user","enabled":true,"installPath":"/fake/context7","installedAt":"2026-01-01T00:00:00.000Z","lastUpdated":"2026-01-02T00:00:00.000Z","mcpServers":{"context7":{"command":"npx","args":["-y","context7"]}},"projectEnabled":false},
 {"id":"frontend-design@claude-plugins-official","version":"d4226d062928","scope":"user","enabled":true,"installPath":"/fake/frontend-design","installedAt":"2026-01-01T00:00:00.000Z","lastUpdated":"2026-01-02T00:00:00.000Z","projectEnabled":false}
]`

var builtinAvailable = `[
 {"pluginId":"42crunch-api-security-testing@claude-plugins-official","name":"42crunch-api-security-testing","description":"Fake available plugin","marketplaceName":"claude-plugins-official","source":{"source":"git-subdir","url":"https://example.invalid/a.git","path":"plugins/a","ref":"v1","sha":"0000000000000000000000000000000000000000"},"installCount":10},
 {"pluginId":"adobe-for-creativity@claude-plugins-official","name":"adobe-for-creativity","description":"Another fake available plugin","marketplaceName":"claude-plugins-official","source":{"source":"git-subdir","url":"https://example.invalid/b.git","path":"plugins/b","ref":"main","sha":"1111111111111111111111111111111111111111"},"installCount":20}
]`

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// invocation is one line of the FAKE_CLAUDE_LOG file.
type invocation struct {
	Argv       []string          `json:"argv"`
	Cwd        string            `json:"cwd"`
	Env        map[string]string `json:"env"`
	StdinPiped bool              `json:"stdin_piped"`
}

var loggedEnv = []string{"CCSHELF_PROFILE", "CLAUDE_CONFIG_DIR", "HOME", "USERPROFILE", "ANTHROPIC_BASE_URL", "FAKE_CLAUDE_EXIT", "FAKE_CLAUDE_SLEEP"}

func record(args []string, getenv func(string) string) {
	path := getenv("FAKE_CLAUDE_LOG")
	if path == "" {
		return
	}
	cwd, _ := os.Getwd()
	inv := invocation{Argv: append([]string{}, args...), Cwd: cwd, Env: map[string]string{}}
	for _, k := range loggedEnv {
		if v := getenv(k); v != "" {
			inv.Env[k] = v
		}
	}
	if fi, err := os.Stdin.Stat(); err == nil {
		inv.StdinPiped = fi.Mode()&os.ModeCharDevice == 0
	}
	line, err := json.Marshal(inv)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	record(args, getenv)
	if len(args) == 1 && (args[0] == "--version" || args[0] == "-v") {
		v := getenv("FAKE_CLAUDE_VERSION")
		if v == "" {
			v = defaultVersion
		}
		fmt.Fprintf(stdout, "%s (Claude Code)\n", v)
		return 0
	}
	if len(args) >= 3 && args[0] == "plugin" && args[1] == "marketplace" && args[2] == "list" {
		return marketplaceList(args[3:], getenv, stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "plugin" && args[1] == "list" {
		return pluginList(args[2:], getenv, stdout, stderr)
	}
	if len(args) >= 1 && args[0] == "agents" {
		if v := getenv("FAKE_CLAUDE_AGENTS_JSON"); v != "" {
			fmt.Fprintln(stdout, v)
		} else {
			fmt.Fprintln(stdout, "[]")
		}
		return 0
	}
	return generic(args, getenv, stdout, stderr)
}

var builtinMarketplaces = `[
 {"name":"acme","source":"github","repo":"acme/plugins","installLocation":"/fake/marketplaces/acme"},
 {"name":"claude-plugins-official","source":"github","repo":"anthropics/claude-plugins-official","installLocation":"/fake/marketplaces/claude-plugins-official"}
]`

// marketplaceList emulates `claude plugin marketplace list [--json]`.
func marketplaceList(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if getenv("FAKE_CLAUDE_MARKETPLACES_FAIL") == "1" {
		fmt.Fprintln(stderr, "Error: failed to list marketplaces (fake failure)")
		return 1
	}
	var asJSON bool
	for _, a := range args {
		if a == "--json" {
			asJSON = true
		} else if strings.HasPrefix(a, "-") {
			fmt.Fprintf(stderr, "error: unknown option '%s'\n", a)
			return 1
		}
	}
	data := []byte(builtinMarketplaces)
	if p := getenv("FAKE_CLAUDE_MARKETPLACES"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			fmt.Fprintln(stderr, "Error: cannot read marketplace list:", err)
			return 1
		}
		data = b
	}
	if asJSON {
		fmt.Fprintln(stdout, strings.TrimSpace(string(data)))
		return 0
	}
	var list []struct{ Name string }
	if json.Unmarshal(data, &list) == nil {
		for _, m := range list {
			fmt.Fprintln(stdout, m.Name)
		}
	}
	return 0
}

func readJSONSource(v string) ([]byte, error) {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "{") || strings.HasPrefix(v, "[") {
		return []byte(v), nil
	}
	return os.ReadFile(v)
}

func installedPlugins(getenv func(string) string) ([]map[string]json.RawMessage, error) {
	data := []byte(builtinPlugins)
	if p := getenv("FAKE_CLAUDE_PLUGINS"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		data = b
	}
	var list []map[string]json.RawMessage
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	return list, nil
}

// nonArrayPlugins reports the content of FAKE_CLAUDE_PLUGINS when it is valid
// JSON that is not an array, so `plugin list` can print it verbatim.
func nonArrayPlugins(getenv func(string) string) (string, bool) {
	p := getenv("FAKE_CLAUDE_PLUGINS")
	if p == "" {
		return "", false
	}
	b, err := os.ReadFile(p)
	if err != nil || !json.Valid(b) {
		return "", false
	}
	t := strings.TrimSpace(string(b))
	if strings.HasPrefix(t, "[") {
		return "", false
	}
	return t, true
}

func sameDir(a, b string) bool {
	norm := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		return filepath.Clean(p)
	}
	return a != "" && b != "" && norm(a) == norm(b)
}

// projectLayerApplies reports whether the project layer is in force for the
// current working directory: always when FAKE_CLAUDE_PROJECT_DIR is unset.
func projectLayerApplies(getenv func(string) string) bool {
	dir := getenv("FAKE_CLAUDE_PROJECT_DIR")
	return dir == "" || sameDir(dir, cwd())
}

// applyListState makes `plugin list --json` reflect what real Claude Code
// reports: the output depends on the working directory (project layer) and
// on managed policy (plugins forced on are marked requiredByOrg).
func applyListState(list []map[string]json.RawMessage, getenv func(string) string) {
	var forced map[string]bool
	if m := getenv("FAKE_CLAUDE_MANAGED"); m != "" {
		if l, ok := parseLayer([]byte(m), false); ok {
			_ = json.Unmarshal(l["enabledPlugins"], &forced)
		}
	}
	var project map[string]bool
	if v := getenv("FAKE_CLAUDE_PROJECT_SETTINGS"); v != "" && getenv("FAKE_CLAUDE_PROJECT_DIR") != "" && projectLayerApplies(getenv) {
		if b, err := readJSONSource(v); err == nil {
			if l, ok := parseLayer(b, false); ok {
				_ = json.Unmarshal(l["enabledPlugins"], &project)
			}
		}
	}
	for _, p := range list {
		var id string
		_ = json.Unmarshal(p["id"], &id)
		if v, ok := project[id]; ok {
			p["enabled"], p["projectEnabled"] = boolRaw(v), boolRaw(v)
		}
		if forced[id] {
			p["requiredByOrg"] = boolRaw(true)
		}
	}
}

func boolRaw(b bool) json.RawMessage {
	if b {
		return json.RawMessage("true")
	}
	return json.RawMessage("false")
}

func pluginList(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if getenv("FAKE_CLAUDE_PLUGIN_LIST_FAIL") == "1" {
		fmt.Fprintln(stderr, "Error: failed to list plugins (fake failure)")
		return 1
	}
	var asJSON, available bool
	for _, a := range args {
		switch a {
		case "--json":
			asJSON = true
		case "--available":
			available = true
		default:
			if strings.HasPrefix(a, "-") {
				fmt.Fprintf(stderr, "error: unknown option '%s'\n", a)
				return 1
			}
		}
	}
	if raw, ok := nonArrayPlugins(getenv); ok {
		fmt.Fprintln(stdout, raw)
		return 0
	}
	list, err := installedPlugins(getenv)
	if err != nil {
		fmt.Fprintln(stderr, "Error: cannot read plugin list:", err)
		return 1
	}
	applyListState(list, getenv)
	if !asJSON {
		for _, p := range list {
			var id string
			_ = json.Unmarshal(p["id"], &id)
			fmt.Fprintln(stdout, id)
		}
		return 0
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if !available {
		_ = enc.Encode(list)
		return 0
	}
	avail := json.RawMessage(builtinAvailable)
	if p := getenv("FAKE_CLAUDE_AVAILABLE"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			fmt.Fprintln(stderr, "Error: cannot read available list:", err)
			return 1
		}
		avail = b
	}
	_ = enc.Encode(map[string]any{"installed": list, "available": avail})
	return 0
}

// options are the flags the fake understands.
type options struct {
	print          bool
	outputFormat   string
	settings       []string
	settingSources *string
	strictMCP      bool
	mcpConfig      []string
	model          string
	sideload       []string
	promptFiles    []string
}

type flagKind int

const (
	noValue  flagKind = iota // a switch
	reqValue                 // takes a value, which may start with "-"
	optValue                 // takes an optional value that does not start with "-"
)

// knownFlags is the explicit allowlist of every option the launcher is
// expected to pass. Anything else is an unknown option and exits 1, as in real
// Claude Code, so a typo in the launcher cannot pass a test.
var knownFlags = map[string]flagKind{
	"--settings": reqValue, "--setting-sources": reqValue, "--mcp-config": reqValue,
	"--strict-mcp-config": noValue, "--model": reqValue, "--effort": reqValue,
	"--append-system-prompt": reqValue, "--append-system-prompt-file": reqValue,
	"--resume": optValue, "-r": optValue, "--continue": noValue, "-c": noValue,
	"-p": noValue, "--print": noValue, "--output-format": reqValue, "--input-format": reqValue,
	"--verbose": noValue, "--max-turns": reqValue, "--version": noValue, "--help": noValue,
	"--add-dir": reqValue, "--plugin-dir": reqValue, "--plugin-url": reqValue, "--agents": reqValue,
	"--permission-mode": reqValue, "--session-id": reqValue, "--name": reqValue, "-n": reqValue,
	"--debug": optValue, "--json": noValue, "--available": noValue,
}

// parseArgs reads the command line. A non-empty second result is the
// complete error message (without "error: ") for an unknown option or a
// missing option value.
func parseArgs(args []string) (options, string) {
	var o options
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, val, hasVal := a, "", false
		if strings.HasPrefix(a, "--") {
			if k, v, ok := strings.Cut(a, "="); ok {
				name, val, hasVal = k, v, true
			}
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			continue // a positional argument (a prompt or session id)
		}
		kind, known := knownFlags[name]
		if !known {
			return o, fmt.Sprintf("unknown option '%s'", a)
		}
		var missing bool
		next := func() string {
			if hasVal {
				return val
			}
			switch kind {
			case reqValue:
				if i+1 < len(args) {
					i++
					return args[i]
				}
				missing = true
			case optValue:
				if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
					i++
					return args[i]
				}
			case noValue:
			}
			return ""
		}
		switch name {
		case "-p", "--print":
			o.print = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++ // the prompt
			}
		case "--output-format":
			o.outputFormat = next()
		case "--settings":
			o.settings = append(o.settings, next())
		case "--setting-sources":
			v := next()
			o.settingSources = &v
		case "--strict-mcp-config":
			o.strictMCP = true
		case "--mcp-config":
			o.mcpConfig = append(o.mcpConfig, next())
			o.sideload = append(o.sideload, name)
		case "--append-system-prompt-file":
			o.promptFiles = append(o.promptFiles, next())
		case "--model":
			o.model = next()
		case "--plugin-dir", "--plugin-url", "--agents":
			next()
			o.sideload = append(o.sideload, name)
		default:
			next()
		}
		if missing {
			return o, fmt.Sprintf("option '%s' argument missing", name)
		}
	}
	return o, ""
}

// layer is a parsed settings object.
type layer map[string]json.RawMessage

// parseLayer parses one settings layer. With bare true (only the
// FAKE_CLAUDE_USER_ENABLED variable) a plain {"id@market": bool} map counts as
// enabledPlugins; everywhere else unknown keys are ignored, as real Claude
// Code does.
func parseLayer(data []byte, bare bool) (layer, bool) {
	var l layer
	if err := json.Unmarshal(data, &l); err != nil || l == nil {
		return nil, false
	}
	known := false
	for k := range l {
		switch k {
		case "enabledPlugins", "skillOverrides", "disableClaudeAiConnectors", "deniedMcpServers", "model", "env", "permissions", "hooks", "disableSideloadFlags":
			known = true
		}
	}
	if bare && !known && len(l) > 0 {
		m := map[string]bool{}
		for k, v := range l {
			var b bool
			if json.Unmarshal(v, &b) != nil {
				return l, validate(l)
			}
			m[k] = b
		}
		raw, _ := json.Marshal(m)
		return layer{"enabledPlugins": raw}, true
	}
	return l, validate(l)
}

// validate is the basic validation real Claude Code applies: wrong types make
// the whole file ignored.
func validate(l layer) bool {
	for k, v := range l {
		switch k {
		case "enabledPlugins":
			var m map[string]bool
			if json.Unmarshal(v, &m) != nil {
				return false
			}
		case "skillOverrides":
			var m map[string]string
			if json.Unmarshal(v, &m) != nil {
				return false
			}
			for _, x := range m {
				switch x {
				case "on", "name-only", "user-invocable-only", "off":
				default:
					return false
				}
			}
		case "disableClaudeAiConnectors":
			var b bool
			if json.Unmarshal(v, &b) != nil {
				return false
			}
		case "deniedMcpServers":
			var a []map[string]json.RawMessage
			if json.Unmarshal(v, &a) != nil {
				return false
			}
		case "model":
			var s string
			if json.Unmarshal(v, &s) != nil {
				return false
			}
		case "env":
			var m map[string]string
			if json.Unmarshal(v, &m) != nil {
				return false
			}
		}
	}
	return true
}

// state is the merged result of all layers.
type state struct {
	plugins    map[string]bool
	skills     map[string]string
	connectors bool // disableClaudeAiConnectors
	denied     map[string]bool
	model      string
	permission string
}

func newState() *state {
	return &state{plugins: map[string]bool{}, skills: map[string]string{}, denied: map[string]bool{}, permission: "default"}
}

// apply merges a layer per key: maps merge key by key, lists union, scalars
// replace, and a true disableClaudeAiConnectors sticks.
func (s *state) apply(l layer) {
	var pl map[string]bool
	if json.Unmarshal(l["enabledPlugins"], &pl) == nil {
		for k, v := range pl {
			s.plugins[k] = v
		}
	}
	var so map[string]string
	if json.Unmarshal(l["skillOverrides"], &so) == nil {
		for k, v := range so {
			s.skills[k] = v
		}
	}
	var b bool
	if json.Unmarshal(l["disableClaudeAiConnectors"], &b) == nil && b {
		s.connectors = true
	}
	var dn []map[string]string
	if json.Unmarshal(l["deniedMcpServers"], &dn) == nil {
		for _, e := range dn {
			if n := e["serverName"]; n != "" {
				s.denied[n] = true
			}
		}
	}
	var m string
	if json.Unmarshal(l["model"], &m) == nil && m != "" {
		s.model = m
	}
	var perm struct {
		DefaultMode string `json:"defaultMode"`
	}
	if json.Unmarshal(l["permissions"], &perm) == nil && perm.DefaultMode != "" {
		s.permission = perm.DefaultMode
	}
}

func hasSource(o options, name string) bool {
	if o.settingSources == nil {
		return true
	}
	for _, p := range strings.Split(*o.settingSources, ",") {
		p = strings.TrimSpace(p)
		if p == name || (name == "project" && p == "local") {
			return true
		}
	}
	return false
}

func generic(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	o, perr := parseArgs(args)
	if perr != "" {
		fmt.Fprintf(stderr, "error: %s\n", perr)
		return 1
	}
	var managed layer
	if m := getenv("FAKE_CLAUDE_MANAGED"); m != "" {
		managed, _ = parseLayer([]byte(m), false)
	}
	var disableSideload bool
	_ = json.Unmarshal(managed["disableSideloadFlags"], &disableSideload)
	if disableSideload && len(o.sideload) > 0 {
		fmt.Fprintf(stderr, "error: %s is blocked by managed policy (disableSideloadFlags)\n", o.sideload[0])
		return 1
	}
	for _, f := range o.mcpConfig {
		b, err := readJSONSource(f)
		if err != nil {
			fmt.Fprintf(stderr, "error: MCP config file not found: %s\n", f)
			return 1
		}
		var cfg struct {
			MCPServers map[string]json.RawMessage `json:"mcpServers"`
		}
		if err := json.Unmarshal(b, &cfg); err != nil {
			fmt.Fprintf(stderr, "error: invalid MCP configuration: %v\n", err)
			return 1
		}
	}
	for _, f := range o.promptFiles {
		if _, err := os.ReadFile(f); err != nil {
			fmt.Fprintf(stderr, "error: system prompt file not found: %s\n", f)
			return 1
		}
	}
	if ms := getenv("FAKE_CLAUDE_SLEEP"); ms != "" {
		if n, err := strconv.Atoi(ms); err == nil && n > 0 {
			time.Sleep(time.Duration(n) * time.Millisecond)
		}
	}
	st := newState()
	plugins, err := installedPlugins(getenv)
	if err != nil {
		fmt.Fprintln(stderr, "Error: cannot read plugin list:", err)
		return 1
	}
	// Layer 1: the user layer (installed plugins' own enabled flags, then
	// FAKE_CLAUDE_USER_ENABLED).
	if hasSource(o, "user") {
		for _, p := range plugins {
			var id string
			var en bool
			_ = json.Unmarshal(p["id"], &id)
			if json.Unmarshal(p["enabled"], &en) == nil && en {
				st.plugins[id] = true
			}
		}
		if v := getenv("FAKE_CLAUDE_USER_ENABLED"); v != "" {
			if b, err := readJSONSource(v); err == nil {
				if l, ok := parseLayer(b, true); ok {
					st.apply(l)
				}
			}
		}
	}
	// Layer 2: project/local settings.
	if v := getenv("FAKE_CLAUDE_PROJECT_SETTINGS"); v != "" && hasSource(o, "project") && projectLayerApplies(getenv) {
		if b, err := readJSONSource(v); err == nil {
			if l, ok := parseLayer(b, false); ok {
				st.apply(l)
			}
		}
	}
	// Layer 3: --settings, in order; a missing file is fatal, an invalid one
	// is silently ignored.
	for _, v := range o.settings {
		var b []byte
		if t := strings.TrimSpace(v); strings.HasPrefix(t, "{") {
			b = []byte(t)
		} else {
			var err error
			if b, err = os.ReadFile(v); err != nil {
				fmt.Fprintf(stderr, "Settings file not found: %s\n", v)
				return 1
			}
		}
		if l, ok := parseLayer(b, false); ok {
			st.apply(l)
		}
	}
	// Managed policy wins over everything.
	var forced map[string]bool
	if json.Unmarshal(managed["enabledPlugins"], &forced) == nil {
		for k, v := range forced {
			st.plugins[k] = v
		}
	}
	if o.model != "" {
		st.model = o.model
	}
	if st.model == "" {
		st.model = "fake-model"
	}
	if o.print && o.outputFormat == "stream-json" {
		emitInit(o, st, plugins, getenv, stdout)
	} else {
		fmt.Fprintln(stdout, "fake claude: ok")
	}
	if v := getenv("FAKE_CLAUDE_EXIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 0
}

func jsonList(v string, def []string) []string {
	if v == "" {
		return def
	}
	b, err := readJSONSource(v)
	if err != nil {
		return def
	}
	var out []string
	if json.Unmarshal(b, &out) != nil {
		return def
	}
	return out
}

// harnessPlugins are entries the Claude Code harness always adds to the init
// plugin list, whatever the settings say.
var harnessPlugins = []string{"cc-plugin-agents-md", "cc-plugin-plugin-authoring", "cc-plugin-telemetry"}

func emitInit(o options, st *state, plugins []map[string]json.RawMessage, getenv func(string) string, stdout io.Writer) {
	type pluginOut struct {
		Name   string `json:"name"`
		Path   string `json:"path"`
		Source string `json:"source"`
	}
	var pluginsOut []pluginOut
	for _, h := range harnessPlugins {
		pluginsOut = append(pluginsOut, pluginOut{h, "", "harness"})
	}
	var skills, slash []string
	mcp := []map[string]string{}
	servers := map[string]bool{}
	connectors := jsonList(getenv("FAKE_CLAUDE_CONNECTORS"), []string{"claude.ai Shopify", "claude.ai Slack"})
	for _, p := range plugins {
		var id, path string
		_ = json.Unmarshal(p["id"], &id)
		if !st.plugins[id] {
			continue
		}
		_ = json.Unmarshal(p["installPath"], &path)
		name, _, _ := strings.Cut(id, "@")
		pluginsOut = append(pluginsOut, pluginOut{name, path, id})
		var pskills []string
		if json.Unmarshal(p["skills"], &pskills) != nil || len(pskills) == 0 {
			pskills = []string{"main"}
		}
		for _, s := range pskills {
			skills = append(skills, name+":"+s)
			slash = append(slash, name+":"+s)
		}
		if !o.strictMCP {
			var ms map[string]json.RawMessage
			if json.Unmarshal(p["mcpServers"], &ms) == nil {
				for s := range ms {
					servers["plugin:"+name+":"+s] = true
				}
			}
		}
	}
	for _, name := range jsonList(getenv("FAKE_CLAUDE_SKILLS"), []string{"pdf", "legacy-helper"}) {
		switch overrideFor(st.skills, name) {
		case "off":
			continue
		case "user-invocable-only":
			slash = append(slash, name)
			continue
		}
		skills = append(skills, name)
		slash = append(slash, name)
	}
	if !o.strictMCP && !st.connectors {
		for _, c := range connectors {
			servers[c] = true
		}
	}
	for _, f := range o.mcpConfig {
		b, err := readJSONSource(f)
		if err != nil {
			continue
		}
		var cfg struct {
			MCPServers map[string]json.RawMessage `json:"mcpServers"`
		}
		if json.Unmarshal(b, &cfg) == nil {
			for s := range cfg.MCPServers {
				servers[s] = true
			}
		}
	}
	tools := []string{"Bash", "Edit", "Read"}
	labels := make([]string, 0, len(servers))
	for s := range servers {
		if !st.denied[s] {
			labels = append(labels, s)
		}
	}
	sort.Strings(labels)
	for _, s := range labels {
		mcp = append(mcp, map[string]string{"name": s, "status": "connected"})
		tools = append(tools, "mcp__"+s)
	}
	sort.Strings(skills)
	sort.Strings(slash)
	init := map[string]any{
		"type": "system", "subtype": "init", "session_id": "fake-session",
		"cwd": cwd(), "model": st.model, "permissionMode": st.permission,
		"plugins": nonNil(pluginsOut), "skills": nonNil(skills), "slash_commands": nonNil(slash),
		"agents": []string{"general-purpose"}, "tools": tools, "mcp_servers": mcp,
	}
	enc := json.NewEncoder(stdout)
	_ = enc.Encode(init)
	_ = enc.Encode(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []map[string]string{{"type": "text", "text": "ok"}}}})
	_ = enc.Encode(map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": "ok"})
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func cwd() string {
	d, _ := os.Getwd()
	return d
}

// skillAliasNamespace is the only namespace under which a standalone skill
// can also be addressed (anthropic-skills:pdf for pdf, as seen in Stage 0).
const skillAliasNamespace = "anthropic-skills:"

// overrideFor looks a standalone skill up by its name or its
// anthropic-skills: alias. Other namespaced keys never match a standalone
// skill, so the result does not depend on map iteration order.
func overrideFor(overrides map[string]string, name string) string {
	if v, ok := overrides[name]; ok {
		return v
	}
	if v, ok := overrides[skillAliasNamespace+name]; ok {
		return v
	}
	return "on"
}
