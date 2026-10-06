// Package codeowners parses CODEOWNERS files and answers "who owns this
// path" with GitHub's semantics.
//
// Supported: comments (a line or trailing part starting with #; \# is a
// literal #), the last matching pattern wins, a leading / anchors the pattern
// to the repository root, a pattern containing a / elsewhere is anchored too,
// a trailing / matches a directory and everything below it, * (not across
// /), ** (across directories), ? (one character, not /), several owners per
// line, and a pattern without owners that removes ownership. Matching is case
// sensitive.
//
// GitHub rejects some syntax and so does this package, by recording an Issue
// and skipping the line: negation with !, character ranges with [ ], and
// owners that are not @user, @org/team or an email address. Group-style
// matching of "docs/*" follows GitHub: it matches the files directly in docs
// but not files in subdirectories, whereas a pattern whose last segment has
// no wildcard (for example "docs" or "/plugins/x") owns everything below it.
package codeowners
