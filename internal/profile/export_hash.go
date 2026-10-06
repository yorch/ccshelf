package profile

// HashItems returns the closure hash of items exactly as Resolve computes it.
// The trust package uses it to confirm that a Closure's Hash matches its Items
// before a trust decision is made or recorded. The items must already be in
// the canonical order Resolve produces (sorted by kind, name and digest).
func HashItems(items []ClosureItem) string { return hashItems(items) }
