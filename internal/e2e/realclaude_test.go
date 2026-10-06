//go:build realclaude

package e2e

// Opt-in smoke test against a real Claude Code. It is excluded from every
// normal build and test run, and it needs authentication (ANTHROPIC_API_KEY),
// so only the nightly workflow (.github/workflows/nightly.yml) runs it:
//
//	CCSHELF_REAL_CLAUDE=1 go test -tags realclaude -count=1 -timeout 20m ./internal/e2e/...
//
// Without CCSHELF_REAL_CLAUDE=1 the test skips. Run it with HOME and the XDG
// directories pointing at an empty temporary directory (the workflow does);
// the test also overrides them for the processes it starts. It never installs,
// enables or removes a plugin, and it runs in a scratch working directory.
// The real claude is found on PATH, or set CCSHELF_REAL_CLAUDE_BIN.

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func realClaude(t *testing.T) string {
	t.Helper()
	if os.Getenv("CCSHELF_REAL_CLAUDE") != "1" {
		t.Skip("set CCSHELF_REAL_CLAUDE=1 to run the real-claude smoke test")
	}
	if p := os.Getenv("CCSHELF_REAL_CLAUDE_BIN"); p != "" {
		return p
	}
	p, err := exec.LookPath("claude")
	if err != nil {
		t.Fatalf("claude not found on PATH: %v", err)
	}
	return p
}

func TestRealClaudeSmoke(t *testing.T) {
	claude := realClaude(t)
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY is not set")
	}
	s := newSandbox(t)
	// The fake must not shadow the real binary here.
	s.Setenv("PATH", filepath.Dir(claude)+string(os.PathListSeparator)+os.Getenv("PATH"))
	s.Setenv("ANTHROPIC_API_KEY", os.Getenv("ANTHROPIC_API_KEY"))
	s.Setenv("FAKE_CLAUDE_LOG", "")
	// A profile that names a plugin that is not installed masks nothing and
	// installs nothing; it only sets a session default.
	s.writeProfile("smoke", "name = \"smoke\"\ndescription = \"nightly smoke\"\n\n[plugins]\nmode = \"allow-only\"\ninclude = []\n\n[session]\neffort = \"low\"\n")

	r := s.run("--claude", claude, "dry-run", "smoke")
	if r.Code != 0 {
		t.Fatalf("dry-run: exit %d\n%s", r.Code, r.Stderr)
	}
	contains(t, "dry-run", r.Stdout, "--settings")

	// Start claude through ccshelf and read the system/init event, the same
	// method as Stage 0 (docs/research/stage0.md).
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	res, err := s.try(ctx, s.Work, "", "--claude", claude, "run", "smoke", "--",
		"-p", "Reply with the single word ok.", "--output-format", "stream-json", "--verbose", "--max-turns", "1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Code != 0 {
		t.Fatalf("run: exit %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	var init map[string]any
	sc := bufio.NewScanner(strings.NewReader(res.Stdout))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var m map[string]any
		if json.Unmarshal(sc.Bytes(), &m) == nil && m["type"] == "system" && m["subtype"] == "init" {
			init = m
			break
		}
	}
	if init == nil {
		t.Fatalf("no system/init event in the output:\n%s", res.Stdout)
	}
	// With include = [] and allow-only, no user plugin may have loaded.
	if plugins, ok := init["plugins"].([]any); ok {
		for _, p := range plugins {
			pm, _ := p.(map[string]any)
			if src, _ := pm["source"].(string); strings.Contains(src, "@") {
				t.Errorf("plugin %v loaded although the profile allows none", pm)
			}
		}
	}
}
