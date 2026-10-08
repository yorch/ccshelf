// Package gitsource provides a profile.Source backed by a pinned git
// repository: the shared "org data repo" that holds profiles/, mcp/registry.toml
// and prompts/ (security requirements SR2 and SR4).
//
// # Lifecycle
//
// New does no I/O. Prepare resolves the pinned ref to a full commit SHA and
// makes sure a verified checkout of exactly that commit exists in the private
// cache. The closure only sees the "git:<url>@<sha>" form of ID. Locator and
// Ref identify the source independently of the commit, so the trust package
// can tell "the tag moved" from "a different source".
//
// # Pinning (SR2)
//
// A Ref must be a tag or a full commit SHA (40 or 64 hex digits). This package
// always rejects branch-like names (main, master, HEAD, develop, origin/...,
// refs/heads/...). Every Prepare resolves a tag with `git ls-remote` and
// prefers the peeled commit of an annotated tag, so a moved tag shows up as a
// different Commit. With Options.RequirePin (trust.require_pin, default true)
// the ref must be a tag or a SHA. Without it, a ref that is not a tag may name
// a branch that is not branch-like, and Prepare still stores it as a SHA. A
// SHA pin needs no network once its checkout is cached.
//
// # Branches (D-54)
//
// Options.Branch tracks a branch instead. It is the only way to follow a
// moving ref, and it is explicit: Ref still refuses branch-like names. The
// branch is resolved with `git ls-remote -- <url> refs/heads/<branch>` to a
// full commit SHA on every Prepare, and ResolveHead does the same without
// preparing a checkout (the launcher uses it for the periodic check). From the
// SHA on, nothing differs from a tag: the checkout, the verification and the
// trust record are per commit, and PrepareCached needs no network. Ref
// returns "branch:<name>", which no tag name can equal, so a branch and a tag
// with the same name never share a trust key. RequirePin applies to Ref only.
//
// # Checkout and verification
//
// The checkout lives in <cache>/<sha256(url)[:16]>/<sha>/ (mode 0700). This
// package builds it in a sibling temporary directory and renames it into
// place. Thus concurrent callers and processes never see a partial tree and
// need no lock. The loser of the race discards its copy and verifies the
// winner's.
//
// Git never writes a working tree. It only does these steps:
//
//   - initializes an object store
//   - fetches the SHA (shallow, no tags, no submodules, and a blob size
//     filter just above the per-file limit)
//   - pins HEAD to it
//   - lists the tree (ls-tree) and reads blobs (cat-file --batch)
//
// This package validates the tree first (content hygiene below). Then it
// writes the files of the watched folders itself from the blobs, with mode
// 0600 and exclusive creation. Without a checkout, no filter driver (git-lfs,
// git-crypt or a custom clean/smudge chosen by a hostile .gitattributes),
// attribute or hook can change a byte: what is on disk is what the commit
// holds. As defense in depth, this package sets GIT_LFS_SKIP_SMUDGE=1 and
// disables lazy fetching of missing objects for everything but fetch itself.
//
// Every use, including reuse of an existing checkout, verifies that:
//
//   - HEAD is the resolved SHA
//   - the commit object hashes to that SHA
//   - the watched folders hold exactly the committed files. It checks sizes
//     first. Then it hashes each file the way git hashes a blob and compares
//     it with the tree entry. It refuses extra or missing files and anything
//     that is not a plain file or directory.
//
// Otherwise Prepare fails with ErrTampered (or ErrHygiene for symlinks and
// special files) and leaves the directory for the user to delete. This
// package never reads anything outside the watched folders.
//
// # The org config and offline use
//
// ccshelf.toml at the source root is a watched file, with the same hygiene
// and size limit. This package reads it from the object store first, because
// its profiles.dir and profiles.mcp_registry say what is watched. If it does
// not parse, Prepare fails with an error that wraps orgconfig.ErrInvalid.
// PrepareCached never contacts the remote.
//
// # Process hygiene
//
// Git runs through exec.CommandContext with explicit argument slices and "--"
// separators. These rules apply:
//
//   - Hooks never run. core.hooksPath points at an empty directory inside the
//     cache, which also overrides a hooks path set in the user's own
//     configuration.
//   - fsmonitor and automatic gc are off.
//   - The ext transport is disabled.
//   - The file transport is allowed only with Options.AllowLocal.
//   - Git does not recurse into submodules.
//   - GIT_ALLOW_PROTOCOL restricts transports to https and ssh.
//   - Git prompts are disabled.
//
// The environment is an allowlist (see keptGitEnv). Every GIT_* variable
// except the ssh and http-proxy settings is dropped, so the environment cannot
// redirect or weaken git. SSH_ASKPASS is dropped too. Other variables (HOME,
// PATH, the proxy variables, SSH_AUTH_SOCK) pass through. Git still reads the
// user's git configuration, credential helpers and ssh configuration from
// HOME and XDG_CONFIG_HOME, so private repositories work.
//
// URLs follow config.ValidateGitURL, so no token can reach Locator, ID, the
// lockfile, argv or an error message. (file and local paths are accepted with
// AllowLocal, which exists for tests.) This package sanitizes git's standard
// error before it enters an error value, because a remote can write to it.
//
// # Content hygiene
//
// Before it writes anything, this package checks these paths in the committed
// tree:
//
//   - everything under profiles/, mcp/, prompts/ and catalog/plugins/ of the
//     source root
//   - the marketplace files named by catalog.marketplaces (default
//     .claude-plugin/marketplace.json)
//
// These are errors that name the path:
//
//   - symlinks and submodules
//   - unusual file modes
//   - files over 1 MiB (or that the server left out for being bigger)
//   - more than 5000 files or 32 MiB in total
//   - path components such as "..", ".git", ":" or names with backslashes,
//     control or invisible characters
//
// watchSet says why the catalog data is watched. With [catalog] enabled =
// false in ccshelf.toml (a profiles-only repo), none of it is watched, so a
// source without it, or with a stray copy, works the same.
package gitsource
