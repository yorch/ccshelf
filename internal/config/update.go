package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Update.Mode values. The default, when [update] is absent, is UpdateOff:
// nothing contacts the network unless the user runs "ccshelf update".
const (
	// UpdateOff never checks for updates by itself.
	UpdateOff = "off"
	// UpdateNotify checks at most once per interval and prints one line when a
	// newer release exists.
	UpdateNotify = "notify"
	// UpdateInstall downloads, verifies and installs a newer release of the
	// same major version (same minor for 0.x).
	UpdateInstall = "install"
)

// Update interval bounds.
const (
	// DefaultUpdateInterval is used when update.interval is empty.
	DefaultUpdateInterval = 24 * time.Hour
	// MinUpdateInterval is the shortest accepted update.interval.
	MinUpdateInterval = time.Hour
	// MaxUpdateInterval is the longest accepted update.interval (one year).
	MaxUpdateInterval = 365 * 24 * time.Hour
)

// UpdateModes returns the accepted values of update.mode.
func UpdateModes() []string { return []string{UpdateOff, UpdateNotify, UpdateInstall} }

// Update holds the self-update settings. The zero value (no [update] section)
// means mode "off", interval 24h and the default release source.
type Update struct {
	// Mode is "off" (default), "notify" or "install".
	Mode string `toml:"mode,omitempty"`
	// Interval is a Go duration string (for example "24h"); at least 1h.
	Interval string `toml:"interval,omitempty"`
	// BaseURL points the updater at a GitHub Enterprise Server: https://host
	// or https://host/owner/repo. Empty means github.com and the repository
	// this binary was released from.
	BaseURL string `toml:"base_url,omitempty"`
	// CosignIdentityRepo is the github.com "owner/repo" whose release workflow
	// must have signed a release's checksums.txt. Empty means the repository
	// this binary was built from. Set it only for a fork whose releases you
	// build and sign yourself: it is a trust decision, and base_url never
	// changes it.
	CosignIdentityRepo string `toml:"cosign_identity_repo,omitempty"`
	// AssetHosts are extra hostnames a release download may be redirected to,
	// for a GitHub Enterprise Server that serves release assets from its own
	// storage or subdomain-isolation host. Exact lower-case DNS hostnames
	// only: no scheme, port, path, IP address or wildcard. Every redirect
	// still has to be https. Naming a host trusts it with the bytes (the
	// SHA-256 and, with cosign, the signature are still checked).
	AssetHosts []string `toml:"asset_hosts,omitempty"`
}

// Present reports whether the user wrote anything in the [update] section.
func (u Update) Present() bool {
	return u.Mode != "" || u.Interval != "" || u.BaseURL != "" || u.CosignIdentityRepo != "" || len(u.AssetHosts) > 0
}

// EffectiveMode returns the mode with the default applied.
func (u Update) EffectiveMode() string {
	if u.Mode == "" {
		return UpdateOff
	}
	return u.Mode
}

// updateIntervalRe is the accepted spelling of update.interval; it is the same
// pattern as schema/config.schema.json, so the schema and the Go parser agree
// (Go's own ParseDuration also takes a sign, a bare ".5h" and "µs").
var updateIntervalRe = regexp.MustCompile(`^([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+$`)

// ParseUpdateInterval parses an update.interval value.
func ParseUpdateInterval(s string) (time.Duration, error) {
	if !updateIntervalRe.MatchString(s) {
		return 0, fmt.Errorf("%q is not a duration such as 24h (digits and ns, us, ms, s, m, h units only)", s)
	}
	return time.ParseDuration(s)
}

// EffectiveInterval returns the interval with the default applied. An invalid
// value (Validate reports it) falls back to the default.
func (u Update) EffectiveInterval() time.Duration {
	if u.Interval == "" {
		return DefaultUpdateInterval
	}
	d, err := ParseUpdateInterval(u.Interval)
	if err != nil || d < MinUpdateInterval || d > MaxUpdateInterval {
		return DefaultUpdateInterval
	}
	return d
}

// validate adds every problem with the [update] section to add.
func (u Update) validate(add func(string, ...any)) {
	if u.Mode != "" && !contains(UpdateModes(), u.Mode) {
		add("update.mode: %q is not one of %v", u.Mode, UpdateModes())
	}
	if u.Interval != "" {
		d, err := ParseUpdateInterval(u.Interval)
		switch {
		case err != nil:
			add("update.interval: %q is not a duration such as 24h", u.Interval)
		case d < MinUpdateInterval:
			add("update.interval: %q is shorter than the minimum %s", u.Interval, MinUpdateInterval)
		case d > MaxUpdateInterval:
			add("update.interval: %q is longer than the maximum %s", u.Interval, MaxUpdateInterval)
		}
	}
	if u.BaseURL != "" {
		if err := ValidateUpdateBaseURL(u.BaseURL, LoopbackHTTPAllowed); err != nil {
			add("update.base_url: %v", err)
		}
	}
	if u.CosignIdentityRepo != "" {
		if err := ValidateCosignIdentityRepo(u.CosignIdentityRepo); err != nil {
			add("update.cosign_identity_repo: %v", err)
		}
	}
	if len(u.AssetHosts) > MaxUpdateAssetHosts {
		add("update.asset_hosts: at most %d hosts", MaxUpdateAssetHosts)
	}
	seen := map[string]bool{}
	for _, h := range u.AssetHosts {
		if err := ValidateAssetHost(h); err != nil {
			add("update.asset_hosts: %v", err)
		} else if seen[h] {
			add("update.asset_hosts: %q is listed twice", h)
		}
		seen[h] = true
	}
}

// MaxUpdateAssetHosts is the most hosts update.asset_hosts may list.
const MaxUpdateAssetHosts = 8

var assetHostRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidateAssetHost reports why raw is not an acceptable update.asset_hosts
// entry, or nil: a lower-case DNS hostname with at least two labels, a
// non-numeric last label, and nothing else (no scheme, port, path, user
// information, wildcard or IP address).
func ValidateAssetHost(raw string) error {
	if len(raw) > 253 || !assetHostRe.MatchString(raw) {
		return fmt.Errorf("%q must be a plain lower-case hostname such as assets.ghe.example.com (no scheme, port, path, wildcard or IP address)", raw)
	}
	return nil
}

// cosignRepoRe is an "owner/repo" on github.com.
var cosignRepoRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidateCosignIdentityRepo reports why raw is not an acceptable
// update.cosign_identity_repo, or nil: exactly owner/repo (the signer is always
// on github.com, so a URL, a host, a scheme, a path, a ref or a wildcard is
// refused).
func ValidateCosignIdentityRepo(raw string) error {
	if !cosignRepoRe.MatchString(raw) || strings.Contains(raw, "..") || strings.HasSuffix(raw, ".git") {
		return fmt.Errorf("%q must be exactly owner/repo on github.com (no URL, host, ref or wildcard)", raw)
	}
	return nil
}

// updateRepoPathRe is the optional /owner/repo path of an update base URL.
var updateRepoPathRe = regexp.MustCompile(`^/[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*/?$`)

// ValidateUpdateBaseURL reports why raw is not an acceptable update base URL,
// or nil. The accepted forms are https://host[:port] and
// https://host[:port]/owner/repo: no user information of any kind, no query
// or fragment, no whitespace or control characters, no token-looking text. A
// plain http URL is accepted only when allowLoopbackHTTP is true and the host
// is a loopback address; production builds pass false (see
// [LoopbackHTTPAllowed]), so it exists for tests only.
func ValidateUpdateBaseURL(raw string, allowLoopbackHTTP bool) error {
	switch {
	case raw == "":
		return errors.New("must not be empty")
	case strings.IndexFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) >= 0:
		return errors.New("must not contain whitespace, control or invisible formatting characters")
	case strings.Contains(raw, "#"):
		return errors.New("must not contain a query or a fragment")
	}
	if k := credentialMarker(raw); k != "" {
		return fmt.Errorf("looks like it embeds a credential (%s...); never put tokens in the configuration", k)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return fmt.Errorf("%q is not a valid URL", raw)
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && allowLoopbackHTTP && IsLoopbackHost(u.Hostname()):
	case u.Scheme == "http":
		return errors.New("must use https")
	default:
		return errors.New("must be https://host or https://host/owner/repo")
	}
	switch {
	case u.User != nil:
		return errors.New("must not contain user information (no user name, password or token)")
	case u.RawQuery != "" || u.ForceQuery:
		return errors.New("must not contain a query or a fragment")
	case u.Path != "" && u.Path != "/" && !updateRepoPathRe.MatchString(u.Path):
		return errors.New("a path, when given, must be /owner/repo")
	case strings.Contains(u.Path, ".."):
		return errors.New("must not contain \"..\"")
	}
	return nil
}

// IsLoopbackHost reports whether host is localhost or a loopback IP address.
func IsLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
