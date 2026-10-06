package gitsource

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	maxStdout = 32 << 20
	maxStderr = 64 << 10
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

// strippedEnv lists variables that redirect git to another repository or make
// it prompt; they are removed from the inherited environment.
var strippedEnv = map[string]bool{
	"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_INDEX_FILE": true,
	"GIT_OBJECT_DIRECTORY": true, "GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	"GIT_NAMESPACE": true, "GIT_COMMON_DIR": true, "GIT_PREFIX": true,
	"GIT_CEILING_DIRECTORIES": true, "GIT_ASKPASS": true, "SSH_ASKPASS": true,
	"GIT_TERMINAL_PROMPT": true, "GIT_ALLOW_PROTOCOL": true,
	"GIT_EXTERNAL_DIFF": true, "GIT_PAGER": true, "GIT_EDITOR": true,
	"GIT_SEQUENCE_EDITOR": true, "GIT_CONFIG_PARAMETERS": true,
	"GIT_CONFIG_COUNT": true, "GIT_EXEC_PATH": true,
}

// gitEnv returns the environment for every git call. The user's global
// configuration, credential helpers and ssh settings are inherited.
func (s *Source) gitEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		if strippedEnv[strings.ToUpper(k)] || strings.HasPrefix(strings.ToUpper(k), "GIT_CONFIG_KEY_") || strings.HasPrefix(strings.ToUpper(k), "GIT_CONFIG_VALUE_") {
			continue
		}
		env = append(env, kv)
	}
	protos := "https:ssh"
	if s.opts.AllowLocal {
		protos += ":file"
	}
	return append(env, "GIT_TERMINAL_PROMPT=0", "GIT_ALLOW_PROTOCOL="+protos)
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
		"-c", "advice.detachedHead=false",
		"-c", "init.defaultBranch=main",
	}
}

// git runs git with the hardening options and returns stdout. dir is the
// working directory; repo, when set, pins the git and work tree explicitly so
// git can never discover another repository above it.
func (s *Source) git(ctx context.Context, dir, repo string, args ...string) (string, error) {
	return s.gitEnvRun(ctx, dir, repo, nil, args...)
}

// gitEnvRun is git with extra environment entries appended.
func (s *Source) gitEnvRun(ctx context.Context, dir, repo string, extra []string, args ...string) (string, error) {
	full := s.hardening()
	if repo != "" {
		full = append(full, "--git-dir="+repo+string(os.PathSeparator)+".git", "--work-tree="+repo)
	}
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, s.gitPath(), full...)
	cmd.Dir = dir
	cmd.Env = append(s.gitEnv(), extra...)
	var out, errb cappedBuffer
	out.max, errb.max = maxStdout, maxStderr
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("git %s: %w", args[0], ctx.Err())
		}
		msg := strings.TrimSpace(errb.buf.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s failed: %s", args[0], msg)
	}
	return out.buf.String(), nil
}

func (s *Source) gitPath() string {
	if s.opts.GitPath != "" {
		return s.opts.GitPath
	}
	return "git"
}
