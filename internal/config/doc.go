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
// The file is a closed schema: unknown keys are errors (with line numbers),
// a missing file yields Default(), and the file is limited to 256 KiB.
// Validation rules:
//
//   - trust.on_change is "prompt" or "fail"; "allow" is rejected because trust
//     is never auto-accepted (SR2).
//   - git sources need a pinned ref when trust.require_pin is true (the
//     default): a 40 or 64 hex digit commit id, or a tag. Names that look like
//     branches (main, master, HEAD, develop, trunk, origin/..., refs/heads/...)
//     are rejected. The URL must not start with "-" or use the "ext::" or
//     "file:" transports, and the folder inside the repo must stay relative.
//   - dir sources need a path; plugin sources need plugin = "name@marketplace".
//   - account names match ^[a-z0-9][a-z0-9-]{0,31}$; config_dir is expanded
//     with ExpandPath (~, $VAR, ${VAR}), must be absolute afterwards and must
//     not be the default Claude Code directory (~/.claude).
//   - default_account must name a configured account.
//
// Save writes atomically (temporary file in the same directory, then rename)
// with mode 0600 in a 0700 directory, and refuses to write through a symlink.
//
// # Accounts
//
// ResolveAccount implements the precedence documented in launcher.md with one
// deliberate refinement for safety: an existing CLAUDE_CONFIG_DIR in the
// environment is never overridden by an implicit choice. The order is
//
//  1. the --account flag (explicit; if the environment also sets
//     CLAUDE_CONFIG_DIR the result has OverridesEnv set so the caller can warn),
//  2. an existing CLAUDE_CONFIG_DIR (FromEnv; the launcher must not set it),
//  3. the profile's account field,
//  4. default_account,
//  5. none (Claude Code's default directory).
package config
