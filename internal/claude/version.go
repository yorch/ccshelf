package claude

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var versionPattern = regexp.MustCompile(`\d+(?:\.\d+)+\S*`)

// ParseVersion extracts the version from `claude --version` output such as
// "2.1.291 (Claude Code)".
func ParseVersion(out string) (string, error) {
	v := versionPattern.FindString(out)
	if v == "" {
		return "", fmt.Errorf("cannot parse claude version from %q", excerpt(out, 80))
	}
	return v, nil
}

// Version runs `claude --version` and returns the bare version number.
func Version(ctx context.Context, bin string) (string, error) {
	var stdout, stderr bytes.Buffer
	ctx, cancel := withDefaultTimeout(ctx, 10*time.Second)
	defer cancel()
	code, err := spawnDir(ctx, "", bin, []string{"--version"}, nil, nil, &stdout, &stderr)
	if err != nil {
		return "", fmt.Errorf("run claude --version: %w", err)
	}
	if code != 0 {
		return "", fmt.Errorf("claude --version exited %d: %s", code, excerpt(stderr.String(), 300))
	}
	return ParseVersion(stdout.String())
}

func versionParts(v string) []int {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "v"))
	var parts []int
	for _, p := range strings.Split(v, ".") {
		end := 0
		for end < len(p) && p[end] >= '0' && p[end] <= '9' {
			end++
		}
		if end == 0 {
			break
		}
		n, err := strconv.Atoi(p[:end])
		if err != nil {
			break
		}
		parts = append(parts, n)
		if end < len(p) { // suffix such as -beta ends the numeric part
			break
		}
	}
	return parts
}

// CompareVersions compares two dotted numeric versions and returns -1, 0 or
// 1. Missing components count as zero and non-numeric suffixes (for example
// "-beta") are ignored.
func CompareVersions(a, b string) int {
	pa, pb := versionParts(a), versionParts(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}

// AtLeast reports whether have is at least want. An unparsable have is
// never at least anything.
func AtLeast(have, want string) bool {
	if len(versionParts(have)) == 0 {
		return false
	}
	return CompareVersions(have, want) >= 0
}
