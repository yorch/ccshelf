package codeowners

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"github.com/yorch/ccshelf/internal/catalog/safepath"
)

// MaxSize is GitHub's size limit for a CODEOWNERS file (3 MB).
const MaxSize = 3 << 20

// Locations are the places GitHub looks for CODEOWNERS, in order.
func Locations() []string {
	return []string{".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"}
}

// Rule is one parsed CODEOWNERS line.
type Rule struct {
	// Line is the 1-based line number.
	Line int
	// Pattern is the pattern as written (with \# unescaped).
	Pattern string
	// Owners may be empty, which removes ownership for matching paths.
	Owners []string
	re     *regexp.Regexp
}

// Issue is a line GitHub would reject.
type Issue struct {
	Line    int
	Message string
}

// File is a parsed CODEOWNERS file.
type File struct {
	Rules  []Rule
	Issues []Issue
}

var ownerRe = regexp.MustCompile(`^(@[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)?|[^@\s]+@[^@\s]+\.[^@\s]+)$`)

// Parse parses CODEOWNERS content. It fails only when the input is larger
// than MaxSize; syntax problems are reported in File.Issues.
func Parse(data []byte) (*File, error) {
	if len(data) > MaxSize {
		return nil, fmt.Errorf("CODEOWNERS is larger than %d bytes", MaxSize)
	}
	f := &File{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), MaxSize)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		pattern := fields[0]
		var owners []string
		for _, tok := range fields[1:] {
			if strings.HasPrefix(tok, "#") {
				break
			}
			owners = append(owners, tok)
		}
		bad := false
		for _, o := range owners {
			if !ownerRe.MatchString(o) {
				f.Issues = append(f.Issues, Issue{n, fmt.Sprintf("invalid owner %q (use @user, @org/team or an email address)", o)})
				bad = true
			}
		}
		re, msg := compile(pattern)
		if msg != "" {
			f.Issues = append(f.Issues, Issue{n, msg})
			bad = true
		}
		if bad {
			continue
		}
		f.Rules = append(f.Rules, Rule{Line: n, Pattern: unescapeHash(pattern), Owners: owners, re: re})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read CODEOWNERS: %w", err)
	}
	return f, nil
}

// Find loads the CODEOWNERS file of a repository from the first of
// Locations() that exists. It returns a nil File and an empty path when there
// is none.
func Find(root string) (*File, string, error) {
	for _, loc := range Locations() {
		data, err := safepath.ReadFile(root, loc, MaxSize)
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, safepath.ErrNotRegular) {
			continue
		}
		if err != nil {
			return nil, loc, fmt.Errorf("read %s: %w", loc, err)
		}
		f, err := Parse(data)
		if err != nil {
			return nil, loc, fmt.Errorf("%s: %w", loc, err)
		}
		return f, loc, nil
	}
	return nil, "", nil
}

// Match returns the last rule that matches path, or false. path is a
// slash-separated file path relative to the repository root; to ask about a
// directory, pass a file path inside it.
func (f *File) Match(path string) (Rule, bool) {
	path = normalize(path)
	for i := len(f.Rules) - 1; i >= 0; i-- {
		if f.Rules[i].re.MatchString(path) {
			return f.Rules[i], true
		}
	}
	return Rule{}, false
}

// Owners returns the owners of path (nil when no rule matches or the last
// matching rule has no owners).
func (f *File) Owners(path string) []string {
	r, ok := f.Match(path)
	if !ok || len(r.Owners) == 0 {
		return nil
	}
	return append([]string(nil), r.Owners...)
}

// Covers reports whether path has at least one owner.
func (f *File) Covers(path string) bool { return len(f.Owners(path)) > 0 }

func normalize(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	for strings.HasPrefix(p, "./") {
		p = p[2:]
	}
	return strings.TrimRight(strings.TrimLeft(p, "/"), "/")
}

func unescapeHash(p string) string {
	if strings.HasPrefix(p, `\#`) {
		return p[1:]
	}
	return p
}

// compile turns a CODEOWNERS pattern into an anchored regular expression over
// slash-separated paths. A non-empty message means the pattern is invalid.
func compile(pattern string) (*regexp.Regexp, string) {
	p := unescapeHash(pattern)
	if strings.HasPrefix(p, "!") {
		return nil, fmt.Sprintf("pattern %q: negation with ! is not supported by CODEOWNERS", pattern)
	}
	if hasUnescaped(p, "[]") {
		return nil, fmt.Sprintf("pattern %q: character ranges with [ ] are not supported by CODEOWNERS", pattern)
	}
	dirOnly := strings.HasSuffix(p, "/")
	p = strings.TrimRight(p, "/")
	anchored := strings.HasPrefix(p, "/")
	p = strings.TrimLeft(p, "/")
	if p == "" {
		if dirOnly || anchored {
			return regexp.MustCompile(`^.+$`), "" // "/" owns the whole repository
		}
		return nil, fmt.Sprintf("pattern %q is empty", pattern)
	}
	segs := strings.Split(p, "/")
	if len(segs) > 1 {
		anchored = true
	}
	var b strings.Builder
	b.WriteString("^")
	if !anchored {
		b.WriteString("(?:.*/)?")
	}
	lastWild := false
	for i, seg := range segs {
		last := i == len(segs)-1
		if seg == "**" {
			if last {
				b.WriteString(".+")
			} else {
				b.WriteString("(?:[^/]+/)*")
			}
			continue
		}
		wild := false
		for j := 0; j < len(seg); j++ {
			c := seg[j]
			switch c {
			case '*':
				for j+1 < len(seg) && seg[j+1] == '*' {
					j++
				}
				b.WriteString("[^/]*")
				wild = true
			case '?':
				b.WriteString("[^/]")
				wild = true
			case '\\':
				if j+1 < len(seg) {
					j++
					b.WriteString(regexp.QuoteMeta(string(seg[j])))
				}
			default:
				b.WriteString(regexp.QuoteMeta(string(c)))
			}
		}
		if last {
			lastWild = wild
		} else {
			b.WriteString("/")
		}
	}
	switch {
	case dirOnly:
		b.WriteString("/.+$")
	case lastWild || segs[len(segs)-1] == "**":
		b.WriteString("$")
	default:
		b.WriteString("(?:/.+)?$")
	}
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil, fmt.Sprintf("pattern %q cannot be compiled: %v", pattern, err)
	}
	return re, ""
}

func hasUnescaped(p, chars string) bool {
	for i := 0; i < len(p); i++ {
		if p[i] == '\\' {
			i++
			continue
		}
		if strings.IndexByte(chars, p[i]) >= 0 {
			return true
		}
	}
	return false
}
