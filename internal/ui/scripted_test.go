package ui

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestScripted(t *testing.T) {
	ctx := context.Background()
	q := Question{Title: "pick", Options: []Option{{Label: "a"}, {Label: "b"}}}
	s := NewScripted(1, []int{1, 0}, true, "name", "tok")
	if i, err := s.Select(ctx, q); err != nil || i != 1 {
		t.Fatalf("select: %d %v", i, err)
	}
	if m, err := s.MultiSelect(ctx, q); err != nil || !reflect.DeepEqual(m, []int{0, 1}) {
		t.Fatalf("multi: %v %v", m, err)
	}
	if b, err := s.Confirm(ctx, "ok?", false); err != nil || !b {
		t.Fatalf("confirm: %v %v", b, err)
	}
	if v, err := s.Input(ctx, "n", "", func(string) error { return nil }); err != nil || v != "name" {
		t.Fatalf("input: %v %v", v, err)
	}
	if v, err := s.Secret(ctx, "t"); err != nil || v != "tok" {
		t.Fatalf("secret: %v %v", v, err)
	}
	if err := s.Done(); err != nil {
		t.Errorf("Done: %v", err)
	}
	if !reflect.DeepEqual(s.Asked, []string{"pick", "pick", "ok?", "n", "t"}) {
		t.Errorf("Asked = %v", s.Asked)
	}
}

func TestScriptedErrors(t *testing.T) {
	ctx := context.Background()
	q := Question{Title: "pick", Options: []Option{{Label: "a"}}}

	s := NewScripted()
	if _, err := s.Confirm(ctx, "x", true); err == nil {
		t.Error("empty queue must fail")
	}
	if s.Done() == nil {
		t.Error("Done must report the unexpected prompt")
	}

	s = NewScripted("str")
	if _, err := s.Select(ctx, q); err == nil {
		t.Error("wrong type must fail")
	}
	if s.Done() == nil {
		t.Error("Done must report wrong type")
	}

	s = NewScripted(1, 2)
	_, _ = s.Select(ctx, Question{Title: "q", Options: []Option{{Label: "a"}, {Label: "b"}}})
	if err := s.Done(); err == nil {
		t.Error("leftover answers must be reported")
	}

	s = NewScripted(5)
	if _, err := s.Select(ctx, q); err == nil {
		t.Error("out of range index must fail")
	}
	s = NewScripted([]int{3})
	if _, err := s.MultiSelect(ctx, q); err == nil {
		t.Error("out of range multi index must fail")
	}

	s = NewScripted(ErrAborted)
	if _, err := s.Confirm(ctx, "x", false); !errors.Is(err, ErrAborted) {
		t.Errorf("error answer: %v", err)
	}

	bad := errors.New("nope")
	s = NewScripted("x")
	if _, err := s.Input(ctx, "n", "", func(string) error { return bad }); !errors.Is(err, bad) {
		t.Errorf("validator error not wrapped: %v", err)
	}

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	s = NewScripted(1, []int{0}, true, "a", "b")
	if _, err := s.Select(cctx, q); !errors.Is(err, context.Canceled) {
		t.Error("select must honor ctx")
	}
	if _, err := s.MultiSelect(cctx, q); !errors.Is(err, context.Canceled) {
		t.Error("multi must honor ctx")
	}
	if _, err := s.Confirm(cctx, "x", false); !errors.Is(err, context.Canceled) {
		t.Error("confirm must honor ctx")
	}
	if _, err := s.Input(cctx, "x", "", nil); !errors.Is(err, context.Canceled) {
		t.Error("input must honor ctx")
	}
	if _, err := s.Secret(cctx, "x"); !errors.Is(err, context.Canceled) {
		t.Error("secret must honor ctx")
	}

	// Wrong types for every kind.
	for name, call := range map[string]func(*Scripted) error{
		"multi":   func(s *Scripted) error { _, err := s.MultiSelect(ctx, q); return err },
		"confirm": func(s *Scripted) error { _, err := s.Confirm(ctx, "x", false); return err },
		"input":   func(s *Scripted) error { _, err := s.Input(ctx, "x", "", nil); return err },
		"secret":  func(s *Scripted) error { _, err := s.Secret(ctx, "x"); return err },
	} {
		if err := call(NewScripted(3.5)); err == nil {
			t.Errorf("%s: wrong type accepted", name)
		}
	}
}

func TestNonInteractive(t *testing.T) {
	ctx := context.Background()
	n := NonInteractive{Flag: "--from"}
	q := Question{Title: "Based on"}
	var mf *MissingFlagError
	checks := map[string]error{}
	_, checks["select"] = n.Select(ctx, q)
	_, checks["multi"] = n.MultiSelect(ctx, q)
	_, checks["confirm"] = n.Confirm(ctx, "Sure?", true)
	_, checks["input"] = n.Input(ctx, "Name", "default", nil)
	_, checks["secret"] = n.Secret(ctx, "Token")
	for name, err := range checks {
		if !errors.As(err, &mf) || mf.Flag != "--from" || mf.Hint == "" {
			t.Errorf("%s: %v", name, err)
		}
		if CodeOf(err) != ExitUsage {
			t.Errorf("%s: code %d", name, CodeOf(err))
		}
	}
	if ok, _ := n.Confirm(ctx, "x", true); ok {
		t.Error("NonInteractive must never confirm")
	}
	_, err := NonInteractive{}.Select(ctx, q)
	if !errors.As(err, &mf) || mf.Flag != "" {
		t.Errorf("zero value: %v", err)
	}
}
