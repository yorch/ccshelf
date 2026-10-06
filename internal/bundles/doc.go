// Package bundles compiles resolved profiles into generated profile bundles:
// dependency-only plugins that let a profile's plugins install natively from
// the org's marketplace (decision D-17).
//
// # Canonical format
//
// Compile writes bundles/profile-<name>/.claude-plugin/plugin.json. When every
// plugin lives in the marketplace that hosts the bundle (Input.Marketplace)
// the file is, byte for byte like examples/org-data-repo/bundles/:
//
//	{
//	  "dependencies": [
//	    "design-kit",
//	    "docs-writer"
//	  ],
//	  "name": "profile-frontend"
//	}
//
// A bare name resolves in the marketplace of the declaring plugin (docs:
// plugins/dependencies, "Declare dependencies"). A plugin of another
// marketplace is written as an object with its keys sorted, and the array is
// sorted by "name@marketplace":
//
//	{
//	  "dependencies": [
//	    "alpha",
//	    {
//	      "marketplace": "shared",
//	      "name": "alpha"
//	    },
//	    "own"
//	  ],
//	  "name": "profile-x"
//	}
//
// Claude Code installs such a dependency only if the hosting (root)
// marketplace lists the other one in allowCrossMarketplaceDependenciesOn in
// its marketplace.json (docs: plugins/dependencies, "Depend on a plugin from
// another marketplace"); BundleInfo.CrossMarketplaces reports which ones a
// bundle needs so the CLI can warn or lint. There is no version (the commit
// SHA is the version), no description and no marketplace key at the top
// level. Output is 2-space JSON, UTF-8, LF line endings and a single
// trailing newline, on every OS.
//
// Profiles that resolve to no plugins (abstract ones such as a shared base)
// are not errors: Compile skips them and lists them in Result.Skipped.
//
// # The bundles directory is generated
//
// There is no marker inside a bundle, so the whole bundles/ directory is
// treated as generated output. Check reports as drift every path that is not
// profile-<name>/.claude-plugin/plugin.json of a wanted bundle: extra files
// inside a wanted bundle (hooks/, .mcp.json...), profile-* directories that
// no profile produces (Drift.Stale, every path inside them) and anything
// else directly under bundles/ (Drift.Extra), whether or not it looks
// generated. Write with PruneStale removes exactly those paths, deepest
// first. If any of them is a symbolic link or special file it refuses before
// removing anything. It never follows symlinks and errors when bundles itself
// is one.
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
// not secrets. Pruning is opt-in (WriteOptions.PruneStale).
package bundles
