// Package launcher implements the launcher commands of ccshelf: run, dry-run,
// show, ls, diff, new, edit, init, trust, account, shell-init, version and
// completion.
//
// Commands(get) returns the cobra commands. The root command (built elsewhere)
// adds them. Every RunE calls get() once, after flag parsing, and reaches the
// terminal, environment, clock and prompter only through the clicore.Context,
// so tests drive a command tree with scripted streams and answers.
//
// # The run pipeline
//
// run and dry-run share one pipeline. It runs in this fixed order and fails
// closed at every step:
//
//  1. Load the configuration.
//  2. Resolve the account. An explicit --account overrides an existing
//     CLAUDE_CONFIG_DIR and prints a warning. Otherwise the pipeline honors
//     the environment and never overrides it.
//  3. Find the claude binary.
//  4. Prepare the profile sources: the personal directory, the configured dir,
//     the git and plugin sources, and the project's .ccshelf folder only when
//     it is trusted.
//  5. Resolve the profile closure.
//  6. Check trust. An untrusted or changed closure exits with code 4 unless
//     the user confirms the printed closure interactively. --yes never accepts
//     trust. The scripted form is "ccshelf trust <profile> --accept <hash>".
//  7. List the installed plugins in the working directory.
//  8. Build the policy capability matrix (read-only, never by trial) and plan
//     what the profile may use.
//  9. Build and validate the settings and write them to the private cache.
//     Then read again and validate the file that the pipeline gives to claude.
//  10. Write the prompt file and MCP config, assemble the arguments and the
//     environment, and start claude (exec on Unix, spawn on Windows).
//
// # Shared sources that fail
//
// When the launcher cannot load a shared source (unreachable remote, broken
// checkout), it leaves the source out and prints a warning. The warning is
// also part of the structured warnings of dry-run --json. For a git source
// with nothing pinned for its tag, the launcher uses the newest cached
// checkout that passes verification, and says so loudly. The protected
// plugins and MCP servers pinned in all trust lockfile entries stay enforced,
// plus those of the failed source's own verified org config when it has one.
// When nothing is known, a LOUD WARNING says that none are enforced. Pruning
// never touches the git checkouts when the launcher cannot read the lockfile.
//
// For personal profiles, the launcher takes a moved tag's protect list without
// a trust prompt by design. Personal closures are never trust-checked, so the
// protect list of a source's current ccshelf.toml applies to them as it
// resolves. The protect lists pinned in a trusted closure change its hash and
// need a review.
//
// # Warnings from the catalog
//
// When an org source carries catalog sidecars (catalog/plugins/<name>.toml, or
// the single-file metadata the org config selects), run, dry-run and show warn
// once for every included plugin whose status is deprecated, naming the
// replacement. It is a warning only and never part of the trust closure. A
// source without sidecars is silent. CatalogProvider gives search and
// recommend catalog data for developers without an org data repo checkout. An
// explicitly configured remote catalog is fetched over HTTPS. Profile sources
// are read from their local directory or verified cache.
//
// Every interactive flow ends by printing the equivalent flag command line.
// The package never writes outside the ccshelf configuration and cache
// directories and never touches Claude Code's own state.
package launcher
