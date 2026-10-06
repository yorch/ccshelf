package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadRejectsCaseVariantKeys(t *testing.T) {
	isolate(t)
	abs := filepath.ToSlash(filepath.Join(t.TempDir(), "acct"))
	cases := []struct{ name, body, want string }{
		{"top level", "Default_Account = \"w\"\n[accounts.w]\nconfig_dir = \"" + abs + "\"\n", `key "Default_Account" must be spelled "default_account"`},
		{"trust table", "[Trust]\nrequire_pin = true\n", `key "Trust" must be spelled "trust"`},
		{"trust key", "[trust]\nRequire_Pin = false\n", `key "Require_Pin" must be spelled "require_pin"`},
		{"override", "[trust]\nrequire_pin = true\nREQUIRE_PIN = false\n", `must be spelled "require_pin"`},
		{"ui table", "[UI]\ncolor = \"never\"\n", `must be spelled "ui"`},
		{"ui key", "[ui]\nColor = \"never\"\n", `must be spelled "color"`},
		{"claude table", "[Claude]\npath = \"/x\"\n", `must be spelled "claude"`},
		{"claude key", "[claude]\nPath = \"/x\"\n", `must be spelled "path"`},
		{"sources table", "[[Sources]]\ntype = \"dir\"\npath = \"/x\"\n", `must be spelled "sources"`},
		{"sources key", "[[sources]]\nType = \"dir\"\npath = \"/x\"\n", `must be spelled "type"`},
		{"source second element", "[[sources]]\ntype = \"dir\"\npath = \"/x\"\n[[sources]]\ntype = \"dir\"\nPath = \"/y\"\n", `must be spelled "path"`},
		{"accounts table", "[Accounts.w]\nconfig_dir = \"" + abs + "\"\n", `must be spelled "accounts"`},
		{"account key", "[accounts.w]\nConfig_Dir = \"" + abs + "\"\n", `must be spelled "config_dir"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(write(t, tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v; want containing %q", err, tc.want)
			}
		})
	}
	// account names are free-form keys: spelling is checked by the name rule only
	if _, err := Load(write(t, "[accounts.work]\nconfig_dir = \""+abs+"\"\n")); err != nil {
		t.Errorf("exact spelling rejected: %v", err)
	}
}

func TestValidatePinRules(t *testing.T) {
	sha40 := strings.Repeat("a", 40)
	ok := []string{
		"v1.2.3", "v2026.10.1", "2026-10-01", "release-2026", "rel_1", "1.0.0+build.5", sha40, strings.ToUpper(sha40), strings.Repeat("0", 64),
		"a" + strings.Repeat("b", 127), "headset-1", "mainline-1", "x",
	}
	bad := []string{
		"", "feature/x", "release/2026.10", "refs/tags/v1", "refs/heads/main", "heads/main", "remotes/o/x", "origin/main",
		"refs", "refs-v1", "heads", "heads-x", "remotes", "remotes.x", "origin", "origin-x",
		"HEAD", "head", "FETCH_HEAD", "ORIG_HEAD", "MERGE_HEAD", "main", "Main", "master", "MASTER", "develop", "dev", "trunk", "release", "latest", "stable", "next", "Latest",
		"abcdef0", "abcdef01", "0123456", strings.Repeat("a", 39), strings.Repeat("A", 20), strings.Repeat("1", 8),
		"a b", "-x", "--upload-pack=x", ".hidden", "a..b", "v1.", "v1.lock", "a\nb", "a:b", "a~1", "a^", "a*", "a?", "a[b", `a\b`, "a@{1}", "é",
		"a" + strings.Repeat("b", 128),
	}
	for _, r := range ok {
		if err := ValidatePin(r); err != nil {
			t.Errorf("%q rejected: %v", r, err)
		}
	}
	for _, r := range bad {
		if err := ValidatePin(r); err == nil {
			t.Errorf("%q accepted", r)
		}
	}
	// the error says what to do
	if err := ValidatePin("abcdef0"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("short sha: %v", err)
	}
	if err := ValidatePin("feature/x"); err == nil || !strings.Contains(err.Error(), "branch") {
		t.Errorf("slash: %v", err)
	}
	// 40 and 64 hex digits are full ids; 7 to 39 and 41 to 63 hex digits are not "short ids" in the 41..63 case
	if err := ValidatePin(strings.Repeat("a", 41)); err != nil {
		t.Errorf("41 hex digits is a valid tag name: %v", err)
	}
	if err := ValidatePin(strings.Repeat("a", 6)); err != nil {
		t.Errorf("6 hex digits is below the short-id range: %v", err)
	}
}

func TestValidateGitURL(t *testing.T) {
	ok := []string{
		"https://github.com/acme/data.git", "https://ghe.example.com:8443/acme/data", "https://h.example/a",
		"ssh://git@github.com/acme/data.git", "ssh://github.com/acme/data.git", "ssh://git@h.example:2222/acme/data.git",
		"git@github.com:acme/data.git", "deploy_user@ghe.example.com:acme/data.git", "git@h.example:/srv/git/data.git",
	}
	bad := map[string]string{
		"":                                "need url",
		"-oProxyCommand=x":                "must not start",
		"--upload-pack=x":                 "must not start",
		"ext::sh -c touch /tmp/x":         "whitespace",
		"ext::sh-c-x":                     "transport",
		"fd::17":                          "transport",
		"EXT::x":                          "transport",
		"myhelper::arg":                   "transport",
		"file:///etc":                     "file:",
		"FILE:///etc":                     "file:",
		"/srv/git/data.git":               "must be https",
		"./data":                          "must be https",
		"../data":                         "must be https",
		"data.git":                        "must be https",
		`C:\repos\data`:                   "must be https",
		"git://h.example/a.git":           "must be https",
		"http://h.example/a.git":          "must be https",
		"HTTPS://h.example/a.git":         "must be https",
		"https://user@h.example/a.git":    "user information",
		"https://user:pw@h.example/a.git": "user information",
		"https://ghp_abc@h.example/a.git": "user information",
		"https://:@h.example/a.git":       "user information",
		"https://h.example":               "repository path",
		"https://h.example/":              "repository path",
		"https://h.example/a.git?x=1":     "query",
		"https://h.example/a.git#frag":    "query",
		"https:///a":                      "valid https",
		"ssh://git:pw@h.example/a.git":    "password",
		"ssh://git@h.example":             "repository path",
		"ssh://h.example/a.git?x=1":       "query",
		"ssh:///a":                        "valid ssh",
		"git@h.example":                   "must be https",
		"git@h.example:":                  "must be https",
		"git@-h.example:a.git":            "must be https",
		"https://h.example/a b.git":       "whitespace",
		"https://h.example/a\x00.git":     "whitespace",
		"https://h.example/a\n.git":       "whitespace",
		"https://h.example/a\u0085.git":   "whitespace",
		"git@h.example:a.git extra":       "whitespace",
		"git@h.example:a;rm":              "must be https",
		"git@h.example:$(x)":              "must be https",
		"ssh://git@h.example/a.git -o x":  "whitespace",
		"https://h.example/a.git\t":       "whitespace",
		"https://h.example/a.git\r":       "whitespace",
		"ftp://h.example/a.git":           "must be https",
		"https://h.example/%zz":           "valid https",
		"https://[::1/a.git":              "valid https",
		"ext::https://h.example/a.git":    "transport",
		"git+ssh://git@h.example/a.git":   "must be https",
		"ssh://git@h.example/a.git#frag":  "query",
		"https://h.example/a.git?":        "query",
		"ssh://git@h.example/a.git?":      "query",
		"git@h.example:a.git\u202e":       "whitespace",
		"git@h.example:a.git\u2028":       "whitespace",
		"git@h.example:a.git\u00a0":       "whitespace",
		"git@h.example:a.git\u0007":       "whitespace",
		"git@h.example:a.git\u007f":       "whitespace",
		"git@h.example:a.git\u0080":       "whitespace",
		"ssh://git@h.example/a.git\u0085": "whitespace",
	}
	for _, u := range ok {
		if err := ValidateGitURL(u); err != nil {
			t.Errorf("%q rejected: %v", u, err)
		}
	}
	for u, want := range bad {
		err := ValidateGitURL(u)
		if err == nil {
			t.Errorf("%q accepted", u)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v; want containing %q", u, err, want)
		}
	}
}

func TestLoadGitCredentialMarkers(t *testing.T) {
	isolate(t)
	for _, marker := range []string{"ghp_abcdef", "github_pat_11AAA", "glpat-xxxx", "xoxb-123", "GHP_UPPER"} {
		for label, body := range map[string]string{
			"url":  "[[sources]]\ntype = \"git\"\nurl = \"https://h.example/" + marker + ".git\"\nref = \"v1\"\n",
			"ref":  "[[sources]]\ntype = \"git\"\nurl = \"https://h.example/a.git\"\nref = \"v1-" + marker + "\"\n",
			"path": "[[sources]]\ntype = \"git\"\nurl = \"https://h.example/a.git\"\nref = \"v1\"\npath = \"" + marker + "\"\n",
			"name": "[[sources]]\ntype = \"git\"\nname = \"x\"\nurl = \"https://h.example/a.git\"\nref = \"v1\"\n",
		} {
			_, err := Load(write(t, body))
			if label == "name" {
				if err != nil {
					t.Errorf("clean source rejected: %v", err)
				}
				continue
			}
			if err == nil || !strings.Contains(err.Error(), "credential") {
				t.Errorf("%s with %s: %v", label, marker, err)
			}
		}
	}
	// the marker check also applies when pins are not required
	body := "[trust]\nrequire_pin = false\n[[sources]]\ntype = \"git\"\nurl = \"https://h.example/a.git\"\nref = \"x\"\npath = \"ghp_zzz\"\n"
	if _, err := Load(write(t, body)); err == nil || !strings.Contains(err.Error(), "credential") {
		t.Errorf("relaxed pin: %v", err)
	}
	if credentialMarker("nothing here") != "" || credentialMarker("a-GLPAT-b") != "glpat-" {
		t.Error("credentialMarker")
	}
}

func TestLoadGitURLThroughValidate(t *testing.T) {
	isolate(t)
	for _, u := range []string{"https://u:p@h.example/a.git", "file:///x", "/srv/git/x", "ext::sh", "-x", "https://tok@h.example/a.git"} {
		body := "[[sources]]\ntype = \"git\"\nurl = \"" + u + "\"\nref = \"v1\"\n"
		if _, err := Load(write(t, body)); err == nil {
			t.Errorf("%q accepted by Load", u)
		}
	}
}

func TestDirSourcePathMustNotBeRelative(t *testing.T) {
	isolate(t)
	abs := filepath.ToSlash(filepath.Join(t.TempDir(), "profiles"))
	for _, p := range []string{abs, "~", "~/profiles", "$HOME/profiles", "${HOME}/profiles"} {
		if _, err := Load(write(t, "[[sources]]\ntype = \"dir\"\npath = \""+p+"\"\n")); err != nil {
			t.Errorf("%q rejected: %v", p, err)
		}
	}
	for _, p := range []string{"profiles", "./profiles", "../profiles", ".ccshelf/profiles", "a/b", "~user/x", ".", ".."} {
		_, err := Load(write(t, "[[sources]]\ntype = \"dir\"\npath = \""+p+"\"\n"))
		if err == nil || !strings.Contains(err.Error(), "must be absolute or start with ~") {
			t.Errorf("%q: %v", p, err)
		}
	}
	// a variable that expands to a relative path is refused at resolution
	t.Setenv("CCSHELF_REL", "relative/dir")
	cfg, err := Load(write(t, "[[sources]]\ntype = \"dir\"\npath = \"$CCSHELF_REL\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Sources[0].ResolvedPath(); err == nil || !strings.Contains(err.Error(), "relative") {
		t.Errorf("ResolvedPath: %v", err)
	}
	if _, err := (SourceConfig{Type: SourceDir, Path: "$NOPE_UNSET_VAR"}).ResolvedPath(); err == nil {
		t.Error("unset variable accepted")
	}
	if err := checkDirSourcePath("a\x00b"); err == nil {
		t.Error("NUL accepted")
	}
}

func TestAccountDirectoryRules(t *testing.T) {
	home := isolate(t)
	base := t.TempDir()
	d := func(parts ...string) string { return filepath.Join(append([]string{base}, parts...)...) }
	claude := filepath.Join(home, ".claude")
	cfgWith := func(dirs map[string]string) *Config {
		c := Default()
		c.Accounts = map[string]Account{}
		for n, p := range dirs {
			c.Accounts[n] = Account{ConfigDir: p}
		}
		return c
	}
	bad := []struct {
		name string
		dirs map[string]string
		want string
	}{
		{"duplicate", map[string]string{"a": d("x"), "b": d("x")}, "same directory"},
		{"duplicate unclean", map[string]string{"a": d("x"), "b": d("y", "..", "x")}, "same directory"},
		{"ancestor", map[string]string{"a": d("x"), "b": d("x", "y")}, "nested"},
		{"descendant", map[string]string{"a": d("x", "y"), "b": d("x")}, "nested"},
		{"deep descendant", map[string]string{"a": d("x"), "b": d("x", "y", "z")}, "nested"},
		{"default dir", map[string]string{"a": claude}, "default directory"},
		{"inside default", map[string]string{"a": filepath.Join(claude, "work")}, "inside Claude Code's default"},
		{"contains default", map[string]string{"a": home}, "contain or sit inside"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			err := cfgWith(tc.dirs).Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v; want containing %q", err, tc.want)
			}
		})
	}
	ok := map[string]string{"a": d("x"), "b": d("xy"), "c": d("x-2"), "d": d("z", "w")}
	if err := cfgWith(ok).Validate(); err != nil {
		t.Errorf("sibling directories rejected: %v", err)
	}
	// a name prefix is not nesting
	if err := cfgWith(map[string]string{"a": d("acct"), "b": d("acct2")}).Validate(); err != nil {
		t.Errorf("prefix names: %v", err)
	}
}

func TestAccountDirectoryRulesFollowSymlinks(t *testing.T) {
	home := isolate(t)
	base := t.TempDir()
	claude := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claude, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "looks-harmless")
	if err := os.Symlink(claude, link); err != nil {
		t.Skip("symlinks unavailable: " + err.Error())
	}
	c := Default()
	c.Accounts = map[string]Account{"a": {ConfigDir: link}}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "default directory") {
		t.Errorf("symlink to ~/.claude: %v", err)
	}
	// a path that goes through the link to a not-yet-existing child
	c.Accounts = map[string]Account{"a": {ConfigDir: filepath.Join(link, "sub")}}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "inside Claude Code's default") {
		t.Errorf("child of a symlink to ~/.claude: %v", err)
	}
	// two names for one real directory
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Skip("symlinks unavailable: " + err.Error())
	}
	c.Accounts = map[string]Account{"a": {ConfigDir: real}, "b": {ConfigDir: alias}}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "same directory") {
		t.Errorf("alias of another account: %v", err)
	}
	c.Accounts = map[string]Account{"a": {ConfigDir: real}, "b": {ConfigDir: filepath.Join(alias, "child")}}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "nested") {
		t.Errorf("child through an alias: %v", err)
	}
}

func TestCanonicalPath(t *testing.T) {
	base := t.TempDir()
	want, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	if got := canonicalPath(filepath.Join(base, "a", "b", "..", "c")); got != filepath.Join(want, "a", "c") {
		t.Errorf("canonicalPath = %q, want %q", got, filepath.Join(want, "a", "c"))
	}
	root := filepath.VolumeName(base) + string(filepath.Separator)
	if got := canonicalPath(root); got == "" {
		t.Error("empty result for the root")
	}
}

func TestPathComparisonPerOS(t *testing.T) {
	base := t.TempDir()
	a := filepath.Join(base, "Claude-Work")
	b := filepath.Join(base, "claude-work")
	for goos, want := range map[string]bool{"darwin": true, "windows": true, "linux": false, "freebsd": false} {
		if got := samePathFor(goos, a, b); got != want {
			t.Errorf("samePathFor(%s) = %v, want %v", goos, got, want)
		}
		// filepath.Rel folds case on a Windows host whatever goos says, so the
		// case-sensitive expectation can only be shown elsewhere.
		if got := nestedFor(goos, a, filepath.Join(b, "x")); got != want && (want || runtime.GOOS != "windows") {
			t.Errorf("nestedFor(%s) = %v, want %v", goos, got, want)
		}
		if !samePathFor(goos, a, filepath.Join(a, "x", "..")) {
			t.Errorf("samePathFor(%s) must clean paths", goos)
		}
	}
	if nestedFor("linux", a, a) || nestedFor("linux", a, filepath.Join(base, "Claude-Work2")) {
		t.Error("equal and prefix-named paths are not nested")
	}
	if !nestedFor("linux", base, a) || !nestedFor("linux", a, base) {
		t.Error("nested must be symmetric")
	}
	if samePath(a, a) != true {
		t.Error("samePath on the running OS")
	}
}

func TestResolveAccountFlagOverridesEnvWithNote(t *testing.T) {
	cfg := Default()
	cfg.Accounts = map[string]Account{"work": {ConfigDir: "/w"}}
	env := envOf(map[string]string{"CLAUDE_CONFIG_DIR": "/e"})
	got, err := ResolveAccount("work", "", cfg, env)
	if err != nil {
		t.Fatal(err)
	}
	if !got.OverridesEnv || !got.SetEnv || got.FromEnv || got.Name != "work" {
		t.Errorf("choice = %+v", got)
	}
	if len(got.Notes) != 1 || !strings.Contains(got.Notes[0], "--account work") || !strings.Contains(got.Notes[0], "CLAUDE_CONFIG_DIR") {
		t.Errorf("notes = %v", got.Notes)
	}
	// without the environment variable there is nothing to say
	got, err = ResolveAccount("work", "", cfg, envOf(nil))
	if err != nil || got.OverridesEnv || len(got.Notes) != 0 {
		t.Errorf("no env: %+v %v", got, err)
	}
	// an implicit choice never overrides the environment
	got, err = ResolveAccount("", "work", cfg, env)
	if err != nil || !got.FromEnv || got.SetEnv || got.OverridesEnv || got.Name != "" {
		t.Errorf("implicit: %+v %v", got, err)
	}
}
