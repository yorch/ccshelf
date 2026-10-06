package trust

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ccshelf/ccshelf/internal/profile"
)

const sha1 = "1111111111111111111111111111111111111111"
const sha2 = "2222222222222222222222222222222222222222"

func TestPersonalIsAlwaysTrusted(t *testing.T) {
	root := t.TempDir()
	put(t, root, "profiles/base.toml", baseTOML)
	r := resolve(t, "base", profile.DirSource(profile.KindPersonal, filepath.Join(root, "profiles")))
	s := newStore(t)
	if v := s.Check(r); v.State != Trusted || len(v.Changes) != 0 {
		t.Fatalf("verdict = %+v", v)
	}
	if err := s.Require(r); err != nil {
		t.Fatal(err)
	}
	if err := s.Accept(r, ""); err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 0 {
		t.Error("personal profiles must not get entries")
	}
	if _, err := os.Stat(s.Path()); err == nil {
		t.Error("nothing should have been written")
	}
}

func TestNewThenAcceptThenTrusted(t *testing.T) {
	org := orgTree(t)
	src := gitLike(org, "v1", sha1)
	r := resolve(t, "dev", src)
	s := newStore(t)

	v := s.Check(r)
	if v.State != New || !v.Risky || v.Profile != "dev" {
		t.Fatalf("verdict = %+v", v)
	}
	err := s.Require(r)
	var nt *NeedsTrustError
	if !errors.As(err, &nt) || nt.ExitCode() != 4 || nt.Profile != "dev" || !IsNeedsTrust(err) {
		t.Fatalf("Require = %v", err)
	}
	if !strings.Contains(err.Error(), "ccshelf trust dev") {
		t.Errorf("message: %v", err)
	}
	if err := s.Accept(r, ""); err != nil {
		t.Fatal(err)
	}
	if v := s.Check(r); v.State != Trusted || v.Risky || len(v.Changes) != 0 {
		t.Fatalf("after accept: %+v", v)
	}
	if err := s.Require(r); err != nil {
		t.Fatal(err)
	}
	es := s.List()
	if len(es) != 1 || es[0].Source != "git:https://example.com/org/data.git" || es[0].Ref != "v1" || es[0].Commit != sha1 ||
		es[0].ClosureHash != r.Closure.Hash || es[0].AcceptedAt.IsZero() || len(es[0].Items) != len(r.Closure.Items) || es[0].ToolVersion == "" {
		t.Fatalf("entry = %+v", es)
	}

	// persists across Open, and the file is private
	s2, err := Open(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if v := s2.Check(r); v.State != Trusted {
		t.Fatalf("reopened: %+v", v)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(s.Path())
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("lockfile mode: %v %v", fi.Mode().Perm(), err)
		}
		di, _ := os.Stat(filepath.Dir(s.Path()))
		if di.Mode().Perm() != 0o700 {
			t.Errorf("dir mode: %v", di.Mode().Perm())
		}
	}
}

func TestAcceptExpectedHash(t *testing.T) {
	org := orgTree(t)
	r := resolve(t, "dev", gitLike(org, "v1", sha1))
	s := newStore(t)
	if err := s.Accept(r, strings.Repeat("0", 64)); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("err = %v", err)
	}
	if len(s.List()) != 0 {
		t.Fatal("a mismatched hash must not record anything")
	}
	if err := s.Accept(r, r.Closure.Hash); err != nil {
		t.Fatal(err)
	}
	if err := s.Accept(nil, ""); err == nil {
		t.Error("nil must fail")
	}
}

func TestInconsistentClosureIsNeverTrusted(t *testing.T) {
	org := orgTree(t)
	r := resolve(t, "dev", gitLike(org, "v1", sha1))
	s := newStore(t)
	if err := s.Accept(r, ""); err != nil {
		t.Fatal(err)
	}
	bad := *r
	bad.Closure.Items = append([]profile.ClosureItem(nil), r.Closure.Items...)
	bad.Closure.Items[0].Digest = strings.Repeat("a", 64)
	v := s.Check(&bad)
	if v.State == Trusted || v.Problem == "" {
		t.Fatalf("verdict = %+v", v)
	}
	var buf bytes.Buffer
	v.Describe(&buf)
	if !strings.Contains(buf.String(), "Problem:") {
		t.Errorf("describe: %s", buf.String())
	}
	if err := s.Accept(&bad, ""); !errors.Is(err, ErrInconsistentClosure) {
		t.Errorf("Accept = %v", err)
	}
}

func TestChangedAndRiskyDiff(t *testing.T) {
	org := orgTree(t)
	src := gitLike(org, "v1", sha1)
	s := newStore(t)
	if err := s.Accept(resolve(t, "dev", src), ""); err != nil {
		t.Fatal(err)
	}
	// edit registry, prompt, add a plugin, and tweak the base profile text
	put(t, org, "mcp/registry.toml", strings.Replace(regTOML, "pd-mcp", "pd-mcp-evil", 1))
	put(t, org, "prompts/dev.md", "Be careless.\n")
	put(t, org, "profiles/dev.toml", strings.Replace(devTOML, `["audit-kit@acme"]`, `["audit-kit@acme", "extra@acme"]`, 1))
	put(t, org, "profiles/base.toml", baseTOML+"# a comment\n")
	r := resolve(t, "dev", gitLike(org, "v1", sha1))
	v := s.Check(r)
	if v.State != Changed || !v.Risky {
		t.Fatalf("verdict = %+v", v)
	}
	kinds := map[string]ChangeKind{}
	for _, c := range v.Changes {
		kinds[c.Item.Kind+":"+c.Item.Name] = c.Kind
	}
	for k, want := range map[string]ChangeKind{
		"registry:pd": Altered, "prompt:prompts/dev.md": Altered, "plugin:extra@acme": Added, "profile:base": Altered, "profile:dev": Altered,
	} {
		if kinds[k] != want {
			t.Errorf("%s = %q, want %q (all: %v)", k, kinds[k], want, kinds)
		}
	}
	// risky changes sort before non-risky ones
	seenSafe := false
	for _, c := range v.Changes {
		if !c.Risky {
			seenSafe = true
		} else if seenSafe {
			t.Errorf("risky change after a safe one: %+v", v.Changes)
		}
	}
	var buf bytes.Buffer
	v.Describe(&buf)
	out := buf.String()
	for _, want := range []string{"changed since you last trusted", "Needs your review", "MCP server pd changed and now runs: npx -y pd-mcp-evil", "prompt text changed (prompts/dev.md)", "plugin extra@acme added", "Other changes:", "profile base changed"} {
		if !strings.Contains(out, want) {
			t.Errorf("describe lacks %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "Needs your review") > strings.Index(out, "Other changes:") {
		t.Errorf("risky must come first:\n%s", out)
	}
	for _, secret := range []string{"hunter2", "zzz"} {
		if strings.Contains(out, secret) {
			t.Errorf("describe leaked %q:\n%s", secret, out)
		}
	}
	if !strings.Contains(out, "PD_TOKEN_REF") || !strings.Contains(out, "<redacted>") {
		t.Errorf("describe should list env names and mask secrets:\n%s", out)
	}
	err := s.Require(r)
	if !IsNeedsTrust(err) || !strings.Contains(err.Error(), "changed since you accepted") {
		t.Errorf("Require = %v", err)
	}
	// accepting the new closure makes it trusted again
	if err := s.Accept(r, r.Closure.Hash); err != nil {
		t.Fatal(err)
	}
	if v := s.Check(r); v.State != Trusted {
		t.Errorf("after re-accept: %+v", v)
	}
	if len(s.List()) != 1 {
		t.Errorf("accepting again must replace the entry: %+v", s.List())
	}
}

func TestRemovedItemsAreReported(t *testing.T) {
	org := orgTree(t)
	s := newStore(t)
	if err := s.Accept(resolve(t, "dev", gitLike(org, "v1", sha1)), ""); err != nil {
		t.Fatal(err)
	}
	put(t, org, "profiles/dev.toml", "name = \"dev\"\ndescription = \"d\"\nextends = [\"base\"]\n")
	v := s.Check(resolve(t, "dev", gitLike(org, "v1", sha1)))
	if v.State != Changed {
		t.Fatalf("verdict = %+v", v)
	}
	var buf bytes.Buffer
	v.Describe(&buf)
	for _, want := range []string{"MCP server pd removed", "plugin audit-kit@acme removed", "system prompt file prompts/dev.md removed"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("lacks %q:\n%s", want, buf.String())
		}
	}
}

func TestTagMoved(t *testing.T) {
	org := orgTree(t)
	s := newStore(t)
	if err := s.Accept(resolve(t, "dev", gitLike(org, "v1", sha1)), ""); err != nil {
		t.Fatal(err)
	}
	// same ref, same content, other commit: still an untrusted update
	r := resolve(t, "dev", gitLike(org, "v1", sha2))
	v := s.Check(r)
	if v.State != TagMoved || v.Ref != "v1" || v.OldCommit != sha1 || v.NewCommit != sha2 || !v.Risky {
		t.Fatalf("verdict = %+v", v)
	}
	var buf bytes.Buffer
	v.Describe(&buf)
	if !strings.Contains(buf.String(), "untrusted update") || !strings.Contains(buf.String(), "111111111111") {
		t.Errorf("describe: %s", buf.String())
	}
	err := s.Require(r)
	if !IsNeedsTrust(err) || !strings.Contains(err.Error(), "different commit") {
		t.Errorf("Require = %v", err)
	}
	// a different ref (a new tag) with another commit is Changed, not TagMoved
	if v := s.Check(resolve(t, "dev", gitLike(org, "v2", sha2))); v.State != Changed {
		t.Errorf("new ref: %+v", v)
	}
	// a different source is New
	other := gitLike(org, "v1", sha1)
	other.locator = "git:https://example.com/other.git"
	if v := s.Check(resolve(t, "dev", other)); v.State != New {
		t.Errorf("other source: %+v", v)
	}
}

func TestPersonalExtendingSharedNeedsTrust(t *testing.T) {
	org := orgTree(t)
	home := t.TempDir()
	put(t, home, "profiles/mine.toml", "name = \"mine\"\ndescription = \"d\"\nextends = [\"dev\"]\n")
	personal := profile.DirSource(profile.KindPersonal, filepath.Join(home, "profiles"))
	r := resolve(t, "mine", personal, gitLike(org, "v1", sha1))
	s := newStore(t)
	if v := s.Check(r); v.State != New {
		t.Fatalf("verdict = %+v", v)
	}
	if err := s.Accept(r, ""); err != nil {
		t.Fatal(err)
	}
	if v := s.Check(r); v.State != Trusted {
		t.Fatalf("verdict = %+v", v)
	}
	if got := s.List(); len(got) != 1 || got[0].Profile != "mine" || got[0].Ref != "v1" {
		t.Errorf("entries = %+v", got)
	}
	r2 := resolve(t, "mine", personal, gitLike(org, "v1", sha2))
	if v := s.Check(r2); v.State != TagMoved {
		t.Errorf("a moved tag under a personal profile: %+v", v)
	}
}

func TestOrgDirSourceKey(t *testing.T) {
	org := orgTree(t)
	src := profile.DirSource(profile.KindOrg, filepath.Join(org, "profiles"))
	r := resolve(t, "base", src)
	s := newStore(t)
	if err := s.Accept(r, ""); err != nil {
		t.Fatal(err)
	}
	if got := s.List(); len(got) != 1 || got[0].Source != "dir:org" || got[0].Ref != "" {
		t.Fatalf("entries = %+v", got)
	}
	if v := s.Check(r); v.State != Trusted {
		t.Errorf("%+v", v)
	}
}

func TestProjectUntrustedUntilStated(t *testing.T) {
	proj := t.TempDir()
	put(t, proj, ".ccshelf/profiles/p.toml", "name = \"p\"\ndescription = \"d\"\n")
	src := profile.DirSource(profile.KindProject, filepath.Join(proj, ".ccshelf", "profiles"))
	r := resolve(t, "p", src)
	s := newStore(t)
	v := s.Check(r)
	if v.State != ProjectUntrusted {
		t.Fatalf("verdict = %+v", v)
	}
	var buf bytes.Buffer
	v.Describe(&buf)
	if !strings.Contains(buf.String(), "project folder that has not been trusted") {
		t.Errorf("describe: %s", buf.String())
	}
	err := s.Require(r)
	if !IsNeedsTrust(err) || !strings.Contains(err.Error(), "untrusted project") {
		t.Errorf("Require = %v", err)
	}
	// project folder trust alone is not enough: the closure needs an entry too
	if v := s.CheckWithProject(r, true); v.State != New {
		t.Errorf("trusted folder, no entry: %+v", v)
	}
	if err := s.Accept(r, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.RequireWithProject(r, true); err != nil {
		t.Errorf("RequireWithProject = %v", err)
	}
	if err := s.RequireWithProject(r, false); err == nil {
		t.Error("an untrusted project folder must fail even with an entry")
	}
}

func TestNilResolved(t *testing.T) {
	s := newStore(t)
	if v := s.Check(nil); v.State != New {
		t.Errorf("%+v", v)
	}
	if err := s.Require(nil); !IsNeedsTrust(err) {
		t.Errorf("Require(nil) = %v", err)
	}
}

func TestRevoke(t *testing.T) {
	org := orgTree(t)
	s := newStore(t)
	a := resolve(t, "dev", gitLike(org, "v1", sha1))
	other := gitLike(org, "v1", sha1)
	other.locator = "git:https://example.com/two.git"
	b := resolve(t, "dev", other)
	c := resolve(t, "base", gitLike(org, "v1", sha1))
	for _, r := range []*profile.Resolved{a, b, c} {
		if err := s.Accept(r, ""); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.List()) != 3 {
		t.Fatalf("entries: %+v", s.List())
	}
	if err := s.Revoke("dev", "git:https://example.com/two.git"); err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 2 {
		t.Fatal("source filter")
	}
	if err := s.Revoke("dev", ""); err != nil {
		t.Fatal(err)
	}
	if v := s.Check(a); v.State != New {
		t.Errorf("after revoke: %+v", v)
	}
	if err := s.Revoke("dev", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("second revoke = %v", err)
	}
	if got := s.List(); len(got) != 1 || got[0].Profile != "base" {
		t.Errorf("entries = %+v", got)
	}
	s2, err := Open(s.Path())
	if err != nil || len(s2.List()) != 1 {
		t.Errorf("persisted revoke: %v %v", s2, err)
	}
}

func TestAcceptNowAndSortedFile(t *testing.T) {
	org := orgTree(t)
	s := newStore(t)
	fixed := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return fixed }
	for _, name := range []string{"dev", "base"} {
		if err := s.Accept(resolve(t, name, gitLike(org, "v1", sha1)), ""); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Version int `json:"version"`
		Entries []struct {
			Profile     string `json:"profile"`
			AcceptedAt  string `json:"acceptedAt"`
			ClosureHash string `json:"closureHash"`
			Items       []struct {
				Kind string `json:"kind"`
			} `json:"items"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if raw.Version != 1 || len(raw.Entries) != 2 || raw.Entries[0].Profile != "base" || raw.Entries[1].Profile != "dev" ||
		raw.Entries[0].AcceptedAt != "2026-10-06T12:00:00Z" || len(raw.Entries[1].Items) == 0 || raw.Entries[1].Items[0].Kind == "" {
		t.Errorf("lockfile = %s", b)
	}
}

func TestOpenRejectsBadFiles(t *testing.T) {
	org := orgTree(t)
	good := newStore(t)
	if err := good.Accept(resolve(t, "dev", gitLike(org, "v1", sha1)), ""); err != nil {
		t.Fatal(err)
	}
	goodBytes, _ := os.ReadFile(good.Path())

	tests := []struct {
		name string
		body string
		want string
	}{
		{"not json", "nope", "parsing"},
		{"unknown key", `{"version":1,"entries":[],"extra":1}`, "unknown field"},
		{"wrong version", `{"version":2,"entries":[]}`, "version 2"},
		{"unknown entry key", `{"version":1,"entries":[{"profile":"a","source":"b","closureHash":"x","items":[],"bogus":1,"acceptedAt":"2026-01-01T00:00:00Z"}]}`, "unknown field"},
		{"bad hash", `{"version":1,"entries":[{"profile":"a","source":"b","closureHash":"xyz","items":[],"acceptedAt":"2026-01-01T00:00:00Z"}]}`, "invalid closure hash"},
		{"no profile", `{"version":1,"entries":[{"profile":"","source":"b","closureHash":"` + strings.Repeat("a", 64) + `","items":[],"acceptedAt":"2026-01-01T00:00:00Z"}]}`, "without a profile"},
		{"hash does not match items", `{"version":1,"entries":[{"profile":"a","source":"b","closureHash":"` + strings.Repeat("a", 64) + `","items":[],"acceptedAt":"2026-01-01T00:00:00Z"}]}`, "edited or is corrupt"},
		{"edited item", strings.Replace(string(goodBytes), `"risky": true`, `"risky": false`, 1), "edited or is corrupt"},
		{"duplicate", dupEntries(t, goodBytes), "duplicate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "lock.json")
			if err := os.WriteFile(p, []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Open(p)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
	// too large
	p := filepath.Join(t.TempDir(), "lock.json")
	if err := os.WriteFile(p, bytes.Repeat([]byte(" "), maxStateFile+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(p); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Errorf("huge file: %v", err)
	}
	// a directory is not a lockfile
	if _, err := Open(t.TempDir()); err == nil {
		t.Error("directory must fail")
	}
}

func dupEntries(t *testing.T, good []byte) string {
	t.Helper()
	var lf struct {
		Version int               `json:"version"`
		Entries []json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(good, &lf); err != nil {
		t.Fatal(err)
	}
	lf.Entries = append(lf.Entries, lf.Entries[0])
	b, _ := json.Marshal(lf)
	return string(b)
}

func TestSymlinkedLockfileRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs privileges on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "elsewhere.json")
	if err := os.WriteFile(target, []byte(`{"version":1,"entries":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "lock.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(link); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("read through symlink: %v", err)
	}
	// writing: start from a missing file, then plant the link before Accept
	s := &Store{path: link, now: time.Now}
	org := orgTree(t)
	err := s.Accept(resolve(t, "dev", gitLike(org, "v1", sha1)), "")
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("write through symlink: %v", err)
	}
	b, _ := os.ReadFile(target)
	if string(b) != `{"version":1,"entries":[]}` {
		t.Errorf("target was modified: %s", b)
	}
}

func TestInsecureDirRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory modes are not checked on Windows")
	}
	dir := filepath.Join(t.TempDir(), "cfg")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(dir, "lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	org := orgTree(t)
	err = s.Accept(resolve(t, "dev", gitLike(org, "v1", sha1)), "")
	if err == nil || !strings.Contains(err.Error(), "writable by group or others") {
		t.Fatalf("err = %v", err)
	}
}

func TestWriteFailures(t *testing.T) {
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// parent is a file
	s := &Store{path: filepath.Join(file, "lock.json"), now: time.Now}
	org := orgTree(t)
	if err := s.Accept(resolve(t, "dev", gitLike(org, "v1", sha1)), ""); err == nil {
		t.Error("parent that is a file must fail")
	}
	// target is a directory
	d := t.TempDir()
	if err := os.Mkdir(filepath.Join(d, "lock.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	s = &Store{path: filepath.Join(d, "lock.json"), now: time.Now}
	if err := s.Accept(resolve(t, "dev", gitLike(org, "v1", sha1)), ""); err == nil {
		t.Error("directory target must fail")
	}
	if err := writeStateFile(filepath.Join(t.TempDir(), "x.json"), make([]byte, maxStateFile+1)); err == nil {
		t.Error("oversized write must fail")
	}
}

func TestConcurrentAcceptCheck(t *testing.T) {
	org := orgTree(t)
	s := newStore(t)
	r := resolve(t, "dev", gitLike(org, "v1", sha1))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Accept(r, ""); err != nil {
				t.Error(err)
			}
			_ = s.Check(r)
			_ = s.List()
		}()
	}
	wg.Wait()
	if v := s.Check(r); v.State != Trusted {
		t.Errorf("%+v", v)
	}
}

func TestDescribeSanitizesAndTruncates(t *testing.T) {
	c := Change{Kind: Added, Item: profile.ClosureItem{Kind: profile.ItemPlugin, Name: "evil\x1b[31m@acme"}, Risky: true}
	v := Verdict{State: New, Profile: "p\x1b[2J", Changes: []Change{c}, Risky: true}
	var buf bytes.Buffer
	v.Describe(&buf)
	if strings.ContainsRune(buf.String(), 0x1b) {
		t.Errorf("escape sequence leaked: %q", buf.String())
	}
	long := clean(strings.Repeat("x", 1000))
	if len(long) > 250 || !strings.HasSuffix(long, "...") {
		t.Errorf("clean did not truncate: %d", len(long))
	}
}

func TestDescribeHTTPServerAndOverrides(t *testing.T) {
	m := profile.MCPServer{Name: "web", Type: profile.MCPHTTP, URL: "https://user:pw@mcp.example.com/x?token=abc#frag"}
	d := describeServer(m)
	if strings.Contains(d, "pw") || strings.Contains(d, "abc") || !strings.Contains(d, "connects to: https://<redacted>@mcp.example.com/x?<redacted>") {
		t.Errorf("describeServer = %q", d)
	}
	m = profile.MCPServer{Type: profile.MCPStdio, Command: "npx", Args: []string{"-y", "x", "--api-key", "SECRET", "--password=hunter2", "TOKEN=abc"},
		Windows: &profile.MCPOverride{Command: "cmd", Args: []string{"/c", "npx"}}, MacOS: &profile.MCPOverride{Command: "m"}, Linux: &profile.MCPOverride{Command: "l"}}
	d = describeServer(m)
	for _, bad := range []string{"SECRET", "hunter2", "abc"} {
		if strings.Contains(d, bad) {
			t.Errorf("leaked %q in %q", bad, d)
		}
	}
	for _, want := range []string{"on windows runs: cmd /c npx", "on macos runs: m", "on linux runs: l"} {
		if !strings.Contains(d, want) {
			t.Errorf("lacks %q in %q", want, d)
		}
	}
}

func TestStateStrings(t *testing.T) {
	for st, want := range map[State]string{Trusted: "trusted", New: "new", Changed: "changed", TagMoved: "tag-moved", ProjectUntrusted: "project-untrusted", State(9): "state(9)"} {
		if st.String() != want {
			t.Errorf("%d = %q", st, st.String())
		}
	}
	var buf bytes.Buffer
	Verdict{State: Trusted, Profile: "x"}.Describe(&buf)
	if !strings.Contains(buf.String(), "is trusted") {
		t.Error(buf.String())
	}
}

func TestDescribeOtherItemKinds(t *testing.T) {
	for _, c := range []Change{
		{Kind: Added, Item: profile.ClosureItem{Kind: profile.ItemProfile, Name: "p"}, Risky: true},
		{Kind: Added, Item: profile.ClosureItem{Kind: profile.ItemPrompt, Name: "prompts/x.md"}, Risky: true},
		{Kind: Altered, Item: profile.ClosureItem{Kind: profile.ItemSource, Name: "git:x@y"}},
		{Kind: Added, Item: profile.ClosureItem{Kind: "mystery", Name: "m"}},
		{Kind: Added, Item: profile.ClosureItem{Kind: profile.ItemRegistry, Name: "r"}, Risky: true},
	} {
		if c.describe() == "" {
			t.Errorf("empty description for %+v", c)
		}
	}
}
