// Package schema embeds the handwritten JSON Schema (draft 2020-12) files for
// ccshelf's three TOML formats: profile manifests, the user config and the MCP
// registry. All schemas set additionalProperties to false, mirroring the strict
// decoding in internal/profile and internal/config.
//
// The files are for editors and CI linters. The Go structs remain the source of
// truth, and this package's tests fail when a struct field, a tag or an
// enumeration drifts from its schema. There is no JSON Schema library in the
// module, so the tests also use a small checker (keywords type, enum, pattern,
// properties, additionalProperties, required, items, uniqueItems, minLength,
// maxLength, propertyNames, $ref to local $defs) against valid and invalid
// examples.
package schema
