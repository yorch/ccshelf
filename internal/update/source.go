package update

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/yorch/ccshelf/internal/config"
)

// DefaultHost is the web host releases come from when no base URL is set.
const DefaultHost = "github.com"

// OIDCIssuer is the certificate issuer of keyless signatures made by GitHub
// Actions.
const OIDCIssuer = "https://token.actions.githubusercontent.com"

// githubAssetHosts are the hosts github.com redirects release downloads to.
var githubAssetHosts = []string{
	"objects.githubusercontent.com",
	"release-assets.githubusercontent.com",
	"github-releases.githubusercontent.com",
}

var repoRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Source says where releases are discovered and downloaded. It never carries
// a credential.
type Source struct {
	// Repo is the "owner/name" of the repository that publishes releases.
	Repo string
	// Web is the scheme and host of the web site, without a path (for
	// example https://github.com or https://ghe.example.com).
	Web url.URL
	// API is the REST API base with no trailing slash (https://api.github.com
	// or https://ghe.example.com/api/v3).
	API string
	// AssetHosts are extra hosts (besides the web host) that a download may be
	// redirected to. NewSource fills in GitHub's release-asset hosts for
	// github.com; a GitHub Enterprise Server serves assets itself.
	AssetHosts []string
	// AllowLoopbackHTTP lets a plain-http loopback base URL through. It is for
	// tests; production wiring passes [config.LoopbackHTTPAllowed], which is
	// false except in the end-to-end test build.
	AllowLoopbackHTTP bool
}

// NewSource builds the Source for repo. baseURL is "" for github.com, or
// https://host (a GitHub Enterprise Server; optionally https://host/owner/repo
// to name a different repository on it).
func NewSource(repo, baseURL string, allowLoopbackHTTP bool) (Source, error) {
	s := Source{AllowLoopbackHTTP: allowLoopbackHTTP}
	web := "https://" + DefaultHost
	if baseURL != "" {
		if err := config.ValidateUpdateBaseURL(baseURL, allowLoopbackHTTP); err != nil {
			return Source{}, fmt.Errorf("update base URL: %w", err)
		}
		u, err := url.Parse(baseURL)
		if err != nil {
			return Source{}, fmt.Errorf("update base URL: %w", err)
		}
		if p := strings.Trim(u.Path, "/"); p != "" {
			repo = p
		}
		web = u.Scheme + "://" + u.Host
	}
	if !repoRe.MatchString(repo) || strings.Contains(repo, "..") {
		return Source{}, fmt.Errorf("repository %q is not owner/name", repo)
	}
	wu, err := url.Parse(web)
	if err != nil {
		return Source{}, fmt.Errorf("update base URL: %w", err)
	}
	s.Repo, s.Web = repo, *wu
	if strings.EqualFold(wu.Hostname(), DefaultHost) && wu.Port() == "" {
		s.API = "https://api." + DefaultHost
		s.AssetHosts = append([]string(nil), githubAssetHosts...)
	} else {
		s.API = web + "/api/v3"
	}
	return s, nil
}

// Valid reports why s cannot be used, or nil.
func (s Source) Valid() error {
	if s.Web.Host == "" || s.API == "" {
		return errors.New("update source is not configured")
	}
	if !repoRe.MatchString(s.Repo) {
		return fmt.Errorf("repository %q is not owner/name", s.Repo)
	}
	return nil
}

// Host returns the web host (with the port, if any); it is the host of the
// certificate identity a release signature must carry.
func (s Source) Host() string { return s.Web.Host }

func (s Source) repoAPI(suffix string) string { return s.API + "/repos/" + s.Repo + suffix }

// LatestURL is the API address of the latest stable release.
func (s Source) LatestURL() string { return s.repoAPI("/releases/latest") }

// ListURL is the API address of the recent releases, pre-releases included.
func (s Source) ListURL() string { return s.repoAPI("/releases?per_page=30") }

// TagURL is the API address of the release for tag.
func (s Source) TagURL(tag string) string { return s.repoAPI("/releases/tags/" + url.PathEscape(tag)) }

// AssetURL is the download address of an asset of the release for tag.
func (s Source) AssetURL(tag, name string) string {
	return s.Web.Scheme + "://" + s.Web.Host + "/" + s.Repo + "/releases/download/" + url.PathEscape(tag) + "/" + url.PathEscape(name)
}

// ReleasePage is the web address of the release notes for tag.
func (s Source) ReleasePage(tag string) string {
	return s.Web.Scheme + "://" + s.Web.Host + "/" + s.Repo + "/releases/tag/" + url.PathEscape(tag)
}

// CosignIdentity is the certificate identity the keyless signature of a
// release's checksums.txt must carry: the release workflow of the repository
// at the release tag.
func (s Source) CosignIdentity(tag string) string {
	return "https://" + s.Web.Host + "/" + s.Repo + "/.github/workflows/release.yml@refs/tags/" + tag
}
