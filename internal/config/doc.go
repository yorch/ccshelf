// Package config loads, validates and saves the user's ccshelf configuration
// file (config.toml) and resolves which Claude Code account a run uses.
//
// # Location
//
// Dir is "$XDG_CONFIG_HOME/ccshelf" (or "~/.config/ccshelf") on macOS
// and Linux and "%APPDATA%\ccshelf" on Windows. Dir ignores a relative
// XDG_CONFIG_HOME, as the XDG specification requires. The profile package
// derives its personal profile directory from Dir. (profile imports config,
// never the other way round, so there is no dependency cycle.) Path,
// LockfilePath and ProjectTrustPath name the files that other packages keep
// next to the config.
//
// # Strictness
//
// The file is a closed schema:
//
//   - Unknown keys are errors (with line numbers).
//   - Keys must be spelled exactly as documented. The decoder alone would
//     accept "Trust" for "trust", so Load checks the spelling itself.
//   - A missing file yields Default().
//   - The file is limited to 256 KiB.
//
// Validation rules:
//
//   - trust.on_change is "prompt" or "fail". Validate rejects "allow" because
//     trust is never auto-accepted (SR2).
//   - git sources need a pinned ref when trust.require_pin is true (the
//     default): a full 40 hex digit commit id (64 for SHA-256 repositories) or
//     a tag name matching ^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$ (see
//     ValidatePin). Validate rejects anything containing "/", a first word of
//     refs, heads, remotes or origin, the names HEAD, FETCH_HEAD, ORIG_HEAD,
//     MERGE_HEAD, main, master, develop, dev, trunk, release, latest, stable
//     and next, and hex strings of 7 to 39 digits (ambiguous short ids). This
//     is a syntax gate, not the security boundary. The git source fetches
//     refs/tags/<ref> only and records the peeled commit SHA, and trust
//     compares that SHA.
//   - git URLs (ValidateGitURL) are https://host/path with no user
//     information at all, ssh://[user@]host/path without a password, or the
//     scp-like user@host:path. Validate rejects file:, bare paths, ext::,
//     fd:: and every <helper>:: transport. It also rejects a leading "-",
//     whitespace, control characters and anything that looks like a token
//     (ghp_, github_pat_, glpat-, xoxb-) anywhere in url, ref, path or name.
//   - dir sources need an absolute path or one starting with "~" (or "$"
//     followed by a variable, which must expand to an absolute path).
//     Validate rejects a relative path, because it would resolve against the
//     working directory, which a cloned repository can influence. ccshelf
//     would then treat the source as the user's own.
//   - dir sources need a path. plugin sources need plugin = "name@marketplace".
//   - account names match ^[a-z0-9][a-z0-9-]{0,31}$. ExpandPath expands
//     config_dir (~, $VAR, ${VAR}). Then config_dir must be absolute and must
//     not be the default Claude Code directory (~/.claude). Validate compares
//     directories after it resolves symlinks (as far as they exist). Two
//     accounts may not share a directory or contain one another. No account
//     directory may contain or sit inside ~/.claude.
//   - default_account must name a configured account.
//
// Save writes atomically (temporary file in the same directory, then rename)
// with mode 0600 in a 0700 directory, and refuses to write through a symlink.
//
// # Accounts
//
// ResolveAccount implements the precedence documented in launcher.md with two
// deliberate rules for safety:
//
//   - An implicit choice (a profile's account or default_account) never
//     overrides an existing CLAUDE_CONFIG_DIR in the environment.
//   - An explicit --account flag does win over an existing CLAUDE_CONFIG_DIR,
//     because the flag is the user's stated intent for this run. The result
//     then has OverridesEnv set and a Note that says so, so that the caller
//     can show it. ResolveAccount never makes the choice silently.
//
// The order is
//
//  1. the --account flag (explicit). When CLAUDE_CONFIG_DIR is also set, the
//     result has OverridesEnv and a Note.
//  2. an existing CLAUDE_CONFIG_DIR (FromEnv). The launcher must not set it.
//  3. the profile's account field
//  4. default_account
//  5. none (Claude Code's default directory)
package config
