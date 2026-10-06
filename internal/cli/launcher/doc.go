// Package launcher implements the launcher commands of ccshelf: run, dry-run,
// show, ls, diff, new, edit, init, trust, account, shell-init, version and
// completion.
//
// Commands(get) returns the cobra commands; the root command (built elsewhere)
// adds them. Every RunE calls get() once, after flag parsing, and reaches the
// terminal, environment, clock and prompter only through the clicore.Context,
// so tests drive a command tree with scripted streams and answers.
//
// # The run pipeline
//
// run and dry-run share one pipeline, in this fixed order, failing closed at
// every step:
//
//  1. load the configuration;
//  2. resolve the account (an explicit --account overrides an existing
//     CLAUDE_CONFIG_DIR with a printed warning; otherwise the environment is
//     honored and never overridden);
//  3. locate the claude binary;
//  4. prepare the profile sources (personal directory, configured dir, git and
//     plugin sources, and the project's .ccshelf folder only when it is
//     trusted);
//  5. resolve the profile closure;
//  6. check trust: an untrusted or changed closure exits with code 4 unless the
//     user confirms the printed closure interactively. --yes never accepts
//     trust; the scripted form is "ccshelf trust <profile> --accept <hash>";
//  7. list the installed plugins in the working directory;
//  8. build the policy capability matrix (read-only, never by trial) and plan
//     what the profile may use;
//  9. build and validate the settings, write them to the private cache, and
//     re-read and validate the file that will be passed to claude;
//  10. write the prompt file and MCP config, assemble the arguments and the
//     environment, and start claude (exec on Unix, spawn on Windows).
//
// # Shared sources that fail
//
// A shared source that cannot be loaded (unreachable remote, broken checkout)
// is left out with a warning, which is also part of the structured warnings
// of dry-run --json. For a git source with nothing pinned for its tag, the
// newest cached checkout that passes verification is used, loudly. The
// protected plugins and MCP servers pinned in all trust lockfile entries stay
// enforced, plus those of the failed source's own verified org config when it
// has one. When nothing is known, a LOUD WARNING says that none are enforced.
// Pruning never touches the git checkouts when the lockfile cannot be read.
//
// For personal profiles a moved tag's protect list is taken without a trust
// prompt by design: personal closures are never trust-checked, so the
// protect list of a source's current ccshelf.toml applies to them as it
// resolves, while the protect lists pinned in a trusted closure change its
// hash and need a review.
//
// Every interactive flow ends by printing the equivalent flag command line.
// The package never writes outside the ccshelf configuration and cache
// directories and never touches Claude Code's own state.
package launcher
