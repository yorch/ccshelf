#!/usr/bin/env bash
# Offline tests for action/scripts/install.sh. Run: bash action/test/run.sh
# Builds a fake release directory and installs from it through file:// URLs.
# No network, no real cosign. The Windows zip path is reviewed, not run here.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL="$HERE/../scripts/install.sh"

case "$(uname -s)" in
  MINGW* | MSYS* | CYGWIN*)
    echo "SKIP: these tests build tar.gz archives and need a Unix-like runner"
    exit 0
    ;;
esac

ROOT="$(mktemp -d "${TMPDIR:-/tmp}/ccshelf-action-test.XXXXXX")" || exit 1
[ -n "$ROOT" ] && [ -d "$ROOT" ] || exit 1
trap '/bin/rm -rf "$ROOT"' EXIT

case "$(uname -s)" in Darwin) OS=darwin ;; *) OS=linux ;; esac
case "$(uname -m)" in arm64 | aarch64) ARCH=arm64 ;; *) ARCH=amd64 ;; esac
VERSION="v0.0.0-test"
ARCHIVE="ccshelf_${VERSION}_${OS}_${ARCH}.tar.gz"

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

PASS=0
FAIL=0
OUT=""
RC=0
N=0
LAST_TMP=""

# run_install [VAR=value ...]: runs install.sh in a clean env, sets OUT and RC.
run_install() {
  N=$((N + 1))
  local tmp="$ROOT/run$N"
  mkdir -p "$tmp"
  : >"$tmp/out"
  : >"$tmp/path"
  OUT="$(
    env -i HOME="$tmp" PATH="$SAFE_PATH" RUNNER_TEMP="$tmp" \
      GITHUB_OUTPUT="$tmp/out" GITHUB_PATH="$tmp/path" \
      INPUT_VERSION="$VERSION" INPUT_BASE_URL="$BASE" \
      "$@" bash "$INSTALL" 2>&1
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
if [ -x "$BINP" ] && [ "$("$BINP" lint)" = "fake ccshelf lint" ]; then
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
run_install INPUT_BASE_URL="file://$REL x" INPUT_SHA256="$GOOD_SHA"
expect_fail "whitespace in base-url fails" "whitespace"
run_install INPUT_WORKING_DIRECTORY="../escape" INPUT_SHA256="$GOOD_SHA"
expect_fail "'..' in working-directory fails" "'..'"
run_install INPUT_WORKING_DIRECTORY="/etc" INPUT_SHA256="$GOOD_SHA"
expect_fail "absolute working-directory fails" "relative"
run_install INPUT_VERIFY_SIGNATURE=maybe
expect_fail "bad verify-signature value fails" "'true' or 'false'"

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
case "$ARGS" in
  *"verify-blob"*"--bundle "*"checksums.txt.sigstore.json"*"--certificate-identity-regexp ^https://github\\.com/ccshelf/ccshelf/\\.github/workflows/release\\.yml@refs/tags/v.*\$"*"--certificate-oidc-issuer https://token.actions.githubusercontent.com"*)
    pass "cosign called with bundle, default identity and issuer"
    ;;
  *)
    OUT="$ARGS"
    fail "cosign called with bundle, default identity and issuer" "unexpected args"
    ;;
esac

run_install PATH="$COSIGN_OK:$SAFE_PATH" \
  INPUT_COSIGN_IDENTITY='^https://ghe.example/acme/mirror/.*$' \
  INPUT_COSIGN_OIDC_ISSUER="https://ghe.example/_services/token"
ARGS="$(cat "$COSIGN_ARGS" 2>/dev/null || true)"
case "$ARGS" in
  *"--certificate-identity-regexp ^https://ghe.example/acme/mirror/.*\$"*"--certificate-oidc-issuer https://ghe.example/_services/token"*)
    expect_ok "custom cosign identity and issuer are passed"
    ;;
  *)
    OUT="$ARGS"
    fail "custom cosign identity and issuer are passed" "unexpected args"
    ;;
esac

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

# ---- the action's argument splitting: no eval, no globbing ------------------
SPLIT="$(INPUT_ARGS='catalog build --out dist/*' bash -c 'read -r -a a <<<"$INPUT_ARGS"; printf "[%s]" "${a[@]}"')"
if [ "$SPLIT" = "[catalog][build][--out][dist/*]" ]; then
  pass "args split without globbing"
else
  OUT="$SPLIT"
  fail "args split without globbing" "got $SPLIT"
fi

echo
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ]
