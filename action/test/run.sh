#!/usr/bin/env bash
# Offline tests for action/scripts/install.sh. Run: bash action/test/run.sh
# Builds a fake release directory and installs from it through file:// URLs.
# No network, no real cosign. The runner OS and architecture are forced through
# RUNNER_OS/RUNNER_ARCH so the suite behaves the same on Linux, macOS and Git Bash on
# Windows; only the checks that truly need a POSIX file system or a zip builder skip.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL="$HERE/../scripts/install.sh"
REPO_ROOT="$(cd "$HERE/../.." && pwd)"
IS_WINDOWS=false
case "$(uname -s)" in
  MINGW* | MSYS* | CYGWIN*) IS_WINDOWS=true ;;
esac

ROOT="$(mktemp -d "${TMPDIR:-/tmp}/ccshelf-action-test.XXXXXX")" || exit 1
[ -n "$ROOT" ] && [ -d "$ROOT" ] || exit 1
trap '/bin/rm -rf "$ROOT"' EXIT

OS=linux
ARCH=amd64
VERSION="v0.0.0-test"
# goreleaser's {{ .Version }} has no leading v; the release path keeps it.
ARCHIVE="ccshelf_${VERSION#v}_${OS}_${ARCH}.tar.gz"

sha() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

# ---- fake release ----------------------------------------------------------
REL="$ROOT/release"
mkdir -p "$REL/$VERSION" "$ROOT/pkg"
printf '#!/bin/sh\necho "fake ccshelf $*"\n' >"$ROOT/pkg/ccshelf"
chmod +x "$ROOT/pkg/ccshelf"
tar -czf "$REL/$VERSION/$ARCHIVE" -C "$ROOT/pkg" ccshelf
GOOD_SHA="$(sha "$REL/$VERSION/$ARCHIVE")"
OTHER_SHA="$(printf '%064d' 7)"
printf '%s  %s\n%s  %s\n' "$GOOD_SHA" "$ARCHIVE" "$OTHER_SHA" "other_file.zip" >"$REL/$VERSION/checksums.txt"
echo '{"fake":"sigstore bundle"}' >"$REL/$VERSION/checksums.txt.sigstore.json"
BASE="file://$REL"

# ---- fake cosign -----------------------------------------------------------
COSIGN_OK="$ROOT/cosign-ok"
COSIGN_BAD="$ROOT/cosign-bad"
COSIGN_ARGS="$ROOT/cosign-args.txt"
mkdir -p "$COSIGN_OK" "$COSIGN_BAD"
printf '#!/bin/sh\necho "$@" >"%s"\nexit 0\n' "$COSIGN_ARGS" >"$COSIGN_OK/cosign"
printf '#!/bin/sh\necho "fake cosign: signature invalid" >&2\nexit 1\n' >"$COSIGN_BAD/cosign"
chmod +x "$COSIGN_OK/cosign" "$COSIGN_BAD/cosign"

# A PATH with the basic tools but no cosign.
SAFE_PATH="/usr/bin:/bin"

SKIPPED=0
skip() {
  echo "skip $1: $2"
  SKIPPED=$((SKIPPED + 1))
}

PASS=0
FAIL=0
OUT=""
RC=0
N=0
LAST_TMP=""

# run_install [VAR=value ...]: runs install.sh (or $INSTALL_SCRIPT) in a clean env, sets OUT and RC.
INSTALL_SCRIPT=""
run_install() {
  N=$((N + 1))
  local tmp="$ROOT/run$N"
  mkdir -p "$tmp"
  : >"$tmp/out"
  : >"$tmp/path"
  OUT="$(
    env -i HOME="$tmp" PATH="$SAFE_PATH" RUNNER_TEMP="$tmp" \
      GITHUB_OUTPUT="$tmp/out" GITHUB_PATH="$tmp/path" \
      RUNNER_OS=Linux RUNNER_ARCH=X64 \
      INPUT_VERSION="$VERSION" INPUT_BASE_URL="$BASE" \
      "$@" bash "${INSTALL_SCRIPT:-$INSTALL}" 2>&1
  )"
  RC=$?
  LAST_TMP="$tmp"
}

pass() {
  echo "ok   $1"
  PASS=$((PASS + 1))
}
fail() {
  echo "FAIL $1: $2"
  echo "$OUT" | sed 's/^/     | /'
  FAIL=$((FAIL + 1))
}

expect_ok() { # name [needle]
  if [ "$RC" -ne 0 ]; then
    fail "$1" "expected success, got exit $RC"
    return
  fi
  if [ -n "${2:-}" ] && ! printf '%s' "$OUT" | grep -qF -- "$2"; then
    fail "$1" "output lacks '$2'"
    return
  fi
  pass "$1"
}
expect_fail() { # name needle
  if [ "$RC" -eq 0 ]; then
    fail "$1" "expected failure, got success"
    return
  fi
  if ! printf '%s' "$OUT" | grep -qF -- "$2"; then
    fail "$1" "output lacks '$2'"
    return
  fi
  pass "$1"
}

# ---- pinned sha256 ---------------------------------------------------------
run_install INPUT_SHA256="$GOOD_SHA"
expect_ok "sha256 pin: success" "matches the pinned sha256"
if grep -q '^path=' "$LAST_TMP/out" && grep -q "^version=$VERSION\$" "$LAST_TMP/out"; then
  pass "outputs path and version written"
else
  fail "outputs path and version written" "GITHUB_OUTPUT: $(cat "$LAST_TMP/out")"
fi
BINP="$(sed -n 's/^path=//p' "$LAST_TMP/out")"
if [ -x "$BINP" ] && [ "$("$BINP" lint 2>/dev/null)" = "fake ccshelf lint" ]; then
  pass "installed binary is executable"
else
  fail "installed binary is executable" "path=$BINP"
fi
if [ -s "$LAST_TMP/path" ]; then
  pass "directory appended to GITHUB_PATH"
else
  fail "directory appended to GITHUB_PATH" "empty"
fi

run_install INPUT_SHA256="$(printf '%064d' 0)"
expect_fail "wrong sha256 pin fails" "mismatch"

run_install INPUT_SHA256="ABC"
expect_fail "malformed sha256 fails" "64 hex"

run_install INPUT_SHA256="$(echo "$GOOD_SHA" | tr 'a-f' 'A-F')"
expect_ok "uppercase sha256 pin is accepted"

# ---- tampered archive ------------------------------------------------------
TAMPER="$ROOT/tamper"
mkdir -p "$TAMPER/$VERSION" "$ROOT/pkg2"
printf '#!/bin/sh\necho evil\n' >"$ROOT/pkg2/ccshelf"
chmod +x "$ROOT/pkg2/ccshelf"
tar -czf "$TAMPER/$VERSION/$ARCHIVE" -C "$ROOT/pkg2" ccshelf
cp "$REL/$VERSION/checksums.txt" "$REL/$VERSION/checksums.txt.sigstore.json" "$TAMPER/$VERSION/"
run_install INPUT_BASE_URL="file://$TAMPER" INPUT_SHA256="$GOOD_SHA"
expect_fail "tampered archive fails the pin" "mismatch"
run_install INPUT_BASE_URL="file://$TAMPER" PATH="$COSIGN_OK:$SAFE_PATH"
expect_fail "tampered archive fails the checksums line" "mismatch"
run_install INPUT_BASE_URL="file://$TAMPER" INPUT_VERIFY_SIGNATURE=false
expect_fail "tampered archive fails with opt-out too" "mismatch"

# ---- checksums.txt problems ------------------------------------------------
NOLINE="$ROOT/noline"
mkdir -p "$NOLINE/$VERSION"
cp "$REL/$VERSION/$ARCHIVE" "$REL/$VERSION/checksums.txt.sigstore.json" "$NOLINE/$VERSION/"
echo "$GOOD_SHA  some_other_archive.tar.gz" >"$NOLINE/$VERSION/checksums.txt"
run_install INPUT_BASE_URL="file://$NOLINE" PATH="$COSIGN_OK:$SAFE_PATH"
expect_fail "missing checksums line fails" "exactly one line"

DUP="$ROOT/dup"
mkdir -p "$DUP/$VERSION"
cp "$REL/$VERSION/$ARCHIVE" "$REL/$VERSION/checksums.txt.sigstore.json" "$DUP/$VERSION/"
printf '%s  %s\n%s  %s\n' "$GOOD_SHA" "$ARCHIVE" "$GOOD_SHA" "$ARCHIVE" >"$DUP/$VERSION/checksums.txt"
run_install INPUT_BASE_URL="file://$DUP" PATH="$COSIGN_OK:$SAFE_PATH"
expect_fail "duplicate checksums line fails" "exactly one line"

# ---- invalid versions ------------------------------------------------------
for v in "1.2.3" "v1.2" "latest" "main" "v1.2.3;rm -rf /" 'v1.2.3$(id)' "v1.2.3/../x" "v1.2.3-" "../v1.2.3"; do
  run_install INPUT_VERSION="$v" INPUT_SHA256="$GOOD_SHA"
  expect_fail "invalid version '$v' fails" "version"
done
run_install INPUT_VERSION="" INPUT_SHA256="$GOOD_SHA"
expect_fail "empty version fails" "required"

# ---- unsafe inputs ---------------------------------------------------------
run_install INPUT_BASE_URL="file://$REL/../release" INPUT_SHA256="$GOOD_SHA"
expect_fail "'..' in base-url fails" "'..'"
run_install INPUT_BASE_URL="http://example.invalid/releases" INPUT_SHA256="$GOOD_SHA"
expect_fail "http base-url fails" "https:// or file://"
run_install INPUT_BASE_URL="ftp://example.invalid" INPUT_SHA256="$GOOD_SHA"
expect_fail "ftp base-url fails" "https:// or file://"
run_install INPUT_BASE_URL="file://relative/dir" INPUT_SHA256="$GOOD_SHA"
expect_fail "file:// without a third slash fails" "file:///"
run_install INPUT_BASE_URL="file://host/share" INPUT_SHA256="$GOOD_SHA"
expect_fail "file:// with a host fails" "file:///"
run_install INPUT_BASE_URL="file://C:/mirror" INPUT_SHA256="$GOOD_SHA"
expect_fail "file:// with a drive letter is accepted (the copy then fails here)" "cannot copy"
run_install INPUT_BASE_URL='https://example.invalid/a$b' INPUT_SHA256="$GOOD_SHA"
expect_fail "odd characters in an https base-url fail" "URL-safe"
run_install INPUT_BASE_URL="file://$REL x" INPUT_SHA256="$GOOD_SHA"
expect_fail "whitespace in base-url fails" "whitespace"
run_install INPUT_WORKING_DIRECTORY="../escape" INPUT_SHA256="$GOOD_SHA"
expect_fail "'..' in working-directory fails" "'..'"
run_install INPUT_WORKING_DIRECTORY="/etc" INPUT_SHA256="$GOOD_SHA"
expect_fail "absolute working-directory fails" "relative"
run_install INPUT_VERIFY_SIGNATURE=maybe
expect_fail "bad verify-signature value fails" "'true' or 'false'"

NL=$'\n'
for pair in \
  "INPUT_VERSION=${VERSION}${NL}evil" \
  "INPUT_SHA256=${GOOD_SHA}${NL}x" \
  "INPUT_BASE_URL=${BASE}${NL}x" \
  "INPUT_WORKING_DIRECTORY=sub${NL}x" \
  "INPUT_COSIGN_IDENTITY=a${NL}b" \
  "INPUT_COSIGN_IDENTITY_REGEXP=a${NL}b" \
  "INPUT_COSIGN_OIDC_ISSUER=https://a${NL}b" \
  "INPUT_TRUSTED_ROOT=a${NL}b" \
  "INPUT_VERIFY_SIGNATURE=true${NL}x"; do
  run_install INPUT_SHA256="$GOOD_SHA" "$pair"
  expect_fail "newline in ${pair%%=*} fails" "control characters"
done
run_install INPUT_SHA256="$GOOD_SHA" "INPUT_WORKING_DIRECTORY=a"$'\r'"b"
expect_fail "carriage return in working-directory fails" "control characters"

# ---- signature path --------------------------------------------------------
run_install
expect_fail "verify-signature without cosign fails closed" "'cosign' is not on PATH"
if printf '%s' "$OUT" | grep -q "set the 'sha256' input"; then
  pass "missing cosign message explains the options"
else
  fail "missing cosign message explains the options" "no instruction"
fi

run_install PATH="$COSIGN_OK:$SAFE_PATH"
expect_ok "fake cosign succeeds" "signature verified"
ARGS="$(cat "$COSIGN_ARGS" 2>/dev/null || true)"
EXACT="https://github.com/yorch/ccshelf/.github/workflows/release.yml@refs/tags/${VERSION}"
case "$ARGS" in
  *"verify-blob"*"--bundle "*"checksums.txt.sigstore.json --certificate-identity ${EXACT} --certificate-oidc-issuer https://token.actions.githubusercontent.com"*)
    pass "cosign called with bundle, the exact default identity and issuer"
    ;;
  *)
    OUT="$ARGS"
    fail "cosign called with bundle, the exact default identity and issuer" "unexpected args"
    ;;
esac
case "$ARGS" in
  *"--certificate-identity-regexp"* | *"--trusted-root"*)
    OUT="$ARGS"
    fail "default call has neither a regexp identity nor a trusted root" "unexpected args"
    ;;
  *) pass "default call has neither a regexp identity nor a trusted root" ;;
esac

run_install PATH="$COSIGN_OK:$SAFE_PATH" \
  INPUT_COSIGN_IDENTITY='https://ghe.example/acme/mirror/.github/workflows/release.yml@refs/tags/v0.0.0-test' \
  INPUT_COSIGN_OIDC_ISSUER="https://ghe.example/_services/token"
ARGS="$(cat "$COSIGN_ARGS" 2>/dev/null || true)"
case "$ARGS" in
  *"--certificate-identity https://ghe.example/acme/mirror/.github/workflows/release.yml@refs/tags/v0.0.0-test --certificate-oidc-issuer https://ghe.example/_services/token"*)
    expect_ok "custom exact cosign identity and issuer are passed"
    ;;
  *)
    OUT="$ARGS"
    fail "custom exact cosign identity and issuer are passed" "unexpected args"
    ;;
esac

run_install PATH="$COSIGN_OK:$SAFE_PATH" \
  INPUT_COSIGN_IDENTITY_REGEXP='^https://ghe\.example/acme/.*$'
ARGS="$(cat "$COSIGN_ARGS" 2>/dev/null || true)"
case "$ARGS" in
  *'--certificate-identity-regexp ^https://ghe\.example/acme/.*$ --certificate-oidc-issuer'*)
    expect_ok "an explicit identity regexp is passed as a regexp"
    ;;
  *)
    OUT="$ARGS"
    fail "an explicit identity regexp is passed as a regexp" "unexpected args"
    ;;
esac

run_install PATH="$COSIGN_OK:$SAFE_PATH" INPUT_COSIGN_IDENTITY="x" INPUT_COSIGN_IDENTITY_REGEXP="y"
expect_fail "exact identity and regexp together fail" "only one of"

run_install PATH="$COSIGN_OK:$SAFE_PATH" INPUT_COSIGN_OIDC_ISSUER="http://insecure.example"
expect_fail "non-https issuer fails" "https://"

ROOTFILE="$ROOT/trusted_root.json"
echo '{}' >"$ROOTFILE"
run_install PATH="$COSIGN_OK:$SAFE_PATH" INPUT_TRUSTED_ROOT="$ROOTFILE"
ARGS="$(cat "$COSIGN_ARGS" 2>/dev/null || true)"
case "$ARGS" in
  *"--trusted-root $ROOTFILE"*) expect_ok "trusted-root is passed to cosign" ;;
  *)
    OUT="$ARGS"
    fail "trusted-root is passed to cosign" "unexpected args"
    ;;
esac
run_install PATH="$COSIGN_OK:$SAFE_PATH" INPUT_TRUSTED_ROOT="$ROOT/missing_root.json"
expect_fail "missing trusted-root file fails" "not a file"

run_install PATH="$COSIGN_BAD:$SAFE_PATH"
expect_fail "failing cosign fails closed" "refusing to install"
if [ ! -s "$LAST_TMP/out" ]; then
  pass "nothing written to GITHUB_OUTPUT after a failed verification"
else
  fail "nothing written to GITHUB_OUTPUT after a failed verification" "$(cat "$LAST_TMP/out")"
fi

NOBUNDLE="$ROOT/nobundle"
mkdir -p "$NOBUNDLE/$VERSION"
cp "$REL/$VERSION/$ARCHIVE" "$REL/$VERSION/checksums.txt" "$NOBUNDLE/$VERSION/"
run_install INPUT_BASE_URL="file://$NOBUNDLE" PATH="$COSIGN_OK:$SAFE_PATH"
expect_fail "missing sigstore bundle fails" "cannot copy"

# ---- explicit opt-out ------------------------------------------------------
run_install INPUT_VERIFY_SIGNATURE=false
expect_ok "opt-out without a pin succeeds" "NOT authenticated"
if printf '%s' "$OUT" | grep -q "::warning"; then
  pass "opt-out prints a workflow warning annotation"
else
  fail "opt-out prints a workflow warning annotation" "no ::warning"
fi

run_install INPUT_VERIFY_SIGNATURE=false INPUT_SHA256="$GOOD_SHA"
if [ "$RC" -eq 0 ] && ! printf '%s' "$OUT" | grep -q "NOT authenticated"; then
  pass "opt-out with a pin gives no warning"
else
  fail "opt-out with a pin gives no warning" "rc=$RC"
fi

# ---- missing release -------------------------------------------------------
run_install INPUT_VERSION="v9.9.9" INPUT_SHA256="$GOOD_SHA"
expect_fail "missing archive fails" "cannot copy"

# ---- embedded checksums (pins.txt) ------------------------------------------
# The script finds pins.txt next to its own scripts/ directory, so the cases run a copy
# of the script with its own action/pins.txt.
PINS_DIR="$ROOT/pins-action"
mkdir -p "$PINS_DIR/scripts"
cp "$INSTALL" "$PINS_DIR/scripts/install.sh"
INSTALL_SCRIPT="$PINS_DIR/scripts/install.sh"

printf '# comment\n\n%s %s %s %s\n' "$VERSION" "$OS" "$ARCH" "$GOOD_SHA" >"$PINS_DIR/pins.txt"
run_install
expect_ok "pins.txt hit authenticates without cosign" "embedded in this action"

printf '%s %s %s %s\r\n' "$VERSION" "$OS" "$ARCH" "$GOOD_SHA" >"$PINS_DIR/pins.txt"
run_install
expect_ok "pins.txt with CRLF line endings works" "embedded in this action"

printf '%s %s %s %s  # trailing comment\n' "$VERSION" "$OS" "$ARCH" "$(echo "$GOOD_SHA" | tr 'a-f' 'A-F')" >"$PINS_DIR/pins.txt"
run_install
expect_ok "pins.txt accepts a trailing comment and uppercase hex" "embedded in this action"

printf '%s %s %s %s\n' "$VERSION" "$OS" "$ARCH" "$(printf '%064d' 1)" >"$PINS_DIR/pins.txt"
run_install PATH="$COSIGN_OK:$SAFE_PATH"
expect_fail "pins.txt mismatch fails (cosign does not rescue it)" "pins.txt says"

run_install INPUT_SHA256="$GOOD_SHA"
expect_ok "an explicit sha256 input wins over pins.txt" "pinned sha256 input"

printf 'v9.9.9 %s %s %s\nv0.0.0-test darwin arm64 %s\n' "$OS" "$ARCH" "$GOOD_SHA" "$GOOD_SHA" >"$PINS_DIR/pins.txt"
run_install
expect_fail "no pins.txt line for this version/os/arch falls through to cosign" "'cosign' is not on PATH"
run_install PATH="$COSIGN_OK:$SAFE_PATH"
expect_ok "no pins.txt line falls through to the signature check" "signature verified"

printf '%s %s %s %s\n%s %s %s %s\n' "$VERSION" "$OS" "$ARCH" "$GOOD_SHA" "$VERSION" "$OS" "$ARCH" "$(printf '%064d' 2)" >"$PINS_DIR/pins.txt"
run_install
expect_fail "conflicting pins.txt lines fail" "conflicting"

for bad in "$VERSION $OS $ARCH tooshort" "$VERSION $OS $ARCH $GOOD_SHA extra" "1.2.3 $OS $ARCH $GOOD_SHA" "$VERSION plan9 $ARCH $GOOD_SHA"; do
  printf '%s\n' "$bad" >"$PINS_DIR/pins.txt"
  run_install INPUT_VERIFY_SIGNATURE=false
  expect_fail "malformed pins.txt line fails closed ($bad)" "malformed"
done
INSTALL_SCRIPT=""

# ---- hashing through stdin (Windows paths contain backslashes) --------------
if [ "$IS_WINDOWS" = true ]; then
  skip "backslash path hashing" "Windows file systems do not allow a backslash in a directory name; the fake sha256sum case runs on POSIX"
else
  REAL_SHA="$(command -v sha256sum 2>/dev/null || true)"
  if [ -z "$REAL_SHA" ]; then REAL_SHA="$(command -v shasum) -a 256"; fi
  FAKE_SHA="$ROOT/fake-sha"
  mkdir -p "$FAKE_SHA"
  # Emulates GNU sha256sum: with a file argument whose name contains a backslash the digest
  # is prefixed with a backslash; reading stdin never prints a name.
  cat >"$FAKE_SHA/sha256sum" <<'FAKE'
#!/bin/sh
REAL="@REAL@"
if [ $# -eq 0 ]; then
  exec $REAL
fi
digest="$($REAL <"$1" | awk '{print $1}')"
case "$1" in
  *\\*) printf '\\%s  %s\n' "$digest" "$1" ;;
  *) printf '%s  %s\n' "$digest" "$1" ;;
esac
FAKE
  sed "s|@REAL@|$REAL_SHA|" "$FAKE_SHA/sha256sum" >"$FAKE_SHA/sha256sum.tmp"
  mv "$FAKE_SHA/sha256sum.tmp" "$FAKE_SHA/sha256sum"
  chmod +x "$FAKE_SHA/sha256sum"
  BSDIR="$ROOT/back\\slash"
  mkdir -p "$BSDIR"
  printf 'x' >"$BSDIR/probe"
  PROBE="$("$FAKE_SHA/sha256sum" "$BSDIR/probe")"
  case "$PROBE" in
    '\'*) pass "fake sha256sum emulates the backslash prefix for path arguments" ;;
    *) fail "fake sha256sum emulates the backslash prefix for path arguments" "got: $PROBE" ;;
  esac
  run_install PATH="$FAKE_SHA:$SAFE_PATH" RUNNER_TEMP="$BSDIR" INPUT_SHA256="$GOOD_SHA"
  expect_ok "archive in a path with a backslash hashes correctly" "matches the pinned sha256"
fi

# ---- names agree with .goreleaser.yaml ---------------------------------------
# The script must request exactly the archive names goreleaser produces. The fixture is
# the checksums.txt of a real `goreleaser release --snapshot` (names and format only).
GR="$REPO_ROOT/.goreleaser.yaml"
FIXTURE="$HERE/testdata/goreleaser-snapshot-checksums.txt"
TEMPLATE_LINE="$(grep -E '^[[:space:]]+name_template: "ccshelf_' "$GR" | head -n 1)"
TEMPLATE="${TEMPLATE_LINE#*\"}"
TEMPLATE="${TEMPLATE%\"*}"
if [ -z "$TEMPLATE" ]; then
  OUT="$TEMPLATE_LINE"
  fail "archive name_template found in .goreleaser.yaml" "no name_template line starting with ccshelf_"
else
  pass "archive name_template found in .goreleaser.yaml"
fi
if grep -qE 'formats: \[tar\.gz\]' "$GR" && grep -qE 'formats: \[zip\]' "$GR"; then
  pass ".goreleaser.yaml archives are tar.gz with a zip override (the script assumes both)"
else
  fail ".goreleaser.yaml archives are tar.gz with a zip override" "formats changed: update install.sh"
fi
SNAP_VERSION="0.0.1-snapshot-none"
for combo in "Linux X64 linux amd64 tar.gz" "Linux ARM64 linux arm64 tar.gz" "macOS X64 darwin amd64 tar.gz" "macOS ARM64 darwin arm64 tar.gz" "Windows X64 windows amd64 zip" "Windows ARM64 windows arm64 zip"; do
  # shellcheck disable=SC2086 # intentional word splitting of the fixed combo string
  set -- $combo
  expected="$(printf '%s' "$TEMPLATE" | sed -e "s|{{ \.Version }}|$SNAP_VERSION|" -e "s|{{ \.Os }}|$3|" -e "s|{{ \.Arch }}|$4|").$5"
  run_install RUNNER_OS="$1" RUNNER_ARCH="$2" INPUT_VERSION="v$SNAP_VERSION" INPUT_BASE_URL="file://$ROOT/nonexistent"
  requested="$(printf '%s\n' "$OUT" | sed -n 's/^ccshelf install: fetching \([^ ]*\) (.*/\1/p')"
  case "$expected" in
    *'{{'*) fail "script requests the goreleaser name for $3/$4" "template not fully expanded: $expected" ;;
    *)
      if [ "$requested" = "$expected" ] && grep -qF "  $expected" "$FIXTURE"; then
        pass "script requests $expected, as goreleaser produces and the snapshot fixture lists"
      else
        fail "script requests the goreleaser name for $3/$4" "script wants '$requested', template gives '$expected'"
      fi
      ;;
  esac
done
run_install INPUT_VERSION="v0.1.0" INPUT_BASE_URL="file://$ROOT/nonexistent"
case "$OUT" in
  *"ccshelf_0.1.0_linux_amd64.tar.gz"*"/nonexistent/v0.1.0/"*) pass "archive name drops the v, the release path keeps it" ;;
  *) fail "archive name drops the v, the release path keeps it" "see output" ;;
esac

# ---- the Windows zip path ------------------------------------------------------
if [ "$IS_WINDOWS" = true ]; then
  skip "windows zip install" "Git Bash has no zip builder; the zip path runs on the Linux and macOS runners with RUNNER_OS=Windows"
elif ! command -v zip >/dev/null 2>&1 || ! command -v unzip >/dev/null 2>&1; then
  skip "windows zip install" "zip and unzip are not both on PATH"
else
  ZIPREL="$ROOT/ziprel/$VERSION"
  ZARCHIVE="ccshelf_${VERSION#v}_windows_amd64.zip"
  mkdir -p "$ZIPREL" "$ROOT/pkgzip"
  printf '#!/bin/sh\necho "fake ccshelf.exe $*"\n' >"$ROOT/pkgzip/ccshelf.exe"
  (cd "$ROOT/pkgzip" && zip -q "$ZIPREL/$ZARCHIVE" ccshelf.exe)
  ZSHA="$(sha "$ZIPREL/$ZARCHIVE")"
  run_install RUNNER_OS=Windows INPUT_BASE_URL="file://$ROOT/ziprel" INPUT_SHA256="$ZSHA" \
    PATH="$SAFE_PATH:$(dirname "$(command -v unzip)")"
  expect_ok "windows zip archive installs ccshelf.exe" "matches the pinned sha256"
  case "$(sed -n 's/^path=//p' "$LAST_TMP/out")" in
    *ccshelf.exe) pass "windows install reports a .exe path" ;;
    *) fail "windows install reports a .exe path" "$(cat "$LAST_TMP/out")" ;;
  esac
fi

# ---- the action's own "Run ccshelf" step: no eval, no globbing ---------------
# Extract the real run script from action.yml (the `run: |` block of the step named
# "Run ccshelf") and execute it, so a change such as `eval` in action.yml is caught.
ACTION_YML="$REPO_ROOT/action/action.yml"
RUN_STEP="$ROOT/run-step.sh"
awk '
  /^    - name: Run ccshelf$/ { in_step = 1; next }
  in_step && /^    - name: / { exit }
  in_step && /^      run: \|$/ { in_run = 1; next }
  in_run {
    if ($0 ~ /^        / || $0 ~ /^$/) { sub(/^        /, ""); print; next }
    exit
  }
' "$ACTION_YML" >"$RUN_STEP"
if [ -s "$RUN_STEP" ] && grep -q 'read -r -a ccshelf_args' "$RUN_STEP"; then
  pass "extracted the Run ccshelf step from action.yml"
else
  OUT="$(cat "$RUN_STEP" 2>/dev/null)"
  fail "extracted the Run ccshelf step from action.yml" "step not found or no longer splits with read -a"
fi

# A fake binary that prints each argument in brackets.
STEPBIN="$ROOT/stepbin"
printf '#!/bin/sh\nprintf "[%%s]" "$@"\n' >"$STEPBIN"
chmod +x "$STEPBIN"
STEPWD="$ROOT/stepwd"
mkdir -p "$STEPWD/sub/dist"
: >"$STEPWD/sub/dist/a.txt"
: >"$STEPWD/sub/dist/b.txt"

# run_step ARGS: runs the extracted step with INPUT_ARGS=ARGS in $STEPWD/sub.
run_step() {
  OUT="$(
    cd "$STEPWD" &&
      env -i PATH="$SAFE_PATH" INPUT_ARGS="$1" INPUT_WORKING_DIRECTORY="sub" CCSHELF_BIN="$STEPBIN" \
        bash -c "$(cat "$RUN_STEP")" 2>&1
  )"
  RC=$?
}

run_step 'catalog build --out dist/*'
if [ "$RC" -eq 0 ] && [ "$OUT" = "[catalog][build][--out][dist/*]" ]; then
  pass "action.yml splits args on spaces without globbing"
else
  fail "action.yml splits args on spaces without globbing" "got: $OUT"
fi

run_step 'lint $(touch pwned) `touch pwned2` ; touch pwned3'
if [ "$RC" -eq 0 ] && [ ! -e "$STEPWD/sub/pwned" ] && [ ! -e "$STEPWD/sub/pwned2" ] && [ ! -e "$STEPWD/sub/pwned3" ] &&
  [ "$OUT" = '[lint][$(touch][pwned)][`touch][pwned2`][;][touch][pwned3]' ]; then
  pass "action.yml never evaluates shell syntax in args"
else
  fail "action.yml never evaluates shell syntax in args" "got: $OUT"
fi

run_step ''
if [ "$RC" -eq 0 ] && printf '%s' "$OUT" | grep -qF "installed at $STEPBIN"; then
  pass "action.yml with empty args only reports the install"
else
  fail "action.yml with empty args only reports the install" "got: $OUT"
fi

run_step "lint${NL}--evil"
expect_fail "action.yml rejects multi-line args" "single line"

# ---- curl is restricted to https, also across redirects ---------------------
# A fake curl records its arguments and serves files from the fake release, so the test
# is offline. Removing either protocol flag from install.sh makes these fail.
FAKE_CURL="$ROOT/fake-curl"
CURL_ARGS="$ROOT/curl-args.txt"
mkdir -p "$FAKE_CURL"
cat >"$FAKE_CURL/curl" <<'FAKE'
#!/bin/sh
out=""
url=""
prev=""
for a in "$@"; do
  if [ "$prev" = "--output" ]; then out="$a"; fi
  prev="$a"
  url="$a"
done
echo "$@" >>"@ARGS@"
cp "@REL@/@VERSION@/${url##*/}" "$out"
FAKE
sed -e "s|@ARGS@|$CURL_ARGS|" -e "s|@REL@|$REL|g" -e "s|@VERSION@|$VERSION|g" "$FAKE_CURL/curl" >"$FAKE_CURL/curl.tmp"
mv "$FAKE_CURL/curl.tmp" "$FAKE_CURL/curl"
chmod +x "$FAKE_CURL/curl"
: >"$CURL_ARGS"
run_install INPUT_BASE_URL="https://mirror.example/releases" INPUT_SHA256="$GOOD_SHA" PATH="$FAKE_CURL:$SAFE_PATH"
expect_ok "https base-url downloads through curl" "matches the pinned sha256"
CARGS="$(cat "$CURL_ARGS")"
case "$CARGS" in
  *"--proto =https "*) pass "curl is limited to https (--proto =https)" ;;
  *) OUT="$CARGS"; fail "curl is limited to https (--proto =https)" "flag missing" ;;
esac
case "$CARGS" in
  *"--proto-redir =https "*) pass "curl redirects are limited to https (--proto-redir =https)" ;;
  *) OUT="$CARGS"; fail "curl redirects are limited to https (--proto-redir =https)" "flag missing" ;;
esac
if [ -n "$CARGS" ] && ! printf '%s\n' "$CARGS" | grep -qv '^-q '; then
  pass "curl ignores ~/.curlrc (-q is the first argument of every call)"
else
  OUT="$CARGS"; fail "curl ignores ~/.curlrc (-q is the first argument of every call)" "-q is not first"
fi
case "$CARGS" in
  *"--fail "*"--location "*) pass "curl fails on HTTP errors and follows redirects only with the proto limits" ;;
  *) OUT="$CARGS"; fail "curl fails on HTTP errors" "flags missing" ;;
esac

# ---- the work directory is private (0700) -----------------------------------
# mktemp -d already creates 0700, so a fake mktemp that returns 0755 proves install.sh
# itself tightens the mode. Modes are not meaningful on Windows file systems.
if [ "$IS_WINDOWS" = true ]; then
  skip "work directory mode" "POSIX modes are not meaningful on Windows"
else
  FAKE_MKTEMP="$ROOT/fake-mktemp"
  mkdir -p "$FAKE_MKTEMP"
  REAL_MKTEMP="$(command -v mktemp)"
  printf '#!/bin/sh\nd="$(%s "$@")" || exit 1\nchmod 755 "$d"\nprintf "%%s\\n" "$d"\n' "$REAL_MKTEMP" >"$FAKE_MKTEMP/mktemp"
  chmod +x "$FAKE_MKTEMP/mktemp"
  run_install INPUT_SHA256="$GOOD_SHA" PATH="$FAKE_MKTEMP:$SAFE_PATH"
  expect_ok "install works with a permissive mktemp" "matches the pinned sha256"
  WORKP="$(dirname "$(dirname "$(sed -n 's/^path=//p' "$LAST_TMP/out")")")"
  MODE="$(ls -ld "$WORKP" | cut -c1-10)"
  if [ "$MODE" = "drwx------" ]; then
    pass "work directory is mode 0700"
  else
    OUT="$WORKP"; fail "work directory is mode 0700" "mode is $MODE"
  fi
fi

# ---- version '..' is rejected explicitly ------------------------------------
# The strict semver pattern allows dots in a pre-release part, so the explicit check is
# the only thing stopping v1.2.3-a..b from reaching the URL.
run_install INPUT_VERSION="v1.2.3-rc..1" INPUT_SHA256="$GOOD_SHA"
expect_fail "version with '..' in the pre-release part fails" "version must not contain '..'"

# ---- pins.txt whose last line has no trailing newline -----------------------
PINS2_DIR="$ROOT/pins2-action"
mkdir -p "$PINS2_DIR/scripts"
cp "$INSTALL" "$PINS2_DIR/scripts/install.sh"
INSTALL_SCRIPT="$PINS2_DIR/scripts/install.sh"
printf '%s %s %s %s' "$VERSION" "$OS" "$ARCH" "$GOOD_SHA" >"$PINS2_DIR/pins.txt"
run_install
expect_ok "pins.txt without a final newline still counts its last line" "embedded in this action"
printf '%s %s %s tooshort' "$VERSION" "$OS" "$ARCH" >"$PINS2_DIR/pins.txt"
run_install INPUT_VERIFY_SIGNATURE=false
expect_fail "a malformed last line without a newline fails closed" "malformed"
INSTALL_SCRIPT=""

echo
echo "passed: $PASS  failed: $FAIL  skipped: $SKIPPED"
[ "$FAIL" -eq 0 ]
