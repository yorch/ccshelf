package ui

import (
	"context"
	"strings"
)

// Option is one choice in a Question.
type Option struct {
	// Label is the text shown and matched when filtering.
	Label string
	// Detail is an optional second column (a description). It is shown but not
	// matched.
	Detail string
	// Value is a stable identifier for the caller. It is also matched when
	// filtering. It may be empty.
	Value string
}

// Question describes a Select or MultiSelect prompt.
type Question struct {
	// Title is the question text.
	Title string
	// Options are the choices. At least one is required.
	Options []Option
	// Default is the index chosen by an empty answer in Select. It only
	// counts when HasDefault is true, so the zero value of Question has no
	// default: a missing field can never select the first option. A default
	// must never be less safe than the flag default (R6).
	Default int
	// HasDefault makes Default effective. A Default that is negative or out of
	// range is ignored.
	HasDefault bool
	// Filterable allows typing a substring of a label or value instead of a
	// number.
	Filterable bool
}

// defaultIndex returns the effective default option, if any.
func (q Question) defaultIndex() (int, bool) {
	if q.HasDefault && q.Default >= 0 && q.Default < len(q.Options) {
		return q.Default, true
	}
	return -1, false
}

// ConfirmRisky asks for confirmation of a risky or irreversible action. It has
// no default and no y/n shortcut: the user must type the word "yes" (exactly,
// ignoring case and surrounding space). Anything else, including an empty
// answer, is "no". It uses only Prompter.Input, so it works with every
// Prompter, and a non-interactive Prompter returns a *MissingFlagError instead
// of answering. Use it, not Confirm with def=true, whenever the safe answer is
// not obviously "no" (R6 rule 5).
func ConfirmRisky(ctx context.Context, p Prompter, text string) (bool, error) {
	line, err := p.Input(ctx, SanitizeLine(text)+" Type yes to confirm", "", nil)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(line), "yes"), nil
}

// Prompter is the thin prompt layer flows use (R6). All prompts go to the
// error stream so results on stdout stay clean. Implementations are not safe
// for concurrent use.
type Prompter interface {
	// Select asks the user to pick one option and returns its index in
	// q.Options.
	Select(ctx context.Context, q Question) (int, error)
	// MultiSelect asks the user to pick any number of options and returns their
	// indexes in ascending order (possibly none).
	MultiSelect(ctx context.Context, q Question) ([]int, error)
	// Confirm asks a yes or no question. An empty answer returns def. Pass
	// def=true only when yes is the safe, reversible answer. For anything risky,
	// use ConfirmRisky.
	Confirm(ctx context.Context, text string, def bool) (bool, error)
	// Input asks for a line of text. An empty answer returns def. validate, when
	// not nil, may reject a value: the user is asked again with its message.
	Input(ctx context.Context, text, def string, validate func(string) error) (string, error)
	// Secret asks for a value without echoing it. The value must never be
	// recorded in a Recorder, logged or written to disk.
	Secret(ctx context.Context, text string) (string, error)
}
