#!/usr/bin/env bash
# Install a verified ccshelf release binary for the current runner.
#
# Inputs arrive as environment variables (never as interpolated shell text):
#   INPUT_VERSION             release tag, strict semver with a leading v (required)
#   INPUT_SHA256              user-pinned SHA-256 of the archive for this OS/arch (optional)
#   INPUT_BASE_URL            https:// or file:/// directory that holds <version>/ release assets
#   INPUT_VERIFY_SIGNATURE    "true" (default) or "false"
#   INPUT_COSIGN_IDENTITY     exact certificate identity for cosign verify-blob (default: the
#                             tool repo's release workflow at refs/tags/<version>)
#   INPUT_COSIGN_IDENTITY_REGEXP  certificate identity regexp instead (explicit opt-in; exclusive
#                             with INPUT_COSIGN_IDENTITY)
#   INPUT_COSIGN_OIDC_ISSUER  certificate OIDC issuer for cosign verify-blob
#   INPUT_TRUSTED_ROOT        optional path to a Sigstore trusted root file (--trusted-root)
#   INPUT_WORKING_DIRECTORY   only validated here
#   RUNNER_TEMP               optional; where the binary is installed
#   GITHUB_OUTPUT, GITHUB_PATH  optional; written when set
#
# Verification order: (1) INPUT_SHA256 pins the archive; else (2) a matching line in
# the action's own pins.txt (<version> <os> <arch> <sha256>, so pinning the action by
# commit SHA pins the binary); else (3) cosign verifies checksums.txt and the archive
# must match its line (cosign needs the Sigstore trusted root: this step is NOT offline
# unless INPUT_TRUSTED_ROOT points at a mirrored root); (4) verify-signature=false
# without a pin is allowed but loudly warned. Nothing is ever silently skipped.
# The release path keeps the leading v (releases/download/v0.1.0/); goreleaser names the
# archive without it (ccshelf_0.1.0_linux_amd64.tar.gz).
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
BASE_URL="${INPUT_BASE_URL:-https://github.com/yorch/ccshelf/releases/download}"
VERIFY_SIG="${INPUT_VERIFY_SIGNATURE:-true}"
COSIGN_IDENTITY="${INPUT_COSIGN_IDENTITY:-}"
COSIGN_IDENTITY_REGEXP="${INPUT_COSIGN_IDENTITY_REGEXP:-}"
COSIGN_ISSUER="${INPUT_COSIGN_OIDC_ISSUER:-https://token.actions.githubusercontent.com}"
TRUSTED_ROOT="${INPUT_TRUSTED_ROOT:-}"
WORKDIR_INPUT="${INPUT_WORKING_DIRECTORY:-.}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PINS_FILE="$SCRIPT_DIR/../pins.txt"

# ---- input validation ------------------------------------------------------
# No input may contain a control character (newlines would forge GITHUB_OUTPUT or
# GITHUB_PATH lines, tabs and carriage returns hide content).
no_control() { # name value
  local name="$1" value="$2"
  if [[ "$value" =~ [[:cntrl:]] ]]; then
    die "input '$name' must not contain newlines or other control characters"
  fi
}
no_control version "$VERSION"
no_control sha256 "$PIN_SHA"
no_control base-url "$BASE_URL"
no_control verify-signature "$VERIFY_SIG"
no_control cosign-identity "$COSIGN_IDENTITY"
no_control cosign-identity-regexp "$COSIGN_IDENTITY_REGEXP"
no_control cosign-oidc-issuer "$COSIGN_ISSUER"
no_control trusted-root "$TRUSTED_ROOT"
no_control working-directory "$WORKDIR_INPUT"

[ -n "$VERSION" ] || die "input 'version' is required (a release tag such as v0.1.0)"
VERSION_RE='^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'
if ! [[ "$VERSION" =~ $VERSION_RE ]]; then
  die "version '$VERSION' is not a strict semver release tag (expected e.g. v0.1.0 or v0.1.0-rc.1)"
fi
case "$VERSION" in
  *..*) die "version must not contain '..'" ;;
esac
VERSION_BARE="${VERSION#v}" # goreleaser's {{ .Version }}: the archive name has no leading v

if [ -n "$PIN_SHA" ]; then
  PIN_SHA="$(printf '%s' "$PIN_SHA" | tr 'A-F' 'a-f')"
  [[ "$PIN_SHA" =~ ^[0-9a-f]{64}$ ]] || die "input 'sha256' must be 64 hex characters"
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
    *' '*) die "input '$name' must not contain whitespace" ;;
  esac
}

reject_unsafe base-url "$BASE_URL"
# https://host/path, or file:/// (absolute path) or file://C:/ (drive letter) for local mirrors.
BASE_URL_RE='^(https://[A-Za-z0-9._~:/@%+=,-]+|file:///[A-Za-z0-9._~:/@%+=,-]*|file://[A-Za-z]:/[A-Za-z0-9._~:/@%+=,-]*)$'
if [[ "$BASE_URL" != https://* && "$BASE_URL" != file://* ]]; then
  die "input 'base-url' must start with https:// or file:///"
fi
[[ "$BASE_URL" =~ $BASE_URL_RE ]] ||
  die "input 'base-url' must be https://<host>/<path> or file:///<path> (or file://<drive>:/<path>) with only URL-safe characters"
BASE_URL="${BASE_URL%/}"

case "$WORKDIR_INPUT" in
  *..*) die "input 'working-directory' must not contain '..'" ;;
  /* | [A-Za-z]:*) die "input 'working-directory' must be relative to the workspace" ;;
esac

if [ -n "$COSIGN_IDENTITY" ] && [ -n "$COSIGN_IDENTITY_REGEXP" ]; then
  die "set only one of 'cosign-identity' (exact) and 'cosign-identity-regexp'"
fi
[[ "$COSIGN_ISSUER" =~ ^https://[^[:space:]]+$ ]] || die "input 'cosign-oidc-issuer' must be an https:// URL"
if [ -n "$TRUSTED_ROOT" ]; then
  reject_unsafe trusted-root "$TRUSTED_ROOT"
  [ -f "$TRUSTED_ROOT" ] || die "input 'trusted-root' is not a file: $TRUSTED_ROOT"
fi

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
ARCHIVE="ccshelf_${VERSION_BARE}_${OS}_${ARCH}.${EXT}"
RELEASE_URL="${BASE_URL}/${VERSION}"

# ---- helpers ---------------------------------------------------------------
# Hash through stdin so the file name never reaches the tool: GNU sha256sum prefixes the
# digest with a backslash when the name contains one (every Windows path), which would
# corrupt the first field.
sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum <"$1" | awk '{print tolower($1)}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 <"$1" | awk '{print tolower($1)}'
  elif command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 <"$1" | awk '{print tolower($NF)}'
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
      # -q first: ignore ~/.curlrc (it could disable certificate checks). Proxy and CA variables stay honored.
      curl -q --fail --location --silent --show-error \
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
[[ "$ACTUAL" =~ ^[0-9a-f]{64}$ ]] || die "could not compute the SHA-256 of $ARCHIVE (got '$ACTUAL')"

# ---- verification ----------------------------------------------------------
# pins.txt ships with the action: "<version> <os> <arch> <sha256>" per line, # comments
# and blank lines allowed. A malformed line anywhere is an error (fail closed). Prints
# the hash for this version/os/arch, or nothing when there is no line.
lookup_pin() {
  local line n=0 ver pos parch hash extra found=""
  [ -f "$PINS_FILE" ] || return 0
  while IFS= read -r line || [ -n "$line" ]; do
    n=$((n + 1))
    line="${line%$'\r'}"
    line="${line%%#*}"
    [[ "$line" =~ ^[[:space:]]*$ ]] && continue
    ver="" pos="" parch="" hash="" extra=""
    read -r ver pos parch hash extra <<<"$line"
    if [ -n "$extra" ] || ! [[ "$ver" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] ||
      ! [[ "$pos" =~ ^(linux|darwin|windows)$ ]] || ! [[ "$parch" =~ ^(amd64|arm64)$ ]] ||
      ! [[ "$hash" =~ ^[0-9a-fA-F]{64}$ ]]; then
      die "pins.txt line $n is malformed (expected '<version> <os> <arch> <sha256>')"
    fi
    hash="$(printf '%s' "$hash" | tr 'A-F' 'a-f')"
    if [ "$ver" = "$VERSION" ] && [ "$pos" = "$OS" ] && [ "$parch" = "$ARCH" ]; then
      if [ -n "$found" ] && [ "$found" != "$hash" ]; then
        die "pins.txt has conflicting lines for $VERSION $OS $ARCH"
      fi
      found="$hash"
    fi
  done <"$PINS_FILE"
  printf '%s' "$found"
}

FILE_PIN=""
if [ -z "$PIN_SHA" ]; then
  FILE_PIN="$(lookup_pin)"
fi

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
elif [ -n "$FILE_PIN" ]; then
  [ "$ACTUAL" = "$FILE_PIN" ] ||
    die "SHA-256 mismatch for $ARCHIVE: archive is $ACTUAL but the action's pins.txt says $FILE_PIN"
  echo "ccshelf install: archive matches the checksum embedded in this action (pins.txt)"
elif [ "$VERIFY_SIG" = true ]; then
  command -v cosign >/dev/null 2>&1 ||
    die "verify-signature is true but 'cosign' is not on PATH. Install cosign in an earlier step (pin it by SHA), or set the 'sha256' input to the archive hash (or use a commit of this action whose pins.txt has your version), or set verify-signature: false and accept an unauthenticated download"
  fetch checksums.txt "$DL/checksums.txt"
  fetch checksums.txt.sigstore.json "$DL/checksums.txt.sigstore.json"
  cosign_args=(verify-blob --bundle "$DL/checksums.txt.sigstore.json")
  if [ -n "$COSIGN_IDENTITY_REGEXP" ]; then
    cosign_args+=(--certificate-identity-regexp "$COSIGN_IDENTITY_REGEXP")
  else
    if [ -z "$COSIGN_IDENTITY" ]; then
      # OWNER: the tool repo; the exact identity of its release workflow for this tag.
      COSIGN_IDENTITY="https://github.com/yorch/ccshelf/.github/workflows/release.yml@refs/tags/${VERSION}"
    fi
    cosign_args+=(--certificate-identity "$COSIGN_IDENTITY")
  fi
  cosign_args+=(--certificate-oidc-issuer "$COSIGN_ISSUER")
  if [ -n "$TRUSTED_ROOT" ]; then
    cosign_args+=(--trusted-root "$TRUSTED_ROOT")
  fi
  # cosign needs the Sigstore trusted root (fetched over the network by default, or
  # supplied with trusted-root): this check is NOT offline. The sha256 pin is.
  cosign "${cosign_args[@]}" "$DL/checksums.txt" || die "cosign could not verify the signature of checksums.txt; refusing to install"
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
