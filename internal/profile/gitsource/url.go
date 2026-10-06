package gitsource

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode"

	"github.com/ccshelf/ccshelf/internal/config"
)

// ErrBadURL is wrapped by every URL validation failure.
var ErrBadURL = errors.New("unacceptable git URL")

var (
	helperLike = regexp.MustCompile(`^[A-Za-z0-9+.-]+::`)
	fullSHA    = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
)

// validateURL checks raw. The rules are those of config.ValidateGitURL (the
// same ones the configuration applies), so a URL that reaches a Source can
// never carry credentials, a query, a fragment or invisible characters into
// Locator, ID, the lockfile, Describe output, argv or error text. With
// allowLocal (tests only) file:// URLs and local paths are also accepted, but
// every other check stays.
func validateURL(raw string, allowLocal bool) error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrBadURL, fmt.Sprintf(format, a...))
	}
	err := config.ValidateGitURL(raw)
	if err == nil {
		// git must never see a host that reads as an option
		if i := strings.Index(raw, "://"); i >= 0 {
			host := raw[i+3:]
			if at := strings.LastIndexByte(host, '@'); at >= 0 {
				host = host[at+1:]
			}
			if strings.HasPrefix(host, "-") {
				return bad("the host starts with \"-\"")
			}
		}
		return nil
	}
	msg := strings.ReplaceAll(err.Error(), raw, "<url>") // never echo what may hold a credential
	if !allowLocal {
		return bad("%s", msg)
	}
	switch {
	case raw == "" || strings.HasPrefix(raw, "-"):
		return bad("%s", msg)
	case strings.IndexFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) >= 0:
		return bad("it contains whitespace, control or invisible formatting characters")
	case helperLike.MatchString(raw):
		return bad("transport helpers such as ext:: are not allowed")
	case strings.Contains(raw, "://") && !strings.HasPrefix(strings.ToLower(raw), "file://"):
		return bad("%s", msg) // a remote URL stays under the strict rules
	case strings.ContainsAny(raw, "?#"):
		return bad("it contains a query or a fragment")
	}
	return nil
}

// cleanSubpath validates and normalizes the folder inside the repository. It
// returns "" for the repository root.
func cleanSubpath(p string) (string, error) {
	if p == "" || p == "." {
		return "", nil
	}
	if strings.ContainsAny(p, "\\:\x00") || strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("subpath %q must be a relative path with forward slashes", p)
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return "", fmt.Errorf("subpath %q must not contain \"..\"", p)
		}
	}
	c := path.Clean(p)
	if c == "." {
		return "", nil
	}
	return c, nil
}
