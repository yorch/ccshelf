package ui

import "context"

// Option is one choice in a Question.
type Option struct {
	// Label is the text shown and matched when filtering.
	Label string
	// Detail is an optional second column (a description). It is shown but not
	// matched.
	Detail string
	// Value is a stable identifier for the caller; it is also matched when
	// filtering. It may be empty.
	Value string
}

// Question describes a Select or MultiSelect prompt.
type Question struct {
	// Title is the question text.
	Title string
	// Options are the choices. At least one is required.
	Options []Option
	// Default is the index chosen by an empty answer in Select. A negative
	// value means there is no default. Note that the zero value selects the
	// first option; set -1 when the first option must not be the default. A
	// default must never be less safe than the flag default (R6).
	Default int
	// Filterable allows typing a substring of a label or value instead of a
	// number.
	Filterable bool
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
	// Confirm asks a yes or no question; an empty answer returns def.
	Confirm(ctx context.Context, text string, def bool) (bool, error)
	// Input asks for a line of text; an empty answer returns def. validate, when
	// not nil, may reject a value: the user is asked again with its message.
	Input(ctx context.Context, text, def string, validate func(string) error) (string, error)
	// Secret asks for a value without echoing it. The value must never be
	// recorded in a Recorder, logged or written to disk.
	Secret(ctx context.Context, text string) (string, error)
}
