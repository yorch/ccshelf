// Package lint checks an org data repo against the catalog rules and reports
// stable, sorted findings.
//
// Run loads the repo and checks it. LoadData and Check are the two halves, so
// the catalog build can reuse the loaded data. Every rule has a code (CAT###),
// a severity and a one-line description listed by Rules, which backs
// "ccshelf lint --list-rules".
//
// The linter reads only: the marketplace files named in ccshelf.toml, plugin
// directories, sidecars, the taxonomy, CODEOWNERS and the names of the files
// in the profiles directory (the profiles package parses the profile
// manifests themselves, not this package). All reads go through package
// safepath. The linter reads nothing outside the repo root, reports symlinks
// that lead out as findings, caps sizes and runs nothing from the repo.
//
// Text in findings comes from untrusted files (plugin names, descriptions,
// owners). FormatText replaces control characters, FormatJSON escapes
// everything, and FormatGitHub escapes the workflow-command metacharacters so
// a plugin name cannot inject a second command into a CI log.
package lint
