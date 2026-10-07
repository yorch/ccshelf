#!/usr/bin/env bash
# Offline, hermetic tests for scripts/install.sh. Run: bash scripts/test_install.sh
#
#   bash scripts/test_install.sh             run the test suite
#   bash scripts/test_install.sh --mutants   mutation-test the installer: each deliberate defect
#                                            (applied to a temp copy) must make the suite fail
#   CCSHELF_INSTALL_SCRIPT=path ...          test another copy of the installer
#   CCSHELF_TEST_SH=dash ...                 run the installer with another POSIX shell
#   CCSHELF_TEST_FAILFAST=1 ...              stop at the first failure
#
# Releases are local trees reached through file:/// (and, for the https code path, a fake curl or
# wget that maps one https URL onto the tree). Runs use a private PATH made of symlinks to the
# basic tools only, so a real curl, wget or cosign on the host can never leak in. Nothing here
# touches the network, ~/.claude or the real home directory.
# shellcheck disable=SC2016,SC2015,SC2012,SC2034 # literal installer text as anchors; test helpers
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL="${CCSHELF_INSTALL_SCRIPT:-$HERE/install.sh}"
TEST_SH="${CCSHELF_TEST_SH:-sh}"
TEST_SH="$(command -v "$TEST_SH")" || {
  echo "shell not found: ${CCSHELF_TEST_SH:-sh}" >&2
  exit 1
}

ROOT="$(mktemp -d "${TMPDIR:-/tmp}/ccshelf-install-test.XXXXXX")" || exit 1
[ -n "$ROOT" ] && [ -d "$ROOT" ] || exit 1
trap '/bin/rm -rf "$ROOT"' EXIT

# ---- mutation mode ----------------------------------------------------------
if [ "${1:-}" = "--mutants" ]; then
  command -v python3 >/dev/null 2>&1 || {
    echo "python3 is needed for --mutants" >&2
    exit 1
  }
  survivors=0
  total=0
  mutate() { # name old new   (old must occur exactly once in the installer)
    total=$((total + 1))
    local copy="$ROOT/mutant-$total.sh"
    if ! python3 -I - "$INSTALL" "$copy" "$2" "$3" <<'PY'; then
import sys
src, dst, old, new = sys.argv[1:5]
text = open(src).read()
if text.count(old) != 1:
    sys.exit("mutation anchor must occur exactly once (%d): %r" % (text.count(old), old))
open(dst, "w").write(text.replace(old, new))
PY
      echo "MUTANT SETUP FAILED: $1"
      survivors=$((survivors + 1))
      return
    fi
    chmod +x "$copy"
    if CCSHELF_TEST_FAILFAST=1 CCSHELF_INSTALL_SCRIPT="$copy" bash "$0" >"$ROOT/mutant-$total.log" 2>&1; then
      echo "SURVIVED: $1 (the suite still passes)"
      survivors=$((survivors + 1))
    else
      echo "killed:   $1"
    fi
  }
  mutate "no SHA-256 comparison" 'if [ "$ACTUAL" != "$EXPECTED" ]; then' 'if false; then'
  mutate "no exactly-one checksum line" '[ "$count" = "1" ] ||' '[ "$count" -ge 1 ] ||'
  mutate "no archive entry allowlist" "if awk 'BEGIN {bad=0}" "if true || awk 'BEGIN {bad=0}"
  mutate "no link or directory check" "if awk '{c=substr" "if true || awk '{c=substr"
  mutate "no top-level ccshelf check" "grep -qx 'ccshelf' \"\$TMP/names.txt\" ||" 'true ||'
  mutate "no --proto" "--proto '=https' " ''
  mutate "no --proto-redir" "--proto-redir '=https' " ''
  mutate "no --max-filesize" '--max-filesize "$_max" ' ''
  mutate "private temp dir not chmod 700" 'chmod 700 "$TMP"' 'true'
  mutate "http base-url accepted" 'http://*) die "--base-url must be https (plain http is refused): $BASE_URL" ;;' 'http://*) ;;'
  mutate "cosign failure ignored" 'die "cosign could not verify the signature of checksums.txt for $VERSION; refusing to install"' 'warn "ignored"'
  mutate "--require-signature ignored" 'elif [ "$REQUIRE_SIG" -eq 1 ]; then' 'elif false; then'
  mutate "wrong default cosign identity" 'workflows/release.yml@refs/tags/$VERSION"' 'workflows/release.yml@refs/heads/main"'
  mutate "symlinked bin dir accepted" '[ ! -L "$BIN_DIR" ] || die "$BIN_DIR is a symlink' 'true || die "$BIN_DIR is a symlink'
  mutate "other-writable bin dir accepted" 'if [ -n "$(find "$BIN_DIR" -prune -perm -0002 2>/dev/null)" ]; then' 'if false; then'
  mutate "foreign-owned bin dir accepted" 'if [ -z "$(find "$BIN_DIR" -prune -user "$(id -u)" 2>/dev/null)" ]; then' 'if false; then'
  mutate "non-ccshelf file replaced" '*) die "$TARGET exists and does not look like ccshelf (use --force to replace it anyway)" ;;' '*) ;;'
  mutate "dry run installs" 'if [ "$DRY_RUN" -eq 1 ]; then
  say "dry run: $ARCHIVE' 'if false; then
  say "dry run: $ARCHIVE'
  mutate "version regex accepts anything" "VERSION_RE='^v[0-9]+\\.[0-9]+\\.[0-9]+(-[0-9A-Za-z.-]+)?\$'" "VERSION_RE='.*'"
  mutate "no non-ASCII/control check" '[ "$_bad" = "0" ] ||' 'true ||'
  mutate "install mode not 755" 'chmod 755 "$STAGE"' 'true'
  mutate "latest not re-pinned to its tag" 'REL="$BASE_URL/download/$VERSION"' 'REL="$BASE_URL/latest/download"'
  echo
  echo "mutants: $total  survived: $survivors"
  [ "$survivors" -eq 0 ]
  exit $?
fi

# ---- fixtures ---------------------------------------------------------------
case "$(uname -s)" in
  Linux) OS=linux ;;
  Darwin) OS=darwin ;;
  *)
    echo "skip: the installer supports Linux and macOS only"
    exit 0
    ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) ARCH=amd64 ;;
  arm64 | aarch64) ARCH=arm64 ;;
  *)
    echo "skip: unsupported architecture"
    exit 0
    ;;
esac
export COPYFILE_DISABLE=1 # bsdtar: no AppleDouble entries in the fixture archives

TAG="v0.0.0-test"
BARE="${TAG#v}"
ARCHIVE="ccshelf_${BARE}_${OS}_${ARCH}.tar.gz"
NOW="$(id -u)"

# A private PATH of symlinks to the basic tools. mktools DIR [tool-to-leave-out ...]
BASIC_TOOLS="awk basename cat chmod cp cut dirname find grep gzip head id ls mkdir mktemp mv openssl printf rm sed sh sha256sum shasum sleep sort stat sysctl tar tr uname wc"
mktools() {
  local dir="$1" t src
  shift
  mkdir -p "$dir"
  for t in $BASIC_TOOLS; do
    case " $* " in *" $t "*) continue ;; esac
    src="$(command -v "$t" 2>/dev/null || true)"
    case "$src" in /*) ln -s "$src" "$dir/$t" ;; esac
  done
}
TOOLS="$ROOT/tools"
mktools "$TOOLS"
for forbidden in curl wget cosign; do
  [ ! -e "$TOOLS/$forbidden" ] || {
    echo "internal error: $forbidden leaked into the tool dir"
    exit 1
  }
done

sha() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum <"$1" | awk '{print $1}'
  else
    shasum -a 256 <"$1" | awk '{print $1}'
  fi
}

# mkrel DIR ARCHIVE_FILE: a release tree for TAG with the given archive, a matching checksums.txt
# and a fake sigstore bundle, plus the latest/download copy of the checksums and bundle.
mkrel() {
  local dir="$1" archive="$2"
  mkdir -p "$dir/download/$TAG" "$dir/latest/download"
  cp "$archive" "$dir/download/$TAG/$ARCHIVE"
  printf '%s  %s\n%s  %s\n' "$(sha "$archive")" "$ARCHIVE" "$(printf '%064d' 7)" "other_file.zip" \
    >"$dir/download/$TAG/checksums.txt"
  echo '{"fake":"sigstore bundle"}' >"$dir/download/$TAG/checksums.txt.sigstore.json"
  cp "$dir/download/$TAG/checksums.txt" "$dir/download/$TAG/checksums.txt.sigstore.json" "$dir/latest/download/"
}

mkdir -p "$ROOT/pkg"
printf '#!/bin/sh\necho "ccshelf v%s fake build"\n' "$BARE" >"$ROOT/pkg/ccshelf"
chmod 755 "$ROOT/pkg/ccshelf"
echo "license" >"$ROOT/pkg/LICENSE"
echo "readme" >"$ROOT/pkg/README.md"
tar -czf "$ROOT/good.tar.gz" -C "$ROOT/pkg" ccshelf LICENSE README.md
REL="$ROOT/rel"
mkrel "$REL" "$ROOT/good.tar.gz"
BASE="file://$REL"

HAVE_PY=1
command -v python3 >/dev/null 2>&1 || HAVE_PY=0

# pyarchive OUT KIND: a hostile or malformed archive (python's tarfile can write what tar will not).
pyarchive() {
  python3 -I - "$1" "$2" <<'PY'
import io, sys, tarfile
out, kind = sys.argv[1], sys.argv[2]
BODY = b"#!/bin/sh\necho ccshelf evil\n"
def add(tf, name, data=BODY, mode=0o755):
    ti = tarfile.TarInfo(name); ti.size = len(data); ti.mode = mode
    tf.addfile(ti, io.BytesIO(data))
def link(tf, name, kind, target):
    ti = tarfile.TarInfo(name); ti.type = kind; ti.linkname = target; ti.mode = 0o755
    tf.addfile(ti)
with tarfile.open(out, "w:gz") as tf:
    if kind == "extra":
        add(tf, "ccshelf"); add(tf, "evil")
    elif kind == "dotdot":
        add(tf, "ccshelf"); add(tf, "../evil")
    elif kind == "absolute":
        add(tf, "ccshelf"); add(tf, "/tmp/ccshelf-evil")
    elif kind == "symlink":
        link(tf, "ccshelf", tarfile.SYMTYPE, "/bin/sh")
    elif kind == "hardlink":
        add(tf, "LICENSE"); link(tf, "ccshelf", tarfile.LNKTYPE, "LICENSE")
    elif kind == "dir":
        add(tf, "ccshelf")
        ti = tarfile.TarInfo("sub"); ti.type = tarfile.DIRTYPE; ti.mode = 0o755; tf.addfile(ti)
    elif kind == "dup":
        add(tf, "ccshelf"); add(tf, "ccshelf")
    elif kind == "nobin":
        add(tf, "LICENSE")
    elif kind == "nested":
        add(tf, "x/ccshelf")
    else:
        sys.exit("unknown kind " + kind)
PY
}

# ---- runner -----------------------------------------------------------------
PASS=0
FAIL=0
SKIPPED=0
OUT=""
RC=0
N=0
LAST=""
LAST_BIN=""
STUBS=""    # extra PATH entries (colon separated, trailing colon) placed before the tool dir
TOOLDIR=""  # an alternative tool dir
ON_PATH=0   # 1: the run's bin dir is on PATH

pass() {
  echo "ok   $1"
  PASS=$((PASS + 1))
}
fail() {
  echo "FAIL $1: $2"
  printf '%s\n' "$OUT" | sed 's/^/     | /'
  FAIL=$((FAIL + 1))
  if [ -n "${CCSHELF_TEST_FAILFAST:-}" ]; then exit 1; fi
}
skip() {
  echo "skip $1: $2"
  SKIPPED=$((SKIPPED + 1))
}

# inst [VAR=value ...] -- [installer args ...]: runs the installer in a clean environment with a
# fresh HOME and TMPDIR; defaults --base-url and --bin-dir (later options win). Sets OUT and RC.
inst() {
  N=$((N + 1))
  local tmp="$ROOT/run$N" envs=("X=1") p
  mkdir -p "$tmp/home" "$tmp/tmpdir"
  chmod 755 "$tmp"
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do
    envs+=("$1")
    shift
  done
  [ $# -gt 0 ] && shift
  LAST="$tmp"
  LAST_BIN="$tmp/bin"
  p="${STUBS}${TOOLDIR:-$TOOLS}"
  if [ "$ON_PATH" -eq 1 ]; then p="$tmp/bin:$p"; fi
  OUT="$(env -i HOME="$tmp/home" TMPDIR="$tmp/tmpdir" PATH="$p" "${envs[@]}" \
    "$TEST_SH" "$INSTALL" --base-url "$BASE" --bin-dir "$LAST_BIN" "$@" 2>&1 </dev/null)"
  RC=$?
  # Every run, successful or not, must leave no temp directory and no staged file behind.
  local leaks
  leaks="$(find "$tmp/tmpdir" -mindepth 1 2>/dev/null | head -n 3)"
  if [ -n "$leaks" ]; then
    fail "run $N cleans up its temp directory" "left behind: $leaks"
  fi
  leaks="$(find "$tmp/bin" -name '.ccshelf.new.*' 2>/dev/null | head -n 3)"
  if [ -n "$leaks" ]; then
    fail "run $N leaves no staged file" "left behind: $leaks"
  fi
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
expect_not_installed() { # name
  if [ -e "$LAST_BIN/ccshelf" ]; then
    fail "$1" "a binary was installed at $LAST_BIN/ccshelf"
  else
    pass "$1"
  fi
}
expect_installed() { # name
  if [ -x "$LAST_BIN/ccshelf" ] && [ "$("$LAST_BIN/ccshelf" version)" = "ccshelf v$BARE fake build" ]; then
    pass "$1"
  else
    fail "$1" "no working binary at $LAST_BIN/ccshelf"
  fi
}
out_has() { # name needle
  if printf '%s' "$OUT" | grep -qF -- "$2"; then pass "$1"; else fail "$1" "output lacks '$2'"; fi
}
out_lacks() { # name needle
  if printf '%s' "$OUT" | grep -qF -- "$2"; then fail "$1" "output has '$2'"; else pass "$1"; fi
}

# ---- fake cosign, curl, wget, uname -----------------------------------------
COSIGN_LOG="$ROOT/cosign.log"
mk_stub_dir() { mkdir -p "$ROOT/stubs-$1"; }
mk_stub_dir cosign-ok
cat >"$ROOT/stubs-cosign-ok/cosign" <<'EOF'
#!/bin/sh
{
  for a in "$@"; do printf 'ARG %s\n' "$a"; done
  last=""
  for a in "$@"; do last="$a"; done
  printf 'DIRMODE %s\n' "$(ls -ld "$(dirname "$last")" | cut -c1-10)"
} >"$COSIGN_LOG"
exit 0
EOF
mk_stub_dir cosign-bad
printf '#!/bin/sh\necho "fake cosign: signature invalid" >&2\nexit 1\n' >"$ROOT/stubs-cosign-bad/cosign"
mk_stub_dir curl
cat >"$ROOT/stubs-curl/curl" <<'EOF'
#!/bin/sh
for a in "$@"; do printf '%s\n' "$a" >>"$CURL_LOG"; done
out=""
url=""
while [ $# -gt 0 ]; do
  case "$1" in
    --output) out="$2"; shift 2 ;;
    --) url="$2"; shift 2 ;;
    *) shift ;;
  esac
done
cp "$CURL_ROOT${url#https://mirror.example.test/rel}" "$out"
EOF
mk_stub_dir wget
cat >"$ROOT/stubs-wget/wget" <<'EOF'
#!/bin/sh
for a in "$@"; do printf '%s\n' "$a" >>"$WGET_LOG"; done
out=""
url=""
while [ $# -gt 0 ]; do
  case "$1" in
    --output-document=*) out="${1#--output-document=}"; shift ;;
    --) url="$2"; shift 2 ;;
    *) shift ;;
  esac
done
cp "$CURL_ROOT${url#https://mirror.example.test/rel}" "$out"
EOF
mk_stub_dir uname
cat >"$ROOT/stubs-uname/uname" <<EOF
#!/bin/sh
case "\$1" in
  -s) [ -n "\${FAKE_UNAME_S:-}" ] && { echo "\$FAKE_UNAME_S"; exit 0; } ;;
  -m) [ -n "\${FAKE_UNAME_M:-}" ] && { echo "\$FAKE_UNAME_M"; exit 0; } ;;
esac
exec "$(command -v uname)" "\$@"
EOF
mk_stub_dir mktemp
cat >"$ROOT/stubs-mktemp/mktemp" <<'EOF'
#!/bin/sh
d="$TMPDIR/ccshelf-install.stub$$"
mkdir -m 755 "$d" && echo "$d"
EOF
chmod +x "$ROOT"/stubs-*/*

# ---- happy path ---------------------------------------------------------------
inst -- --version "$TAG"
expect_ok "pinned version installs" "installed $LAST_BIN/ccshelf"
expect_installed "the installed binary runs"
out_has "prints the version after installing" "ccshelf v$BARE fake build"
out_has "prints the plan: version source" "from:      $BASE"
out_has "prints the plan: directory" "into:      $LAST_BIN"
out_has "hints at PATH when the directory is not on it" "export PATH=\"$LAST_BIN:"
out_has "warns when cosign is missing (checksum-only)" "cosign is not installed"
case "$(ls -l "$LAST_BIN/ccshelf" | cut -c1-10)" in
  -rwxr-xr-x) pass "installed with mode 755 even under umask 077" ;;
  *) fail "installed with mode 755 even under umask 077" "$(ls -l "$LAST_BIN/ccshelf")" ;;
esac
case "$(ls -ld "$LAST_BIN" | cut -c1-10)" in
  drwxr-xr-x) pass "a created bin dir is 0755" ;;
  *) fail "a created bin dir is 0755" "$(ls -ld "$LAST_BIN")" ;;
esac
[ "$(find "$LAST_BIN" -mindepth 1 | wc -l | tr -d ' ')" = "1" ] && pass "only the binary is installed (no LICENSE, no README)" ||
  fail "only the binary is installed (no LICENSE, no README)" "$(ls -A "$LAST_BIN")"

ON_PATH=1
inst -- --version "$TAG"
ON_PATH=0
expect_ok "install with the bin dir on PATH"
out_lacks "no PATH hint when the directory is on PATH" "is not on your PATH"

inst -- --version "$TAG" --quiet
expect_ok "quiet install"
out_lacks "quiet hides the plan" "installing ccshelf"
out_has "quiet still shows warnings" "cosign is not installed"

# default bin dir is $HOME/.local/bin
inst -- --version "$TAG" --bin-dir "$ROOT/run$((N + 1))/home/.local/bin"
expect_ok "an explicit bin dir under HOME works"

N0=$N
mkdir -p "$ROOT/run$((N + 1))/home"
OUT="$(env -i HOME="$ROOT/home-default" TMPDIR="$ROOT" PATH="$TOOLS" "$TEST_SH" "$INSTALL" --base-url "$BASE" --version "$TAG" --quiet 2>&1 </dev/null)"
RC=$?
if [ "$RC" -eq 0 ] && [ -x "$ROOT/home-default/.local/bin/ccshelf" ]; then
  pass "the default bin dir is \$HOME/.local/bin (created if missing)"
else
  fail "the default bin dir is \$HOME/.local/bin (created if missing)" "rc=$RC"
fi
N=$N0

# idempotent
FIXED="$ROOT/fixed-bin"
inst -- --version "$TAG" --bin-dir "$FIXED"
expect_ok "first install into a fixed dir"
inst -- --version "$TAG" --bin-dir "$FIXED"
expect_ok "reinstalling the same version is fine" "replacing the installed ccshelf v$BARE"

# help and option errors
inst -- --help
expect_ok "--help" "Usage: install.sh"
out_lacks "--help does not offer a way to skip the checksum" "no-verify"
inst -- --bogus
expect_fail "unknown option" "unknown option '--bogus'"
inst -- --version
expect_fail "option without a value" "needs a value"

# ---- latest resolution through checksums.txt -----------------------------------
inst --
expect_ok "latest resolves through checksums.txt" "latest release is $TAG"
expect_installed "latest installs the resolved release"

# The archive is fetched from the pinned tag, not from latest/: remove it from latest.
[ ! -e "$REL/latest/download/$ARCHIVE" ] && pass "fixture: latest/download holds no archive (it must be re-pinned to the tag)" ||
  fail "fixture" "latest holds an archive"

NOARCH="$ROOT/rel-latest-none"
mkdir -p "$NOARCH/latest/download"
printf '%s  ccshelf_%s_plan9_amd64.tar.gz\n' "$(printf '%064d' 3)" "$BARE" >"$NOARCH/latest/download/checksums.txt"
BASE_SAVE="$BASE"
BASE="file://$NOARCH"
inst --
expect_fail "latest with no archive for this platform" "lists no ccshelf archive for ${OS}/${ARCH}"
TWO="$ROOT/rel-latest-two"
mkdir -p "$TWO/latest/download"
printf '%s  ccshelf_1.0.0_%s_%s.tar.gz\n%s  ccshelf_1.0.1_%s_%s.tar.gz\n' "$(printf '%064d' 3)" "$OS" "$ARCH" "$(printf '%064d' 4)" "$OS" "$ARCH" \
  >"$TWO/latest/download/checksums.txt"
BASE="file://$TWO"
inst --
expect_fail "latest with two archives for this platform" "more than one ccshelf archive"
BADNAME="$ROOT/rel-latest-bad"
mkdir -p "$BADNAME/latest/download"
printf '%s  ccshelf_x;id_%s_%s.tar.gz\n' "$(printf '%064d' 3)" "$OS" "$ARCH" >"$BADNAME/latest/download/checksums.txt"
BASE="file://$BADNAME"
inst --
expect_fail "latest with a malicious archive name" "unexpected archive name"
BASE="$BASE_SAVE"

# ---- checksum failures ---------------------------------------------------------
TAMPER="$ROOT/rel-tamper"
mkdir -p "$ROOT/pkg-evil"
printf '#!/bin/sh\necho evil\n' >"$ROOT/pkg-evil/ccshelf"
chmod 755 "$ROOT/pkg-evil/ccshelf"
tar -czf "$ROOT/evil.tar.gz" -C "$ROOT/pkg-evil" ccshelf
mkrel "$TAMPER" "$ROOT/good.tar.gz"
cp "$ROOT/evil.tar.gz" "$TAMPER/download/$TAG/$ARCHIVE" # checksums.txt still lists the good archive
BASE="file://$TAMPER"
inst -- --version "$TAG"
expect_fail "a tampered archive fails the checksum" "SHA-256 mismatch"
expect_not_installed "a tampered archive installs nothing"
BASE="$BASE_SAVE"

DUPREL="$ROOT/rel-dup"
mkrel "$DUPREL" "$ROOT/good.tar.gz"
printf '%s  %s\n%s  %s\n' "$(sha "$ROOT/good.tar.gz")" "$ARCHIVE" "$(sha "$ROOT/good.tar.gz")" "$ARCHIVE" >"$DUPREL/download/$TAG/checksums.txt"
BASE="file://$DUPREL"
inst -- --version "$TAG"
expect_fail "a duplicate checksum line fails" "exactly one line"
expect_not_installed "a duplicate checksum line installs nothing"
printf '%s  %s\n%s  %s\n' "$(sha "$ROOT/good.tar.gz")" "$ARCHIVE" "$(printf '%064d' 9)" "$ARCHIVE" >"$DUPREL/download/$TAG/checksums.txt"
inst -- --version "$TAG"
expect_fail "conflicting checksum lines fail" "exactly one line"
printf '%s  %s\n' "$(sha "$ROOT/good.tar.gz")" "something_else.tar.gz" >"$DUPREL/download/$TAG/checksums.txt"
inst -- --version "$TAG"
expect_fail "a missing checksum line fails" "exactly one line"
printf '%s  %s\n' "nothex" "$ARCHIVE" >"$DUPREL/download/$TAG/checksums.txt"
inst -- --version "$TAG"
expect_fail "a malformed checksums.txt fails" "malformed"
printf '%s  %s\ngarbage line here now\n' "$(sha "$ROOT/good.tar.gz")" "$ARCHIVE" >"$DUPREL/download/$TAG/checksums.txt"
inst -- --version "$TAG"
expect_fail "a malformed extra line fails closed" "malformed"
printf '%s *%s\n' "$(sha "$ROOT/good.tar.gz")" "$ARCHIVE" >"$DUPREL/download/$TAG/checksums.txt"
inst -- --version "$TAG"
expect_ok "binary-mode '*name' checksum lines are accepted"
UPPER="$(sha "$ROOT/good.tar.gz" | tr 'a-f' 'A-F')"
printf '%s  %s\n' "$UPPER" "$ARCHIVE" >"$DUPREL/download/$TAG/checksums.txt"
inst -- --version "$TAG"
expect_ok "an upper-case digest is accepted"
: >"$DUPREL/download/$TAG/checksums.txt"
inst -- --version "$TAG"
expect_fail "an empty checksums.txt fails" "empty"
rm -f "$DUPREL/download/$TAG/checksums.txt"
inst -- --version "$TAG"
expect_fail "a missing checksums.txt fails" "cannot read"
BASE="$BASE_SAVE"

inst -- --version "v9.9.9"
expect_fail "an unknown release fails" "cannot read"
expect_not_installed "an unknown release installs nothing"

# ---- hostile archives (checksum matches: only the listing check can stop them) ------
if [ "$HAVE_PY" -eq 1 ]; then
  for kind in extra dotdot absolute symlink hardlink dir dup nobin nested; do
    pyarchive "$ROOT/bad-$kind.tar.gz" "$kind"
    mkrel "$ROOT/rel-$kind" "$ROOT/bad-$kind.tar.gz"
    BASE="file://$ROOT/rel-$kind"
    inst -- --version "$TAG"
    if [ "$RC" -ne 0 ] && [ ! -e "$LAST_BIN/ccshelf" ] && printf '%s' "$OUT" | grep -q "refusing to extract\|does not contain ccshelf"; then
      pass "archive with '$kind' entries is rejected and nothing is installed"
    else
      fail "archive with '$kind' entries is rejected and nothing is installed" "rc=$RC"
    fi
  done
  BASE="$BASE_SAVE"
  if [ -e /tmp/ccshelf-evil ]; then fail "an absolute path entry is never written" "/tmp/ccshelf-evil exists"; else pass "an absolute path entry is never written"; fi
else
  skip "hostile archives" "python3 is not installed"
fi

# ---- input validation ------------------------------------------------------------
for v in "1.2.3" "v1.2" "latest" "main" 'v1.2.3;rm -rf /' 'v1.2.3$(id)' "v1.2.3/../x" "v1.2.3-" "../v1.2.3" "v1.2.3-rc..1" "V1.2.3"; do
  inst -- --version "$v"
  expect_fail "invalid version '$v'" "version"
done
inst -- --version "$TAG"$'\n'"evil"
expect_fail "newline in --version" "control"
inst -- --version "$TAG" --base-url "http://example.test/releases"
expect_fail "http base-url is refused" "plain http is refused"
inst -- --version "$TAG" --base-url "ftp://example.test/releases"
expect_fail "ftp base-url is refused" "must start with https://"
inst -- --version "$TAG" --base-url "file://relative/dir"
expect_fail "file:// without a third slash is refused" "must start with https://"
inst -- --version "$TAG" --base-url "file://$REL/../rel"
expect_fail "'..' in base-url is refused" "'..'"
inst -- --version "$TAG" --base-url 'https://example.test/a$b'
expect_fail "odd characters in base-url are refused" "may only contain"
inst -- --version "$TAG" --base-url "$BASE"$'\n'"x"
expect_fail "newline in base-url is refused" "control"
inst -- --version "$TAG" --bin-dir "relative/bin"
expect_fail "relative bin dir" "absolute path"
inst -- --version "$TAG" --bin-dir "$ROOT/a/../b"
expect_fail "'..' in bin dir" "'.' or '..'"
inst -- --version "$TAG" --bin-dir "$ROOT/bin"$'\n'"x"
expect_fail "newline in bin dir" "control"
inst -- --version "$TAG" --bin-dir "$ROOT/bin"$'\t'"x"
expect_fail "tab in bin dir" "control"
inst -- --version "$TAG" --bin-dir "$ROOT/bin-é"
expect_fail "non-ASCII in bin dir" "non-ASCII"
inst -- --version "$TAG" --cosign-issuer "http://issuer.test"
expect_fail "http cosign issuer" "https:// URL"
inst -- --version "$TAG" --cosign-identity 'id with space'
expect_fail "whitespace in cosign identity" "--cosign-identity may only contain"

# ---- target directory safety -------------------------------------------------------
mkdir -p "$ROOT/real-dir"
ln -s "$ROOT/real-dir" "$ROOT/link-dir"
inst -- --version "$TAG" --bin-dir "$ROOT/link-dir"
expect_fail "a symlinked bin dir is refused" "is a symlink"
[ ! -e "$ROOT/real-dir/ccshelf" ] && pass "nothing was written through the symlink" || fail "nothing was written through the symlink" "found"

mkdir -p "$ROOT/open-dir"
chmod 777 "$ROOT/open-dir"
inst -- --version "$TAG" --bin-dir "$ROOT/open-dir"
expect_fail "an other-writable bin dir is refused" "writable by other users"
[ ! -e "$ROOT/open-dir/ccshelf" ] && pass "nothing was written to the other-writable dir" || fail "nothing was written to the other-writable dir" "found"

mkdir -p "$ROOT/file-as-dir"
: >"$ROOT/file-as-dir/f"
inst -- --version "$TAG" --bin-dir "$ROOT/file-as-dir/f"
expect_fail "a bin dir that is a file is refused" "is not a directory"

if [ "$NOW" != "0" ]; then
  inst -- --version "$TAG" --bin-dir /usr/bin
  expect_fail "a foreign-owned bin dir is refused" "not owned by you"
else
  skip "foreign-owned bin dir" "running as root"
fi

# an existing file named ccshelf
EXIST="$ROOT/exist-bin"
mkdir -p "$EXIST"
printf '#!/bin/sh\necho "not it"\n' >"$EXIST/ccshelf"
chmod 755 "$EXIST/ccshelf"
inst -- --version "$TAG" --bin-dir "$EXIST"
expect_fail "an existing non-ccshelf file is refused" "does not look like ccshelf"
[ "$("$EXIST/ccshelf")" = "not it" ] && pass "the existing file is untouched" || fail "the existing file is untouched" "changed"
inst -- --version "$TAG" --bin-dir "$EXIST" --force
expect_ok "--force replaces a non-ccshelf file" "--force: replacing"
[ "$("$EXIST/ccshelf" version)" = "ccshelf v$BARE fake build" ] && pass "--force installed the new binary" || fail "--force installed the new binary" "not replaced"

SYM="$ROOT/sym-bin"
mkdir -p "$SYM"
ln -s "$ROOT/pkg/ccshelf" "$SYM/ccshelf"
inst -- --version "$TAG" --bin-dir "$SYM"
expect_fail "an existing symlink named ccshelf is refused" "is a symlink"
inst -- --version "$TAG" --bin-dir "$SYM" --force
expect_ok "--force replaces the symlink itself"
[ -L "$SYM/ccshelf" ] && fail "--force replaced the link, not its target" "still a symlink" || pass "--force replaced the link, not its target"
[ -x "$ROOT/pkg/ccshelf" ] && grep -q 'fake build' "$ROOT/pkg/ccshelf" && pass "the link's target is untouched" || fail "the link's target is untouched" "changed"

# ---- dry run ------------------------------------------------------------------------------
inst -- --version "$TAG" --dry-run
expect_ok "--dry-run" "nothing was installed"
[ ! -e "$LAST_BIN" ] && pass "--dry-run creates no directory and no binary" || fail "--dry-run creates no directory and no binary" "$(ls -A "$LAST_BIN")"
BASE="file://$TAMPER"
inst -- --version "$TAG" --dry-run
expect_fail "--dry-run still verifies the checksum" "SHA-256 mismatch"
BASE="$BASE_SAVE"

# ---- signatures -----------------------------------------------------------------------------------
STUBS="$ROOT/stubs-cosign-ok:"
inst COSIGN_LOG="$COSIGN_LOG" -- --version "$TAG"
expect_ok "cosign present and good" "signature verified"
out_lacks "no missing-cosign warning when cosign ran" "cosign is not installed"
EXACT="https://github.com/yorch/ccshelf/.github/workflows/release.yml@refs/tags/$TAG"
if grep -qx 'ARG verify-blob' "$COSIGN_LOG" && grep -qx 'ARG --bundle' "$COSIGN_LOG" &&
  grep -qx "ARG $EXACT" "$COSIGN_LOG" && grep -qx 'ARG https://token.actions.githubusercontent.com' "$COSIGN_LOG" &&
  grep -q '^ARG .*/checksums.txt.sigstore.json$' "$COSIGN_LOG" && tail -n 2 "$COSIGN_LOG" | head -n 1 | grep -q '^ARG .*/checksums.txt$'; then
  pass "cosign gets the bundle, the exact release identity, the issuer and checksums.txt"
else
  OUT="$(cat "$COSIGN_LOG")"
  fail "cosign gets the bundle, the exact release identity, the issuer and checksums.txt" "unexpected arguments"
fi
inst COSIGN_LOG="$COSIGN_LOG" -- --version "$TAG" --cosign-identity "https://ghe.example.test/org/ccshelf/.github/workflows/release.yml@refs/tags/$TAG" --cosign-issuer "https://ghe.example.test/_services/token"
if grep -qx 'ARG https://ghe.example.test/org/ccshelf/.github/workflows/release.yml@refs/tags/v0.0.0-test' "$COSIGN_LOG" && grep -qx 'ARG https://ghe.example.test/_services/token' "$COSIGN_LOG"; then
  pass "--cosign-identity and --cosign-issuer override the defaults"
else
  fail "--cosign-identity and --cosign-issuer override the defaults" "$(cat "$COSIGN_LOG")"
fi
inst COSIGN_LOG="$COSIGN_LOG" -- --version "$TAG" --require-signature
expect_ok "--require-signature with cosign present"
inst COSIGN_LOG="$COSIGN_LOG" -- --version "$TAG" --dry-run
expect_ok "--dry-run with cosign verifies the signature too" "signature verified"
inst COSIGN_LOG="$COSIGN_LOG" --
expect_ok "latest + cosign verifies the pinned tag's identity" "signature verified"
grep -qx "ARG $EXACT" "$COSIGN_LOG" && pass "latest: the identity names the resolved tag" || fail "latest: the identity names the resolved tag" "$(cat "$COSIGN_LOG")"

# A missing bundle with cosign installed is an error, not a downgrade.
NOBUNDLE="$ROOT/rel-nobundle"
mkrel "$NOBUNDLE" "$ROOT/good.tar.gz"
rm -f "$NOBUNDLE/download/$TAG/checksums.txt.sigstore.json"
BASE="file://$NOBUNDLE"
inst COSIGN_LOG="$COSIGN_LOG" -- --version "$TAG"
expect_fail "cosign present but no bundle published fails closed" "cannot read"
expect_not_installed "no bundle installs nothing"
BASE="$BASE_SAVE"

STUBS="$ROOT/stubs-cosign-bad:"
inst -- --version "$TAG"
expect_fail "cosign present and bad refuses to install" "cosign could not verify"
expect_not_installed "a bad signature installs nothing"
inst -- --version "$TAG" --dry-run
expect_fail "a bad signature fails a dry run too" "cosign could not verify"

# The temp dir is private while cosign runs, even if mktemp hands out a 0755 directory.
STUBS="$ROOT/stubs-mktemp:$ROOT/stubs-cosign-ok:"
inst COSIGN_LOG="$COSIGN_LOG" -- --version "$TAG"
if grep -qx 'DIRMODE drwx------' "$COSIGN_LOG"; then
  pass "the download directory is mode 0700"
else
  fail "the download directory is mode 0700" "$(grep DIRMODE "$COSIGN_LOG")"
fi
STUBS=""

inst -- --version "$TAG" --require-signature
expect_fail "--require-signature without cosign is an error" "cosign is not on PATH"
expect_not_installed "--require-signature without cosign installs nothing"
inst -- --version "$TAG" --require-signature --dry-run
expect_fail "--require-signature without cosign fails a dry run too" "cosign is not on PATH"

# ---- the https code path (fake curl and wget) ---------------------------------------------------------
CURL_LOG="$ROOT/curl.log"
: >"$CURL_LOG"
STUBS="$ROOT/stubs-curl:"
TOOLDIR="$ROOT/tools-nowget"
mktools "$TOOLDIR"
inst CURL_LOG="$CURL_LOG" CURL_ROOT="$REL" -- --version "$TAG" --base-url "https://mirror.example.test/rel"
expect_ok "an https base-url downloads through curl"
expect_installed "the curl-downloaded release installs"
missing=""
for needle in --proto =https --proto-redir --max-filesize --fail --location; do
  grep -qx -- "$needle" "$CURL_LOG" || missing="$missing $needle"
done
[ -z "$missing" ] && pass "curl runs with --proto =https, --proto-redir, --max-filesize, --fail and --location" ||
  fail "curl runs with --proto =https, --proto-redir, --max-filesize, --fail and --location" "missing:$missing"
grep -qx 'https://mirror.example.test/rel/download/v0.0.0-test/checksums.txt' "$CURL_LOG" && pass "curl fetches checksums.txt from the tag's download path" ||
  fail "curl fetches checksums.txt from the tag's download path" "$(cat "$CURL_LOG")"
: >"$CURL_LOG"
inst CURL_LOG="$CURL_LOG" CURL_ROOT="$REL" -- --base-url "https://mirror.example.test/rel/"
expect_ok "latest over https" "latest release is $TAG"
if grep -qx 'https://mirror.example.test/rel/latest/download/checksums.txt' "$CURL_LOG" && ! grep -qi 'api' "$CURL_LOG"; then
  pass "latest is resolved from latest/download/checksums.txt, not the API"
else
  fail "latest is resolved from latest/download/checksums.txt, not the API" "$(cat "$CURL_LOG")"
fi
STUBS="$ROOT/stubs-wget:"
WGET_LOG="$ROOT/wget.log"
: >"$WGET_LOG"
inst WGET_LOG="$WGET_LOG" CURL_ROOT="$REL" -- --version "$TAG" --base-url "https://mirror.example.test/rel"
expect_ok "without curl, wget is used"
if grep -qx -- '--https-only' "$WGET_LOG"; then pass "wget runs with --https-only"; else fail "wget runs with --https-only" "$(cat "$WGET_LOG")"; fi
STUBS=""
inst -- --version "$TAG" --base-url "https://mirror.example.test/rel"
expect_fail "no curl and no wget" "neither curl nor wget"
TOOLDIR=""

# ---- platform detection ---------------------------------------------------------------------------
STUBS="$ROOT/stubs-uname:"
for s in FreeBSD SunOS Plan9 OpenBSD; do
  inst FAKE_UNAME_S="$s" -- --version "$TAG"
  expect_fail "unsupported OS $s" "unsupported operating system"
done
inst FAKE_UNAME_S="MINGW64_NT-10.0" -- --version "$TAG"
expect_fail "Git Bash points to install.ps1" "install.ps1"
for m in i686 riscv64 armv7l ppc64le s390x; do
  inst FAKE_UNAME_M="$m" -- --version "$TAG"
  expect_fail "unsupported architecture $m" "unsupported CPU architecture"
done
inst FAKE_UNAME_M="x86_64" -- --version "$TAG" --dry-run
if [ "$ARCH" = amd64 ]; then expect_ok "x86_64 maps to amd64"; else expect_fail "x86_64 maps to amd64" "exactly one line"; fi
inst FAKE_UNAME_M="aarch64" -- --version "$TAG" --dry-run
if [ "$ARCH" = arm64 ]; then expect_ok "aarch64 maps to arm64"; else expect_fail "aarch64 maps to arm64" "exactly one line"; fi
STUBS=""

# ---- SHA-256 tool fallbacks -------------------------------------------------------------------------
for keep in sha256sum shasum openssl; do
  if [ -z "$(command -v "$keep" 2>/dev/null)" ]; then
    skip "only $keep available" "not installed"
    continue
  fi
  TOOLDIR="$ROOT/tools-only-$keep"
  excl=""
  for t in sha256sum shasum openssl; do [ "$t" = "$keep" ] || excl="$excl $t"; done
  # shellcheck disable=SC2086
  mktools "$TOOLDIR" $excl
  inst -- --version "$TAG"
  expect_ok "SHA-256 with only $keep"
  BASE="file://$TAMPER"
  inst -- --version "$TAG"
  expect_fail "tampered archive detected with only $keep" "SHA-256 mismatch"
  BASE="$BASE_SAVE"
done
TOOLDIR="$ROOT/tools-nosha"
mktools "$TOOLDIR" sha256sum shasum openssl
inst -- --version "$TAG"
expect_fail "no SHA-256 tool at all" "no SHA-256 tool"
TOOLDIR=""

# ---- concurrent runs --------------------------------------------------------------------------------
CONC="$ROOT/conc-bin"
for i in 1 2 3 4 5 6; do
  mkdir -p "$ROOT/conc$i/home" "$ROOT/conc$i/tmp"
  (env -i HOME="$ROOT/conc$i/home" TMPDIR="$ROOT/conc$i/tmp" PATH="$TOOLS" "$TEST_SH" "$INSTALL" --base-url "$BASE" --bin-dir "$CONC" --version "$TAG" --quiet >"$ROOT/conc$i/out" 2>&1 </dev/null; echo $? >"$ROOT/conc$i/rc") &
done
wait
bad=""
for i in 1 2 3 4 5 6; do [ "$(cat "$ROOT/conc$i/rc")" = 0 ] || bad="$bad $i"; done
if [ -z "$bad" ] && [ "$("$CONC/ccshelf" version)" = "ccshelf v$BARE fake build" ] && [ "$(ls -A "$CONC")" = "ccshelf" ]; then
  pass "six concurrent installs into one directory all succeed and leave one good binary"
else
  OUT="failed runs:$bad; dir: $(ls -A "$CONC" 2>&1)"
  fail "six concurrent installs into one directory all succeed and leave one good binary" "see above"
fi

echo
echo "passed: $PASS  failed: $FAIL  skipped: $SKIPPED"
[ "$FAIL" -eq 0 ]
