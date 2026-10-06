// Package ui is the terminal layer of ccshelf: how a command decides whether it
// may prompt, how it asks, how it prints, and how it exits (requirement R6).
//
// Flags are the contract and prompts are an optional front-end over the same
// command definitions. This package gives every command the same behavior.
//
// # End to end use in the CLI
//
//  1. At startup build the streams and the mode once:
//
//     streams := ui.StdStreams()
//     mode := ui.DetectMode(os.Getenv, os.Stdin, os.Stdout, ui.ModeFlags{
//     NoInteractive: noInteractive, NoColor: noColor, Plain: plain, JSON: asJSON,
//     ConfigColor: cfg.UI.Color, ConfigInteractive: cfg.UI.Interactive,
//     })
//
//     Interactive is true only when stdin and stdout are terminals,
//     --no-interactive is absent, CI is unset and TERM is not dumb (rule 2).
//     Color follows NO_COLOR, --no-color, TERM=dumb and whether stdout is a
//     terminal (rule 7). On Windows the console's virtual-terminal mode is
//     enabled best effort; when that fails the mode falls back to plain output
//     without color.
//
//  2. Pick the prompter from the mode, and ask only for values that no flag
//     supplied (rule 3):
//
//     var p ui.Prompter = ui.NonInteractive{}
//     if mode.Interactive {
//     p = ui.NewTTY(streams, mode) // or ui.NewScripted(...) in tests
//     }
//
//     NonInteractive returns *MissingFlagError for every prompt, so a script
//     gets an exit code 2 error that names the missing flags instead of a hang.
//     A flow that knows the flag can use ui.NonInteractive{Flag: "--from"} or
//     return ui.MissingFlags(hint, "--plugin", "--skill-off") itself before
//     prompting. Prompts and menus are written to Streams.Err so Streams.Out
//     stays clean for results. The launcher prompts only before it starts
//     claude and never leaves a prompt open afterwards (rule 6); the TTY
//     prompter uses no raw mode, so there is no terminal state to restore.
//
//  3. Record what the user chose and finish with the equivalent command (rule 4):
//
//     rec := ui.NewRecorder("new", name)
//     rec.Flag("--from", parent)
//     rec.Flag("--plugin", plugin)
//     rec.Print(streams.Err, runtime.GOOS)
//
//     Equivalent quotes for the user's shell (PowerShell on Windows, POSIX
//     elsewhere; QuoteCmd exists for cmd.exe) and refuses control characters.
//     Never record a secret.
//
//  4. Print results with Table and WriteJSON. Table sanitizes cells, truncates
//     to mode.Width counting wide Unicode characters, and uses ASCII in plain
//     mode. WriteJSON writes {"version":1,"kind":...,"data":...}; within a
//     version fields may be added but never removed, renamed or retyped.
//     Status prefixes ok:, warn: or error: so color is never the only signal.
//     Pass any text that came from a profile or catalog through Sanitize, and
//     print environment maps with RedactEnv (SR4).
//
//  5. At the top of main, map errors to exit codes and report them:
//
//     if err != nil {
//     ui.Report(streams.Err, err, mode)
//     os.Exit(ui.CodeOf(err))
//     }
//
//     Wrap errors with ui.Usage, ui.Policy, ui.TrustRequired or ui.Failure to
//     choose the code; CodeOf also maps *MissingFlagError to 2 and
//     ErrAborted and context.Canceled to 130.
//
// # Exit codes (rule 8)
//
//	0   ExitOK           success
//	1   ExitFailure      failure
//	2   ExitUsage        usage error, including a value that was needed but
//	                     could not be prompted for
//	3   ExitPolicy       blocked by managed policy
//	4   ExitTrust        the profile needs trust (fail closed, SR2)
//	130 ExitInterrupted  interrupted (Ctrl+C, Ctrl+D at a prompt)
//
// # Safety rules built in
//
// A prompt is never answered with a default in a non-interactive run
// (NonInteractive.Confirm does not return its default). A Question's default
// must not be less safe than the flag default, and trust is never accepted by a
// default or by --yes (rule 5). Secret reads without echo from a terminal.
// Output that includes values from other repositories is stripped of control
// characters.
package ui
