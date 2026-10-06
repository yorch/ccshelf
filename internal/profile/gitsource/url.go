package gitsource

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode"
)

// ErrBadURL is wrapped by every URL validation failure.
var ErrBadURL = errors.New("unacceptable git URL")

var (
	scpLike    = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9][A-Za-z0-9.-]*:[A-Za-z0-9._~%+/][^\s]*$`)
	helperLike = regexp.MustCompile(`^[A-Za-z0-9+.-]+::`)
	fullSHA    = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
)

// validateURL checks raw against the allowed transports.
func validateURL(raw string, allowLocal bool) error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrBadURL, fmt.Sprintf(format, a...))
	}
	if raw == "" {
		return bad("the URL is empty")
	}
	if strings.HasPrefix(raw, "-") {
		return bad("it starts with \"-\"")
	}
	for _, r := range raw {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return bad("it contains whitespace or control characters")
		}
	}
	if helperLike.MatchString(raw) {
		return bad("transport helpers such as ext:: are not allowed")
	}
	if i := strings.Index(raw, "://"); i >= 0 {
		u, err := url.Parse(raw)
		if err != nil {
			return bad("it does not parse")
		}
		switch strings.ToLower(u.Scheme) {
		case "https", "ssh":
			if u.Host == "" || strings.HasPrefix(u.Host, "-") {
				return bad("the host is missing or starts with \"-\"")
			}
			if u.User != nil {
				if _, hasPw := u.User.Password(); hasPw {
					return bad("it embeds a password; use a credential helper or an ssh key")
				}
			}
			return nil
		case "file":
			if allowLocal {
				return nil
			}
			return bad("the file transport is not allowed")
		default:
			return bad("scheme %q is not allowed (use https or ssh)", u.Scheme)
		}
	}
	if scpLike.MatchString(raw) {
		return nil
	}
	if allowLocal {
		return nil
	}
	return bad("use an https:// or ssh:// URL, or user@host:path")
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
