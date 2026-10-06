// Package safepath confines file access to a repository root.
//
// The catalog and the org configuration read files from an org data repo whose
// content is untrusted (any contributor can open a pull request). Every path
// that comes from such a file, and every path the tool derives from a plugin
// name, must stay inside the repo root: no absolute paths, no ".." segments
// and no symlink that leads out of the root. Reads also cap the file size and
// accept only regular files.
//
// Resolution and open are two steps, so a concurrent writer inside the repo
// could swap a path between them. The tools using this package run on a
// checked-out tree in CI or on a developer's clone, where that is out of scope.
package safepath
