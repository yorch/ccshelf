// Package claude is everything ccshelf needs from the claude binary:
// finding it, asking it questions, parsing what it prints, and starting it.
//
// # Contracts
//
// Finding: [Locate] tries an explicit override, then CCSHELF_CLAUDE, then
// PATH, then ~/.local/bin. On Windows only claude.exe is run. A lone
// .cmd/.bat shim gives a [ShimOnlyError] (errors.Is(err, [ErrShimOnly])).
// A missing binary matches [ErrNotFound].
//
// Asking: [Version], [ListInstalled], [ListAvailable] and [ReferencedPaths]
// run claude as a child with a context. Listing runs in a caller-chosen
// working directory (the result depends on it), has a 30 second default
// timeout, and reports a stderr excerpt on failure. [InstalledCache] memoizes
// the installed list in the cache directory (fingerprinted on the working
// directory, the claude binary, the user, project and managed settings files
// and the plugin registry). [ReferencedPaths] is best effort
// and returns nil, nil on any failure.
//
// Parsing: [Plugin] decodes the JSON array printed by `claude plugin list
// --json` (unknown keys are kept in Extra). Listing accepts only that array
// (or, with --available, an object whose "installed" is an array): null, {},
// a string or any other shape is an error naming the shape, never an empty
// list, so a misparsed answer cannot turn into "nothing to mask". [ParseInit]
// extracts the system/init event from `--output-format stream-json --verbose`
// output. [ListMarketplaces] runs the read-only `claude plugin marketplace list
// --json` (an array of name, source, repo|url|path and installLocation,
// verified against the real claude) and fails closed on any other shape.
// [MarketplaceOrigin] returns where a marketplace was added from.
// [CompareVersions] and [AtLeast] compare dotted versions.
//
// Starting: [Start] replaces the process on Unix (syscall.Exec, returning only
// on error). On Windows it spawns, waits and returns the exit code. There the
// launcher ignores Ctrl+C. On a termination request the child is asked to
// terminate first and is killed after [TermGrace] if it does not, and a job
// object with kill-on-close keeps it from outliving the launcher. The Windows
// code is untested on real Windows. [StartHook] is a documented test hook. [Spawn] runs a child with explicit stdio on every OS,
// never through a shell, and returns 128+n for a Unix signal death.
//
// [Env] merges environment variables deterministically (on Windows names are
// case-insensitive and entries named like "=C:" are kept).
//
// Tests in other packages should point the code at the fake claude built by
// internal/testutil, never at the real binary.
package claude
