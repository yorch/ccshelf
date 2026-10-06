package ui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/term"
)

// TTY is the line-oriented Prompter for terminals. It never uses raw mode, so
// it works on any terminal, including Windows consoles and screen readers: a
// question is printed as a numbered list and the user types a number or, when
// the question is filterable, part of a label (type-to-filter). Ambiguous text
// narrows the list and asks again; "/" clears the filter. Ctrl+D or end of
// input returns ErrAborted; canceling the context returns ctx.Err().
//
// Prompts are written to Streams.Err and answers are read from Streams.In. A
// canceled read cannot be interrupted at the operating system level, so one
// goroutine may remain blocked reading input until the process exits; that is
// harmless because a canceled command is about to exit.
type TTY struct {
	streams Streams
	mode    Mode
	r       *bufio.Reader

	pending chan lineResult
}

var _ Prompter = (*TTY)(nil)

type lineResult struct {
	s   string
	err error
}

// NewTTY returns a TTY prompter reading from streams.In and writing prompts
// to streams.Err. Plain mode prints ASCII only.
func NewTTY(streams Streams, mode Mode) *TTY {
	in := streams.In
	if in == nil {
		in = strings.NewReader("")
	}
	if streams.Err == nil {
		streams.Err = io.Discard
	}
	return &TTY{streams: streams, mode: mode, r: bufio.NewReader(in)}
}

func (t *TTY) printf(format string, a ...any) {
	_, _ = fmt.Fprintf(t.streams.Err, format, a...)
}

// readLine reads one line without its terminator. It honors ctx.
func (t *TTY) readLine(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if t.pending == nil {
		ch := make(chan lineResult, 1)
		t.pending = ch
		go func() {
			s, err := t.r.ReadString('\n')
			ch <- lineResult{s, err}
		}()
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case res := <-t.pending:
		t.pending = nil
		return finishLine(res)
	}
}

func finishLine(res lineResult) (string, error) {
	if res.err != nil && !(errors.Is(res.err, io.EOF) && res.s != "") {
		if errors.Is(res.err, io.EOF) {
			return "", ErrAborted
		}
		return "", fmt.Errorf("reading input: %w", res.err)
	}
	return strings.TrimRight(res.s, "\r\n"), nil
}

func (t *TTY) renderOption(i int, o Option, def bool) string {
	var b strings.Builder
	mark := " "
	if def {
		mark = "*"
	}
	fmt.Fprintf(&b, " %s%3d) %s", mark, i+1, SanitizeLine(o.Label))
	if o.Detail != "" {
		sep := "  "
		if t.mode.Plain {
			sep = " - "
		}
		b.WriteString(sep + SanitizeLine(o.Detail))
	}
	return b.String()
}

func (t *TTY) printList(q Question, cands []int) {
	d, hasDef := q.defaultIndex()
	for _, i := range cands {
		t.printf("%s\n", t.renderOption(i, q.Options[i], hasDef && i == d))
	}
}

func allIndexes(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

// matches returns the candidates whose label or value contains text, ignoring
// case. An exact (case-insensitive) label or value match wins on its own.
func matches(q Question, cands []int, text string) []int {
	lt := strings.ToLower(text)
	var exact, partial []int
	for _, i := range cands {
		l, v := strings.ToLower(q.Options[i].Label), strings.ToLower(q.Options[i].Value)
		switch {
		case l == lt || (v != "" && v == lt):
			exact = append(exact, i)
		case strings.Contains(l, lt) || (v != "" && strings.Contains(v, lt)):
			partial = append(partial, i)
		}
	}
	if len(exact) == 1 {
		return exact
	}
	return append(exact, partial...)
}

func (t *TTY) title(q Question) {
	t.printf("? %s", SanitizeLine(q.Title))
	if q.Filterable {
		t.printf("  (type a number or part of a name to filter)")
	}
	t.printf("\n")
}

// Select implements Prompter.
func (t *TTY) Select(ctx context.Context, q Question) (int, error) {
	if len(q.Options) == 0 {
		return -1, errors.New("select: no options to choose from")
	}
	cands := allIndexes(len(q.Options))
	t.title(q)
	t.printList(q, cands)
	def, hasDef := q.defaultIndex()
	for {
		if hasDef {
			t.printf("Choose [%d]: ", def+1)
		} else {
			t.printf("Choose: ")
		}
		line, err := t.readLine(ctx)
		if err != nil {
			return -1, err
		}
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			if hasDef {
				return def, nil
			}
			t.printf("Please choose one.\n")
			continue
		case line == "/" && q.Filterable:
			cands = allIndexes(len(q.Options))
			t.printList(q, cands)
			continue
		}
		if n, convErr := strconv.Atoi(line); convErr == nil {
			if n >= 1 && n <= len(q.Options) {
				return n - 1, nil
			}
			t.printf("%d is not between 1 and %d.\n", n, len(q.Options))
			continue
		}
		if !q.Filterable {
			t.printf("Enter a number between 1 and %d.\n", len(q.Options))
			continue
		}
		m := matches(q, cands, line)
		switch len(m) {
		case 0:
			t.printf("Nothing matches %q. Type / to show everything again.\n", SanitizeLine(line))
		case 1:
			return m[0], nil
		default:
			cands = m
			t.printf("%d matches for %q:\n", len(m), SanitizeLine(line))
			t.printList(q, cands)
		}
	}
}

// MultiSelect implements Prompter. Answers are numbers, ranges ("2-4") or
// names, separated by commas or spaces; an empty answer selects nothing.
func (t *TTY) MultiSelect(ctx context.Context, q Question) ([]int, error) {
	if len(q.Options) == 0 {
		return nil, errors.New("multi-select: no options to choose from")
	}
	t.title(q)
	t.printList(Question{Options: q.Options}, allIndexes(len(q.Options)))
	for {
		t.printf("Select (numbers like 1,3 or 2-4; empty for none): ")
		line, err := t.readLine(ctx)
		if err != nil {
			return nil, err
		}
		sel, perr := parseMulti(q, line)
		if perr != nil {
			t.printf("%s\n", SanitizeLine(perr.Error()))
			continue
		}
		return sel, nil
	}
}

func parseMulti(q Question, line string) ([]int, error) {
	seen := map[int]bool{}
	for _, tok := range strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		if lo, hi, ok := strings.Cut(tok, "-"); ok && lo != "" && hi != "" {
			a, errA := strconv.Atoi(lo)
			b, errB := strconv.Atoi(hi)
			if errA == nil && errB == nil {
				if a < 1 || b > len(q.Options) || a > b {
					return nil, fmt.Errorf("range %q is not within 1-%d", tok, len(q.Options))
				}
				for i := a; i <= b; i++ {
					seen[i-1] = true
				}
				continue
			}
		}
		if n, err := strconv.Atoi(tok); err == nil {
			if n < 1 || n > len(q.Options) {
				return nil, fmt.Errorf("%d is not between 1 and %d", n, len(q.Options))
			}
			seen[n-1] = true
			continue
		}
		if !q.Filterable {
			return nil, fmt.Errorf("%q is not a number between 1 and %d", tok, len(q.Options))
		}
		m := matches(q, allIndexes(len(q.Options)), tok)
		switch len(m) {
		case 0:
			return nil, fmt.Errorf("nothing matches %q", tok)
		case 1:
			seen[m[0]] = true
		default:
			return nil, fmt.Errorf("%q matches %d options; use their numbers", tok, len(m))
		}
	}
	out := make([]int, 0, len(seen))
	for i := range seen {
		out = append(out, i)
	}
	sort.Ints(out)
	return out, nil
}

// Confirm implements Prompter.
func (t *TTY) Confirm(ctx context.Context, text string, def bool) (bool, error) {
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	for {
		t.printf("? %s %s ", SanitizeLine(text), hint)
		line, err := t.readLine(ctx)
		if err != nil {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "":
			return def, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		t.printf("Please answer y or n.\n")
	}
}

// Input implements Prompter.
func (t *TTY) Input(ctx context.Context, text, def string, validate func(string) error) (string, error) {
	for {
		if def != "" {
			t.printf("? %s [%s]: ", SanitizeLine(text), SanitizeLine(def))
		} else {
			t.printf("? %s: ", SanitizeLine(text))
		}
		line, err := t.readLine(ctx)
		if err != nil {
			return "", err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			line = def
		}
		if validate != nil {
			if verr := validate(line); verr != nil {
				t.printf("%s\n", SanitizeLine(verr.Error()))
				continue
			}
		}
		return line, nil
	}
}

// Test seams for Secret; production code never reassigns them.
var (
	termGetState     = term.GetState
	termRestore      = term.Restore
	termReadPassword = term.ReadPassword
)

// Secret implements Prompter. When input is a terminal the value is read
// without echo; otherwise (a pipe in tests) a line is read. The terminal state
// is saved first and restored on every way out, including a canceled context
// and an interrupt or termination signal (which cancel the read instead of
// killing the process with echo still off).
func (t *TTY) Secret(ctx context.Context, text string) (string, error) {
	t.printf("? %s: ", SanitizeLine(text))
	f, ok := t.streams.In.(*os.File)
	if !ok || f == nil || !isTerminal(f.Fd()) {
		return t.readLine(ctx)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	fd := int(f.Fd()) //nolint:gosec // fd fits in int
	state, err := termGetState(fd)
	if err != nil {
		return "", fmt.Errorf("saving the terminal state: %w", err)
	}
	restore := func() { _ = termRestore(fd, state) }
	defer restore()

	sctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	type res struct {
		b   []byte
		err error
	}
	ch := make(chan res, 1)
	readPassword := termReadPassword
	go func() {
		b, err := readPassword(fd)
		ch <- res{b, err}
	}()
	select {
	case <-sctx.Done():
		restore()
		t.printf("\n")
		return "", sctx.Err()
	case r := <-ch:
		restore()
		t.printf("\n")
		if r.err != nil {
			if errors.Is(r.err, io.EOF) {
				return "", ErrAborted
			}
			return "", fmt.Errorf("reading secret: %w", r.err)
		}
		return string(r.b), nil
	}
}
