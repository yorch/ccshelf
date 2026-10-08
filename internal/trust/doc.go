// Package trust decides whether a resolved profile may run (SR2). It keeps the
// trust lockfile, compares a closure with what the user accepted, explains
// differences in plain words and tracks per-repository trust for project
// profiles. See docs/design/security.md, "Security requirements (SR1 to SR5)".
//
// # Lockfile
//
// lock.json is versioned JSON with a closed schema, limited to 4 MiB. Store
// writes it atomically (exclusive create with mode 0600, fsync, rename) in a
// directory of mode 0700 that only the user may write to, and never follows
// it through a symlink. Open fails, rather than starting empty, when the file
// is unreadable, has unknown keys or has an entry whose hash is not the hash
// of its items. Thus a hand-edited lockfile fails closed.
//
// The source of an entry is the most specific shared source in the profile's
// chain, without its commit. Because the commit is not part of the key, Check
// can tell a tag that moved (same ref, new commit: TagMoved) from a new
// source.
//
// # What needs an entry
//
// Personal-only closures are always Trusted and never get an entry, because
// they are the user's own files. Every closure that includes an org or project
// profile anywhere in its extends chain needs one, even when the requested
// profile is personal, because an inherited shared profile brings its MCP
// servers, prompt and plugins with it. (So an edit to a personal profile that
// extends a shared one changes the closure hash and asks again.)
//
// # Checking and accepting
//
// Check never re-reads files. Callers must run what they generate from the
// same Resolved value they checked, which closes the gap between accepting
// and running. Check also verifies that Closure.Hash is the hash of
// Closure.Items. An inconsistent closure is never Trusted and never a "needs
// trust" outcome: Require returns ErrInconsistentClosure, which callers report
// as exit 1, not exit 4.
//
// Accept always needs the hash that was reviewed, so what was shown is what
// is stored. "--yes" never reaches Accept, because trust is never
// auto-accepted (R6). Non-interactive callers use Require and fail closed
// (exit code 4, ExitNeedsTrust).
//
// An Entry keeps enough to explain a later change field by field. ccshelf
// never shows values of *_REF names and of secret-looking names.
//
// All state files (lockfile, project trust file) are written under an
// exclusive operating system lock ("<file>.lock", flock or LockFileEx), so
// concurrent processes never lose each other's accept or revoke. On read, a
// state file is refused unless the user owns it and it has mode 0600 in a
// 0700-style directory (Unix). Where the OS has directory descriptors,
// project folders are read relative to them (openat with O_NOFOLLOW).
// Directories count toward the size limits.
//
// Describe lists risky changes first. It shows registry arguments verbatim,
// because they are committed code and hiding them would let a profile
// conceal what it runs. Server URLs show only scheme, host and path. Describe
// wraps long text and never truncates it. It replaces control characters and
// invisible and bidirectional formatting characters, so repository content
// cannot inject terminal escape sequences or disguise text.
//
// # Project trust
//
// ProjectStore records the real path of a repository and a hash over every
// entry below <root>/.ccshelf (sorted, names and bytes). Symlinks and special
// files are errors. Thus an edit to the folder revokes trust automatically.
// Project trust only lets ccshelf load project profiles (ProjectAllowed).
// Their closure still needs a lockfile entry, and Store.Check returns
// ProjectUntrusted unless the caller uses CheckWithProject.
package trust
