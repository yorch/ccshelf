// Package marketplace reads a Claude Code plugin marketplace repository
// without running the claude binary.
//
// A marketplace is a repository with a .claude-plugin/marketplace.json file
// that lists plugins and where to fetch each one. This package parses that
// file tolerantly (unknown keys are recorded, not rejected) but strictly on
// types, so a wrong type is reported with its JSON path. It also inspects the
// in-repo plugin directories: manifest, hooks, MCP servers and the number of
// skills, agents and commands.
//
// Everything is read through package safepath, so a hostile repository cannot
// make the reader leave the repository root, and every file read has a size
// cap. Nothing from the repository is executed.
//
// Field names follow the marketplace reference as verified in
// docs/research/landscape.md section E. The free-form "metadata" object on a
// plugin entry is not read by Claude Code; ccshelf uses it only in
// single-file catalog mode.
package marketplace
