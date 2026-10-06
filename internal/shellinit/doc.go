// Package shellinit generates the shell integration printed by
// "ccshelf shell-init <shell>": one small function per profile that runs
// "ccshelf run <profile>" with the rest of the command line passed through.
//
// # Naming rule
//
// The function for profile "sre" is named "cs-sre" (Prefix plus the profile
// name). Profile names must match ^[a-z0-9][a-z0-9-]{0,62}$; anything else,
// including every name that holds a quote, space, "$", backtick, ";" or a
// path separator, is rejected before any text is generated, so a hostile
// profile name can never reach a shell. The ccshelf executable is always
// quoted for the target shell and must be an absolute path without control
// characters, so a function never depends on PATH or the working directory.
//
// # Loading
//
//	bash, zsh   eval "$(ccshelf shell-init zsh)"
//	fish        ccshelf shell-init fish | source
//	pwsh        ccshelf shell-init pwsh | Out-String | Invoke-Expression
//	cmd         doskey macros: ccshelf shell-init cmd > %TEMP%\ccshelf-init.cmd
//	            && call %TEMP%\ccshelf-init.cmd; or persistent .cmd shims
//	            written by WriteCmdShims into a directory on PATH
//
// The eval above runs only the text ccshelf generated from validated profile
// names and a quoted executable path; the generated text never evaluates its
// arguments again (no eval inside the functions).
//
// # cmd.exe
//
// cmd has no functions, so Generate("cmd") emits doskey macros, which only
// exist in the interactive session that loaded them, and WriteCmdShims writes
// "cs-<profile>.cmd" batch files for use from any cmd or script. The
// executable path may not contain a double quote, "%" or "$" for cmd (doskey
// treats "$" specially and "%" cannot be escaped reliably).
package shellinit
