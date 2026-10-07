package update

import (
	"net/http"
	"strings"
	"testing"
)

func TestNewSourceGitHub(t *testing.T) {
	s, err := NewSource("yorch/ccshelf", "", false)
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]string{
		"latest":   s.LatestURL(),
		"list":     s.ListURL(),
		"tag":      s.TagURL("v0.2.0"),
		"asset":    s.AssetURL("v0.2.0", "ccshelf_0.2.0_linux_amd64.tar.gz"),
		"page":     s.ReleasePage("v0.2.0"),
		"identity": s.CosignIdentity("v0.2.0"),
	} {
		want := map[string]string{
			"latest":   "https://api.github.com/repos/yorch/ccshelf/releases/latest",
			"list":     "https://api.github.com/repos/yorch/ccshelf/releases?per_page=30",
			"tag":      "https://api.github.com/repos/yorch/ccshelf/releases/tags/v0.2.0",
			"asset":    "https://github.com/yorch/ccshelf/releases/download/v0.2.0/ccshelf_0.2.0_linux_amd64.tar.gz",
			"page":     "https://github.com/yorch/ccshelf/releases/tag/v0.2.0",
			"identity": "https://github.com/yorch/ccshelf/.github/workflows/release.yml@refs/tags/v0.2.0",
		}[name]
		if got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if len(s.AssetHosts) == 0 {
		t.Error("github.com must allow its release-asset hosts")
	}
	if s.Host() != "github.com" {
		t.Errorf("Host = %q", s.Host())
	}
}

func TestNewSourceEnterprise(t *testing.T) {
	s, err := NewSource("yorch/ccshelf", "https://ghe.example.com:8443/tools/ccshelf/", false)
	if err != nil {
		t.Fatal(err)
	}
	if s.Repo != "tools/ccshelf" {
		t.Errorf("Repo = %q: the base URL path names the repository", s.Repo)
	}
	if got, want := s.LatestURL(), "https://ghe.example.com:8443/api/v3/repos/tools/ccshelf/releases/latest"; got != want {
		t.Errorf("LatestURL = %q, want %q", got, want)
	}
	if got, want := s.AssetURL("v1.0.0", "x"), "https://ghe.example.com:8443/tools/ccshelf/releases/download/v1.0.0/x"; got != want {
		t.Errorf("AssetURL = %q, want %q", got, want)
	}
	// The mirror moves bytes; the signer is the compiled-in repository on
	// github.com, never the mirror host or the repository named in base_url.
	if got, want := s.CosignIdentity("v1.0.0"), "https://github.com/yorch/ccshelf/.github/workflows/release.yml@refs/tags/v1.0.0"; got != want {
		t.Errorf("identity = %q, want %q", got, want)
	}
	if len(s.AssetHosts) != 0 {
		t.Errorf("an enterprise server serves its own assets, got %v", s.AssetHosts)
	}
	s2, err := NewSource("yorch/ccshelf", "https://ghe.example.com", false)
	if err != nil || s2.Repo != "yorch/ccshelf" {
		t.Errorf("a bare host keeps the default repository: %+v %v", s2, err)
	}
}

func TestNewSourceRejects(t *testing.T) {
	for name, tc := range map[string]struct {
		repo, base string
		loop       bool
		want       string
	}{
		"http":          {"a/b", "http://ghe.example.com", false, "https"},
		"loopback prod": {"a/b", "http://127.0.0.1:1", false, "https"},
		"userinfo":      {"a/b", "https://u:p@ghe.example.com", false, "user information"},
		"bad repo":      {"nonsense", "", false, "owner/name"},
		"dotdot repo":   {"a/..", "", false, "owner/name"},
		"path":          {"a/b", "https://ghe.example.com/x/y/z", false, "/owner/repo"},
	} {
		if _, err := NewSource(tc.repo, tc.base, tc.loop); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want %q", name, err, tc.want)
		}
	}
	if _, err := NewSource("a/b", "http://127.0.0.1:1", true); err != nil {
		t.Errorf("loopback with the test allowance: %v", err)
	}
	if err := (Source{}).Valid(); err == nil {
		t.Error("the zero Source must not be valid")
	}
}

func TestCosignIdentityIsPinnedWhateverTheBaseURLSays(t *testing.T) {
	want := "https://github.com/yorch/ccshelf/.github/workflows/release.yml@refs/tags/v1.2.3"
	for _, base := range []string{
		"", "https://github.com/evil/ccshelf", "https://github.com/yorch/ccshelf",
		"https://ghe.example.com", "https://ghe.example.com/evil/ccshelf",
	} {
		s, err := NewSource("yorch/ccshelf", base, false)
		if err != nil {
			t.Fatalf("%q: %v", base, err)
		}
		if got := s.CosignIdentity("v1.2.3"); got != want {
			t.Errorf("base %q: identity = %q, want %q", base, got, want)
		}
	}
}

func TestWithSigner(t *testing.T) {
	s, _ := NewSource("yorch/ccshelf", "https://ghe.example.com/mirror/ccshelf", false)
	if s2, err := s.WithSigner(""); err != nil || s2.CosignIdentity("v1") != s.CosignIdentity("v1") {
		t.Errorf("empty keeps the default: %v", err)
	}
	s2, err := s.WithSigner("acme/ccshelf-fork")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := s2.CosignIdentity("v1.0.0"), "https://github.com/acme/ccshelf-fork/.github/workflows/release.yml@refs/tags/v1.0.0"; got != want {
		t.Errorf("identity = %q, want %q", got, want)
	}
	if s2.Repo != "mirror/ccshelf" {
		t.Errorf("the download repository must not change: %q", s2.Repo)
	}
	for _, bad := range []string{"https://github.com/a/b", "github.com/a/b", "a", "a/b/c", "a/b@main", "../x/y", "a/..", "a/b*", "a/b.git", "a b/c"} {
		if _, err := s.WithSigner(bad); err == nil {
			t.Errorf("WithSigner(%q) accepted", bad)
		}
	}
	if err := (Source{Repo: "a/b", Web: s.Web, API: s.API, SignerRepo: "bad"}).Valid(); err == nil {
		t.Error("Valid must reject a bad signer repository")
	}
}

func TestWithAssetHosts(t *testing.T) {
	gh, _ := NewSource("yorch/ccshelf", "", false)
	s, err := gh.WithAssetHosts([]string{"assets.ghe.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.AssetHosts) != len(gh.AssetHosts)+1 || s.AssetHosts[len(s.AssetHosts)-1] != "assets.ghe.example.com" {
		t.Errorf("hosts = %v: the listed host must be added to GitHub's", s.AssetHosts)
	}
	if got := s.ExtraAssetHosts(); len(got) != 1 || got[0] != "assets.ghe.example.com" {
		t.Errorf("ExtraAssetHosts = %v", got)
	}
	if got := gh.ExtraAssetHosts(); len(got) != 0 {
		t.Errorf("GitHub's own hosts are not extra: %v", got)
	}
	if len(gh.AssetHosts) == len(s.AssetHosts) {
		t.Error("the receiver must not be modified")
	}
	for _, bad := range []string{"*.example.com", "https://a.example.com", "a.example.com:8443", "a.example.com/x", "10.0.0.1", "localhost", "A.Example.com", "a..example.com", "-a.example.com", ""} {
		if _, err := gh.WithAssetHosts([]string{bad}); err == nil {
			t.Errorf("asset host %q accepted", bad)
		}
	}
}

func TestRedirectToListedAssetHost(t *testing.T) {
	ghe, _ := NewSource("yorch/ccshelf", "https://ghe.example.com", false)
	mk := func(raw string) *http.Request {
		r, _ := http.NewRequest("GET", raw, nil)
		return r
	}
	via := []*http.Request{mk("https://ghe.example.com/a")}
	if err := RedirectPolicy(ghe)(mk("https://assets.ghe.example.com/x"), via); err == nil {
		t.Fatal("an unlisted asset host must be refused")
	}
	with, _ := ghe.WithAssetHosts([]string{"assets.ghe.example.com"})
	if err := RedirectPolicy(with)(mk("https://assets.ghe.example.com/x"), via); err != nil {
		t.Errorf("listed host refused: %v", err)
	}
	if err := RedirectPolicy(with)(mk("https://assets.ghe.example.com.evil.example/x"), via); err == nil {
		t.Error("a lookalike of a listed host must be refused (exact match)")
	}
	if err := RedirectPolicy(with)(mk("https://evil.assets.ghe.example.com/x"), via); err == nil {
		t.Error("a subdomain of a listed host must be refused (no wildcards)")
	}
	if err := RedirectPolicy(with)(mk("http://assets.ghe.example.com/x"), via); err == nil {
		t.Error("a listed host over http must be refused")
	}
}
