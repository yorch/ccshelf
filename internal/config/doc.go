// Package config loads, validates and saves the user's ccshelf configuration
// file (config.toml) and resolves which Claude Code account a run uses.
//
// # Location
//
// Dir follows XDG on macOS and Linux and uses %APPDATA% on Windows. The
// profile package derives its personal profile directory from Dir. (profile
// imports config, never the other way round.) Path, LockfilePath and
// ProjectTrustPath name the files that other packages keep next to the
// config.
//
// # Strictness
//
// The file is a closed schema of at most 256 KiB. Unknown keys are errors
// (with line numbers). Keys must be spelled exactly as documented: the
// decoder alone would accept "Trust" for "trust", so Load checks the spelling
// itself.
//
// Validation rules:
//
//   - trust.on_change is "prompt" or "fail". Validate rejects "allow" because
//     trust is never auto-accepted (SR2).
//   - git sources need a pinned ref when trust.require_pin is true (the
//     default), see ValidatePin. Its check is a syntax gate, not the security
//     boundary: trust compares the peeled commit SHA.
//   - git URLs follow ValidateGitURL. Validate also rejects anything that
//     looks like a token (ghp_, github_pat_, glpat-, xoxb- and others)
//     anywhere in url, ref, path or name.
//   - dir sources need a path that is absolute or starts with "~" (or "$"
//     followed by a variable, which must expand to an absolute path). A
//     relative path would resolve against the working directory, which a
//     cloned repository can influence, and ccshelf would then treat the
//     source as the user's own.
//   - plugin sources need plugin = "name@marketplace".
//   - account names match ^[a-z0-9][a-z0-9-]{0,31}$. After ExpandPath,
//     config_dir must be absolute. Validate compares directories after it
//     resolves symlinks (as far as they exist). Two accounts may not share a
//     directory or contain one another. No account directory may be, contain
//     or sit inside the default Claude Code directory (~/.claude).
//   - default_account must name a configured account.
//
// Save writes atomically with mode 0600 in a 0700 directory, and refuses to
// write through a symlink.
//
// # Accounts
//
// ResolveAccount uses this order (see docs/design/launcher.md, "Three ways to
// combine, from least to most built-in"):
//
//  1. the --account flag (explicit). It wins over an existing
//     CLAUDE_CONFIG_DIR, because the flag is the user's stated intent for
//     this run. The result then has OverridesEnv set and a Note, so the
//     choice is never silent.
//  2. an existing CLAUDE_CONFIG_DIR (FromEnv). The launcher must not set it.
//     An implicit choice (3 or 4) never overrides it.
//  3. the profile's account field
//  4. default_account
//  5. none (Claude Code's default directory)
package config
