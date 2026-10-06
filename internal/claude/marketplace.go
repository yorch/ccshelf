package claude

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Marketplace is one configured marketplace as reported by
// `claude plugin marketplace list --json`.
//
// The shape was checked by running that command read-only against a scratch
// configuration: an array of objects with "name", "source", a source specific
// key ("repo" for source "github", "url" for "git", "path" for "directory")
// and "installLocation" {V}. Other source values (for example a plain URL or
// a single file) are {U}: they are parsed defensively through [Marketplace.Origin].
type Marketplace struct {
	// Name is the local alias of the marketplace (the part after "@" in plugin
	// ids).
	Name string
	// Kind is the "source" value: github, git, directory, ...
	Kind string
	// Repo, URL and Path are the source specific keys, as reported.
	Repo, URL, Path string
	// InstallLocation is where Claude Code keeps the marketplace.
	InstallLocation string
	// Extra keeps every other key.
	Extra map[string]json.RawMessage
}

// Origin returns the real source the marketplace was added from: the URL for a
// git or URL source, "owner/repo" for a github source, the path for a local
// one. It returns an error for a source it does not understand or one without
// the key that source kind needs, so that an unknown shape fails closed.
func (m Marketplace) Origin() (string, error) {
	var v, key string
	switch strings.ToLower(m.Kind) {
	case "github":
		v, key = m.Repo, "repo"
	case "git", "url":
		v, key = m.URL, "url"
	case "directory", "file":
		v, key = m.Path, "path"
	default:
		return "", fmt.Errorf("marketplace %q has the unknown source kind %q", m.Name, m.Kind)
	}
	if strings.TrimSpace(v) == "" {
		return "", fmt.Errorf("marketplace %q (source %s) reports no %s", m.Name, m.Kind, key)
	}
	return strings.TrimSpace(v), nil
}

// UnmarshalJSON decodes one marketplace object. Known keys with the wrong type
// are errors; unknown keys land in Extra.
func (m *Marketplace) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		return fmt.Errorf("marketplace entry: expected an object")
	}
	*m = Marketplace{}
	for _, f := range []struct {
		k string
		d *string
	}{
		{"name", &m.Name},
		{"source", &m.Kind},
		{"repo", &m.Repo},
		{"url", &m.URL},
		{"path", &m.Path},
		{"installLocation", &m.InstallLocation},
	} {
		v, ok := raw[f.k]
		if !ok {
			continue
		}
		delete(raw, f.k)
		if err := json.Unmarshal(v, f.d); err != nil {
			return fmt.Errorf("marketplace key %q: %w", f.k, err)
		}
	}
	if m.Name == "" {
		return fmt.Errorf("marketplace entry has no name")
	}
	if len(raw) > 0 {
		m.Extra = raw
	}
	return nil
}

// parseMarketplaces accepts only the JSON array the command prints. null, an
// object, a string or empty output is an error naming the shape: a list that
// fails open would let any marketplace pass as the expected one.
func parseMarketplaces(data []byte) ([]Marketplace, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, fmt.Errorf("empty marketplace list output")
	}
	if data[0] != '[' {
		return nil, fmt.Errorf("marketplace list: expected a JSON array, got %s", shapeOf(data))
	}
	var list []Marketplace
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parse marketplace list: %w", err)
	}
	return list, nil
}

// ListMarketplaces returns the configured marketplaces as seen from dir, sorted
// by name. It runs the read-only `claude plugin marketplace list --json`. env is
// the child environment (nil inherits); when ctx has no deadline a 30 second
// timeout applies. Any other output shape is an error.
func ListMarketplaces(ctx context.Context, bin, dir string, env []string) ([]Marketplace, error) {
	ctx, cancel := withDefaultTimeout(ctx, DefaultTimeout)
	defer cancel()
	var stdout, stderr limitedBuffer
	code, err := spawnDir(ctx, dir, bin, []string{"plugin", "marketplace", "list", "--json"}, env, nil, &stdout, &stderr)
	if err != nil {
		return nil, fmt.Errorf("claude plugin marketplace list: %w", err)
	}
	if code != 0 {
		return nil, fmt.Errorf("claude plugin marketplace list exited %d: %s", code, excerpt(stderr.String(), 300))
	}
	if stdout.over {
		return nil, fmt.Errorf("claude plugin marketplace list produced more than %d bytes", maxListOutput)
	}
	list, err := parseMarketplaces(stdout.Bytes())
	if err != nil {
		return nil, err
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}

// MarketplaceOrigin returns the real source of the named marketplace from a
// listing. An unknown name is an error.
func MarketplaceOrigin(list []Marketplace, name string) (string, error) {
	var found []Marketplace
	for _, m := range list {
		if m.Name == name {
			found = append(found, m)
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("marketplace %q is not configured", name)
	case 1:
		return found[0].Origin()
	}
	return "", fmt.Errorf("marketplace %q is listed %d times", name, len(found))
}

// scpAddress is the scp-like git address [user@]host:path.
var scpAddress = regexp.MustCompile(`^(?:[^@/\s]+@)?([^:/\s]+):(.+)$`)

// ownerRepo is the owner/repo shorthand of a GitHub marketplace.
var ownerRepo = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)

// canonicalGitURL reduces a git or URL address to host[:port]/path so that the
// ssh and https forms of the same repository compare equal on any host (GitHub
// Enterprise included): the scheme (https and ssh only), the user name, a
// default port, a trailing slash and a ".git" suffix are dropped, scp-like
// addresses are read as ssh, and only the host is case-folded. Other schemes
// stay in the result, so they never equal an https or ssh address.
func canonicalGitURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	var scheme, host, p string
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.Hostname() == "" {
			return "", fmt.Errorf("%q is not a usable repository address", raw)
		}
		scheme, host, p = strings.ToLower(u.Scheme), strings.ToLower(u.Hostname()), u.Path
		if port := u.Port(); port != "" && !(scheme == "https" && port == "443") && !(scheme == "ssh" && port == "22") {
			host += ":" + port
		}
	} else if m := scpAddress.FindStringSubmatch(s); m != nil {
		scheme, host, p = "ssh", strings.ToLower(m[1]), m[2]
	} else {
		return "", fmt.Errorf("%q is not a usable repository address", raw)
	}
	p = strings.Trim(strings.TrimSuffix(strings.TrimRight(p, "/"), ".git"), "/")
	if p == "" {
		return "", fmt.Errorf("%q has no repository path", raw)
	}
	key := host + "/" + p
	if scheme != "https" && scheme != "ssh" {
		key = scheme + "://" + key
	}
	return key, nil
}

// Identity returns a canonical, kind-tagged identity of where the marketplace
// comes from, for comparing it with an expected source and for binding trust to
// it: "github:owner/repo" (case-folded), "git:host/path" and "url:host/path"
// (see canonicalGitURL), and for a local "directory" or "file" source a hash of
// the path, so no machine path leaks into an identifier. For the repository
// kinds a "ref" and a sub-"path" are part of the identity ("@ref", "#path"),
// so two marketplaces of one repository at different refs or folders differ.
// An unknown kind, a missing key or a malformed ref is an error.
func (m Marketplace) Identity() (string, error) {
	origin, err := m.Origin()
	if err != nil {
		return "", err
	}
	kind := strings.ToLower(m.Kind)
	switch kind {
	case "directory", "file":
		sum := sha256.Sum256([]byte(origin))
		return kind + ":sha256-" + hex.EncodeToString(sum[:8]), nil
	case "github":
		origin = strings.ToLower(origin)
	default:
		if origin, err = canonicalGitURL(origin); err != nil {
			return "", fmt.Errorf("marketplace %q: %w", m.Name, err)
		}
	}
	id := kind + ":" + origin
	if raw, ok := m.Extra["ref"]; ok {
		var ref string
		if err := json.Unmarshal(raw, &ref); err != nil {
			return "", fmt.Errorf("marketplace %q: key \"ref\": %w", m.Name, err)
		}
		if ref = strings.TrimSpace(ref); ref != "" {
			id += "@" + ref
		}
	}
	if p := strings.Trim(strings.TrimSpace(m.Path), "/"); p != "" {
		id += "#" + p
	}
	return id, nil
}

// ExpectedIdentity returns the [Marketplace.Identity] an organization's
// expected marketplace source stands for: the owner/repo shorthand is a
// "github" source, a git URL (https, ssh or scp-like) a "git" source, with no
// ref and no sub-path. A marketplace added with a ref or sub-path, or as a
// "url" or local source, therefore never equals it.
func ExpectedIdentity(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if ownerRepo.MatchString(raw) {
		return "github:" + strings.ToLower(raw), nil
	}
	c, err := canonicalGitURL(raw)
	if err != nil {
		return "", err
	}
	return "git:" + c, nil
}

// MarketplaceIdentity is [MarketplaceOrigin] for the [Marketplace.Identity] of
// the named marketplace.
func MarketplaceIdentity(list []Marketplace, name string) (string, error) {
	var found []Marketplace
	for _, m := range list {
		if m.Name == name {
			found = append(found, m)
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("marketplace %q is not configured", name)
	case 1:
		return found[0].Identity()
	}
	return "", fmt.Errorf("marketplace %q is listed %d times", name, len(found))
}
