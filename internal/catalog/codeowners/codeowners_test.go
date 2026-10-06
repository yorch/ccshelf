package codeowners

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The example file mirrors GitHub's documentation for CODEOWNERS.
const official = `# This is a comment.
# Each line is a file pattern followed by one or more owners.

# These owners will be the default owners for everything in
# the repo. Unless a later match takes precedence,
# @global-owner1 and @global-owner2 will be requested for
# review when someone opens a pull request.
*       @global-owner1 @global-owner2

# Order is important; the last matching pattern takes the most
# precedence. When someone opens a pull request that only
# modifies JS files, only @js-owner and not the global
# owner(s) will be requested for a review.
*.js    @js-owner #This is an inline comment.

# You can also use email addresses if you prefer. They'll be
# used to look up users just like we do for commit author
# emails.
*.go docs@example.com

# Teams can be specified as code owners as well. Teams should
# be identified in the format @org/team-name. Teams must have
# explicit write access to the repository. In this example,
# the octocats team in the octo-org organization owns all .txt files.
*.txt @octo-org/octocats

# In this example, @doctocat owns any files in the build/logs
# directory at the root of the repository and any of its
# subdirectories.
/build/logs/ @doctocat

# The 'docs/*' pattern will match files like
# 'docs/getting-started.md' but not further nested files like
# 'docs/build-app/troubleshooting.md'.
docs/*  docs@example.com

# In this example, @octocat owns any file in an apps directory
# anywhere in your repository.
apps/ @octocat

# In this example, @doctocat owns any file in the '/docs'
# directory in the root of your repository and any of its
# subdirectories.
/docs/ @doctocat

# In this example, any change inside the '/scripts' directory
# will require approval from @doctocat or @octocat.
/scripts/ @doctocat @octocat

# In this example, @octocat owns any file in a '/logs' directory such as
# '/build/logs', '/scripts/logs', and '/deeply/nested/logs'. Any changes
# in a '/logs' directory will require approval from @octocat.
**/logs @octocat

# In this example, @octocat owns any file in the '/apps'
# directory in the root of your repository except for the '/apps/github'
# subdirectory, as its owners are left empty.
/apps/ @octocat
/apps/github
`

func TestOfficialExamples(t *testing.T) {
	f, err := Parse([]byte(official))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Issues) != 0 {
		t.Fatalf("issues: %+v", f.Issues)
	}
	tests := []struct {
		path string
		want []string
	}{
		{"README.md", []string{"@global-owner1", "@global-owner2"}},
		{"src/deep/main.js", []string{"@js-owner"}},
		{"main.go", []string{"docs@example.com"}},
		{"notes/a.txt", []string{"@octo-org/octocats"}},
		{"build/logs/today.log", []string{"@octocat"}}, // **/logs is later than /build/logs/
		{"build/logs", []string{"@octocat"}},
		{"docs/getting-started.md", []string{"@doctocat"}}, // /docs/ is later than docs/*
		{"scripts/run.sh", []string{"@doctocat", "@octocat"}},
		{"deeply/nested/logs/x", []string{"@octocat"}},
		{"deeply/nested/logs", []string{"@octocat"}},
		{"apps/web/index.html", []string{"@octocat"}},
		{"apps/github/x", nil},
		{"apps/github", nil},
		{"other/apps/x.rb", []string{"@octocat"}},
	}
	for _, tt := range tests {
		if got := f.Owners(tt.path); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Owners(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
	if f.Covers("apps/github/x") || !f.Covers("README.md") {
		t.Error("Covers wrong")
	}
}

func TestDocsStarOnly(t *testing.T) {
	f, _ := Parse([]byte("docs/*  docs@example.com\n"))
	if got := f.Owners("docs/getting-started.md"); len(got) != 1 {
		t.Errorf("docs/* should match direct files: %v", got)
	}
	if got := f.Owners("docs/build-app/troubleshooting.md"); got != nil {
		t.Errorf("docs/* must not match nested files: %v", got)
	}
	if got := f.Owners("sub/docs/a.md"); got != nil {
		t.Errorf("docs/* is anchored: %v", got)
	}
}

func TestPatternSemantics(t *testing.T) {
	tests := []struct {
		pattern, path string
		match         bool
	}{
		{"*.md", "a/b/c.md", true},
		{"*.md", "c.mdx", false},
		{"/*.md", "c.md", true},
		{"/*.md", "a/c.md", false},
		{"/plugins/*/hooks/", "plugins/x/hooks/hooks.json", true},
		{"/plugins/*/hooks/", "plugins/x/y/hooks/hooks.json", false},
		{"/plugins/*/hooks/", "plugins/x/hooks", false},
		{"/plugins/*/.mcp.json", "plugins/x/.mcp.json", true},
		{"/plugins/*/.mcp.json", "plugins/x/y/.mcp.json", false},
		{"/.github/", ".github/CODEOWNERS", true},
		{"/.github/", ".github/workflows/a.yml", true},
		{"/ccshelf.toml", "ccshelf.toml", true},
		{"/ccshelf.toml", "x/ccshelf.toml", false},
		{"/plugins/design-kit", "plugins/design-kit/a/b", true},
		{"/plugins/design-kit", "plugins/design-kit", true},
		{"/plugins/design-kit", "plugins/design-kit2/a", false},
		{"/a/**/b", "a/b", true},
		{"/a/**/b", "a/x/y/b", true},
		{"/a/**/b", "a/x/y/c", false},
		{"/a/**", "a/x/y", true},
		{"/a/**", "a", false},
		{"**/a/b", "a/b", true},
		{"**/a/b", "x/y/a/b", true},
		{"**/a/b", "x/ya/b", false},
		{"f?o.txt", "foo.txt", true},
		{"f?o.txt", "f/o.txt", false},
		{"f?o.txt", "fo.txt", false},
		{"/a*b/c", "axxb/c", true},
		{"/a*b/c", "ax/xb/c", false},
		{`\#hash`, "#hash", true},
		{`\#hash`, "hash", false},
		{"/", "any/file", true},
		{"*", "x/y/z", true},
		{"/dir/", "dir/f", true},
		{"/dir/", "dir", false},
		{"a.b", "axb", false},
		{"/CASE", "case", false},
	}
	for _, tt := range tests {
		f, err := Parse([]byte(tt.pattern + " @o\n"))
		if err != nil || len(f.Rules) != 1 {
			t.Errorf("%q: parse = %+v, %v", tt.pattern, f, err)
			continue
		}
		if got := f.Covers(tt.path); got != tt.match {
			t.Errorf("pattern %q path %q: got %v want %v (regex %s)", tt.pattern, tt.path, got, tt.match, f.Rules[0].re)
		}
	}
}

func TestParseIssuesAndEdges(t *testing.T) {
	src := "!neg @a\n" +
		"[ab].txt @a\n" +
		"ok.txt notanowner\n" +
		"fine.txt @a # trailing @b\n" +
		"\ufeff# bom comment\n" +
		"\n" +
		"   indented.txt   @a/team   a@b.io  \n" +
		"/dir/ #only comment\n"
	f, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Issues) != 3 {
		t.Fatalf("issues = %+v", f.Issues)
	}
	for _, is := range f.Issues {
		if is.Line < 1 || is.Line > 3 || is.Message == "" {
			t.Errorf("bad issue %+v", is)
		}
	}
	if !reflect.DeepEqual(f.Owners("fine.txt"), []string{"@a"}) {
		t.Errorf("inline comment: %v", f.Owners("fine.txt"))
	}
	if !reflect.DeepEqual(f.Owners("x/indented.txt"), []string{"@a/team", "a@b.io"}) {
		t.Errorf("multi owners: %v", f.Owners("x/indented.txt"))
	}
	if r, ok := f.Match("dir/x"); !ok || len(r.Owners) != 0 || r.Line != 8 {
		t.Errorf("ownerless rule: %+v %v", r, ok)
	}
	if f.Owners("neg") != nil {
		t.Error("invalid line must be skipped")
	}
	// Paths are normalized.
	g, _ := Parse([]byte("/a/b @o\n"))
	for _, p := range []string{"./a/b", "/a/b", "a/b/", "a\\b"} {
		if !g.Covers(p) {
			t.Errorf("normalize %q", p)
		}
	}
	// Returned owners are a copy.
	o := g.Owners("a/b")
	o[0] = "x"
	if g.Owners("a/b")[0] != "@o" {
		t.Error("Owners must return a copy")
	}
	if _, err := Parse(make([]byte, MaxSize+1)); err == nil {
		t.Error("oversized file accepted")
	}
	if h, _ := Parse(nil); h.Covers("x") {
		t.Error("empty file covers nothing")
	}
}

func TestFind(t *testing.T) {
	root := t.TempDir()
	f, p, err := Find(root)
	if f != nil || p != "" || err != nil {
		t.Errorf("none: %v %q %v", f, p, err)
	}
	write := func(rel, content string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("docs/CODEOWNERS", "* @docs\n")
	if f, p, err = Find(root); err != nil || p != "docs/CODEOWNERS" || f.Owners("x")[0] != "@docs" {
		t.Errorf("docs location: %v %q %v", f, p, err)
	}
	write("CODEOWNERS", "* @root\n")
	if f, p, _ = Find(root); p != "CODEOWNERS" || f.Owners("x")[0] != "@root" {
		t.Errorf("root location: %q", p)
	}
	write(".github/CODEOWNERS", "* @gh\n")
	if f, p, _ = Find(root); p != ".github/CODEOWNERS" || f.Owners("x")[0] != "@gh" {
		t.Errorf(".github location: %q", p)
	}
	write(".github/CODEOWNERS", strings.Repeat("#", MaxSize+1))
	if _, _, err = Find(root); err == nil {
		t.Error("oversized file should fail")
	}
}
