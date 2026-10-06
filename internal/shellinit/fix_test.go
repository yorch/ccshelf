package shellinit

import (
	"os/exec"
	"strings"
	"testing"
)

func TestIsAbsUNCNeedsShare(t *testing.T) {
	tests := []struct {
		p    string
		want bool
	}{
		{`\\x`, false},
		{`\\`, false},
		{`\\server\`, false},
		{`\\\share`, false},
		{`\\server\share`, true},
		{`\\server\share\ccshelf.exe`, true},
		{`\\?\C:\x`, true},
		{`C:\x`, true},
		{`C:`, false},
		{`rel\x`, false},
	}
	for _, tt := range tests {
		if got := isAbs("windows", tt.p); got != tt.want {
			t.Errorf("isAbs(windows, %q) = %v, want %v", tt.p, got, tt.want)
		}
	}
}

func TestCmdHintIsQuoted(t *testing.T) {
	out, err := Generate(Cmd, "windows", []string{"a"}, winExe)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `> "%TEMP%\ccshelf-init.cmd" && call "%TEMP%\ccshelf-init.cmd"`) {
		t.Errorf("hint not quoted:\n%s", out)
	}
}

func TestExeRejectsInvisible(t *testing.T) {
	for _, exe := range []string{"/opt/a\u200Bb", "/opt/a\u2060b", "/opt/a\nb", "/opt/a\u202Eb"} {
		if _, err := Generate(Bash, "linux", []string{"a"}, exe); err == nil {
			t.Errorf("accepted %q", exe)
		}
	}
}

// TestAliasDoesNotBreakEval loads the generated script in a real shell that
// already has an alias with the function's name.
func TestAliasDoesNotBreakEval(t *testing.T) {
	for _, shell := range []string{Bash, Zsh} {
		t.Run(shell, func(t *testing.T) {
			path, err := exec.LookPath(shell)
			if err != nil {
				t.Skipf("%s is not installed", shell)
			}
			script, err := Generate(shell, "linux", []string{"x"}, "/bin/echo")
			if err != nil {
				t.Fatal(err)
			}
			// -f / --norc avoid user startup files; aliases need expand_aliases in bash.
			pre := "alias cs-x='echo ALIAS'\n"
			if shell == Bash {
				pre = "shopt -s expand_aliases\n" + pre
			}
			cmd := exec.Command(path, "-c", pre+"eval \"$SCRIPT\"\ncs-x hello") //nolint:gosec // test with fixed shell names
			cmd.Env = []string{"SCRIPT=" + script, "PATH=/usr/bin:/bin"}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			if got := strings.TrimSpace(string(out)); got != "run x hello" {
				t.Errorf("got %q (alias shadowed or parse error)", got)
			}
		})
	}
}
