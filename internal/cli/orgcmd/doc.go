// Package orgcmd implements the org and catalog commands of the ccshelf CLI:
// lint, compile, catalog build, search, recommend and doctor. They work on an
// org data repo, whose root comes from the global --root flag or the current
// directory.
//
// Every command reads and writes only through its clicore.Context (streams,
// environment, clock, working directory), prints deterministic output (sorted,
// no timestamps unless asked for) and writes nothing outside the repo root
// (compile, into bundles/) or the directory given with --out (catalog build).
// Exit codes follow package ui: 1 for failures such as lint errors or stale
// bundles, 2 for usage errors and 3 when managed policy blocks something
// (doctor --policy).
//
// Commands(get) returns the commands. The root command in internal/cli adds
// them. The launcher's init command (including init --org) is not here.
package orgcmd
