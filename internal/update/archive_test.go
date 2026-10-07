package update

import (
	"archive/tar"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeArchive(t *testing.T, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "archive")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

var payload = []byte("the binary")

func TestExtractTarGz(t *testing.T) {
	lic := entry{name: "LICENSE", body: []byte("MIT"), mode: 0o644}
	rd := entry{name: "README.md", body: []byte("hi"), mode: 0o644}
	bin := entry{name: "ccshelf", body: payload}
	for name, tc := range map[string]struct {
		entries []entry
		max     int64
		err     string
	}{
		"binary only":         {[]entry{bin}, 1 << 20, ""},
		"with the extras":     {[]entry{bin, lic, rd}, 1 << 20, ""},
		"extras first":        {[]entry{lic, bin, rd}, 1 << 20, ""},
		"empty binary":        {[]entry{{name: "ccshelf"}}, 1 << 20, ""},
		"traversal":           {[]entry{{name: "../ccshelf", body: payload}}, 1 << 20, "top level"},
		"traversal in middle": {[]entry{{name: "a/../ccshelf", body: payload}}, 1 << 20, "top level"},
		"absolute":            {[]entry{{name: "/usr/bin/ccshelf", body: payload}}, 1 << 20, "absolute"},
		"drive":               {[]entry{{name: "C:ccshelf", body: payload}}, 1 << 20, "absolute"},
		"backslash":           {[]entry{{name: `x\ccshelf`, body: payload}}, 1 << 20, "top level"},
		"subdirectory":        {[]entry{{name: "dist/ccshelf", body: payload}}, 1 << 20, "top level"},
		"dot slash":           {[]entry{{name: "./ccshelf", body: payload}}, 1 << 20, "top level"},
		"symlink":             {[]entry{bin, {name: "LICENSE", typ: tar.TypeSymlink, link: "/etc/passwd"}}, 1 << 20, "not a regular file"},
		"symlink binary":      {[]entry{{name: "ccshelf", typ: tar.TypeSymlink, link: "/bin/sh"}}, 1 << 20, "not a regular file"},
		"hardlink":            {[]entry{bin, {name: "README.md", typ: tar.TypeLink, link: "ccshelf"}}, 1 << 20, "not a regular file"},
		"directory":           {[]entry{{name: "dist", typ: tar.TypeDir}, bin}, 1 << 20, "not a regular file"},
		"device":              {[]entry{{name: "x", typ: tar.TypeChar}, bin}, 1 << 20, "not a regular file"},
		"extra entry":         {[]entry{bin, {name: "install.sh", body: []byte("rm -rf")}}, 1 << 20, "unexpected entry"},
		"hidden entry":        {[]entry{bin, {name: ".hidden", body: []byte("x")}}, 1 << 20, "unexpected entry"},
		"duplicate":           {[]entry{bin, bin}, 1 << 20, "twice"},
		"missing":             {[]entry{lic, rd}, 1 << 20, "not in the archive"},
		"wrong name":          {[]entry{{name: "ccshelf.exe", body: payload}}, 1 << 20, "unexpected entry"},
		"oversize":            {[]entry{bin}, int64(len(payload)) - 1, "limit"},
		"exact size":          {[]entry{bin}, int64(len(payload)), ""},
		"too many entries":    {manyEntries(20, bin), 1 << 20, "more than 16"},
		"control character":   {[]entry{{name: "cc\x01shelf", body: payload}, bin}, 1 << 20, "control"},
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			n, err := ExtractBinary(writeArchive(t, makeTarGz(t, tc.entries...)), false, "ccshelf", &out, tc.max)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("error = %v, want %q", err, tc.err)
				}
				if !errors.Is(err, ErrArchive) && !strings.Contains(tc.err, "limit") {
					t.Errorf("error %v is not an ErrArchive", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := tc.entries[0].body
			for _, e := range tc.entries {
				if e.name == "ccshelf" {
					want = e.body
				}
			}
			if n != int64(len(want)) || !bytes.Equal(out.Bytes(), want) {
				t.Errorf("extracted %q (%d bytes), want %q", out.Bytes(), n, want)
			}
		})
	}
}

func manyEntries(n int, bin entry) []entry {
	out := []entry{bin}
	for i := 0; i < n; i++ {
		out = append(out, entry{name: "LICENSE", body: []byte("x")})
	}
	return out
}

func TestExtractZip(t *testing.T) {
	bin := zentry{"ccshelf.exe", payload, 0o755}
	lic := zentry{"LICENSE", []byte("MIT"), 0o644}
	for name, tc := range map[string]struct {
		entries []zentry
		max     int64
		err     string
	}{
		"binary only":     {[]zentry{bin}, 1 << 20, ""},
		"with the extras": {[]zentry{bin, lic, {"README.md", []byte("x"), 0o644}}, 1 << 20, ""},
		"traversal":       {[]zentry{{"../ccshelf.exe", payload, 0o755}}, 1 << 20, "top level"},
		"absolute":        {[]zentry{{"/ccshelf.exe", payload, 0o755}}, 1 << 20, "absolute"},
		"backslash":       {[]zentry{{`..\ccshelf.exe`, payload, 0o755}}, 1 << 20, "top level"},
		"subdirectory":    {[]zentry{{"bin/ccshelf.exe", payload, 0o755}}, 1 << 20, "top level"},
		"symlink":         {[]zentry{bin, {"LICENSE", []byte("/etc/passwd"), os.ModeSymlink | 0o777}}, 1 << 20, "not a regular file"},
		"directory":       {[]zentry{{"d/", nil, os.ModeDir | 0o755}, bin}, 1 << 20, "top level"},
		"extra":           {[]zentry{bin, {"evil.dll", []byte("x"), 0o644}}, 1 << 20, "unexpected entry"},
		"duplicate":       {[]zentry{bin, bin}, 1 << 20, "twice"},
		"missing":         {[]zentry{lic}, 1 << 20, "not in the archive"},
		"oversize":        {[]zentry{bin}, int64(len(payload)) - 1, "limit"},
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			_, err := ExtractBinary(writeArchive(t, makeZip(t, tc.entries...)), true, "ccshelf.exe", &out, tc.max)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("error = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil || !bytes.Equal(out.Bytes(), payload) {
				t.Fatalf("got %q, %v", out.Bytes(), err)
			}
		})
	}
}

func TestExtractNotAnArchive(t *testing.T) {
	var out bytes.Buffer
	if _, err := ExtractBinary(writeArchive(t, []byte("plain text")), false, "ccshelf", &out, 1<<20); !errors.Is(err, ErrArchive) {
		t.Errorf("tar.gz: %v", err)
	}
	if _, err := ExtractBinary(writeArchive(t, []byte("plain text")), true, "ccshelf.exe", &out, 1<<20); !errors.Is(err, ErrArchive) {
		t.Errorf("zip: %v", err)
	}
	if _, err := ExtractBinary(filepath.Join(t.TempDir(), "missing"), false, "ccshelf", &out, 1<<20); err == nil {
		t.Error("a missing archive must fail")
	}
	// A gzip stream that ends mid-entry.
	full := makeTarGz(t, entry{name: "ccshelf", body: bytes.Repeat([]byte("x"), 100000)})
	if _, err := ExtractBinary(writeArchive(t, full[:len(full)/2]), false, "ccshelf", &out, 1<<20); err == nil {
		t.Error("a truncated archive must fail")
	}
}
