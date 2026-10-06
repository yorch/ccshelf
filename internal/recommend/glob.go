package recommend

import "strings"

// maxGlobLen caps pattern length; longer patterns never match.
const maxGlobLen = 512

// Match reports whether the slash-separated path matches the glob pattern.
// See the package documentation for the exact semantics: matching is case
// insensitive, "*" and "?" stay inside one path segment, a "**" segment
// matches zero or more whole segments, and nothing else is special.
func Match(pattern, path string) bool {
	if pattern == "" || len(pattern) > maxGlobLen {
		return false
	}
	p := splitSegments(strings.ToLower(pattern))
	s := splitSegments(strings.ToLower(path))
	return matchSegments(p, s)
}

// splitSegments splits on "/" and drops empty and "." segments, so leading,
// trailing and doubled slashes do not matter.
func splitSegments(s string) []string {
	parts := strings.Split(strings.ReplaceAll(s, "\\", "/"), "/")
	out := parts[:0]
	for _, p := range parts {
		if p == "" || p == "." {
			continue
		}
		out = append(out, p)
	}
	return out
}

// matchSegments is a dynamic program over (pattern index, path index), so a
// pattern full of "**" cannot cause exponential backtracking.
func matchSegments(p, s []string) bool {
	// ok[i][j]: p[i:] matches s[j:].
	ok := make([][]bool, len(p)+1)
	for i := range ok {
		ok[i] = make([]bool, len(s)+1)
	}
	ok[len(p)][len(s)] = true
	for i := len(p) - 1; i >= 0; i-- {
		for j := len(s); j >= 0; j-- {
			if p[i] == "**" {
				// zero segments, or consume one and stay on the "**".
				ok[i][j] = ok[i+1][j] || (j < len(s) && ok[i][j+1])
				continue
			}
			ok[i][j] = j < len(s) && matchSegment(p[i], s[j]) && ok[i+1][j+1]
		}
	}
	return ok[0][0]
}

// matchSegment matches one segment with "*" (any run of characters) and "?"
// (one character). Both are already lower-cased.
func matchSegment(pat, s string) bool {
	pr, sr := []rune(pat), []rune(s)
	px, sx := 0, 0
	star, mark := -1, 0
	for sx < len(sr) {
		switch {
		case px < len(pr) && pr[px] == '*':
			star, mark = px, sx
			px++
		case px < len(pr) && (pr[px] == '?' || pr[px] == sr[sx]):
			px++
			sx++
		case star >= 0:
			px = star + 1
			mark++
			sx = mark
		default:
			return false
		}
	}
	for px < len(pr) && pr[px] == '*' {
		px++
	}
	return px == len(pr)
}

// matchAnyDepth matches the pattern as written when it contains a "/" and at
// any depth (as if prefixed with "**/") when it is a single segment.
func matchAnyDepth(pattern, path string) bool {
	if !strings.Contains(strings.Trim(pattern, "/"), "/") && !strings.HasPrefix(pattern, "/") {
		return Match("**/"+pattern, path)
	}
	return Match(pattern, path)
}
