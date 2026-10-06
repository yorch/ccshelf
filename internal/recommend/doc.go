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
// Plugins are matched on the relevance signals that Claude Code defines
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
//   - manifestDeps: {file, pattern}; file is an RE2 regular expression that
//     must match a whole collected manifest path or its base name, pattern is
//     an RE2 regular expression searched in its content. Invalid or longer
//     than 256 byte expressions never match. RE2 runs in linear time and the
//     input is capped at 64 KiB, which bounds the work per match.
//   - hosts: exact, case-insensitive hostname equality with Signals.Hosts.
//
// At most 20 entries of each signal kind are used.
//
// # Glob semantics (Match)
//
// Paths and patterns use "/" (a backslash is treated as "/"). Matching is
// case insensitive. "*" matches any run of characters inside one path
// segment, "?" matches one character inside a segment, and a segment that is
// exactly "**" matches zero or more whole segments. Empty and "." segments
// are ignored. There are no character classes or braces. A match must cover
// the whole path.
//
// # Profiles
//
// Profiles are scored on keywords. The words of each when_to_use phrase
// (three or more letters, minus common stopwords, plurals folded) are looked
// up in a vocabulary built from the directory: the names of its last three
// segments, file extensions and well known file names, implied commands, and
// dependency names found in manifests. Each distinct match adds 1; each
// avoid_when match subtracts 1.5. Profiles scoring under 1 are not listed.
//
// # Deprecation
//
// Deprecated plugins and profiles are never recommended. When one matches, the
// entry named by superseded_by is recommended in its place (Replaces says
// which), if it exists and is not deprecated.
//
// # Output
//
// Recommendations are ordered by score (high first), then profiles before
// plugins, then name. Every recommendation lists why it was made.
package recommend
