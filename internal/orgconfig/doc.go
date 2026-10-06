// Package orgconfig reads ccshelf.toml, the org config at the root of an
// organization's data repo.
//
// The file is strict TOML: an unknown section or key is an error, not
// ignored, because a misspelled lint rule that silently does nothing is worse
// than a failed run. A missing file is not an error; Load then returns the
// defaults. Every path in the file is relative to the repo root, uses forward
// slashes and must stay inside the root (no absolute path, no "..", no
// symlink that leads out).
//
// The [protect] section lists plugins and MCP servers that no profile may
// mask (security requirement SR3). The names are validated here; enforcement
// lives in the launcher.
package orgconfig
