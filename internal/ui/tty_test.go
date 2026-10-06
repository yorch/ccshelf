package ui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

func newTestTTY(input string) (*TTY, *bytes.Buffer) {
	var errBuf bytes.Buffer
	t := NewTTY(Streams{In: strings.NewReader(input), Out: io.Discard, Err: &errBuf}, Mode{Plain: true})
	return t, &errBuf
}

var fruit = Question{
	Title:      "Pick",
	Filterable: true,
	Default:    -1,
	Options: []Option{
		{Label: "apple", Detail: "red", Value: "a"},
		{Label: "apricot", Value: "ap"},
		{Label: "banana", Value: "b"},
		{Label: "cherry", Value: "c"},
	},
}

func TestTTYSelect(t *testing.T) {
	withDefault := fruit
	withDefault.Default = 2
	noFilter := fruit
	noFilter.Filterable = false
	tests := []struct {
		name  string
		q     Question
		input string
		want  int
		err   error
		out   []string // substrings expected in prompt output
	}{
		{name: "number", q: fruit, input: "3\n", want: 2},
		{name: "number with spaces", q: fruit, input: " 4 \n", want: 3},
		{name: "default on empty", q: withDefault, input: "\n", want: 2, out: []string{"Choose [3]:", "*  3) banana"}},
		{name: "no default reprompts", q: fruit, input: "\n2\n", want: 1, out: []string{"Please choose one."}},
		{name: "out of range then ok", q: fruit, input: "9\n0\n1\n", want: 0, out: []string{"9 is not between 1 and 4."}},
		{name: "unique substring", q: fruit, input: "che\n", want: 3},
		{name: "case insensitive", q: fruit, input: "BAN\n", want: 2},
		{name: "match on value", q: fruit, input: "ap\n", want: 1}, // exact value "ap" wins over label prefix
		{name: "ambiguous narrows then number", q: fruit, input: "r\n4\n", want: 3, out: []string{"2 matches"}},
		{name: "ambiguous then narrower text", q: fruit, input: "ap\n", want: 1},
		{name: "ambiguous narrows with text", q: Question{Filterable: true, Default: -1, Options: []Option{{Label: "alpha"}, {Label: "alpine"}, {Label: "alpaca"}}},
			input: "alp\npine\n", want: 1, out: []string{"3 matches"}},
		{name: "no match then reset", q: fruit, input: "zzz\n/\n2\n", want: 1, out: []string{"Nothing matches"}},
		{name: "reset list", q: fruit, input: "r\n/\n4\n", want: 3},
		{name: "not filterable rejects text", q: noFilter, input: "apple\n1\n", want: 0, out: []string{"Enter a number between 1 and 4."}},
		{name: "last line without newline", q: fruit, input: "2", want: 1},
		{name: "eof aborts", q: fruit, input: "", want: -1, err: ErrAborted},
		{name: "eof after bad input aborts", q: fruit, input: "x\n", want: -1, err: ErrAborted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, errBuf := newTestTTY(tt.input)
			got, err := p.Select(context.Background(), tt.q)
			if !errors.Is(err, tt.err) {
				t.Fatalf("err = %v, want %v", err, tt.err)
			}
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
			for _, s := range tt.out {
				if !strings.Contains(errBuf.String(), s) {
					t.Errorf("output missing %q:\n%s", s, errBuf.String())
				}
			}
		})
	}
}

func TestTTYSelectSanitizesAndDoesNotTouchStdout(t *testing.T) {
	var out, errBuf bytes.Buffer
	p := NewTTY(Streams{In: strings.NewReader("1\n"), Out: &out, Err: &errBuf}, Mode{})
	q := Question{Title: "T\x1b[2J", Default: -1, Options: []Option{{Label: "a\x1bb", Detail: "d\x07"}}}
	if _, err := p.Select(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("stdout written: %q", out.String())
	}
	if strings.ContainsAny(errBuf.String(), "\x1b\x07") {
		t.Errorf("control characters leaked: %q", errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "a\uFFFDb  d\uFFFD") {
		t.Errorf("non-plain detail separator missing: %q", errBuf.String())
	}
}

func TestTTYSelectNoOptions(t *testing.T) {
	p, _ := newTestTTY("")
	if _, err := p.Select(context.Background(), Question{}); err == nil {
		t.Error("expected error")
	}
	if _, err := p.MultiSelect(context.Background(), Question{}); err == nil {
		t.Error("expected error")
	}
}

func TestTTYMultiSelect(t *testing.T) {
	nf := fruit
	nf.Filterable = false
	tests := []struct {
		name  string
		q     Question
		input string
		want  []int
		err   error
		out   string
	}{
		{name: "numbers", q: fruit, input: "1,3\n", want: []int{0, 2}},
		{name: "space separated unsorted dupes", q: fruit, input: "4 1 4\n", want: []int{0, 3}},
		{name: "range", q: fruit, input: "2-4\n", want: []int{1, 2, 3}},
		{name: "empty selects none", q: fruit, input: "\n", want: []int{}},
		{name: "name", q: fruit, input: "banana, cher\n", want: []int{2, 3}},
		{name: "ambiguous name reprompts", q: fruit, input: "ap\nbanana\n", want: []int{1}, out: ""},
		{name: "ambiguous partial", q: fruit, input: "app\n", want: []int{0}},
		{name: "ambiguous errors", q: Question{Filterable: true, Options: []Option{{Label: "xa"}, {Label: "xb"}}}, input: "x\n1\n", want: []int{0}, out: "matches 2 options"},
		{name: "out of range", q: fruit, input: "7\n2\n", want: []int{1}, out: "7 is not between"},
		{name: "bad range", q: fruit, input: "3-9\n4-2\n1\n", want: []int{0}, out: "is not within"},
		{name: "no match", q: fruit, input: "zzz\n1\n", want: []int{0}, out: "nothing matches"},
		{name: "text without filter", q: nf, input: "apple\n2\n", want: []int{1}, out: "is not a number"},
		{name: "eof", q: fruit, input: "", err: ErrAborted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, errBuf := newTestTTY(tt.input)
			got, err := p.MultiSelect(context.Background(), tt.q)
			if !errors.Is(err, tt.err) {
				t.Fatalf("err = %v, want %v", err, tt.err)
			}
			if err == nil && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
			if tt.out != "" && !strings.Contains(errBuf.String(), tt.out) {
				t.Errorf("output missing %q:\n%s", tt.out, errBuf.String())
			}
		})
	}
}

func TestTTYConfirm(t *testing.T) {
	tests := []struct {
		input string
		def   bool
		want  bool
		err   error
	}{
		{"y\n", false, true, nil},
		{"YES\n", false, true, nil},
		{"n\n", true, false, nil},
		{"No\n", true, false, nil},
		{"\n", true, true, nil},
		{"\n", false, false, nil},
		{"maybe\ny\n", false, true, nil},
		{"", true, false, ErrAborted},
	}
	for _, tt := range tests {
		p, errBuf := newTestTTY(tt.input)
		got, err := p.Confirm(context.Background(), "Sure?", tt.def)
		if !errors.Is(err, tt.err) || got != tt.want {
			t.Errorf("%q def=%v: got %v, %v want %v, %v", tt.input, tt.def, got, err, tt.want, tt.err)
		}
		hint := "[y/N]"
		if tt.def {
			hint = "[Y/n]"
		}
		if !strings.Contains(errBuf.String(), hint) {
			t.Errorf("hint %s missing: %q", hint, errBuf.String())
		}
	}
}

func TestTTYInput(t *testing.T) {
	notEmpty := func(s string) error {
		if s == "" {
			return errors.New("must not be empty")
		}
		return nil
	}
	p, errBuf := newTestTTY("\n  hello  \n")
	got, err := p.Input(context.Background(), "Name", "", notEmpty)
	if err != nil || got != "hello" || !strings.Contains(errBuf.String(), "must not be empty") {
		t.Errorf("got %q, %v, out %q", got, err, errBuf.String())
	}
	p, errBuf = newTestTTY("\n")
	got, err = p.Input(context.Background(), "Name", "dflt", nil)
	if err != nil || got != "dflt" || !strings.Contains(errBuf.String(), "[dflt]") {
		t.Errorf("default: got %q, %v", got, err)
	}
	p, _ = newTestTTY("")
	if _, err = p.Input(context.Background(), "Name", "", nil); !errors.Is(err, ErrAborted) {
		t.Errorf("eof: %v", err)
	}
}

func TestTTYSecretReadsLineFromPipe(t *testing.T) {
	p, errBuf := newTestTTY("hunter2\r\n")
	got, err := p.Secret(context.Background(), "Token")
	if err != nil || got != "hunter2" {
		t.Errorf("got %q, %v", got, err)
	}
	if strings.Contains(errBuf.String(), "hunter2") {
		t.Error("secret echoed to the prompt stream")
	}
	if _, err = p.Secret(context.Background(), "Token"); !errors.Is(err, ErrAborted) {
		t.Errorf("eof: %v", err)
	}
}

func TestTTYContextCancel(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	p := NewTTY(Streams{In: pr, Err: io.Discard}, Mode{Plain: true})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := p.Confirm(ctx, "x", false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if CodeOf(err) != ExitInterrupted {
		t.Errorf("code %d", CodeOf(err))
	}
	// Already-cancelled context fails fast for every prompt, without reading.
	if _, err = p.Select(ctx, fruit); !errors.Is(err, context.Canceled) {
		t.Errorf("select: %v", err)
	}
	if _, err = p.Secret(ctx, "s"); !errors.Is(err, context.Canceled) {
		t.Errorf("secret: %v", err)
	}
	// A later prompt on a live context reuses the pending read, so no input is lost.
	go func() { _, _ = pw.Write([]byte("y\n")) }()
	ok, err := p.Confirm(context.Background(), "again", false)
	if err != nil || !ok {
		t.Errorf("after cancel: %v %v", ok, err)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("device gone") }

func TestTTYReadError(t *testing.T) {
	p := NewTTY(Streams{In: errReader{}}, Mode{})
	_, err := p.Confirm(context.Background(), "x", false)
	if err == nil || errors.Is(err, ErrAborted) || !strings.Contains(err.Error(), "device gone") {
		t.Errorf("got %v", err)
	}
	// nil streams are tolerated.
	p = NewTTY(Streams{}, Mode{})
	if _, err = p.Confirm(context.Background(), "x", false); !errors.Is(err, ErrAborted) {
		t.Errorf("nil input: %v", err)
	}
}
