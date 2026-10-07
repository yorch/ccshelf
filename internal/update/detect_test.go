package update

import "testing"

func TestDetectInstall(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   DetectInput
		kind string
		cmd  string
	}{
		// Homebrew
		{"brew cellar", DetectInput{GOOS: "darwin", Paths: []string{"/opt/homebrew/Cellar/ccshelf/0.1.0/bin/ccshelf"}}, MethodHomebrew, "brew upgrade ccshelf"},
		{"brew apple prefix link", DetectInput{GOOS: "darwin", Paths: []string{"/opt/homebrew/bin/ccshelf"}}, MethodHomebrew, "brew upgrade ccshelf"},
		{"brew intel link resolves", DetectInput{GOOS: "darwin", Paths: []string{"/usr/local/bin/ccshelf", "/usr/local/Cellar/ccshelf/0.1.0/bin/ccshelf"}}, MethodHomebrew, "brew upgrade ccshelf"},
		{"brew caskroom", DetectInput{GOOS: "darwin", Paths: []string{"/opt/homebrew/Caskroom/ccshelf/0.1.0/ccshelf"}}, MethodHomebrew, "brew upgrade ccshelf"},
		{"linuxbrew", DetectInput{GOOS: "linux", Paths: []string{"/home/linuxbrew/.linuxbrew/bin/ccshelf"}}, MethodHomebrew, "brew upgrade ccshelf"},
		{"linuxbrew cellar", DetectInput{GOOS: "linux", Paths: []string{"/home/linuxbrew/.linuxbrew/Cellar/ccshelf/0.1.0/bin/ccshelf"}}, MethodHomebrew, "brew upgrade ccshelf"},
		// Scoop
		{"scoop apps", DetectInput{GOOS: "windows", Paths: []string{`C:\Users\me\scoop\apps\ccshelf\current\ccshelf.exe`}}, MethodScoop, "scoop update ccshelf"},
		{"scoop shim", DetectInput{GOOS: "windows", Paths: []string{`C:\Users\me\scoop\shims\ccshelf.exe`}}, MethodScoop, "scoop update ccshelf"},
		{"scoop global", DetectInput{GOOS: "windows", Paths: []string{`C:\ProgramData\scoop\apps\ccshelf\0.1.0\ccshelf.exe`}}, MethodScoop, "scoop update ccshelf"},
		{"scoop lower case", DetectInput{GOOS: "windows", Paths: []string{`c:\users\me\SCOOP\APPS\ccshelf\current\ccshelf.exe`}}, MethodScoop, "scoop update ccshelf"},
		// WinGet
		{"winget packages", DetectInput{GOOS: "windows", Paths: []string{`C:\Users\me\AppData\Local\Microsoft\WinGet\Packages\ccshelf.ccshelf_Microsoft.Winget.Source_8wekyb3d8bbwe\ccshelf.exe`}}, MethodWinget, "winget upgrade ccshelf.ccshelf"},
		{"winget links", DetectInput{GOOS: "windows", Paths: []string{`C:\Users\me\AppData\Local\Microsoft\WinGet\Links\ccshelf.exe`}}, MethodWinget, "winget upgrade ccshelf.ccshelf"},
		{"windowsapps", DetectInput{GOOS: "windows", Paths: []string{`C:\Users\me\AppData\Local\Microsoft\WindowsApps\ccshelf.exe`}}, MethodWinget, "winget upgrade ccshelf.ccshelf"},
		// go install
		{"gobin", DetectInput{GOOS: "linux", Paths: []string{"/opt/gobin/ccshelf"}, GOBIN: "/opt/gobin"}, MethodGo, "go install github.com/yorch/ccshelf/cmd/ccshelf@latest"},
		{"gopath", DetectInput{GOOS: "linux", Paths: []string{"/work/go/bin/ccshelf"}, GOPATH: "/work/go"}, MethodGo, "go install github.com/yorch/ccshelf/cmd/ccshelf@latest"},
		{"gopath list", DetectInput{GOOS: "linux", Paths: []string{"/b/bin/ccshelf"}, GOPATH: "/a:/b"}, MethodGo, "go install github.com/yorch/ccshelf/cmd/ccshelf@latest"},
		{"default gopath", DetectInput{GOOS: "darwin", Paths: []string{"/Users/me/go/bin/ccshelf"}, Home: "/Users/me"}, MethodGo, "go install github.com/yorch/ccshelf/cmd/ccshelf@latest"},
		{"windows gopath", DetectInput{GOOS: "windows", Paths: []string{`C:\Users\me\go\bin\ccshelf.exe`}, Home: `C:\Users\me`}, MethodGo, "go install github.com/yorch/ccshelf/cmd/ccshelf@latest"},
		{"fork repo in advice", DetectInput{GOOS: "linux", Paths: []string{"/g/bin/ccshelf"}, GOBIN: "/g/bin", Repo: "acme/ccshelf"}, MethodGo, "go install github.com/acme/ccshelf/cmd/ccshelf@latest"},
		// system locations
		{"usr bin", DetectInput{GOOS: "linux", Paths: []string{"/usr/bin/ccshelf"}}, MethodSystem, "update ccshelf with your system's package manager"},
		{"nix", DetectInput{GOOS: "linux", Paths: []string{"/nix/store/abc-ccshelf-0.1.0/bin/ccshelf"}}, MethodSystem, "update ccshelf with your system's package manager"},
		{"snap", DetectInput{GOOS: "linux", Paths: []string{"/snap/ccshelf/current/bin/ccshelf"}}, MethodSystem, "update ccshelf with your system's package manager"},
		// manual
		{"local bin", DetectInput{GOOS: "linux", Paths: []string{"/usr/local/bin/ccshelf"}}, MethodManual, ""},
		{"home bin", DetectInput{GOOS: "linux", Paths: []string{"/home/me/.local/bin/ccshelf"}, Home: "/home/me"}, MethodManual, ""},
		{"mac local", DetectInput{GOOS: "darwin", Paths: []string{"/Users/me/bin/ccshelf"}, Home: "/Users/me"}, MethodManual, ""},
		{"windows local", DetectInput{GOOS: "windows", Paths: []string{`C:\Users\me\bin\ccshelf.exe`}, Home: `C:\Users\me`}, MethodManual, ""},
		{"lookalike", DetectInput{GOOS: "linux", Paths: []string{"/home/me/myhomebrewish/ccshelf"}}, MethodManual, ""},
		{"scoop lookalike", DetectInput{GOOS: "windows", Paths: []string{`C:\tools\scooped\apps\ccshelf.exe`}}, MethodManual, ""},
		{"no paths", DetectInput{GOOS: "linux"}, MethodManual, ""},
		// containers come last
		{"container manual", DetectInput{GOOS: "linux", Paths: []string{"/usr/local/bin/ccshelf"}, InContainer: true}, MethodContainer, "pull or rebuild the image with the newer ccshelf"},
		{"brew in container keeps brew", DetectInput{GOOS: "linux", Paths: []string{"/home/linuxbrew/.linuxbrew/bin/ccshelf"}, InContainer: true}, MethodHomebrew, "brew upgrade ccshelf"},
		// rules are per OS
		{"cellar path on windows is not brew", DetectInput{GOOS: "windows", Paths: []string{`C:\Cellar\ccshelf.exe`}}, MethodManual, ""},
		{"scoop path on linux is not scoop", DetectInput{GOOS: "linux", Paths: []string{"/home/me/scoop/apps/ccshelf/ccshelf"}}, MethodManual, ""},
		{"usr bin on windows", DetectInput{GOOS: "windows", Paths: []string{"/usr/bin/ccshelf"}}, MethodManual, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := DetectInstall(tc.in)
			if m.Kind != tc.kind || m.Command != tc.cmd {
				t.Errorf("DetectInstall = %+v, want kind %s command %q", m, tc.kind, tc.cmd)
			}
			if m.SelfUpdatable() != (tc.kind == MethodManual) {
				t.Errorf("SelfUpdatable = %v for %s", m.SelfUpdatable(), tc.kind)
			}
		})
	}
}
