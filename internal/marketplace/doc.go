// Package marketplace reads a Claude Code plugin marketplace repository
// without running the claude binary.
//
// A marketplace is a repository with a .claude-plugin/marketplace.json file
// that lists plugins and where to fetch each one. This package parses that
// file tolerantly (it records unknown keys and does not reject them) but
// strictly on types, so it reports a wrong type with its JSON path. It also inspects the
// in-repo plugin directories: manifest, hooks, MCP servers and the number of
// skills, agents and commands.
//
// The package reads everything through package safepath, so a hostile
// repository cannot make the reader leave the repository root, and every file
// read has a size cap. The package runs nothing from the repository.
//
// Field names follow the marketplace reference as verified in
// docs/research/landscape.md section E. Claude Code does not read the
// free-form "metadata" object on a plugin entry. ccshelf uses it only in
// single-file catalog mode.
package marketplace
