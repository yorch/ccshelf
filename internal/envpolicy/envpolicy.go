// Package envpolicy decides which environment variable names a profile may
// set for a Claude Code session (security requirement SR1).
//
// A settings file can carry an "env" map, and a few variables are enough to
// redirect every prompt and file to another endpoint (ANTHROPIC_BASE_URL, a
// proxy variable, NODE_OPTIONS) or to run code at process start (LD_PRELOAD,
// BASH_ENV, PYTHONSTARTUP, JAVA_TOOL_OPTIONS). A denylist can never be
// complete, so the policy is an ALLOWLIST. A profile may set only these names:
//
//   - names in its own namespace, CCSHELF_VAR_<NAME>
//   - reference variables whose name ends in _REF (for example
//     FIGMA_TOKEN_REF). The value is a reference that tools get by name. This
//     package never resolves or logs it.
//   - CCSHELF_PROFILE, which the launcher itself sets
//
// A _REF suffix is NOT a guarantee that nothing interprets the name. For
// example, GitHub Actions tooling reads GITHUB_REF. The allowlist and the
// denylist together are the policy. The denylist has known-dangerous prefixes
// such as ANTHROPIC_, GITHUB_, RUNNER_ and ACTIONS_, suffixes such as
// _OPTIONS, any name that contains _PROXY, and exact names such as PATH and CI.
// The policy applies the denylist first. Thus it refuses GITHUB_REF although
// the name ends in _REF, and it gives clearer error messages.
package envpolicy

import (
	"fmt"
	"regexp"
	"strings"
)

// Profile is the one variable the launcher itself sets to tell tools which
// profile a session was started with.
const Profile = "CCSHELF_PROFILE"

// AdditionalDirsClaudeMD is the variable that makes Claude Code load CLAUDE.md
// files from added directories. Only the launcher sets it, and only when a
// profile has instructions. A profile may not set it: the CLAUDE_CODE_ prefix
// is denied. The settings validator accepts it with the value "1" only.
const AdditionalDirsClaudeMD = "CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD"

var namePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// allowPattern is the allowlist: the CCSHELF_VAR_ namespace or a _REF name.
var allowPattern = regexp.MustCompile(`^(CCSHELF_VAR_[A-Z0-9_]{1,40}|[A-Z][A-Z0-9_]{0,55}_REF)$`)

var deniedPrefixes = []string{
	"ANTHROPIC_", "CLAUDE_CODE_", "CLAUDE_", "NODE_", "OTEL_", "DYLD_", "LD_",
	"GIT_", "SSH_", "AWS_", "AZURE_", "GOOGLE_", "NPM_",
	"PYTHON", "PERL", "RUBY", "JAVA_", "_JAVA", "JDK_", "BUN_", "DENO_", "ELECTRON_",
	"DOCKER_", "KUBE", "PIP_", "CARGO_", "YARN_", "GEM_", "BASH", "ZSH",
	"GITHUB_", "RUNNER_", "ACTIONS_",
}

var deniedSuffixes = []string{"_OPTIONS", "_OPTS"}

var deniedExact = map[string]bool{
	"PATH": true, "HOME": true, "SHELL": true, "USER": true,
	"PYTHONPATH": true, "PYTHONSTARTUP": true, "PERL5OPT": true, "RUBYOPT": true,
	"BASH_ENV": true, "ENV": true, "IFS": true,
	"TMPDIR": true, "TEMP": true, "TMP": true,
	"SSL_CERT_FILE": true, "SSL_CERT_DIR": true, "CURL_CA_BUNDLE": true, "REQUESTS_CA_BUNDLE": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "ALL_PROXY": true, "NO_PROXY": true,
	"GCONV_PATH": true, "SHELLOPTS": true, "BASHOPTS": true, "PS4": true, "PROMPT_COMMAND": true,
	"ZDOTDIR": true, "EDITOR": true, "VISUAL": true, "PAGER": true, "BROWSER": true,
	"LESSOPEN": true, "LESSCLOSE": true, "SSLKEYLOGFILE": true, "KUBECONFIG": true,
	"CI": true,
}

// DeniedReason returns why a profile may not set name, or "" when a profile
// may set it. A profile may always set CCSHELF_PROFILE.
func DeniedReason(name string) string {
	if name == Profile {
		return ""
	}
	if !namePattern.MatchString(name) {
		return "must match ^[A-Z][A-Z0-9_]{0,63}$"
	}
	if r := denylistReason(name); r != "" {
		return r
	}
	if !allowPattern.MatchString(name) {
		return "a profile variable name must be CCSHELF_VAR_<NAME> or end in _REF (allowlist)"
	}
	return ""
}

// denylistReason applies only the denylist (prefixes, suffixes, _PROXY and
// exact names), skipping the allowlist, so tests can exercise the defense in
// depth directly.
func denylistReason(name string) string {
	for _, p := range deniedPrefixes {
		if strings.HasPrefix(name, p) {
			return fmt.Sprintf("names starting with %s can redirect or reconfigure Claude Code", p)
		}
	}
	for _, suf := range deniedSuffixes {
		if strings.HasSuffix(name, suf) {
			return fmt.Sprintf("names ending in %s usually inject options into a runtime", suf)
		}
	}
	if strings.Contains(name, "_PROXY") {
		return "proxy variables can redirect traffic"
	}
	if deniedExact[name] {
		return "this variable changes how processes start or where traffic goes"
	}
	return ""
}

// Allowed reports whether a profile may set the variable name.
func Allowed(name string) bool { return DeniedReason(name) == "" }

// Check returns an error that names the first denied variable in names, or
// nil. The caller is responsible for the sort order of names.
func Check(names []string) error {
	for _, n := range names {
		if r := DeniedReason(n); r != "" {
			return fmt.Errorf("environment variable %q is not allowed in a profile: %s", n, r)
		}
	}
	return nil
}
