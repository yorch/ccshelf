package gitdata

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
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

func nullDevice() string {
	if runtime.GOOS == "windows" {
		return "NUL"
	}
	return "/dev/null"
}

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

// run executes git in dir with the safe environment and returns stdout.
func run(ctx context.Context, dir string, args ...string) (string, error) {
	bin, err := exec.LookPath("git")
	if err != nil {
		return "", ErrNoGit
	}
	ctx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()
	full := append([]string{"-C", dir, "-c", "core.fsmonitor=false", "-c", "core.quotepath=false", "-c", "log.showSignature=false"}, args...)
	cmd := exec.CommandContext(ctx, bin, full...)
	cmd.Env = env()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, msg)
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

// LatestTag returns the newest tag reachable from HEAD, or "" when there is
// none.
func LatestTag(ctx context.Context, root string) (string, error) {
	out, err := run(ctx, root, "describe", "--tags", "--abbrev=0")
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
