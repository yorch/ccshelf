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
# Releases are local trees reached through file:/// (and, for the https code path, a fake curl
# that maps one https URL onto the tree). Runs use a private PATH made of symlinks to the
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
  JOBS="${CCSHELF_MUTANT_JOBS:-6}"
  total=0
  pids=""
  running=0
  flush() { # wait for the running mutants (results are printed in order at the end)
    local pid
    for pid in $pids; do wait "$pid"; done
    pids=""
    running=0
  }
  mutate() { # name old new   (old must occur exactly once in the installer)
    total=$((total + 1))
    local id=$total copy="$ROOT/mutant-$total.sh"
    if ! python3 -I - "$INSTALL" "$copy" "$2" "$3" <<'PY'; then
import sys
src, dst, old, new = sys.argv[1:5]
text = open(src).read()
if text.count(old) != 1:
    sys.exit("mutation anchor must occur exactly once (%d): %r" % (text.count(old), old))
open(dst, "w").write(text.replace(old, new))
PY
      echo "MUTANT SETUP FAILED: $1" >"$ROOT/mutant-$id.result"
      return
    fi
    chmod +x "$copy"
    mkdir -p "$ROOT/mutant-tmp-$id"
    (
      # A mutant can make the installer hang (for example by reading a FIFO): the suite then gets
      # killed after 10 minutes, which counts as killed.
      if python3 -I - "$copy" "$0" "$ROOT/mutant-$id.log" "$ROOT/mutant-tmp-$id" <<'PY'
import os, signal, subprocess, sys
copy, me, log, tmp = sys.argv[1:5]
env = dict(os.environ, CCSHELF_TEST_FAILFAST="1", CCSHELF_INSTALL_SCRIPT=copy, TMPDIR=tmp)
with open(log, "w") as f:
    p = subprocess.Popen(["bash", me], stdout=f, stderr=subprocess.STDOUT, env=env, start_new_session=True)
    try:
        rc = p.wait(timeout=600)
    except subprocess.TimeoutExpired:
        os.killpg(p.pid, signal.SIGKILL)
        p.wait()
        f.write("\nMUTANT TIMED OUT: the suite hung, so the defect was noticed\n")
        rc = 124
sys.exit(0 if rc == 0 else 1)
PY
      then
        echo "SURVIVED: $1 (the suite still passes)" >"$ROOT/mutant-$id.result"
      else
        echo "killed:   $1" >"$ROOT/mutant-$id.result"
      fi
    ) &
    pids="$pids $!"
    running=$((running + 1))
    if [ "$running" -ge "$JOBS" ]; then flush; fi
  }
  # ---- verification of the download
  mutate "no SHA-256 comparison" 'if [ "$ACTUAL" != "$EXPECTED" ]; then' 'if false; then'
  mutate "no exactly-one checksum line" '[ "$count" = "1" ] ||' '[ "$count" -ge 1 ] ||'
  mutate "checksum line matched by substring" 'if (f==n) c++}' 'if (index(f,n)) c++}'
  mutate "hash not lower-cased" 'if (f==n) print tolower($1)}' 'if (f==n) print $1}'
  mutate "computed digest not checked for hex" "matches '^[0-9a-f]{64}\$' \"\$ACTUAL\" ||" 'true ||'
  mutate "pinned checksums.txt format unchecked" 'check_checksums_format "$TMP/checksums.txt"' 'true'
  mutate "latest checksums.txt format unchecked" 'check_checksums_format "$TMP/latest-checksums.txt"' 'true'
  mutate "latest not re-pinned to its tag" 'REL="$BASE_URL/download/$VERSION"' 'REL="$BASE_URL/latest/download"'
  mutate "latest: more than one archive accepted" 'die "the latest release lists more than one ccshelf archive for ${OS}/${ARCH}" ;;' ': ;;'
  mutate "latest: archive version not validated" "matches '^[0-9]+\\.[0-9]+\\.[0-9]+(-[0-9A-Za-z.-]+)?\$' \"\$bare\" ||" 'true ||'
  mutate "latest: '..' in the archive version accepted" '*..*) die "the latest release has an unexpected archive name: $names" ;;' '*..*) ;;'
  # ---- the https transport
  mutate "no --proto" "--proto '=https' " ''
  mutate "--proto allows http" "--proto '=https' --proto-redir" "--proto '=http,https' --proto-redir"
  mutate "no --proto-redir" "--proto-redir '=https' " ''
  mutate "--proto-redir allows http" "--proto-redir '=https'" "--proto-redir '=http,https'"
  mutate "no --max-filesize" '--max-filesize "$_max" ' ''
  mutate "no --fail" '--fail --location' '--location'
  mutate "curl reads ~/.curlrc (no -q)" "curl -q --proto" 'curl --proto'
  mutate "curl -q not first" "curl -q --proto '=https' --proto-redir '=https'" "curl --proto '=https' -q --proto-redir '=https'"
  mutate "curl keeps the caller's stdin" '--output "$_dest" -- "$_url" </dev/null' '--output "$_dest" -- "$_url"'
  mutate "http base-url accepted" 'http://*) die "--base-url must be https (plain http is refused): $BASE_URL" ;;' 'http://*) ;;'
  mutate "downloaded file not checked for size" '[ "$_size" -le "$_max" ] || die' 'true || die'
  mutate "empty download accepted" '[ "$_size" -gt 0 ] || die "downloaded file is empty: $_url"' 'true'
  # ---- signature
  mutate "cosign failure ignored" 'die "cosign could not verify the signature of checksums.txt for $VERSION; refusing to install"' 'warn "ignored"'
  mutate "--require-signature ignored" 'elif [ "$REQUIRE_SIG" -eq 1 ]; then' 'elif false; then'
  mutate "wrong default cosign identity" 'workflows/release.yml@refs/tags/$VERSION"' 'workflows/release.yml@refs/heads/main"'
  mutate "cosign issuer dropped" '    --certificate-oidc-issuer "$COSIGN_ISSUER" \
' ''
  mutate "cosign keeps the caller's stdin" '"$TMP/checksums.txt" </dev/null ||' '"$TMP/checksums.txt" ||'
  # ---- the archive
  mutate "no archive entry allowlist" "if awk 'BEGIN {bad=0}" "if true || awk 'BEGIN {bad=0}"
  mutate "no duplicate entry check" ' if (seen[$0]++) bad=1' ''
  mutate "no link or directory check" "if awk '{c=substr" "if true || awk '{c=substr"
  mutate "link check ignores the type" 'if (c != "-") bad=1' 'if (0) bad=1'
  mutate "no top-level ccshelf check" "grep -qx 'ccshelf' \"\$TMP/names.txt\" ||" 'true ||'
  mutate "listing counts not compared" '[ "$n_names" = "$n_types" ] ||' 'true ||'
  mutate "entry count not capped" '[ "$n_names" -ge 1 ] && [ "$n_names" -le 3 ] ||' 'true ||'
  mutate "binary size not capped" '[ "$size" -le "$MAX_BINARY_BYTES" ] ||' 'true ||'
  mutate "extraction not cut off (no head -c)" '| head -c "$((MAX_BINARY_BYTES + 1))" >' '| cat >'
  mutate "empty binary accepted" '[ "$size" -gt 0 ] || die "the ccshelf entry' 'true || die "the ccshelf entry'
  # ---- inputs
  mutate "version regex accepts anything" "VERSION_RE='^v[0-9]+\\.[0-9]+\\.[0-9]+(-[0-9A-Za-z.-]+)?\$'" "VERSION_RE='.*'"
  mutate "version regex not anchored at the start" "VERSION_RE='^v[0-9]" "VERSION_RE='v[0-9]"
  mutate "version regex not anchored at the end" "(-[0-9A-Za-z.-]+)?\$'
if [ -n \"\$VERSION\" ]" "(-[0-9A-Za-z.-]+)?'
if [ -n \"\$VERSION\" ]"
  mutate "no non-ASCII/control check" '[ "$_bad" = "0" ] ||' 'true ||'
  mutate "HOME not checked for odd characters" 'printable "HOME" "$BIN_DIR"' 'true'
  mutate "base-url pattern not anchored at the end" "file:///[A-Za-z0-9._~:/@%+=,-]*)\$' \"\$BASE_URL\"" "file:///[A-Za-z0-9._~:/@%+=,-]*)' \"\$BASE_URL\""
  mutate "cosign issuer pattern not anchored" "matches '^https://[A-Za-z0-9._~:/@%+=,-]+\$' \"\$COSIGN_ISSUER\"" "matches '^https://[A-Za-z0-9._~:/@%+=,-]+' \"\$COSIGN_ISSUER\""
  mutate "bin dir '..' components accepted" '*/../* | */.. | */./* | */.)' '*/xx-never/*)'
  # ---- the target directory and the existing file
  mutate "private temp dir not chmod 700" 'chmod 700 "$TMP"' 'true'
  mutate "umask 077 dropped" '
umask 077
' '
umask 000
'
  mutate "symlinked bin dir accepted" '[ ! -L "$BIN_DIR" ] || die "$BIN_DIR is a symlink' 'true || die "$BIN_DIR is a symlink'
  mutate "other-writable bin dir accepted" 'if [ -n "$(find "$BIN_DIR" -prune -perm -0002 2>/dev/null)" ]; then' 'if false; then'
  mutate "foreign-owned bin dir accepted" 'if [ -z "$(find "$BIN_DIR" -prune -user "$(id -u)" 2>/dev/null)" ]; then' 'if false; then'
  mutate "unwritable bin dir not noticed" '[ -w "$BIN_DIR" ] ||' 'true ||'
  mutate "bin dir that is a file accepted" '[ -d "$BIN_DIR" ] || die "$BIN_DIR exists and is not a directory"' 'true'
  mutate "non-ccshelf file replaced" '*) die "$TARGET exists and does not look like ccshelf (use --force to replace it anyway)" ;;' '*) ;;'
  mutate "existing file run without the name check" "grep -aqF 'ccshelf' \"\$TARGET\" 2>/dev/null </dev/null ||" 'true ||'
  mutate "existing file run without a timeout" '"$TIMEOUT_TOOL" 5 "$TARGET" version' '"$TARGET" version'
  mutate "existing file given 500 seconds" '"$TIMEOUT_TOOL" 5 "$TARGET" version' '"$TIMEOUT_TOOL" 500 "$TARGET" version'
  mutate "existing file keeps the caller's stdin" 'version 2>/dev/null </dev/null | head -c 1024' 'version 2>/dev/null | head -c 1024'
  mutate "special file named ccshelf replaced" '[ -f "$TARGET" ] || die "$TARGET exists and is not a regular file' 'true || die "$TARGET exists and is not a regular file'
  mutate "--force replaces a directory" 'if [ -d "$TARGET" ] && [ ! -L "$TARGET" ]; then' 'if false; then'
  mutate "--force keeps a symlink in the way" 'if [ -L "$TARGET" ]; then
  rm -f -- "$TARGET"' 'if false; then
  rm -f -- "$TARGET"'
  mutate "no check of the file at the target" 'if [ ! -f "$TARGET" ] || [ -L "$TARGET" ]; then' 'if false; then'
  mutate "no digest check of the file at the target" '[ "$(sha256_of "$TARGET")" = "$VERIFIED_SUM" ] ||' 'true ||'
  mutate "dry run installs" 'if [ "$DRY_RUN" -eq 1 ]; then
  say "dry run: $ARCHIVE' 'if false; then
  say "dry run: $ARCHIVE'
  mutate "install mode not 755" 'chmod 755 "$STAGE"' 'true'
  mutate "staged file not removed on exit" 'rm -f "$STAGE" 2>/dev/null || true' 'true'
  mutate "version smoke test ignores --quiet" 'if [ "$QUIET" -eq 0 ]; then
  "$TARGET" version' 'if true; then
  "$TARGET" version'
  mutate "version smoke test keeps the caller's stdin" '"$TARGET" version </dev/null || warn' '"$TARGET" version || warn'
  # ---- signals
  mutate "no HUP trap" "trap 'exit 129' HUP" 'true'
  mutate "no INT trap" "trap 'exit 130' INT" 'true'
  mutate "no TERM trap" "trap 'exit 143' TERM" 'true'
  flush
  survivors=0
  failed=0
  n=0
  while [ $n -lt $total ]; do
    n=$((n + 1))
    r="$(cat "$ROOT/mutant-$n.result" 2>/dev/null || echo "MUTANT RESULT MISSING $n")"
    echo "$r"
    case "$r" in
      killed:*) ;;
      *) survivors=$((survivors + 1)) ;;
    esac
  done
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
BASIC_TOOLS="awk basename cat chmod cp cut dirname find grep gzip head id ln ls mkdir mktemp mv openssl printf rm sed sh sha256sum shasum sleep sort stat sysctl tar timeout tr uname wc"
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
for forbidden in curl wget cosign gtimeout; do
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
  {
    printf '%s  %s\n' "$(sha "$archive")" "$ARCHIVE"
    # What a real release lists next to the archive: SBOMs named like the archive plus a suffix
    # (a name match by substring would see two lines), the other platforms and the installers.
    printf '%s  %s.sbom.json\n' "$(printf '%064d' 5)" "$ARCHIVE"
    for o in darwin linux; do for a in amd64 arm64; do
      [ "$o/$a" = "$OS/$ARCH" ] && continue
      printf '%s  ccshelf_%s_%s_%s.tar.gz\n%s  ccshelf_%s_%s_%s.tar.gz.sbom.json\n' \
        "$(printf '%064d' 6)" "$BARE" "$o" "$a" "$(printf '%064d' 8)" "$BARE" "$o" "$a"
    done; done
    for a in amd64 arm64; do
      printf '%s  ccshelf_%s_windows_%s.zip\n%s  ccshelf_%s_windows_%s.zip.sbom.json\n' \
        "$(printf '%064d' 6)" "$BARE" "$a" "$(printf '%064d' 8)" "$BARE" "$a"
    done
    printf '%s  install.sh\n%s  install.ps1\n%s  other_file.zip\n' "$(printf '%064d' 1)" "$(printf '%064d' 2)" "$(printf '%064d' 7)"
  } >"$dir/download/$TAG/checksums.txt"
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
    elif kind == "special":
        ti = tarfile.TarInfo("ccshelf"); ti.type = tarfile.FIFOTYPE; ti.mode = 0o755
        tf.addfile(ti)
    elif kind == "empty":
        pass
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
POISON="$ROOT/poison"
printf 'poison line that no child process may read\n' >"$POISON"
STDIN_FILE=/dev/null # what the installer gets on stdin (a "poison" file proves it never leaks on)
INSTALL_RUN=""       # run another copy of the installer (for example one with small size caps)
NO_BINDIR=0          # 1: do not pass --bin-dir (exercise the HOME default)

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
  local bindir=(--bin-dir "$LAST_BIN")
  [ "$NO_BINDIR" -eq 1 ] && bindir=()
  OUT="$(env -i HOME="$tmp/home" TMPDIR="$tmp/tmpdir" PATH="$p" "${envs[@]}" \
    "$TEST_SH" "${INSTALL_RUN:-$INSTALL}" --base-url "$BASE" "${bindir[@]}" "$@" 2>&1 <"$STDIN_FILE")"
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
  if read -r _line; then echo "STDIN-LEAK"; fi
  for a in "$@"; do printf 'ARG %s\n' "$a"; done
  last=""
  for a in "$@"; do last="$a"; done
  printf 'DIRMODE %s\n' "$(ls -ld "$(dirname "$last")" | cut -c1-10)"
  printf 'FILEMODE %s\n' "$(ls -l "$last" | cut -c1-10)"
} >"$COSIGN_LOG"
exit 0
EOF
mk_stub_dir cosign-bad
printf '#!/bin/sh\necho "fake cosign: signature invalid" >&2\nexit 1\n' >"$ROOT/stubs-cosign-bad/cosign"
mk_stub_dir curl
cat >"$ROOT/stubs-curl/curl" <<'EOF'
#!/bin/sh
{
  echo CALL
  for a in "$@"; do printf '%s\n' "$a"; done
  echo END
  # stdin must be /dev/null: curl is run inside `curl | sh`, where stdin is the script itself.
  if read -r _line; then echo "STDIN-LEAK"; fi
} >>"$CURL_LOG"
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
mk_stub_dir slow-curl
cat >"$ROOT/stubs-slow-curl/curl" <<'EOF'
#!/bin/sh
echo $$ >"$SLOW_PID_FILE"
exec sleep 60
EOF
mk_stub_dir wget
cat >"$ROOT/stubs-wget/wget" <<'EOF'
#!/bin/sh
echo "wget must never be run" >>"$WGET_LOG"
exit 1
EOF
mk_stub_dir timeout
cat >"$ROOT/stubs-timeout/timeout" <<'EOF'
#!/bin/sh
for a in "$@"; do printf '%s\n' "$a"; done >>"$TIMEOUT_LOG"
echo END >>"$TIMEOUT_LOG"
shift
exec "$@"
EOF
mk_stub_dir head
cat >"$ROOT/stubs-head/head" <<EOF
#!/bin/sh
echo "\$*" >>"\$HEAD_LOG"
exec "$(command -v head)" "\$@"
EOF
mk_stub_dir tar
cat >"$ROOT/stubs-tar/tar" <<EOF
#!/bin/sh
"$(command -v tar)" "\$@" || exit \$?
case " \$* " in
  *" -tvzf "*) [ -n "\${FAKE_TAR_EXTRA_LINE:-}" ] && echo "-rw-r--r-- 0 0 0 Jan 1 00:00 extra" ;;
esac
exit 0
EOF
mk_stub_dir badsha
printf '#!/bin/sh\ncat >/dev/null\necho "not-a-digest  -"\n' >"$ROOT/stubs-badsha/sha256sum"
mk_stub_dir mv
cat >"$ROOT/stubs-mv/mv" <<EOF
#!/bin/sh
# FAKE_MV=corrupt: the file at the destination is not what was moved; FAKE_MV=link: it is a symlink.
last=""
prev=""
for a in "\$@"; do prev="\$last"; last="\$a"; done
case "\${FAKE_MV:-}" in
  fail) case "\$last" in */ccshelf) echo "fake mv: cannot move" >&2; exit 1 ;; esac ;;
  corrupt) case "\$last" in */ccshelf) rm -f "\$prev" "\$last"; echo "evil" >"\$last"; chmod 755 "\$last"; exit 0 ;; esac ;;
  link) case "\$last" in */ccshelf) rm -f "\$prev" "\$last"; ln -s /bin/sh "\$last"; exit 0 ;; esac ;;
esac
exec "$(command -v mv)" "\$@"
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
out_lacks "quiet hides the version line" "fake build"

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
BADFMT="$ROOT/rel-latest-badfmt"
mkdir -p "$BADFMT/latest/download"
printf 'nothex  ccshelf_%s_%s_%s.tar.gz\n' "$BARE" "$OS" "$ARCH" >"$BADFMT/latest/download/checksums.txt"
BASE="file://$BADFMT"
inst --
expect_fail "a malformed latest checksums.txt fails closed" "checksums.txt is malformed"
printf '%s  ccshelf_%s_%s_%s.tar.gz\ngarbage line\n' "$(printf '%064d' 3)" "$BARE" "$OS" "$ARCH" >"$BADFMT/latest/download/checksums.txt"
inst --
expect_fail "a malformed extra line in the latest checksums.txt fails closed" "checksums.txt is malformed"
DOTS="$ROOT/rel-latest-dots"
mkdir -p "$DOTS/latest/download"
printf '%s  ccshelf_1.2.3-rc..1_%s_%s.tar.gz\n' "$(printf '%064d' 3)" "$OS" "$ARCH" >"$DOTS/latest/download/checksums.txt"
BASE="file://$DOTS"
inst --
expect_fail "latest naming a version with '..' is refused" "unexpected archive name"
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
  # kind:message. Each kind must be stopped by its own check, so a check that is removed is noticed.
  for spec in "extra:unexpected, duplicate or unsafe entry" "dotdot:unexpected, duplicate or unsafe entry" \
    "absolute:unexpected, duplicate or unsafe entry" "symlink:link, directory or special file" \
    "hardlink:link, directory or special file" "special:link, directory or special file" \
    "dir:unexpected, duplicate or unsafe entry" "dup:unexpected, duplicate or unsafe entry" \
    "nobin:does not contain ccshelf at its top level" "nested:unexpected, duplicate or unsafe entry" \
    "empty:has 0 entries"; do
    kind="${spec%%:*}"
    want="${spec#*:}"
    pyarchive "$ROOT/bad-$kind.tar.gz" "$kind"
    mkrel "$ROOT/rel-$kind" "$ROOT/bad-$kind.tar.gz"
    BASE="file://$ROOT/rel-$kind"
    inst -- --version "$TAG"
    if [ "$RC" -ne 0 ] && [ ! -e "$LAST_BIN/ccshelf" ] && printf '%s' "$OUT" | grep -qF "$want"; then
      pass "archive with '$kind' entries is rejected ($want) and nothing is installed"
    else
      fail "archive with '$kind' entries is rejected ($want) and nothing is installed" "rc=$RC"
    fi
  done
  # An inconsistent listing (more type lines than names) is refused before anything is extracted.
  BASE="$BASE_SAVE"
  STUBS="$ROOT/stubs-tar:"
  inst FAKE_TAR_EXTRA_LINE=1 -- --version "$TAG"
  STUBS=""
  expect_fail "an inconsistent archive listing is refused" "the archive listing is inconsistent"
  expect_not_installed "an inconsistent archive listing installs nothing"
  BASE="$BASE_SAVE"
  if [ -e /tmp/ccshelf-evil ]; then fail "an absolute path entry is never written" "/tmp/ccshelf-evil exists"; else pass "an absolute path entry is never written"; fi
else
  skip "hostile archives" "python3 is not installed"
fi

# ---- input validation ------------------------------------------------------------
for v in "1.2.3" "v1.2" "latest" "main" 'v1.2.3;rm -rf /' 'v1.2.3$(id)' "v1.2.3/../x" "v1.2.3-" "../v1.2.3" "v1.2.3-rc..1" "V1.2.3" "xv1.2.3" "1v1.2.3" "-v1.2.3" "v1.2.3 x" "vv1.2.3" " v1.2.3"; do
  inst -- --version "$v"
  expect_fail "invalid version '$v'" "version"
done
inst -- --version "v1.2.3-rc..1"
expect_fail "'..' inside an otherwise valid version" "version must not contain '..'"
inst -- --version "xv1.2.3"
expect_fail "a version must start with v (anchored regex)" "is not a release tag"
inst -- --version "v1.2.3 x"
expect_fail "a version must end at the patch/pre-release (anchored regex)" "is not a release tag"
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
inst -- --version "$TAG" --cosign-issuer "https://issuer.test/a b"
expect_fail "whitespace in the cosign issuer" "https:// URL"
inst -- --version "$TAG" --bin-dir "$ROOT/a/./b"
expect_fail "'.' component in bin dir" "'.' or '..'"
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


# ---- a directory or link named ccshelf (--force must neither install into it nor through it) ------
DIRT="$ROOT/dirt-bin"
mkdir -p "$DIRT/ccshelf"
echo keep >"$DIRT/ccshelf/keep"
inst -- --version "$TAG" --bin-dir "$DIRT"
expect_fail "a directory named ccshelf is refused" "is a directory"
inst -- --version "$TAG" --bin-dir "$DIRT" --force
expect_fail "--force does not replace a directory named ccshelf" "is a directory"
if [ "$(ls -A "$DIRT/ccshelf")" = "keep" ] && [ -d "$DIRT/ccshelf" ]; then
  pass "the directory named ccshelf is untouched (nothing was moved into it)"
else
  fail "the directory named ccshelf is untouched (nothing was moved into it)" "$(ls -A "$DIRT/ccshelf")"
fi

OUTSIDE="$ROOT/outside-dir"
mkdir -p "$OUTSIDE"
LD="$ROOT/linkdir-bin"
mkdir -p "$LD"
ln -s "$OUTSIDE" "$LD/ccshelf"
inst -- --version "$TAG" --bin-dir "$LD"
expect_fail "a symlink to a directory named ccshelf is refused" "is a symlink"
inst -- --version "$TAG" --bin-dir "$LD" --force
expect_ok "--force replaces a symlink to a directory" "--force: replacing"
if [ ! -L "$LD/ccshelf" ] && [ -f "$LD/ccshelf" ] && [ "$("$LD/ccshelf" version)" = "ccshelf v$BARE fake build" ]; then
  pass "the link itself was replaced by the binary"
else
  fail "the link itself was replaced by the binary" "$(ls -l "$LD")"
fi
if [ -z "$(ls -A "$OUTSIDE")" ]; then pass "nothing was written outside the bin dir through the link"; else fail "nothing was written outside the bin dir through the link" "$(ls -A "$OUTSIDE")"; fi

LF="$ROOT/linkfile-bin"
mkdir -p "$LF"
echo "precious" >"$ROOT/precious"
ln -s "$ROOT/precious" "$LF/ccshelf"
inst -- --version "$TAG" --bin-dir "$LF" --force
expect_ok "--force replaces a symlink to a file"
[ ! -L "$LF/ccshelf" ] && [ "$(cat "$ROOT/precious")" = "precious" ] && pass "the file the link pointed to is untouched" ||
  fail "the file the link pointed to is untouched" "$(ls -l "$LF")"

DL="$ROOT/dangling-bin"
mkdir -p "$DL"
ln -s "$ROOT/does-not-exist" "$DL/ccshelf"
inst -- --version "$TAG" --bin-dir "$DL"
expect_fail "a dangling symlink named ccshelf is refused" "is a symlink"
inst -- --version "$TAG" --bin-dir "$DL" --force
expect_ok "--force replaces a dangling symlink"
[ ! -L "$DL/ccshelf" ] && [ -f "$DL/ccshelf" ] && [ ! -e "$ROOT/does-not-exist" ] && pass "the dangling link was replaced and its target never created" ||
  fail "the dangling link was replaced and its target never created" "$(ls -l "$DL")"

FIFO="$ROOT/fifo-bin"
mkdir -p "$FIFO"
if mkfifo "$FIFO/ccshelf" 2>/dev/null; then
  inst -- --version "$TAG" --bin-dir "$FIFO"
  expect_fail "a special file named ccshelf is refused" "is not a regular file"
else
  skip "a special file named ccshelf" "mkfifo is not available"
fi
if [ "$NOW" != "0" ]; then
  RO="$ROOT/ro-bin"
  mkdir -p "$RO"
  chmod 555 "$RO"
  inst -- --version "$TAG" --bin-dir "$RO"
  expect_fail "a bin dir you cannot write to is refused" "is not writable"
  chmod 755 "$RO"
else
  skip "read-only bin dir" "running as root"
fi

# What ends up at the target is verified after the move: the same bytes, as a regular file.
STUBS="$ROOT/stubs-mv:"
inst FAKE_MV=corrupt -- --version "$TAG"
expect_fail "a file that differs from the verified binary after the move is an error" "does not match the verified binary"
inst FAKE_MV=link -- --version "$TAG"
expect_fail "a symlink at the target after the move is an error" "is not a regular file after the install"
inst FAKE_MV=fail -- --version "$TAG"
expect_fail "a failing move is an error and leaves no staged file" "cannot move the new binary into place"
STUBS=""

# ---- identifying an existing ccshelf: never run an unrelated file, never run one for long ----------------
SENT="$ROOT/ran-sentinel"
IDB="$ROOT/id-bin"
mkdir -p "$IDB"
printf '#!/bin/sh\ntouch "$SENTINEL"\necho "hello"\n' >"$IDB/ccshelf"
chmod 755 "$IDB/ccshelf"
/bin/rm -f "$SENT"
inst SENTINEL="$SENT" -- --version "$TAG" --bin-dir "$IDB"
expect_fail "a file without the name ccshelf inside is refused" "does not look like ccshelf"
[ ! -e "$SENT" ] && pass "an unrelated file is not even executed" || fail "an unrelated file is not even executed" "it ran"
printf '#!/bin/sh\n# ccshelf (fake)\ntouch "$SENTINEL"\necho "hello"\n' >"$IDB/ccshelf"
inst SENTINEL="$SENT" -- --version "$TAG" --bin-dir "$IDB"
if [ -n "$(command -v timeout 2>/dev/null)" ] || [ -n "$(command -v gtimeout 2>/dev/null)" ]; then
  expect_fail "a file that mentions ccshelf but is not it is refused after a bounded run" "does not look like ccshelf"
else
  skip "a bounded run of a look-alike" "no timeout tool on this host"
fi
TIMEOUT_LOG="$ROOT/timeout.log"
: >"$TIMEOUT_LOG"
printf '#!/bin/sh\necho "ccshelf v0.0.1 (old)"\n' >"$IDB/ccshelf"
STUBS="$ROOT/stubs-timeout:"
inst TIMEOUT_LOG="$TIMEOUT_LOG" -- --version "$TAG" --bin-dir "$IDB"
expect_ok "an existing ccshelf is replaced (run through timeout)" "replacing the installed ccshelf v0.0.1"
if [ "$(sed -n 1p "$TIMEOUT_LOG")" = "5" ] && [ "$(sed -n 2p "$TIMEOUT_LOG")" = "$IDB/ccshelf" ] && [ "$(sed -n 3p "$TIMEOUT_LOG")" = "version" ]; then
  pass "the existing binary runs as: timeout 5 <binary> version"
else
  fail "the existing binary runs as: timeout 5 <binary> version" "$(cat "$TIMEOUT_LOG")"
fi
STUBS=""
REALTO="$(command -v timeout 2>/dev/null || command -v gtimeout 2>/dev/null || true)"
if [ -n "$REALTO" ]; then
  printf '#!/bin/sh\n# ccshelf (hangs)\nexec sleep 60\n' >"$IDB/ccshelf"
  chmod 755 "$IDB/ccshelf"
  mkdir -p "$ROOT/tools-realtimeout"
  mktools "$ROOT/tools-realtimeout" timeout
  ln -s "$REALTO" "$ROOT/tools-realtimeout/timeout"
  TOOLDIR="$ROOT/tools-realtimeout"
  t0="$(date +%s)"
  inst -- --version "$TAG" --bin-dir "$IDB"
  t1="$(date +%s)"
  TOOLDIR=""
  expect_fail "a hanging existing binary does not hang the install" "does not look like ccshelf"
  [ $((t1 - t0)) -lt 40 ] && pass "the hanging binary was stopped by the 5-second limit (took $((t1 - t0))s)" || fail "the hanging binary was stopped by the 5-second limit" "took $((t1 - t0))s"
else
  skip "a hanging existing binary" "no timeout tool on this host"
fi
# Without a timeout tool (stock macOS) nothing is run: the name check alone decides.
mkdir -p "$ROOT/tools-notimeout"
mktools "$ROOT/tools-notimeout" timeout
TOOLDIR="$ROOT/tools-notimeout"
printf '#!/bin/sh\n# ccshelf (marker)\ntouch "$SENTINEL"\necho "ccshelf v0.0.1"\n' >"$IDB/ccshelf"
chmod 755 "$IDB/ccshelf"
/bin/rm -f "$SENT"
inst SENTINEL="$SENT" -- --version "$TAG" --bin-dir "$IDB"
expect_ok "without a timeout tool an existing ccshelf is replaced without being run" "not run"
[ ! -e "$SENT" ] && pass "without a timeout tool the existing binary is never executed" || fail "without a timeout tool the existing binary is never executed" "it ran"
printf '#!/bin/sh\ntouch "$SENTINEL"\n' >"$IDB/ccshelf"
inst SENTINEL="$SENT" -- --version "$TAG" --bin-dir "$IDB"
expect_fail "without a timeout tool a file lacking the name is still refused" "does not look like ccshelf"
TOOLDIR=""

# The installer's own smoke test and the identification run get /dev/null on stdin.
PROBE="$ROOT/probe-pkg"
mkdir -p "$PROBE"
printf '#!/bin/sh\nif read -r _l; then echo "ccshelf STDIN-LEAK"; fi\necho "ccshelf v%s fake build"\n' "$BARE" >"$PROBE/ccshelf"
chmod 755 "$PROBE/ccshelf"
tar -czf "$ROOT/probe.tar.gz" -C "$PROBE" ccshelf
mkrel "$ROOT/rel-probe" "$ROOT/probe.tar.gz"
BASE="file://$ROOT/rel-probe"
STDIN_FILE="$POISON"
PB="$ROOT/probe-bin"
inst -- --version "$TAG" --bin-dir "$PB"
inst -- --version "$TAG" --bin-dir "$PB"
STDIN_FILE=/dev/null
out_lacks "the installed binary and the old one are run with /dev/null on stdin" "STDIN-LEAK"
BASE="$BASE_SAVE"

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
# cosign_pair LOG FLAG VALUE: the flag line is directly followed by the value line (pairs, not tokens).
cosign_pair() { awk -v f="ARG $2" -v v="ARG $3" 'prev == f && $0 == v {ok = 1} {prev = $0} END {exit !ok}' "$1"; }
if grep -qx 'ARG verify-blob' "$COSIGN_LOG" && cosign_pair "$COSIGN_LOG" --certificate-identity "$EXACT" &&
  cosign_pair "$COSIGN_LOG" --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' &&
  grep -q '^ARG --bundle$' "$COSIGN_LOG" && grep -A1 '^ARG --bundle$' "$COSIGN_LOG" | tail -n 1 | grep -q '^ARG .*/checksums.txt.sigstore.json$' &&
  grep '^ARG ' "$COSIGN_LOG" | tail -n 1 | grep -q '^ARG .*/checksums.txt$'; then
  pass "cosign gets the bundle, the exact release identity, the issuer and checksums.txt"
else
  OUT="$(cat "$COSIGN_LOG")"
  fail "cosign gets the bundle, the exact release identity, the issuer and checksums.txt" "unexpected arguments"
fi
STDIN_FILE="$POISON"
inst COSIGN_LOG="$COSIGN_LOG" -- --version "$TAG"
STDIN_FILE=/dev/null
grep -qx 'STDIN-LEAK' "$COSIGN_LOG" && fail "cosign gets /dev/null on stdin (file mirror)" "cosign read the installer's stdin" || pass "cosign gets /dev/null on stdin (file mirror)"
inst COSIGN_LOG="$COSIGN_LOG" -- --version "$TAG" --cosign-identity "https://ghe.example.test/org/ccshelf/.github/workflows/release.yml@refs/tags/$TAG" --cosign-issuer "https://ghe.example.test/_services/token"
if cosign_pair "$COSIGN_LOG" --certificate-identity 'https://ghe.example.test/org/ccshelf/.github/workflows/release.yml@refs/tags/v0.0.0-test' &&
  cosign_pair "$COSIGN_LOG" --certificate-oidc-issuer 'https://ghe.example.test/_services/token'; then
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
cosign_pair "$COSIGN_LOG" --certificate-identity "$EXACT" && pass "latest: the identity names the resolved tag" || fail "latest: the identity names the resolved tag" "$(cat "$COSIGN_LOG")"

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
if grep -qx 'FILEMODE -rw-------' "$COSIGN_LOG"; then
  pass "downloaded files are private (0600, umask 077)"
else
  fail "downloaded files are private (0600, umask 077)" "$(grep FILEMODE "$COSIGN_LOG")"
fi
STUBS=""

inst -- --version "$TAG" --require-signature
expect_fail "--require-signature without cosign is an error" "cosign is not on PATH"
expect_not_installed "--require-signature without cosign installs nothing"
inst -- --version "$TAG" --require-signature --dry-run
expect_fail "--require-signature without cosign fails a dry run too" "cosign is not on PATH"

# ---- the https code path (fake curl) ---------------------------------------------------------------
# curl_problems LOG: checks every recorded curl call as flag/value PAIRS, not just tokens: -q is the
# very first argument, --proto and --proto-redir are followed by =https, --fail and --location are
# there, the URL follows `--`, and --max-filesize carries the cap for the file (archives: 150 MiB,
# text: 1 MiB). Prints one line per problem.
curl_problems() {
  awk '
    function has(f,   j) { for (j = 0; j < i; j++) if (a[j] == f) return 1; return 0 }
    function pair(f, v,   j) { for (j = 0; j < i - 1; j++) if (a[j] == f && a[j + 1] == v) return 1; return 0 }
    function check(   url, cap) {
      if (a[0] != "-q") print "call " n ": -q is not the first argument (" a[0] ")"
      if (!pair("--proto", "=https")) print "call " n ": no --proto =https pair"
      if (!pair("--proto-redir", "=https")) print "call " n ": no --proto-redir =https pair"
      if (!has("--fail")) print "call " n ": no --fail"
      if (!has("--location")) print "call " n ": no --location"
      url = a[i - 1]
      if (a[i - 2] != "--") print "call " n ": the URL does not follow --"
      if (url !~ /^https:\/\//) print "call " n ": URL is not https: " url
      cap = (url ~ /\.tar\.gz$/) ? "157286400" : "1048576"
      if (!pair("--max-filesize", cap)) print "call " n ": no --max-filesize " cap " pair for " url
    }
    /^CALL$/ { inb = 1; n++; i = 0; delete a; next }
    /^END$/ { inb = 0; check(); next }
    /^STDIN-LEAK$/ { print "curl read the installer stdin"; next }
    inb { a[i++] = $0 }
    END { if (n == 0) print "no curl call recorded" }
  ' "$1"
}
CURL_LOG="$ROOT/curl.log"
: >"$CURL_LOG"
STUBS="$ROOT/stubs-curl:"
STDIN_FILE="$POISON"
inst CURL_LOG="$CURL_LOG" CURL_ROOT="$REL" -- --version "$TAG" --base-url "https://mirror.example.test/rel"
STDIN_FILE=/dev/null
expect_ok "an https base-url downloads through curl"
expect_installed "the curl-downloaded release installs"
problems="$(curl_problems "$CURL_LOG")"
[ -z "$problems" ] && pass "every curl call: -q first, --proto =https, --proto-redir =https, --fail, --location, --max-filesize cap, URL after --" ||
  { OUT="$problems"; fail "curl flag/value pairs" "see below"; }
[ "$(grep -c '^CALL$' "$CURL_LOG")" = "2" ] && pass "a pinned install makes exactly two curl calls (checksums.txt, archive)" ||
  fail "a pinned install makes exactly two curl calls" "$(grep -c '^CALL$' "$CURL_LOG")"
grep -qx 'https://mirror.example.test/rel/download/v0.0.0-test/checksums.txt' "$CURL_LOG" && pass "curl fetches checksums.txt from the tag's download path" ||
  fail "curl fetches checksums.txt from the tag's download path" "$(cat "$CURL_LOG")"
grep -q '^STDIN-LEAK$' "$CURL_LOG" && fail "curl gets /dev/null on stdin" "curl read the installer's stdin" || pass "curl gets /dev/null on stdin (never the script piped into sh)"
: >"$CURL_LOG"
inst CURL_LOG="$CURL_LOG" CURL_ROOT="$REL" -- --base-url "https://mirror.example.test/rel/"
expect_ok "latest over https" "latest release is $TAG"
if grep -qx 'https://mirror.example.test/rel/latest/download/checksums.txt' "$CURL_LOG" && ! grep -qi 'api' "$CURL_LOG"; then
  pass "latest is resolved from latest/download/checksums.txt, not the API"
else
  fail "latest is resolved from latest/download/checksums.txt, not the API" "$(cat "$CURL_LOG")"
fi
problems="$(curl_problems "$CURL_LOG")"
[ -z "$problems" ] && pass "latest over https: every curl call keeps the flag/value pairs" || { OUT="$problems"; fail "latest over https: curl pairs" "see below"; }

# With cosign present the bundle is fetched too, and cosign never reads the installer's stdin.
: >"$CURL_LOG"
STUBS="$ROOT/stubs-cosign-ok:$ROOT/stubs-curl:"
STDIN_FILE="$POISON"
inst CURL_LOG="$CURL_LOG" CURL_ROOT="$REL" COSIGN_LOG="$COSIGN_LOG" -- --version "$TAG" --base-url "https://mirror.example.test/rel"
STDIN_FILE=/dev/null
expect_ok "https + cosign installs"
problems="$(curl_problems "$CURL_LOG")"
[ -z "$problems" ] && pass "the bundle download keeps the same curl flag/value pairs" || { OUT="$problems"; fail "bundle download curl pairs" "see below"; }
grep -qx 'STDIN-LEAK' "$COSIGN_LOG" && fail "cosign gets /dev/null on stdin" "cosign read the installer's stdin" || pass "cosign gets /dev/null on stdin"

# No curl: https needs it and wget is never a fallback; a file:/// mirror needs no client at all.
: >"${WGET_LOG:=$ROOT/wget.log}"
STUBS="$ROOT/stubs-wget:"
inst WGET_LOG="$WGET_LOG" CURL_ROOT="$REL" -- --version "$TAG" --base-url "https://mirror.example.test/rel"
expect_fail "without curl the https install stops, even with wget on PATH" "curl is required; install it (for example: apk add curl, apt install curl)"
expect_not_installed "no curl installs nothing"
[ ! -s "$WGET_LOG" ] && pass "wget is never run" || fail "wget is never run" "$(cat "$WGET_LOG")"
STUBS=""
inst -- --version "$TAG" --base-url "https://mirror.example.test/rel" --dry-run
expect_fail "no curl, no wget: curl is required" "curl is required"
inst -- --version "$TAG"
expect_ok "a file:/// mirror needs no download tool"

# ---- size caps (a copy of the installer with tiny caps: archive 8192, binary 2048, text 4096) -------------
SMALL="$ROOT/install-small.sh"
sed -e 's/^MAX_ARCHIVE_BYTES=.*/MAX_ARCHIVE_BYTES=8192/' -e 's/^MAX_BINARY_BYTES=.*/MAX_BINARY_BYTES=2048/' \
  -e 's/^MAX_TEXT_BYTES=.*/MAX_TEXT_BYTES=4096/' "$INSTALL" >"$SMALL"
if [ "$(grep -c '^MAX_[A-Z]*_BYTES=[0-9]*$' "$SMALL")" = 3 ] && grep -qx 'MAX_ARCHIVE_BYTES=8192' "$SMALL" &&
  grep -qx 'MAX_BINARY_BYTES=2048' "$SMALL" && grep -qx 'MAX_TEXT_BYTES=4096' "$SMALL"; then
  pass "fixture: the small-cap copy of the installer"
else
  fail "fixture: the small-cap copy of the installer" "the cap lines were not found"
fi
INSTALL_RUN="$SMALL"
inst -- --version "$TAG"
expect_ok "the small-cap copy still installs the tiny good release"

BIGSUMS="$ROOT/rel-bigsums"
mkrel "$BIGSUMS" "$ROOT/good.tar.gz"
for i in $(seq 1 60); do printf '%064d  filler_%s.zip\n' "$i" "$i"; done >>"$BIGSUMS/download/$TAG/checksums.txt"
BASE="file://$BIGSUMS"
inst -- --version "$TAG"
expect_fail "a checksums.txt over the text cap is refused" "is larger than 4096 bytes"
expect_not_installed "an oversize checksums.txt installs nothing"
cp "$BIGSUMS/download/$TAG/checksums.txt" "$BIGSUMS/latest/download/checksums.txt"
inst --
expect_fail "the latest checksums.txt over the text cap is refused" "is larger than 4096 bytes"
BASE="$BASE_SAVE"

if [ "$HAVE_PY" -eq 1 ]; then
  python3 -I - "$ROOT/pkg-big" "$ROOT/big.tar.gz" "$ROOT/zero.tar.gz" "$ROOT/empty-bin.tar.gz" "$ROOT/four.tar.gz" <<'PY'
import io, os, sys, tarfile
d, big, zero, emptyb, four = sys.argv[1:6]
def mk(out, entries):
    with tarfile.open(out, "w:gz") as tf:
        for name, data in entries:
            ti = tarfile.TarInfo(name); ti.size = len(data); ti.mode = 0o755
            tf.addfile(ti, io.BytesIO(data))
mk(big, [("ccshelf", os.urandom(12000))])                       # archive > 8192 (incompressible)
mk(zero, [("ccshelf", b"echo ccshelf\n" + b"\0" * 6000)])      # archive tiny, binary > 2048
mk(emptyb, [("ccshelf", b"")])
mk(four, [("ccshelf", b"x"), ("LICENSE", b"x"), ("README.md", b"x"), ("extra", b"x")])
PY
  for kind in big zero empty-bin four; do
    f="$ROOT/$kind.tar.gz"
    [ "$kind" = four ] && f="$ROOT/four.tar.gz"
    mkrel "$ROOT/rel-$kind" "$f"
  done
  BASE="file://$ROOT/rel-big"
  inst -- --version "$TAG"
  expect_fail "an archive over the archive cap is refused" "is larger than 8192 bytes"
  expect_not_installed "an oversize archive installs nothing"
  BASE="file://$ROOT/rel-zero"
  HEAD_LOG="$ROOT/head.log"
  : >"$HEAD_LOG"
  STUBS="$ROOT/stubs-head:"
  inst HEAD_LOG="$HEAD_LOG" -- --version "$TAG"
  STUBS=""
  expect_fail "an entry over the binary cap is refused (a decompression bomb)" "is larger than 2048 bytes"
  expect_not_installed "an oversize binary installs nothing"
  grep -qx -- '-c 2049' "$HEAD_LOG" && pass "extraction is cut off by head -c <cap + 1>" || fail "extraction is cut off by head -c <cap + 1>" "head calls: $(cat "$HEAD_LOG")"
  BASE="file://$ROOT/rel-empty-bin"
  inst -- --version "$TAG"
  expect_fail "an empty ccshelf entry is refused" "is empty"
  expect_not_installed "an empty binary installs nothing"
  BASE="file://$ROOT/rel-four"
  inst -- --version "$TAG"
  expect_fail "an archive with more than three entries is refused" "has 4 entries"
  BASE="$BASE_SAVE"
else
  skip "size cap fixtures" "python3 is not installed"
fi
INSTALL_RUN=""

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
if [ "$ARCH" = amd64 ]; then expect_ok "x86_64 maps to amd64"; else expect_fail "x86_64 maps to amd64" "ccshelf_${BARE}_${OS}_amd64.tar.gz"; fi
inst FAKE_UNAME_M="aarch64" -- --version "$TAG" --dry-run
if [ "$ARCH" = arm64 ]; then expect_ok "aarch64 maps to arm64"; else expect_fail "aarch64 maps to arm64" "ccshelf_${BARE}_${OS}_arm64.tar.gz"; fi
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
# A SHA-256 tool that prints junk is caught before the comparison.
STUBS="$ROOT/stubs-badsha:"
inst -- --version "$TAG"
expect_fail "a SHA-256 tool that prints a non-digest is refused" "could not compute the SHA-256"
STUBS=""
TOOLDIR="$ROOT/tools-nosha"
mktools "$TOOLDIR" sha256sum shasum openssl
inst -- --version "$TAG"
expect_fail "no SHA-256 tool at all" "no SHA-256 tool"
TOOLDIR=""

# ---- HOME is validated like any other path ------------------------------------------------------------
NO_BINDIR=1
inst HOME="/tmp/home"$'\n'"evil" -- --version "$TAG" --dry-run
expect_fail "a newline in HOME is refused when --bin-dir is not given" "HOME must not contain"
inst HOME="/tmp/hom"$'\303\251' -- --version "$TAG" --dry-run
expect_fail "a non-ASCII HOME is refused when --bin-dir is not given" "HOME must not contain"
inst HOME="relative/home" -- --version "$TAG" --dry-run
expect_fail "a relative HOME is refused" "absolute path"
inst HOME="" -- --version "$TAG" --dry-run
expect_fail "an empty HOME needs --bin-dir" "HOME is not set"
NO_BINDIR=0

# ---- signals: a killed installer still removes its temp directory and its staged file -------------------------
if [ "$HAVE_PY" -eq 1 ]; then
  for sig in HUP INT TERM; do
    case "$sig" in HUP) want=129 ;; INT) want=130 ;; TERM) want=143 ;; esac
    sdir="$ROOT/sig-$sig"
    mkdir -p "$sdir/home" "$sdir/tmpdir"
    res="$(python3 -I - "$sig" "$TEST_SH" "$INSTALL" "$sdir" "$ROOT/stubs-slow-curl" "$TOOLS" <<'PY'
import os, signal, subprocess, sys, time
sig, sh, install, sdir, stub, tools = sys.argv[1:7]
pidfile = os.path.join(sdir, "stub.pid")
env = {"HOME": sdir + "/home", "TMPDIR": sdir + "/tmpdir", "PATH": stub + ":" + tools, "SLOW_PID_FILE": pidfile}
p = subprocess.Popen([sh, install, "--version", "v0.0.0-test", "--bin-dir", sdir + "/bin",
                      "--base-url", "https://mirror.example.test/rel"],
                     env=env, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
for _ in range(300):
    if os.path.exists(pidfile) and open(pidfile).read().strip():
        break
    time.sleep(0.05)
else:
    p.kill(); print("no-stub"); sys.exit(0)
stubpid = int(open(pidfile).read().strip())
signum = getattr(signal, "SIG" + sig)
os.kill(p.pid, signum)      # the shell runs its trap once the foreground child has ended,
os.kill(stubpid, signum)    # which a terminal sends to the whole foreground group: do the same
try:
    rc = p.wait(timeout=20)
except subprocess.TimeoutExpired:
    p.kill(); print("hang"); sys.exit(0)
left = os.listdir(sdir + "/tmpdir")
print("rc=%d left=%d" % (rc, len(left)))
PY
)"
    if [ "$res" = "rc=$want left=0" ]; then
      pass "SIG$sig: exit status $want and no temp directory left behind"
    else
      OUT="$res"
      fail "SIG$sig: exit status $want and no temp directory left behind" "got: $res"
    fi
  done
else
  skip "signal handling" "python3 is not installed"
fi

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
