// Package trust decides whether a resolved profile may run (SR2). It keeps the
// trust lockfile, compares a closure with what the user accepted, explains
// differences in plain words and tracks per-repository trust for project
// profiles.
//
// # Lockfile
//
// Store reads and writes lock.json (see config.LockfilePath). The file is
// versioned JSON with a closed schema, limited to 4 MiB. Store writes it
// atomically (temporary file created exclusively with mode 0600, fsync,
// rename) in a directory of mode 0700 that only the user may write to. Store
// never follows the file through a symlink, on read or write. An Entry is
// keyed by (profile, source). It holds the source's ref and commit, the
// accepted closure items and hash, the acceptance time and the tool version.
// Open fails, rather than starting empty, when the file is unreadable, has
// unknown keys or has an entry whose hash is not the hash of its items. Thus a
// hand-edited lockfile fails closed.
//
// The source of an entry is the most specific shared source in the profile's
// chain, without its commit: "git:<url>" (a source that implements
// Locator and Ref, as gitsource and pluginsource do), or the portable id such
// as "dir:org". Because the commit is not part of the key, Check can tell a
// tag that moved (same ref, new commit: TagMoved) from a new source.
//
// # What needs an entry
//
// Personal-only closures are always Trusted and never get an entry, because
// they are the user's own files. Every closure that includes an org or project
// profile anywhere in its extends chain needs one, even when the requested
// profile is personal. The reason is that an inherited shared profile brings
// its MCP servers, prompt and plugins with it. (A consequence: an edit to a
// personal profile that extends a shared one changes the closure hash and
// asks again.)
//
// # Checking and accepting
//
// Check hashes r.Closure as given and never re-reads files. Callers must
// generate settings, MCP config and the prompt file from the same Resolved
// value they checked, and must not mutate it in between. This closes the gap
// between accepting and running. Check also verifies that Closure.Hash is the
// hash of Closure.Items (profile.HashItems). An inconsistent closure is never
// Trusted.
//
// Accept records a closure and always needs the hash that was reviewed. An
// empty hash is ErrHashRequired, and a hash that is not the current closure
// hash is ErrHashMismatch. Thus what was shown (Verdict.Hash) is what is
// stored. The scripted form is "ccshelf trust <profile> --accept
// <closure-hash>". The interactive path shows Verdict.Describe, asks, and
// passes Verdict.Hash. "--yes" never reaches Accept, because trust is never
// auto-accepted (R6). Non-interactive callers use Require, which returns a
// *NeedsTrustError (exit code 4, ExitNeedsTrust) unless the verdict is
// Trusted.
//
// An entry also records:
//
//   - the ref and commit of every shared source in the chain (a tag that moved
//     in an inherited source is TagMoved)
//   - the canonical controls text of every profile in the chain
//
// Thus ccshelf can explain a later change to what a profile can do field by
// field: environment variables added, removed or changed (ccshelf never shows
// values of *_REF names and of secret-looking names), plugin includes and
// excludes, inherit_user_settings, account and so on. An inconsistent closure
// (hash not matching its items) is never a "needs trust" outcome. Require
// returns ErrInconsistentClosure, which callers report as a failure (exit 1),
// not as exit 4.
//
// This package writes all state files (lockfile, project trust file) under an
// exclusive operating system lock ("<file>.lock", flock or LockFileEx). Thus
// concurrent processes never lose each other's accept or revoke. On read, it
// refuses a state file unless the user owns it and it has mode 0600 in a
// 0700-style directory (Unix). Where the OS has directory descriptors, it reads
// project folders relative to them (openat with O_NOFOLLOW). Directories count
// toward the size limits.
//
// Describe lists risky changes first (registry entries, prompts, plugin
// includes, profiles that set environment names), in plain words such as
// "new MCP server pagerduty-ro runs: npx -y ...". It shows registry arguments
// verbatim, because they are committed code, and if it hid them, a profile
// could conceal what it runs. Server URLs show only scheme, host and path.
// Describe wraps long text and never truncates it. It replaces control
// characters and invisible and bidirectional formatting characters, so
// repository content cannot inject terminal escape sequences or disguise text.
//
// # Project trust
//
// ProjectStore (project-trust.json, see config.ProjectTrustPath) records the
// real path of a repository and a hash over every entry below <root>/.ccshelf
// (sorted, names and bytes). Symlinks and special files are errors. Thus an
// edit to the folder revokes trust automatically. ccshelf loads project
// profiles only when ProjectAllowed is true (the configuration enables
// trust.trust_project_profiles and the folder is trusted and unchanged). Even
// then, their closure needs a lockfile entry. Store.Check returns
// ProjectUntrusted unless the caller states, through CheckWithProject, that the
// folder is trusted.
package trust
