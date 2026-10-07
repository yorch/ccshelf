package update

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Version is a semantic version (https://semver.org) without build metadata,
// which never takes part in precedence.
type Version struct {
	Major, Minor, Patch uint64
	// Pre holds the dot-separated pre-release identifiers, if any.
	Pre []string
}

var (
	semverRe  = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
	numericRe = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
	// pseudoRe matches the timestamp-and-commit suffix of a Go pseudo-version.
	pseudoRe = regexp.MustCompile(`-(0\.)?[0-9]{14}-[0-9a-f]{12}`)
)

// maxVersionLen bounds what ParseVersion looks at.
const maxVersionLen = 64

// ParseVersion parses "1.2.3", "v1.2.3" and "1.2.3-rc.1" (with optional
// "+build" metadata, which is dropped). Leading zeros, missing components and
// anything else are errors.
func ParseVersion(s string) (Version, error) {
	if len(s) > maxVersionLen {
		return Version{}, errors.New("version is too long")
	}
	s = strings.TrimPrefix(s, "v")
	m := semverRe.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("%q is not a semantic version such as 1.2.3", s)
	}
	var v Version
	nums := []*uint64{&v.Major, &v.Minor, &v.Patch}
	for i, p := range nums {
		n, err := strconv.ParseUint(m[i+1], 10, 32)
		if err != nil {
			return Version{}, fmt.Errorf("version component %q is too large", m[i+1])
		}
		*p = n
	}
	if m[4] != "" {
		v.Pre = strings.Split(m[4], ".")
		for _, id := range v.Pre {
			if numericRe.MatchString(id) {
				continue
			}
			if allDigits(id) {
				return Version{}, fmt.Errorf("pre-release identifier %q has a leading zero", id)
			}
		}
	}
	return v, nil
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// String returns the version without a leading "v".
func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if len(v.Pre) > 0 {
		s += "-" + strings.Join(v.Pre, ".")
	}
	return s
}

// Tag returns the release tag form, "v1.2.3".
func (v Version) Tag() string { return "v" + v.String() }

// IsPrerelease reports whether v has pre-release identifiers.
func (v Version) IsPrerelease() bool { return len(v.Pre) > 0 }

// Compare returns -1, 0 or +1 as v is older than, equal to or newer than o,
// by semantic version precedence: a pre-release is older than its release,
// numeric identifiers compare as numbers and sort before alphanumeric ones.
func (v Version) Compare(o Version) int {
	for _, p := range [][2]uint64{{v.Major, o.Major}, {v.Minor, o.Minor}, {v.Patch, o.Patch}} {
		if c := cmpUint(p[0], p[1]); c != 0 {
			return c
		}
	}
	switch {
	case len(v.Pre) == 0 && len(o.Pre) == 0:
		return 0
	case len(v.Pre) == 0:
		return 1
	case len(o.Pre) == 0:
		return -1
	}
	for i := 0; i < len(v.Pre) && i < len(o.Pre); i++ {
		if c := comparePre(v.Pre[i], o.Pre[i]); c != 0 {
			return c
		}
	}
	return cmpInt(len(v.Pre), len(o.Pre))
}

func comparePre(a, b string) int {
	an, bn := numericRe.MatchString(a), numericRe.MatchString(b)
	switch {
	case an && bn:
		x, _ := strconv.ParseUint(a, 10, 64)
		y, _ := strconv.ParseUint(b, 10, 64)
		if x == y {
			// Same value but different length would need leading zeros,
			// which the parser rejects; keep the comparison total anyway.
			return cmpInt(len(a), len(b))
		}
		return cmpUint(x, y)
	case an:
		return -1
	case bn:
		return 1
	}
	return strings.Compare(a, b)
}

func cmpUint(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// IsDevVersion reports whether s identifies a build that did not come from a
// release: "dev" or empty, a Go pseudo-version, a "+dirty" or "(devel)" build,
// or anything that is not a semantic version. Such a build is never updated
// without --force, because there is no meaningful "newer".
func IsDevVersion(s string) bool {
	if s == "" || s == "dev" || s == "(devel)" || s == "unknown" {
		return true
	}
	if strings.Contains(s, "+dirty") || strings.Contains(s, "+incompatible") || pseudoRe.MatchString(s) {
		return true
	}
	_, err := ParseVersion(s)
	return err != nil
}

// AutoEligible reports whether latest may be installed without the user
// asking by name: newer than current, not a pre-release, and within the same
// major version (for 0.x, the same minor version, because a 0.x minor bump may
// break compatibility). A new major (or new 0.x minor) is only ever offered.
func AutoEligible(current, latest Version) bool {
	if latest.IsPrerelease() || latest.Compare(current) <= 0 {
		return false
	}
	if latest.Major != current.Major {
		return false
	}
	return current.Major > 0 || latest.Minor == current.Minor
}
