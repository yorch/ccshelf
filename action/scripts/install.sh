#!/usr/bin/env bash
# Install a verified ccshelf release binary for the current runner.
#
# Inputs arrive as environment variables (never as interpolated shell text):
#   INPUT_VERSION             release tag, strict semver with a leading v (required)
#   INPUT_SHA256              user-pinned SHA-256 of the archive for this OS/arch (optional)
#   INPUT_BASE_URL            https:// or file:// directory that holds <version>/ release assets
#   INPUT_VERIFY_SIGNATURE    "true" (default) or "false"
#   INPUT_COSIGN_IDENTITY     certificate identity regexp for cosign verify-blob
#   INPUT_COSIGN_OIDC_ISSUER  certificate OIDC issuer for cosign verify-blob
#   INPUT_WORKING_DIRECTORY   only validated here
#   RUNNER_TEMP               optional; where the binary is installed
#   GITHUB_OUTPUT, GITHUB_PATH  optional; written when set
#
# Verification order: (1) INPUT_SHA256 pins the archive; else (2) cosign verifies
# checksums.txt and the archive must match its line; (3) verify-signature=false
# without a pin is allowed but loudly warned. Nothing is ever silently skipped.
# Works on bash 3.2 (macOS) and Git Bash on Windows.
set -euo pipefail

die() {
  echo "ccshelf install: error: $*" >&2
  exit 1
}

warn_loud() {
  echo "::warning title=ccshelf binary NOT authenticated::$*" >&2
  echo "ccshelf install: WARNING: $*" >&2
}

VERSION="${INPUT_VERSION:-}"
PIN_SHA="${INPUT_SHA256:-}"
BASE_URL="${INPUT_BASE_URL:-https://github.com/ccshelf/ccshelf/releases/download}"
VERIFY_SIG="${INPUT_VERIFY_SIGNATURE:-true}"
COSIGN_IDENTITY="${INPUT_COSIGN_IDENTITY:-^https://github\\.com/ccshelf/ccshelf/\\.github/workflows/release\\.yml@refs/tags/v.*\$}"
COSIGN_ISSUER="${INPUT_COSIGN_OIDC_ISSUER:-https://token.actions.githubusercontent.com}"
WORKDIR_INPUT="${INPUT_WORKING_DIRECTORY:-.}"

# ---- input validation ------------------------------------------------------
[ -n "$VERSION" ] || die "input 'version' is required (a release tag such as v0.1.0)"
if ! printf '%s' "$VERSION" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'; then
  die "version '$VERSION' is not a strict semver release tag (expected e.g. v0.1.0 or v0.1.0-rc.1)"
fi
case "$VERSION" in
  *..*) die "version must not contain '..'" ;;
esac

if [ -n "$PIN_SHA" ]; then
  PIN_SHA="$(printf '%s' "$PIN_SHA" | tr 'A-F' 'a-f')"
  printf '%s' "$PIN_SHA" | grep -Eq '^[0-9a-f]{64}$' || die "input 'sha256' must be 64 hex characters"
fi

case "$VERIFY_SIG" in
  true | false) ;;
  *) die "input 'verify-signature' must be 'true' or 'false', got '$VERIFY_SIG'" ;;
esac

reject_unsafe() { # name value
  local name="$1" value="$2"
  case "$value" in
    *..*) die "input '$name' must not contain '..'" ;;
  esac
  case "$value" in
    *$'\n'* | *$'\r'* | *$'\t'* | *' '*) die "input '$name' must not contain whitespace" ;;
  esac
}

reject_unsafe base-url "$BASE_URL"
case "$BASE_URL" in
  https://* | file://*) ;;
  *) die "input 'base-url' must start with https:// or file://" ;;
esac
BASE_URL="${BASE_URL%/}"

case "$WORKDIR_INPUT" in
  *..*) die "input 'working-directory' must not contain '..'" ;;
  /* | [A-Za-z]:*) die "input 'working-directory' must be relative to the workspace" ;;
  *$'\n'*) die "input 'working-directory' must not contain a newline" ;;
esac
case "$COSIGN_IDENTITY$COSIGN_ISSUER" in
  *$'\n'*) die "cosign inputs must not contain a newline" ;;
esac

# ---- platform --------------------------------------------------------------
detect_os() {
  local raw="${RUNNER_OS:-}"
  if [ -z "$raw" ]; then raw="$(uname -s)"; fi
  case "$raw" in
    Linux | linux) echo linux ;;
    macOS | Darwin | darwin) echo darwin ;;
    Windows | MINGW* | MSYS* | CYGWIN* | windows) echo windows ;;
    *) die "unsupported operating system '$raw'" ;;
  esac
}

detect_arch() {
  local raw="${RUNNER_ARCH:-}"
  if [ -z "$raw" ]; then raw="$(uname -m)"; fi
  case "$raw" in
    X64 | x86_64 | amd64 | AMD64) echo amd64 ;;
    ARM64 | arm64 | aarch64) echo arm64 ;;
    *) die "unsupported CPU architecture '$raw'" ;;
  esac
}

OS="$(detect_os)"
ARCH="$(detect_arch)"
if [ "$OS" = windows ]; then
  EXT=zip
  BIN=ccshelf.exe
else
  EXT=tar.gz
  BIN=ccshelf
fi
ARCHIVE="ccshelf_${VERSION}_${OS}_${ARCH}.${EXT}"
RELEASE_URL="${BASE_URL}/${VERSION}"

# ---- helpers ---------------------------------------------------------------
sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print tolower($1)}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print tolower($1)}'
  elif command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 "$1" | awk '{print tolower($NF)}'
  else
    die "no SHA-256 tool found (need sha256sum, shasum or openssl)"
  fi
}

fetch() { # name destination
  local name="$1" dest="$2"
  case "$RELEASE_URL" in
    file://*)
      cp -- "${RELEASE_URL#file://}/${name}" "$dest" || die "cannot copy ${RELEASE_URL}/${name}"
      ;;
    *)
      curl --fail --location --silent --show-error \
        --proto '=https' --proto-redir '=https' \
        --connect-timeout 20 --max-time 300 --retry 2 \
        --output "$dest" -- "${RELEASE_URL}/${name}" || die "download failed: ${RELEASE_URL}/${name}"
      ;;
  esac
}

extract() { # archive dir
  local archive="$1" dir="$2"
  if [ "$EXT" = "tar.gz" ]; then
    tar -xzf "$archive" -C "$dir" -- "$BIN" || die "cannot extract $BIN from $ARCHIVE"
  else
    if command -v unzip >/dev/null 2>&1; then
      unzip -q -o "$archive" "$BIN" -d "$dir" || die "cannot extract $BIN from $ARCHIVE"
    elif command -v 7z >/dev/null 2>&1; then
      7z x -y "-o$dir" "$archive" "$BIN" >/dev/null || die "cannot extract $BIN from $ARCHIVE"
    elif command -v powershell.exe >/dev/null 2>&1; then
      ARCHIVE_PATH="$archive" DEST_PATH="$dir" powershell.exe -NoProfile -Command \
        'Expand-Archive -LiteralPath $env:ARCHIVE_PATH -DestinationPath $env:DEST_PATH -Force' ||
        die "cannot extract $ARCHIVE"
    else
      die "no zip extractor found (need unzip, 7z or powershell.exe)"
    fi
  fi
  [ -f "$dir/$BIN" ] || die "archive $ARCHIVE does not contain $BIN at its top level"
}

# ---- work area -------------------------------------------------------------
TEMP_ROOT="${RUNNER_TEMP:-}"
if [ -z "$TEMP_ROOT" ]; then TEMP_ROOT="${TMPDIR:-/tmp}"; fi
[ -d "$TEMP_ROOT" ] || die "temporary directory '$TEMP_ROOT' does not exist"
WORK="$(mktemp -d "${TEMP_ROOT%/}/ccshelf-install.XXXXXX")" || die "cannot create a temporary directory"
chmod 700 "$WORK"
DL="$WORK/download"
BINDIR="$WORK/bin"
mkdir -p "$DL" "$BINDIR"

echo "ccshelf install: fetching ${ARCHIVE} (${VERSION}) from ${RELEASE_URL}"
fetch "$ARCHIVE" "$DL/$ARCHIVE"
ACTUAL="$(sha256_of "$DL/$ARCHIVE")"

# ---- verification ----------------------------------------------------------
check_against_checksums() { # requires $DL/checksums.txt
  local expected count
  count="$(awk -v f="$ARCHIVE" '{n=$2; sub(/^\*/,"",n); if (n==f) c++} END {print c+0}' "$DL/checksums.txt")"
  [ "$count" = 1 ] || die "checksums.txt must contain exactly one line for $ARCHIVE (found $count)"
  expected="$(awk -v f="$ARCHIVE" '{n=$2; sub(/^\*/,"",n); if (n==f) print tolower($1)}' "$DL/checksums.txt")"
  [ "$ACTUAL" = "$expected" ] ||
    die "SHA-256 mismatch for $ARCHIVE: archive is $ACTUAL but checksums.txt says $expected"
}

if [ -n "$PIN_SHA" ]; then
  [ "$ACTUAL" = "$PIN_SHA" ] ||
    die "SHA-256 mismatch for $ARCHIVE: archive is $ACTUAL but the pinned 'sha256' input is $PIN_SHA"
  echo "ccshelf install: archive matches the pinned sha256 input"
elif [ "$VERIFY_SIG" = true ]; then
  command -v cosign >/dev/null 2>&1 ||
    die "verify-signature is true but 'cosign' is not on PATH. Install cosign in an earlier step (pin it by SHA), or set the 'sha256' input to the archive hash, or set verify-signature: false and accept an unauthenticated download"
  fetch checksums.txt "$DL/checksums.txt"
  fetch checksums.txt.sigstore.json "$DL/checksums.txt.sigstore.json"
  cosign verify-blob \
    --bundle "$DL/checksums.txt.sigstore.json" \
    --certificate-identity-regexp "$COSIGN_IDENTITY" \
    --certificate-oidc-issuer "$COSIGN_ISSUER" \
    "$DL/checksums.txt" || die "cosign could not verify the signature of checksums.txt; refusing to install"
  check_against_checksums
  echo "ccshelf install: checksums.txt signature verified and archive matches it"
else
  warn_loud "verify-signature is false and no 'sha256' is pinned: the downloaded ccshelf ${VERSION} archive is NOT authenticated. Anyone who can alter the download location can run code in this job. Pin 'sha256' or enable verify-signature."
  fetch checksums.txt "$DL/checksums.txt"
  check_against_checksums
  echo "ccshelf install: archive matches checksums.txt (unauthenticated, integrity only)"
fi
echo "ccshelf install: verified sha256 ${ACTUAL}"

# ---- install ---------------------------------------------------------------
extract "$DL/$ARCHIVE" "$BINDIR"
chmod +x "$BINDIR/$BIN"
BIN_PATH="$BINDIR/$BIN"

if [ -n "${GITHUB_OUTPUT:-}" ]; then
  {
    echo "path=${BIN_PATH}"
    echo "version=${VERSION}"
  } >>"$GITHUB_OUTPUT"
fi
if [ -n "${GITHUB_PATH:-}" ]; then
  echo "$BINDIR" >>"$GITHUB_PATH"
fi
echo "ccshelf install: installed ${BIN_PATH}"
