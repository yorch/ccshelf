package gitsource

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/ccshelf/ccshelf/internal/ui"
)

const (
	maxStdout = 32 << 20
	maxStderr = 64 << 10
	// maxGitMessage bounds the text of a git error that reaches an error value.
	maxGitMessage = 2000
)

// errTooMuch is returned by capped writers.
var errTooMuch = errors.New("git produced too much output")

type cappedBuffer struct {
	buf bytes.Buffer
	max int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.buf.Len()+len(p) > c.max {
		return 0, errTooMuch
	}
	return c.buf.Write(p)
}

// keptGitEnv lists the only GIT_* variables passed on to git. Everything else
// that starts with GIT_ is dropped, because a hostile or merely unusual
// environment can redirect git (GIT_DIR, GIT_OBJECT_DIRECTORY, GIT_REPLACE_REF_BASE,
// GIT_SHALLOW_FILE, GIT_ATTR_SOURCE), swap its configuration (GIT_CONFIG_GLOBAL,
// GIT_CONFIG_SYSTEM, GIT_CONFIG_PARAMETERS, GIT_CONFIG_COUNT, GIT_CONFIG_KEY_*),
// weaken TLS (GIT_SSL_NO_VERIFY), run programs (GIT_EXEC_PATH, GIT_PROXY_COMMAND,
// GIT_EXTERNAL_DIFF, GIT_ASKPASS) or write trace files (GIT_TRACE*). Kept on
// purpose are the ssh and http-proxy settings a person needs to reach their
// git host. The user's own git configuration is still read from HOME and
// XDG_CONFIG_HOME, which carries credential helpers and url rewrites.
var keptGitEnv = map[string]bool{
	"GIT_SSH": true, "GIT_SSH_COMMAND": true, "GIT_SSH_VARIANT": true,
	"GIT_HTTP_PROXY_AUTHMETHOD": true,
}

// keepEnv reports whether the environment variable k is passed to git.
func keepEnv(k string) bool {
	u := strings.ToUpper(k)
	switch {
	case strings.HasPrefix(u, "GIT_"):
		return keptGitEnv[u]
	case u == "SSH_ASKPASS" || u == "SSH_ASKPASS_REQUIRE":
		return false // never prompt through a helper program
	}
	return true
}

// gitEnv returns the environment for every git call: the inherited
// environment filtered by keepEnv, then the settings this package owns. Git
// never prompts, never smudges LFS pointers, and may use only the allowed
// transports.
func (s *Source) gitEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		if keepEnv(k) {
			env = append(env, kv)
		}
	}
	protos := "https:ssh"
	if s.opts.AllowLocal {
		protos += ":file"
	}
	return append(env, "GIT_TERMINAL_PROMPT=0", "GIT_ALLOW_PROTOCOL="+protos, "GIT_LFS_SKIP_SMUDGE=1",
		// A replace ref in a cached object store would let git show other
		// content than the pinned commit holds.
		"GIT_NO_REPLACE_OBJECTS=1")
}

// hardening returns the -c options applied to every git call.
func (s *Source) hardening() []string {
	file := "never"
	if s.opts.AllowLocal {
		file = "user"
	}
	return []string{
		"-c", "core.hooksPath=" + s.hooksDir,
		"-c", "core.fsmonitor=false",
		"-c", "protocol.ext.allow=never",
		"-c", "protocol.file.allow=" + file,
		"-c", "submodule.recurse=false",
		"-c", "core.symlinks=false",
		"-c", "core.useReplaceRefs=false",
		// git for Windows silently ignores files whose path exceeds MAX_PATH
		// (260), such as a pack of a checkout with a long cache path, and then
		// reports the commit as missing. No effect elsewhere.
		"-c", "core.longpaths=true",
		"-c", "gc.auto=0",
		"-c", "maintenance.auto=false",
		"-c", "advice.detachedHead=false",
		"-c", "init.defaultBranch=main",
	}
}

// git runs git with the hardening options and returns stdout. dir is the
// working directory; repo, when set, pins the git directory explicitly so
// git can never discover another repository above it.
func (s *Source) git(ctx context.Context, dir, repo string, args ...string) (string, error) {
	return s.run(ctx, dir, repo, nil, maxStdout, args...)
}

// run is git with standard input and a limit on standard output.
func (s *Source) run(ctx context.Context, dir, repo string, stdin io.Reader, maxOut int, args ...string) (string, error) {
	full := s.hardening()
	if repo != "" {
		full = append(full, "--git-dir="+repo+string(os.PathSeparator)+".git")
	}
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, s.gitPath(), full...)
	cmd.Dir = dir
	cmd.Env = s.gitEnv()
	if args[0] != "fetch" {
		// Only fetch may talk to the network: anything else that finds an
		// object missing must fail, not fetch it behind our back.
		cmd.Env = append(cmd.Env, "GIT_NO_LAZY_FETCH=1")
	}
	cmd.Stdin = stdin
	var out, errb cappedBuffer
	out.max, errb.max = maxOut, maxStderr
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("git %s: %w", args[0], ctx.Err())
		}
		msg := gitMessage(errb.buf.String())
		if msg == "" {
			msg = ui.SanitizeLine(err.Error())
		}
		return "", fmt.Errorf("git %s failed: %s", args[0], msg)
	}
	return out.buf.String(), nil
}

// gitMessage turns git's standard error into one safe line. A remote server
// can write to it (sideband "remote:" lines), so control characters and
// invisible formatting characters are replaced before it reaches an error
// value or a terminal.
func gitMessage(stderr string) string {
	var parts []string
	for _, l := range strings.Split(stderr, "\n") {
		if l = strings.TrimSpace(ui.SanitizeLine(l)); l != "" {
			parts = append(parts, l)
		}
	}
	msg := strings.Join(parts, " | ")
	if len(msg) > maxGitMessage {
		msg = strings.ToValidUTF8(msg[:maxGitMessage], "") + "..."
	}
	return msg
}

func (s *Source) gitPath() string {
	if s.opts.GitPath != "" {
		return s.opts.GitPath
	}
	return "git"
}
