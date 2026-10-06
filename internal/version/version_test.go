package version

import (
	"encoding/json"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	bi := &debug.BuildInfo{
		Main: debug.Module{Version: "v1.2.3"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0123456789abcdef"},
			{Key: "vcs.time", Value: "2026-10-06T12:00:00Z"},
			{Key: "other", Value: "x"},
		},
	}
	devel := &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}

	tests := []struct {
		name                  string
		version, commit, date string
		bi                    *debug.BuildInfo
		haveBI                bool
		want                  Details
	}{
		{
			name:    "ldflags win over build info",
			version: "0.1.0", commit: "abcdef1234567", date: "2026-01-02",
			bi: bi, haveBI: true,
			want: Details{Version: "0.1.0", Commit: "abcdef1", Date: "2026-01-02"},
		},
		{
			name:    "ldflags version with v prefix is trimmed",
			version: "v0.1.0", commit: "none", date: "unknown",
			bi: nil, haveBI: false,
			want: Details{Version: "0.1.0", Commit: "none", Date: "unknown"},
		},
		{
			name:    "fallback to build info",
			version: "dev", commit: "none", date: "unknown",
			bi: bi, haveBI: true,
			want: Details{Version: "1.2.3", Commit: "0123456", Date: "2026-10-06"},
		},
		{
			name:    "devel main version stays dev",
			version: "dev", commit: "none", date: "unknown",
			bi: devel, haveBI: true,
			want: Details{Version: "dev", Commit: "none", Date: "unknown"},
		},
		{
			name:    "no build info",
			version: "dev", commit: "none", date: "unknown",
			bi: nil, haveBI: false,
			want: Details{Version: "dev", Commit: "none", Date: "unknown"},
		},
		{
			name:    "haveBI true but nil info",
			version: "dev", commit: "none", date: "unknown",
			bi: nil, haveBI: true,
			want: Details{Version: "dev", Commit: "none", Date: "unknown"},
		},
		{
			name:    "RFC3339 date from ldflags is cut to the day",
			version: "0.2.0", commit: "abc", date: "2026-10-06T01:02:03Z",
			bi: nil, haveBI: false,
			want: Details{Version: "0.2.0", Commit: "abc", Date: "2026-10-06"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolve(tt.version, tt.commit, tt.date, tt.bi, tt.haveBI, "go1.27.1", "linux", "amd64")
			tt.want.GoVersion, tt.want.OS, tt.want.Arch = "go1.27.1", "linux", "amd64"
			if got != tt.want {
				t.Errorf("resolve() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestFormat(t *testing.T) {
	tests := []struct {
		name string
		in   Details
		want string
	}{
		{
			"release",
			Details{Version: "0.1.0", Commit: "abc1234", Date: "2026-10-06", GoVersion: "go1.27.1", OS: "darwin", Arch: "arm64"},
			"ccshelf v0.1.0 (commit abc1234, built 2026-10-06, go1.27.1, darwin/arm64)",
		},
		{
			"dev build has no v prefix",
			Details{Version: "dev", Commit: "none", Date: "unknown", GoVersion: "go1.27.1", OS: "linux", Arch: "amd64"},
			"ccshelf dev (commit none, built unknown, go1.27.1, linux/amd64)",
		},
		{
			"already prefixed",
			Details{Version: "v1.0.0", Commit: "c", Date: "d", GoVersion: "g", OS: "o", Arch: "a"},
			"ccshelf v1.0.0 (commit c, built d, g, o/a)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := format(tt.in); got != tt.want {
				t.Errorf("format() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStringAndInfo(t *testing.T) {
	s := String()
	if !strings.HasPrefix(s, "ccshelf ") {
		t.Errorf("String() = %q, want ccshelf prefix", s)
	}
	d := Info()
	if d.OS != runtime.GOOS || d.Arch != runtime.GOARCH || d.GoVersion != runtime.Version() {
		t.Errorf("Info() platform = %s/%s %s", d.OS, d.Arch, d.GoVersion)
	}
	if d.Version == "" || d.Commit == "" || d.Date == "" {
		t.Errorf("Info() has empty fields: %+v", d)
	}
}

func TestInfoUsesLdflagVars(t *testing.T) {
	oldV, oldC, oldD := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = oldV, oldC, oldD })
	Version, Commit, Date = "9.8.7", "deadbeefcafe", "2030-01-01"
	d := Info()
	if d.Version != "9.8.7" || d.Commit != "deadbee" || d.Date != "2030-01-01" {
		t.Errorf("Info() = %+v", d)
	}
}

func TestJSON(t *testing.T) {
	in := Details{Version: "0.1.0", Commit: "abc1234", Date: "2026-10-06", GoVersion: "go1.27.1", OS: "linux", Arch: "arm64"}
	b, err := in.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var out Details
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Errorf("round trip = %+v, want %+v", out, in)
	}
	for _, key := range []string{`"version"`, `"commit"`, `"date"`, `"goVersion"`, `"os"`, `"arch"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("JSON missing key %s: %s", key, b)
		}
	}
}
