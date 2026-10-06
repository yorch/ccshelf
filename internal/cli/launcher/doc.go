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
// Every interactive flow ends by printing the equivalent flag command line.
// The package never writes outside the ccshelf configuration and cache
// directories and never touches Claude Code's own state.
package launcher
