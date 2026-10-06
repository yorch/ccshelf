// Package bundles compiles resolved profiles into generated profile bundles:
// dependency-only plugins that let a profile's plugins install natively from
// the org's marketplace (decision D-17).
//
// # Canonical format
//
// Compile writes bundles/profile-<name>/.claude-plugin/plugin.json with
// exactly this shape, which matches examples/org-data-repo/bundles/ byte for
// byte:
//
//	{
//	  "dependencies": [
//	    "design-kit",
//	    "docs-writer"
//	  ],
//	  "name": "profile-frontend"
//	}
//
// Two keys only, dependencies then name (sorted). There is no version (the
// commit SHA is the version), no description and no marketplace: dependencies
// are bare plugin names, because the bundle is served from the marketplace its
// plugins live in. The input plugin ids (name@marketplace) are validated and
// the marketplace suffix is dropped; two ids with the same name from different
// marketplaces are rejected as ambiguous. Output is 2-space JSON, UTF-8, LF
// line endings and a single trailing newline, on every OS.
//
// The task text sketched a different format (a description marker and
// {name, marketplace} dependency objects); the committed template wins. The
// description marker therefore does not exist, so Write only prunes a stale
// bundle directory when it is recognizably generated: it contains nothing but
// .claude-plugin/plugin.json, and that file is byte-identical to what Compile
// would produce for its own name and dependencies.
//
// # Line endings
//
// Check compares bytes. A file whose LF was converted to CRLF by a Windows
// checkout is reported as Modified (the diff says that only line endings
// differ). The data repo must keep LF: the starter template should ship a
// .gitattributes with "*.json text eol=lf" (this tool repo does), otherwise
// core.autocrlf on Windows makes the drift check fail on a clean checkout.
//
// # Safety
//
// Write only creates or replaces files below <root>/bundles, never follows a
// symbolic link (a symlinked bundles directory or intermediate directory is an
// error), refuses paths that are absolute, contain "..", backslashes or drive
// colons, and replaces files atomically (temporary file then rename). Files
// are 0644 and directories 0755 because they are public repository content,
// not secrets. Pruning is opt-in (WriteOptions.PruneStale) and removes only
// files it can prove were generated, with os.Remove so a directory holding
// anything else is left alone.
package bundles
