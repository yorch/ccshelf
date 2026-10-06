// Package cache manages the launcher's private cache directory and the
// content-addressed files in it (security requirement SR4).
//
// # Contract
//
//   - [Dir] returns the cache directory, creating it with mode 0700. It
//     refuses a path that is a symlink, is owned by another user (Unix), or is
//     writable by group or others. On Windows the owner-only protection comes
//     from the default ACL inherited from %LOCALAPPDATA%, which is private to
//     the user; the package does not rewrite DACLs, and the ownership and
//     mode checks are skipped there.
//   - [Write] stores bytes under the name <prefix>-<16 hex of sha256>.<ext>.
//     The file is created through a temporary file with O_EXCL and mode 0600,
//     fsynced, then renamed. An existing target is re-read and compared with
//     the content: identical bytes are reused, anything else returns
//     [ErrTampered]. Symlinks are never followed (O_NOFOLLOW on Unix, an Lstat
//     check on Windows).
//   - [WriteReplace] and [ReadFile] hold small mutable files (for example a
//     memoized plugin list) under the same rules, with replace-by-rename.
//   - [GC] removes only files whose names match what this package creates,
//     older than a maximum age and not reported as in use.
//
// Callers must never log file contents. Everything is safe for concurrent use
// by goroutines and by processes.
package cache
