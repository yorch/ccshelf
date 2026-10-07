package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/yorch/ccshelf/internal/ui"
)

func TestGroupedHelp(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"help"}, nil} {
		var out, errb bytes.Buffer
		if code := Execute(context.Background(), testEnv(&out, &errb), args); code != ui.ExitOK {
			t.Fatalf("%v: exit %d: %s", args, code, &errb)
		}
		for _, want := range []string{"Getting started:", "Profiles:", "Discovery and diagnostics:", "Org data repo maintenance:", "Setup and utilities:", "ccshelf new my-profile"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("%v: missing %q in %s", args, want, &out)
			}
		}
		if strings.Contains(out.String(), "Additional Commands:") || strings.Contains(out.String(), "\x1b") || errb.Len() != 0 {
			t.Errorf("unexpected ungrouped commands, escapes or stderr: %s%s", &out, &errb)
		}
	}
}

func TestUsageHelpHints(t *testing.T) {
	for _, tc := range []struct {
		args []string
		path string
	}{
		{[]string{"--bogus"}, "ccshelf"},
		{[]string{"nope"}, "ccshelf"},
		{[]string{"lint", "--bogus"}, "ccshelf lint"},
		{[]string{"account", "ls", "extra"}, "ccshelf account ls"},
	} {
		for _, machine := range []bool{false, true} {
			var out, errb bytes.Buffer
			args := append([]string{}, tc.args...)
			if machine {
				args = append([]string{"--json"}, args...)
			}
			if code := Execute(context.Background(), testEnv(&out, &errb), args); code != ui.ExitUsage {
				t.Fatalf("%v: exit %d: %s", args, code, &errb)
			}
			want := "run " + tc.path + " --help for usage and examples"
			if machine {
				var e struct {
					Kind string
					Data struct {
						Hint string
						Code int
					}
				}
				if err := json.Unmarshal(errb.Bytes(), &e); err != nil {
					t.Fatal(err)
				}
				if e.Data.Hint != want || e.Data.Code != ui.ExitUsage || e.Kind != "error" {
					t.Errorf("%v: unexpected JSON: %s", args, &errb)
				}
			} else if !strings.Contains(errb.String(), "hint: "+want) {
				t.Errorf("%v: missing hint: %s", args, &errb)
			}
			if out.Len() != 0 {
				t.Errorf("usage error wrote stdout: %s", &out)
			}
		}
	}
}

func TestEarlyJSONRequest(t *testing.T) {
	var out, errb bytes.Buffer
	root := NewRoot(testEnv(&out, &errb))
	cmd, _, err := root.Find([]string{"new"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"--json", "unknown"}, true},
		{[]string{"--json=true", "unknown"}, true},
		{[]string{"--json", "--json=false", "unknown"}, false},
		{[]string{"--json=invalid", "unknown"}, false},
		{[]string{"--config", "--json", "unknown"}, false},
		{[]string{"new", "--description", "--json"}, false},
		{[]string{"run", "mine", "--", "--json"}, false},
	} {
		if got := requestsJSON(tc.args, cmd, root); got != tc.want {
			t.Errorf("%v: %v, want %v", tc.args, got, tc.want)
		}
	}
}

func TestUsageHintPreservesSpecificErrors(t *testing.T) {
	var out, errb bytes.Buffer
	root := NewRoot(testEnv(&out, &errb))
	for _, err := range []error{
		ui.MissingFlags("profile name", "--name"),
		usageHint{error: errors.New("invalid"), hint: "a specific fix"},
	} {
		got := withUsageHint(err, root, root)
		if !errors.Is(got, err) {
			t.Errorf("original error lost: %v", got)
		}
		var wantHint, gotHint interface{ Hint() string }
		if errors.As(err, &wantHint) && (!errors.As(got, &gotHint) || gotHint.Hint() != wantHint.Hint()) {
			t.Errorf("specific hint replaced: %v", got)
		}
	}
	err := withUsageHint(ui.Usage(errors.New("invalid")), root, root)
	if exitCodeOf(err) != ui.ExitUsage {
		t.Fatal("wrapping changed exit code")
	}
}
