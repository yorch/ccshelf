// Package ui is the terminal layer of ccshelf: how a command decides whether it
// may prompt, how it asks, how it prints, and how it exits (requirement R6).
//
// Flags are the contract and prompts are an optional front-end over the same
// command definitions. This package gives every command the same behavior.
// The rule numbers below refer to docs/design/cli.md, "Interaction model
// (R6)".
//
// # End to end use in the CLI
//
//  1. At startup build the streams and the mode once (DetectMode, rules 2
//     and 7):
//
//     streams := ui.StdStreams()
//     mode := ui.DetectMode(os.Getenv, os.Stdin, os.Stdout, ui.ModeFlags{
//     NoInteractive: noInteractive, NoColor: noColor, Plain: plain, JSON: asJSON,
//     ConfigColor: cfg.UI.Color, ConfigInteractive: cfg.UI.Interactive,
//     })
//
//     On Windows the console's virtual-terminal mode is enabled best effort.
//     When that fails, the mode falls back to plain output without color.
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
//     Prompts go to Streams.Err so Streams.Out stays clean for results. The
//     launcher prompts only before it starts claude (rule 6). The TTY prompter
//     restores raw input and scoped output before every keyboard prompt
//     returns (D-46). Secret restores the terminal state on every exit,
//     including cancellation and interrupts.
//
//  3. Record what the user chose and finish with the equivalent command
//     (Recorder, rule 4). Never record a secret.
//
//  4. Print results with Table, WriteJSON and Status. Within a JSON version
//     fields may be added but never removed, renamed or retyped. Pass any
//     text that came from a profile or catalog through Sanitize, and print
//     environment maps with RedactEnv (SR4).
//
//  5. At the top of main, map errors to exit codes and report them:
//
//     if err != nil {
//     ui.Report(streams.Err, err, mode)
//     os.Exit(ui.CodeOf(err))
//     }
//
//     Wrap errors with ui.Usage, ui.Policy, ui.TrustRequired or ui.Failure to
//     choose the code. The Exit* constants list the codes (rule 8).
//
// # Safety rules built in
//
// A prompt is never answered with a default in a non-interactive run. A
// Question's default must not be less safe than the flag default, and trust
// is never accepted by a default or by --yes (rule 5). Secret reads without
// echo from a terminal. Output that includes values from other repositories
// is stripped of control characters.
package ui
