// Package cache manages the launcher's private cache directory and the
// content-addressed files in it (security requirement SR4).
//
// # Contract
//
//   - [Dir] returns the cache directory, creating it with mode 0700. It
//     refuses a path that is a symlink or is owned by another user. A
//     directory the user owns whose mode is wider than 0700 is repaired with
//     chmod 0700 (on a no-follow handle) and accepted.
//   - [Write] stores bytes under the name <prefix>-<32 hex of sha256>.<ext>
//     (128 bits of the digest).
//     The file is created through a temporary file with O_EXCL and mode 0600,
//     fsynced, then renamed. An existing target is re-read and compared with
//     the content: identical bytes are reused, anything else returns
//     [ErrTampered]. Symlinks are never followed (O_NOFOLLOW on Unix,
//     FILE_FLAG_OPEN_REPARSE_POINT plus an attribute re-check on the open
//     handle on Windows).
//   - [WriteReplace] and [ReadFile] hold small mutable files (for example a
//     memoized plugin list) under the same rules, with replace-by-rename.
//   - [GC] removes only files whose names match what this package creates,
//     older than a maximum age and not reported as in use.
//   - [WriteDir] holds a content-addressed directory with one file. It checks
//     the directory before every reuse and rebuilds it when it differs.
//   - [Prune] and [PruneDir] do the same and also remove git checkouts
//     (git/<url key>/<commit>) and [WriteDir] directories that have not been
//     used for the maximum age, except the ones the caller reports as pinned.
//   - Errors about a file that cannot be trusted name the file to delete.
//
// # Windows (untested on real Windows)
//
// The Windows code in nofollow_windows.go compiles and vets for windows/amd64
// and windows/arm64 but has never been run on Windows. It gives the cache
// directory a protected DACL granting only the current user (inherited by
// new files), creates files with the same owner-only descriptor, verifies the
// directory owner is the current user (or the token's default owner when
// elevated), and refuses reparse points. Treat it as unverified until a
// Windows CI run exercises it.
//
// Callers must never log file contents. Everything is safe for concurrent use
// by goroutines and by processes.
package cache
