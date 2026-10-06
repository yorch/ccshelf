package policy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func darwinOpt(dir, plist string) Options {
	return Options{GOOS: "darwin", ManagedDir: dir, ConvertPlist: func(context.Context, string) ([]byte, error) {
		return []byte(plist), nil
	}}
}

func sourceUsed(p *Policy, suffix string) (used, found bool) {
	for _, s := range p.Sources {
		if strings.HasSuffix(s.Location, suffix) {
			return s.Used, true
		}
	}
	return false, false
}

// P1 and P2: boolean handling of lock keys and plain keys.
func TestBooleanKeys(t *testing.T) {
	locks := map[string]func(*Policy) *bool{
		"disableSideloadFlags":            func(p *Policy) *bool { return p.DisableSideloadFlags },
		"allowManagedHooksOnly":           func(p *Policy) *bool { return p.AllowManagedHooksOnly },
		"allowManagedPermissionRulesOnly": func(p *Policy) *bool { return p.AllowManagedPermissionRulesOnly },
		"allowManagedMcpServersOnly":      func(p *Policy) *bool { return p.AllowManagedMcpServersOnly },
	}
	vals := []struct {
		json string
		want *bool // nil: the key reads as unset
		warn bool
	}{
		{`true`, bp(true), false},
		{`false`, bp(false), false},
		{`null`, nil, false},
		{`"true"`, bp(true), true},
		{`"false"`, bp(false), true},
		{`"True"`, bp(true), true},
		{`"yes"`, bp(true), true},
		{`""`, bp(true), true},
		{`1`, bp(true), true},
		{`0`, bp(true), true},
		{`[]`, bp(true), true},
		{`{}`, bp(true), true},
	}
	for key, get := range locks {
		for _, v := range vals {
			t.Run(key+"="+v.json, func(t *testing.T) {
				dir := t.TempDir()
				write(t, filepath.Join(dir, "managed-settings.json"), `{"`+key+`":`+v.json+`}`)
				p := detect(t, linuxOpt(dir))
				got := get(p)
				if (got == nil) != (v.want == nil) || (got != nil && *got != *v.want) {
					t.Fatalf("got %v, want %v", got, v.want)
				}
				if hasWarning(p, key) != v.warn {
					t.Fatalf("warning = %v, want %v: %v", hasWarning(p, key), v.warn, p.Warnings)
				}
			})
		}
	}
	plain := map[string]func(*Policy) *bool{
		"disableAllHooks":           func(p *Policy) *bool { return p.DisableAllHooks },
		"disableClaudeAiConnectors": func(p *Policy) *bool { return p.DisableClaudeAiConnectors },
		"allowAllClaudeAiMcps":      func(p *Policy) *bool { return p.AllowAllClaudeAiMcps },
	}
	for key, get := range plain {
		for _, v := range vals {
			want := v.want
			if v.warn && v.json != `"true"` && v.json != `"false"` {
				want = nil
			} else if v.json == `"true"` || v.json == `"false"` {
				want = nil // a quoted boolean is invalid for a non-lock key
			}
			t.Run(key+"="+v.json, func(t *testing.T) {
				dir := t.TempDir()
				write(t, filepath.Join(dir, "managed-settings.json"), `{"`+key+`":`+v.json+`}`)
				p := detect(t, linuxOpt(dir))
				got := get(p)
				if (got == nil) != (want == nil) || (got != nil && *got != *want) {
					t.Fatalf("got %v, want %v", got, want)
				}
				if v.json != "null" && v.json != "true" && v.json != "false" && !hasWarning(p, key) {
					t.Fatalf("a dropped value must warn: %v", p.Warnings)
				}
			})
		}
	}
	if b, ok := toBool("true"); ok || b {
		t.Fatal("toBool must only accept JSON booleans")
	}
	if b, ok := toBool(json1()); ok || b {
		t.Fatal("toBool must reject numbers")
	}
	if b, ok := toBool(true); !ok || !b {
		t.Fatal("toBool(true)")
	}
}

func json1() any { return float64(1) }

// P2: an invalid lock inside permissions reads as its restrictive value.
func TestInvalidBypassLock(t *testing.T) {
	for _, v := range []string{`"nope"`, `false`, `1`, `""`} {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "managed-settings.json"), `{"permissions":{"disableBypassPermissionsMode":`+v+`}}`)
		p := detect(t, linuxOpt(dir))
		if p.DisableBypassPermissionsMode == nil || !*p.DisableBypassPermissionsMode || !hasWarning(p, "disableBypassPermissionsMode") {
			t.Fatalf("%s: %+v", v, p.DisableBypassPermissionsMode)
		}
	}
}

// P8: an invalid lock value in a lower source cannot be overwritten by a
// higher false under merge.
func TestMergeFailClosedPerSource(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "managed-settings.json"), `{"managedSourcesBehavior":"merge","allowManagedHooksOnly":"maybe","disableSideloadFlags":"true"}`)
	o := darwinOpt(dir, `{"managedSourcesBehavior":"merge","allowManagedHooksOnly":false,"disableSideloadFlags":false,"disableAllHooks":true}`)
	p := detect(t, o)
	if p.ManagedSourcesBehavior != "merge" {
		t.Fatal(p.ManagedSourcesBehavior)
	}
	if p.AllowManagedHooksOnly == nil || !*p.AllowManagedHooksOnly {
		t.Fatal("invalid lower lock must stay true")
	}
	if p.DisableSideloadFlags == nil || !*p.DisableSideloadFlags {
		t.Fatal("lower true must stay true")
	}
}

func TestMergeBehaviors(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "managed-settings.json"), `{
 "managedSourcesBehavior":"merge",
 "deniedMcpServers":[{"serverName":"low"}],
 "blockedMarketplaces":[{"source":"github","repo":"low/x"}],
 "pluginSuggestionMarketplaces":["low"],
 "strictKnownMarketplaces":[{"source":"github","repo":"low/allowed"}],
 "allowedMcpServers":[{"serverName":"low-allowed"}],
 "managedMcpServers":{"a":{"command":"x"}},
 "permissions":{"disableBypassPermissionsMode":"disable"},
 "enabledPlugins":{"a@m":true}
}`)
	plist := `{
 "managedSourcesBehavior":"merge",
 "deniedMcpServers":[{"serverName":"high"}],
 "blockedMarketplaces":[{"source":"github","repo":"high/x"}],
 "pluginSuggestionMarketplaces":["high"],
 "strictKnownMarketplaces":[{"source":"github","repo":"high/allowed"}],
 "allowedMcpServers":[{"serverName":"high-allowed"}],
 "managedMcpServers":{"b":{"command":"y"}},
 "permissions":{"defaultMode":"plan"},
 "enabledPlugins":{"b@m":true}
}`
	p := detect(t, darwinOpt(dir, plist))
	if len(p.DeniedMcpServers) != 2 || len(p.BlockedMarketplaces) != 2 || len(p.PluginSuggestionMarketplaces) != 2 {
		t.Fatalf("lists must combine: %+v", p)
	}
	if len(p.StrictKnownMarketplaces) != 1 || p.StrictKnownMarketplaces[0].Ref != "high/allowed" || len(p.AllowedMcpServers) != 1 || p.AllowedMcpServers[0].Value != "high-allowed" {
		t.Fatalf("allowlists come whole from the highest source: %+v %+v", p.StrictKnownMarketplaces, p.AllowedMcpServers)
	}
	if !reflect.DeepEqual(p.ManagedMcpServers, []string{"a", "b"}) {
		t.Fatalf("managedMcpServers: %v", p.ManagedMcpServers)
	}
	if p.DisableBypassPermissionsMode == nil || !*p.DisableBypassPermissionsMode {
		t.Fatal("a lower disable lock must survive a higher permissions block")
	}
	if len(p.EnabledPlugins) != 1 || !p.EnabledPlugins["b@m"] {
		t.Fatalf("other keys take the highest source's value whole: %v", p.EnabledPlugins)
	}
	for _, s := range p.Sources {
		if s.Present && !s.Used && s.Kind != KindFile {
			t.Fatalf("every admin source is used under merge: %+v", s)
		}
	}
}

func TestBehaviorSelection(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "managed-settings.json"), `{"managedSourcesBehavior":"merge","disableAllHooks":true}`)
	for _, v := range []string{`"first-wins"`, `"bogus"`, `""`, `5`} {
		p := detect(t, darwinOpt(dir, `{"managedSourcesBehavior":`+v+`,"disableSideloadFlags":true}`))
		if p.ManagedSourcesBehavior != "first-wins" || p.DisableAllHooks != nil {
			t.Fatalf("%s: only \"merge\" selects merge: %+v", v, p)
		}
	}
	// The highest-ranked source that carries a policy key decides; a lower
	// source asking for merge is ignored.
	p := detect(t, darwinOpt(dir, `{"disableSideloadFlags":true}`))
	if p.ManagedSourcesBehavior != "first-wins" {
		t.Fatal("a lower source must not switch the behavior")
	}
	if used, found := sourceUsed(p, "managed-settings.json"); !found || used {
		t.Fatalf("lower tier must be unused under first-wins: used=%v found=%v", used, found)
	}
	if used, _ := sourceUsed(p, "com.anthropic.claudecode.plist)"); !used {
		t.Fatal("plist tier must be used")
	}
	// A control-key-only top source defers to the next one.
	p = detect(t, darwinOpt(dir, `{"wslInheritsWindowsSettings":true}`))
	if p.ManagedSourcesBehavior != "merge" {
		t.Fatalf("control-key-only document does not decide: %s", p.ManagedSourcesBehavior)
	}
}

// Mutation: the cross-source pull of allowedMcpServers under the MCP lock.
func TestCrossSourceKeys(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "managed-settings.json"), `{"allowManagedMcpServersOnly":true,"allowedMcpServers":[{"serverName":"from-file"}],"disableClaudeAiConnectors":true,"allowAllClaudeAiMcps":true,"deniedMcpServers":[{"serverName":"d-file"}]}`)
	p := detect(t, darwinOpt(dir, `{"disableAllHooks":true,"deniedMcpServers":[{"serverName":"d-plist"}]}`))
	if p.DisableAllHooks == nil {
		t.Fatal("plist wins")
	}
	if p.AllowManagedMcpServersOnly == nil || !*p.AllowManagedMcpServersOnly || p.DisableClaudeAiConnectors == nil || p.AllowAllClaudeAiMcps == nil {
		t.Fatalf("keys read from every admin source: %+v", p)
	}
	if len(p.AllowedMcpServers) != 1 || p.AllowedMcpServers[0].Value != "from-file" {
		t.Fatalf("allowedMcpServers is pulled in when the MCP lock is on: %+v", p.AllowedMcpServers)
	}
	if len(p.DeniedMcpServers) != 2 {
		t.Fatalf("%+v", p.DeniedMcpServers)
	}
	// Without the lock the lower allowlist is not pulled in.
	write(t, filepath.Join(dir, "managed-settings.json"), `{"allowedMcpServers":[{"serverName":"from-file"}]}`)
	p = detect(t, darwinOpt(dir, `{"disableAllHooks":true}`))
	if p.AllowedMcpServers != nil || p.AllowManagedMcpServersOnly != nil {
		t.Fatalf("not pulled without the lock: %+v", p)
	}
	// A false lock in a lower source does not set the key.
	write(t, filepath.Join(dir, "managed-settings.json"), `{"allowManagedMcpServersOnly":false,"allowedMcpServers":[{"serverName":"x"}]}`)
	p = detect(t, darwinOpt(dir, `{"disableAllHooks":true}`))
	if p.AllowManagedMcpServersOnly != nil || p.AllowedMcpServers != nil {
		t.Fatalf("%+v", p)
	}
}

// Mutation: drop-in entries of extraKnownMarketplaces and managedMcpServers
// replace whole, not key by key.
func TestDropInWholeEntryReplace(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "managed-settings.json"), `{"extraKnownMarketplaces":{"a":{"source":{"source":"github","repo":"x/1"},"extra":1},"keep":{"source":{"source":"github","repo":"k/k"}}}}`)
	write(t, filepath.Join(dir, "managed-settings.d", "10.json"), `{"extraKnownMarketplaces":{"a":{"source":{"source":"url","url":"https://h.example/m.json"}}}}`)
	p := detect(t, linuxOpt(dir))
	got := p.ExtraKnownMarketplaces["a"]
	if got.Kind != "url" || got.Ref != "https://h.example/m.json" {
		t.Fatalf("entry must be replaced whole: %+v", got)
	}
	if p.ExtraKnownMarketplaces["keep"].Ref != "k/k" {
		t.Fatalf("other entries stay: %+v", p.ExtraKnownMarketplaces)
	}

	dst := map[string]any{"managedMcpServers": map[string]any{"a": map[string]any{"command": "x", "args": []any{"1"}}}, "other": map[string]any{"k": 1.0, "n": map[string]any{"a": 1.0}}}
	src := map[string]any{"managedMcpServers": map[string]any{"a": map[string]any{"url": "y"}}, "other": map[string]any{"n": map[string]any{"b": 2.0}}}
	mergeInto(dst, src, true)
	if !reflect.DeepEqual(dst["managedMcpServers"], map[string]any{"a": map[string]any{"url": "y"}}) {
		t.Fatalf("%v", dst["managedMcpServers"])
	}
	if !reflect.DeepEqual(dst["other"], map[string]any{"k": 1.0, "n": map[string]any{"a": 1.0, "b": 2.0}}) {
		t.Fatalf("nested blocks merge key by key: %v", dst["other"])
	}
}

// P5: aliases are normalized per document, before merging.
func TestAliases(t *testing.T) {
	t.Run("drop-in alias combines with the earlier canonical list", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "managed-settings.json"), `{"strictKnownMarketplaces":[{"source":"github","repo":"a/old"}]}`)
		write(t, filepath.Join(dir, "managed-settings.d", "10.json"), `{"allowedMarketplaces":[{"source":"github","repo":"b/new"}],"additionalMarketplaces":{"m":{"source":{"source":"github","repo":"c/d"}}}}`)
		p := detect(t, linuxOpt(dir))
		// Lists combine across a file and its drop-ins, which only works
		// when the alias was renamed before merging.
		if len(p.StrictKnownMarketplaces) != 2 || p.StrictKnownMarketplaces[1].Ref != "b/new" {
			t.Fatalf("%+v", p.StrictKnownMarketplaces)
		}
		if p.ExtraKnownMarketplaces["m"].Ref != "c/d" {
			t.Fatalf("%+v", p.ExtraKnownMarketplaces)
		}
		if len(p.Other) != 0 {
			t.Fatalf("aliases are known keys: %v", p.Other)
		}
	})
	t.Run("both spellings in one file: canonical wins", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "managed-settings.json"), `{"allowedMarketplaces":[{"source":"github","repo":"alias/x"}],"strictKnownMarketplaces":[{"source":"github","repo":"canon/x"}]}`)
		p := detect(t, linuxOpt(dir))
		if len(p.StrictKnownMarketplaces) != 1 || p.StrictKnownMarketplaces[0].Ref != "canon/x" || !hasWarning(p, "alias") {
			t.Fatalf("%+v %v", p.StrictKnownMarketplaces, p.Warnings)
		}
	})
	t.Run("first-wins across tiers sees the canonical key", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "managed-settings.json"), `{"strictKnownMarketplaces":[{"source":"github","repo":"low/x"}]}`)
		p := detect(t, darwinOpt(dir, `{"allowedMarketplaces":[]}`))
		if p.StrictKnownMarketplaces == nil || len(p.StrictKnownMarketplaces) != 0 {
			t.Fatalf("the plist alias must win and block everything: %+v", p.StrictKnownMarketplaces)
		}
	})
}

// P6: a FIFO is never opened.
func TestFIFONeverBlocks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("FIFOs are Unix only")
	}
	dir := t.TempDir()
	if err := mkfifo(filepath.Join(dir, "managed-settings.json")); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	if err := mkfifo(filepath.Join(dir, "managed-settings.d-target")); err != nil {
		t.Skip(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "managed-settings.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "managed-settings.d-target"), filepath.Join(dir, "managed-settings.d", "10-link.json")); err != nil {
		t.Skip(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	p, err := Detect(ctx, linuxOpt(dir))
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("Detect blocked on a FIFO")
	}
	if !p.Unreadable || !hasUnknown(p, "not a regular file") || !hasUnknown(p, "does not point to a regular file") {
		t.Fatalf("%v", p.Unknown)
	}
}

func TestReadFileHonorsContext(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.json"), `{}`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := &detector{ctx: ctx, p: &Policy{}}
	// With both the read result and the cancellation ready either may win;
	// the call must simply return promptly with a sane outcome.
	data, err := d.readFile(dir, filepath.Join(dir, "a.json"))
	if err == nil && string(data) != "{}" {
		t.Fatalf("%q", data)
	}
}

// P7: symlinks that leave the managed directory.
func TestSymlinkOutsideOwnership(t *testing.T) {
	mk := func(t *testing.T, mode os.FileMode) (dir, target string) {
		t.Helper()
		dir, outside := t.TempDir(), t.TempDir()
		target = filepath.Join(outside, "policy.json")
		write(t, target, `{"disableSideloadFlags":true}`)
		if err := os.Chmod(target, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, "managed-settings.json")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		return dir, target
	}
	owner := func(uid uint32, ok bool) func(fi os.FileInfo) (uint32, bool) {
		return func(os.FileInfo) (uint32, bool) { return uid, ok }
	}
	t.Run("root-owned and not writable by others: followed", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX modes")
		}
		dir, _ := mk(t, 0o644)
		o := linuxOpt(dir)
		o.FileOwner = owner(0, true)
		p := detect(t, o)
		if p.DisableSideloadFlags == nil || !*p.DisableSideloadFlags || p.Unreadable {
			t.Fatalf("%+v", p)
		}
	})
	for name, tc := range map[string]struct {
		mode os.FileMode
		uid  uint32
		ok   bool
	}{
		"owned by another user": {0o644, 1000, true},
		"group writable":        {0o664, 0, true},
		"world writable":        {0o646, 0, true},
		"owner unknown":         {0o644, 0, false},
	} {
		t.Run(name, func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Skip("POSIX modes")
			}
			dir, _ := mk(t, tc.mode)
			o := linuxOpt(dir)
			o.FileOwner = owner(tc.uid, tc.ok)
			p := detect(t, o)
			if p.DisableSideloadFlags != nil || !p.Unreadable || !hasUnknown(p, "owned by root") {
				t.Fatalf("%+v", p.Unknown)
			}
			m := Evaluate(p, nil)
			if m.Features[PluginDir].State != Unknown {
				t.Fatalf("an unreadable source makes features unknown: %+v", m.Features[PluginDir])
			}
		})
	}
	t.Run("default owner check refuses a file owned by the test user", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("needs a non-root POSIX user")
		}
		dir, _ := mk(t, 0o644)
		p := detect(t, linuxOpt(dir))
		if p.DisableSideloadFlags != nil || !p.Unreadable {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("dangling link is unreadable, not absent", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Symlink(filepath.Join(dir, "nowhere.json"), filepath.Join(dir, "managed-settings.json")); err != nil {
			t.Skip(err)
		}
		p := detect(t, linuxOpt(dir))
		if !p.Unreadable || !hasUnknown(p, "cannot be resolved") {
			t.Fatalf("%v", p.Unknown)
		}
	})
	t.Run("link to a directory", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(dir, "d"), filepath.Join(dir, "managed-settings.json")); err != nil {
			t.Skip(err)
		}
		p := detect(t, linuxOpt(dir))
		if !p.Unreadable || !hasUnknown(p, "regular file") {
			t.Fatalf("%v", p.Unknown)
		}
	})
}

// Mutation: a permission-denied read is Unknown but is not also a warning.
func TestPermissionDeniedHasNoWarning(t *testing.T) {
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
	if !p.Unreadable || len(p.Warnings) != 0 {
		t.Fatalf("unreadable=%v warnings=%v", p.Unreadable, p.Warnings)
	}
}

// Mutation: WSL, an unreadable Windows document counts as present.
func TestWSLUnreadableWindowsDocument(t *testing.T) {
	linux, win := t.TempDir(), t.TempDir()
	write(t, filepath.Join(linux, "managed-settings.json"), `{"disableAllHooks":true}`)
	write(t, filepath.Join(win, "managed-settings.json"), `{"wslInheritsWindowsSettings":true}`)
	write(t, filepath.Join(win, "managed-settings.d", "10-bad.json"), `{`)
	yes := true
	p := detect(t, Options{GOOS: "linux", ManagedDir: linux, WindowsDir: win, WSL: &yes})
	if p.DisableAllHooks != nil || !p.Unreadable {
		t.Fatalf("an unreadable Windows document must hide /etc: %+v", p)
	}
}

// P4: partial visibility.
func TestPartialVisibility(t *testing.T) {
	yes := true
	t.Run("WSL without a Windows folder", func(t *testing.T) {
		p := detect(t, Options{GOOS: "linux", ManagedDir: t.TempDir(), WindowsDir: filepath.Join(t.TempDir(), "none"), WSL: &yes})
		if !p.PartialVisibility || len(p.PartialReasons) != 1 {
			t.Fatalf("%+v", p)
		}
		m := Evaluate(p, nil)
		for _, id := range []FeatureID{PluginDir, Agents, AddMCPConfig, StrictMCPConfig, SettingSources, ForcedPlugins} {
			if m.Features[id].State != Unknown || !strings.Contains(m.Features[id].Reason, "partially visible") {
				t.Errorf("%s: %+v", id, m.Features[id])
			}
		}
		for _, id := range []FeatureID{SettingsMasking, HideConnectors, DenyMCPServers, AppendSystemPromptFile} {
			if m.Features[id].State != Available {
				t.Errorf("%s must stay available: %+v", id, m.Features[id])
			}
		}
		if !strings.Contains(m.Text(), "Partial visibility") || !strings.Contains(m.Text(), "Server-managed settings") {
			t.Fatal(m.Text())
		}
		b, _ := m.JSON()
		if !strings.Contains(string(b), `"partial_visibility": true`) || !strings.Contains(string(b), `"server_managed_readable": false`) {
			t.Fatal(string(b))
		}
	})
	t.Run("WSL inheriting Windows policy", func(t *testing.T) {
		win := t.TempDir()
		write(t, filepath.Join(win, "managed-settings.json"), `{"wslInheritsWindowsSettings":true,"disableAllHooks":true}`)
		p := detect(t, Options{GOOS: "linux", ManagedDir: t.TempDir(), WindowsDir: win, WSL: &yes})
		if !p.PartialVisibility || !strings.Contains(p.PartialReasons[0], "registry") {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("WSL with a readable Windows folder that does not inherit", func(t *testing.T) {
		win := t.TempDir()
		write(t, filepath.Join(win, "managed-settings.json"), `{"disableAllHooks":true}`)
		p := detect(t, Options{GOOS: "linux", ManagedDir: t.TempDir(), WindowsDir: win, WSL: &yes})
		if p.PartialVisibility {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("WSL with an unreadable Windows folder", func(t *testing.T) {
		win := t.TempDir()
		write(t, filepath.Join(win, "managed-settings.json"), `{`)
		p := detect(t, Options{GOOS: "linux", ManagedDir: t.TempDir(), WindowsDir: win, WSL: &yes})
		if !p.PartialVisibility {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("a visible blocked state beats unknown", func(t *testing.T) {
		m := Evaluate(&Policy{PartialVisibility: true, PartialReasons: []string{"x"}, DisableSideloadFlags: bp(true)}, nil)
		if m.Features[PluginDir].State != Blocked {
			t.Fatal(m.Features[PluginDir])
		}
	})
	t.Run("a fully visible machine without policy is available", func(t *testing.T) {
		p := detect(t, linuxOpt(t.TempDir()))
		if p.PartialVisibility {
			t.Fatal("not partial")
		}
		m := Evaluate(p, nil)
		for _, id := range AllFeatures {
			if m.Features[id].State != Available {
				t.Errorf("%s = %s", id, m.Features[id].State)
			}
		}
		b, _ := m.JSON()
		if !strings.Contains(string(b), `"partial_visibility": false`) {
			t.Fatal(string(b))
		}
	})
	t.Run("plan warns and attempts", func(t *testing.T) {
		m := Evaluate(&Policy{PartialVisibility: true, PartialReasons: []string{"WSL blind spot"}}, nil)
		a, err := m.Plan(Needs{PluginDir: true}, "fail")
		if err != nil || !a.PluginDir || !strings.Contains(strings.Join(a.Warnings, ""), "WSL blind spot") {
			t.Fatalf("%+v %v", a, err)
		}
	})
}

// P3: --strict-mcp-config and the MCP filters.
func TestStrictMCPConfig(t *testing.T) {
	t.Run("managed-mcp.json blocks both flags", func(t *testing.T) {
		m := Evaluate(&Policy{ManagedMCPFile: true}, nil)
		for _, id := range []FeatureID{AddMCPConfig, StrictMCPConfig} {
			f := m.Features[id]
			if f.State != Blocked || f.Source != "managed-mcp.json" || !strings.Contains(f.Reason, "exits at startup") {
				t.Errorf("%s: %+v", id, f)
			}
		}
		if !strings.Contains(strings.Join(m.Warnings, "\n"), "managed-mcp.json is deployed") {
			t.Fatalf("%v", m.Warnings)
		}
		a, err := m.Plan(Needs{StrictMCPConfig: true}, "warn")
		if err != nil || a.StrictMCPConfig || len(a.Dropped) != 1 || a.Dropped[0] != StrictMCPConfig {
			t.Fatalf("%+v %v", a, err)
		}
		_, err = m.Plan(Needs{StrictMCPConfig: true}, "fail")
		var be *BlockedError
		if !errors.As(err, &be) || be.Feature != StrictMCPConfig {
			t.Fatalf("%v", err)
		}
	})
	t.Run("sideload flags disabled", func(t *testing.T) {
		m := Evaluate(&Policy{DisableSideloadFlags: bp(true)}, nil)
		if m.Features[StrictMCPConfig].State != Blocked || m.Features[StrictMCPConfig].Source != "disableSideloadFlags" {
			t.Fatalf("%+v", m.Features[StrictMCPConfig])
		}
	})
	t.Run("unreadable", func(t *testing.T) {
		if Evaluate(&Policy{Unreadable: true}, nil).Features[StrictMCPConfig].State != Unknown {
			t.Fatal("unknown")
		}
	})
	t.Run("open", func(t *testing.T) {
		m := Evaluate(nil, nil)
		a, err := m.Plan(Needs{StrictMCPConfig: true, ExtraMCPServers: true}, "fail")
		if err != nil || !a.StrictMCPConfig || !a.ExtraMCPServers || len(a.Warnings) != 0 {
			t.Fatalf("%+v %v", a, err)
		}
	})
	t.Run("filters warn when servers are added", func(t *testing.T) {
		p := &Policy{AllowedMcpServers: []ServerRule{{"serverName", "x"}}, AllowManagedMcpServersOnly: bp(true), DeniedMcpServers: []ServerRule{{"serverName", "y"}}}
		m := Evaluate(p, nil)
		if f := m.Features[AddMCPConfig]; f.State != Available || !strings.Contains(f.Reason, "allowedMcpServers, allowManagedMcpServersOnly, deniedMcpServers") {
			t.Fatalf("%+v", f)
		}
		a, err := m.Plan(Needs{ExtraMCPServers: true}, "warn")
		if err != nil || !a.ExtraMCPServers || !strings.Contains(strings.Join(a.Warnings, ""), "will filter the MCP servers") {
			t.Fatalf("%+v %v", a, err)
		}
		a, _ = m.Plan(Needs{HideConnectors: true}, "warn")
		if len(a.Warnings) != 0 {
			t.Fatalf("no warning when the profile adds no servers: %v", a.Warnings)
		}
		// allowManagedMcpServersOnly=false and empty deny list are not filters.
		m = Evaluate(&Policy{AllowManagedMcpServersOnly: bp(false), DeniedMcpServers: []ServerRule{}}, nil)
		if len(m.mcpFilters) != 0 {
			t.Fatalf("%v", m.mcpFilters)
		}
	})
}

// P9: rule entries, redaction and secrets.
func TestServerRuleValidation(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "managed-settings.json"), `{"allowedMcpServers":[
 {"serverName":5},{"serverName":""},{"serverName":"ok"},
 {"serverUrl":["x"]},{"serverUrl":"//u:SECRET@h.example/x?k=SECRET2"},
 {"serverCommand":"npx"},{"serverCommand":[1]},{"serverCommand":["npx"]},
 "text",{"other":1}
]}`)
	p := detect(t, linuxOpt(dir))
	want := []ServerRule{{"serverName", "ok"}, {"serverUrl", "//h.example/x"}, {"serverCommand", "npx"}}
	if !reflect.DeepEqual(p.AllowedMcpServers, want) {
		t.Fatalf("%+v", p.AllowedMcpServers)
	}
	if !hasWarning(p, "7 entries that are not valid rules") {
		t.Fatalf("%v", p.Warnings)
	}
	b, _ := Evaluate(p, nil).JSON()
	if strings.Contains(string(b), "SECRET") || strings.Contains(Evaluate(p, nil).Text(), "SECRET") {
		t.Fatal("secret leaked")
	}
}

func TestRedactURLForms(t *testing.T) {
	for in, out := range map[string]string{
		"//u:p@h.example/x":        "//h.example/x",
		"//u:p@h.example/x?q=1#f":  "//h.example/x",
		"//h.example/x?":           "//h.example/x",
		"https://h/p?":             "https://h/p",
		"user:pw@host.example/p":   "host.example/p",
		"plain/path":               "plain/path",
		"//[::1":                   "(unparseable url)",
		"https://u:p@h.example:8/": "https://h.example:8/",
	} {
		if got := redactURL(in); got != out {
			t.Errorf("redactURL(%q) = %q, want %q", in, got, out)
		}
	}
}

func FuzzRedactURL(f *testing.F) {
	f.Add("user", "pass", "host.example", "p", "q")
	f.Add("a", "b", "c", "", "token")
	f.Fuzz(func(t *testing.T, user, pass, host, path, query string) {
		for _, s := range []string{user, pass, host, path, query} {
			for _, r := range s {
				if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
					t.Skip()
				}
			}
		}
		if user == "" || pass == "" || host == "" || query == "" {
			t.Skip()
		}
		for _, in := range []string{
			"https://" + user + ":" + pass + "@" + host + "/" + path + "?k=" + query + "#" + query,
			"//" + user + ":" + pass + "@" + host + "/" + path + "?k=" + query,
			user + ":" + pass + "@" + host + "/" + path + "?k=" + query,
			"ssh://" + user + "@" + host + "/" + path + "?k=" + query,
			user + "@" + host + ":" + path + "?k=" + query,
		} {
			out := redactURL(in)
			if strings.ContainsAny(out, "?#") || strings.Contains(out, pass+"@") || strings.Contains(out, "="+query) {
				t.Fatalf("redactURL(%q) = %q leaks", in, out)
			}
			// The credentials must not survive anywhere in Policy output.
			d := &detector{p: &Policy{}}
			doc := map[string]any{
				"allowedMcpServers":       []any{map[string]any{"serverUrl": in}},
				"strictKnownMarketplaces": []any{map[string]any{"source": "url", "url": in}},
			}
			d.interpret(doc)
			b, _ := Evaluate(d.p, nil).JSON()
			if strings.Contains(string(b), pass+"@") || strings.Contains(string(b), "="+query) {
				t.Fatalf("output leaks for %q: %s", in, b)
			}
		}
	})
}

// P10: only lower-case .json drop-ins are read; hidden files are ignored.
func TestDropInExtensions(t *testing.T) {
	dir := t.TempDir()
	d := filepath.Join(dir, "managed-settings.d")
	write(t, filepath.Join(d, "10-ok.json"), `{"disableAllHooks":true}`)
	write(t, filepath.Join(d, "20-upper.JSON"), `{"disableSideloadFlags":true}`)
	write(t, filepath.Join(d, ".30-hidden.json"), `{"allowManagedHooksOnly":true}`)
	write(t, filepath.Join(d, "40-note.txt"), `not json`)
	write(t, filepath.Join(d, "50-bak.json.bak"), `not json`)
	p := detect(t, linuxOpt(dir))
	if p.DisableAllHooks == nil || p.DisableSideloadFlags != nil || p.AllowManagedHooksOnly != nil || p.Unreadable {
		t.Fatalf("%+v", p)
	}
	for _, s := range p.Sources {
		if strings.Contains(s.Location, "JSON") || strings.Contains(s.Location, "hidden") {
			t.Fatalf("must not be listed: %+v", s)
		}
	}
}

func TestMacOSPerUserPlistIsUnknown(t *testing.T) {
	p := detect(t, darwinOpt(t.TempDir(), `{}`))
	if !hasUnknown(p, "per-user managed preferences") {
		t.Fatalf("%v", p.Unknown)
	}
	if hasUnknown(detect(t, linuxOpt(t.TempDir())), "per-user managed preferences") {
		t.Fatal("macOS only")
	}
}
