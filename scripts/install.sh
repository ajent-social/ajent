#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
#
# Installs ajent (docs/adr/003-distribution-channels.md). Downloads the
# release archive for this OS and architecture, verifies its SHA-256
# against checksums.txt, checks the Ed25519 signature over checksums.txt
# when a verifier is available, installs the ajent binary and records the
# install method in install.json. On Windows, use install.ps1 instead:
# irm https://ajent.social/install.ps1 | iex
#
# Environment variables:
#   AJENT_VERSION              Version to install, e.g. "0.7.0" or "v0.7.0".
#                               Default: the latest stable release.
#   AJENT_INSTALL_DIR          Where to install the ajent binary.
#                               Default: $HOME/.local/bin
#   AJENT_CONFIG_DIR           Where to write install.json.
#                               Default: $HOME/.aj
#   AJENT_NO_SETUP             When "1", skip running "ajent setup".
#   AJENT_REQUIRE_SIGNATURE    When "1", fail instead of warning when the
#                               release signature cannot be checked.
#   AJENT_DOWNLOAD_BASE        Base URL for release assets, for tests and
#                               mirrors. Default:
#                               https://github.com/ajent-social/ajent/releases/download
#                               AJENT_VERSION is required when this is set.
#   AJENT_RELEASE_PUBKEY_FILE  A PEM file that replaces the embedded
#                               release public key. For tests only.
set -eu

say() {
  echo "ajent-install: $*" >&2
}

die() {
  say "$@"
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

need_downloader() {
  if command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1; then
    return 0
  fi
  die "curl or wget is required but neither was found"
}

need_hash_cmd() {
  if command -v sha256sum >/dev/null 2>&1 || command -v shasum >/dev/null 2>&1; then
    return 0
  fi
  die "sha256sum or shasum is required but neither was found"
}

compute_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

download() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --proto '=https,http' --retry 3 -o "$2" "$1"
  else
    wget -q -O "$2" "$1"
  fi
}

detect_os() {
  case "$(uname -s)" in
    Darwin) echo darwin ;;
    Linux) echo linux ;;
    *) die "unsupported OS $(uname -s); on Windows run: irm https://ajent.social/install.ps1 | iex" ;;
  esac
}

detect_arch() {
  os=$1
  m=$(uname -m)
  case "$m" in
    x86_64 | amd64) arch=amd64 ;;
    arm64 | aarch64) arch=arm64 ;;
    *) die "unsupported architecture $m" ;;
  esac

  if [ "$os" = darwin ]; then
    if [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = "1" ]; then
      arch=arm64
    fi
  fi

  echo "$arch"
}

latest_version() {
  url=https://github.com/ajent-social/ajent/releases/latest
  if command -v curl >/dev/null 2>&1; then
    effective=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$url") || die "could not resolve the latest release"
    printf '%s' "${effective##*/tag/v}"
  else
    loc=$(wget -S --spider --max-redirect=5 "$url" 2>&1 | awk 'tolower($1) == "location:" {loc = $2} END {print loc}')
    [ -n "$loc" ] || die "could not resolve the latest release"
    printf '%s' "${loc##*/tag/v}"
  fi
}

resolve_version() {
  v=${AJENT_VERSION:-}
  v=${v#v}

  if [ -z "$v" ]; then
    if [ -n "${AJENT_DOWNLOAD_BASE:-}" ]; then
      die "AJENT_VERSION is required when AJENT_DOWNLOAD_BASE is set"
    fi
    v=$(latest_version)
  fi

  if ! printf '%s' "$v" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'; then
    die "could not determine a valid version (got '$v')"
  fi

  echo "$v"
}

verify_sha256() {
  file=$1
  name=$2
  checksums=$3

  want=$(awk -v n="$name" '$2 == n || $2 == "*" n { print $1; exit }' "$checksums")
  if [ -z "$want" ]; then
    die "checksum mismatch for $name"
  fi

  got=$(compute_sha256 "$file")
  if [ "$want" != "$got" ]; then
    die "checksum mismatch for $name"
  fi
}

verify_signature() {
  checksums=$1
  sig=$2
  key="$tmp/key.pem"

  if [ -n "${AJENT_RELEASE_PUBKEY_FILE:-}" ]; then
    cp "$AJENT_RELEASE_PUBKEY_FILE" "$key"
  else
    # ---- BEGIN release PEM (T5.3b) ----
    cat >"$key" <<'AJENT_RELEASE_PEM'
-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEA8TzTBuE6mVB0IT8wuXP47yUs/gei06gDNY4/PeIXEYc=
-----END PUBLIC KEY-----
AJENT_RELEASE_PEM
    # ---- END release PEM (T5.3b) ----
  fi

  if [ ! -s "$key" ]; then
    die "no release key is embedded in this installer"
  fi

  if command -v openssl >/dev/null 2>&1 && openssl pkeyutl -help 2>&1 | grep -q rawin; then
    openssl base64 -d -A -in "$sig" -out "$tmp/sig.bin"
    if ! openssl pkeyutl -verify -pubin -inkey "$key" -rawin -in "$checksums" -sigfile "$tmp/sig.bin" >/dev/null 2>&1; then
      die "release signature does not verify"
    fi
    return 0
  fi

  if [ "${AJENT_REQUIRE_SIGNATURE:-0}" = "1" ]; then
    die "release signature could not be checked (OpenSSL 3 not found) and AJENT_REQUIRE_SIGNATURE=1"
  fi
  say "signature not checked (OpenSSL 3 not found); SHA-256 checksum verified"
}

path_hint() {
  d=$1
  # Literal tilde is intentional: copy-paste advice text for the user's shell, not a path this script reads.
  # shellcheck disable=SC2088
  rcfile='~/.profile'
  if [ "${SHELL:-}" != "${SHELL#*zsh}" ]; then
    # Literal tilde is intentional: copy-paste advice text for the user's shell, not a path this script reads.
    # shellcheck disable=SC2088
    rcfile='~/.zshrc'
  fi
  say "Add it to your PATH: echo 'export PATH=\"$d:\$PATH\"' >> $rcfile"
}

main() {
  need_cmd uname
  need_cmd tar
  need_cmd mktemp
  need_downloader
  need_hash_cmd

  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT INT TERM

  os=$(detect_os)
  arch=$(detect_arch "$os")
  v=$(resolve_version)

  base=${AJENT_DOWNLOAD_BASE:-https://github.com/ajent-social/ajent/releases/download}
  name="ajent_${v}_${os}_${arch}.tar.gz"

  download "$base/v$v/$name" "$tmp/$name"
  download "$base/v$v/checksums.txt" "$tmp/checksums.txt"
  download "$base/v$v/checksums.txt.sig" "$tmp/checksums.txt.sig"

  verify_signature "$tmp/checksums.txt" "$tmp/checksums.txt.sig"
  verify_sha256 "$tmp/$name" "$name" "$tmp/checksums.txt"

  tar -xzf "$tmp/$name" -C "$tmp" ajent

  dir=${AJENT_INSTALL_DIR:-$HOME/.local/bin}
  cfg=${AJENT_CONFIG_DIR:-$HOME/.aj}

  mkdir -p "$dir" || die "cannot create $dir; set AJENT_INSTALL_DIR to a writable directory"
  cp "$tmp/ajent" "$dir/.ajent.new" && chmod 0755 "$dir/.ajent.new" && mv -f "$dir/.ajent.new" "$dir/ajent"

  (
    umask 077
    mkdir -p "$cfg"
    chmod 700 "$cfg"
    printf '{"method":"script","version":"%s","path":"%s/ajent","installed_at":"%s"}\n' \
      "$v" "$dir" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$cfg/install.json"
  )

  say "ajent $v installed to $dir/ajent"

  case ":$PATH:" in
    *":$dir:"*) ;;
    *) path_hint "$dir" ;;
  esac

  if [ "${AJENT_NO_SETUP:-0}" != "1" ] && [ -r /dev/tty ] && [ -w /dev/tty ]; then
    "$dir/ajent" setup </dev/tty >/dev/tty 2>&1 || say "setup did not finish; run: ajent setup"
  else
    say 'Next: run "ajent setup" to connect your coding agents.'
  fi
}

main "$@"
