package ui

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if norm(got) != norm(string(want)) {
		t.Errorf("%s mismatch\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func TestTableGolden(t *testing.T) {
	headers := []string{"NAME", "DESCRIPTION", "PLUGINS"}
	rows := [][]string{
		{"frontend", "React, CSS and accessibility work", "3"},
		{"sre", "Incident response and observability for the platform team", "12"},
		{"日本語", "wide characters \x1b[31mred\x1b[0m", "1"},
		{"short"},
	}
	var b bytes.Buffer
	if err := Table(&b, headers, rows, Mode{}); err != nil {
		t.Fatal(err)
	}
	golden(t, "table_wide.golden", b.String())

	b.Reset()
	if err := Table(&b, headers, rows, Mode{Width: 50, Plain: true}); err != nil {
		t.Fatal(err)
	}
	golden(t, "table_narrow_plain.golden", b.String())
	for _, line := range strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n") {
		if DisplayWidth(line) > 50 {
			t.Errorf("line wider than 50: %q", line)
		}
	}
}

func TestTableEdges(t *testing.T) {
	var b bytes.Buffer
	if err := Table(&b, nil, nil, Mode{}); err != nil || b.Len() != 0 {
		t.Errorf("empty table: %q %v", b.String(), err)
	}
	b.Reset()
	if err := Table(&b, nil, [][]string{{"a", "b"}}, Mode{}); err != nil || b.String() != "a  b\n" {
		t.Errorf("no headers: %q %v", b.String(), err)
	}
	b.Reset()
	// Width below the minimum column: rows are still printed, never panic.
	if err := Table(&b, []string{"A", "B"}, [][]string{{"aaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbb"}}, Mode{Width: 5}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "…") {
		t.Errorf("expected truncation: %q", b.String())
	}
	if err := Table(failWriter{}, []string{"A"}, nil, Mode{}); err == nil {
		t.Error("write error not returned")
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestWriteJSON(t *testing.T) {
	type item struct {
		Name    string            `json:"name"`
		Plugins []string          `json:"plugins"`
		Extra   map[string]string `json:"extra"`
	}
	var b bytes.Buffer
	err := WriteJSON(&b, "profile.list", []item{{Name: "a<b>", Plugins: []string{"x"}, Extra: map[string]string{"z": "1", "a": "2"}}})
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "json_list.golden", b.String())

	b.Reset()
	if err := WriteJSON(&b, "nothing", nil); err != nil || !strings.Contains(b.String(), `"data": null`) {
		t.Errorf("nil data: %q %v", b.String(), err)
	}
	if err := WriteJSON(&b, "", 1); err == nil {
		t.Error("empty kind accepted")
	}
	if err := WriteJSON(&b, "bad", make(chan int)); err == nil {
		t.Error("unencodable value accepted")
	}
}

func TestStatusAndColor(t *testing.T) {
	if got := Status(Mode{}, LevelOK, "fine"); got != "ok: fine" {
		t.Errorf("got %q", got)
	}
	if got := Status(Mode{}, LevelWarn, "hm"); got != "warn: hm" {
		t.Errorf("got %q", got)
	}
	if got := Status(Mode{Color: true}, LevelError, "x"); got != "\x1b[31merror:\x1b[0m x" {
		t.Errorf("got %q", got)
	}
	if got := Bold(Mode{Color: true}, "b"); got != "\x1b[1mb\x1b[0m" {
		t.Errorf("got %q", got)
	}
	if got := Dim(Mode{}, "d"); got != "d" {
		t.Errorf("got %q", got)
	}
	if got := Colorize(Mode{Color: true}, "1", ""); got != "" {
		t.Errorf("empty text colored: %q", got)
	}
}

func TestRedact(t *testing.T) {
	env := map[string]string{"B": "secret1", "A": "secret2"}
	r := RedactEnv(env)
	if len(r) != 2 || r["A"] != RedactedValue || r["B"] != RedactedValue {
		t.Errorf("got %v", r)
	}
	if env["A"] != "secret2" {
		t.Error("input modified")
	}
	if got := strings.Join(RedactEnvList(env), ","); got != "A=<redacted>,B=<redacted>" {
		t.Errorf("got %q", got)
	}
	tests := []struct{ in, want []string }{
		{[]string{"run", "sre", "--token", "abc", "--from", "x"}, []string{"run", "sre", "--token", "<redacted>", "--from", "x"}},
		{[]string{"--api-key=abc", "--name=n"}, []string{"--api-key=<redacted>", "--name=n"}},
		{[]string{"MY_SECRET=1", "PLAIN=2", "novalue"}, []string{"MY_SECRET=<redacted>", "PLAIN=2", "novalue"}},
		{[]string{"--password"}, []string{"--password"}},
		{[]string{"--monkey", "banana"}, []string{"--monkey", "banana"}},
		{[]string{"--sshkey", "k"}, []string{"--sshkey", "k"}},
		{[]string{"--deploy-key", "k"}, []string{"--deploy-key", "<redacted>"}},
		{nil, []string{}},
	}
	for _, tt := range tests {
		got := RedactArgs(tt.in)
		if strings.Join(got, "\x00") != strings.Join(tt.want, "\x00") {
			t.Errorf("RedactArgs(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// norm makes comparisons independent of git line-ending conversion.
func norm(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }
