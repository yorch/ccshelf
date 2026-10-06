package trust

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yorch/ccshelf/internal/profile"
)

// shared is a test source that behaves like a git source: it has a locator,
// a ref and a commit.
type shared struct {
	profile.Source
	locator, ref, commit string
}

func (s *shared) Locator() string { return s.locator }
func (s *shared) Ref() string     { return s.ref }
func (s *shared) Commit() string  { return s.commit }
func (s *shared) ID() string      { return s.locator + "@" + s.commit }
func (s *shared) Open(name string) (*profile.File, error) {
	f, err := s.Source.Open(name)
	if err != nil {
		return nil, err
	}
	f.Source = s
	return f, nil
}

func put(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

const (
	baseTOML = "name = \"base\"\ndescription = \"d\"\n"
	devTOML  = "name = \"dev\"\ndescription = \"d\"\nextends = [\"base\"]\n[plugins]\ninclude = [\"audit-kit@acme\"]\n[mcp]\nservers = [\"pd\"]\n[session]\nappend_system_prompt_file = \"prompts/dev.md\"\n"
	regTOML  = "[servers.pd]\ntype = \"stdio\"\ncommand = \"npx\"\nargs = [\"-y\", \"pd-mcp\", \"--token\", \"hunter2\", \"API_KEY=zzz\"]\nenv_refs = [\"PD_TOKEN_REF\"]\n"
)

// orgTree writes an org source tree and returns its root.
func orgTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	put(t, root, "profiles/base.toml", baseTOML)
	put(t, root, "profiles/dev.toml", devTOML)
	put(t, root, "mcp/registry.toml", regTOML)
	put(t, root, "prompts/dev.md", "Be careful.\nLine two.\n")
	return root
}

func gitLike(root, ref, commit string) *shared {
	return &shared{Source: profile.DirSource(profile.KindOrg, filepath.Join(root, "profiles")), locator: "git:https://example.com/org/data.git", ref: ref, commit: commit}
}

func resolve(t *testing.T, name string, src ...profile.Source) *profile.Resolved {
	t.Helper()
	r, err := profile.Resolve(name, src, profile.ResolveOptions{AllowProject: true})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "cfg", "lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// accept records r the way a caller does after review: with its own hash.
func accept(s *Store, r *profile.Resolved) error { return s.Accept(r, r.Closure.Hash) }
