package envpolicy

import "testing"

func TestAllowed(t *testing.T) {
	allowed := []string{"FIGMA_TOKEN_REF", "CCSHELF_PROFILE", "CCSHELF_VAR_TEAM", "CCSHELF_VAR_A_B_2", "PAGERDUTY_TOKEN_REF"}
	for _, n := range allowed {
		if !Allowed(n) {
			t.Errorf("Allowed(%q) = false (%s), want true", n, DeniedReason(n))
		}
	}
	denied := []string{
		"ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CONFIG_DIR",
		"NODE_OPTIONS", "NODE_EXTRA_CA_CERTS", "JAVA_TOOL_OPTIONS", "_JAVA_OPTIONS", "JDK_JAVA_OPTIONS", "PYTHONHOME", "PYTHONWARNINGS", "PERL5LIB", "RUBYLIB", "PROMPT_COMMAND", "ZDOTDIR", "EDITOR", "VISUAL", "PAGER", "BROWSER", "DOCKER_HOST", "KUBECONFIG", "SSLKEYLOGFILE", "GCONV_PATH", "SHELLOPTS", "BASHOPTS", "PS4", "PIP_INDEX_URL", "CARGO_HOME", "YARN_RC_FILENAME", "GEM_HOME", "BUN_INSTALL", "DENO_DIR", "ELECTRON_RUN_AS_NODE", "MY_OPTS", "SOME_OPTIONS", "LD_LIBRARY_PATH_REF_X", "OTEL_EXPORTER_OTLP_ENDPOINT", "LD_PRELOAD",
		"DYLD_INSERT_LIBRARIES", "HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "NO_PROXY", "MY_PROXY_URL",
		"PATH", "HOME", "SHELL", "BASH_ENV", "ENV", "PYTHONPATH", "SSL_CERT_FILE", "TMPDIR",
		"GIT_SSH_COMMAND", "SSH_AUTH_SOCK", "AWS_PROFILE", "NPM_CONFIG_REGISTRY",
		"MY_TOOL_MODE", "A", "TEAM_NAME_2", "FOO", "CCSHELF_VAR_", "CCSHELF_VARTEAM", "REF", "_REF", "lowercase", "1LEADING_DIGIT", "HAS-DASH", "", "WAY_TOO_LONG_" + string(make([]byte, 0)) + "XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX",
	}
	for _, n := range denied {
		if Allowed(n) {
			t.Errorf("Allowed(%q) = true, want false", n)
		}
	}
}

func TestCheck(t *testing.T) {
	if err := Check([]string{"FOO_REF", "CCSHELF_PROFILE"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := Check([]string{"FOO_REF", "NODE_OPTIONS"}); err == nil {
		t.Fatal("expected an error for NODE_OPTIONS")
	}
}

func TestCIAndGitHubNames(t *testing.T) {
	for _, n := range []string{"CI", "GITHUB_REF", "GITHUB_TOKEN_REF", "RUNNER_TEMP", "ACTIONS_RUNTIME_TOKEN", "RUNNER_REF", "ACTIONS_ID_REF"} {
		if Allowed(n) {
			t.Errorf("Allowed(%q) = true, want false", n)
		}
	}
	if !Allowed("CIRCLE_TOKEN_REF") {
		t.Error("CIRCLE_TOKEN_REF should stay allowed")
	}
}

// TestDenylistDirect exercises the denylist without the allowlist, so the
// defense in depth is not hidden behind it.
func TestDenylistDirect(t *testing.T) {
	for n := range deniedExact {
		if denylistReason(n) == "" {
			t.Errorf("denylistReason(%q) is empty", n)
		}
	}
	// Names that no prefix or suffix catches, so only deniedExact or the
	// _PROXY check can refuse them.
	for _, n := range []string{"PATH", "HOME", "CI", "EDITOR", "PAGER", "IFS", "SSLKEYLOGFILE", "MY_PROXY_URL", "FOO_PROXY", "X_PROXY_Y"} {
		if denylistReason(n) == "" {
			t.Errorf("denylistReason(%q) is empty", n)
		}
	}
	for _, n := range []string{"CCSHELF_VAR_X", "FOO_REF", "SOMETHING"} {
		if r := denylistReason(n); r != "" {
			t.Errorf("denylistReason(%q) = %q, want empty", n, r)
		}
	}
	if DeniedReason("SOMETHING") == "" {
		t.Error("the allowlist must still refuse SOMETHING")
	}
}
