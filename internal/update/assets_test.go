package update

import (
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
)

func TestArchiveFor(t *testing.T) {
	v, _ := ParseVersion("v0.2.0")
	for _, tc := range []struct {
		goos, goarch string
		name, binary string
		zip          bool
	}{
		{"linux", "amd64", "ccshelf_0.2.0_linux_amd64.tar.gz", "ccshelf", false},
		{"linux", "arm64", "ccshelf_0.2.0_linux_arm64.tar.gz", "ccshelf", false},
		{"darwin", "arm64", "ccshelf_0.2.0_darwin_arm64.tar.gz", "ccshelf", false},
		{"darwin", "amd64", "ccshelf_0.2.0_darwin_amd64.tar.gz", "ccshelf", false},
		{"windows", "amd64", "ccshelf_0.2.0_windows_amd64.zip", "ccshelf.exe", true},
		{"windows", "arm64", "ccshelf_0.2.0_windows_arm64.zip", "ccshelf.exe", true},
	} {
		a, err := ArchiveFor(v, tc.goos, tc.goarch)
		if err != nil {
			t.Errorf("%s/%s: %v", tc.goos, tc.goarch, err)
			continue
		}
		if a.Name != tc.name || a.Binary != tc.binary || a.Zip != tc.zip {
			t.Errorf("%s/%s = %+v, want %s %s zip=%v", tc.goos, tc.goarch, a, tc.name, tc.binary, tc.zip)
		}
	}
	for _, p := range [][2]string{{"freebsd", "amd64"}, {"linux", "386"}, {"linux", "riscv64"}, {"plan9", "amd64"}, {"", ""}} {
		if _, err := ArchiveFor(v, p[0], p[1]); err == nil {
			t.Errorf("%v must be unsupported", p)
		}
	}
	pre, _ := ParseVersion("1.0.0-rc.1")
	if a, _ := ArchiveFor(pre, "linux", "amd64"); a.Name != "ccshelf_1.0.0-rc.1_linux_amd64.tar.gz" {
		t.Errorf("pre-release name = %q", a.Name)
	}
}

const (
	h1 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	h2 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestParseChecksums(t *testing.T) {
	good := h1 + "  ccshelf_0.2.0_linux_amd64.tar.gz\n" + h2 + "  ccshelf_0.2.0_darwin_arm64.tar.gz\n"
	for name, tc := range map[string]struct {
		data, file, want, err string
	}{
		"one":        {good, "ccshelf_0.2.0_linux_amd64.tar.gz", h1, ""},
		"other":      {good, "ccshelf_0.2.0_darwin_arm64.tar.gz", h2, ""},
		"upper":      {strings.ToUpper(h1) + "  f\n", "f", h1, ""},
		"binary":     {h1 + " *f\n", "f", h1, ""},
		"crlf":       {h1 + "  f\r\n" + h2 + "  g\r\n", "f", h1, ""},
		"blank":      {"\n" + h1 + "  f\n\n", "f", h1, ""},
		"missing":    {good, "nope.tar.gz", "", "no entry"},
		"duplicate":  {h1 + "  f\n" + h2 + "  f\n", "f", "", "2 entries"},
		"same twice": {h1 + "  f\n" + h1 + "  f\n", "f", "", "2 entries"},
		"prefix":     {h1 + "  xf\n", "f", "", "no entry"},
		"short hash": {"abcd  f\n", "f", "", "line 1"},
		"one space":  {h1 + " f\n", "f", "", "line 1"},
		"garbage":    {"hello\n" + h1 + "  f\n", "f", "", "line 1"},
		"empty":      {"", "f", "", "no entry"},
	} {
		got, err := ParseChecksums([]byte(tc.data), tc.file)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%s: error = %v, want %q", name, err, tc.err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s: = %q, %v; want %q", name, got, err, tc.want)
		}
	}
}

func TestVerifySHA256(t *testing.T) {
	sum := sha256.Sum256([]byte("hello"))
	want := sha256Hex([]byte("hello"))
	if err := VerifySHA256(sum, want); err != nil {
		t.Fatal(err)
	}
	other := sha256Hex([]byte("hullo"))
	if err := VerifySHA256(sum, other); !errors.Is(err, ErrChecksum) {
		t.Errorf("mismatch error = %v", err)
	}
	for _, bad := range []string{"", "zz", want[:62], want + "00"} {
		if err := VerifySHA256(sum, bad); !errors.Is(err, ErrChecksum) {
			t.Errorf("recorded %q: error = %v", bad, err)
		}
	}
}
