#!/usr/bin/env bash
# Builds a fake "release" tree for the CURRENT runner out of the real source, for the
# install-test job: the real ccshelf binary in an archive named like goreleaser's
# (ccshelf_<version>_<os>_<arch>.tar.gz, .zip on Windows), a checksums.txt that also lists the two
# installers, and a placeholder signature bundle. The layout is the one GitHub serves:
#   <root>/download/<tag>/...   and   <root>/latest/download/{checksums.txt,...}
# so scripts/install.sh and scripts/install.ps1 can be pointed at it with a file:/// base URL.
#
# Usage: scripts/make-e2e-release.sh ROOT TAG
# Prints, on stdout, `base-url=file://...` and `tag=...` (append to $GITHUB_OUTPUT).
# Git Bash on Windows has no zip, so the zip is written by a tiny Go program.
set -euo pipefail

root="${1:?usage: make-e2e-release.sh ROOT TAG}"
tag="${2:?usage: make-e2e-release.sh ROOT TAG}"
repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
bare="${tag#v}"
goos="$(go env GOOS)"
goarch="$(go env GOARCH)"
exe="$(go env GOEXE)"
pkg="github.com/yorch/ccshelf/internal/version"
stage="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/ccshelf-e2e.XXXXXX")"
trap 'rm -rf "$stage"' EXIT

mkdir -p "$root/download/$tag" "$root/latest/download"
CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X ${pkg}.Version=${bare} -X ${pkg}.Commit=e2e" \
  -o "$stage/ccshelf${exe}" "$repo/cmd/ccshelf"
cp "$repo/LICENSE" "$repo/README.md" "$stage/"

name="ccshelf_${bare}_${goos}_${goarch}"
if [ "$goos" = "windows" ]; then
  archive="${name}.zip"
  mkdir -p "$stage/zipper"
  cat >"$stage/zipper/main.go" <<'GO'
package main

import (
	"archive/zip"
	"io"
	"os"
)

func main() {
	out, err := os.Create(os.Args[1])
	if err != nil {
		panic(err)
	}
	zw := zip.NewWriter(out)
	for _, name := range os.Args[3:] {
		w, err := zw.Create(name)
		if err != nil {
			panic(err)
		}
		in, err := os.Open(os.Args[2] + "/" + name)
		if err != nil {
			panic(err)
		}
		if _, err := io.Copy(w, in); err != nil {
			panic(err)
		}
		in.Close()
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	if err := out.Close(); err != nil {
		panic(err)
	}
}
GO
  (cd "$stage/zipper" && go run main.go "$root/download/$tag/$archive" "$stage" ccshelf.exe LICENSE README.md)
else
  archive="${name}.tar.gz"
  tar -czf "$root/download/$tag/$archive" -C "$stage" ccshelf LICENSE README.md
fi

# Hash on stdin: a path with backslashes (Windows) would prefix the digest with a backslash.
sha() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum <"$1" | cut -d' ' -f1
  else
    shasum -a 256 <"$1" | cut -d' ' -f1
  fi
}
{
  printf '%s  %s\n' "$(sha "$root/download/$tag/$archive")" "$archive"
  printf '%s  %s\n' "$(sha "$repo/scripts/install.sh")" "install.sh"
  printf '%s  %s\n' "$(sha "$repo/scripts/install.ps1")" "install.ps1"
} >"$root/download/$tag/checksums.txt"
echo '{"placeholder":"not a real sigstore bundle"}' >"$root/download/$tag/checksums.txt.sigstore.json"
cp "$root/download/$tag/checksums.txt" "$root/download/$tag/checksums.txt.sigstore.json" "$root/latest/download/"

# file:///abs/path on Unix, file:///D:/path on Windows (cygpath -m gives D:/path).
if command -v cygpath >/dev/null 2>&1; then
  base="file:///$(cygpath -m "$root")"
else
  base="file://${root}"
fi
echo "base-url=${base}"
echo "tag=${tag}"
