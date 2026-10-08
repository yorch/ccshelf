// Package recommend suggests plugins and profiles for a directory. It is
// rule based, deterministic and offline: there is no model call and no
// network access.
//
// # Signals
//
// Collect walks a directory (breadth first, at most MaxFiles files and
// MaxDepth levels), never follows symlinks, skips .git, node_modules, vendor
// and similar directories, ignores entries it cannot read and names with
// control characters, and reads only small manifest files (package.json,
// go.mod, pyproject.toml and the like, at most MaxManifestSize bytes each).
// File names imply command line tools: a Makefile implies make, a Dockerfile
// docker, *.tf files terraform.
//
// # Plugins
//
// The package matches plugins on the relevance signals that Claude Code defines
// (relevance.signals in marketplace.json). The catalog does not carry them,
// so the caller passes them with WithRelevance (see RelevanceOf). Each kind
// of signal adds its weight once: cwd 2, filesRead 3, manifestDeps 3, cli 1,
// hosts 1.
//
//   - cwd: glob tested against the absolute path of the directory, and
//     against its repository-relative path and that of every directory above
//     it inside the repository. A pattern that does not start with "/" also
//     matches at the end of the absolute path, as if prefixed with "**/".
//   - cli: the first whitespace-separated word of the signal must equal,
//     exactly, a command name found in Signals.CLIs.
//   - filesRead: glob tested against the relative paths in Signals.Files. A
//     pattern without "/" matches the file name at any depth.
//   - manifestDeps: {file, pattern}. file is an RE2 regular expression that
//     must match a whole collected manifest path or its base name, and
//     pattern is an RE2 regular expression that the package searches for in
//     its content. Expressions that are invalid or longer than 256 bytes
//     never match. RE2 runs in linear time and the package caps the input at
//     64 KiB, which bounds the work per match.
//   - hosts: exact, case-insensitive hostname equality with Signals.Hosts.
//
// The package uses at most 20 entries of each signal kind.
//
// # Glob semantics (Match)
//
// Paths and patterns use "/" (Match treats a backslash as "/"). Matching is
// case insensitive. "*" matches any run of characters inside one path
// segment, "?" matches one character inside a segment, and a segment that is
// exactly "**" matches zero or more whole segments. Match ignores empty and
// "." segments. There are no character classes or braces. A match must cover
// the whole path.
//
// # Profiles
//
// The package scores profiles on keywords. It looks up the words of each
// when_to_use phrase (three or more letters, minus common stopwords, plurals
// folded) in a vocabulary built from the directory: the names of its last
// three segments, file extensions and well known file names, implied
// commands, and dependency names found in manifests. Each distinct match
// adds 1. Each avoid_when match subtracts 1.5. The output does not list
// profiles that score under 1.
//
// # Deprecation
//
// The package never recommends deprecated plugins and profiles. When one
// matches, it recommends the entry named by superseded_by in its place
// (Replaces says which), if that entry exists and is not deprecated.
//
// # Output
//
// The package orders recommendations by score (high first), then profiles
// before plugins, then name. Every recommendation lists why the package made
// it.
package recommend
