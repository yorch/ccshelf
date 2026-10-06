// Package analytics reads optional plugin usage data. There is no telemetry
// in ccshelf: nothing is ever sent about the user, and this package makes a
// network call only when the user supplies an admin key and asks for it.
//
// # Two sources
//
// Fetch calls the Claude Enterprise Analytics API, GET
// {BaseURL}/v1/organizations/analytics/plugins. The admin key is read from
// the environment variable named by Options.KeyEnv (default
// CCSHELF_ANALYTICS_KEY) at call time. It is never stored, logged or put in
// argv, only sent in the x-api-key header, and it is redacted from every
// error text. The base URL must be https (Options.AllowInsecure exists for
// httptest servers), redirects to another host or scheme are refused, the
// response size is capped at 8 MiB per page, and every request has a timeout.
//
// ParseOTelJSONL reads a file the user exported from their own OpenTelemetry
// pipeline with claude_code.skill_activated, plugin_loaded and
// plugin_installed events. Claude Code redacts plugin and skill names unless
// OTEL_LOG_TOOL_DETAILS=1 is set; redacted events are counted in
// Usage.Redacted and cannot be attributed to a plugin.
//
// # API shape (verified against the public reference)
//
// The shape below was read from
// https://platform.claude.com/docs/en/api/admin/analytics/plugins/list (a
// read-only GET of the public page; the API itself was never called).
//
// Request: query starting_date (inclusive) and ending_date (exclusive), both
// YYYY-MM-DD, at most 366 days apart and no earlier than 2026-01-01; limit
// (1 to 1000); page (the opaque next_page cursor); filter[] entries such as
// product:claude_code. Headers: x-api-key, anthropic-version: 2023-06-01. The
// key needs the read:analytics scope.
//
// Response: {"data":[{"plugin_name", "plugin_id" (may be null, for example
// for third-party plugins), "install_count" (distinct users, may be null),
// "invocation_count", "distinct_user_count", "product", ...}],
// "next_page": string or null}. The plugin_name "third-party" is an aggregate
// bucket, not a plugin; it is kept under that name.
//
// Assumptions: in range mode there is one row per plugin (and per product
// when grouped); if several rows share a plugin they are summed. A row's
// identity is plugin_id when present, else plugin_name. install_count is a
// distinct-user count, so summing rows over products can overstate it;
// ccshelf only tests it against zero. Decoding is tolerant: unknown fields
// are ignored and null counts are zero.
//
// # Matching plugins
//
// Usage.PerPlugin is keyed by plugin id (name@marketplace) when known and by
// plain name otherwise. Use Usage.Lookup to find a plugin by either form.
package analytics
