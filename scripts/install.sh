#!/bin/sh
# ccshelf installer for Linux and macOS (WSL counts as Linux). POSIX sh, no bashisms.
#
#   curl -fsSL https://github.com/yorch/ccshelf/releases/latest/download/install.sh | sh
#   curl -fsSL https://github.com/yorch/ccshelf/releases/latest/download/install.sh | sh -s -- --version v0.1.0
#
# What it does: resolves the release (no GitHub API, so no rate limit and no token), downloads
# checksums.txt and the archive for this platform from the SAME release, verifies the archive
# against checksums.txt (SHA-256, always), verifies the signature of checksums.txt when `cosign`
# is on PATH (a mismatch is fatal; --require-signature makes a missing cosign fatal too), checks
# the archive listing, extracts only the `ccshelf` binary and installs it atomically.
# It never uses sudo, never edits shell profiles, never sends telemetry, never evals downloaded
# text and never runs anything it downloaded except the final `ccshelf version` smoke test.
# See SECURITY.md ("What the installer verifies") for what this does and does not prove.
set -eu

umask 077

PROG="ccshelf-install"
DEFAULT_BASE_URL="https://github.com/yorch/ccshelf/releases" # OWNER
DEFAULT_ISSUER="https://token.actions.githubusercontent.com"
# Size caps (bytes): a corrupt or hostile mirror cannot fill the disk.
MAX_ARCHIVE_BYTES=157286400
MAX_BINARY_BYTES=268435456
MAX_TEXT_BYTES=1048576

VERSION=""
BIN_DIR=""
BASE_URL="$DEFAULT_BASE_URL"
REQUIRE_SIG=0
COSIGN_IDENTITY=""
COSIGN_ISSUER="$DEFAULT_ISSUER"
DRY_RUN=0
FORCE=0
QUIET=0

TMP=""
STAGE=""

die() {
  printf '%s: error: %s\n' "$PROG" "$*" >&2
  exit 1
}

warn() {
  printf '%s: warning: %s\n' "$PROG" "$*" >&2
}

say() {
  if [ "$QUIET" -eq 0 ]; then
    printf '%s: %s\n' "$PROG" "$*"
  fi
}

cleanup() {
  if [ -n "$STAGE" ]; then
    rm -f "$STAGE" 2>/dev/null || true
  fi
  if [ -n "$TMP" ] && [ -d "$TMP" ]; then
    rm -rf "$TMP" 2>/dev/null || true
  fi
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

usage() {
  cat <<'EOF'
ccshelf installer (Linux and macOS). Unofficial; not affiliated with Anthropic.

Usage: install.sh [options]

  --version vX.Y.Z        install this release (default: the latest release)
  --bin-dir DIR           install into DIR (default: $HOME/.local/bin; created if missing;
                          must be an absolute path that is not a symlink, is owned by you and
                          is not writable by others)
  --base-url URL          release download base, for GitHub Enterprise Server or a mirror
                          (default: https://github.com/yorch/ccshelf/releases; https only,
                          or file:///dir for a local mirror)
  --require-signature     fail if cosign is not installed (default: verify the signature when
                          cosign is on PATH, and say so when it is not)
  --cosign-identity ID    certificate identity cosign must see (default: the release workflow
                          of the public repository at the release tag)
  --cosign-issuer URL     certificate OIDC issuer (default: GitHub Actions)
  --force                 replace an existing file or symlink named ccshelf that is not ccshelf
                          (a directory with that name is never replaced)
  --dry-run               download and verify, then stop: install nothing
  --quiet                 print only warnings and errors
  --help                  show this help

The SHA-256 check against the release's checksums.txt cannot be turned off.
Downloads need curl (https only, redirects included; ~/.curlrc is ignored). Proxy and CA
environment variables are honored; TLS verification is never disabled.
When piped from curl, pass options after `sh -s --`.
EOF
}

need_value() { # option remaining-arg-count
  [ "$2" -ge 2 ] || die "option $1 needs a value (see --help)"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --version)
      need_value "$1" "$#"
      VERSION="$2"
      shift 2
      ;;
    --version=*)
      VERSION="${1#--version=}"
      shift
      ;;
    --bin-dir)
      need_value "$1" "$#"
      BIN_DIR="$2"
      shift 2
      ;;
    --bin-dir=*)
      BIN_DIR="${1#--bin-dir=}"
      shift
      ;;
    --base-url)
      need_value "$1" "$#"
      BASE_URL="$2"
      shift 2
      ;;
    --base-url=*)
      BASE_URL="${1#--base-url=}"
      shift
      ;;
    --cosign-identity)
      need_value "$1" "$#"
      COSIGN_IDENTITY="$2"
      shift 2
      ;;
    --cosign-identity=*)
      COSIGN_IDENTITY="${1#--cosign-identity=}"
      shift
      ;;
    --cosign-issuer)
      need_value "$1" "$#"
      COSIGN_ISSUER="$2"
      shift 2
      ;;
    --cosign-issuer=*)
      COSIGN_ISSUER="${1#--cosign-issuer=}"
      shift
      ;;
    --require-signature)
      REQUIRE_SIG=1
      shift
      ;;
    --force)
      FORCE=1
      shift
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    --quiet)
      QUIET=1
      shift
      ;;
    --help | -h)
      usage
      exit 0
      ;;
    *)
      die "unknown option '$1' (see --help)"
      ;;
  esac
done

# ---- input validation -------------------------------------------------------
# Every value is checked against a strict pattern before it is used. No value may contain a
# control character (a newline would split a message or a path) or a non-ASCII byte.
printable() { # name value
  # Count the bytes that are not printable ASCII. The count goes through a pipe, not through
  # command substitution of the value, so a trailing newline cannot be stripped away.
  _bad="$(printf '%s' "$2" | LC_ALL=C tr -d '[:print:]' | wc -c | tr -d ' ')"
  [ "$_bad" = "0" ] || die "$1 must not contain newlines, control or non-ASCII characters"
}

matches() { # extended-regex value
  printf '%s\n' "$2" | grep -Eq -- "$1"
}

printable "--version" "$VERSION"
printable "--bin-dir" "$BIN_DIR"
printable "--base-url" "$BASE_URL"
printable "--cosign-identity" "$COSIGN_IDENTITY"
printable "--cosign-issuer" "$COSIGN_ISSUER"

VERSION_RE='^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'
if [ -n "$VERSION" ]; then
  matches "$VERSION_RE" "$VERSION" ||
    die "version '$VERSION' is not a release tag like v0.1.0 or v0.1.0-rc.1"
  case "$VERSION" in
    *..*) die "version must not contain '..'" ;;
  esac
fi

case "$BASE_URL" in
  *..*) die "--base-url must not contain '..'" ;;
esac
case "$BASE_URL" in
  https://* | file:///*) ;;
  http://*) die "--base-url must be https (plain http is refused): $BASE_URL" ;;
  *) die "--base-url must start with https:// (or file:/// for a local mirror)" ;;
esac
matches '^(https://[A-Za-z0-9._~:/@%+=,-]+|file:///[A-Za-z0-9._~:/@%+=,-]*)$' "$BASE_URL" ||
  die "--base-url may only contain letters, digits and . _ ~ : / @ % + = , -"
BASE_URL="${BASE_URL%/}"

matches '^https://[A-Za-z0-9._~:/@%+=,-]+$' "$COSIGN_ISSUER" ||
  die "--cosign-issuer must be an https:// URL"
if [ -n "$COSIGN_IDENTITY" ]; then
  matches '^[A-Za-z0-9._~:/@%+=,#-]+$' "$COSIGN_IDENTITY" ||
    die "--cosign-identity may only contain letters, digits and . _ ~ : / @ % + = , # -"
fi

if [ -z "$BIN_DIR" ]; then
  [ -n "${HOME:-}" ] || die "HOME is not set; pass --bin-dir"
  BIN_DIR="$HOME/.local/bin"
  printable "HOME" "$BIN_DIR"
fi
case "$BIN_DIR" in
  /*) ;;
  *) die "--bin-dir must be an absolute path: $BIN_DIR" ;;
esac
case "$BIN_DIR" in
  */../* | */.. | */./* | */.)
    die "--bin-dir must not contain '.' or '..' components: $BIN_DIR"
    ;;
esac
if [ "$BIN_DIR" != "/" ]; then
  BIN_DIR="${BIN_DIR%/}"
fi

# ---- platform ---------------------------------------------------------------
case "$(uname -s)" in
  Linux) OS=linux ;;
  Darwin) OS=darwin ;;
  MINGW* | MSYS* | CYGWIN*)
    die "this installer is for Linux and macOS. On Windows use install.ps1 (see the README)"
    ;;
  *) die "unsupported operating system '$(uname -s)': only Linux and macOS are supported" ;;
esac

case "$(uname -m)" in
  x86_64 | amd64) ARCH=amd64 ;;
  arm64 | aarch64) ARCH=arm64 ;;
  *) die "unsupported CPU architecture '$(uname -m)': only amd64 and arm64 are supported" ;;
esac
if [ "$OS" = darwin ] && [ "$ARCH" = amd64 ]; then
  # A shell running under Rosetta reports x86_64 on an Apple silicon Mac: install the native build.
  if [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || true)" = "1" ]; then
    ARCH=arm64
  fi
fi

# ---- tools ------------------------------------------------------------------
# curl is the only network client: it is the one tool whose flags can pin https for redirects too,
# ignore the user's config file and cap the size (a wget fallback could do none of that reliably,
# and busybox wget accepts none of it). file:/// mirrors need no client.
HAVE_CURL=0
if command -v curl >/dev/null 2>&1; then
  HAVE_CURL=1
fi

# 5-second limit for running an existing ccshelf to identify it (see the target checks below).
TIMEOUT_TOOL=""
if command -v timeout >/dev/null 2>&1; then
  TIMEOUT_TOOL=timeout
elif command -v gtimeout >/dev/null 2>&1; then
  TIMEOUT_TOOL=gtimeout
fi

# Hash through stdin: the file name never reaches the tool (GNU sha256sum prefixes the digest with
# a backslash when the name contains one).
sha256_of() { # file
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

file_size() { # file
  wc -c <"$1" | tr -d ' '
}

NEED_CURL="curl is required; install it (for example: apk add curl, apt install curl)"

# fetch URL DEST MAXBYTES. https only for the network; file:/// is a local copy.
fetch() {
  _url="$1"
  _dest="$2"
  _max="$3"
  case "$_url" in
    file://*)
      cp -- "${_url#file://}" "$_dest" 2>/dev/null || die "cannot read $_url"
      ;;
    https://*)
      [ "$HAVE_CURL" -eq 1 ] || die "$NEED_CURL"
      # -q must be the first argument: it makes curl ignore ~/.curlrc (which could disable
      # certificate checks or add a proxy). Proxy and CA environment variables are still honored;
      # TLS verification is never turned off here.
      curl -q --proto '=https' --proto-redir '=https' --fail --location --silent --show-error \
        --max-filesize "$_max" --connect-timeout 20 --max-time 300 --retry 2 \
        --output "$_dest" -- "$_url" </dev/null || die "download failed: $_url"
      ;;
    *) die "refusing to fetch a non-https URL: $_url" ;;
  esac
  [ -f "$_dest" ] || die "download produced no file: $_url"
  _size="$(file_size "$_dest")"
  [ "$_size" -gt 0 ] || die "downloaded file is empty: $_url"
  [ "$_size" -le "$_max" ] || die "downloaded file is larger than $_max bytes: $_url"
}

# check_checksums_format FILE: every non-blank line is "<64 hex>  <name>".
check_checksums_format() {
  awk 'NF==0 {next} NF!=2 || length($1)!=64 || $1 ~ /[^0-9a-fA-F]/ {bad=1} END {exit bad}' "$1" ||
    die "checksums.txt is malformed (every line must be '<sha256>  <file name>')"
}

# count_lines_for FILE NAME: how many lines of checksums.txt name this file.
count_lines_for() {
  awk -v n="$2" '{f=$2; sub(/^\*/, "", f); if (f==n) c++} END {print c+0}' "$1"
}

hash_for() { # FILE NAME
  awk -v n="$2" '{f=$2; sub(/^\*/, "", f); if (f==n) print tolower($1)}' "$1"
}

# ---- work area --------------------------------------------------------------
TMP="$(mktemp -d "${TMPDIR:-/tmp}/ccshelf-install.XXXXXX")" || die "cannot create a temporary directory"
chmod 700 "$TMP"

# ---- plan -------------------------------------------------------------------
if [ -n "$VERSION" ]; then
  want="$VERSION"
else
  want="the latest release"
fi
say "installing ccshelf ($want) for ${OS}/${ARCH}"
say "  from:      $BASE_URL"
say "  into:      $BIN_DIR"
if [ "$DRY_RUN" -eq 1 ]; then
  say "  dry run: downloads and verifies, installs nothing"
fi

# ---- resolve the release ----------------------------------------------------
if [ -z "$VERSION" ]; then
  fetch "$BASE_URL/latest/download/checksums.txt" "$TMP/latest-checksums.txt" "$MAX_TEXT_BYTES"
  check_checksums_format "$TMP/latest-checksums.txt"
  suffix="_${OS}_${ARCH}.tar.gz"
  names="$(awk -v suffix="$suffix" '
    {f=$2; sub(/^\*/, "", f)}
    substr(f, 1, 8) == "ccshelf_" && length(f) > length(suffix) && substr(f, length(f) - length(suffix) + 1) == suffix {print f}
  ' "$TMP/latest-checksums.txt")"
  if [ -z "$names" ]; then
    die "the latest release lists no ccshelf archive for ${OS}/${ARCH}"
  fi
  case "$names" in
    *"
"*) die "the latest release lists more than one ccshelf archive for ${OS}/${ARCH}" ;;
  esac
  bare="${names#ccshelf_}"
  bare="${bare%"$suffix"}"
  matches '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' "$bare" ||
    die "the latest release has an unexpected archive name: $names"
  case "$bare" in
    *..*) die "the latest release has an unexpected archive name: $names" ;;
  esac
  VERSION="v$bare"
  say "latest release is $VERSION"
fi
BARE="${VERSION#v}"
ARCHIVE="ccshelf_${BARE}_${OS}_${ARCH}.tar.gz"
REL="$BASE_URL/download/$VERSION"
if [ -z "$COSIGN_IDENTITY" ]; then
  COSIGN_IDENTITY="https://github.com/yorch/ccshelf/.github/workflows/release.yml@refs/tags/$VERSION" # OWNER
fi

# ---- checksums, signature, archive -------------------------------------------
# Everything below comes from the one pinned tag, even when the version was resolved from "latest",
# so a release published in between cannot mix two releases.
fetch "$REL/checksums.txt" "$TMP/checksums.txt" "$MAX_TEXT_BYTES"
check_checksums_format "$TMP/checksums.txt"
count="$(count_lines_for "$TMP/checksums.txt" "$ARCHIVE")"
[ "$count" = "1" ] ||
  die "checksums.txt must contain exactly one line for $ARCHIVE (found $count); is $VERSION a release with a ${OS}/${ARCH} build?"
EXPECTED="$(hash_for "$TMP/checksums.txt" "$ARCHIVE")"

if command -v cosign >/dev/null 2>&1; then
  fetch "$REL/checksums.txt.sigstore.json" "$TMP/checksums.txt.sigstore.json" "$MAX_TEXT_BYTES"
  say "verifying the signature of checksums.txt with cosign"
  cosign verify-blob --bundle "$TMP/checksums.txt.sigstore.json" \
    --certificate-identity "$COSIGN_IDENTITY" \
    --certificate-oidc-issuer "$COSIGN_ISSUER" \
    "$TMP/checksums.txt" </dev/null ||
    die "cosign could not verify the signature of checksums.txt for $VERSION; refusing to install"
  say "signature verified (identity $COSIGN_IDENTITY)"
elif [ "$REQUIRE_SIG" -eq 1 ]; then
  die "--require-signature was given but cosign is not on PATH; install cosign (https://docs.sigstore.dev/cosign/) and retry"
else
  warn "cosign is not installed: the signature is NOT checked. The archive is verified only against checksums.txt from the same release, which detects corruption but not a tampered release. Install cosign or pass --require-signature to make this an error."
fi

fetch "$REL/$ARCHIVE" "$TMP/$ARCHIVE" "$MAX_ARCHIVE_BYTES"
ACTUAL="$(sha256_of "$TMP/$ARCHIVE")"
matches '^[0-9a-f]{64}$' "$ACTUAL" || die "could not compute the SHA-256 of $ARCHIVE"
if [ "$ACTUAL" != "$EXPECTED" ]; then
  die "SHA-256 mismatch for $ARCHIVE: the download is $ACTUAL but checksums.txt says $EXPECTED; nothing was installed"
fi
say "SHA-256 matches checksums.txt ($ACTUAL)"

# ---- archive listing --------------------------------------------------------
# Only these top-level regular files may exist, each at most once, and `ccshelf` must be one.
# Absolute paths, '..', directories, symlinks and hardlinks are therefore all rejected.
tar -tzf "$TMP/$ARCHIVE" >"$TMP/names.txt" 2>/dev/null </dev/null || die "cannot read the archive $ARCHIVE"
tar -tvzf "$TMP/$ARCHIVE" >"$TMP/types.txt" 2>/dev/null </dev/null || die "cannot read the archive $ARCHIVE"
n_names="$(wc -l <"$TMP/names.txt" | tr -d ' ')"
n_types="$(wc -l <"$TMP/types.txt" | tr -d ' ')"
[ "$n_names" = "$n_types" ] || die "the archive listing is inconsistent; refusing to extract"
if [ "$n_names" -lt 1 ] || [ "$n_names" -gt 3 ]; then
  die "the archive has $n_names entries, expected ccshelf with at most LICENSE and README.md"
fi
if awk 'BEGIN {bad=0} {if ($0 != "ccshelf" && $0 != "LICENSE" && $0 != "README.md") bad=1; if (seen[$0]++) bad=1} END {exit bad}' "$TMP/names.txt"; then
  :
else
  die "the archive contains an unexpected, duplicate or unsafe entry (only ccshelf, LICENSE and README.md are allowed); refusing to extract"
fi
grep -qx 'ccshelf' "$TMP/names.txt" || die "the archive does not contain ccshelf at its top level"
if awk '{c=substr($0, 1, 1); if (c != "-") bad=1} END {exit bad}' "$TMP/types.txt"; then
  :
else
  die "the archive contains a link, directory or special file; refusing to extract"
fi

if [ "$DRY_RUN" -eq 1 ]; then
  say "dry run: $ARCHIVE ($VERSION) downloaded and verified; nothing was installed"
  exit 0
fi

# ---- the target directory ---------------------------------------------------
if [ ! -e "$BIN_DIR" ] && [ ! -L "$BIN_DIR" ]; then
  (umask 022 && mkdir -p -- "$BIN_DIR") || die "cannot create $BIN_DIR"
fi
[ ! -L "$BIN_DIR" ] || die "$BIN_DIR is a symlink; refusing to install through it (pass a real directory with --bin-dir)"
[ -d "$BIN_DIR" ] || die "$BIN_DIR exists and is not a directory"
if [ -z "$(find "$BIN_DIR" -prune -user "$(id -u)" 2>/dev/null)" ]; then
  die "$BIN_DIR is not owned by you; refusing to install into it"
fi
if [ -n "$(find "$BIN_DIR" -prune -perm -0002 2>/dev/null)" ]; then
  die "$BIN_DIR is writable by other users; refusing to install into it"
fi
[ -w "$BIN_DIR" ] || die "$BIN_DIR is not writable"

TARGET="$BIN_DIR/ccshelf"
if [ -e "$TARGET" ] || [ -L "$TARGET" ]; then
  # A real directory is never replaced, not even with --force: moving the new binary "into" it
  # would report success without installing anything.
  if [ -d "$TARGET" ] && [ ! -L "$TARGET" ]; then
    die "$TARGET is a directory; refusing to replace it (--force does not replace directories; move it away and retry)"
  fi
  if [ "$FORCE" -eq 1 ]; then
    say "--force: replacing $TARGET"
  else
    [ ! -L "$TARGET" ] || die "$TARGET is a symlink; refusing to replace it (use --force to replace it anyway)"
    [ -f "$TARGET" ] || die "$TARGET exists and is not a regular file (use --force to replace it anyway)"
    # Identify the existing file without running it first: it must contain the name ccshelf. Only
    # then is it run, for its version, and only under a 5-second limit. Without `timeout` (stock
    # macOS) it is not run at all and the name check alone decides.
    grep -aqF 'ccshelf' "$TARGET" 2>/dev/null </dev/null ||
      die "$TARGET exists and does not look like ccshelf (use --force to replace it anyway)"
    if [ -n "$TIMEOUT_TOOL" ]; then
      old="$("$TIMEOUT_TOOL" 5 "$TARGET" version 2>/dev/null </dev/null | head -c 1024 | head -n 1 || true)"
      case "$old" in
        "ccshelf "*) say "replacing the installed ${old%% (*}" ;;
        *) die "$TARGET exists and does not look like ccshelf (use --force to replace it anyway)" ;;
      esac
    else
      say "replacing the existing ccshelf (not run: no timeout tool to bound it)"
    fi
  fi
fi

# ---- extract and install ----------------------------------------------------
# The one entry is streamed to a file we name ourselves: the archive never chooses a path, an
# owner or a mode. The size cap bounds a decompression bomb.
tar -xzOf "$TMP/$ARCHIVE" ccshelf </dev/null 2>/dev/null | head -c "$((MAX_BINARY_BYTES + 1))" >"$TMP/ccshelf" ||
  die "cannot extract ccshelf from $ARCHIVE"
size="$(file_size "$TMP/ccshelf")"
[ "$size" -gt 0 ] || die "the ccshelf entry of $ARCHIVE is empty"
[ "$size" -le "$MAX_BINARY_BYTES" ] || die "the ccshelf entry of $ARCHIVE is larger than $MAX_BINARY_BYTES bytes"

VERIFIED_SUM="$(sha256_of "$TMP/ccshelf")"
STAGE="$BIN_DIR/.ccshelf.new.$$"
rm -f "$STAGE"
cp "$TMP/ccshelf" "$STAGE" || die "cannot write into $BIN_DIR"
chmod 755 "$STAGE"
# A symlink named ccshelf (only reachable with --force) is removed itself: mv would otherwise
# follow a link to a directory and move the binary into it, outside this directory.
if [ -L "$TARGET" ]; then
  rm -f -- "$TARGET" || die "cannot remove the symlink $TARGET"
fi
mv -f "$STAGE" "$TARGET" || die "cannot move the new binary into place at $TARGET"
STAGE=""
# Whatever the file system did, what is at TARGET must be our regular file with the verified bytes.
if [ ! -f "$TARGET" ] || [ -L "$TARGET" ]; then
  die "$TARGET is not a regular file after the install; the binary was not installed there"
fi
[ "$(sha256_of "$TARGET")" = "$VERIFIED_SUM" ] ||
  die "the file at $TARGET does not match the verified binary; the install is not trustworthy"

say "installed $TARGET"
if [ "$QUIET" -eq 0 ]; then
  "$TARGET" version </dev/null || warn "installed, but '$TARGET version' failed"
fi

case ":$PATH:" in
  *":$BIN_DIR:"*)
    found="$(command -v ccshelf 2>/dev/null || true)"
    if [ -n "$found" ] && [ "$found" != "$TARGET" ]; then
      warn "another ccshelf comes first on your PATH: $found (this install is $TARGET)"
    fi
    ;;
  *)
    say "$BIN_DIR is not on your PATH. Add it for this shell with:"
    say "  export PATH=\"$BIN_DIR:\$PATH\""
    say "and put the same line in your shell profile to keep it."
    ;;
esac
