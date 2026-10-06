// Package config loads, validates and saves the user's ccshelf configuration
// file (config.toml) and resolves which Claude Code account a run uses.
//
// # Location
//
// ConfigDir is "$XDG_CONFIG_HOME/ccshelf" (or "~/.config/ccshelf") on macOS
// and Linux and "%APPDATA%\ccshelf" on Windows. A relative XDG_CONFIG_HOME is
// ignored, as the XDG specification requires. The profile package derives its
// personal profile directory from ConfigDir (profile imports config, never the
// other way round, so there is no dependency cycle). Path, LockfilePath and
// ProjectTrustPath name the files other packages keep next to the config.
//
// # Strictness
//
// The file is a closed schema: unknown keys are errors (with line numbers) and
// keys must be spelled exactly as documented (the decoder alone would accept
// "Trust" for "trust", so Load checks the spelling itself),
// a missing file yields Default(), and the file is limited to 256 KiB.
// Validation rules:
//
//   - trust.on_change is "prompt" or "fail"; "allow" is rejected because trust
//     is never auto-accepted (SR2).
//   - git sources need a pinned ref when trust.require_pin is true (the
//     default): a full 40 hex digit commit id (64 for SHA-256 repositories) or
//     a tag name matching ^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$ (see
//     ValidatePin). Anything containing "/", a first word of refs, heads,
//     remotes or origin, the names HEAD, FETCH_HEAD, ORIG_HEAD, MERGE_HEAD,
//     main, master, develop, dev, trunk, release, latest, stable and next, and
//     hex strings of 7 to 39 digits (ambiguous short ids) are rejected. This is
//     a syntax gate, not the security boundary: the git source fetches
//     refs/tags/<ref> only and records the peeled commit SHA, and trust
//     compares that SHA.
//   - git URLs (ValidateGitURL) are https://host/path with no user
//     information at all, ssh://[user@]host/path without a password, or the
//     scp-like user@host:path. file:, bare paths, ext::, fd:: and every
//     <helper>:: transport, a leading "-", whitespace, control characters and
//     anything that looks like a token (ghp_, github_pat_, glpat-, xoxb-)
//     anywhere in url, ref, path or name are rejected.
//   - dir sources need an absolute path or one starting with "~" (or "$"
//     followed by a variable, which must expand to an absolute path). A
//     relative path is rejected because it would resolve against the working
//     directory, which a cloned repository can influence, and the source would
//     then be treated as the user's own.
//   - dir sources need a path; plugin sources need plugin = "name@marketplace".
//   - account names match ^[a-z0-9][a-z0-9-]{0,31}$; config_dir is expanded
//     with ExpandPath (~, $VAR, ${VAR}), must be absolute afterwards and must
//     not be the default Claude Code directory (~/.claude). Directories are
//     compared after resolving symlinks (as far as they exist), and two
//     accounts may not share a directory or contain one another, and no
//     account directory may contain or sit inside ~/.claude.
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
//   - an existing CLAUDE_CONFIG_DIR in the environment is never overridden by an
//     implicit choice (a profile's account or default_account); and
//   - an explicit --account flag does win over an existing CLAUDE_CONFIG_DIR,
//     because the flag is the user's stated intent for this run. The result
//     then has OverridesEnv set and a Note saying so, so that the caller can
//     show it; the choice is never made silently.
//
// The order is
//
//  1. the --account flag (explicit; with CLAUDE_CONFIG_DIR also set the result
//     has OverridesEnv and a Note),
//  2. an existing CLAUDE_CONFIG_DIR (FromEnv; the launcher must not set it),
//  3. the profile's account field,
//  4. default_account,
//  5. none (Claude Code's default directory).
package config
