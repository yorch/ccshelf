// Package claude is everything ccshelf needs from the claude binary:
// finding it, asking it questions, parsing what it prints, and starting it.
//
// # Contracts
//
// Finding: [Locate] tries an explicit override, then CCSHELF_CLAUDE, then
// PATH, then ~/.local/bin. On Windows only claude.exe is run; a lone
// .cmd/.bat shim gives a [ShimOnlyError] (errors.Is(err, [ErrShimOnly])).
// A missing binary matches [ErrNotFound].
//
// Asking: [Version], [ListInstalled], [ListAvailable] and [ReferencedPaths]
// run claude as a child with a context. Listing runs in a caller-chosen
// working directory (the result depends on it), has a 30 second default
// timeout, and reports a stderr excerpt on failure. [InstalledCache] memoizes
// the installed list in the cache directory. [ReferencedPaths] is best effort
// and returns nil, nil on any failure.
//
// Parsing: [Plugin] decodes the JSON array printed by `claude plugin list
// --json` (unknown keys are kept in Extra); [ParseInit] extracts the
// system/init event from `--output-format stream-json --verbose` output;
// [CompareVersions] and [AtLeast] compare dotted versions.
//
// Starting: [Start] replaces the process on Unix (syscall.Exec, returning only
// on error) and spawns, waits and returns the exit code on Windows (Ctrl+C is
// ignored in the launcher; termination kills the child). [StartHook] is a
// documented test hook. [Spawn] runs a child with explicit stdio on every OS,
// never through a shell, and returns 128+n for a Unix signal death.
//
// [Env] merges environment variables deterministically.
//
// Tests in other packages should point the code at the fake claude built by
// internal/testutil, never at the real binary.
package claude
