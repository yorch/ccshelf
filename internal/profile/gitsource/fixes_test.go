package gitsource

import (
	"bytes"
	"compress/zlib"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[strings.ToUpper(k)] = v
	}
	return m
}

func TestGitEnvIsAnAllowlist(t *testing.T) {
	stripped := []string{
		"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
		"GIT_NAMESPACE", "GIT_COMMON_DIR", "GIT_PREFIX", "GIT_CEILING_DIRECTORIES", "GIT_ASKPASS", "SSH_ASKPASS",
		"SSH_ASKPASS_REQUIRE", "GIT_EXTERNAL_DIFF", "GIT_PAGER", "GIT_EDITOR", "GIT_SEQUENCE_EDITOR",
		"GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0", "GIT_CONFIG_KEY_7",
		"GIT_EXEC_PATH", "GIT_SSL_NO_VERIFY", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "GIT_CONFIG_NOSYSTEM",
		"GIT_ATTR_SOURCE", "GIT_REPLACE_REF_BASE", "GIT_SHALLOW_FILE", "GIT_TRACE", "GIT_TRACE_PACKET", "GIT_TRACE2",
		"GIT_TRACE2_EVENT", "GIT_TRACE_CURL", "GIT_PROXY_COMMAND", "GIT_CURL_VERBOSE", "GIT_SSL_CAINFO",
		"GIT_LFS_SKIP_SMUDGE", "GIT_TEMPLATE_DIR", "GIT_GLOB_PATHSPECS", "GIT_NO_REPLACE_OBJECTS", "GIT_PROTOCOL",
		"GIT_ALLOW_PROTOCOL", "GIT_TERMINAL_PROMPT", "GIT_NO_LAZY_FETCH", "GIT_SSH_VARIANTX", "GIT_FUTURE_THING",
	}
	for _, k := range stripped {
		t.Setenv(k, "evil")
	}
	t.Setenv("Git_Dir", "mixed-case") // names compare case-insensitively
	kept := map[string]string{
		"GIT_SSH": "/usr/bin/ssh", "GIT_SSH_COMMAND": "ssh -i /k", "GIT_SSH_VARIANT": "ssh",
		"GIT_HTTP_PROXY_AUTHMETHOD": "basic", "HTTPS_PROXY": "http://proxy:3128", "SSH_AUTH_SOCK": "/s", "PATH": os.Getenv("PATH"),
	}
	for k, v := range kept {
		t.Setenv(k, v)
	}
	s, _ := New(Options{URL: "https://h/r", Ref: "v1"})
	env := envMap(s.gitEnv())
	owned := map[string]string{"GIT_TERMINAL_PROMPT": "0", "GIT_ALLOW_PROTOCOL": "https:ssh", "GIT_LFS_SKIP_SMUDGE": "1"}
	for _, k := range stripped {
		if v, ok := env[k]; ok && owned[k] != v {
			t.Errorf("%s=%q reached git", k, v)
		}
	}
	if _, ok := env["GIT_DIR"]; ok {
		t.Error("GIT_DIR (any case) reached git")
	}
	for k, v := range owned {
		if env[k] != v {
			t.Errorf("%s = %q, want %q", k, env[k], v)
		}
	}
	for k, v := range kept {
		if env[strings.ToUpper(k)] != v {
			t.Errorf("%s was dropped", k)
		}
	}
	for k := range env {
		if strings.HasPrefix(k, "GIT_") && !(owned[k] != "" || keptGitEnv[k]) {
			t.Errorf("unexpected GIT variable %s", k)
		}
	}
	s.opts.AllowLocal = true
	if envMap(s.gitEnv())["GIT_ALLOW_PROTOCOL"] != "https:ssh:file" {
		t.Error("AllowLocal must allow the file protocol")
	}
}

// wrapper writes a git wrapper script that logs argv and the GIT_ environment
// of every call, then runs the real git.
func wrapper(t *testing.T) (path, argLog, envLog string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell wrapper scripts are not portable to Windows")
	}
	d := t.TempDir()
	argLog, envLog = filepath.Join(d, "args.log"), filepath.Join(d, "env.log")
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + argLog + "\nenv | grep -i '^GIT_' | sort | tr '\\n' ' ' >> " + envLog + "\necho >> " + envLog + "\nexec " + real + " \"$@\"\n"
	path = filepath.Join(d, "git-wrapper")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path, argLog, envLog
}

// subcommand returns the git subcommand of a logged command line.
func subcommand(line string) string {
	f := strings.Fields(line)
	for i := 0; i < len(f); i++ {
		switch {
		case f[i] == "-c":
			i++
		case strings.HasPrefix(f[i], "--git-dir="):
		default:
			return f[i]
		}
	}
	return ""
}

func TestEveryGitCallIsHardenedAndNeverTouchesAWorkTree(t *testing.T) {
	f := newFixture(t)
	f.seed()
	f.git("tag", "v1")
	for _, k := range []string{"GIT_SSL_NO_VERIFY", "GIT_TRACE", "GIT_CONFIG_COUNT", "GIT_REPLACE_REF_BASE", "GIT_EXEC_PATH"} {
		t.Setenv(k, "1")
	}
	gp, argLog, envLog := wrapper(t)
	s, err := New(Options{URL: f.url(), Ref: "v1", CacheDir: f.cache, RequirePin: true, AllowLocal: true, GitPath: gp})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Prepare(context.Background()); err != nil { // the verification path
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(argLog)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	seen := map[string]bool{}
	allowed := map[string]bool{"init": true, "remote": true, "fetch": true, "update-ref": true, "rev-parse": true, "cat-file": true, "ls-remote": true, "ls-tree": true}
	for _, l := range lines {
		sub := subcommand(l)
		seen[sub] = true
		if !allowed[sub] {
			t.Errorf("git %s must never run: %s", sub, l)
		}
		for _, opt := range []string{
			"-c core.symlinks=false", "-c core.fsmonitor=false", "-c protocol.ext.allow=never",
			"-c protocol.file.allow=user", "-c core.hooksPath=", "-c submodule.recurse=false", "-c gc.auto=0",
		} {
			if !strings.Contains(l, opt) {
				t.Errorf("%q lacks %q", l, opt)
			}
		}
		if sub == "fetch" {
			for _, opt := range []string{"--no-recurse-submodules", "--depth 1", "--no-tags", "--filter=blob:limit="} {
				if !strings.Contains(l, opt) {
					t.Errorf("fetch lacks %q: %s", opt, l)
				}
			}
		}
	}
	for _, want := range []string{"init", "fetch", "ls-tree", "cat-file", "rev-parse", "update-ref", "ls-remote"} {
		if !seen[want] {
			t.Errorf("git %s was never run (seen %v)", want, seen)
		}
	}
	envRaw, _ := os.ReadFile(envLog)
	for _, l := range strings.Split(strings.TrimSpace(string(envRaw)), "\n") {
		for _, bad := range []string{"GIT_SSL_NO_VERIFY", "GIT_TRACE", "GIT_CONFIG_COUNT", "GIT_REPLACE_REF_BASE", "GIT_EXEC_PATH", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM"} {
			if strings.Contains(l, bad+"=") {
				t.Errorf("%s reached git: %s", bad, l)
			}
		}
		if !strings.Contains(l, "GIT_LFS_SKIP_SMUDGE=1") || !strings.Contains(l, "GIT_TERMINAL_PROMPT=0") {
			t.Errorf("missing owned settings: %s", l)
		}
	}
}

func TestFileAtTheSizeLimitIsExtracted(t *testing.T) {
	f := newFixture(t)
	f.seed()
	f.write("prompts/limit.md", strings.Repeat("a", MaxFileSize))
	f.commit("limit")
	f.git("tag", "v1")
	s := f.source("v1", "")
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(s.Root(), "prompts", "limit.md"))
	if err != nil || fi.Size() != MaxFileSize {
		t.Fatalf("limit file: %v %v", fi, err)
	}
}

func TestExtractionIsExactlyTheWatchedFolders(t *testing.T) {
	f := newFixture(t)
	f.seed()
	f.write("docs/big.txt", strings.Repeat("z", 3*MaxFileSize))
	f.write("src/other.go", "package x\n")
	f.commit("more")
	f.git("tag", "v1")
	s := f.source("v1", "")
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"docs", "src", "README.md"} {
		if _, err := os.Lstat(filepath.Join(s.Root(), rel)); err == nil {
			t.Errorf("%s must not be written", rel)
		}
	}
}

func rewriteLooseObject(t *testing.T, gitDir, oid string, raw []byte) {
	t.Helper()
	p := filepath.Join(gitDir, "objects", oid[:2], oid[2:])
	if _, err := os.Stat(p); err != nil {
		t.Skipf("the commit is not a loose object here: %v", err)
	}
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	_, _ = zw.Write(raw)
	_ = zw.Close()
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestForgedCommitObjectIsRejected(t *testing.T) {
	f := newFixture(t)
	sha := f.seed()
	s := f.source(sha, "")
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	forged := "tree " + f.git("rev-parse", "HEAD^{tree}") + "\nauthor x <x@example.com> 1 +0000\ncommitter x <x@example.com> 1 +0000\n\nforged\n"
	rewriteLooseObject(t, filepath.Join(s.Root(), ".git"), sha, []byte("commit "+itoa(len(forged))+"\x00"+forged))
	err := f.source(sha, "").Prepare(context.Background())
	if !errors.Is(err, ErrTampered) || !strings.Contains(err.Error(), "does not hash") {
		t.Fatalf("err = %v", err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func TestHEADPointingAtAnotherRealCommitIsRejected(t *testing.T) {
	f := newFixture(t)
	first := f.seed()
	f.write("profiles/more.toml", "name = \"more\"\ndescription = \"d\"\n")
	second := f.commit("more")
	s := f.source(first, "")
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	// make the second commit exist in the cached object store, then move HEAD to it
	gitDir := filepath.Join(s.Root(), ".git")
	if out, err := exec.Command("git", "--git-dir="+gitDir, "fetch", "--quiet", "--no-tags", "origin", second).CombinedOutput(); err != nil {
		t.Fatalf("fetch: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte(second+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := f.source(first, "").Prepare(context.Background())
	if !errors.Is(err, ErrTampered) || !strings.Contains(err.Error(), "expected "+first) {
		t.Fatalf("err = %v", err)
	}
}

func TestGitErrorTextIsSanitized(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts are not portable to Windows")
	}
	script := filepath.Join(t.TempDir(), "evilgit")
	body := "#!/bin/sh\nprintf 'remote: \\033[31mevil\\342\\200\\256txt\\r\\nremote: \\033[2Jline2\\n' >&2\nexit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	s, _ := New(Options{URL: "https://h/r", Ref: strings.Repeat("a", 40), CacheDir: t.TempDir(), GitPath: script})
	err := s.Prepare(context.Background())
	if err == nil {
		t.Fatal("expected a failure")
	}
	for _, bad := range []string{"\x1b", "\r", "\u202e", "\n"} {
		if strings.Contains(err.Error(), bad) {
			t.Errorf("error text contains %q: %q", bad, err.Error())
		}
	}
	if !strings.Contains(err.Error(), "remote:") || !strings.Contains(err.Error(), "line2") {
		t.Errorf("the message should still be informative: %v", err)
	}
}

func TestGitMessage(t *testing.T) {
	if got := gitMessage("a\n\n b \x1b[0m\n"); got != "a | b �[0m" {
		t.Errorf("got %q", got)
	}
	long := gitMessage(strings.Repeat("x", 5000))
	if len(long) > maxGitMessage+3 || !strings.HasSuffix(long, "...") {
		t.Errorf("len %d", len(long))
	}
	if gitMessage("  \n ") != "" {
		t.Error("blank stderr must give an empty message")
	}
}

func TestURLErrorsNeverEchoCredentials(t *testing.T) {
	for _, raw := range []string{"https://ghp_secret@/x", "https://ghp_secret@host/x", "https://u:ghp_secret@host/x", "ssh://u:ghp_secret@host/x"} {
		_, err := New(Options{URL: raw, Ref: "v1"})
		if err == nil || strings.Contains(err.Error(), "ghp_secret") {
			t.Errorf("%s: %v", raw, err)
		}
	}
}

func TestCheckTreeSizesAndModes(t *testing.T) {
	e := func(mode string, size int64, unknown bool) []treeEntry {
		return []treeEntry{{mode: mode, size: size, sizeUnknown: unknown, path: "prompts/x.md"}}
	}
	for name, tt := range map[string]struct {
		entries []treeEntry
		want    string
	}{
		"at the limit":       {e("100644", MaxFileSize, false), ""},
		"over the limit":     {e("100644", MaxFileSize+1, false), "larger than"},
		"unknown size":       {e("100644", 0, true), "missing or larger"},
		"odd mode":           {e("100664", 1, false), "unsupported mode"},
		"executable":         {e("100755", 1, false), ""},
		"outside is ignored": {[]treeEntry{{mode: "100644", size: MaxFileSize * 9, path: "docs/big"}}, ""},
	} {
		err := checkTree(tt.entries, "", newWatch(nil))
		switch {
		case tt.want == "" && err != nil:
			t.Errorf("%s: %v", name, err)
		case tt.want != "" && (err == nil || !errors.Is(err, ErrHygiene) || !strings.Contains(err.Error(), tt.want)):
			t.Errorf("%s: %v", name, err)
		}
	}
	many := make([]treeEntry, MaxWatchedFiles+1)
	for i := range many {
		many[i] = treeEntry{mode: "100644", size: 1, path: "prompts/" + itoa(i)}
	}
	if err := checkTree(many, "", newWatch(nil)); !errors.Is(err, ErrHygiene) {
		t.Errorf("file count: %v", err)
	}
	big := make([]treeEntry, 40)
	for i := range big {
		big[i] = treeEntry{mode: "100644", size: MaxFileSize, path: "prompts/" + itoa(i)}
	}
	if err := checkTree(big, "", newWatch(nil)); !errors.Is(err, ErrHygiene) {
		t.Errorf("total bytes: %v", err)
	}
	for _, p := range []string{"profiles/a:b", "profiles/a\u202eb", "profiles/a\u200bb"} {
		if err := checkTree([]treeEntry{{mode: "100644", path: p}}, "", newWatch(nil)); !errors.Is(err, ErrHygiene) {
			t.Errorf("%q: %v", p, err)
		}
	}
}

func TestParseLsTreeFields(t *testing.T) {
	oid := strings.Repeat("a", 40)
	es, err := parseLsTree("100644 blob " + oid + "      12\tprofiles/a b.toml\x00100644 blob " + oid + "     BAD\tprofiles/big\x00")
	if err != nil || len(es) != 2 {
		t.Fatalf("%v %v", es, err)
	}
	if es[0].oid != oid || es[0].typ != "blob" || es[0].size != 12 || es[0].path != "profiles/a b.toml" || !es[1].sizeUnknown {
		t.Errorf("%+v", es)
	}
	if _, err := parseLsTree("100644 blob " + oid + "     -5\tx\x00"); err == nil {
		t.Error("a negative size must fail")
	}
}

// diskFixture builds a checkout directory with the given files and the
// matching tree entries.
func diskFixture(t *testing.T, files map[string]string) (string, []treeEntry) {
	t.Helper()
	root := t.TempDir()
	var entries []treeEntry
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		id, err := gitObjectID(strings.Repeat("0", 40), "blob", []byte(body))
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, treeEntry{mode: "100644", typ: "blob", oid: id, size: int64(len(body)), path: rel})
	}
	return root, entries
}

func TestVerifyDisk(t *testing.T) {
	files := map[string]string{"profiles/a.toml": "name = \"a\"\n", "prompts/p.md": "hi\n", "mcp/registry.toml": "x"}
	type tc struct {
		name   string
		mutate func(t *testing.T, root string)
		want   error
		text   string
	}
	big := strings.Repeat("b", MaxFileSize+1)
	cases := []tc{
		{"intact", func(*testing.T, string) {}, nil, ""},
		{"same size, other bytes", func(t *testing.T, r string) { mustWrite(t, filepath.Join(r, "prompts", "p.md"), "ho\n") }, ErrTampered, "differs from the commit"},
		{"other size", func(t *testing.T, r string) { mustWrite(t, filepath.Join(r, "prompts", "p.md"), "hello\n") }, ErrTampered, "bytes on disk"},
		{"extra file", func(t *testing.T, r string) { mustWrite(t, filepath.Join(r, "profiles", "x.toml"), "y") }, ErrTampered, "not in the commit"},
		{"extra directory", func(t *testing.T, r string) { _ = os.MkdirAll(filepath.Join(r, "profiles", "sub"), 0o700) }, ErrTampered, "not in commit"},
		{"missing file", func(t *testing.T, r string) { _ = os.Remove(filepath.Join(r, "mcp", "registry.toml")) }, ErrTampered, "missing"},
		{"watched folder is a file", func(t *testing.T, r string) {
			_ = os.RemoveAll(filepath.Join(r, "mcp"))
			mustWrite(t, filepath.Join(r, "mcp"), "x")
		}, ErrHygiene, "not a plain directory"},
		{"non-regular file", func(t *testing.T, r string) {
			if runtime.GOOS == "windows" {
				t.Skip("fifo")
			}
			_ = os.Remove(filepath.Join(r, "prompts", "p.md"))
			if err := mkfifo(filepath.Join(r, "prompts", "p.md")); err != nil {
				t.Skip("no fifo here")
			}
		}, ErrHygiene, "not a regular file"},
		{"oversized on disk", func(t *testing.T, r string) { mustWrite(t, filepath.Join(r, "prompts", "p.md"), big) }, ErrHygiene, "larger than"},
	}
	if runtime.GOOS != "windows" {
		cases = append(cases,
			tc{"symlink file", func(t *testing.T, r string) {
				_ = os.Remove(filepath.Join(r, "profiles", "a.toml"))
				if err := os.Symlink("/etc/passwd", filepath.Join(r, "profiles", "a.toml")); err != nil {
					t.Fatal(err)
				}
			}, ErrHygiene, "is a symlink"},
			tc{"symlinked watched dir", func(t *testing.T, r string) {
				_ = os.RemoveAll(filepath.Join(r, "prompts"))
				if err := os.Symlink(t.TempDir(), filepath.Join(r, "prompts")); err != nil {
					t.Fatal(err)
				}
			}, ErrHygiene, "not a plain directory"},
		)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, entries := diskFixture(t, files)
			c.mutate(t, root)
			if c.name == "oversized on disk" {
				id, _ := gitObjectID(strings.Repeat("0", 40), "blob", []byte(big))
				for i := range entries {
					if entries[i].path == "prompts/p.md" {
						entries[i].oid, entries[i].size = id, int64(len(big))
					}
				}
			}
			err := verifyDisk(root, "", entries, newWatch(nil))
			if c.want == nil {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, c.want) || !strings.Contains(err.Error(), c.text) {
				t.Fatalf("err = %v, want %v containing %q", err, c.want, c.text)
			}
		})
	}
}

func TestVerifyDiskBaseMustBePlain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	root := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "org")); err != nil {
		t.Fatal(err)
	}
	if err := verifyDisk(root, "org", nil, newWatch(nil)); !errors.Is(err, ErrHygiene) {
		t.Errorf("symlinked base: %v", err)
	}
}

func mustWrite(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
