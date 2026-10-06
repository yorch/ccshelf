// Package account manages named Claude Code accounts: a name that maps to a
// separate CLAUDE_CONFIG_DIR (launcher.md, "Combining profiles with
// accounts", way 2).
//
// An account isolates credentials, user settings, installed plugins and
// marketplaces, history and memory, because Claude Code keeps all of them in
// its configuration directory. A profile then filters within an account. This
// package only prepares and describes accounts; it never reads, copies or
// moves credentials, never touches the macOS keychain, never runs claude and
// never deletes a directory.
//
// # Add
//
// Add validates the name (config.ValidAccountName: a name, never a path) and
// the directory (absolute after ~ and $VAR expansion, not Claude Code's
// default directory ~/.claude, not inside it and not an ancestor of it, not
// reached through a symlink, not already used by another account, and, when it
// exists, a directory that is empty or already this account's directory). It
// creates the directory with mode 0700 and returns a Plan: the one-time steps
// the person must do by hand (start claude with the variable set, /login, then
// marketplace adds and plugin installs inside it). Plan.Lines renders them
// quoted for a shell. The configuration file is changed only when
// Options.Persist is set, and then through config.Save (atomic, 0600).
//
// # Use
//
// List and Describe report accounts, Env returns the environment additions for
// a child process (CLAUDE_CONFIG_DIR=<dir>), and Remove drops the config entry
// only: the directory, with its login and history, is left alone and Removal
// says so.
//
// An existing CLAUDE_CONFIG_DIR in the environment is never overridden here;
// that precedence belongs to config.ResolveAccount.
package account
