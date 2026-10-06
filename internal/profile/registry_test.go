package profile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRegistryFixture(t *testing.T) {
	reg, err := LoadRegistry(fixture(t, "tree", "org", "mcp", "registry.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(reg) != 2 || reg["figma"].Type != "stdio" || reg["figma"].Windows == nil || reg["docs"].URL == "" || reg["figma"].Name != "figma" {
		t.Errorf("registry: %+v", reg)
	}
}

func TestParseRegistryInvalid(t *testing.T) {
	tests := []struct{ name, body, want string }{
		{"unknown key", "[servers.a]\ncommand = \"x\"\nbogus = 1\n", "bogus"},
		{"unknown top", "[other]\nx = 1\n", "other"},
		{"stdio no command", "[servers.a]\ntype = \"stdio\"\n", "need a command"},
		{"default type needs command", "[servers.a]\nurl = \"https://x.example\"\n", "not allowed for a stdio"},
		{"bad type", "[servers.a]\ntype = \"ws\"\n", "not one of"},
		{"http no url", "[servers.a]\ntype = \"http\"\n", "https://"},
		{"http plain", "[servers.a]\ntype = \"http\"\nurl = \"http://x.example\"\n", "https://"},
		{"http creds", "[servers.a]\ntype = \"http\"\nurl = \"https://u:p@x.example\"\n", "credentials"},
		{"http with command", "[servers.a]\ntype = \"sse\"\nurl = \"https://x.example\"\ncommand = \"x\"\n", "take url"},
		{"http env refs", "[servers.a]\ntype = \"http\"\nurl = \"https://x.example\"\nenv_refs = [\"A_REF\"]\n", "only supported for stdio"},
		{"bad name", "[servers.\"a b\"]\ncommand = \"x\"\n", "server name"},
		{"env denied", "[servers.a]\ncommand = \"x\"\nenv_refs = [\"ANTHROPIC_API_KEY\"]\n", "not allowed"},
		{"env dup", "[servers.a]\ncommand = \"x\"\nenv_refs = [\"A_REF\", \"A_REF\"]\n", "duplicate"},
		{"secret in args", "[servers.a]\ncommand = \"x\"\nargs = [\"--token=${TOKEN}\"]\n", "env_refs"},
		{"secret in command", "[servers.a]\ncommand = \"${X}\"\n", "env_refs"},
		{"newline in arg", "[servers.a]\ncommand = \"x\"\nargs = [\"a\\nb\"]\n", "control characters"},
		{"override on http", "[servers.a]\ntype = \"http\"\nurl = \"https://x.example\"\n[servers.a.linux]\ncommand = \"x\"\n", "stdio servers only"},
		{"override no command", "[servers.a]\ncommand = \"x\"\n[servers.a.macos]\nargs = [\"x\"]\n", "required in an override"},
		{"override secret", "[servers.a]\ncommand = \"x\"\n[servers.a.windows]\ncommand = \"c\"\nargs = [\"${X}\"]\n", "env_refs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseRegistry([]byte(tt.body), "registry.toml")
			mustErrContain(t, err, tt.want)
		})
	}
}

func TestLoadRegistryErrors(t *testing.T) {
	if _, err := LoadRegistry(filepath.Join(t.TempDir(), "none.toml")); err == nil {
		t.Error("missing file accepted")
	}
	dir := mk(t, map[string]string{"big.toml": "# " + strings.Repeat("x", MaxRegistrySize)})
	_, err := LoadRegistry(filepath.Join(dir, "big.toml"))
	mustErrContain(t, err, "larger than")
	if _, err := LoadRegistry(dir); err == nil {
		t.Error("directory accepted")
	}
}

func TestForOSAndJSON(t *testing.T) {
	reg, _ := LoadRegistry(fixture(t, "tree", "org", "mcp", "registry.toml"))
	f := reg["figma"]
	w := f.ForOS("windows")
	if w.Command != "cmd" || w.Args[0] != "/c" || w.Windows != nil {
		t.Errorf("windows: %+v", w)
	}
	if l := f.ForOS("linux"); l.Command != "npx" || len(l.Args) != 2 {
		t.Errorf("linux: %+v", l)
	}
	if f.ForOS("plan9").Command != "npx" || f.Windows == nil {
		t.Error("ForOS must not mutate or misapply")
	}
	for goos, want := range map[string]string{
		"linux":   `{"command":"npx","args":["-y","figma-mcp"],"env":{"FIGMA_TOKEN_REF":"${FIGMA_TOKEN_REF}"}}`,
		"windows": `{"command":"cmd","args":["/c","npx","-y","figma-mcp"],"env":{"FIGMA_TOKEN_REF":"${FIGMA_TOKEN_REF}"}}`,
		"darwin":  `{"command":"npx","args":["-y","figma-mcp"],"env":{"FIGMA_TOKEN_REF":"${FIGMA_TOKEN_REF}"}}`,
	} {
		got, err := f.ClaudeJSON(goos)
		if err != nil || string(got) != want {
			t.Errorf("%s: %s, %v", goos, got, err)
		}
	}
	got, _ := reg["docs"].ClaudeJSON("linux")
	if string(got) != `{"type":"http","url":"https://mcp.example.com/docs"}` {
		t.Errorf("http: %s", got)
	}
	sse := MCPServer{Name: "s", Type: "sse", URL: "https://x.example/sse"}
	if got, _ := sse.ClaudeJSON("linux"); string(got) != `{"type":"sse","url":"https://x.example/sse"}` {
		t.Errorf("sse: %s", got)
	}
	if _, err := (MCPServer{Name: "a", Type: "stdio"}).ClaudeJSON("linux"); err == nil {
		t.Error("stdio without command")
	}
	if _, err := (MCPServer{Name: "a", Type: "http"}).ClaudeJSON("linux"); err == nil {
		t.Error("http without url")
	}
	if _, err := (MCPServer{Name: "a", Type: "ws"}).ClaudeJSON("linux"); err == nil {
		t.Error("bad type")
	}
	noArgs := MCPServer{Name: "n", Type: "stdio", Command: "x"}
	if got, _ := noArgs.ClaudeJSON("linux"); string(got) != `{"command":"x"}` {
		t.Errorf("no args: %s", got)
	}
}

func TestMCPConfigJSONGolden(t *testing.T) {
	reg, _ := LoadRegistry(fixture(t, "tree", "org", "mcp", "registry.toml"))
	for _, goos := range []string{"linux", "windows"} {
		got, err := MCPConfigJSON(reg, goos)
		if err != nil {
			t.Fatal(err)
		}
		if !json.Valid(got) {
			t.Fatal("invalid JSON")
		}
		golden := filepath.Join("testdata", "mcp-config-"+goos+".golden.json")
		if os.Getenv("UPDATE_GOLDEN") != "" {
			_ = os.WriteFile(golden, got, 0o600)
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("%s mismatch:\n%s", goos, got)
		}
		if strings.Contains(string(got), "op://") {
			t.Error("secret reference value leaked")
		}
	}
	if _, err := MCPConfigJSON(map[string]MCPServer{"a": {Type: "http"}}, "linux"); err == nil {
		t.Error("expected error")
	}
	if b, err := MCPConfigJSON(nil, "linux"); err != nil || !strings.Contains(string(b), "mcpServers") {
		t.Errorf("empty: %s %v", b, err)
	}
}
