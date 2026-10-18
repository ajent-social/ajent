#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
#
# Exercises scripts/install.sh end to end against a loopback HTTP server
# serving a locally built release (docs/tasks/T5.9.md step 4). Never
# downloads from a real host: every case below points install.sh at
# 127.0.0.1 only.
#
# Usage: scripts/test-install.sh <dist-dir> <version>
set -eu

dist=${1:?usage: test-install.sh <dist-dir> <version>}
version=${2:?usage: test-install.sh <dist-dir> <version>}

command -v go >/dev/null 2>&1 || { echo "test-install: go is required" >&2; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "test-install: python3 is required" >&2; exit 1; }

unset CDPATH
root=$(cd -- "$(dirname -- "$0")/.." && pwd)
install_sh="$root/scripts/install.sh"

tmp=$(mktemp -d)
status=0
server_pid=""

# cleanup is invoked indirectly via "trap cleanup EXIT INT TERM" below.
# Trap-only functions are reported as unused/unreachable by some ShellCheck versions.
# shellcheck disable=SC2329,SC2317
cleanup() {
  if [ -n "$server_pid" ]; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -rf "$tmp"
}
trap cleanup EXIT INT TERM

pass() {
  echo "PASS $1"
}

fail() {
  echo "FAIL $1: $2"
  status=1
}

# ajent_install_test_archive_name prints the release archive name for the
# host running this test, matching install.sh's own os/arch detection.
ajent_install_test_archive_name() {
  case "$(uname -s)" in
    Darwin) os=darwin ;;
    Linux) os=linux ;;
    *)
      echo "test-install: unsupported host OS $(uname -s)" >&2
      exit 1
      ;;
  esac

  case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    arm64 | aarch64) arch=arm64 ;;
    *)
      echo "test-install: unsupported host arch $(uname -m)" >&2
      exit 1
      ;;
  esac

  if [ "$os" = darwin ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = "1" ]; then
    arch=arm64
  fi

  echo "ajent_${version}_${os}_${arch}.tar.gz"
}

# ajent_install_test_build_tree populates dest (a fresh "v<version>"
# directory) with every archive and SBOM-free artifact from $dist plus the
# freshly (re)signed checksums.txt and checksums.txt.sig.
ajent_install_test_build_tree() {
  dest=$1
  mkdir -p "$dest"
  for f in "$dist"/*.tar.gz; do
    [ -e "$f" ] && cp "$f" "$dest/"
  done
  for f in "$dist"/*.zip; do
    [ -e "$f" ] && cp "$f" "$dest/"
  done
  cp "$dist/checksums.txt" "$dest/checksums.txt"
  cp "$dist/checksums.txt.sig" "$dest/checksums.txt.sig"
}

ajent_install_test_wait_for_server() {
  i=0
  while [ "$i" -lt 100 ]; do
    if curl -fsS -o /dev/null "$base/" 2>/dev/null; then
      return 0
    fi
    i=$((i + 1))
    sleep 0.1
  done
  return 1
}

ajent_install_test_file_mode() {
  if stat -f '%Lp' "$1" >/dev/null 2>&1; then
    stat -f '%Lp' "$1"
  else
    stat -c '%a' "$1"
  fi
}

ajent_install_test_openssl3_available() {
  command -v openssl >/dev/null 2>&1 && openssl pkeyutl -help 2>&1 | grep -q rawin
}

run_ok() {
  bin_dir="$tmp/bin"
  cfg_dir="$tmp/cfg"

  out=$(AJENT_DOWNLOAD_BASE="$base/ok" AJENT_VERSION="$version" AJENT_INSTALL_DIR="$bin_dir" \
    AJENT_CONFIG_DIR="$cfg_dir" AJENT_NO_SETUP=1 AJENT_RELEASE_PUBKEY_FILE="$tmp/k.pem" \
    sh "$install_sh" 2>&1) || { fail ok "install.sh exited non-zero: $out"; return; }

  if [ ! -x "$bin_dir/ajent" ]; then
    fail ok "ajent binary was not installed"
    return
  fi

  if ! version_json=$("$bin_dir/ajent" version --json 2>&1); then
    fail ok "ajent version --json failed: $version_json"
    return
  fi
  got_version=$(printf '%s' "$version_json" | jq -r .version)
  if [ "$got_version" != "$version" ]; then
    fail ok "unexpected version: got '$got_version', want '$version'"
    return
  fi

  if [ ! -f "$cfg_dir/install.json" ]; then
    fail ok "install.json was not written"
    return
  fi
  if ! jq -e '.method == "script"' "$cfg_dir/install.json" >/dev/null; then
    fail ok "install.json missing method:script: $(cat "$cfg_dir/install.json")"
    return
  fi

  case "$(uname -s)" in
    MINGW* | MSYS* | CYGWIN*) ;;
    *)
      mode=$(ajent_install_test_file_mode "$cfg_dir/install.json")
      if [ "$mode" != "600" ]; then
        fail ok "install.json mode is $mode, want 600"
        return
      fi
      ;;
  esac

  pass ok
}

run_bad_checksum() {
  dest="$tmp/www/bad-checksum/v$version"
  ajent_install_test_build_tree "$dest"

  archive=$(ajent_install_test_archive_name)
  printf 'x' >>"$dest/$archive"

  if out=$(AJENT_DOWNLOAD_BASE="$base/bad-checksum" AJENT_VERSION="$version" AJENT_INSTALL_DIR="$tmp/bin2" \
    AJENT_CONFIG_DIR="$tmp/cfg2" AJENT_NO_SETUP=1 AJENT_RELEASE_PUBKEY_FILE="$tmp/k.pem" \
    sh "$install_sh" 2>&1); then
    rc=0
  else
    rc=$?
  fi

  if [ "$rc" -eq 0 ]; then
    fail bad-checksum "install.sh exited 0"
    return
  fi
  case "$out" in
    *"checksum mismatch"*) ;;
    *)
      fail bad-checksum "output does not contain 'checksum mismatch': $out"
      return
      ;;
  esac
  if [ -e "$tmp/bin2/ajent" ]; then
    fail bad-checksum "ajent was installed despite a bad checksum"
    return
  fi

  pass bad-checksum
}

run_bad_signature() {
  if ! ajent_install_test_openssl3_available; then
    echo "SKIP bad-signature (no OpenSSL 3 with 'pkeyutl -rawin' support)"
    return
  fi

  dest="$tmp/www/bad-signature/v$version"
  ajent_install_test_build_tree "$dest"

  go run ./internal/tools/signchecksums -keygen "$tmp/k2" >/dev/null
  RELEASE_ED25519_KEY=$(cat "$tmp/k2.seed") \
    go run ./internal/tools/signchecksums -in "$dest/checksums.txt" -out "$dest/checksums.txt.sig"

  if out=$(AJENT_DOWNLOAD_BASE="$base/bad-signature" AJENT_VERSION="$version" AJENT_INSTALL_DIR="$tmp/bin3" \
    AJENT_CONFIG_DIR="$tmp/cfg3" AJENT_NO_SETUP=1 AJENT_RELEASE_PUBKEY_FILE="$tmp/k.pem" \
    sh "$install_sh" 2>&1); then
    rc=0
  else
    rc=$?
  fi

  if [ "$rc" -eq 0 ]; then
    fail bad-signature "install.sh exited 0"
    return
  fi
  case "$out" in
    *"release signature does not verify"*) ;;
    *)
      fail bad-signature "output does not contain 'release signature does not verify': $out"
      return
      ;;
  esac

  pass bad-signature
}

run_require_signature_without_openssl() {
  dest="$tmp/www/require-signature/v$version"
  ajent_install_test_build_tree "$dest"

  minpath="$tmp/minpath"
  mkdir -p "$minpath"
  tools="uname tar mktemp curl awk cp chmod mv mkdir date grep cat rm"
  if command -v sha256sum >/dev/null 2>&1; then
    tools="$tools sha256sum"
  else
    tools="$tools shasum"
  fi
  for t in $tools; do
    p=$(command -v "$t" 2>/dev/null) || continue
    ln -sf "$p" "$minpath/$t"
  done

  if out=$(PATH="$minpath" AJENT_DOWNLOAD_BASE="$base/require-signature" AJENT_VERSION="$version" \
    AJENT_INSTALL_DIR="$tmp/bin4" AJENT_CONFIG_DIR="$tmp/cfg4" AJENT_NO_SETUP=1 \
    AJENT_REQUIRE_SIGNATURE=1 AJENT_RELEASE_PUBKEY_FILE="$tmp/k.pem" \
    sh "$install_sh" 2>&1); then
    rc=0
  else
    rc=$?
  fi

  if [ "$rc" -eq 0 ]; then
    fail require-signature-without-openssl "install.sh exited 0: $out"
    return
  fi

  pass require-signature-without-openssl
}

# 1. throwaway signing key
go run ./internal/tools/signchecksums -keygen "$tmp/k" >/dev/null

# 2. sign the real checksums.txt
RELEASE_ED25519_KEY=$(cat "$tmp/k.seed") \
  go run ./internal/tools/signchecksums -in "$dist/checksums.txt" -out "$dist/checksums.txt.sig"

# 3. serve tree (one root, one case per subdirectory, one server for all of them)
ajent_install_test_build_tree "$tmp/www/ok/v$version"

# 4. loopback server, bound to 127.0.0.1 only
mkdir -p "$tmp/www"
python3 -m http.server 8766 --bind 127.0.0.1 --directory "$tmp/www" >"$tmp/server.log" 2>&1 &
server_pid=$!

base="http://127.0.0.1:8766"
ajent_install_test_wait_for_server || {
  echo "test-install: loopback server did not start" >&2
  cat "$tmp/server.log" >&2 || true
  exit 1
}

run_ok
run_bad_checksum
run_bad_signature
run_require_signature_without_openssl

exit "$status"
