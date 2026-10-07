package ui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/term"
)

func parsedKeys(t *testing.T, input string) []pickerKey {
	t.Helper()
	var p keyParser
	var out []pickerKey
	for _, b := range []byte(input) {
		if k, ok := p.feed(b); ok {
			out = append(out, k)
		}
	}
	if len(p.pending) != 0 {
		t.Fatalf("incomplete input %q", input)
	}
	return out
}

func TestPickerKeyParser(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  pickerKey
	}{
		{"\x1b[A", pickerKey{kind: keyUp}},
		{"\x1bOB", pickerKey{kind: keyDown}},
		{"\x1b[1;5A", pickerKey{kind: keyUp}},
		{"\x1b[C", pickerKey{kind: keyIgnore}},
		{"\r", pickerKey{kind: keyEnter}},
		{"\n", pickerKey{kind: keyEnter}},
		{" ", pickerKey{kind: keySpace}},
		{"\x7f", pickerKey{kind: keyBackspace}},
		{"\b", pickerKey{kind: keyBackspace}},
		{"\x15", pickerKey{kind: keyReset}},
		{"\x03", pickerKey{kind: keyCancel}},
		{"\x04", pickerKey{kind: keyCancel}},
		{"\x1a", pickerKey{kind: keyCancel}},
		{"界", pickerKey{text: '界'}},
		{"\u202e", pickerKey{kind: keyIgnore}},
		{"\xff", pickerKey{kind: keyIgnore}},
	} {
		if got := parsedKeys(t, tt.input); !reflect.DeepEqual(got, []pickerKey{tt.want}) {
			t.Errorf("%q: %v, want %v", tt.input, got, tt.want)
		}
	}
	var p keyParser
	for _, b := range []byte("\x1b[" + strings.Repeat("1", 200)) {
		p.feed(b)
		if len(p.pending) > 16 {
			t.Fatal("unbounded key buffer")
		}
	}
}

func drivePicker(t *testing.T, p *pickerState, input string) bool {
	t.Helper()
	done := false
	for _, k := range parsedKeys(t, input) {
		var err error
		done, err = p.apply(k)
		if err != nil {
			t.Fatal(err)
		}
	}
	return done
}

func TestPickerNavigationFiltering(t *testing.T) {
	p := newPickerState(fruit, false)
	if !drivePicker(t, p, "\x1b[A\x1b[B\r") || p.result()[0] != 1 {
		t.Fatalf("navigation: %+v", p)
	}
	p = newPickerState(fruit, false)
	if drivePicker(t, p, "zzz\r") || len(p.candidates) != 0 {
		t.Fatal("empty filter selected something")
	}
	if !strings.Contains(strings.Join(p.lines(80, 14), "\n"), "No matches") {
		t.Fatal("missing empty state")
	}
	drivePicker(t, p, "\x15BAN")
	if !reflect.DeepEqual(p.candidates, []int{2}) || !drivePicker(t, p, "\r") {
		t.Fatalf("filter: %+v", p)
	}
	drivePicker(t, p, "\x7f\x15")
	if len(p.candidates) != len(fruit.Options) || p.filter != "" {
		t.Fatal("reset failed")
	}
	q := fruit
	q.Default, q.HasDefault = 2, true
	p = newPickerState(q, false)
	if !drivePicker(t, p, "\r") || p.result()[0] != 2 {
		t.Fatal("explicit default lost")
	}
	q.Filterable = false
	p = newPickerState(q, false)
	drivePicker(t, p, "zz")
	if p.filter != "" {
		t.Fatal("non-filterable question filtered")
	}
}

func TestPickerMultiSelectPreservesHiddenSelections(t *testing.T) {
	p := newPickerState(fruit, true)
	drivePicker(t, p, " \x1b[B ba ") // apple, apricot; filter to banana and select it
	if !reflect.DeepEqual(p.result(), []int{0, 1, 2}) {
		t.Fatalf("got %v", p.result())
	}
	if !strings.Contains(strings.Join(p.lines(80, 14), "\n"), "3 selected (including hidden)") {
		t.Fatal("hidden selection count missing")
	}
	drivePicker(t, p, "\x15 ") // reset then deselect apple
	if !drivePicker(t, p, "zzz\r") || !reflect.DeepEqual(p.result(), []int{1, 2}) {
		t.Fatalf("got %v", p.result())
	}
	p = newPickerState(fruit, true)
	if !drivePicker(t, p, "\r") || len(p.result()) != 0 {
		t.Fatal("empty submit must select none")
	}
}

func TestPickerRenderingIsBoundedAndSanitized(t *testing.T) {
	q := Question{Title: "title\n\x1b[2J\u202e" + strings.Repeat("界", 500), Filterable: true}
	for range 200 {
		q.Options = append(q.Options, Option{Label: "bad\n\x1b[31m", Detail: strings.Repeat("界", 500)})
	}
	p := newPickerState(q, true)
	p.cursor = 199
	for _, width := range []int{19, 40, 99} {
		lines := p.lines(width, 14)
		if len(lines) > 14 {
			t.Fatalf("height = %d", len(lines))
		}
		for _, line := range lines {
			if HasControl(line) || DisplayWidth(line) > width {
				t.Errorf("unsafe or wide %q", line)
			}
		}
		if !strings.Contains(lines[len(lines)-1], ">") {
			t.Fatal("cursor scrolled out of view")
		}
	}
	// Zero-width marks must not bypass the output bound.
	p.q.Title = strings.Repeat("\u0301", 10000)
	p.q.Options[199].Label = strings.Repeat("\u0301", 10000)
	for _, line := range p.lines(40, 14) {
		if len(line) > 2048 {
			t.Fatalf("unbounded combining marks: %d bytes", len(line))
		}
	}
	drivePicker(t, p, strings.Repeat("a", 2000))
	if len(p.filter) > 256 {
		t.Fatal("filter unbounded")
	}
}

func TestPickerKeyReaderCancellationEOFAndEscape(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readPickerKey(ctx, func(time.Duration) (byte, bool, error) { t.Fatal("read on canceled context"); return 0, false, nil }, &keyParser{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, readErr := range []error{io.EOF, errors.New("disconnected")} {
		_, err := readPickerKey(context.Background(), func(time.Duration) (byte, bool, error) { return 0, false, readErr }, &keyParser{})
		if err == nil || (errors.Is(readErr, io.EOF) && !errors.Is(err, ErrAborted)) {
			t.Fatal(err)
		}
	}
	i := 0
	k, err := readPickerKey(context.Background(), func(wait time.Duration) (byte, bool, error) {
		i++
		if i == 1 {
			return 27, true, nil
		}
		time.Sleep(wait)
		return 0, false, nil
	}, &keyParser{})
	if err != nil || k.kind != keyCancel {
		t.Fatalf("escape: %v %v", k, err)
	}
}

func TestPickerNonFileAndPlainFallback(t *testing.T) {
	for _, mode := range []Mode{{Interactive: true}, {Interactive: true, Plain: true}, {}} {
		var out bytes.Buffer
		p := NewTTY(Streams{In: strings.NewReader("2\n1,3\n"), Err: &out}, mode)
		if i, err := p.Select(context.Background(), fruit); err != nil || i != 1 {
			t.Fatalf("select %d %v", i, err)
		}
		if indexes, err := p.MultiSelect(context.Background(), fruit); err != nil || !reflect.DeepEqual(indexes, []int{0, 2}) {
			t.Fatalf("multi %v %v", indexes, err)
		}
		if strings.Contains(out.String(), "\x1b") || !strings.Contains(out.String(), "Choose:") {
			t.Fatalf("not line mode: %q", out.String())
		}
	}
}

func installPickerHooks(t *testing.T) {
	t.Helper()
	oldT, oldM, oldR, oldS, oldB, oldO := isTerminal, pickerMakeRaw, pickerRestore, pickerSize, pickerRead, pickerOutput
	t.Cleanup(func() {
		isTerminal, pickerMakeRaw, pickerRestore, pickerSize, pickerRead, pickerOutput = oldT, oldM, oldR, oldS, oldB, oldO
	})
	isTerminal = func(uintptr) bool { return true }
	pickerSize = func(int) (int, int, error) { return 80, 24, nil }
}

func TestPickerRestoresBeforeReturning(t *testing.T) {
	for _, kind := range []string{"success", "multi", "ctrl-c", "ctrl-d", "escape", "eof", "read error", "write error", "context cancel", "restore error", "output restore error", "resize", "size error"} {
		t.Run(kind, func(t *testing.T) {
			installPickerHooks(t)
			in, err := os.CreateTemp(t.TempDir(), "in")
			if err != nil {
				t.Fatal(err)
			}
			defer in.Close()
			out, err := os.CreateTemp(t.TempDir(), "out")
			if err != nil {
				t.Fatal(err)
			}
			defer out.Close()
			inputRestored, outputRestored := false, false
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pickerMakeRaw = func(int) (*term.State, error) {
				if kind == "write error" {
					out.Close()
				}
				return &term.State{}, nil
			}
			pickerRestore = func(int, *term.State) error {
				inputRestored = true
				if kind == "restore error" {
					return errors.New("restore failed")
				}
				return nil
			}
			pickerOutput = func(*os.File) (func() error, bool) {
				return func() error {
					outputRestored = true
					if kind == "output restore error" {
						return errors.New("output restore failed")
					}
					return nil
				}, true
			}
			if kind == "resize" || kind == "size error" {
				calls := 0
				pickerSize = func(int) (int, int, error) {
					calls++
					if calls > 1 {
						if kind == "size error" {
							return 0, 0, errors.New("size failed")
						}
						return 40, 12, nil
					}
					return 80, 24, nil
				}
			}
			pickerRead = func(*os.File) func(time.Duration) (byte, bool, error) {
				return func(time.Duration) (byte, bool, error) {
					switch kind {
					case "ctrl-c":
						return 3, true, nil
					case "ctrl-d":
						return 4, true, nil
					case "escape":
						return 27, true, nil
					case "eof":
						return 0, false, io.EOF
					case "read error":
						return 0, false, errors.New("read failed")
					case "context cancel":
						cancel()
						return 0, false, nil
					default:
						return '\r', true, nil
					}
				}
			}
			// A standalone Escape needs an idle poll, not repeated Escape bytes.
			if kind == "escape" {
				first := true
				pickerRead = func(*os.File) func(time.Duration) (byte, bool, error) {
					return func(wait time.Duration) (byte, bool, error) {
						if first {
							first = false
							return 27, true, nil
						}
						time.Sleep(wait)
						return 0, false, nil
					}
				}
			}
			p := NewTTY(Streams{In: in, Err: out}, Mode{Interactive: true})
			if kind == "multi" {
				_, err = p.MultiSelect(ctx, fruit)
			} else {
				_, err = p.Select(ctx, fruit)
			}
			if !inputRestored || !outputRestored {
				t.Fatalf("restoration input=%v output=%v", inputRestored, outputRestored)
			}
			if kind == "success" || kind == "multi" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestPickerUnsupportedConsoleFallsBack(t *testing.T) {
	for _, kind := range []string{"raw", "output", "size", "narrow", "short", "plain"} {
		t.Run(kind, func(t *testing.T) {
			installPickerHooks(t)
			in, err := os.CreateTemp(t.TempDir(), "in")
			if err != nil {
				t.Fatal(err)
			}
			defer in.Close()
			in.WriteString("2\n")
			in.Seek(0, io.SeekStart)
			out, err := os.CreateTemp(t.TempDir(), "out")
			if err != nil {
				t.Fatal(err)
			}
			defer out.Close()
			pickerMakeRaw = func(int) (*term.State, error) {
				if kind != "raw" {
					t.Fatal("unexpected raw mode")
				}
				return nil, errors.New("unsupported")
			}
			restored := false
			pickerOutput = func(*os.File) (func() error, bool) {
				return func() error { restored = true; return nil }, kind != "output"
			}
			if kind == "size" {
				pickerSize = func(int) (int, int, error) { return 0, 0, errors.New("unknown") }
			}
			if kind == "narrow" {
				pickerSize = func(int) (int, int, error) { return 19, 24, nil }
			}
			if kind == "short" {
				pickerSize = func(int) (int, int, error) { return 80, 5, nil }
			}
			p := NewTTY(Streams{In: in, Err: out}, Mode{Interactive: true, Plain: kind == "plain"})
			if i, err := p.Select(context.Background(), fruit); err != nil || i != 1 {
				t.Fatalf("got %d %v", i, err)
			}
			if kind == "raw" && !restored {
				t.Fatal("output state not restored on raw failure")
			}
		})
	}
}
