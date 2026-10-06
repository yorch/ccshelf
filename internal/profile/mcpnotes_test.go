package profile

import (
	"strings"
	"testing"
)

func TestDocumentedWindowsLauncher(t *testing.T) {
	cases := []struct {
		name    string
		command string
		args    []string
		want    bool
	}{
		{"npx pinned", "cmd", []string{"/c", "npx", "-y", "pkg@1.2.3"}, true},
		{"scoped, v prefix, upper case /C, npx.cmd", "cmd", []string{"/C", "NPX.cmd", "-y", "@acme/pkg@v1.0.0-rc.1"}, true},
		{"cmd by path is not the exact shape", `C:\Windows\System32\cmd.exe`, []string{"/c", "npx", "-y", "pkg@1.0.0"}, false},
		{"npx --yes with extra args", "cmd", []string{"/c", "npx", "--yes", "pkg@1.2.3", "--port", "80"}, true},
		{"node is not a runner", "cmd", []string{"/c", "node", "server.js", "pkg@2.0.0"}, false},
		{"hostile: node -e", "cmd", []string{"/c", "node", "-e", "require('child_process')", "x@1.0.0"}, false},
		{"hostile: npx -c", "cmd", []string{"/c", "npx", "-c", "calc", "x@1.0.0"}, false},
		{"hostile: UNC runner", "cmd", []string{"/c", `\\evil\share\npx`, "-y", "pkg@1.0.0"}, false},
		{"hostile: path runner", "cmd", []string{"/c", `C:\Users\Public\npx.cmd`, "-y", "pkg@1.0.0"}, false},
		{"hostile: --package next to pinned decoy", "cmd", []string{"/c", "npx", "-y", "decoy@1.0.0", "--package=evil@latest"}, false},
		{"hostile: --package separate", "cmd", []string{"/c", "npx", "-y", "decoy@1.0.0", "--package", "evil"}, false},
		{"hostile: parenthesis", "cmd", []string{"/c", "npx", "-y", "pkg@1.0.0", "(calc)"}, false},
		{"hostile: bang", "cmd", []string{"/c", "npx", "-y", "pkg@1.0.0", "!x!"}, false},
		{"hostile: single quote", "cmd", []string{"/c", "npx", "-y", "pkg@1.0.0", "'a'"}, false},
		{"npx without -y", "cmd", []string{"/c", "npx", "pkg@1.0.0"}, false},
		{"npx other flag before package", "cmd", []string{"/c", "npx", "--no-install", "pkg@1.0.0"}, false},
		{"partial version", "cmd", []string{"/c", "uvx", "tool@3"}, false},
		{"uvx", "cmd", []string{"/c", "uvx", "tool@0.4.1"}, true},
		{"bunx", "cmd", []string{"/c", "bunx", "tool@3.0.0"}, true},
		{"unpinned", "cmd", []string{"/c", "npx", "-y", "pkg"}, false},
		{"latest tag", "cmd", []string{"/c", "npx", "-y", "pkg@latest"}, false},
		{"range is not a pin", "cmd", []string{"/c", "npx", "-y", "pkg@^1.0.0"}, false},
		{"no package arg", "cmd", []string{"/c", "npx"}, false},
		{"/k is not /c", "cmd", []string{"/k", "npx", "-y", "pkg@1.0.0"}, false},
		{"other runner", "cmd", []string{"/c", "python", "-m", "pkg@1.0.0"}, false},
		{"a second command", "cmd", []string{"/c", "npx", "-y", "pkg@1.0.0", "&", "calc"}, false},
		{"a pipe in an argument", "cmd", []string{"/c", "npx", "-y", "pkg@1.0.0|calc"}, false},
		{"variable expansion", "cmd", []string{"/c", "npx", "-y", "pkg@1.0.0", "%COMSPEC%"}, false},
		{"powershell", "powershell", []string{"-command", "npx", "pkg@1.0.0"}, false},
		{"bash", "bash", []string{"-c", "npx pkg@1.0.0"}, false},
		{"npx directly", "npx", []string{"-y", "pkg@1.0.0"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := MCPServer{Name: "srv", Type: MCPStdio, Command: tc.command, Args: tc.args}
			warns, notes := MCPWarnings(s), MCPNotes(s)
			if got := isDocumentedLauncher(tc.command, tc.args); got != tc.want {
				t.Fatalf("isDocumentedLauncher = %v, want %v", got, tc.want)
			}
			switch {
			case tc.want && (len(warns) != 0 || len(notes) != 1):
				t.Errorf("a documented launcher: warnings %v, notes %v", warns, notes)
			case !tc.want && len(notes) != 0:
				t.Errorf("not documented, but notes %v", notes)
			}
			if !tc.want && tc.command != "npx" && len(warns) == 0 {
				t.Errorf("another shell or launcher must keep its warning")
			}
		})
	}
}

func TestMCPNotesNameTheOverride(t *testing.T) {
	s := MCPServer{
		Name: "figma", Type: MCPStdio, Command: "npx", Args: []string{"-y", "pkg@1.0.0"},
		Windows: &MCPOverride{Command: "cmd", Args: []string{"/c", "npx", "-y", "pkg@1.0.0"}},
	}
	notes := MCPNotes(s)
	if len(notes) != 1 || !strings.Contains(notes[0], `"figma"`) || !strings.HasSuffix(notes[0], " on windows") || !strings.Contains(notes[0], "cmd /c npx -y pkg@1.0.0") {
		t.Errorf("notes = %v", notes)
	}
	if len(MCPWarnings(s)) != 0 {
		t.Errorf("warnings = %v", MCPWarnings(s))
	}
	if n := MCPNotes(MCPServer{Name: "h", Type: MCPHTTP, URL: "https://x.example"}); len(n) != 0 {
		t.Errorf("http server notes = %v", n)
	}
	// A control character disqualifies the launcher: no note, and the warning stays.
	ctl := MCPServer{Name: "x", Type: MCPStdio, Command: "cmd", Args: []string{"/c", "npx", "-y", "pkg@1.0.0", "a\x1b[2Jb"}}
	if n := MCPNotes(ctl); len(n) != 0 {
		t.Errorf("notes = %q", n)
	}
	if w := MCPWarnings(ctl); len(w) != 1 || strings.Contains(w[0], "\x1b") {
		t.Errorf("warnings = %q", w)
	}
}
