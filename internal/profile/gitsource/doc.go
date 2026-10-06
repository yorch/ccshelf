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
// built in a sibling temporary directory and renamed into place, so concurrent
// callers and processes never see a partial tree and need no lock; the loser
// of the race discards its copy and verifies the winner's.
//
// Git never writes a working tree. It only initializes an object store, fetches
// the SHA (--depth 1, --no-tags, --no-recurse-submodules, and a
// --filter=blob:limit just above the per-file limit so a server that supports
// filters does not send big blobs), pins HEAD to it and lists the tree
// (ls-tree) and reads blobs (cat-file --batch). The tree is validated first
// (content hygiene below), then this package writes the files of the watched
// folders itself, with mode 0600 and exclusive creation, from the blobs. No
// checkout means no filter driver (git-lfs, git-crypt or a custom clean/smudge
// chosen by a hostile .gitattributes), attribute or hook can change a byte:
// what is on disk is what the commit holds. GIT_LFS_SKIP_SMUDGE=1 is set as
// defense in depth, and lazy fetching of missing objects is disabled for
// everything but fetch itself.
//
// Every use, including reuse of an existing checkout, verifies that HEAD is the
// resolved SHA, that the commit object hashes to that SHA, and that the watched
// folders hold exactly the committed files: sizes first, then each file hashed
// the way git hashes a blob and compared with the tree entry, with extra or
// missing files and anything that is not a plain file or directory refused.
// Otherwise Prepare fails with ErrTampered (or ErrHygiene for symlinks and
// special files) and the directory is left for the user to delete. Nothing
// outside the watched folders is ever read.
//
// # The org config and offline use
//
// ccshelf.toml at the source root is a watched file like the folders, with the
// same hygiene and size limit. It is read from the object store first; its
// profiles.dir and profiles.mcp_registry say which folder and which registry
// file are watched, and its [protect] lists are exposed through OrgConfig. A
// ccshelf.toml that does not parse fails Prepare with an error wrapping
// orgconfig.ErrInvalid. PrepareCached prepares from an already verified
// checkout of a known commit (the launcher takes it from the trust lockfile)
// without contacting the remote.
//
// # Process hygiene
//
// Git runs through exec.CommandContext with explicit argument slices and "--"
// separators. Hooks never run (core.hooksPath points at an empty directory
// inside the cache, which also overrides a hooks path set in the user's own
// configuration), fsmonitor and automatic gc are off, the ext transport is
// disabled, the file transport is allowed only with Options.AllowLocal,
// submodules are not recursed and GIT_ALLOW_PROTOCOL restricts transports to
// https and ssh. Git prompts are disabled.
//
// The environment is an allowlist: every GIT_* variable is dropped (so
// GIT_CONFIG_GLOBAL, GIT_SSL_NO_VERIFY, GIT_EXEC_PATH, GIT_PROXY_COMMAND,
// GIT_TRACE*, GIT_REPLACE_REF_BASE, GIT_ATTR_SOURCE, GIT_SHALLOW_FILE and the
// rest cannot redirect or weaken git) except GIT_SSH, GIT_SSH_COMMAND,
// GIT_SSH_VARIANT and GIT_HTTP_PROXY_AUTHMETHOD, which reach the user's git
// host; SSH_ASKPASS is dropped too. Other variables (HOME, PATH, the proxy
// variables, SSH_AUTH_SOCK) pass through, and the user's git configuration,
// credential helpers and ssh configuration are read from HOME and
// XDG_CONFIG_HOME so private repositories work.
//
// URLs follow config.ValidateGitURL: https, ssh or scp-like user@host:path
// only, with no userinfo on https, no password on ssh, no query or fragment,
// no whitespace, control or invisible formatting characters, no leading "-"
// and no transport helper, so no token can reach Locator, ID, the lockfile,
// argv or an error message. (file and local paths are accepted with
// AllowLocal, which exists for tests.) Git's standard error is sanitized
// before it enters an error value, since a remote can write to it.
//
// # Content hygiene
//
// Before anything is written, everything under profiles/, mcp/ and prompts/ of
// the source root is checked in the committed tree: symlinks, submodules,
// unusual file modes, files over 1 MiB (or that the server left out for being
// bigger), more than 5000 files or 32 MiB in total, and path components such as
// "..", ".git", ":" or names with backslashes, control or invisible characters
// are errors that name the path. Only those folders are ever read by the
// profile package.
package gitsource
