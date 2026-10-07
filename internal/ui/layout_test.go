package ui

import (
	"bytes"
	"strings"
	"testing"
)

func TestStackedTable(t *testing.T) {
	for _, plain := range []bool{false, true} {
		var b bytes.Buffer
		err := Table(&b, []string{"NAME", "DESCRIPTION", "OWNER", "STATUS", "KIND"}, [][]string{
			{"web", "A complete description without losing details", "@example/team", "active", "personal"},
			{"日本語", "Hostile\ntext\x1b[31m", "", "", "personal"},
		}, Mode{Width: 32, Plain: plain})
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(b.String(), "\n") {
			if DisplayWidth(line) > 32 {
				t.Errorf("overflow: %q", line)
			}
		}
		for _, want := range []string{"NAME: web", "description without losing\ndetails", "OWNER: @example/team", "NAME: 日本語", "Hostile text?[31m"} {
			if !strings.Contains(b.String(), want) {
				t.Errorf("missing %q in %s", want, b.String())
			}
		}
		if strings.ContainsAny(b.String(), "\x1b…") {
			t.Errorf("escape or truncation in %q", b.String())
		}
	}
	if err := Table(failWriter{}, []string{"NAME", "DESCRIPTION"}, [][]string{{"longname", "longdescription"}}, Mode{Width: 5}); err == nil {
		t.Fatal("stacked output must propagate write errors")
	}
}

func TestWrapLine(t *testing.T) {
	for _, tc := range []struct {
		text  string
		width int
		want  string
	}{
		{"a complete description", 12, "a complete|description"},
		{"abcdefghijk", 4, "abcd|efgh|ijk"},
		{"日本語", 4, "日本|語"},
		{"日本語", 1, "?|?|?"},
		{"e\u0301abcd", 3, "e\u0301ab|cd"},
	} {
		got := wrapLine(tc.text, tc.width)
		if strings.Join(got, "|") != tc.want {
			t.Errorf("wrapLine(%q, %d) = %q, want %q", tc.text, tc.width, got, tc.want)
		}
		for _, line := range got {
			if DisplayWidth(line) > tc.width {
				t.Errorf("overflow: %q", line)
			}
		}
	}
}

func TestEmptyState(t *testing.T) {
	var b bytes.Buffer
	if err := EmptyState(&b, "no results\nforged", "try\x1b again\nhint: forged"); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != "no results�forged\nhint: try� again�hint: forged\n" {
		t.Errorf("unsanitized empty state: %q", got)
	}
	if err := EmptyState(failWriter{}, "empty", "try again"); err == nil {
		t.Fatal("write error not propagated")
	}
}
