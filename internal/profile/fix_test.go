package profile

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// ---- B1: exact key spelling ----

func TestParseRejectsCaseVariantKeys(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"top level key", "Name = \"x\"\n", `key "Name" must be spelled "name"`},
		{"top level key after the right one", "name = \"x\"\nDescription = \"d\"\n", `key "Description" must be spelled "description"`},
		{"plugins table", "name = \"x\"\n[Plugins]\nmode = \"additive\"\n", `key "Plugins" must be spelled "plugins"`},
		{"plugins key", "name = \"x\"\n[plugins]\nMode = \"additive\"\n", `key "Mode" must be spelled "mode"`},
		{"skills table", "name = \"x\"\n[SKILLS]\noff = [\"a\"]\n", `must be spelled "skills"`},
		{"skills key", "name = \"x\"\n[skills]\nName_Only = [\"a\"]\n", `must be spelled "name_only"`},
		{"mcp table", "name = \"x\"\n[MCP]\nservers = [\"a\"]\n", `must be spelled "mcp"`},
		{"mcp key", "name = \"x\"\n[mcp]\nStrict = true\n", `must be spelled "strict"`},
		{"mcp connectors", "name = \"x\"\n[mcp]\nclaudeAI_connectors = \"none\"\n", `must be spelled "claudeai_connectors"`},
		{"session table", "name = \"x\"\n[Session]\nmodel = \"opus\"\n", `must be spelled "session"`},
		{"session key", "name = \"x\"\n[session]\nModel = \"opus\"\n", `must be spelled "model"`},
		{"session env table", "name = \"x\"\n[session.Env]\nCCSHELF_VAR_A = \"1\"\n", `must be spelled "env"`},
		{"policy table", "name = \"x\"\n[Policy]\non_blocked = \"fail\"\n", `must be spelled "policy"`},
		{"policy key", "name = \"x\"\n[policy]\nOn_Blocked = \"fail\"\n", `must be spelled "on_blocked"`},
		{"the dangerous override", "name = \"x\"\n[session]\ninherit_user_settings = true\nInherit_User_Settings = false\n", `key "Inherit_User_Settings" must be spelled "inherit_user_settings"`},
		{"superseded_by", "name = \"x\"\nstatus = \"deprecated\"\nSuperseded_By = \"y\"\n", `must be spelled "superseded_by"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.body), "")
			mustErrContain(t, err, tc.want)
		})
	}
	if _, err := Parse([]byte("name = \"x\"\n[session]\ninherit_user_settings = true\n"), ""); err != nil {
		t.Errorf("exact spelling rejected: %v", err)
	}
	// the problem carries the dotted path and a line number
	_, err := Parse([]byte("name = \"x\"\n[session]\nModel = \"opus\"\n"), "")
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Problems) != 1 || ve.Problems[0].Field != "session.Model" || ve.Problems[0].Line != 3 {
		t.Errorf("problems = %+v", ve)
	}
}

func TestRegistryRejectsCaseVariantKeys(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"servers table", "[Servers.a]\ncommand = \"x\"\n", `must be spelled "servers"`},
		{"server key", "[servers.a]\nCommand = \"x\"\n", `must be spelled "command"`},
		{"type key", "[servers.a]\nType = \"stdio\"\ncommand = \"x\"\n", `must be spelled "type"`},
		{"env_refs key", "[servers.a]\ncommand = \"x\"\nEnv_Refs = [\"A_REF\"]\n", `must be spelled "env_refs"`},
		{"url key", "[servers.a]\ntype = \"http\"\nURL = \"https://x.example\"\n", `must be spelled "url"`},
		{"windows table", "[servers.a]\ncommand = \"x\"\n[servers.a.Windows]\ncommand = \"c\"\n", `must be spelled "windows"`},
		{"windows key", "[servers.a]\ncommand = \"x\"\n[servers.a.windows]\nCommand = \"c\"\n", `must be spelled "command"`},
		{"macos args", "[servers.a]\ncommand = \"x\"\n[servers.a.macos]\ncommand = \"c\"\nArgs = [\"1\"]\n", `must be spelled "args"`},
		{"linux table", "[servers.a]\ncommand = \"x\"\n[servers.a.LINUX]\ncommand = \"c\"\n", `must be spelled "linux"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseRegistry([]byte(tc.body), "registry.toml")
			mustErrContain(t, err, tc.want)
		})
	}
	// server names are free-form: mixed case is fine
	if _, err := ParseRegistry([]byte("[servers.MixedCase]\ncommand = \"x\"\n"), "r"); err != nil {
		t.Error(err)
	}
}

func TestSpellingProblemsInvalidTOML(t *testing.T) {
	if _, err := spellingProblems([]byte("= x"), Manifest{}, ""); err == nil {
		t.Error("expected an error")
	}
}

// ---- B2: confinement ----

func TestCheckPromptPath(t *testing.T) {
	good := []string{"prompts/a.md", "prompts/sub/a.md", "prompts/a.b.md"}
	bad := []string{
		"", "a.md", "prompts", "prompts/", ".ssh/id_ed25519", "prompts/.hidden", "prompts/.git/config", "prompts/sub/.env",
		"../x", "prompts/../x", "/etc/passwd", "C:/x", `prompts\a.md`, "Prompts/a.md", "other/a.md", "profiles/a.toml", "mcp/registry.toml",
	}
	for _, p := range good {
		if err := CheckPromptPath(p); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
	for _, p := range bad {
		if err := CheckPromptPath(p); err == nil {
			t.Errorf("%q accepted", p)
		}
	}
}

// rawSource is a Source whose profiles are built in memory, so that a hostile
// manifest can reach the resolver without passing through Parse.
type rawSource struct {
	id    string
	kind  Kind
	root  string
	files map[string]*Manifest
}

func (r *rawSource) ID() string     { return r.id }
func (r *rawSource) Kind() Kind     { return r.kind }
func (r *rawSource) Root() string   { return r.root }
func (r *rawSource) Commit() string { return "" }
func (r *rawSource) Names() ([]string, error) {
	return sortedKeys(r.files), nil
}

func (r *rawSource) Open(name string) (*File, error) {
	m, ok := r.files[name]
	if !ok {
		return nil, ErrNotFound
	}
	return &File{Name: name, Manifest: m, Source: r, Raw: []byte(name)}, nil
}

func TestResolverConfinesPromptPaths(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "data")
	if err := os.MkdirAll(filepath.Join(root, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ssh", "id_ed25519"), []byte("KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "x"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{".ssh/id_ed25519", "../x", "prompts/../.ssh/id_ed25519", "profiles/a.toml", "x"} {
		t.Run(p, func(t *testing.T) {
			s := &rawSource{id: "raw:a", kind: KindOrg, root: root, files: map[string]*Manifest{
				"a": {Name: "a", Session: Session{AppendSystemPromptFile: p}},
			}}
			r, err := Resolve("a", []Source{s}, ResolveOptions{})
			if !errors.Is(err, ErrPath) {
				t.Fatalf("err = %v, resolved prompt %q", err, func() []byte {
					if r != nil {
						return r.Prompt
					}
					return nil
				}())
			}
		})
	}
}

func TestHostileHomeSource(t *testing.T) {
	isolate(t)
	home, _ := os.UserHomeDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(home, ".ssh", "id_ed25519"), []byte("KEY"), 0o600))
	must(os.MkdirAll(filepath.Join(home, "profiles"), 0o700))
	must(os.WriteFile(filepath.Join(home, "profiles", "a.toml"), []byte("name = \"a\"\n[session]\nappend_system_prompt_file = \".ssh/id_ed25519\"\n"), 0o600))
	must(os.WriteFile(filepath.Join(home, "profiles", "b.toml"), []byte("name = \"b\"\n"), 0o600))
	s := DirSource(KindPersonal, filepath.Join(home, "profiles"))
	if s.Root() != "" {
		t.Errorf("Root = %q: the home directory must be refused as a root", s.Root())
	}
	if _, err := s.Names(); !errors.Is(err, ErrPath) {
		t.Errorf("Names: %v", err)
	}
	if _, err := s.Open("b"); !errors.Is(err, ErrPath) {
		t.Errorf("Open: %v", err)
	}
	for _, n := range []string{"a", "b"} {
		if _, err := Resolve(n, []Source{s}, ResolveOptions{}); err == nil {
			t.Errorf("%s resolved from a home-directory source", n)
		}
	}
	if _, err := List([]Source{s}); err == nil {
		t.Error("List accepted a refused source")
	}
	// and the same prompt can never be named by a profile at all
	_, err := Parse([]byte("name = \"a\"\n[session]\nappend_system_prompt_file = \".ssh/id_ed25519\"\n"), "")
	mustErrContain(t, err, "must be a file below prompts/")
	_, err = Parse([]byte("name = \"a\"\n[session]\nappend_system_prompt_file = \"../x\"\n"), "")
	mustErrContain(t, err, "..")
	_, err = Parse([]byte("name = \"a\"\n[session]\nappend_system_prompt_file = \"prompts/.hidden\"\n"), "")
	mustErrContain(t, err, `starting with "."`)
}

func TestDirSourceRefusedRoots(t *testing.T) {
	isolate(t)
	cfg, err := PersonalDir()
	if err != nil {
		t.Fatal(err)
	}
	cfgRoot := filepath.Dir(cfg)
	fsRoot := filepath.VolumeName(cfgRoot) + string(filepath.Separator)
	home, _ := os.UserHomeDir()
	cases := map[string]string{
		"home bare":    home,
		"config bare":  cfgRoot,
		"fs root":      fsRoot,
		"fs root dir":  filepath.Join(fsRoot, "profiles"),
		"home profile": filepath.Join(home, "profiles"),
	}
	for name, dir := range cases {
		t.Run(name, func(t *testing.T) {
			s := DirSource(KindOrg, dir)
			if s.Root() != "" {
				t.Errorf("Root = %q", s.Root())
			}
			if _, err := s.Names(); !errors.Is(err, ErrPath) {
				t.Errorf("Names: %v", err)
			}
		})
	}
	// the ccshelf config directory is fine as the parent of profiles/
	ok := DirSource(KindPersonal, cfg)
	if ok.Root() != cfgRoot {
		t.Errorf("personal root = %q, want %q", ok.Root(), cfgRoot)
	}
	if _, err := ok.Names(); err != nil {
		t.Errorf("personal source refused: %v", err)
	}
}

func TestDirSourceRootDerivation(t *testing.T) {
	base := t.TempDir()
	derived := DirSource(KindOrg, filepath.Join(base, "profiles"))
	if derived.Root() != base {
		t.Errorf("derived root = %q, want %q", derived.Root(), base)
	}
	bare := DirSource(KindOrg, filepath.Join(base, "mine"))
	if bare.Root() != filepath.Join(base, "mine") {
		t.Errorf("bare root = %q", bare.Root())
	}
	// a bare folder cannot carry prompts or a registry
	root := filepath.Join(base, "mine")
	for rel, body := range map[string]string{
		"a.toml":              "name = \"a\"\n[session]\nappend_system_prompt_file = \"prompts/p.md\"\n",
		"b.toml":              "name = \"b\"\n[mcp]\nservers = [\"s\"]\n",
		"prompts/p.md":        "text",
		"mcp/registry.toml":   "[servers.s]\ncommand = \"x\"\n",
		"c.toml":              "name = \"c\"\n",
		"profiles/other.toml": "name = \"other\"\n",
	} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, err := Resolve("a", []Source{bare}, ResolveOptions{})
	if !errors.Is(err, ErrPath) || !strings.Contains(err.Error(), `not named "profiles"`) {
		t.Errorf("prompt in a bare source: %v", err)
	}
	_, err = Resolve("b", []Source{bare}, ResolveOptions{})
	if !errors.Is(err, ErrUnknownMCPServer) {
		t.Errorf("registry in a bare source: %v", err)
	}
	if _, err := Resolve("c", []Source{bare}, ResolveOptions{}); err != nil {
		t.Errorf("plain profile in a bare source: %v", err)
	}
}

func TestRegistryPathIsFixed(t *testing.T) {
	// a registry anywhere but <root>/mcp/registry.toml is never read
	root := mk(t, map[string]string{
		"profiles/a.toml": "name = \"a\"\n[mcp]\nservers = [\"s\"]\n",
		"registry.toml":   "[servers.s]\ncommand = \"x\"\n",
	})
	_, err := Resolve("a", []Source{src(KindOrg, root)}, ResolveOptions{})
	if !errors.Is(err, ErrUnknownMCPServer) {
		t.Errorf("err = %v", err)
	}
}

// ---- B7: symlinks ----

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks unavailable: " + err.Error())
	}
}

func TestSymlinksAreRefusedEverywhere(t *testing.T) {
	root := mk(t, map[string]string{
		"profiles/real.toml": "name = \"real\"\n",
		"prompts/ok.md":      "ok",
		"mcp/registry.toml":  "[servers.s]\ncommand = \"x\"\n",
		"elsewhere/p.md":     "p",
	})
	symlinkOrSkip(t, filepath.Join(root, "profiles", "real.toml"), filepath.Join(root, "profiles", "link.toml"))
	symlinkOrSkip(t, filepath.Join(root, "elsewhere"), filepath.Join(root, "prompts", "dir"))
	s := src(KindOrg, root)
	if _, err := s.Open("link"); !errors.Is(err, ErrPath) {
		t.Errorf("profile symlink: %v", err)
	}
	if _, _, err := readConfined(root, "prompts/dir/p.md", 100); !errors.Is(err, ErrPath) {
		t.Errorf("directory symlink in a prompt path: %v", err)
	}
	if _, _, err := readConfined(root, "prompts/ok.md", 100); err != nil {
		t.Errorf("plain file: %v", err)
	}
	// the registry file itself
	reg := mk(t, map[string]string{
		"profiles/a.toml": "name = \"a\"\n[mcp]\nservers = [\"s\"]\n",
		"real-reg.toml":   "[servers.s]\ncommand = \"x\"\n",
	})
	if err := os.MkdirAll(filepath.Join(reg, "mcp"), 0o700); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, filepath.Join(reg, "real-reg.toml"), filepath.Join(reg, "mcp", "registry.toml"))
	if _, err := Resolve("a", []Source{src(KindOrg, reg)}, ResolveOptions{}); !errors.Is(err, ErrPath) {
		t.Errorf("registry symlink: %v", err)
	}
	if _, err := LoadRegistry(filepath.Join(reg, "mcp", "registry.toml")); !errors.Is(err, ErrPath) {
		t.Errorf("LoadRegistry through a symlink: %v", err)
	}
	// a symlinked profiles directory
	other := mk(t, map[string]string{"real-profiles/a.toml": "name = \"a\"\n"})
	linked := t.TempDir()
	symlinkOrSkip(t, filepath.Join(other, "real-profiles"), filepath.Join(linked, "profiles"))
	if _, err := src(KindOrg, linked).Open("a"); !errors.Is(err, ErrPath) {
		t.Errorf("profiles directory symlink: %v", err)
	}
}

func TestReadOpenedDetectsSwap(t *testing.T) {
	dir := mk(t, map[string]string{"a": "A", "b": "B"})
	fa, err := os.Lstat(filepath.Join(dir, "a"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readOpened(filepath.Join(dir, "b"), fa, 10); !errors.Is(err, ErrPath) {
		t.Errorf("a file that is not the one that was checked: %v", err)
	}
	if b, err := readOpened(filepath.Join(dir, "a"), fa, 10); err != nil || string(b) != "A" {
		t.Errorf("same file: %q %v", b, err)
	}
	if _, err := readOpened(dir, fa, 10); err == nil {
		t.Error("a directory was read")
	}
	if _, _, err := readConfined(dir, "a/../b", 10); err == nil {
		t.Error("dotdot component accepted")
	}
}

func TestReadFileNoFollow(t *testing.T) {
	dir := mk(t, map[string]string{"a": "A"})
	if b, err := readFileNoFollow(filepath.Join(dir, "a"), 10); err != nil || string(b) != "A" {
		t.Errorf("%q %v", b, err)
	}
	symlinkOrSkip(t, filepath.Join(dir, "a"), filepath.Join(dir, "l"))
	if _, err := readFileNoFollow(filepath.Join(dir, "l"), 10); !errors.Is(err, ErrPath) {
		t.Errorf("symlink: %v", err)
	}
	if _, err := readFileNoFollow(filepath.Join(dir, "none"), 10); err == nil {
		t.Error("missing file")
	}
}

// ---- B8: Kind zero value ----

type zeroKind struct{ Source }

func (zeroKind) Kind() Kind { return KindInvalid }

func TestInvalidKindIsRejected(t *testing.T) {
	root := mk(t, map[string]string{"profiles/a.toml": "name = \"a\"\n"})
	for _, k := range []Kind{KindInvalid, Kind(0), Kind(99), Kind(-3)} {
		s := &rawSource{id: "raw:z", kind: k, root: root, files: map[string]*Manifest{"a": {Name: "a"}}}
		if _, err := Resolve("a", []Source{s}, ResolveOptions{}); !errors.Is(err, ErrInvalidKind) {
			t.Errorf("Resolve kind %d: %v", int(k), err)
		}
		if _, err := List([]Source{s}); !errors.Is(err, ErrInvalidKind) {
			t.Errorf("List kind %d: %v", int(k), err)
		}
	}
	if _, err := Resolve("a", []Source{zeroKind{src(KindOrg, root)}}, ResolveOptions{}); !errors.Is(err, ErrInvalidKind) {
		t.Errorf("embedded zero kind: %v", err)
	}
	// a valid source beside an invalid one still fails
	if _, err := Resolve("a", []Source{src(KindOrg, root), zeroKind{src(KindOrg, root)}}, ResolveOptions{}); !errors.Is(err, ErrInvalidKind) {
		t.Errorf("mixed: %v", err)
	}
	if _, err := Resolve("a", []Source{nil}, ResolveOptions{}); !errors.Is(err, ErrInvalidKind) {
		t.Errorf("nil source: %v", err)
	}
	var zero Kind
	if zero != KindInvalid || zero.Valid() {
		t.Error("the zero Kind must be invalid")
	}
}

// ---- B3: closure risk classification ----

func TestClosureProfileSplit(t *testing.T) {
	const base = "name = \"a\"\n"
	cases := []struct {
		name     string
		body     string
		identity bool // identity digest changes
		controls bool // controls digest changes
	}{
		{"description", "name = \"a\"\ndescription = \"d\"\n", true, false},
		{"owner", "name = \"a\"\nowner = \"o\"\n", true, false},
		{"status", "name = \"a\"\nstatus = \"experimental\"\n", true, false},
		{"superseded_by", "name = \"a\"\nstatus = \"deprecated\"\nsuperseded_by = \"b\"\n", true, false},
		{"when_to_use", "name = \"a\"\nwhen_to_use = [\"w\"]\n", true, false},
		{"avoid_when", "name = \"a\"\navoid_when = [\"w\"]\n", true, false},
		{"model", base + "[session]\nmodel = \"opus\"\n", true, false},
		{"effort", base + "[session]\neffort = \"high\"\n", true, false},
		{"account", "name = \"a\"\naccount = \"work\"\n", false, true},
		{"extends", "name = \"a\"\nextends = [\"b\"]\n", false, true},
		{"plugins.mode", base + "[plugins]\nmode = \"additive\"\n", false, true},
		{"plugins.exclude", base + "[plugins]\nexclude = [\"p@m\"]\n", false, true},
		{"plugins.include", base + "[plugins]\ninclude = [\"p@m\"]\n", false, true},
		{"skills.off", base + "[skills]\noff = [\"s\"]\n", false, true},
		{"skills.name_only", base + "[skills]\nname_only = [\"s\"]\n", false, true},
		{"mcp.servers", base + "[mcp]\nservers = [\"s\"]\n", false, true},
		{"mcp.strict", base + "[mcp]\nstrict = true\n", false, true},
		{"mcp.strict false", base + "[mcp]\nstrict = false\n", false, true},
		{"mcp.claudeai_connectors", base + "[mcp]\nclaudeai_connectors = \"none\"\n", false, true},
		{"inherit_user_settings", base + "[session]\ninherit_user_settings = false\n", false, true},
		{"env", base + "[session.env]\nCCSHELF_VAR_A = \"1\"\n", false, true},
		{"append_system_prompt_file", base + "[session]\nappend_system_prompt_file = \"prompts/p.md\"\n", false, true},
		{"on_blocked", base + "[policy]\non_blocked = \"fail\"\n", false, true},
		{"comment only", base + "# a comment\n", false, false},
		{"whitespace only", "name   =   \"a\"\n\n\n", false, false},
	}
	digests := func(body string) (string, string) {
		m, err := Parse([]byte(body), "")
		if err != nil {
			t.Fatalf("%q: %v", body, err)
		}
		id, ctl, err := profileDigests(m)
		if err != nil {
			t.Fatal(err)
		}
		return id, ctl
	}
	id0, ctl0 := digests(base)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, ctl := digests(tc.body)
			if (id != id0) != tc.identity {
				t.Errorf("identity changed = %v, want %v", id != id0, tc.identity)
			}
			if (ctl != ctl0) != tc.controls {
				t.Errorf("controls changed = %v, want %v", ctl != ctl0, tc.controls)
			}
		})
	}
	// two values of one control differ too
	_, a := digests(base + "[session.env]\nCCSHELF_VAR_A = \"1\"\n")
	_, b := digests(base + "[session.env]\nCCSHELF_VAR_A = \"2\"\n")
	if a == b {
		t.Error("env values must be part of the controls digest")
	}
	// list order is not significant, extends order is
	_, l1 := digests(base + "[plugins]\ninclude = [\"a@m\", \"b@m\"]\n")
	_, l2 := digests(base + "[plugins]\ninclude = [\"b@m\", \"a@m\"]\n")
	if l1 != l2 {
		t.Error("include order changed the digest")
	}
	_, e1 := digests("name = \"a\"\nextends = [\"x\", \"y\"]\n")
	_, e2 := digests("name = \"a\"\nextends = [\"y\", \"x\"]\n")
	if e1 == e2 {
		t.Error("extends order must change the digest")
	}
}

// tomlPaths lists the dotted TOML path of every leaf field of t.
func tomlPaths(t reflect.Type, prefix string) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := strings.Split(f.Tag.Get("toml"), ",")[0]
		path := prefix + name
		if f.Type.Kind() == reflect.Struct {
			out = append(out, tomlPaths(f.Type, path+".")...)
			continue
		}
		out = append(out, path)
	}
	return out
}

func TestEveryManifestFieldIsCovered(t *testing.T) {
	// Every field of Manifest must be named by one of the two canonical forms;
	// this fails when a field is added to Manifest and forgotten in the closure.
	covered := map[string]bool{}
	for _, c := range []any{profileIdentity{}, profileControls{}} {
		ct := reflect.TypeOf(c)
		for i := 0; i < ct.NumField(); i++ {
			covered[strings.Split(ct.Field(i).Tag.Get("json"), ",")[0]] = true
		}
	}
	paths := tomlPaths(reflect.TypeOf(Manifest{}), "")
	if len(paths) < 20 {
		t.Fatalf("only %d manifest fields found: %v", len(paths), paths)
	}
	for _, p := range paths {
		if !covered[p] {
			t.Errorf("manifest field %q is not covered by the closure", p)
		}
	}
}

func TestClosureProfileItems(t *testing.T) {
	r := closureFor(t, baseFiles(), KindOrg, "a")
	var kinds []string
	for _, it := range r.Closure.Items {
		if it.Name == "a" {
			kinds = append(kinds, fmt.Sprintf("%s:%t", it.Kind, it.Risky))
		}
	}
	if strings.Join(kinds, ",") != "profile:false,profile-controls:true" {
		t.Errorf("items for a: %v", kinds)
	}
}

// ---- closure determinism (surviving mutations) ----

func TestCanonicalJSONSortsEnvRefs(t *testing.T) {
	a := MCPServer{Name: "s", Type: MCPStdio, Command: "x", EnvRefs: []string{"B_REF", "A_REF", "C_REF"}}
	b := MCPServer{Name: "s", Type: MCPStdio, Command: "x", EnvRefs: []string{"C_REF", "A_REF", "B_REF"}}
	ja, err := a.canonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	jb, _ := b.canonicalJSON()
	if string(ja) != string(jb) {
		t.Errorf("canonical forms differ:\n%s\n%s", ja, jb)
	}
	if a.EnvRefs[0] != "B_REF" {
		t.Error("canonicalJSON must not reorder the caller's slice")
	}
}

func TestClosureIndependentOfRegistryOrder(t *testing.T) {
	reg := func(refs string) map[string]string {
		return map[string]string{
			"profiles/a.toml":   "name = \"a\"\n[mcp]\nservers = [\"s\", \"t\"]\n",
			"mcp/registry.toml": "[servers.s]\ncommand = \"x\"\nenv_refs = " + refs + "\n[servers.t]\ncommand = \"y\"\n",
		}
	}
	h1 := closureFor(t, reg(`["A_REF", "B_REF"]`), KindOrg, "a").Closure.Hash
	h2 := closureFor(t, reg(`["B_REF", "A_REF"]`), KindOrg, "a").Closure.Hash
	if h1 != h2 {
		t.Error("env_refs order changed the closure hash")
	}
}

// twoCommitSources are two org dir sources with the same portable name but
// different commits, so their closure items differ only in the digest.
type commitDir struct {
	Source
	sha string
}

func (c commitDir) Commit() string { return c.sha }
func (c commitDir) Open(name string) (*File, error) {
	f, err := c.Source.Open(name)
	if f != nil {
		f.Source = c
	}
	return f, err
}

func TestClosureItemOrderTiebreakAndShuffle(t *testing.T) {
	root := mk(t, map[string]string{
		"profiles/a.toml": "name = \"a\"\nextends = [\"b\"]\n",
		"profiles/b.toml": "name = \"b\"\n",
	})
	// "a" comes from one source and "b" from another with the same portable id
	// ("dir:org") but a different commit: the two source items share kind and
	// name and differ only in digest.
	build := func() *Resolved {
		s1 := commitDir{src(KindOrg, root), "1111"}
		fa, err := s1.Open("a")
		if err != nil {
			t.Fatal(err)
		}
		s2 := commitDir{src(KindOrg, mk(t, map[string]string{"profiles/b.toml": "name = \"b\"\n"})), "2222"}
		fb, err := s2.Open("b")
		if err != nil {
			t.Fatal(err)
		}
		return &Resolved{Chain: []*File{fb, fa}, Merged: Manifest{}.WithDefaults()}
	}
	res := build()
	c1, err := buildClosure(res)
	if err != nil {
		t.Fatal(err)
	}
	swapped := build()
	swapped.Chain[0], swapped.Chain[1] = swapped.Chain[1], swapped.Chain[0]
	c2, err := buildClosure(swapped)
	if err != nil {
		t.Fatal(err)
	}
	if c1.Hash != c2.Hash {
		t.Error("closure hash depends on chain order of equally named source items")
	}
	var srcDigests []string
	for _, it := range c1.Items {
		if it.Kind == ItemSource {
			srcDigests = append(srcDigests, it.Digest)
		}
	}
	if len(srcDigests) != 2 || srcDigests[0] >= srcDigests[1] {
		t.Errorf("source items must be ordered by digest when kind and name tie: %v", srcDigests)
	}
}

func TestClosureHashIndependentOfShuffle(t *testing.T) {
	r := closureFor(t, baseFiles(), KindOrg, "a")
	want := r.Closure.Hash
	items := append([]ClosureItem(nil), r.Closure.Items...)
	// HashItems expects canonical order; the sorted order is stable under
	// many random shuffles followed by the canonical sort used by buildClosure.
	for i := 0; i < 20; i++ {
		shuffled := append([]ClosureItem(nil), items...)
		for j := len(shuffled) - 1; j > 0; j-- {
			n, _ := rand.Int(rand.Reader, big.NewInt(int64(j+1)))
			k := int(n.Int64())
			shuffled[j], shuffled[k] = shuffled[k], shuffled[j]
		}
		sortItems(shuffled)
		if got := HashItems(shuffled); got != want {
			t.Fatalf("hash after shuffle = %s, want %s", got, want)
		}
	}
	// a changed risky flag changes the hash
	flipped := append([]ClosureItem(nil), items...)
	flipped[0].Risky = !flipped[0].Risky
	if HashItems(flipped) == want {
		t.Error("risky flag must be hashed")
	}
}

// ---- Resolve rules ----

func TestSR3TransitiveChecks(t *testing.T) {
	org := mk(t, map[string]string{
		"profiles/bad.toml":  "name = \"bad\"\n[session]\ninherit_user_settings = false\n",
		"profiles/good.toml": "name = \"good\"\n",
	})
	pers := mk(t, map[string]string{
		"profiles/mine.toml":    "name = \"mine\"\nextends = [\"bad\"]\n",
		"profiles/drops.toml":   "name = \"drops\"\nextends = [\"good\"]\n[session]\ninherit_user_settings = false\n",
		"profiles/viadrop.toml": "name = \"viadrop\"\nextends = [\"drops\"]\n",
	})
	// a personal profile extending an org profile that drops the user layer
	if _, err := Resolve("mine", []Source{src(KindOrg, org), src(KindPersonal, pers)}, ResolveOptions{}); !errors.Is(err, ErrSharedDropsUserLayer) {
		t.Errorf("personal extends org(false): %v", err)
	}
	// a project profile extending a personal one that drops it
	proj := mk(t, map[string]string{"profiles/p.toml": "name = \"p\"\nextends = [\"drops\"]\n"})
	_, err := Resolve("p", []Source{src(KindProject, proj), src(KindPersonal, pers), src(KindOrg, org)}, ResolveOptions{AllowProject: true})
	if !errors.Is(err, ErrSharedDropsUserLayer) {
		t.Errorf("project extends personal(false): %v", err)
	}
	// an org profile extending a personal one (two levels up) that drops it
	orgKid := mk(t, map[string]string{"profiles/k.toml": "name = \"k\"\nextends = [\"viadrop\"]\n"})
	_, err = Resolve("k", []Source{src(KindOrg, orgKid), src(KindPersonal, pers), src(KindOrg, org)}, ResolveOptions{})
	if !errors.Is(err, ErrSharedDropsUserLayer) {
		t.Errorf("org extends personal(false) transitively: %v", err)
	}
	// the child cannot launder the drop by setting the value back to true: the
	// parent's own setting is what the shared child would pull in
	orgBack := mk(t, map[string]string{"profiles/back.toml": "name = \"back\"\nextends = [\"drops\"]\n[session]\ninherit_user_settings = true\n"})
	_, err = Resolve("back", []Source{src(KindOrg, orgBack), src(KindPersonal, pers), src(KindOrg, org)}, ResolveOptions{})
	if !errors.Is(err, ErrSharedDropsUserLayer) || !strings.Contains(err.Error(), "pulls in") {
		t.Errorf("org child resetting inherit_user_settings: %v", err)
	}
	projBack := mk(t, map[string]string{"profiles/pb.toml": "name = \"pb\"\nextends = [\"drops\"]\n[session]\ninherit_user_settings = true\n"})
	_, err = Resolve("pb", []Source{src(KindProject, projBack), src(KindPersonal, pers), src(KindOrg, org)}, ResolveOptions{AllowProject: true})
	if !errors.Is(err, ErrSharedDropsUserLayer) {
		t.Errorf("project child resetting inherit_user_settings: %v", err)
	}
	// a personal profile on a personal parent that drops it is fine
	r, err := Resolve("viadrop", []Source{src(KindPersonal, pers), src(KindOrg, org)}, ResolveOptions{})
	if err != nil || r.Merged.InheritsUserSettings() {
		t.Errorf("personal chain: %v", err)
	}
}

func TestProjectExtendsPersonalParentWithPrivilegedFields(t *testing.T) {
	cases := map[string]string{
		"mcp":     "[mcp]\nservers = [\"s\"]\n",
		"prompt":  "[session]\nappend_system_prompt_file = \"prompts/x.md\"\n",
		"account": "account = \"work\"\n",
		"env":     "[session.env]\nCCSHELF_VAR_A = \"1\"\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			pers := mk(t, map[string]string{
				"profiles/parent.toml": "name = \"parent\"\n" + body,
				"profiles/mid.toml":    "name = \"mid\"\nextends = [\"parent\"]\n",
				"prompts/x.md":         "x",
				"mcp/registry.toml":    "[servers.s]\ncommand = \"x\"\n",
			})
			for _, parent := range []string{"parent", "mid"} {
				proj := mk(t, map[string]string{"profiles/p.toml": fmt.Sprintf("name = \"p\"\nextends = [%q]\n", parent)})
				_, err := Resolve("p", []Source{src(KindPersonal, pers), src(KindProject, proj)}, ResolveOptions{AllowProject: true})
				if !errors.Is(err, ErrProjectForbidden) || !strings.Contains(err.Error(), "extends") {
					t.Errorf("parent %s: %v", parent, err)
				}
			}
		})
	}
	// a project parent of a project profile is checked by its own fields
	pers := mk(t, map[string]string{"profiles/plain.toml": "name = \"plain\"\n"})
	proj := mk(t, map[string]string{"profiles/p.toml": "name = \"p\"\nextends = [\"plain\"]\n"})
	if _, err := Resolve("p", []Source{src(KindPersonal, pers), src(KindProject, proj)}, ResolveOptions{AllowProject: true}); err != nil {
		t.Errorf("plain personal parent: %v", err)
	}
}

func TestEnvValueLengthLimit(t *testing.T) {
	prof := func(n int) string {
		return "name = \"x\"\n[session.env]\nCCSHELF_VAR_A = \"" + strings.Repeat("v", n) + "\"\n"
	}
	if _, err := Parse([]byte(prof(MaxEnvValueSize)), ""); err != nil {
		t.Errorf("exactly %d bytes rejected: %v", MaxEnvValueSize, err)
	}
	_, err := Parse([]byte(prof(MaxEnvValueSize+1)), "")
	mustErrContain(t, err, "at most 4096 bytes")
	if MaxEnvValueSize != 4096 {
		t.Error("the documented limit is 4096")
	}
}

func TestInheritFalseWarning(t *testing.T) {
	pers := mk(t, map[string]string{
		"profiles/lean.toml": "name = \"lean\"\n[session]\ninherit_user_settings = false\n",
		"profiles/full.toml": "name = \"full\"\n",
	})
	r, err := Resolve("lean", []Source{src(KindPersonal, pers)}, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var found string
	for _, w := range r.Warnings {
		if strings.Contains(w, "inherit_user_settings = false") {
			found = w
		}
	}
	for _, want := range []string{"user settings", "user plugins", "user skills", "user MCP", "hooks", "model", "not loaded"} {
		if !strings.Contains(found, want) {
			t.Errorf("warning %q lacks %q", found, want)
		}
	}
	r, err = Resolve("full", []Source{src(KindPersonal, pers)}, ResolveOptions{})
	if err != nil || len(r.Warnings) != 0 {
		t.Errorf("no warning expected: %v %v", r.Warnings, err)
	}
	// the warning also reaches Describe
	r, _ = Resolve("lean", []Source{src(KindPersonal, pers)}, ResolveOptions{})
	if !strings.Contains(Describe(r), "Warning: profile lean sets inherit_user_settings = false") {
		t.Error("Describe lacks the warning")
	}
}

// ---- B12: case ----

func TestCaseOnlyDifferencesAreErrors(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"include twice", "name = \"x\"\n[plugins]\ninclude = [\"Foo@m\", \"foo@m\"]\n", "differs only by case"},
		{"marketplace case", "name = \"x\"\n[plugins]\ninclude = [\"foo@Market\", \"foo@market\"]\n", "differs only by case"},
		{"exclude twice", "name = \"x\"\n[plugins]\nexclude = [\"Foo@m\", \"foo@m\"]\n", "differs only by case"},
		{"include vs exclude", "name = \"x\"\n[plugins]\ninclude = [\"Foo@m\"]\nexclude = [\"foo@m\"]\n", "in both"},
		{"skills off twice", "name = \"x\"\n[skills]\noff = [\"Doc\", \"doc\"]\n", "differs only by case"},
		{"skills off vs name_only", "name = \"x\"\n[skills]\noff = [\"Doc\"]\nname_only = [\"doc\"]\n", "in both"},
		{"exact duplicate still says duplicate", "name = \"x\"\n[plugins]\ninclude = [\"a@m\", \"a@m\"]\n", "duplicate entry"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.body), "")
			mustErrContain(t, err, tc.want)
		})
	}
}

func TestExcludeRemovesIncludeIgnoringCase(t *testing.T) {
	root := mk(t, map[string]string{
		"profiles/base.toml": "name = \"base\"\n[plugins]\nexclude = [\"Noisy@Acme\"]\n[skills]\noff = [\"Legacy\"]\n",
		"profiles/kid.toml":  "name = \"kid\"\nextends = [\"base\"]\n[plugins]\ninclude = [\"noisy@acme\", \"core@acme\"]\n[skills]\nname_only = [\"legacy\", \"keep\"]\n",
	})
	r, err := Resolve("kid", []Source{src(KindOrg, root)}, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(r.Merged.Plugins.Include, ",") != "core@acme" {
		t.Errorf("include = %v", r.Merged.Plugins.Include)
	}
	if strings.Join(r.Merged.Skills.NameOnly, ",") != "keep" {
		t.Errorf("name_only = %v", r.Merged.Skills.NameOnly)
	}
	w := strings.Join(r.Warnings, "\n")
	for _, want := range []string{"plugin noisy@acme is removed because Noisy@Acme is excluded", "skill legacy is removed because Legacy is excluded", "profile kid includes noisy@acme but base excludes it"} {
		if !strings.Contains(w, want) {
			t.Errorf("warnings lack %q:\n%s", want, w)
		}
	}
}

// ---- B6: text hygiene, URLs, redaction ----

func TestFreeTextRejectsControlsAndBidi(t *testing.T) {
	bad := map[string]string{
		"bell":      "a\u0007b",
		"escape":    "a\u001bb",
		"del":       "a\u007fb",
		"c1 nel":    "a\u0085b",
		"c1 csi":    "a\u009bb",
		"rlo":       "a\u202eb",
		"lre":       "a\u202ab",
		"isolate":   "a\u2066b",
		"pdi":       "a\u2069b",
		"lrm":       "a\u200eb",
		"nul":       "a\x00b",
		"newline":   "a\nb",
		"return":    "a\rb",
		"form feed": "a\fb",
	}
	for label, v := range bad {
		for _, f := range []struct{ field, body string }{
			{"description", "name = \"x\"\ndescription = %s\n"},
			{"owner", "name = \"x\"\nowner = %s\n"},
			{"when_to_use", "name = \"x\"\nwhen_to_use = [%s]\n"},
			{"avoid_when", "name = \"x\"\navoid_when = [%s]\n"},
			{"env value", "name = \"x\"\n[session.env]\nCCSHELF_VAR_A = %s\n"},
		} {
			t.Run(label+"/"+f.field, func(t *testing.T) {
				if _, err := Parse([]byte(fmt.Sprintf(f.body, tomlString(v))), ""); err == nil {
					t.Errorf("accepted %q", v)
				}
			})
		}
	}
	// a tab is fine in free text but not in an env value
	if _, err := Parse([]byte("name = \"x\"\ndescription = \"a\\tb\"\n"), ""); err != nil {
		t.Errorf("tab in description: %v", err)
	}
	if _, err := Parse([]byte("name = \"x\"\n[session.env]\nCCSHELF_VAR_A = \"a\\tb\"\n"), ""); err == nil {
		t.Error("tab in an env value accepted")
	}
	// ordinary non-ASCII text is fine
	if _, err := Parse([]byte("name = \"x\"\ndescription = \"Équipe web, 日本語, emoji 🎨\"\n"), ""); err != nil {
		t.Errorf("unicode text: %v", err)
	}
}

// tomlString renders s as a TOML basic string with every non-printable rune escaped.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r >= 0x7f:
			fmt.Fprintf(&b, "\\u%04X", r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func TestRegistryRejectsControlsAndBidi(t *testing.T) {
	for _, v := range []string{"a\u0007b", "a\u0085b", "a\u202eb", "a\u2067b", "a\nb"} {
		for _, f := range []string{
			"[servers.a]\ncommand = %s\n",
			"[servers.a]\ncommand = \"x\"\nargs = [%s]\n",
			"[servers.a]\ncommand = \"x\"\n[servers.a.windows]\ncommand = %s\n",
			"[servers.a]\ncommand = \"x\"\n[servers.a.linux]\ncommand = \"c\"\nargs = [%s]\n",
			"[servers.a]\ntype = \"http\"\nurl = %s\n",
		} {
			if _, err := ParseRegistry([]byte(fmt.Sprintf(f, tomlString(v))), "r"); err == nil {
				t.Errorf("accepted %q in %q", v, f)
			}
		}
	}
	if _, err := ParseRegistry([]byte("[servers.a]\ncommand = \"x\"\nargs = [\"a\\tb\"]\n"), "r"); err != nil {
		t.Errorf("tab in args: %v", err)
	}
}

func TestRegistryURLRules(t *testing.T) {
	cases := []struct{ name, url, want string }{
		{"query", "https://x.example/mcp?v=1", "must not contain a query string"},
		{"empty query", "https://x.example/mcp?", "must not contain a query string"},
		{"token", "https://x.example/mcp?token=abc", "looks like a credential"},
		{"key", "https://x.example/mcp?key=abc", "looks like a credential"},
		{"secret", "https://x.example/mcp?client_secret=abc", "looks like a credential"},
		{"password", "https://x.example/mcp?password=abc", "looks like a credential"},
		{"auth", "https://x.example/mcp?auth=abc", "looks like a credential"},
		{"sig", "https://x.example/mcp?sig=abc", "looks like a credential"},
		{"api_key", "https://x.example/mcp?API_KEY=abc", "looks like a credential"},
		{"access_token", "https://x.example/mcp?access_token=abc", "looks like a credential"},
		{"uppercase scheme", "HTTPS://x.example/mcp", "lowercase scheme"},
		{"mixed scheme", "Https://x.example/mcp", "lowercase scheme"},
		{"no host", "https:///x", "https://"},
		{"userinfo", "https://u@x.example/", "credentials"},
		{"fragment", "https://x.example/mcp#f", "fragment"},
		{"whitespace", "https://x.example/a%20b c", "whitespace"},
	}
	for _, typ := range []string{"http", "sse"} {
		for _, tc := range cases {
			t.Run(typ+"/"+tc.name, func(t *testing.T) {
				_, err := ParseRegistry([]byte(fmt.Sprintf("[servers.a]\ntype = %q\nurl = %q\n", typ, tc.url)), "r")
				mustErrContain(t, err, tc.want)
			})
		}
	}
	mustErrContain(t, func() error {
		_, err := ParseRegistry([]byte("[servers.a]\ntype = \"http\"\nurl = \"https://x.example/mcp?v=1\"\n"), "r")
		return err
	}(), "env_refs")
	if _, err := ParseRegistry([]byte("[servers.a]\ntype = \"http\"\nurl = \"https://x.example:8443/a/b\"\n"), "r"); err != nil {
		t.Errorf("plain https URL: %v", err)
	}
}

func TestRedactURL(t *testing.T) {
	cases := map[string]string{
		"https://x.example/a/b?token=s&x=1#frag": "https://x.example/a/b",
		"https://u:p@x.example:8443/a":           "https://x.example:8443/a",
		"https://x.example":                      "https://x.example",
		"not a url":                              "<invalid url>",
		"":                                       "<invalid url>",
		"%zz":                                    "<invalid url>",
	}
	for in, want := range cases {
		if got := redactURL(in); got != want {
			t.Errorf("redactURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDescribeNeverPrintsSecrets(t *testing.T) {
	root := mk(t, map[string]string{
		"profiles/a.toml":   "name = \"a\"\n[mcp]\nservers = [\"s\", \"h\"]\n[session]\nappend_system_prompt_file = \"prompts/p.md\"\n[session.env]\nFIGMA_TOKEN_REF = \"op://vault/item/SECRETVALUE\"\nCCSHELF_VAR_X = \"hunter2\"\n",
		"prompts/p.md":      "PROMPTTEXT do the thing\n",
		"mcp/registry.toml": "[servers.s]\ncommand = \"run\"\n[servers.h]\ntype = \"http\"\nurl = \"https://h.example/mcp\"\n",
	})
	r, err := Resolve("a", []Source{src(KindOrg, root)}, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// a URL with credentials in it can only arrive by bypassing the registry
	// checks; Describe must still strip it.
	h := r.MCP["h"]
	h.URL = "https://user:pw@h.example/mcp/path?token=LEAK#frag"
	r.MCP["h"] = h
	out := Describe(r)
	for _, leak := range []string{"SECRETVALUE", "hunter2", "PROMPTTEXT", "LEAK", "user:pw", "frag", "op://"} {
		if strings.Contains(out, leak) {
			t.Errorf("Describe leaks %q:\n%s", leak, out)
		}
	}
	for _, want := range []string{"env CCSHELF_VAR_X = <redacted>", "env FIGMA_TOKEN_REF = <redacted>", "server h: http https://h.example/mcp/path\n", "prompts/p.md (", "bytes, sha256:"} {
		if !strings.Contains(out, want) {
			t.Errorf("Describe lacks %q:\n%s", want, out)
		}
	}
}

// ---- B10: shell-interpreter warnings ----

func TestMCPWarnings(t *testing.T) {
	cases := []struct {
		name string
		s    MCPServer
		want string // substring of the single expected warning; "" for none
	}{
		{"npx", MCPServer{Command: "npx", Args: []string{"-y", "pkg"}}, ""},
		{"sh", MCPServer{Command: "sh", Args: []string{"run.sh"}}, "shell"},
		{"bash path", MCPServer{Command: "/bin/bash"}, "shell"},
		{"zsh", MCPServer{Command: "zsh"}, "shell"},
		{"fish", MCPServer{Command: "fish"}, "shell"},
		{"cmd", MCPServer{Command: "cmd", Args: []string{"/c", "npx", "pkg"}}, "shell"},
		{"cmd.exe windows path", MCPServer{Command: `C:\Windows\System32\CMD.EXE`}, "shell"},
		{"powershell", MCPServer{Command: "powershell"}, "shell"},
		{"pwsh", MCPServer{Command: "pwsh.exe"}, "shell"},
		{"env", MCPServer{Command: "env", Args: []string{"A=1", "x"}}, "shell"},
		{"python -c", MCPServer{Command: "python3", Args: []string{"-c", "import x"}}, "inline code"},
		{"python3.12 -c", MCPServer{Command: "python3.12", Args: []string{"-c", "x"}}, "inline code"},
		{"python script", MCPServer{Command: "python3", Args: []string{"server.py"}}, ""},
		{"node -e", MCPServer{Command: "node", Args: []string{"-e", "x"}}, "inline code"},
		{"node --eval", MCPServer{Command: "node", Args: []string{"--eval", "x"}}, "inline code"},
		{"node script", MCPServer{Command: "node", Args: []string{"server.js"}}, ""},
		{"nodemon is not node", MCPServer{Command: "nodemon", Args: []string{"x"}}, ""},
		{"other with -c", MCPServer{Command: "npx", Args: []string{"-c", "echo hi"}}, "-c flag"},
		{"other with /c", MCPServer{Command: "run", Args: []string{"/C", "x"}}, "/C flag"},
		{"docker -e is an env flag", MCPServer{Command: "docker", Args: []string{"run", "-e", "A=1", "img"}}, ""},
		{"http server", MCPServer{Type: MCPHTTP, URL: "https://x.example"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.s
			s.Name = "srv"
			if s.Type == "" {
				s.Type = MCPStdio
			}
			got := MCPWarnings(s)
			if tc.want == "" {
				if len(got) != 0 {
					t.Errorf("warnings = %v", got)
				}
				return
			}
			if len(got) != 1 || !strings.Contains(got[0], tc.want) || !strings.Contains(got[0], `"srv"`) {
				t.Errorf("warnings = %v, want one containing %q", got, tc.want)
			}
		})
	}
	// overrides are checked and named
	s := MCPServer{Name: "srv", Type: MCPStdio, Command: "npx", Windows: &MCPOverride{Command: "cmd", Args: []string{"/c", "npx"}}, MacOS: &MCPOverride{Command: "sh"}, Linux: &MCPOverride{Command: "npx"}}
	got := strings.Join(MCPWarnings(s), "\n")
	if !strings.Contains(got, "on windows") || !strings.Contains(got, "on macos") || strings.Contains(got, "on linux") {
		t.Errorf("override warnings:\n%s", got)
	}
}

func TestResolveSurfacesMCPWarnings(t *testing.T) {
	root := mk(t, map[string]string{
		"profiles/a.toml":   "name = \"a\"\n[mcp]\nservers = [\"shelly\"]\n",
		"mcp/registry.toml": "[servers.shelly]\ncommand = \"bash\"\nargs = [\"-c\", \"curl x | sh\"]\n[servers.unused]\ncommand = \"sh\"\n",
	})
	r, err := Resolve("a", []Source{src(KindOrg, root)}, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], `"shelly"`) {
		t.Errorf("warnings = %v (only servers a profile uses are reported)", r.Warnings)
	}
	if !strings.Contains(Describe(r), "Warning: MCP server \"shelly\"") {
		t.Error("Describe lacks the warning")
	}
}

func TestCommandBase(t *testing.T) {
	for in, want := range map[string]string{"/usr/bin/BASH": "bash", `C:\x\Cmd.exe`: "cmd", "node": "node", "": "."} {
		if got := commandBase(in); got != want {
			t.Errorf("commandBase(%q) = %q, want %q", in, got, want)
		}
	}
	if isVersionSuffix("") || !isVersionSuffix("3.12") || isVersionSuffix("3x") {
		t.Error("isVersionSuffix")
	}
}
