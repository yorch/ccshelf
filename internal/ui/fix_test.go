package ui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/term"
)

func TestSanitizeInvisible(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"zwsp", "a\u200Bb", "a�b"},
		{"arabic letter mark", "a\u061Cb", "a�b"},
		{"bom", "a\uFEFFb", "a�b"},
		{"word joiner", "a\u2060b", "a�b"},
		{"soft hyphen", "a\u00ADb", "a�b"},
		{"tag char", "a\U000E0041b", "a�b"},
		{"tag block end", "a\U000E007Fb", "a�b"},
		{"keeps newline", "a\nb", "a\nb"},
	}
	for _, tt := range tests {
		if got := Sanitize(tt.in); got != tt.want {
			t.Errorf("%s: Sanitize(%q) = %q, want %q", tt.name, tt.in, got, tt.want)
		}
		if tt.in != "a\nb" && !HasControl(tt.in) {
			t.Errorf("%s: HasControl false", tt.name)
		}
	}
	for in, want := range map[string]string{
		"a\nb": "a�b", "a\tb": "a b", "plain": "plain", "x\x1b[2Jy": "x�[2Jy", "bad\xff": "bad�",
	} {
		if got := SanitizeLine(in); got != want {
			t.Errorf("SanitizeLine(%q) = %q, want %q", in, got, want)
		}
	}
	if HasControl("plain text-1") || !HasControl("a\nb") || !HasControl("a\tb") || !HasControl("\xff") {
		t.Error("HasControl")
	}
}

func TestTTYNewlinesCannotForgeLines(t *testing.T) {
	evil := "x\n? Fake prompt\nhint: run rm"
	var errBuf bytes.Buffer
	p := NewTTY(Streams{In: strings.NewReader("1\n"), Err: &errBuf}, Mode{Plain: true})
	q := Question{Title: evil, Options: []Option{{Label: evil, Detail: evil}}}
	if _, err := p.Select(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	p2 := NewTTY(Streams{In: strings.NewReader("ok\n"), Err: &errBuf}, Mode{Plain: true})
	if _, err := p2.Input(context.Background(), evil, evil, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	p3 := NewTTY(Streams{In: strings.NewReader("bad\nok\n"), Err: &errBuf}, Mode{Plain: true})
	_, err := p3.Input(context.Background(), "t", "", func(s string) error {
		if s == "bad" {
			return errors.New(evil)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	p4 := NewTTY(Streams{In: strings.NewReader("y\n"), Err: &errBuf}, Mode{Plain: true})
	if _, err := p4.Confirm(context.Background(), evil, false); err != nil {
		t.Fatal(err)
	}
	p5 := NewTTY(Streams{In: strings.NewReader("1\n"), Err: &errBuf}, Mode{Plain: true})
	if _, err := p5.MultiSelect(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(errBuf.String(), "\n") {
		if strings.HasPrefix(line, "hint:") || strings.HasPrefix(line, "? Fake") {
			t.Errorf("forged line in output: %q", line)
		}
	}
}

func TestReportNewlinesAndHint(t *testing.T) {
	var b bytes.Buffer
	Report(&b, errors.New("bad\nhint: do evil"), Mode{})
	if b.String() != "error: bad�hint: do evil\n" {
		t.Errorf("error text: %q", b.String())
	}
	b.Reset()
	Report(&b, &MissingFlagError{Flag: "--a\nhint: x"}, Mode{})
	if strings.Count(b.String(), "\nhint:") != 1 {
		t.Errorf("flag newline forged a hint: %q", b.String())
	}
	b.Reset()
	Report(&b, evilHint{}, Mode{})
	if b.String() != "error: boom\nhint: a�[2Jb�hint: forged\n" {
		t.Errorf("hint not sanitized: %q", b.String())
	}
	n := NonInteractive{}
	_, err := n.Confirm(context.Background(), "q\nhint: x", false)
	var mf *MissingFlagError
	if !errors.As(err, &mf) || strings.Contains(mf.Hint, "\n") {
		t.Errorf("hint kept a newline: %v", err)
	}
}

type evilHint struct{}

func (evilHint) Error() string { return "boom" }
func (evilHint) Hint() string  { return "a\x1b[2Jb\nhint: forged" }

func TestQuestionDefaultIsOptIn(t *testing.T) {
	opts := []Option{{Label: "danger"}, {Label: "safe"}}
	// The zero value of Default must not select the first option.
	p, errBuf := newTestTTY("\n2\n")
	got, err := p.Select(context.Background(), Question{Title: "t", Options: opts})
	if err != nil || got != 1 || !strings.Contains(errBuf.String(), "Please choose one.") {
		t.Errorf("zero-value default: %d %v %q", got, err, errBuf.String())
	}
	// HasDefault makes Default effective; out of range is ignored.
	p, _ = newTestTTY("\n")
	if got, err = p.Select(context.Background(), Question{Title: "t", Options: opts, Default: 1, HasDefault: true}); err != nil || got != 1 {
		t.Errorf("explicit default: %d %v", got, err)
	}
	p, _ = newTestTTY("\n1\n")
	if got, _ = p.Select(context.Background(), Question{Title: "t", Options: opts, Default: 5, HasDefault: true}); got != 0 {
		t.Errorf("out-of-range default: %d", got)
	}
}

func TestConfirmRisky(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{{"yes\n", true}, {" YES \n", true}, {"y\n", false}, {"\n", false}, {"no\n", false}, {"yess\n", false}}
	for _, tt := range tests {
		p, errBuf := newTestTTY(tt.in)
		got, err := ConfirmRisky(context.Background(), p, "Trust it?")
		if err != nil || got != tt.want {
			t.Errorf("%q: %v %v", tt.in, got, err)
		}
		if !strings.Contains(errBuf.String(), "Type yes to confirm") {
			t.Errorf("prompt: %q", errBuf.String())
		}
	}
	s := NewScripted("yes")
	if ok, err := ConfirmRisky(context.Background(), s, "x"); err != nil || !ok {
		t.Errorf("scripted: %v %v", ok, err)
	}
	var mf *MissingFlagError
	if _, err := ConfirmRisky(context.Background(), NonInteractive{}, "x"); !errors.As(err, &mf) {
		t.Errorf("non-interactive: %v", err)
	}
}

func TestRecorderFixes(t *testing.T) {
	r := NewRecorder("new", "-weird", "ok")
	r.Flag("--from", "sre")
	r.Bool("force")
	r.Bool("--author")
	r.Flag("--author", "Ann")
	want := []string{"new", "--from", "sre", "--force", "--author", "--author", "Ann", "--", "-weird", "ok"}
	if got := r.Args(); strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("Args = %q", got)
	}
	var b bytes.Buffer
	if err := r.Print(&b, "linux"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "redacted") || !strings.Contains(b.String(), "--author Ann -- -weird ok") {
		t.Errorf("author redacted or no --: %q", b.String())
	}

	// A valueless secret-looking switch does not eat the next token; a
	// registered secret flag does redact its value, even a dash value.
	r = NewRecorder("x", "NAME=1", "MY_TOKEN=abc")
	r.Bool("--password")
	r.Flag("--name", "visible")
	r.SecretFlag("--note", "-hidden")
	r.Flag("--api-token", "-t")
	b.Reset()
	if err := r.PrintFor(&b, ShellFish); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	for _, bad := range []string{"abc", "hidden", "-t'"} {
		if strings.Contains(got, bad) {
			t.Errorf("leaked %q: %q", bad, got)
		}
	}
	for _, good := range []string{"--password --name visible", "MY_TOKEN=", "--note"} {
		if !strings.Contains(got, good) {
			t.Errorf("missing %q: %q", good, got)
		}
	}
	if args := r.Args(); args[len(args)-1] != "--api-token=-t" {
		t.Errorf("Args must not redact: %q", args)
	}
}

func TestRedactArgsFailsTowardRedaction(t *testing.T) {
	tests := []struct{ in, want []string }{
		{[]string{"--author", "Ann"}, []string{"--author", "Ann"}},
		{[]string{"--apiKey", "k"}, []string{"--apiKey", "<redacted>"}},
		{[]string{"--accessToken", "k"}, []string{"--accessToken", "<redacted>"}},
		{[]string{"--accessToken=k"}, []string{"--accessToken=<redacted>"}},
		{[]string{"--token", "-dash"}, []string{"--token", "<redacted>"}},
		{[]string{"--token", "--json"}, []string{"--token", "<redacted>"}},
		{[]string{"--token", "--", "x"}, []string{"--token", "--", "x"}},
		{[]string{"--token"}, []string{"--token"}},
		{[]string{"--client-secret", "s", "--MyPassword", "p"}, []string{"--client-secret", "<redacted>", "--MyPassword", "<redacted>"}},
		{[]string{"--tokenfile", "f"}, []string{"--tokenfile", "<redacted>"}},
		{[]string{"--passphrase", "p", "--passwd", "q", "--credentials", "c"}, []string{"--passphrase", "<redacted>", "--passwd", "<redacted>", "--credentials", "<redacted>"}},
		{[]string{"--bearer", "b", "--cookie", "c", "--private-thing", "p"}, []string{"--bearer", "<redacted>", "--cookie", "<redacted>", "--private-thing", "<redacted>"}},
		{[]string{"--apikey", "k", "--api_key", "k", "--api-key", "k"}, []string{"--apikey", "<redacted>", "--api_key", "<redacted>", "--api-key", "<redacted>"}},
		{[]string{"--auth", "a"}, []string{"--auth", "<redacted>"}},
		{[]string{"--key", "k", "--keys", "k", "--key-id", "k", "--keyboard", "k"}, []string{"--key", "<redacted>", "--keys", "<redacted>", "--key-id", "<redacted>", "--keyboard", "k"}},
		{[]string{"--secretary", "s", "--tokenizer", "t"}, []string{"--secretary", "s", "--tokenizer", "t"}},
		{[]string{"--", "--token", "x", "MY_SECRET=1"}, []string{"--", "--token", "x", "MY_SECRET=<redacted>"}},
	}
	for _, tt := range tests {
		if got := RedactArgs(tt.in); strings.Join(got, "\x00") != strings.Join(tt.want, "\x00") {
			t.Errorf("RedactArgs(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestQuoteFish(t *testing.T) {
	tests := []struct{ in, want string }{
		{"plain", "plain"},
		{"", "''"},
		{"a b", "'a b'"},
		{`a\b`, `'a\\b'`},
		{`it's`, `'it\'s'`},
		{`\'`, `'\\\''`},
		{"$HOME", "'$HOME'"},
	}
	for _, tt := range tests {
		got, err := QuoteFish(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("QuoteFish(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
	if _, err := QuoteFish("a\nb"); !errors.Is(err, ErrUnquotable) {
		t.Errorf("newline: %v", err)
	}
	if q, err := Quote(ShellFish, `a\b`); err != nil || q != `'a\\b'` {
		t.Errorf("Quote fish: %q %v", q, err)
	}
	if s, ok := ShellForName("zsh"); !ok || s != ShellPOSIX {
		t.Error("ShellForName zsh")
	}
	for name, want := range map[string]string{"fish": ShellFish, "pwsh": ShellPowerShell, "cmd": ShellCmd, "sh": ShellPOSIX} {
		if s, ok := ShellForName(name); !ok || s != want {
			t.Errorf("ShellForName(%s) = %q", name, s)
		}
	}
	if _, ok := ShellForName("tcsh"); ok {
		t.Error("tcsh accepted")
	}
	var b bytes.Buffer
	if err := EquivalentFor(&b, ShellFish, []string{"new", `a\b`}); err != nil || b.String() != "Equivalent: ccshelf new 'a\\\\b'\n" {
		t.Errorf("EquivalentFor: %q %v", b.String(), err)
	}
}

func TestQuoteFishRoundTrip(t *testing.T) {
	fish, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("fish is not installed")
	}
	for _, s := range []string{`a\b`, `it's`, `\'`, `a\\'b`, `$x "y" *`, `trailing\`} {
		q, err := QuoteFish(s)
		if err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(fish, "--no-config", "-c", "printf '%s' "+q).Output() //nolint:gosec // test
		if err != nil || string(out) != s {
			t.Errorf("fish round trip of %q via %s: %q %v", s, q, out, err)
		}
	}
}

// fakeTerm replaces the terminal seams used by Secret.
type fakeTerm struct {
	getErr   error
	restores int
	read     func(fd int) ([]byte, error)
}

func (f *fakeTerm) install(t *testing.T) *os.File {
	t.Helper()
	oldT, oldG, oldR, oldP := isTerminal, termGetState, termRestore, termReadPassword
	t.Cleanup(func() { isTerminal, termGetState, termRestore, termReadPassword = oldT, oldG, oldR, oldP })
	isTerminal = func(uintptr) bool { return true }
	termGetState = func(int) (*term.State, error) { return &term.State{}, f.getErr }
	termRestore = func(int, *term.State) error { f.restores++; return nil }
	termReadPassword = f.read
	in, err := os.CreateTemp(t.TempDir(), "tty")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = in.Close() })
	return in
}

func TestSecretRestoresTerminalState(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		f := &fakeTerm{read: func(int) ([]byte, error) { return []byte("pw"), nil }}
		in := f.install(t)
		p := NewTTY(Streams{In: in, Err: io.Discard}, Mode{})
		got, err := p.Secret(context.Background(), "T")
		if err != nil || got != "pw" || f.restores == 0 {
			t.Errorf("got %q %v restores=%d", got, err, f.restores)
		}
	})
	t.Run("read error and eof", func(t *testing.T) {
		for _, rerr := range []error{errors.New("boom"), io.EOF} {
			f := &fakeTerm{read: func(int) ([]byte, error) { return nil, rerr }}
			in := f.install(t)
			p := NewTTY(Streams{In: in, Err: io.Discard}, Mode{})
			_, err := p.Secret(context.Background(), "T")
			if err == nil || f.restores == 0 {
				t.Errorf("err=%v restores=%d", err, f.restores)
			}
			if errors.Is(rerr, io.EOF) && !errors.Is(err, ErrAborted) {
				t.Errorf("eof: %v", err)
			}
		}
	})
	t.Run("cancel", func(t *testing.T) {
		block := make(chan struct{})
		t.Cleanup(func() { close(block) })
		f := &fakeTerm{read: func(int) ([]byte, error) { <-block; return nil, io.EOF }}
		in := f.install(t)
		p := NewTTY(Streams{In: in, Err: io.Discard}, Mode{})
		ctx, cancel := context.WithCancel(context.Background())
		go func() { time.Sleep(20 * time.Millisecond); cancel() }()
		_, err := p.Secret(ctx, "T")
		if !errors.Is(err, context.Canceled) || f.restores == 0 {
			t.Errorf("err=%v restores=%d: echo may stay off", err, f.restores)
		}
	})
	t.Run("get state fails", func(t *testing.T) {
		f := &fakeTerm{getErr: errors.New("no tty"), read: func(int) ([]byte, error) { t.Error("read started"); return nil, nil }}
		in := f.install(t)
		p := NewTTY(Streams{In: in, Err: io.Discard}, Mode{})
		if _, err := p.Secret(context.Background(), "T"); err == nil {
			t.Error("expected error")
		}
	})
}
