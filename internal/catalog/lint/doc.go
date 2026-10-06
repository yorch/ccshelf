// Package lint checks an org data repo against the catalog rules and reports
// stable, sorted findings.
//
// Run loads the repo and checks it; LoadData and Check are the two halves, so
// the catalog build can reuse the loaded data. Every rule has a code (CAT###),
// a severity and a one-line description listed by Rules, which backs
// "ccshelf lint --list-rules".
//
// The linter reads only: the marketplace files named in ccshelf.toml, plugin
// directories, sidecars, the taxonomy, CODEOWNERS and the names of the files
// in the profiles directory (profile manifests themselves are parsed by the
// profiles package, not here). All reads go through package safepath: nothing
// outside the repo root is read, symlinks that lead out are findings, sizes
// are capped and nothing from the repo is executed.
//
// Text in findings comes from untrusted files (plugin names, descriptions,
// owners). FormatText replaces control characters, FormatJSON escapes
// everything, and FormatGitHub escapes the workflow-command metacharacters so
// a plugin name cannot inject a second command into a CI log.
package lint
