// Package sidecar reads the catalog-only metadata of plugins: one TOML sidecar
// file per plugin (catalog/plugins/<name>.toml), or, in single-file mode,
// the free-form "metadata" object of each marketplace entry, and the
// taxonomy file (catalog/taxonomy.toml).
//
// Parsing is strict about shape (unknown keys and wrong types are problems)
// and does not judge values: whether a status is in the enum, a date is
// valid or a docs URL is http(s) is the lint's job, so that one report lists
// everything. The package returns a broken sidecar as a Problem, and a broken
// sidecar never stops the load of the others.
package sidecar
