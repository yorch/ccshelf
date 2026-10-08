package update

import (
	"path/filepath"
	"strings"
)

// Install method kinds reported by [DetectInstall].
const (
	// MethodManual is a binary the user (or the install script) put in place:
	// ccshelf may replace it.
	MethodManual = "manual"
	// MethodHomebrew is a Homebrew formula or cask.
	MethodHomebrew = "homebrew"
	// MethodScoop is a Scoop app.
	MethodScoop = "scoop"
	// MethodWinget is a WinGet package.
	MethodWinget = "winget"
	// MethodGo is "go install".
	MethodGo = "go"
	// MethodSystem is a distribution package or an immutable system location.
	MethodSystem = "system"
	// MethodContainer is a container image.
	MethodContainer = "container"
)

// Method is how this copy of ccshelf was installed.
type Method struct {
	// Kind is one of the Method* constants.
	Kind string
	// Command is what to run instead of self-updating. It is empty for
	// MethodManual.
	Command string
}

// SelfUpdatable reports whether ccshelf may replace its own binary.
func (m Method) SelfUpdatable() bool { return m.Kind == MethodManual }

// DetectInput is what DetectInstall looks at. Everything is a value, so the
// detector is a pure function and tests can cover every OS on any machine.
type DetectInput struct {
	// GOOS selects the path syntax and the rules that apply.
	GOOS string
	// Paths are the executable as invoked and as resolved through symlinks
	// (either may be empty). DetectInstall recognizes a package manager by
	// either.
	Paths []string
	// Home, GOBIN and GOPATH are the user's home directory and the Go
	// environment values (GOPATH may hold several entries).
	Home, GOBIN, GOPATH string
	// InContainer is true inside a container (see InContainer in the CLI).
	InContainer bool
	// Repo is the "owner/name" used in the "go install" advice.
	Repo string
}

// norm turns a path into a lower-case, forward-slash form with a leading and
// trailing slash, so that "/x/" component tests are simple substring tests.
func norm(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	return "/" + strings.Trim(strings.ToLower(p), "/") + "/"
}

func dirOf(p string, goos string) string {
	if goos == "windows" {
		p = strings.ReplaceAll(p, `\`, "/")
		if i := strings.LastIndex(p, "/"); i >= 0 {
			return p[:i]
		}
		return p
	}
	return filepath.Dir(p)
}

// rule is one row of the detection table.
type rule struct {
	kind    string
	command func(in DetectInput) string
	match   func(in DetectInput, p, dir string) bool
	goos    []string // empty: every OS
}

func has(p string, parts ...string) bool {
	for _, s := range parts {
		if strings.Contains(p, s) {
			return true
		}
	}
	return false
}

// detectRules are tried in order; the first rule that matches any path wins.
var detectRules = []rule{
	{
		kind:    MethodHomebrew,
		command: func(DetectInput) string { return "brew upgrade ccshelf" },
		match: func(_ DetectInput, p, _ string) bool {
			return has(p, "/cellar/", "/caskroom/", "/linuxbrew/", "/homebrew/")
		},
		goos: []string{"darwin", "linux"},
	},
	{
		kind:    MethodScoop,
		command: func(DetectInput) string { return "scoop update ccshelf" },
		match: func(_ DetectInput, p, _ string) bool {
			return has(p, "/scoop/apps/", "/scoop/shims/", "/scoop/persist/")
		},
		goos: []string{"windows"},
	},
	{
		kind:    MethodWinget,
		command: func(DetectInput) string { return "winget upgrade ccshelf.ccshelf" },
		match: func(_ DetectInput, p, _ string) bool {
			return has(p, "/microsoft/winget/packages/", "/microsoft/winget/links/", "/microsoft/windowsapps/")
		},
		goos: []string{"windows"},
	},
	{
		kind: MethodGo,
		command: func(in DetectInput) string {
			repo := in.Repo
			if repo == "" {
				repo = "yorch/ccshelf"
			}
			return "go install github.com/" + repo + "/cmd/ccshelf@latest"
		},
		match: func(in DetectInput, _, dir string) bool {
			d := norm(dir)
			if in.GOBIN != "" && d == norm(in.GOBIN) {
				return true
			}
			gopath := in.GOPATH
			if gopath == "" && in.Home != "" {
				gopath = in.Home + "/go"
			}
			sep := ":"
			if in.GOOS == "windows" {
				sep = ";"
			}
			for _, g := range strings.Split(gopath, sep) {
				if g != "" && d == norm(g+"/bin") {
					return true
				}
			}
			return false
		},
	},
	{
		kind:    MethodSystem,
		command: func(DetectInput) string { return "update ccshelf with your system's package manager" },
		match: func(_ DetectInput, p, dir string) bool {
			d := norm(dir)
			return d == "/usr/bin/" || d == "/usr/sbin/" || d == "/bin/" || d == "/sbin/" ||
				has(p, "/nix/store/", "/snap/", "/usr/lib/", "/usr/libexec/")
		},
		goos: []string{"linux", "darwin"},
	},
}

// DetectInstall recognizes a copy of ccshelf that a package manager (or an
// image) owns, which ccshelf must not replace behind its back. The table is
// ordered. The first rule matching any of the paths decides. A container is
// reported last, so a Homebrew or Go install inside one still gets its own
// command.
func DetectInstall(in DetectInput) Method {
	for _, r := range detectRules {
		if len(r.goos) > 0 && !contains(r.goos, in.GOOS) {
			continue
		}
		for _, raw := range in.Paths {
			if raw == "" {
				continue
			}
			if r.match(in, norm(raw), dirOf(raw, in.GOOS)) {
				return Method{Kind: r.kind, Command: r.command(in)}
			}
		}
	}
	if in.InContainer {
		return Method{Kind: MethodContainer, Command: "pull or rebuild the image with the newer ccshelf"}
	}
	return Method{Kind: MethodManual}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
