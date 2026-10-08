// Package doctor analyzes an org's catalog, profiles and installed plugins
// and returns findings. It is a pure analysis package: it reads nothing from
// disk, runs no process and makes no network call. The caller (the CLI)
// gathers the input, including policy findings from the policy package, and
// prints the report.
//
// # Checks
//
// Each check has a stable code, DOC001 to DOC012. Rules lists the code,
// severity and description of every check (the checks table in checks.go).
//
// Policy findings supplied in Input.Policy are copied into the report
// unchanged. Token cost hints are out of scope: no estimates are made.
//
// A check whose input is missing is skipped and named in Report.Skipped, so
// "no findings" is never confused with "not checked".
//
// # Output
//
// Report.JSON uses the same finding fields as the lint report (severity,
// code, message, plugin, hint) plus check and profile. Text taken from
// untrusted files is sanitized (control characters become spaces) before it
// enters a message, and shell commands and TOML snippets in hints are
// produced only from values that pass a strict character check.
package doctor
