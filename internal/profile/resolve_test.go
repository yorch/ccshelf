package profile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func orgSrc(t *testing.T) Source {
	return DirSource(KindOrg, filepath.Join(fixture(t, "tree", "org"), "profiles"))
}

func TestResolveOrgFixture(t *testing.T) {
	r, err := Resolve("frontend", []Source{orgSrc(t)}, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "frontend" || r.Kind != KindOrg || len(r.Chain) != 2 || r.Chain[0].Name != "base" || r.Chain[1].Name != "frontend" {
		t.Fatalf("chain: %+v", r)
	}
	m := r.Merged
	if !reflect.DeepEqual(m.Plugins.Include, []string{"core@acme", "design-kit@acme"}) {
		t.Errorf("include = %v", m.Plugins.Include)
	}
	if !reflect.DeepEqual(m.Plugins.Exclude, []string{"noisy@acme"}) {
		t.Errorf("exclude = %v", m.Plugins.Exclude)
	}
	if m.Plugins.Mode != "allow-only" || m.Policy.OnBlocked != "warn" || m.Status != "active" || !m.InheritsUserSettings() {
		t.Errorf("defaults: %+v", m)
	}
	if m.Session.Env["CCSHELF_VAR_TEAM"] != "platform" || m.Session.Env["FIGMA_TOKEN_REF"] == "" {
		t.Errorf("env: %v", m.Session.Env)
	}
	if m.Owner != "@web-platform" || len(m.WhenToUse) != 2 {
		t.Errorf("identity: %+v", m)
	}
	if len(r.MCP) != 2 || r.MCP["figma"].Command != "npx" {
		t.Errorf("mcp: %v", r.MCP)
	}
	if string(r.Prompt) != "Prefer accessible markup.\nAlways run the visual checks.\n" {
		t.Errorf("prompt: %q", r.Prompt)
	}
}

func TestDescribeGolden(t *testing.T) {
	r, err := Resolve("frontend", []Source{orgSrc(t)}, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := Describe(r)
	golden := filepath.Join("testdata", "describe-frontend.golden.txt")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		_ = os.WriteFile(golden, []byte(got), 0o600)
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("describe mismatch:\n%s", got)
	}
	if strings.Contains(got, fixture(t)) {
		t.Error("absolute path in Describe output")
	}
}

func TestMergeSemantics(t *testing.T) {
	root := mk(t, map[string]string{
		"profiles/a.toml": `name = "a"
description = "A"
owner = "@a"
status = "experimental"
when_to_use = ["a thing"]
account = "one"
[plugins]
mode = "additive"
include = ["p1@m", "p2@m", "p3@m"]
exclude = ["x1@m"]
[skills]
off = ["s-off"]
name_only = ["s1", "s2"]
[mcp]
claudeai_connectors = "keep"
strict = true
[session]
model = "opus"
effort = "low"
[session.env]
CCSHELF_VAR_A = "a"
CCSHELF_VAR_SHARED = "from-a"
[policy]
on_blocked = "fail"
`,
		"profiles/b.toml": `name = "b"
description = "B"
[plugins]
include = ["p4@m", "p1@m", "x1@m"]
exclude = ["p2@m"]
[skills]
off = ["s1"]
name_only = ["s-off", "s3"]
[mcp]
strict = false
[session]
effort = "high"
inherit_user_settings = false
[session.env]
CCSHELF_VAR_SHARED = "from-b"
CCSHELF_VAR_B = "b"
`,
		"profiles/c.toml": `name = "c"
description = "C"
extends = ["a", "b"]
account = "two"
[plugins]
include = ["p5@m"]
`,
	})
	r, err := Resolve("c", []Source{src(KindPersonal, root)}, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	m := r.Merged
	check := func(name string, got, want any) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %v; want %v", name, got, want)
		}
	}
	check("chain", chainNames(r), []string{"a", "b", "c"})
	check("include", m.Plugins.Include, []string{"p1@m", "p3@m", "p4@m", "p5@m"})
	check("exclude", m.Plugins.Exclude, []string{"x1@m", "p2@m"})
	check("skills.off", m.Skills.Off, []string{"s-off", "s1"})
	check("skills.name_only", m.Skills.NameOnly, []string{"s2", "s3"})
	check("mode", m.Plugins.Mode, "additive")
	check("connectors", m.MCP.ClaudeAIConnectors, "keep")
	check("strict", *m.MCP.Strict, false)
	check("model", m.Session.Model, "opus")
	check("effort", m.Session.Effort, "high")
	check("inherit", m.InheritsUserSettings(), false)
	check("on_blocked", m.Policy.OnBlocked, "fail")
	check("account", m.Account, "two")
	check("env", m.Session.Env, map[string]string{"CCSHELF_VAR_A": "a", "CCSHELF_VAR_SHARED": "from-b", "CCSHELF_VAR_B": "b"})
	// not inherited
	check("description", m.Description, "C")
	check("owner", m.Owner, "")
	check("status", m.Status, "active")
	check("when_to_use", m.WhenToUse, []string(nil))
	check("extends", m.Extends, []string{"a", "b"})
	wantW := []string{
		"profile b includes x1@m but a excludes it. Exclusion wins",
		"profile b sets skill s-off to name-only but a turns it off. Off wins",
	}
	for _, w := range wantW {
		if !contains(r.Warnings, w) {
			t.Errorf("missing warning %q in %v", w, r.Warnings)
		}
	}
}

func chainNames(r *Resolved) []string {
	var n []string
	for _, f := range r.Chain {
		n = append(n, f.Name)
	}
	return n
}

func TestMergeDiamondAndOrder(t *testing.T) {
	root := mk(t, map[string]string{
		"profiles/base.toml": "name = \"base\"\n[plugins]\ninclude = [\"b@m\"]\n",
		"profiles/l.toml":    "name = \"l\"\nextends = [\"base\"]\n[plugins]\ninclude = [\"l@m\"]\n",
		"profiles/r.toml":    "name = \"r\"\nextends = [\"base\"]\n[plugins]\ninclude = [\"r@m\"]\n",
		"profiles/top.toml":  "name = \"top\"\nextends = [\"l\", \"r\"]\n",
	})
	r, err := Resolve("top", []Source{src(KindOrg, root)}, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(chainNames(r), []string{"base", "l", "r", "top"}) {
		t.Errorf("chain = %v", chainNames(r))
	}
	if !reflect.DeepEqual(r.Merged.Plugins.Include, []string{"b@m", "l@m", "r@m"}) {
		t.Errorf("include = %v", r.Merged.Plugins.Include)
	}
}

func TestResolveNotFound(t *testing.T) {
	_, err := Resolve("nope", []Source{orgSrc(t)}, ResolveOptions{})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	_, err = Resolve("x", nil, ResolveOptions{})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	root := mk(t, map[string]string{"profiles/a.toml": "name = \"a\"\nextends = [\"ghost\"]\n"})
	_, err = Resolve("a", []Source{src(KindOrg, root)}, ResolveOptions{})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("missing parent: %v", err)
	}
	mustErrContain(t, err, `extends "ghost"`)
}

func TestResolveInvalidProfile(t *testing.T) {
	root := mk(t, map[string]string{"profiles/a.toml": "name = \"a\"\nhooks = 1\n"})
	_, err := Resolve("a", []Source{src(KindOrg, root)}, ResolveOptions{})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("err = %v", err)
	}
}

func TestCollisions(t *testing.T) {
	two := func(name string) map[string]string { return map[string]string{"profiles/dup.toml": "name = \"dup\"\n"} }
	org1, org2 := mk(t, two("")), mk(t, two(""))
	pers, pers2, proj := mk(t, two("")), mk(t, two("")), mk(t, two(""))

	// org vs org
	_, err := Resolve("dup", []Source{src(KindOrg, org1), src(KindOrg, org2)}, ResolveOptions{})
	if !errors.Is(err, ErrCollision) {
		t.Errorf("org/org: %v", err)
	}
	// personal shadows org, with a warning naming the shadowed source
	r, err := Resolve("dup", []Source{src(KindOrg, org1), src(KindPersonal, pers)}, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindPersonal || len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "personal profile dup shadows") || !strings.Contains(r.Warnings[0], org1) {
		t.Errorf("shadow: %+v", r)
	}
	// personal shadows two org sources
	r, err = Resolve("dup", []Source{src(KindOrg, org1), src(KindOrg, org2), src(KindPersonal, pers)}, ResolveOptions{})
	if err != nil || r.Kind != KindPersonal {
		t.Errorf("personal vs 2 orgs: %v", err)
	}
	// two personal
	_, err = Resolve("dup", []Source{src(KindPersonal, pers), src(KindPersonal, pers2)}, ResolveOptions{})
	if !errors.Is(err, ErrCollision) {
		t.Errorf("personal/personal: %v", err)
	}
	// project never shadows or is shadowed
	for _, others := range [][]Source{{src(KindOrg, org1)}, {src(KindPersonal, pers)}} {
		_, err = Resolve("dup", append(others, src(KindProject, proj)), ResolveOptions{AllowProject: true})
		if !errors.Is(err, ErrCollision) {
			t.Errorf("project vs %v: %v", others[0].Kind(), err)
		}
	}
	// without AllowProject the project source is invisible, so no collision
	r, err = Resolve("dup", []Source{src(KindOrg, org1), src(KindProject, proj)}, ResolveOptions{})
	if err != nil || r.Kind != KindOrg {
		t.Errorf("untrusted project must be ignored: %v", err)
	}
	// the same source listed twice is not a collision
	s := src(KindOrg, org1)
	if _, err = Resolve("dup", []Source{s, s}, ResolveOptions{}); err != nil {
		t.Errorf("duplicate source: %v", err)
	}
}

func TestProjectProfiles(t *testing.T) {
	proj := mk(t, map[string]string{"profiles/p.toml": "name = \"p\"\n[plugins]\ninclude = [\"a@b\"]\n"})
	_, err := Resolve("p", []Source{src(KindProject, proj)}, ResolveOptions{})
	if !errors.Is(err, ErrProjectNotTrusted) {
		t.Errorf("untrusted: %v", err)
	}
	r, err := Resolve("p", []Source{src(KindProject, proj)}, ResolveOptions{AllowProject: true})
	if err != nil || r.Kind != KindProject {
		t.Errorf("trusted: %v", err)
	}
}

func TestProjectForbiddenFields(t *testing.T) {
	cases := map[string]string{
		"mcp.servers":                       "[mcp]\nservers = [\"figma\"]\n",
		"session.env":                       "[session.env]\nCCSHELF_VAR_A = \"1\"\n",
		"session.append_system_prompt_file": "[session]\nappend_system_prompt_file = \"prompts/x.md\"\n",
		"account":                           "account = \"work\"\n",
		"session.inherit_user_settings":     "[session]\ninherit_user_settings = false\n",
	}
	for field, body := range cases {
		t.Run(field, func(t *testing.T) {
			root := mk(t, map[string]string{
				"profiles/p.toml":   "name = \"p\"\n" + body,
				"prompts/x.md":      "x",
				"mcp/registry.toml": "[servers.figma]\ncommand = \"x\"\n",
			})
			_, err := Resolve("p", []Source{src(KindProject, root)}, ResolveOptions{AllowProject: true})
			if !errors.Is(err, ErrProjectForbidden) || !strings.Contains(err.Error(), field) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestProjectExtendsPrivilegedParent(t *testing.T) {
	org := mk(t, map[string]string{
		"profiles/withenv.toml":  "name = \"withenv\"\n[session.env]\nCCSHELF_VAR_A = \"1\"\n",
		"profiles/plain.toml":    "name = \"plain\"\n[plugins]\ninclude = [\"a@b\"]\n",
		"profiles/viaplain.toml": "name = \"viaplain\"\nextends = [\"withenv\"]\n",
	})
	for _, parent := range []string{"withenv", "viaplain"} {
		proj := mk(t, map[string]string{"profiles/p.toml": fmt.Sprintf("name = \"p\"\nextends = [%q]\n", parent)})
		_, err := Resolve("p", []Source{src(KindOrg, org), src(KindProject, proj)}, ResolveOptions{AllowProject: true})
		if !errors.Is(err, ErrProjectForbidden) {
			t.Errorf("parent %s: %v", parent, err)
		}
	}
	proj := mk(t, map[string]string{"profiles/p.toml": "name = \"p\"\nextends = [\"plain\"]\n"})
	r, err := Resolve("p", []Source{src(KindOrg, org), src(KindProject, proj)}, ResolveOptions{AllowProject: true})
	if err != nil || !contains(r.Merged.Plugins.Include, "a@b") {
		t.Errorf("plugin-only parent should be fine: %v", err)
	}
}

func TestSharedDropsUserLayer(t *testing.T) {
	org := mk(t, map[string]string{
		"profiles/bad.toml":  "name = \"bad\"\n[session]\ninherit_user_settings = false\n",
		"profiles/good.toml": "name = \"good\"\n[session]\ninherit_user_settings = true\n",
		"profiles/kid.toml":  "name = \"kid\"\nextends = [\"bad\"]\n",
	})
	for _, n := range []string{"bad", "kid"} {
		_, err := Resolve(n, []Source{src(KindOrg, org)}, ResolveOptions{})
		if !errors.Is(err, ErrSharedDropsUserLayer) {
			t.Errorf("%s: %v", n, err)
		}
	}
	if _, err := Resolve("good", []Source{src(KindOrg, org)}, ResolveOptions{}); err != nil {
		t.Error(err)
	}
	// personal profile may drop the user layer, even on top of org parents
	pers := mk(t, map[string]string{"profiles/mine.toml": "name = \"mine\"\nextends = [\"good\"]\n[session]\ninherit_user_settings = false\n"})
	r, err := Resolve("mine", []Source{src(KindOrg, org), src(KindPersonal, pers)}, ResolveOptions{})
	if err != nil || r.Merged.InheritsUserSettings() {
		t.Errorf("personal: %v", err)
	}
	// an org profile that extends a personal profile which drops it is refused
	orgKid := mk(t, map[string]string{"profiles/kid2.toml": "name = \"kid2\"\nextends = [\"mine\"]\n"})
	_, err = Resolve("kid2", []Source{src(KindOrg, orgKid), src(KindPersonal, pers), src(KindOrg, org)}, ResolveOptions{})
	if !errors.Is(err, ErrSharedDropsUserLayer) {
		t.Errorf("org extends personal: %v", err)
	}
}

func TestCycleAndDepth(t *testing.T) {
	root := mk(t, map[string]string{
		"profiles/a.toml": "name = \"a\"\nextends = [\"b\"]\n",
		"profiles/b.toml": "name = \"b\"\nextends = [\"c\"]\n",
		"profiles/c.toml": "name = \"c\"\nextends = [\"a\"]\n",
		"profiles/s.toml": "name = \"s\"\nextends = [\"s\"]\n",
	})
	_, err := Resolve("a", []Source{src(KindOrg, root)}, ResolveOptions{})
	if !errors.Is(err, ErrCycle) || !strings.Contains(err.Error(), "a -> b -> c -> a") {
		t.Errorf("cycle: %v", err)
	}
	_, err = Resolve("s", []Source{src(KindOrg, root)}, ResolveOptions{})
	if !errors.Is(err, ErrCycle) {
		t.Errorf("self: %v", err)
	}
	chain := func(n int) map[string]string {
		files := map[string]string{}
		for i := 0; i < n; i++ {
			body := fmt.Sprintf("name = \"p%d\"\n", i)
			if i+1 < n {
				body += fmt.Sprintf("extends = [\"p%d\"]\n", i+1)
			}
			files[fmt.Sprintf("profiles/p%d.toml", i)] = body
		}
		return files
	}
	if _, err := Resolve("p0", []Source{src(KindOrg, mk(t, chain(MaxExtendsDepth)))}, ResolveOptions{}); err != nil {
		t.Errorf("depth %d: %v", MaxExtendsDepth, err)
	}
	_, err = Resolve("p0", []Source{src(KindOrg, mk(t, chain(MaxExtendsDepth+1)))}, ResolveOptions{})
	if !errors.Is(err, ErrDepth) {
		t.Errorf("depth+1: %v", err)
	}
}

func TestPromptRules(t *testing.T) {
	prof := func(p string) string { return "name = \"a\"\n[session]\nappend_system_prompt_file = \"" + p + "\"\n" }
	root := mk(t, map[string]string{
		"profiles/a.toml":    prof("prompts/ok.md"),
		"prompts/ok.md":      "line1\r\nline2\r\n",
		"prompts/big.md":     strings.Repeat("x", MaxPromptSize+1),
		"profiles/big.toml":  strings.Replace(prof("prompts/big.md"), `"a"`, `"big"`, 1),
		"profiles/gone.toml": strings.Replace(prof("prompts/gone.md"), `"a"`, `"gone"`, 1),
		"profiles/dir.toml":  strings.Replace(prof("prompts"), `"a"`, `"dir"`, 1),
		"secret.md":          "secret",
	})
	s := src(KindPersonal, root)
	r, err := Resolve("a", []Source{s}, ResolveOptions{})
	if err != nil || string(r.Prompt) != "line1\nline2\n" {
		t.Fatalf("ok: %q %v", r.Prompt, err)
	}
	for _, n := range []string{"big", "gone", "dir"} {
		if _, err := Resolve(n, []Source{s}, ResolveOptions{}); err == nil {
			t.Errorf("%s accepted", n)
		}
	}
	if runtime.GOOS != "windows" {
		outside := filepath.Join(t.TempDir(), "secret.md")
		_ = os.WriteFile(outside, []byte("secret"), 0o600)
		if err := os.Symlink(outside, filepath.Join(root, "prompts", "link.md")); err == nil {
			_ = os.WriteFile(filepath.Join(root, "profiles", "link.toml"), []byte(strings.Replace(prof("prompts/link.md"), `"a"`, `"link"`, 1)), 0o600)
			_, err := Resolve("link", []Source{s}, ResolveOptions{})
			if !errors.Is(err, ErrPath) {
				t.Errorf("symlink escape: %v", err)
			}
		}
		// B7: a symlink is refused even when it stays inside the root
		if err := os.Symlink(filepath.Join(root, "prompts", "ok.md"), filepath.Join(root, "prompts", "alias.md")); err == nil {
			_ = os.WriteFile(filepath.Join(root, "profiles", "alias.toml"), []byte(strings.Replace(prof("prompts/alias.md"), `"a"`, `"alias"`, 1)), 0o600)
			if _, err := Resolve("alias", []Source{s}, ResolveOptions{}); !errors.Is(err, ErrPath) {
				t.Errorf("inside symlink: %v", err)
			}
		}
	}
}

// fakeSource has no root, to cover the "no root" paths.
type fakeSource struct {
	Source
}

func (f fakeSource) Root() string { return "" }

func (f fakeSource) Open(name string) (*File, error) {
	file, err := f.Source.Open(name)
	if file != nil {
		file.Source = f
	}
	return file, err
}

func TestPromptNoRoot(t *testing.T) {
	root := mk(t, map[string]string{"profiles/a.toml": "name = \"a\"\n[session]\nappend_system_prompt_file = \"prompts/p.md\"\n"})
	_, err := Resolve("a", []Source{fakeSource{src(KindPersonal, root)}}, ResolveOptions{})
	if !errors.Is(err, ErrPath) {
		t.Errorf("err = %v", err)
	}
}

func TestMCPResolution(t *testing.T) {
	reg := "[servers.figma]\ncommand = \"npx\"\n"
	org := mk(t, map[string]string{
		"profiles/a.toml":   "name = \"a\"\n[mcp]\nservers = [\"figma\"]\n",
		"profiles/bad.toml": "name = \"bad\"\n[mcp]\nservers = [\"ghost\"]\n",
		"mcp/registry.toml": reg,
	})
	r, err := Resolve("a", []Source{src(KindOrg, org)}, ResolveOptions{})
	if err != nil || r.MCP["figma"].Command != "npx" {
		t.Fatalf("a: %v", err)
	}
	_, err = Resolve("bad", []Source{src(KindOrg, org)}, ResolveOptions{})
	if !errors.Is(err, ErrUnknownMCPServer) {
		t.Errorf("unknown: %v", err)
	}
	// no registry at all
	noReg := mk(t, map[string]string{"profiles/a.toml": "name = \"a\"\n[mcp]\nservers = [\"figma\"]\n"})
	_, err = Resolve("a", []Source{src(KindOrg, noReg)}, ResolveOptions{})
	if !errors.Is(err, ErrUnknownMCPServer) {
		t.Errorf("no registry: %v", err)
	}
	// invalid registry
	badReg := mk(t, map[string]string{"profiles/a.toml": "name = \"a\"\n[mcp]\nservers = [\"figma\"]\n", "mcp/registry.toml": "[servers.figma]\ncommand = \"x\"\nbogus = 1\n"})
	_, err = Resolve("a", []Source{src(KindOrg, badReg)}, ResolveOptions{})
	mustErrContain(t, err, "bogus")
	// personal child lists a server defined only by the org parent's registry
	pers := mk(t, map[string]string{"profiles/mine.toml": "name = \"mine\"\nextends = [\"a\"]\n[mcp]\nservers = [\"figma\"]\n"})
	if _, err := Resolve("mine", []Source{src(KindOrg, org), src(KindPersonal, pers)}, ResolveOptions{}); err != nil {
		t.Errorf("child+parent same def: %v", err)
	}
	// conflicting definitions in two registries
	pers2 := mk(t, map[string]string{
		"profiles/mine.toml": "name = \"mine\"\nextends = [\"a\"]\n[mcp]\nservers = [\"figma\"]\n",
		"mcp/registry.toml":  "[servers.figma]\ncommand = \"evil\"\n",
	})
	_, err = Resolve("mine", []Source{src(KindOrg, org), src(KindPersonal, pers2)}, ResolveOptions{})
	if !errors.Is(err, ErrCollision) {
		t.Errorf("conflict: %v", err)
	}
}

func TestDeprecatedWarning(t *testing.T) {
	r, err := Resolve("old", []Source{orgSrc(t)}, ResolveOptions{})
	if err != nil || !contains(r.Warnings, "profile old is deprecated. Use frontend") {
		t.Errorf("warnings: %v %v", r, err)
	}
	if r.Merged.Status != "deprecated" || r.Merged.SupersededBy != "frontend" {
		t.Errorf("merged: %+v", r.Merged)
	}
}

func TestEnvDenialsAtParse(t *testing.T) {
	for _, name := range []string{"CCSHELF_PROFILE", "FOO", "MY_VAR", "ANTHROPIC_BASE_URL", "HTTPS_PROXY", "NODE_OPTIONS", "PATH"} {
		raw := fmt.Sprintf("name = \"x\"\n[session.env]\n%s = \"v\"\n", name)
		if _, err := Parse([]byte(raw), ""); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	for _, name := range []string{"FIGMA_TOKEN_REF", "CCSHELF_VAR_X"} {
		raw := fmt.Sprintf("name = \"x\"\n[session.env]\n%s = \"v\"\n", name)
		if _, err := Parse([]byte(raw), ""); err != nil {
			t.Errorf("%s rejected: %v", name, err)
		}
	}
}
