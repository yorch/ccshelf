package ui

import "testing"

func TestSanitize(t *testing.T) {
	tests := []struct{ in, want string }{
		{"plain", "plain"},
		{"a\tb\nc", "a\tb\nc"},
		{"esc\x1b[31m", "esc\uFFFD[31m"},
		{"cr\rx", "cr\uFFFDx"},
		{"nul\x00", "nul\uFFFD"},
		{"bad\xffutf", "bad\uFFFDutf"},
		{"bidi\u202Eevil", "bidi\uFFFDevil"},
		{"sep\u2028x", "sep\uFFFDx"},
		{"日本語", "日本語"},
	}
	for _, tt := range tests {
		if got := Sanitize(tt.in); got != tt.want {
			t.Errorf("Sanitize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDisplayWidthAndTruncate(t *testing.T) {
	widths := map[string]int{
		"abc": 3, "日本": 4, "e\u0301": 1, "": 0, "😀": 2, "a\u200Db": 2, "한글": 4, "Ａ": 2, "✅": 2, "✓": 1,
	}
	for s, w := range widths {
		if got := DisplayWidth(s); got != w {
			t.Errorf("DisplayWidth(%q) = %d, want %d", s, got, w)
		}
	}
	tests := []struct {
		in    string
		width int
		ascii bool
		want  string
	}{
		{"hello", 10, false, "hello"},
		{"hello world", 8, false, "hello w…"},
		{"hello world", 8, true, "hello..."},
		{"日本語です", 6, false, "日本…"},
		{"日本語です", 5, true, "日..."},
		{"hello", 1, false, "…"},
		{"hello", 2, true, ".."},
		{"hello", 0, false, ""},
		{"e\u0301e\u0301e\u0301", 2, false, "e\u0301…"},
	}
	for _, tt := range tests {
		got := Truncate(tt.in, tt.width, tt.ascii)
		if got != tt.want {
			t.Errorf("Truncate(%q, %d, %v) = %q, want %q", tt.in, tt.width, tt.ascii, got, tt.want)
		}
		if tt.width > 0 && DisplayWidth(got) > tt.width {
			t.Errorf("Truncate(%q, %d) too wide: %q", tt.in, tt.width, got)
		}
	}
}
