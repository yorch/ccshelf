// Package catalog builds the plugin catalog of an org data repo: a merged,
// sanitized, deterministic view of the marketplace entries, their sidecar
// metadata, what inspecting the plugin directories found, and optional git
// history, plus renderers (catalog.json, CATALOG.md) and a text search.
//
// The static site lives in package catalog/site, the lint in catalog/lint,
// and the readers in internal/marketplace, internal/orgconfig and the
// sub-packages sidecar, codeowners, gitdata and safepath.
//
// Everything in a Catalog is plain text from untrusted files (any
// contributor can open a pull request). Build therefore strips control
// characters, caps lengths and keeps links only when they are absolute http
// or https URLs. The renderers escape again for their own medium: Markdown
// escaping for CATALOG.md, and the site builds its DOM with textContent only.
//
// Output is deterministic: sorted, no absolute paths, and no timestamp
// unless Options.Now is set. Another package parses the profiles, so the
// caller passes them in as []ProfileInfo.
package catalog
