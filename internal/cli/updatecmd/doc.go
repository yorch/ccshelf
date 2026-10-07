// Package updatecmd implements the "ccshelf update" command and the opt-in
// automatic update hooks around the other commands. The mechanics (discovery,
// download, verification, replacement) live in internal/update; this package
// is the command line: flags, prompts, output, exit codes and the wiring of
// the hermetic Context into an update.Updater.
package updatecmd
