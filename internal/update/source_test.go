package update

import (
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
	if got, want := s.CosignIdentity("v1.0.0"), "https://ghe.example.com:8443/tools/ccshelf/.github/workflows/release.yml@refs/tags/v1.0.0"; got != want {
		t.Errorf("identity = %q", got)
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
