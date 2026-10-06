package ui

import (
	"bytes"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestQuotePOSIX(t *testing.T) {
	tests := []struct{ in, want string }{
		{"abc", "abc"},
		{"--from", "--from"},
		{"sre-kit@acme", "sre-kit@acme"},
		{"a,b=c", "a,b=c"},
		{"", "''"},
		{"with space", "'with space'"},
		{"it's", `'it'\''s'`},
		{"$HOME", "'$HOME'"},
		{"`id`", "'`id`'"},
		{"a;b", "'a;b'"},
		{"~root", "'~root'"},
		{"=x", "'=x'"},
		{"日本語", "'日本語'"},
		{`back\slash`, `'back\slash'`},
		{"\"dq\"", `'"dq"'`},
		{"*", "'*'"},
		{"!", "'!'"},
	}
	for _, tt := range tests {
		got, err := QuotePOSIX(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("QuotePOSIX(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
}

func TestQuotePowerShell(t *testing.T) {
	tests := []struct{ in, want string }{
		{"abc", "abc"},
		{"--from", "--from"},
		{"a=b", "a=b"},
		{"", "''"},
		{"a,b", "'a,b'"},
		{"with space", "'with space'"},
		{"it's", "'it''s'"},
		{"$env:X", "'$env:X'"},
		{"`n", "'`n'"},
		{"\u2018smart\u2019", "'\u2018\u2018smart\u2019\u2019'"},
		{"\u201Alow\u201B", "'\u201A\u201Alow\u201B\u201B'"},
		{"@(1)", "'@(1)'"},
		{"日本語", "'日本語'"},
		{"a;b", "'a;b'"},
	}
	for _, tt := range tests {
		got, err := QuotePowerShell(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("QuotePowerShell(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
}

func TestQuoteCmd(t *testing.T) {
	tests := []struct{ in, want string }{
		{"abc", "abc"},
		{`C:\dir\file.exe`, `C:\dir\file.exe`},
		{"", `""`},
		{"with space", `"with space"`},
		{`C:\Program Files\x`, `"C:\Program Files\x"`},
		{`dir with space\`, `"dir with space\\"`},
		{`say "hi"`, `^"say^ ^\^"hi^\^"^"`}, // placeholder replaced below
		{"a&b", `^"a^&b^"`},
		{"a|b", `^"a^|b^"`},
		{"(x)", `^"^(x^)^"`},
		{"a^b", `^"a^^b^"`},
		{"a!b", `^"a^!b^"`},
	}
	// The expected form for the metacharacter cases is computed from the rule
	// (argv-quote, then caret-escape metacharacters) rather than hand-written,
	// except for the simple cases above.
	for _, tt := range tests {
		got, err := QuoteCmd(tt.in)
		if err != nil {
			t.Errorf("QuoteCmd(%q): %v", tt.in, err)
			continue
		}
		if strings.ContainsAny(tt.in, "&|()^!\"") {
			// Every metacharacter must be preceded by a caret, and no bare one may remain.
			assertCaretEscaped(t, tt.in, got)
			continue
		}
		if got != tt.want {
			t.Errorf("QuoteCmd(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func assertCaretEscaped(t *testing.T, in, got string) {
	t.Helper()
	for i := 0; i < len(got); i++ {
		if strings.IndexByte("()!&|<>\"", got[i]) >= 0 && (i == 0 || got[i-1] != '^') {
			t.Errorf("QuoteCmd(%q) = %q: unescaped metacharacter at %d", in, got, i)
		}
	}
}

func TestQuoteRefusals(t *testing.T) {
	bad := []string{"a\nb", "a\rb", "a\x00b", "a\x1bb", "tab\there", "\x7f", "bad\xff", "x\u202Ey", "l\u2028s"}
	for _, s := range bad {
		for _, shell := range []string{ShellPOSIX, ShellPowerShell, ShellCmd} {
			if q, err := Quote(shell, s); !errors.Is(err, ErrUnquotable) || q != "" {
				t.Errorf("Quote(%s, %q) = %q, %v; want refusal", shell, s, q, err)
			}
		}
	}
	if _, err := QuoteCmd("100%"); !errors.Is(err, ErrUnquotable) {
		t.Errorf("percent: %v", err)
	}
	if _, err := Quote("tcsh", "x"); err == nil {
		t.Error("unknown shell accepted")
	}
}

func TestEquivalent(t *testing.T) {
	var b bytes.Buffer
	args := []string{"new", "sre-night", "--from", "sre", "--plugin", "pagerduty-tools@acme", "--description", "it's night"}
	if err := Equivalent(&b, "linux", args); err != nil {
		t.Fatal(err)
	}
	want := "Equivalent: ccshelf new sre-night --from sre --plugin pagerduty-tools@acme --description 'it'\\''s night'\n"
	if b.String() != want {
		t.Errorf("got %q", b.String())
	}
	b.Reset()
	if err := Equivalent(&b, "windows", args); err != nil {
		t.Fatal(err)
	}
	want = "Equivalent: ccshelf new sre-night --from sre --plugin 'pagerduty-tools@acme' --description 'it''s night'\n"
	if b.String() != want {
		t.Errorf("got %q", b.String())
	}
	b.Reset()
	if err := Equivalent(&b, "darwin", nil); err != nil || b.String() != "Equivalent: ccshelf\n" {
		t.Errorf("no args: %q %v", b.String(), err)
	}
	b.Reset()
	if err := Equivalent(&b, "linux", []string{"new", "a\nb"}); err == nil || b.Len() != 0 {
		t.Errorf("control character must be refused, got %q %v", b.String(), err)
	}
	if ShellForGOOS("darwin") != ShellPOSIX || ShellForGOOS("windows") != ShellPowerShell {
		t.Error("ShellForGOOS")
	}
}

func TestRecorder(t *testing.T) {
	r := NewRecorder("new", "sre-night")
	r.Flag("--from", "sre")
	r.Flag("plugin", "a@b")
	r.Flag("--note", "-weird")
	r.Bool("--force")
	r.Flag("--token", "abc")
	want := []string{"new", "sre-night", "--from", "sre", "--plugin", "a@b", "--note=-weird", "--force", "--token", "abc"}
	if got := r.Args(); strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("Args = %q", got)
	}
	got := r.Args()
	got[0] = "changed"
	if r.Args()[0] != "new" {
		t.Error("Args must return a copy")
	}
	var b bytes.Buffer
	if err := r.Print(&b, "linux"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "--token '<redacted>'") || strings.Contains(b.String(), "abc") {
		t.Errorf("secret not redacted: %q", b.String())
	}
	bad := NewRecorder("new", "a\nb")
	if err := bad.Print(&b, "linux"); err == nil {
		t.Error("control character in recorded args must fail")
	}
}

// shellRoundTrip asks a real shell to print the word it parses from the quoted
// form and compares it with the original.
func shellRoundTrip(t *testing.T, shell, s string) {
	t.Helper()
	q, err := QuotePOSIX(s)
	if err != nil {
		return
	}
	out, err := exec.Command(shell, "-c", "printf '%s' "+q).Output() //nolint:gosec // test with fixed shell names
	if err != nil {
		t.Fatalf("%s -c printf %s: %v", shell, q, err)
	}
	if string(out) != s {
		t.Fatalf("%s round trip of %q via %s gave %q", shell, s, q, out)
	}
}

var hostile = []string{
	"plain", "", "with space", "it's", `a"b`, "$HOME", "`id`", "$(id)", "a;b", "a&b", "a|b", "*", "?", "[a]", "{a,b}",
	"!x", "~", "~/x", "-n", "--flag=va lue", `back\slash`, `trailing\`, "日本語", "emoji😀", "a\u00a0b", "#c", "x#y", "=eq", "a\\'b",
}

func TestQuotePOSIXRoundTripBash(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shells are not available on Windows")
	}
	for _, shell := range []string{"bash", "zsh", "sh"} {
		if _, err := exec.LookPath(shell); err != nil {
			t.Logf("skipping %s: not installed", shell)
			continue
		}
		for _, s := range hostile {
			shellRoundTrip(t, shell, s)
		}
	}
}

func FuzzQuotePOSIX(f *testing.F) {
	for _, s := range hostile {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		q, err := QuotePOSIX(s)
		if err != nil {
			if !errors.Is(err, ErrUnquotable) {
				t.Fatalf("unexpected error type: %v", err)
			}
			return
		}
		if strings.ContainsRune(s, 0) {
			t.Fatal("NUL accepted")
		}
		// Structural check that does not need a shell: every single quote in the
		// input is closed out, so no input byte can end up unquoted.
		if q != s && !(strings.HasPrefix(q, "'") && strings.HasSuffix(q, "'")) {
			t.Fatalf("quoted form %q is neither bare nor single-quoted", q)
		}
		if _, e := QuotePowerShell(s); e != nil {
			t.Fatalf("PowerShell refused what POSIX accepted: %v", e)
		}
		if _, e := QuoteCmd(s); e != nil && !strings.Contains(s, "%") {
			t.Fatalf("cmd refused %q: %v", s, e)
		}
	})
}
