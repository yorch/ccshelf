package recommend

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ccshelf/ccshelf/internal/catalog"
	"github.com/ccshelf/ccshelf/internal/marketplace"
)

const marketJSON = `{
 "name": "acme",
 "owner": {"name": "Acme"},
 "plugins": [
  {"name": "tf-helper", "source": "./plugins/tf-helper", "description": "terraform",
   "relevance": {"topic": "terraform", "signals": {"cwd": ["**/infra"], "cli": ["terraform plan"], "filesRead": ["*.tf"]}}},
  {"name": "react-kit", "source": "./plugins/react-kit", "description": "react",
   "relevance": {"signals": {"manifestDeps": [{"file": "package.json", "pattern": "\"react\""}], "filesRead": ["src/**/*.tsx"]}}},
  {"name": "go-kit", "source": "./plugins/go-kit", "description": "go",
   "relevance": {"signals": {"manifestDeps": [{"file": "go.mod", "pattern": "^module "}], "cli": ["go"]}}},
  {"name": "old-tf", "source": "./plugins/old-tf", "description": "old terraform",
   "relevance": {"signals": {"filesRead": ["*.tf"]}}},
  {"name": "orphan-old", "source": "./plugins/orphan-old", "description": "old",
   "relevance": {"signals": {"filesRead": ["*.tf"]}}},
  {"name": "no-signals", "source": "./plugins/no-signals", "description": "none"},
  {"name": "bad-regex", "source": "./plugins/bad-regex", "description": "bad",
   "relevance": {"signals": {"manifestDeps": [{"file": "package.json", "pattern": "("}]}}},
  {"name": "host-only", "source": "./plugins/host-only", "description": "hosts",
   "relevance": {"signals": {"hosts": ["Git.Acme.Example"]}}}
 ]
}`

func testCatalog() *catalog.Catalog {
	e := func(name, status, super string) catalog.Entry {
		return catalog.Entry{Name: name, Marketplace: "acme", Status: status, SupersededBy: super}
	}
	return &catalog.Catalog{Plugins: []catalog.Entry{
		e("tf-helper", "active", ""), e("react-kit", "experimental", ""), e("go-kit", "active", ""),
		e("old-tf", "deprecated", "tf-helper"), e("orphan-old", "deprecated", ""),
		e("no-signals", "active", ""), e("bad-regex", "active", ""), e("host-only", "active", ""),
	}}
}

func opts(t *testing.T) []Option {
	m, err := marketplace.Parse([]byte(marketJSON))
	if err != nil {
		t.Fatal(err)
	}
	return []Option{WithRelevance(RelevanceOf(m, nil))}
}

func names(rs []Recommendation) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Kind+":"+r.Name)
	}
	return out
}

func TestForCatalogTerraformRepo(t *testing.T) {
	root := terraformRepo(t)
	sig, err := Collect(context.Background(), filepath.Join(root, "infra"))
	if err != nil {
		t.Fatal(err)
	}
	profiles := []ProfileInfo{
		{Name: "sre", WhenToUse: []string{"Terraform and infrastructure work"}},
		{Name: "frontend", WhenToUse: []string{"React and TypeScript apps"}},
	}
	recs := ForCatalog(testCatalog(), sig, profiles, opts(t)...)
	// tf-helper: cwd (2) + cli (1) + filesRead (3); the reasons also mention
	// the deprecated old-tf that points to it.
	want := []string{"plugin:tf-helper@acme", "profile:sre"}
	if got := names(recs); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v\n%+v", got, want, recs)
	}
	tf := recs[0]
	if tf.Score != weightCwd+weightFilesRead+weightCLI {
		t.Errorf("tf-helper score = %v", tf.Score)
	}
	if len(tf.Why) != 4 {
		t.Errorf("Why = %v", tf.Why)
	}
	// old-tf is deprecated and its replacement is tf-helper: never listed itself.
	for _, r := range recs {
		if r.Name == "old-tf@acme" || r.Name == "orphan-old@acme" {
			t.Errorf("deprecated entry recommended: %+v", r)
		}
	}
	if tf.Replaces != "" {
		t.Errorf("tf-helper matched on its own, Replaces = %q", tf.Replaces)
	}
}

func TestForCatalogDeprecatedReplacementOnly(t *testing.T) {
	sig := &Signals{Cwd: filepath.Join(t.TempDir(), "x"), Files: []string{"main.tf"}}
	recs := ForCatalog(testCatalog(), sig, nil, opts(t)...)
	// tf-helper has filesRead *.tf (matches itself), old-tf matches and points
	// to it; the result has one tf-helper entry with the better score.
	var found []Recommendation
	for _, r := range recs {
		if r.Name == "tf-helper@acme" {
			found = append(found, r)
		}
	}
	if len(found) != 1 {
		t.Fatalf("recs = %+v", recs)
	}
	if found[0].Score != weightFilesRead {
		t.Errorf("score = %v", found[0].Score)
	}
	for _, r := range recs {
		if r.Name == "orphan-old@acme" || r.Name == "old-tf@acme" {
			t.Errorf("deprecated recommended: %+v", r)
		}
	}
}

func TestForCatalogReplacementCarriesReason(t *testing.T) {
	c := &catalog.Catalog{Plugins: []catalog.Entry{
		{Name: "old-tf", Marketplace: "acme", Status: "deprecated", SupersededBy: "newer"},
		{Name: "newer", Marketplace: "acme", Status: "active"},
	}}
	sig := &Signals{Cwd: "/x", Files: []string{"a.tf"}}
	recs := ForCatalog(c, sig, nil, opts(t)...)
	if len(recs) != 1 || recs[0].Name != "newer@acme" || recs[0].Replaces != "old-tf@acme" {
		t.Fatalf("recs = %+v", recs)
	}
	if !strings.Contains(recs[0].Why[0], "replaces deprecated old-tf@acme") {
		t.Errorf("Why = %v", recs[0].Why)
	}
}

func TestForCatalogReactAndGo(t *testing.T) {
	sig, err := Collect(context.Background(), reactRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	recs := ForCatalog(testCatalog(), sig, []ProfileInfo{
		{Name: "frontend", WhenToUse: []string{"React and TypeScript apps", "Writing UI components"}, AvoidWhen: []string{"Terraform"}},
		{Name: "backend", WhenToUse: []string{"Go services"}},
	}, opts(t)...)
	got := names(recs)
	want := []string{"plugin:react-kit@acme", "profile:frontend"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v\n%+v", got, want, recs)
	}
	if recs[0].Score != weightManifestDeps+weightFilesRead || recs[0].Status != "experimental" {
		t.Errorf("react-kit = %+v", recs[0])
	}

	sig, err = Collect(context.Background(), goRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	recs = ForCatalog(testCatalog(), sig, nil, opts(t)...)
	if got := names(recs); !reflect.DeepEqual(got, []string{"plugin:go-kit@acme"}) {
		t.Fatalf("got %v", got)
	}
	if recs[0].Score != weightManifestDeps+weightCLI {
		t.Errorf("go-kit score = %v", recs[0].Score)
	}
}

func TestForCatalogHostsAndDegenerate(t *testing.T) {
	sig := &Signals{Cwd: "/x", Hosts: []string{"git.acme.example"}}
	recs := ForCatalog(testCatalog(), sig, nil, opts(t)...)
	if got := names(recs); !reflect.DeepEqual(got, []string{"plugin:host-only@acme"}) {
		t.Errorf("hosts: %v", got)
	}
	if ForCatalog(testCatalog(), nil, nil) != nil {
		t.Error("nil signals")
	}
	if recs := ForCatalog(nil, sig, nil); len(recs) != 0 {
		t.Errorf("nil catalog: %v", recs)
	}
	// Without relevance data no plugin can match.
	if recs := ForCatalog(testCatalog(), &Signals{Cwd: "/x", Files: []string{"a.tf"}}, nil); len(recs) != 0 {
		t.Errorf("no relevance: %v", recs)
	}
}

func TestForCatalogRelevanceByPlainName(t *testing.T) {
	c := &catalog.Catalog{Plugins: []catalog.Entry{{Name: "p", Marketplace: "m"}}}
	rel := map[string]*marketplace.Relevance{"p": {Signals: marketplace.Signals{FilesRead: []string{"*.go"}}}}
	recs := ForCatalog(c, &Signals{Cwd: "/x", Files: []string{"a/b.go"}}, nil, WithRelevance(rel))
	if len(recs) != 1 || recs[0].Name != "p@m" {
		t.Errorf("recs = %+v", recs)
	}
}

func TestOrderingIsStable(t *testing.T) {
	c := &catalog.Catalog{Plugins: []catalog.Entry{{Name: "b", Marketplace: "m"}, {Name: "a", Marketplace: "m"}}}
	sig := &Signals{Cwd: "/x", Files: []string{"x.go"}}
	rel := map[string]*marketplace.Relevance{
		"a@m": {Signals: marketplace.Signals{FilesRead: []string{"*.go"}}},
		"b@m": {Signals: marketplace.Signals{FilesRead: []string{"*.go"}}},
	}
	for i := 0; i < 5; i++ {
		recs := ForCatalog(c, sig, []ProfileInfo{{Name: "z", WhenToUse: []string{"golang services"}}}, WithRelevance(rel))
		if got := names(recs); !reflect.DeepEqual(got, []string{"plugin:a@m", "plugin:b@m", "profile:z"}) &&
			!reflect.DeepEqual(got, []string{"profile:z", "plugin:a@m", "plugin:b@m"}) {
			t.Fatalf("order = %v", got)
		}
		// profile z scores 1 (golang), plugins 3: plugins first.
		if got := names(recs); got[0] != "plugin:a@m" || got[2] != "profile:z" {
			t.Fatalf("order = %v", got)
		}
	}
}

func TestMatchCwd(t *testing.T) {
	root := t.TempDir()
	sig := &Signals{Cwd: filepath.Join(root, "Repo", "Infra", "Prod"), RepoRoot: filepath.Join(root, "Repo")}
	tests := []struct {
		pattern string
		want    bool
	}{
		{"infra/prod", true},
		{"INFRA", true},      // a directory above, inside the repo
		{"**/infra/*", true}, // any depth
		{"prod", true},       // as if prefixed with **/
		{"repo", false},      // the repo root itself is not a candidate
		{"other/**", false},
		{"**/Repo/**", true},
		{"/" + strings.TrimPrefix(filepath.ToSlash(sig.Cwd), "/"), true},
		{"/no/such", false},
	}
	for _, tc := range tests {
		got := len(matchCwd([]string{tc.pattern}, sig)) == 1
		if got != tc.want {
			t.Errorf("matchCwd(%q) = %v, want %v", tc.pattern, got, tc.want)
		}
	}
	if matchCwd([]string{"x"}, &Signals{}) != nil {
		t.Error("empty cwd")
	}
}

func TestMatchCLI(t *testing.T) {
	sig := &Signals{CLIs: []string{"terraform", "make"}}
	got := matchCLI([]string{"terraform plan", "Terraform", "makefile", "make", "", "terraform apply"}, sig)
	if !reflect.DeepEqual(got, []string{"terraform", "make"}) {
		t.Errorf("got %v", got)
	}
}

func TestMatchManifestDeps(t *testing.T) {
	sig := &Signals{ManifestFiles: map[string][]byte{
		"web/package.json": []byte(`{"dependencies":{"react":"1"}}`),
		"go.mod":           []byte("module x\n"),
	}}
	tests := []struct {
		name string
		dep  marketplace.ManifestDep
		want bool
	}{
		{"base name", marketplace.ManifestDep{File: "package.json", Pattern: `"react"`}, true},
		{"full path", marketplace.ManifestDep{File: `web/package\.json`, Pattern: `react`}, true},
		{"alternation", marketplace.ManifestDep{File: `go\.mod|package\.json`, Pattern: `^module`}, true},
		{"anchored file", marketplace.ManifestDep{File: "json", Pattern: "react"}, false},
		{"pattern absent", marketplace.ManifestDep{File: "package.json", Pattern: "vue"}, false},
		{"invalid pattern", marketplace.ManifestDep{File: "package.json", Pattern: "("}, false},
		{"invalid file", marketplace.ManifestDep{File: "(", Pattern: "x"}, false},
		{"empty pattern", marketplace.ManifestDep{File: "package.json"}, false},
		{"too long", marketplace.ManifestDep{File: "package.json", Pattern: strings.Repeat("a", maxPatternLen+1)}, false},
		{"lookahead unsupported", marketplace.ManifestDep{File: "package.json", Pattern: "(?=react)"}, false},
	}
	for _, tc := range tests {
		got := len(matchManifestDeps([]marketplace.ManifestDep{tc.dep}, sig)) > 0
		if got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
	if matchManifestDeps([]marketplace.ManifestDep{{File: "a", Pattern: "b"}}, &Signals{}) != nil {
		t.Error("no manifests")
	}
}

func TestProfileKeywordScoring(t *testing.T) {
	sig := &Signals{
		Cwd:           "/work/payments-service",
		Files:         []string{"infra/main.tf", "Dockerfile", "app/main.go"},
		CLIs:          []string{"docker", "go", "terraform"},
		ManifestFiles: map[string][]byte{"requirements.txt": []byte("django==4\npytest\n")},
	}
	recs := profileRecs(sig, []ProfileInfo{
		{Name: "sre", WhenToUse: []string{"Infrastructure as code with Terraform", "Container builds"}},
		{Name: "py", WhenToUse: []string{"Django web apps"}},
		{Name: "rust", WhenToUse: []string{"Rust crates"}},
		{Name: "weak", WhenToUse: []string{"terraform"}, AvoidWhen: []string{"docker containers", "golang"}},
		{Name: "empty"},
		{Name: "old", Status: "deprecated", SupersededBy: "sre", WhenToUse: []string{"terraform legacy"}},
		{Name: "orphan", Status: "deprecated", WhenToUse: []string{"terraform"}},
		{Name: "loop", Status: "deprecated", SupersededBy: "loop", WhenToUse: []string{"terraform"}},
	})
	byName := map[string]Recommendation{}
	for _, r := range recs {
		byName[r.Name] = r
	}
	if r := byName["sre"]; r.Score < 3 {
		t.Errorf("sre = %+v", r)
	}
	if byName["py"].Score != 1 || !strings.Contains(strings.Join(byName["py"].Why, "|"), "dependency django") {
		t.Errorf("py = %+v", byName["py"])
	}
	for _, n := range []string{"rust", "weak", "empty", "old", "orphan", "loop"} {
		if _, ok := byName[n]; ok {
			t.Errorf("%s should not be recommended: %+v", n, byName[n])
		}
	}
	if byName["sre"].Replaces != "" && byName["sre"].Replaces != "old" {
		t.Errorf("sre.Replaces = %q", byName["sre"].Replaces)
	}
}

func TestProfileReplacementWins(t *testing.T) {
	sig := &Signals{Cwd: "/w", CLIs: []string{"terraform"}, Files: []string{"a.tf"}}
	recs := profileRecs(sig, []ProfileInfo{
		{Name: "new", WhenToUse: []string{"nothing relevant here"}},
		{Name: "old", Status: "deprecated", SupersededBy: "new", WhenToUse: []string{"terraform"}},
	})
	if len(recs) != 1 || recs[0].Name != "new" || recs[0].Replaces != "old" {
		t.Fatalf("recs = %+v", recs)
	}
}

func TestVocabularyAndTokens(t *testing.T) {
	if got := tokens("Use the Go and K8s for CI/CD"); !reflect.DeepEqual(got, []string{"k8s"}) {
		t.Errorf("tokens = %v", got)
	}
	if stem("containers") != "container" || stem("class") != "class" || stem("go") != "go" {
		t.Error("stem")
	}
	v := vocabulary(&Signals{Cwd: "/a/b/c/d/e", Files: []string{"x/App.TSX", "Makefile", "noext"}, Hosts: []string{"git.acme.example"}})
	for _, w := range []string{"react", "typescript", "make", "acme"} {
		if _, ok := v[w]; !ok {
			t.Errorf("vocabulary lacks %q: %v", w, v)
		}
	}
	if _, ok := v["a"]; ok {
		t.Error("only the last three segments count")
	}
}

func TestProfilesFromCatalog(t *testing.T) {
	got := ProfilesFromCatalog([]catalog.ProfileInfo{{Name: "a", Status: "active", WhenToUse: []string{"x"}}})
	if len(got) != 1 || got[0].Name != "a" || got[0].WhenToUse[0] != "x" {
		t.Errorf("got %+v", got)
	}
}

func TestRelevanceOf(t *testing.T) {
	m, err := marketplace.Parse([]byte(marketJSON))
	if err != nil {
		t.Fatal(err)
	}
	r := RelevanceOf(m, nil)
	if r["tf-helper@acme"] == nil || r["no-signals@acme"] != nil {
		t.Errorf("RelevanceOf = %v", r)
	}
}
