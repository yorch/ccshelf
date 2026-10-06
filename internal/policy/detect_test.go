package policy

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func detect(t *testing.T, opt Options) *Policy {
	t.Helper()
	p, err := Detect(context.Background(), opt)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func linuxOpt(dir string) Options {
	f := false
	return Options{GOOS: "linux", ManagedDir: dir, WSL: &f}
}

func bp(v bool) *bool { return &v }

func hasUnknown(p *Policy, sub string) bool {
	for _, u := range p.Unknown {
		if strings.Contains(u, sub) {
			return true
		}
	}
	return false
}

func hasWarning(p *Policy, sub string) bool {
	for _, u := range p.Warnings {
		if strings.Contains(u, sub) {
			return true
		}
	}
	return false
}

func TestNoPolicy(t *testing.T) {
	p := detect(t, linuxOpt(t.TempDir()))
	if p.Unreadable || len(p.Warnings) != 0 || p.DisableSideloadFlags != nil || p.EnabledPlugins != nil {
		t.Fatalf("%+v", p)
	}
	if !hasUnknown(p, "server-managed") {
		t.Fatal("server-managed settings must always be listed as unknown")
	}
	if p.ManagedSourcesBehavior != "first-wins" {
		t.Fatal(p.ManagedSourcesBehavior)
	}
	m := Evaluate(p, nil)
	for _, id := range AllFeatures {
		if m.Features[id].State != Available {
			t.Errorf("%s = %s", id, m.Features[id].State)
		}
	}
}

func TestMissingDirAndCancelledContext(t *testing.T) {
	p := detect(t, linuxOpt(filepath.Join(t.TempDir(), "nope")))
	if p.Unreadable {
		t.Fatal("absent dir is not unreadable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Detect(ctx, linuxOpt(t.TempDir())); err == nil {
		t.Fatal("canceled context must error")
	}
}

func TestPermissivePolicy(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "managed-settings.json"), `{
 "strictKnownMarketplaces":[{"source":"github","repo":"acme/plugins"},{"source":"git","url":"https://user:tok@git.example.com/x.git?token=s#f"}],
 "someFutureKey": {"secret":"hunter2"}
}`)
	p := detect(t, linuxOpt(dir))
	if len(p.StrictKnownMarketplaces) != 2 {
		t.Fatalf("%+v", p.StrictKnownMarketplaces)
	}
	if got := p.StrictKnownMarketplaces[1].Ref; got != "https://git.example.com/x.git" {
		t.Fatalf("credentials not redacted: %q", got)
	}
	if !reflect.DeepEqual(p.Other, []string{"someFutureKey"}) {
		t.Fatalf("other = %v", p.Other)
	}
	if p.DisableSideloadFlags != nil {
		t.Fatal("must be unset")
	}
	m := Evaluate(p, nil)
	if m.Features[PluginDir].State != Available {
		t.Fatal("plugin-dir should be available")
	}
	b, _ := m.JSON()
	if strings.Contains(string(b), "hunter2") || strings.Contains(string(b), "tok@") {
		t.Fatal("secret leaked")
	}
}

func TestSideloadOnly(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "managed-settings.json"), `{"disableSideloadFlags": true}`)
	p := detect(t, linuxOpt(dir))
	if p.DisableSideloadFlags == nil || !*p.DisableSideloadFlags {
		t.Fatal("not read")
	}
	m := Evaluate(p, nil)
	for _, id := range []FeatureID{PluginDir, Agents, AddMCPConfig} {
		if m.Features[id].State != Blocked || m.Features[id].Source != "disableSideloadFlags" {
			t.Errorf("%s = %+v", id, m.Features[id])
		}
	}
	for _, id := range []FeatureID{SettingsMasking, HideConnectors, DenyMCPServers, SettingSources, AppendSystemPromptFile} {
		if m.Features[id].State != Available {
			t.Errorf("%s = %+v", id, m.Features[id])
		}
	}
}

func TestForcedPluginsOnly(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "managed-settings.json"), `{"enabledPlugins":{"audit@acme":true,"old@acme":false,"bad@acme":"yes"}}`)
	p := detect(t, linuxOpt(dir))
	if !hasWarning(p, "enabledPlugins.bad@acme") {
		t.Fatalf("warnings: %v", p.Warnings)
	}
	m := Evaluate(p, nil)
	if !reflect.DeepEqual(m.LockedPlugins(), []string{"audit@acme"}) || !reflect.DeepEqual(m.BlockedPlugins(), []string{"old@acme"}) {
		t.Fatalf("%v %v", m.LockedPlugins(), m.BlockedPlugins())
	}
	if m.Features[PluginDir].State != Available {
		t.Fatal("plugin-dir must stay available")
	}
	if m.Features[ForcedPlugins].State != Blocked {
		t.Fatal("forced plugins cannot be masked")
	}
}

func TestBoth(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "managed-settings.json"), `{"disableSideloadFlags":true,"enabledPlugins":{"a@m":true}}`)
	p := detect(t, linuxOpt(dir))
	m := Evaluate(p, nil)
	if m.Features[PluginDir].State != Blocked || len(m.LockedPlugins()) != 1 {
		t.Fatalf("%+v", m.Features)
	}
}

func TestMalformedIsUnknownNotNoPolicy(t *testing.T) {
	for name, content := range map[string]string{
		"junk": `{not json`, "array": `[1]`, "trailing": `{} {}`, "scalar": `"x"`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, "managed-settings.json"), content)
			p := detect(t, linuxOpt(dir))
			if !p.Unreadable || len(p.Warnings) == 0 || !hasUnknown(p, "malformed") {
				t.Fatalf("%+v", p)
			}
			m := Evaluate(p, nil)
			for _, id := range []FeatureID{PluginDir, Agents, AddMCPConfig, SettingSources} {
				if m.Features[id].State != Unknown {
					t.Errorf("%s = %s", id, m.Features[id].State)
				}
			}
			if m.Features[SettingsMasking].State != Available {
				t.Fatal("masking is always available")
			}
		})
	}
}

func TestEmptyAndBOM(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "managed-settings.json"), "")
	if p := detect(t, linuxOpt(dir)); p.Unreadable || len(p.Warnings) != 0 {
		t.Fatalf("empty file is {}: %+v", p)
	}
	write(t, filepath.Join(dir, "managed-settings.json"), "\xef\xbb\xbf{\"disableAllHooks\":true}")
	if p := detect(t, linuxOpt(dir)); p.DisableAllHooks == nil || !*p.DisableAllHooks {
		t.Fatal("BOM not tolerated")
	}
}

func TestDropInOverride(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "managed-settings.json"), `{
 "disableSideloadFlags": true,
 "deniedMcpServers":[{"serverName":"a"}],
 "enabledPlugins":{"x@m":true},
 "extraKnownMarketplaces":{"one":{"source":{"source":"github","repo":"o/one"}}},
 "managedMcpServers":{"s1":{"type":"http","url":"https://h/x","headers":{"Authorization":"Bearer SECRET"}}}}`)
	d := filepath.Join(dir, "managed-settings.d")
	write(t, filepath.Join(d, "10-a.json"), `{"disableSideloadFlags": false,"deniedMcpServers":[{"serverName":"b"},{"serverName":"a"}],"enabledPlugins":{"y@m":false}}`)
	write(t, filepath.Join(d, "20-b.json"), `{"extraKnownMarketplaces":{"one":{"source":{"source":"github","repo":"o/uno"}},"two":{"source":{"source":"directory","path":"/srv/m"}}},"managedMcpServers":{"s2":{"type":"sse","url":"https://h/y"}}}`)
	write(t, filepath.Join(d, ".hidden.json"), `{"disableAllHooks":true}`)
	write(t, filepath.Join(d, "30-notes.txt"), `garbage`)
	write(t, filepath.Join(d, "05-first.json"), `{"disableSideloadFlags": true, "disableClaudeAiConnectors": true}`)
	p := detect(t, linuxOpt(dir))
	// Order: managed-settings.json, 05, 10, 20: the later single value wins.
	if p.DisableSideloadFlags == nil || *p.DisableSideloadFlags {
		t.Fatal("drop-in 10 must override to false")
	}
	if p.DisableAllHooks != nil {
		t.Fatal("hidden file read")
	}
	if p.DisableClaudeAiConnectors == nil || !*p.DisableClaudeAiConnectors {
		t.Fatal("05 not applied")
	}
	if len(p.DeniedMcpServers) != 2 {
		t.Fatalf("lists combine without duplicates: %+v", p.DeniedMcpServers)
	}
	if len(p.EnabledPlugins) != 2 || !p.EnabledPlugins["x@m"] || p.EnabledPlugins["y@m"] {
		t.Fatalf("nested blocks merge key by key: %v", p.EnabledPlugins)
	}
	if p.ExtraKnownMarketplaces["one"].Ref != "o/uno" || len(p.ExtraKnownMarketplaces) != 2 {
		t.Fatalf("entries replace whole by name: %+v", p.ExtraKnownMarketplaces)
	}
	if !reflect.DeepEqual(p.ManagedMcpServers, []string{"s1", "s2"}) {
		t.Fatalf("%v", p.ManagedMcpServers)
	}
	var dropins int
	for _, s := range p.Sources {
		if s.Kind == KindDropIn {
			dropins++
			if !s.Present || !s.Used || len(s.Keys) == 0 {
				t.Errorf("%+v", s)
			}
		}
	}
	if dropins != 3 {
		t.Fatalf("dropins = %d", dropins)
	}
}

func TestMalformedDropIn(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "managed-settings.json"), `{"disableSideloadFlags": true}`)
	write(t, filepath.Join(dir, "managed-settings.d", "10-bad.json"), `{`)
	p := detect(t, linuxOpt(dir))
	if !p.Unreadable {
		t.Fatal("a malformed drop-in makes the policy unknown")
	}
	if p.DisableSideloadFlags == nil || !*p.DisableSideloadFlags {
		t.Fatal("the readable part is still interpreted")
	}
}

func TestWrongTypes(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "managed-settings.json"), `{
 "disableSideloadFlags": 5,
 "allowManagedHooksOnly": [],
 "disableAllHooks": "maybe",
 "enabledPlugins": [],
 "strictKnownMarketplaces": "x",
 "blockedMarketplaces": [1, {"source":"github","repo":"a/b"}],
 "deniedMcpServers": [{"nothing":1}, 3],
 "allowedMcpServers": {},
 "managedMcpServers": [],
 "extraKnownMarketplaces": {"a": 1},
 "pluginSuggestionMarketplaces": [1, "ok"],
 "permissions": "x",
 "disableClaudeAiConnectors": {}
}`)
	p := detect(t, linuxOpt(dir))
	if p.DisableSideloadFlags == nil || !*p.DisableSideloadFlags || p.AllowManagedHooksOnly == nil || !*p.AllowManagedHooksOnly {
		t.Fatal("lock keys with the wrong type must fail closed")
	}
	if p.DisableAllHooks != nil || p.DisableClaudeAiConnectors != nil || p.EnabledPlugins != nil || p.StrictKnownMarketplaces != nil {
		t.Fatalf("non-lock keys with the wrong type stay unset: %+v", p)
	}
	if len(p.BlockedMarketplaces) != 1 || len(p.PluginSuggestionMarketplaces) != 1 {
		t.Fatalf("%+v", p)
	}
	if len(p.Warnings) < 10 {
		t.Fatalf("warnings: %v", p.Warnings)
	}
	if p.Unreadable {
		t.Fatal("wrong types are warnings, not failures")
	}
}

func TestEmptyAllowlistsAreDistinguishable(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "managed-settings.json"), `{"strictKnownMarketplaces":[],"allowedMcpServers":[],"permissions":{"disableBypassPermissionsMode":"disable"}}`)
	p := detect(t, linuxOpt(dir))
	if p.StrictKnownMarketplaces == nil || len(p.StrictKnownMarketplaces) != 0 || p.AllowedMcpServers == nil {
		t.Fatalf("empty list must stay non-nil: %+v", p)
	}
	if p.DisableBypassPermissionsMode == nil || !*p.DisableBypassPermissionsMode {
		t.Fatal("disableBypassPermissionsMode")
	}
	write(t, filepath.Join(dir, "managed-settings.json"), `{"allowedMarketplaces":[{"source":"github","repo":"a/b"}],"permissions":{"disableBypassPermissionsMode":"nope"}}`)
	p = detect(t, linuxOpt(dir))
	if len(p.StrictKnownMarketplaces) != 1 || p.DisableBypassPermissionsMode == nil || *p.DisableBypassPermissionsMode {
		t.Fatalf("alias / bad value: %+v", p)
	}
}

func TestServerRulesRedaction(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "managed-settings.json"), `{"allowedMcpServers":[
 {"serverName":"gh"},
 {"serverUrl":"https://u:p@mcp.example.com/x?key=SECRET"},
 {"serverCommand":["npx","-y","pkg","--token=SECRET"]},
 {"serverCommand":[]}
]}`)
	p := detect(t, linuxOpt(dir))
	want := []ServerRule{{"serverName", "gh"}, {"serverUrl", "https://mcp.example.com/x"}, {"serverCommand", "npx"}, {"serverCommand", ""}}
	if !reflect.DeepEqual(p.AllowedMcpServers, want) {
		t.Fatalf("%+v", p.AllowedMcpServers)
	}
	for in, out := range map[string]string{
		"git@github.com:org/repo.git": "github.com:org/repo.git",
		"https://h/p?x=1#f":           "https://h/p",
		"https://a b/%zz":             "(unparseable url)",
		"git@host:p?token=1":          "host:p",
		"ssh://git:pw@host.example/r": "ssh://host.example/r",
	} {
		if got := redactURL(in); got != out {
			t.Errorf("redactURL(%q) = %q, want %q", in, got, out)
		}
	}
}

func TestManagedMCPFile(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "managed-mcp.json"), `{"mcpServers":{}}`)
	p := detect(t, linuxOpt(dir))
	if !p.ManagedMCPFile {
		t.Fatal("not detected")
	}
	m := Evaluate(p, nil)
	if f := m.Features[AddMCPConfig]; f.State != Blocked || f.Source != "managed-mcp.json" {
		t.Fatalf("%+v", f)
	}
	if m.Features[DenyMCPServers].State != Available {
		t.Fatal("deniedMcpServers still works")
	}
}

func TestFileHardening(t *testing.T) {
	t.Run("directory in place of file", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "managed-settings.json"), 0o755); err != nil {
			t.Fatal(err)
		}
		p := detect(t, linuxOpt(dir))
		if !p.Unreadable || !hasUnknown(p, "not a regular file") {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("too large", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "managed-settings.json"), `{"x":"`+strings.Repeat("a", maxDocSize)+`"}`)
		p := detect(t, linuxOpt(dir))
		if !p.Unreadable || !hasUnknown(p, "limit") {
			t.Fatalf("%+v", p.Unknown)
		}
	})
	t.Run("symlink escaping the directory", func(t *testing.T) {
		dir, outside := t.TempDir(), t.TempDir()
		write(t, filepath.Join(outside, "evil.json"), `{"disableSideloadFlags":true}`)
		if err := os.Symlink(filepath.Join(outside, "evil.json"), filepath.Join(dir, "managed-settings.json")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		p := detect(t, linuxOpt(dir))
		if p.DisableSideloadFlags != nil || !p.Unreadable || !hasUnknown(p, "leaves the managed directory") {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("symlink inside the directory", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "real.json"), `{"disableAllHooks":true}`)
		if err := os.Symlink(filepath.Join(dir, "real.json"), filepath.Join(dir, "managed-settings.json")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		p := detect(t, linuxOpt(dir))
		if p.DisableAllHooks == nil || !*p.DisableAllHooks {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("unreadable permission", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("needs POSIX permissions and a non-root user")
		}
		dir := t.TempDir()
		f := filepath.Join(dir, "managed-settings.json")
		write(t, f, `{}`)
		if err := os.Chmod(f, 0); err != nil {
			t.Fatal(err)
		}
		p := detect(t, linuxOpt(dir))
		if !p.Unreadable || !hasUnknown(p, "permission denied") {
			t.Fatalf("%+v", p.Unknown)
		}
	})
	t.Run("unreadable drop-in dir", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("needs POSIX permissions and a non-root user")
		}
		dir := t.TempDir()
		d := filepath.Join(dir, "managed-settings.d")
		write(t, filepath.Join(d, "a.json"), `{}`)
		if err := os.Chmod(d, 0); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(d, 0o755)
		p := detect(t, linuxOpt(dir))
		if !p.Unreadable {
			t.Fatalf("%+v", p.Unknown)
		}
	})
}

func TestMacOSPlist(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "managed-settings.json"), `{"disableAllHooks":true,"deniedMcpServers":[{"serverName":"file-one"}]}`)
	conv := func(out string, err error) func(context.Context, string) ([]byte, error) {
		return func(_ context.Context, path string) ([]byte, error) {
			if !strings.HasSuffix(path, "com.anthropic.claudecode.plist") {
				t.Errorf("plist path %q", path)
			}
			return []byte(out), err
		}
	}
	base := Options{GOOS: "darwin", ManagedDir: dir}

	t.Run("plist wins over the file (first-wins)", func(t *testing.T) {
		o := base
		o.ConvertPlist = conv(`{"disableSideloadFlags":true,"deniedMcpServers":[{"serverName":"plist-one"}]}`, nil)
		p := detect(t, o)
		if p.DisableSideloadFlags == nil || !*p.DisableSideloadFlags {
			t.Fatal("plist not read")
		}
		if p.DisableAllHooks != nil {
			t.Fatal("the file must be ignored under first-wins")
		}
		if len(p.DeniedMcpServers) != 2 {
			t.Fatalf("deniedMcpServers is read from every admin source: %+v", p.DeniedMcpServers)
		}
		var mdm Source
		for _, s := range p.Sources {
			if s.Kind == KindMDM {
				mdm = s
			}
		}
		if !mdm.Present || !mdm.Used || !strings.Contains(mdm.Location, "com.anthropic.claudecode") {
			t.Fatalf("%+v", mdm)
		}
	})
	t.Run("merge", func(t *testing.T) {
		o := base
		o.ConvertPlist = conv(`{"managedSourcesBehavior":"merge","disableSideloadFlags":false,"allowManagedHooksOnly":true,"strictKnownMarketplaces":[{"source":"github","repo":"p/p"}]}`, nil)
		write(t, filepath.Join(dir, "managed-settings.json"), `{"disableAllHooks":true,"disableSideloadFlags":true,"allowManagedHooksOnly":false,"strictKnownMarketplaces":[{"source":"github","repo":"f/f"}],"deniedMcpServers":[{"serverName":"file-one"}],"permissions":{"disableBypassPermissionsMode":"disable"}}`)
		p := detect(t, o)
		if p.ManagedSourcesBehavior != "merge" {
			t.Fatal(p.ManagedSourcesBehavior)
		}
		if p.DisableAllHooks == nil || !*p.DisableAllHooks {
			t.Fatal("merge applies the file too")
		}
		if p.DisableSideloadFlags == nil || !*p.DisableSideloadFlags {
			t.Fatal("locks take the strictest value")
		}
		if len(p.StrictKnownMarketplaces) != 1 || p.StrictKnownMarketplaces[0].Ref != "p/p" {
			t.Fatalf("allowlists come whole from the highest source: %+v", p.StrictKnownMarketplaces)
		}
		if p.DisableBypassPermissionsMode == nil || !*p.DisableBypassPermissionsMode {
			t.Fatal("permissions merge")
		}
	})
	t.Run("absent plist", func(t *testing.T) {
		o := base
		o.ConvertPlist = conv("", fs.ErrNotExist)
		p := detect(t, o)
		if p.Unreadable {
			t.Fatal("absent is not unknown")
		}
	})
	t.Run("plutil failure is Unknown, not an error", func(t *testing.T) {
		o := base
		o.ConvertPlist = conv("", errors.New("plutil failed: exit status 1"))
		p := detect(t, o)
		if !p.Unreadable || !hasUnknown(p, "plutil failed") {
			t.Fatalf("%+v", p.Unknown)
		}
	})
	t.Run("bad plist JSON", func(t *testing.T) {
		o := base
		o.ConvertPlist = conv("[1]", nil)
		p := detect(t, o)
		if !p.Unreadable || len(p.Warnings) == 0 {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("oversized plist", func(t *testing.T) {
		o := base
		o.ConvertPlist = conv(strings.Repeat(" ", maxDocSize+1), nil)
		if p := detect(t, o); !p.Unreadable {
			t.Fatal("oversized output must be unreadable")
		}
	})
	t.Run("custom plist path", func(t *testing.T) {
		o := base
		o.PlistPath = "/x/y/com.anthropic.claudecode.plist"
		o.ConvertPlist = conv(`{}`, nil)
		detect(t, o)
	})
}

func TestRunPlutil(t *testing.T) {
	if _, err := runPlutil(context.Background(), filepath.Join(t.TempDir(), "absent.plist")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("absent plist: %v", err)
	}
	if runtime.GOOS != "darwin" {
		t.Skip("plutil exists only on macOS")
	}
	f := filepath.Join(t.TempDir(), "x.plist")
	write(t, f, `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>disableSideloadFlags</key><true/></dict></plist>`)
	out, err := runPlutil(context.Background(), f)
	if err != nil || !strings.Contains(string(out), "disableSideloadFlags") {
		t.Fatalf("%s %v", out, err)
	}
	write(t, f, "not a plist")
	if _, err := runPlutil(context.Background(), f); err == nil {
		t.Fatal("malformed plist must fail")
	}
}

func TestWindowsRegistry(t *testing.T) {
	notFound := func(Hive) (string, error) { return "", fs.ErrNotExist }
	reg := func(hklm, hkcu string) func(Hive) (string, error) {
		return func(h Hive) (string, error) {
			v := hklm
			if h == HKCU {
				v = hkcu
			}
			if v == "<absent>" {
				return "", fs.ErrNotExist
			}
			if v == "<error>" {
				return "", errors.New("access denied")
			}
			return v, nil
		}
	}
	t.Run("HKLM", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "managed-settings.json"), `{"disableAllHooks":true}`)
		p := detect(t, Options{GOOS: "windows", ManagedDir: dir, ReadRegistry: reg(`{"disableSideloadFlags":true}`, "<absent>")})
		if p.DisableSideloadFlags == nil || !*p.DisableSideloadFlags || p.DisableAllHooks != nil {
			t.Fatalf("HKLM must outrank the file: %+v", p)
		}
		found := false
		for _, s := range p.Sources {
			if s.Kind == KindRegistry && s.Present && s.Location == `HKLM\SOFTWARE\Policies\ClaudeCode\Settings` && s.Used {
				found = true
			}
		}
		if !found {
			t.Fatalf("%+v", p.Sources)
		}
	})
	t.Run("file only, no registry", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "managed-settings.json"), `{"disableAllHooks":true}`)
		p := detect(t, Options{GOOS: "windows", ManagedDir: dir, ReadRegistry: notFound})
		if p.DisableAllHooks == nil || p.Unreadable {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("HKCU fallback only when no admin document", func(t *testing.T) {
		p := detect(t, Options{GOOS: "windows", ManagedDir: t.TempDir(), ReadRegistry: reg("<absent>", `{"disableSideloadFlags":true}`)})
		if p.DisableSideloadFlags == nil || !*p.DisableSideloadFlags {
			t.Fatal("HKCU fallback not used")
		}
		dir := t.TempDir()
		write(t, filepath.Join(dir, "managed-settings.json"), `{"disableAllHooks":true}`)
		p = detect(t, Options{GOOS: "windows", ManagedDir: dir, ReadRegistry: reg("<absent>", `{"disableSideloadFlags":true}`)})
		if p.DisableSideloadFlags != nil {
			t.Fatal("HKCU must never apply beneath an admin document")
		}
		// An unreadable admin document also blocks HKCU.
		write(t, filepath.Join(dir, "managed-settings.json"), `{`)
		p = detect(t, Options{GOOS: "windows", ManagedDir: dir, ReadRegistry: reg("<absent>", `{"disableSideloadFlags":true}`)})
		if p.DisableSideloadFlags != nil || !p.Unreadable {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("malformed HKLM", func(t *testing.T) {
		p := detect(t, Options{GOOS: "windows", ManagedDir: t.TempDir(), ReadRegistry: reg(`oops`, "<absent>")})
		if !p.Unreadable || len(p.Warnings) == 0 {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("empty HKLM", func(t *testing.T) {
		p := detect(t, Options{GOOS: "windows", ManagedDir: t.TempDir(), ReadRegistry: reg(" ", "<absent>")})
		if !p.Unreadable {
			t.Fatal("empty HKLM value is an error in Claude Code")
		}
	})
	t.Run("HKLM read error", func(t *testing.T) {
		p := detect(t, Options{GOOS: "windows", ManagedDir: t.TempDir(), ReadRegistry: reg("<error>", "<absent>")})
		if !p.Unreadable || !hasUnknown(p, "access denied") {
			t.Fatalf("%+v", p.Unknown)
		}
	})
	t.Run("broken HKCU never blocks", func(t *testing.T) {
		for _, v := range []string{"oops", "<error>", " "} {
			p := detect(t, Options{GOOS: "windows", ManagedDir: t.TempDir(), ReadRegistry: reg("<absent>", v)})
			if p.Unreadable {
				t.Fatalf("HKCU %q must not make the policy unreadable", v)
			}
		}
	})
	t.Run("default reader off Windows is Unknown", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("real registry")
		}
		p := detect(t, Options{GOOS: "windows", ManagedDir: t.TempDir()})
		if !p.Unreadable {
			t.Fatal("stub must report unknown")
		}
	})
}

func TestWSL(t *testing.T) {
	linux, win := t.TempDir(), t.TempDir()
	write(t, filepath.Join(linux, "managed-settings.json"), `{"disableAllHooks":true}`)
	yes := true
	opt := Options{GOOS: "linux", ManagedDir: linux, WindowsDir: win, WSL: &yes}

	t.Run("not inherited", func(t *testing.T) {
		write(t, filepath.Join(win, "managed-settings.json"), `{"disableSideloadFlags":true}`)
		p := detect(t, opt)
		if p.DisableSideloadFlags != nil || p.DisableAllHooks == nil {
			t.Fatalf("WSL reads only /etc/claude-code by default: %+v", p)
		}
		if !hasUnknown(p, "registry") {
			t.Fatal("registry cannot be read from WSL")
		}
	})
	t.Run("inherited", func(t *testing.T) {
		write(t, filepath.Join(win, "managed-settings.json"), `{"wslInheritsWindowsSettings":true,"disableSideloadFlags":true}`)
		p := detect(t, opt)
		if p.WSLInheritsWindowsSettings == nil || !*p.WSLInheritsWindowsSettings {
			t.Fatal("flag not reported")
		}
		if p.DisableSideloadFlags == nil || !*p.DisableSideloadFlags || p.DisableAllHooks != nil {
			t.Fatalf("the Windows chain wins and /etc is ignored when a Windows admin document exists: %+v", p)
		}
	})
	t.Run("inherited but Windows file only has the control key", func(t *testing.T) {
		write(t, filepath.Join(win, "managed-settings.json"), `{"wslInheritsWindowsSettings":true}`)
		p := detect(t, opt)
		if p.DisableAllHooks == nil {
			t.Fatal("a control-key-only document does not count; /etc applies")
		}
	})
	t.Run("invalid value counts as on", func(t *testing.T) {
		write(t, filepath.Join(win, "managed-settings.json"), `{"wslInheritsWindowsSettings":"later","disableSideloadFlags":true}`)
		p := detect(t, opt)
		if p.DisableSideloadFlags == nil {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("inherit false", func(t *testing.T) {
		write(t, filepath.Join(win, "managed-settings.json"), `{"wslInheritsWindowsSettings":false,"disableSideloadFlags":true}`)
		p := detect(t, opt)
		if p.DisableSideloadFlags != nil || p.WSLInheritsWindowsSettings == nil || *p.WSLInheritsWindowsSettings {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("not WSL", func(t *testing.T) {
		no := false
		o := opt
		o.WSL = &no
		write(t, filepath.Join(win, "managed-settings.json"), `{"wslInheritsWindowsSettings":true,"disableSideloadFlags":true}`)
		if p := detect(t, o); p.DisableSideloadFlags != nil {
			t.Fatal("Windows folder must not be read outside WSL")
		}
	})
}

func TestOptionsDefaults(t *testing.T) {
	o := Options{}
	if o.goos() != runtime.GOOS {
		t.Fatal("goos default")
	}
	yes := true
	if (Options{WSL: &yes}).isWSL("darwin") {
		t.Fatal("WSL only exists on linux")
	}
	if (Options{}).isWSL("windows") {
		t.Fatal("windows is not WSL")
	}
	_ = (Options{}).isWSL("linux") // reads /proc, must not panic
	if orDefault("", "d") != "d" || orDefault("x", "d") != "x" {
		t.Fatal("orDefault")
	}
	if got := dedupe([]string{"a", "a", "b"}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatal(got)
	}
}
