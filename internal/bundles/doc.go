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
// plugins/dependencies, "Declare dependencies"). Compile writes a plugin of
// another marketplace as an object with its keys sorted, and it sorts the
// array by "name@marketplace":
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
// another marketplace"). BundleInfo.CrossMarketplaces reports them. There is
// no version (the commit SHA is the version), no description and no
// marketplace key at the top level. Output is 2-space JSON, UTF-8, LF line
// endings and a single trailing newline, on every OS.
//
// # The bundles directory is generated
//
// There is no marker inside a bundle, so the package treats the whole
// bundles/ directory as generated output. Check reports as drift every path
// that is not the plugin.json of a wanted bundle, whether or not it looks
// generated. WriteOptions.PruneStale removes exactly those paths, deepest
// first.
//
// # Line endings
//
// Check compares bytes. When a Windows checkout converts the LF of a file to
// CRLF, Check reports the file as Modified (the diff says that only line
// endings differ). The data repo must keep LF with a .gitattributes rule
// "*.json text eol=lf" (the starter template ships one). Otherwise
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
