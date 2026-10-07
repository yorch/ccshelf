package version

import (
	"encoding/json"
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// Build identity. The linker overrides these with -X; the defaults mark a
// build that did not go through the release pipeline.
var (
	// Version is the release version without a leading "v" (for example
	// "0.1.0"), or "dev" for an untagged build.
	Version = "dev"
	// Commit is the source commit hash, or "none".
	Commit = "none"
	// Date is the build or commit date, or "unknown".
	Date = "unknown"
	// Repo is the GitHub "owner/name" this binary was released from; "ccshelf
	// update" looks for releases there. The linker sets it for release builds.
	Repo = "yorch/ccshelf"
)

// Placeholder values used when nothing better is known.
const (
	defaultVersion = "dev"
	defaultCommit  = "none"
	defaultDate    = "unknown"
	shortCommitLen = 7
)

// Details is the build identity in a form suitable for --json output.
type Details struct {
	// Version is the version without a leading "v".
	Version string `json:"version"`
	// Commit is the abbreviated source commit hash.
	Commit string `json:"commit"`
	// Date is the build date.
	Date string `json:"date"`
	// GoVersion is the Go toolchain that built the binary.
	GoVersion string `json:"goVersion"`
	// OS is the target operating system (GOOS).
	OS string `json:"os"`
	// Arch is the target architecture (GOARCH).
	Arch string `json:"arch"`
}

// Info returns the build identity, filling gaps from the build info embedded
// by the Go toolchain when the linker flags were not set.
func Info() Details {
	bi, ok := debug.ReadBuildInfo()
	return resolve(Version, Commit, Date, bi, ok, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

// String returns the one-line description printed by --version, for example
// "ccshelf v0.1.0 (commit abc1234, built 2026-10-06, go1.27.1, darwin/arm64)".
func String() string {
	return format(Info())
}

// format renders d as the --version line.
func format(d Details) string {
	v := d.Version
	if v != defaultVersion && !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return fmt.Sprintf("ccshelf %s (commit %s, built %s, %s, %s/%s)",
		v, d.Commit, d.Date, d.GoVersion, d.OS, d.Arch)
}

// JSON returns d encoded as indented JSON.
func (d Details) JSON() ([]byte, error) {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode version info: %w", err)
	}
	return b, nil
}

// resolve merges linker-provided values with embedded build info. It is a
// pure function so that tests can drive every branch.
func resolve(version, commit, date string, bi *debug.BuildInfo, haveBI bool, goVersion, goos, goarch string) Details {
	if haveBI && bi != nil {
		if version == defaultVersion {
			mv := bi.Main.Version
			if mv != "" && mv != "(devel)" {
				version = mv
			}
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if commit == defaultCommit && s.Value != "" {
					commit = s.Value
				}
			case "vcs.time":
				if date == defaultDate && s.Value != "" {
					date = s.Value
				}
			}
		}
	}
	if len(commit) > shortCommitLen {
		commit = commit[:shortCommitLen]
	}
	if version != defaultVersion {
		version = strings.TrimPrefix(version, "v")
	}
	if i := strings.IndexByte(date, 'T'); i > 0 {
		date = date[:i]
	}
	return Details{Version: version, Commit: commit, Date: date, GoVersion: goVersion, OS: goos, Arch: goarch}
}
