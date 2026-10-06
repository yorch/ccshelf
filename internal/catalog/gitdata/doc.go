// Package gitdata collects optional history data for the catalog by running
// the user's git: the last commit date and the number of distinct authors of
// each plugin directory, the latest tag, and which plugin directories changed
// since a tag.
//
// git is started with exec.CommandContext, never through a shell, with a
// timeout per call, GIT_TERMINAL_PROMPT=0, and the user and system git
// configuration neutralized so that a hook, alias, pager or credential helper
// from a machine's config cannot change what runs. Every inherited GIT_*
// variable is dropped. Paths and refs from outside are validated and passed
// after "--" or as fully qualified refs so they cannot be read as options.
//
// Authors are counted from author email addresses but only the count leaves
// this package: no names or emails are returned.
package gitdata
