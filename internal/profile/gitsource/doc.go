// Package gitsource provides a profile.Source backed by a pinned git
// repository: the shared "org data repo" that holds profiles/, mcp/registry.toml
// and prompts/ (security requirements SR2 and SR4).
//
// # Lifecycle
//
// New validates the options without touching the network or the file system.
// Prepare then resolves the pinned ref to a full commit SHA, makes sure a
// verified checkout of exactly that commit exists in the private cache, and
// prepares the Source for use. Until Prepare succeeds, Names, Open and Root
// return ErrNotPrepared and Commit returns "". ID is "git:<url>@<ref>" before
// Prepare and "git:<url>@<sha>" after it; the closure only ever sees the
// second form. Locator and Ref identify the source independently of the
// commit, which lets the trust package tell "the tag moved" from "a different
// source".
//
// # Pinning (SR2)
//
// A Ref must be a tag or a full commit SHA (40 or 64 hex digits). Branch-like
// names (main, master, HEAD, develop, origin/..., refs/heads/...) are always
// rejected. A tag is resolved with `git ls-remote`, preferring the peeled
// commit of an annotated tag, on every Prepare, so a tag that was moved on the
// remote shows up as a different Commit. With Options.RequirePin (what the
// launcher sets from trust.require_pin, default true) the ref must be a tag
// or a SHA; without it a ref that is not a tag may name a branch that is not
// branch-like, resolved once and still stored as a SHA. A SHA pin needs no
// network once its checkout is cached.
//
// # Checkout and verification
//
// The checkout lives in <cache>/<sha256(url)[:16]>/<sha>/ (mode 0700). It is
// built in a sibling temporary directory (git init, remote add, fetch --depth
// 1 of the SHA, falling back to fetching the tag, checkout --detach) and
// renamed into place, so concurrent callers and processes never see a partial
// tree and need no lock; the loser of the race discards its copy and verifies
// the winner's. Every use, including reuse of an existing checkout, verifies
// that HEAD is the resolved SHA, rebuilds the index from HEAD (so content is
// re-hashed rather than trusted by timestamp) and requires a clean status
// including untracked and ignored files; otherwise Prepare fails with
// ErrTampered and the directory is left for the user to delete.
//
// # Process hygiene
//
// Git runs through exec.CommandContext with explicit argument slices and "--"
// separators. Hooks never run (core.hooksPath points at an empty directory
// inside the cache, which also overrides a hooks path set in the user's own
// configuration), fsmonitor is off, the ext transport is disabled, the file
// transport is allowed only with Options.AllowLocal, submodules are not
// recursed and GIT_ALLOW_PROTOCOL restricts transports to https and ssh. Git
// prompts are disabled (GIT_TERMINAL_PROMPT=0, GIT_ASKPASS and SSH_ASKPASS
// removed) and variables that redirect git to other repositories are
// stripped. The user's git configuration, credential helpers and ssh
// configuration are otherwise inherited so private repositories work.
// URLs may not start with "-", contain control characters or whitespace, use
// a transport helper ("ext::"), embed a password, or use any scheme other
// than https, ssh or scp-like user@host:path (plus file and local paths with
// AllowLocal, which exists for tests).
//
// # Content hygiene
//
// After checkout, everything under profiles/, mcp/ and prompts/ of the source
// root is checked, both in the committed tree (so symlinks are caught even
// though the checkout uses core.symlinks=false) and on disk: symlinks,
// submodules, files over 1 MiB and path components such as "..", ".git" or
// names with backslashes or control characters are errors that name the
// path. Only those folders are ever read by the profile package.
package gitsource
