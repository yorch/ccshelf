// Package trust decides whether a resolved profile may run (SR2): it keeps the
// trust lockfile, compares a closure with what the user accepted, explains
// differences in plain words and tracks per-repository trust for project
// profiles.
//
// # Lockfile
//
// Store reads and writes lock.json (see config.LockfilePath): versioned JSON
// with a closed schema, limited to 4 MiB, written atomically (temporary file
// created exclusively with mode 0600, fsync, rename) in a directory of mode
// 0700 that only the user may write to. The file is never followed through a
// symlink, on read or write. An Entry is keyed by (profile, source) and holds
// the source's ref and commit, the accepted closure items and hash, the
// acceptance time and the tool version. Open fails, rather than starting
// empty, when the file is unreadable, has unknown keys or an entry whose
// hash is not the hash of its items, so a hand-edited lockfile fails closed.
//
// The source of an entry is the most specific shared source in the profile's
// chain, without its commit: "git:<url>" (a source that implements
// Locator and Ref, as gitsource and pluginsource do), or the portable id such
// as "dir:org". Because the commit is not part of the key, Check can tell a
// tag that moved (same ref, new commit: TagMoved) from a new source.
//
// # What needs an entry
//
// Personal-only closures are always Trusted and never get an entry: they are
// the user's own files. Every closure that includes an org or project profile
// anywhere in its extends chain needs one, even when the requested profile is
// personal, because an inherited shared profile brings its MCP servers,
// prompt and plugins with it. (A consequence: editing a personal profile that
// extends a shared one changes the closure hash and asks again.)
//
// # Checking and accepting
//
// Check hashes r.Closure as given and never re-reads files. Callers must
// generate settings, MCP config and the prompt file from the same Resolved
// value they checked, and must not mutate it in between; this closes the gap
// between accepting and running. Check also verifies that Closure.Hash is the
// hash of Closure.Items (profile.HashItems); an inconsistent closure is
// never Trusted.
//
// Accept records a closure. With a non-empty expectedHash it requires the
// closure hash to equal it, which is the scripted "ccshelf trust <profile>
// --accept <closure-hash>" form that names exactly what is accepted. The
// interactive path shows Verdict.Describe and asks. "--yes" never reaches
// Accept: trust is never auto-accepted (R6). Non-interactive callers use
// Require, which returns a *NeedsTrustError (exit code 4, ExitNeedsTrust)
// unless the verdict is Trusted.
//
// Describe lists risky changes first (registry entries, prompts, plugin
// includes, profiles that set environment names), in plain words such as
// "new MCP server pagerduty-ro runs: npx -y ...". It prints names, commands
// and environment variable names only; arguments that look like secrets and
// URL credentials are masked, and control characters are replaced so
// repository content cannot inject terminal escape sequences.
//
// # Project trust
//
// ProjectStore (project-trust.json, see config.ProjectTrustPath) records the
// real path of a repository and a hash over every entry below <root>/.ccshelf
// (sorted, names and bytes; symlinks and special files are errors), so editing
// the folder revokes trust automatically. Project profiles are loaded only
// when ProjectAllowed is true (the configuration enables
// trust.trust_project_profiles and the folder is trusted and unchanged), and
// even then their closure needs a lockfile entry: Store.Check returns
// ProjectUntrusted unless the caller states, through CheckWithProject, that the
// folder is trusted.
package trust
