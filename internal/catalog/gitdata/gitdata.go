package gitdata

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// CallTimeout bounds each git invocation.
const CallTimeout = 30 * time.Second

// Info is the history summary of one directory.
type Info struct {
	// LastCommit is the committer date of the newest commit touching the
	// directory, as YYYY-MM-DD in UTC. Empty when it has no commits.
	LastCommit string `json:"last_commit,omitempty"`
	// Authors is the number of distinct author email addresses.
	Authors int `json:"authors,omitempty"`
}

// ErrNoGit is returned when the git binary cannot be found.
var ErrNoGit = errors.New("git was not found in PATH")

var tagRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/+-]{0,199}$`)

// ValidTag reports whether s is acceptable as a tag name argument.
func ValidTag(s string) bool {
	return tagRe.MatchString(s) && !strings.Contains(s, "..") && !strings.HasSuffix(s, "/") && !strings.HasSuffix(s, ".lock")
}

// nullDevice names an empty git configuration file. git for Windows maps
// "/dev/null" to the NUL device itself; passing "NUL" made git for Windows on
// arm64 fail with "unable to access 'NUL'".
func nullDevice() string { return "/dev/null" }

// env builds the environment for git: the parent environment without any
// GIT_* variable, plus a neutral configuration.
func env() []string {
	var out []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(strings.ToUpper(kv), "GIT_") {
			continue
		}
		out = append(out, kv)
	}
	return append(out,
		"GIT_CONFIG_GLOBAL="+nullDevice(),
		"GIT_CONFIG_SYSTEM="+nullDevice(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_PAGER=cat",
		"LC_ALL=C",
	)
}

// gitArgs builds the argument list for one git call: the fixed hardening
// options come first, then the caller's arguments. extra holds additional
// "-c" options.
func gitArgs(dir string, extra []string, args ...string) []string {
	full := []string{"-C", dir, "-c", "core.fsmonitor=false", "-c", "core.quotepath=false", "-c", "log.showSignature=false"}
	full = append(full, extra...)
	return append(full, args...)
}

// dubiousOwnership reports whether git refused the directory because it is
// owned by another user (typical for a container job that checks the
// repository out as root and runs the tool as someone else).
func dubiousOwnership(err error) bool {
	return err != nil && strings.Contains(err.Error(), "dubious ownership")
}

// run executes git in dir with the safe environment and returns stdout. The
// global and system configuration are nulled, so git's safe.directory list is
// empty; when git refuses the directory for ownership, the call is retried
// once with safe.directory set to exactly that directory, which the caller
// named explicitly as the repository to read.
func run(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := runGit(ctx, gitArgs(dir, nil, args...), args[0])
	if dubiousOwnership(err) {
		abs, aerr := filepath.Abs(dir)
		if aerr != nil {
			return "", err
		}
		return runGit(ctx, gitArgs(dir, []string{"-c", "safe.directory=" + filepath.ToSlash(abs)}, args...), args[0])
	}
	return out, err
}

func runGit(ctx context.Context, full []string, name string) (string, error) {
	bin, err := exec.LookPath("git")
	if err != nil {
		return "", ErrNoGit
	}
	ctx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, full...)
	cmd.Env = env()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return "", fmt.Errorf("git %s: %w: %s", name, err, msg)
	}
	return stdout.String(), nil
}

// IsRepo reports whether root is inside a git work tree.
func IsRepo(ctx context.Context, root string) bool {
	out, err := run(ctx, root, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

func checkDir(d string) error {
	if d == "" || strings.HasPrefix(d, "-") || strings.ContainsRune(d, 0) || strings.HasPrefix(d, "/") {
		return fmt.Errorf("invalid directory %q", d)
	}
	return nil
}

// Collect returns history data for each directory (repo-relative, slash
// separated). Directories without commits are returned with a zero Info.
func Collect(ctx context.Context, root string, dirs []string) (map[string]Info, error) {
	out := make(map[string]Info, len(dirs))
	for _, d := range dirs {
		if err := checkDir(d); err != nil {
			return nil, err
		}
		stdout, err := run(ctx, root, "log", "--format=%ct %ae", "--", d)
		if err != nil {
			return nil, err
		}
		var info Info
		authors := map[string]bool{}
		var newest int64
		for _, line := range strings.Split(stdout, "\n") {
			ts, email, ok := strings.Cut(strings.TrimSpace(line), " ")
			if !ok {
				continue
			}
			var secs int64
			if _, err := fmt.Sscanf(ts, "%d", &secs); err != nil {
				continue
			}
			if secs > newest {
				newest = secs
			}
			authors[strings.ToLower(email)] = true
		}
		if newest > 0 {
			info.LastCommit = time.Unix(newest, 0).UTC().Format("2006-01-02")
		}
		info.Authors = len(authors)
		out[d] = info
	}
	return out, nil
}

// ErrShallow is returned by Shallow-aware callers when the clone lacks
// history.
var ErrShallow = errors.New("the repository is a shallow clone, so last-change dates, author counts and tags would be wrong")

// IsShallow reports whether root is a shallow clone. Old git versions that do
// not know --is-shallow-repository print the option back; that counts as not
// shallow.
func IsShallow(ctx context.Context, root string) (bool, error) {
	out, err := run(ctx, root, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "true", nil
}

// DefaultReleaseTagPattern is the glob of release tags: v followed by a
// digit. It does not match plugin tags such as <plugin>--v1.2.0.
const DefaultReleaseTagPattern = "v[0-9]*"

var patternRe = regexp.MustCompile(`^[A-Za-z0-9*?\[\]!._/+-]{1,100}$`)

// ValidTagPattern reports whether s is acceptable as a release-tag glob.
func ValidTagPattern(s string) bool {
	return patternRe.MatchString(s) && !strings.HasPrefix(s, "-") && !strings.Contains(s, "..")
}

// LatestTag returns the newest release tag reachable from HEAD that matches
// DefaultReleaseTagPattern, or "" when there is none.
func LatestTag(ctx context.Context, root string) (string, error) {
	return LatestReleaseTag(ctx, root, DefaultReleaseTagPattern)
}

// LatestReleaseTag returns the newest tag reachable from HEAD that matches
// the glob pattern (git describe --match), never a per-plugin tag of the
// form <plugin>--v<version>, or "" when there is none. An empty pattern means
// DefaultReleaseTagPattern.
func LatestReleaseTag(ctx context.Context, root, pattern string) (string, error) {
	if pattern == "" {
		pattern = DefaultReleaseTagPattern
	}
	if !ValidTagPattern(pattern) {
		return "", fmt.Errorf("invalid release tag pattern %q", pattern)
	}
	out, err := run(ctx, root, "describe", "--tags", "--abbrev=0", "--match", pattern, "--exclude", "*--v*")
	if err != nil {
		if strings.Contains(err.Error(), "No names found") || strings.Contains(err.Error(), "No tags can describe") ||
			strings.Contains(err.Error(), "does not have any commits") || strings.Contains(err.Error(), "bad revision") {
			return "", nil
		}
		return "", err
	}
	tag := strings.TrimSpace(out)
	if !ValidTag(tag) {
		return "", nil
	}
	return tag, nil
}

// ChangedSince returns the subset of dirs that contain a file changed between
// the tag and HEAD, sorted.
func ChangedSince(ctx context.Context, root, tag string, dirs []string) ([]string, error) {
	if !ValidTag(tag) {
		return nil, fmt.Errorf("invalid tag %q", tag)
	}
	for _, d := range dirs {
		if err := checkDir(d); err != nil {
			return nil, err
		}
	}
	stdout, err := run(ctx, root, "diff", "--name-only", "--no-renames", "--no-ext-diff", "refs/tags/"+tag, "HEAD", "--")
	if err != nil {
		return nil, err
	}
	files := strings.Split(strings.TrimSpace(stdout), "\n")
	var changed []string
	for _, d := range dirs {
		prefix := strings.TrimSuffix(d, "/") + "/"
		for _, f := range files {
			if f == strings.TrimSuffix(d, "/") || strings.HasPrefix(f, prefix) {
				changed = append(changed, d)
				break
			}
		}
	}
	sort.Strings(changed)
	return changed, nil
}

// Init runs "git init" in dir with the initial branch main. It is used by
// "ccshelf catalog init --git-init" and only when dir is not a repository yet:
// it creates .git and nothing else, and never commits, fetches or pushes. The
// directory must exist. Like every call here, it runs with the user's global
// and system git configuration switched off.
//
// The template directory is switched off (--template= with no value): the
// system's git templates would copy sample hooks, which are code, into the new
// repository.
func Init(ctx context.Context, dir string) error {
	_, err := runGit(ctx, gitArgs(dir, []string{"-c", "init.defaultBranch=main"}, "init", "--quiet", "--template="), "init")
	return err
}

// EnclosingWorkTree returns the top-level directory of the work tree that
// contains dir (or, when dir does not exist yet, its nearest existing parent),
// or "" when there is none. "git init" in such a directory creates a nested
// repository. It is read-only; the result is untrusted text.
func EnclosingWorkTree(ctx context.Context, dir string) string {
	d := filepath.Clean(dir)
	for {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			break
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
	out, err := run(ctx, d, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// DefaultBranch returns the default branch of the repository whose root is
// dir, and where it was read from: the branch that the remote "origin" points
// at ("origin/HEAD", set by a clone), else the branch that HEAD names ("HEAD",
// which works before the first commit). It returns "" when neither exists
// (a detached HEAD, no repository). It is read-only and makes no network call;
// the name is untrusted text that the caller validates. dir must be the root
// of the repository: git would otherwise read a parent repository.
func DefaultBranch(ctx context.Context, dir string) (branch, source string) {
	if out, err := run(ctx, dir, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if b, ok := strings.CutPrefix(strings.TrimSpace(out), "origin/"); ok && b != "" {
			return b, "origin/HEAD"
		}
	}
	if out, err := run(ctx, dir, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		if b := strings.TrimSpace(out); b != "" {
			return b, "HEAD"
		}
	}
	return "", ""
}

// RemoteURL returns the URL of the remote "origin" of the repository in dir,
// or "" when there is none (or dir is not a repository). It is read-only and
// makes no network call. The URL is untrusted text: callers validate or
// sanitize it before use.
func RemoteURL(ctx context.Context, dir string) string {
	out, err := run(ctx, dir, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
