package launcher

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type showMCPDoc struct {
	Data struct {
		MCP map[string]json.RawMessage `json:"mcp"`
	} `json:"data"`
}

func (h *harness) showMCP(name string) map[string]json.RawMessage {
	h.t.Helper()
	h.mustRun("--json", "show", name)
	var env showMCPDoc
	if err := json.Unmarshal(h.out.Bytes(), &env); err != nil {
		h.t.Fatal(err)
	}
	for k, v := range env.Data.MCP {
		var b bytes.Buffer
		if err := json.Compact(&b, v); err != nil {
			h.t.Fatal(err)
		}
		env.Data.MCP[k] = b.Bytes()
	}
	return env.Data.MCP
}

func TestShowJSONDefaults(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("bare", "name = \"bare\"\n")
	mcp := h.showMCP("bare")
	if string(mcp["claudeai_connectors"]) != `"keep"` || string(mcp["strict"]) != "false" {
		t.Errorf("effective values: %s", mcp)
	}
	var got []string
	if err := json.Unmarshal(mcp["defaults"], &got); err != nil || strings.Join(got, ",") != "claudeai_connectors,strict" {
		t.Errorf("defaults = %s", mcp["defaults"])
	}

	h.writeProfile("one", "name = \"one\"\n[mcp]\nstrict = true\n")
	mcp = h.showMCP("one")
	if string(mcp["strict"]) != "true" || string(mcp["defaults"]) != `["claudeai_connectors"]` {
		t.Errorf("one set: %s", mcp)
	}

	h.writeProfile("full", "name = \"full\"\n[mcp]\nclaudeai_connectors = \"keep\"\nstrict = false\n")
	mcp = h.showMCP("full")
	if string(mcp["claudeai_connectors"]) != `"keep"` || string(mcp["defaults"]) != "[]" {
		t.Errorf("explicit values: %s", mcp)
	}

	h.writeProfile("none", "name = \"none\"\n[mcp]\nclaudeai_connectors = \"none\"\nstrict = true\n")
	mcp = h.showMCP("none")
	if string(mcp["claudeai_connectors"]) != `"none"` || string(mcp["strict"]) != "true" || string(mcp["defaults"]) != "[]" {
		t.Errorf("none: %s", mcp)
	}
}

func (h *harness) diffData(a, b string) diffDoc {
	h.t.Helper()
	h.mustRun("--json", "diff", a, b)
	var env struct {
		Data diffDoc `json:"data"`
	}
	if err := json.Unmarshal(h.out.Bytes(), &env); err != nil {
		h.t.Fatal(err)
	}
	return env.Data
}

func TestDiffComparesEffectiveMCPValues(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("unset", "name = \"unset\"\n")
	h.writeProfile("explicit", "name = \"explicit\"\n[mcp]\nclaudeai_connectors = \"keep\"\nstrict = false\n")
	h.writeProfile("hidden", "name = \"hidden\"\n[mcp]\nclaudeai_connectors = \"none\"\nstrict = true\n")

	if d := h.diffData("unset", "explicit"); !d.Identical || len(d.Scalars) != 0 {
		t.Errorf("unset vs explicit default: %+v", d)
	}
	h.mustRun("diff", "unset", "explicit")
	if !strings.Contains(h.out.String(), "same settings") {
		t.Errorf("text: %s", h.out)
	}

	d := h.diffData("unset", "hidden")
	want := map[string][2]string{
		"mcp.claudeai_connectors": {"keep", "none"},
		"mcp.strict":              {"false", "true"},
	}
	if d.Identical || len(d.Scalars) != len(want) {
		t.Fatalf("scalars: %+v", d)
	}
	for _, s := range d.Scalars {
		if w, ok := want[s.Field]; !ok || s.A != w[0] || s.B != w[1] {
			t.Errorf("scalar %+v", s)
		}
	}
	h.mustRun("diff", "unset", "hidden")
	if !strings.Contains(h.out.String(), "keep -> none") || !strings.Contains(h.out.String(), "false -> true") {
		t.Errorf("text: %s", h.out)
	}
}

func TestAccountKeepsVariablesAndWritesBackup(t *testing.T) {
	h := newHarness(t)
	start := "# my notes\n[[sources]]\ntype = \"dir\"\npath = \"$HOME/profiles\"\n"
	h.writeConfig(start)
	dir := filepath.Join(t.TempDir(), "claude-work")

	h.mustRun("account", "add", "work", "--dir", dir, "--default")
	got := h.readConfig()
	if !strings.Contains(got, `path = '$HOME/profiles'`) && !strings.Contains(got, `path = "$HOME/profiles"`) {
		t.Errorf("source path was expanded:\n%s", got)
	}
	cfg := h.loadConfig()
	if cfg.DefaultAccount != "work" || cfg.Accounts["work"].ConfigDir != dir {
		t.Errorf("config: %+v", cfg)
	}
	bak, err := os.ReadFile(h.configFile() + ".bak")
	if err != nil || string(bak) != start {
		t.Fatalf("backup = %q, %v", bak, err)
	}
	if runtime.GOOS != "windows" {
		for _, p := range []string{h.configFile(), h.configFile() + ".bak"} {
			if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o600 {
				t.Errorf("%s: %v %v", p, fi, err)
			}
		}
	}

	// Adding the same account again changes nothing in the account entry.
	h.mustRun("account", "add", "work", "--dir", dir)
	h.mustRun("account", "rm", "work")
	got = h.readConfig()
	if strings.Contains(got, "work") || !strings.Contains(got, "$HOME/profiles") {
		t.Errorf("after rm:\n%s", got)
	}
	if bak, _ := os.ReadFile(h.configFile() + ".bak"); !strings.Contains(string(bak), "work") {
		t.Errorf("backup should hold the file before rm: %s", bak)
	}
}

func TestAccountKeepsVariableInAccountDir(t *testing.T) {
	h := newHarness(t)
	h.writeConfig("[accounts.old]\nconfig_dir = \"$HOME/.claude-old\"\n")
	dir := filepath.Join(t.TempDir(), "claude-new")
	h.mustRun("account", "add", "new", "--dir", dir)
	if got := h.readConfig(); !strings.Contains(got, "$HOME/.claude-old") {
		t.Errorf("account dir was expanded:\n%s", got)
	}
	// The same directory under another name is still refused.
	if code := h.run("account", "add", "other", "--dir", filepath.Join(h.dirs["HOME"], ".claude-old")); code == 0 {
		t.Error("duplicate directory accepted")
	}
	h.mustRun("account", "rm", "new")
	if got := h.readConfig(); !strings.Contains(got, "$HOME/.claude-old") {
		t.Errorf("account dir was expanded:\n%s", got)
	}
}

func TestAccountSaveRefusesChangedFile(t *testing.T) {
	h := newHarness(t)
	h.writeConfig(baseConfig)
	wc, err := readConfigForWrite(h.configFile())
	if err != nil {
		t.Fatal(err)
	}
	other := baseConfig + "\n[ui]\ncolor = \"always\"\n"
	h.writeConfig(other)
	wc.cfg.DefaultAccount = ""
	wc.cfg.UI.Color = "never"
	err = wc.save(wc.cfg)
	if err == nil || !strings.Contains(err.Error(), "changed while editing, nothing written") {
		t.Fatalf("err = %v", err)
	}
	if h.readConfig() != other {
		t.Error("the other change was overwritten")
	}
}
