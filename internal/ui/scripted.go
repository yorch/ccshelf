package ui

import (
	"context"
	"fmt"
	"sort"
)

// Scripted is a Prompter for tests that answers from a queue.
//
// Answer types by prompt: Select takes an int (index); MultiSelect takes
// []int; Confirm takes a bool; Input and Secret take a string. Any answer may
// instead be an error, which the prompt returns (for example ErrAborted). A
// prompt of the wrong kind, or a prompt with the queue empty, is an error, and
// Done reports answers that were never used.
type Scripted struct {
	answers []any
	// Asked lists the prompt titles seen so far, in order, for assertions.
	Asked []string
	err   error
}

var _ Prompter = (*Scripted)(nil)

// NewScripted returns a Scripted prompter with the given answers.
func NewScripted(answers ...any) *Scripted {
	return &Scripted{answers: append([]any(nil), answers...)}
}

// Done returns an error if a prompt was unexpected or answers are left over.
func (s *Scripted) Done() error {
	if s.err != nil {
		return s.err
	}
	if len(s.answers) > 0 {
		return fmt.Errorf("scripted prompter: %d unused answer(s), next is %v", len(s.answers), s.answers[0])
	}
	return nil
}

// next pops the next answer, recording a sticky error when none is left.
func (s *Scripted) next(kind, title string) (any, error) {
	s.Asked = append(s.Asked, title)
	if len(s.answers) == 0 {
		err := fmt.Errorf("scripted prompter: unexpected %s prompt %q: no answer left", kind, title)
		if s.err == nil {
			s.err = err
		}
		return nil, err
	}
	a := s.answers[0]
	s.answers = s.answers[1:]
	if e, ok := a.(error); ok {
		return nil, e
	}
	return a, nil
}

func (s *Scripted) wrong(kind, title string, a any) error {
	err := fmt.Errorf("scripted prompter: %s prompt %q got an answer of type %T", kind, title, a)
	if s.err == nil {
		s.err = err
	}
	return err
}

// Select implements Prompter.
func (s *Scripted) Select(ctx context.Context, q Question) (int, error) {
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	a, err := s.next("select", q.Title)
	if err != nil {
		return -1, err
	}
	i, ok := a.(int)
	if !ok {
		return -1, s.wrong("select", q.Title, a)
	}
	if i < 0 || i >= len(q.Options) {
		return -1, fmt.Errorf("scripted prompter: select %q: index %d out of range (%d options)", q.Title, i, len(q.Options))
	}
	return i, nil
}

// MultiSelect implements Prompter.
func (s *Scripted) MultiSelect(ctx context.Context, q Question) ([]int, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a, err := s.next("multi-select", q.Title)
	if err != nil {
		return nil, err
	}
	idx, ok := a.([]int)
	if !ok {
		return nil, s.wrong("multi-select", q.Title, a)
	}
	out := append([]int(nil), idx...)
	sort.Ints(out)
	for _, i := range out {
		if i < 0 || i >= len(q.Options) {
			return nil, fmt.Errorf("scripted prompter: multi-select %q: index %d out of range (%d options)", q.Title, i, len(q.Options))
		}
	}
	return out, nil
}

// Confirm implements Prompter.
func (s *Scripted) Confirm(ctx context.Context, text string, _ bool) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	a, err := s.next("confirm", text)
	if err != nil {
		return false, err
	}
	b, ok := a.(bool)
	if !ok {
		return false, s.wrong("confirm", text, a)
	}
	return b, nil
}

// Input implements Prompter. The validator is applied to the scripted answer.
func (s *Scripted) Input(ctx context.Context, text, _ string, validate func(string) error) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	a, err := s.next("input", text)
	if err != nil {
		return "", err
	}
	v, ok := a.(string)
	if !ok {
		return "", s.wrong("input", text, a)
	}
	if validate != nil {
		if verr := validate(v); verr != nil {
			return "", fmt.Errorf("scripted prompter: input %q rejected: %w", text, verr)
		}
	}
	return v, nil
}

// Secret implements Prompter.
func (s *Scripted) Secret(ctx context.Context, text string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	a, err := s.next("secret", text)
	if err != nil {
		return "", err
	}
	v, ok := a.(string)
	if !ok {
		return "", s.wrong("secret", text, a)
	}
	return v, nil
}
