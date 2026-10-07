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
	// SignerRepo is the "owner/name" of the repository whose release workflow
	// must have signed checksums.txt (on github.com, whatever host the bytes
	// come from). It is the repository this binary was built from, never
	// derived from the base URL: a mirror only moves bytes. An organization
	// that signs its own fork's releases sets it explicitly with [WithSigner].
	SignerRepo string
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

	// builtinAssetHosts is how many of AssetHosts came from NewSource.
	builtinAssetHosts int
}

// NewSource builds the Source for repo, the repository this binary was
// released from (the compiled-in version.Repo). baseURL is "" for github.com,
// or https://host (a GitHub Enterprise Server; optionally
// https://host/owner/repo to name a different repository to download from).
// The signer identity stays repo's, whatever baseURL says.
func NewSource(repo, baseURL string, allowLoopbackHTTP bool) (Source, error) {
	s := Source{AllowLoopbackHTTP: allowLoopbackHTTP, SignerRepo: repo}
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
		s.builtinAssetHosts = len(s.AssetHosts)
	} else {
		s.API = web + "/api/v3"
	}
	return s, nil
}

// WithSigner returns s with the signer repository replaced by signerRepo
// ("owner/name"; the host is always github.com). It is a trust decision the
// user makes in [update] cosign_identity_repo. An empty signerRepo keeps s.
func (s Source) WithSigner(signerRepo string) (Source, error) {
	if signerRepo == "" {
		return s, nil
	}
	if err := config.ValidateCosignIdentityRepo(signerRepo); err != nil {
		return Source{}, fmt.Errorf("update cosign_identity_repo: %w", err)
	}
	s.SignerRepo = signerRepo
	return s, nil
}

// ExtraAssetHosts returns the hosts added with [Source.WithAssetHosts] (not
// GitHub's own), for display.
func (s Source) ExtraAssetHosts() []string {
	if len(s.AssetHosts) <= s.builtinAssetHosts {
		return nil
	}
	return append([]string(nil), s.AssetHosts[s.builtinAssetHosts:]...)
}

// WithAssetHosts returns s with hosts added to the hosts a download may be
// redirected to (exact hostnames, already validated by the configuration; they
// are checked again here). They are additions: the release host and, for
// github.com, GitHub's asset hosts stay.
func (s Source) WithAssetHosts(hosts []string) (Source, error) {
	if len(hosts) == 0 {
		return s, nil
	}
	if len(hosts) > config.MaxUpdateAssetHosts {
		return Source{}, fmt.Errorf("update asset_hosts: at most %d hosts", config.MaxUpdateAssetHosts)
	}
	all := append([]string(nil), s.AssetHosts...)
	for _, h := range hosts {
		if err := config.ValidateAssetHost(h); err != nil {
			return Source{}, fmt.Errorf("update asset_hosts: %w", err)
		}
		all = append(all, h)
	}
	s.AssetHosts = all
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
	if !repoRe.MatchString(s.SignerRepo) || strings.Contains(s.SignerRepo, "..") {
		return fmt.Errorf("signer repository %q is not owner/name", s.SignerRepo)
	}
	return nil
}

// Host returns the web host (with the port, if any) releases are downloaded
// from.
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
// release's checksums.txt must carry: the release workflow of the signer
// repository on github.com at the release tag. It does not depend on the
// download host or on the repository downloaded from, so a base URL can move
// the bytes of a release but never change who must have signed it.
func (s Source) CosignIdentity(tag string) string {
	return "https://" + DefaultHost + "/" + s.SignerRepo + "/.github/workflows/release.yml@refs/tags/" + tag
}
