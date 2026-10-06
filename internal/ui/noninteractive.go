package ui

import "context"

// NonInteractive is the Prompter used when prompting is not allowed (R6 rule
// 2). Every call fails with a *MissingFlagError, which maps to exit code 2, so
// a script gets an error that names what to pass instead of a hang.
type NonInteractive struct {
	// Flag optionally names the flag that supplies the value being asked for.
	// A flow that knows the flag can wrap the prompter per question.
	Flag string
}

var _ Prompter = NonInteractive{}

func (n NonInteractive) missing(hint string) error {
	return &MissingFlagError{Flag: n.Flag, Hint: Sanitize(hint)}
}

// Select returns a *MissingFlagError.
func (n NonInteractive) Select(_ context.Context, q Question) (int, error) {
	return -1, n.missing(q.Title)
}

// MultiSelect returns a *MissingFlagError.
func (n NonInteractive) MultiSelect(_ context.Context, q Question) ([]int, error) {
	return nil, n.missing(q.Title)
}

// Confirm returns a *MissingFlagError. It never returns def: a non-interactive
// run must not answer a confirmation by itself.
func (n NonInteractive) Confirm(_ context.Context, text string, _ bool) (bool, error) {
	return false, n.missing(text)
}

// Input returns a *MissingFlagError.
func (n NonInteractive) Input(_ context.Context, text, _ string, _ func(string) error) (string, error) {
	return "", n.missing(text)
}

// Secret returns a *MissingFlagError.
func (n NonInteractive) Secret(_ context.Context, text string) (string, error) {
	return "", n.missing(text)
}
