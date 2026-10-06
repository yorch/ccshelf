package recommend

import (
	"strings"
	"testing"
)

func TestMatch(t *testing.T) {
	tests := []struct {
		pattern, path string
		want          bool
	}{
		{"main.tf", "main.tf", true},
		{"MAIN.TF", "main.tf", true},
		{"main.tf", "MAIN.tf", true},
		{"*.tf", "main.tf", true},
		{"*.tf", "infra/main.tf", false},
		{"**/*.tf", "main.tf", true},
		{"**/*.tf", "a/b/c/main.tf", true},
		{"infra/**", "infra", true},
		{"infra/**", "infra/a/b", true},
		{"infra/**", "other/infra", false},
		{"infra/*/main.tf", "infra/prod/main.tf", true},
		{"infra/*/main.tf", "infra/prod/x/main.tf", false},
		{"infra/**/main.tf", "infra/main.tf", true},
		{"infra/**/main.tf", "infra/a/b/main.tf", true},
		{"file?.txt", "file1.txt", true},
		{"file?.txt", "file12.txt", false},
		{"file?.txt", "file.txt", false},
		{"?", "/", false},
		{"a*b*c", "aXXbYYc", true},
		{"a*b*c", "aXXbYY", false},
		{"*", "", false},
		{"**", "", true},
		{"**", "a/b", true},
		{"a**b", "axxb", true},
		{"a**b", "a/b", false},
		{"/abs/dir", "abs/dir", true},
		{"a//b/./c/", "a/b/c", true},
		{`a\b`, "a/b", true},
		{"[ab]", "a", false},
		{"[ab]", "[ab]", true},
		{"", "a", false},
		{"é*", "École", true},
		{strings.Repeat("a", maxGlobLen+1), "a", false},
	}
	for _, tc := range tests {
		if got := Match(tc.pattern, tc.path); got != tc.want {
			t.Errorf("Match(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

func TestMatchPathologicalIsFast(t *testing.T) {
	p := strings.Repeat("**/", 80) + "x"
	path := strings.Repeat("a/", 60) + "y"
	if Match(p, path) {
		t.Error("must not match")
	}
}

func TestMatchAnyDepth(t *testing.T) {
	tests := []struct {
		pattern, path string
		want          bool
	}{
		{"*.tf", "a/b/main.tf", true},
		{"*.tf", "main.tf", true},
		{"infra/*.tf", "infra/main.tf", true},
		{"infra/*.tf", "x/infra/main.tf", false},
		{"/main.tf", "main.tf", true},
		{"/main.tf", "x/main.tf", false},
		{"package.json", "web/package.json", true},
	}
	for _, tc := range tests {
		if got := matchAnyDepth(tc.pattern, tc.path); got != tc.want {
			t.Errorf("matchAnyDepth(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}
