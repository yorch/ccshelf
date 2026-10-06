// Package doctor analyzes an org's catalog, profiles and installed plugins
// and returns findings. It is a pure analysis package: it reads nothing from
// disk, runs no process and makes no network call. The caller (the CLI)
// gathers the input, including policy findings from the policy package, and
// prints the report.
//
// # Checks
//
// Each check has a stable code. The code, severity and one-line description
// of every check are listed by Rules.
//
//	DOC001 overlap            plugins that share at least 60% of their tags
//	                          (Jaccard: shared tags over all tags of the two,
//	                          with at least two shared) or that list each other
//	                          in overlaps_with
//	DOC002 unused             plugins in no profile (protected plugins
//	                          excepted) and, with usage data, with no
//	                          skill_activated events in the window; reported
//	                          as unconfirmed when usage names are redacted
//	DOC003 deprecated-in-use  profiles that include a deprecated plugin, with
//	                          the replacement
//	DOC004 review             review_by in the past or missing
//	DOC005 owner              plugin without an owner
//	DOC006 standalone-skills  standalone skills no profile mentions, with the
//	                          exact [skills] off snippet to add
//	DOC007 missing-upstream   installed plugins whose marketplace is in the
//	                          catalog but no longer lists them
//	DOC008 not-installed      profile includes a plugin that is not installed,
//	                          with the install command
//	DOC009 forced-by-policy   plugins that managed policy forces on (cannot be
//	                          masked by any profile)
//	DOC010 platform-review    plugins with hooks, MCP or LSP servers that the
//	                          CODEOWNERS rules do not route to platform review
//	                          (read from the lint report; skipped, with the
//	                          reason, when lint.platform_owners is empty)
//	DOC011 protected-masked   a profile that excludes a protected plugin; the
//	                          launcher keeps it on, so the exclude is moot
//	DOC012 protected-mcp      a profile that hides claude.ai connectors or sets
//	                          mcp.strict while the org protects an MCP server
//	                          that would go with them (the launcher refuses it)
//
// Policy findings supplied in Input.Policy are copied into the report
// unchanged. Token cost hints are out of scope: no estimates are made.
//
// A check whose input is missing is skipped and named in Report.Skipped, so
// "no findings" is never confused with "not checked".
//
// # Output
//
// Report.Text renders findings grouped by check; Report.JSON renders the data
// payload {"summary":...,"findings":[...],"skipped":[...]} (the CLI wraps it
// in the common {"version","kind","data"} envelope) with the same finding fields as the lint report (severity, code, message,
// plugin, hint) plus check and profile. Text taken from untrusted files is
// sanitized (control characters become spaces) before it enters a message, and
// shell commands and TOML snippets in hints are produced only from values that
// pass a strict character check.
package doctor
