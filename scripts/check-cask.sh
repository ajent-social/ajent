#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Validates a GoReleaser-rendered Homebrew cask (Casks/ajent.rb).
#
# It asserts the cask contains at least two GitHub release download URLs
# (macOS arm64 and amd64), at least two `sha256 "..."` lines each holding a
# 64 hex character digest, and that every sha256 digest found in the cask
# also appears in dist/checksums.txt (catching a stale or tampered digest).
#
# Usage: scripts/check-cask.sh <path-to-ajent.rb> [checksums.txt]
#
# The default checksums file is dist/checksums.txt relative to the current
# working directory.
set -eu

cask=${1:?"usage: $0 <path-to-ajent.rb> [checksums.txt]"}
checksums=${2:-dist/checksums.txt}

if [ ! -f "$cask" ]; then
  echo "check-cask: cask file not found: $cask" >&2
  exit 1
fi

if [ ! -f "$checksums" ]; then
  echo "check-cask: checksums file not found: $checksums" >&2
  exit 1
fi

url_prefix='https://github.com/ajent-social/ajent-client/releases/download/'
url_count=$(grep -c "$url_prefix" "$cask" || true)
if [ "$url_count" -lt 2 ]; then
  echo "check-cask: expected at least 2 lines containing '$url_prefix' (arm and intel), found $url_count" >&2
  exit 1
fi

sha_lines=$(grep -E '^[[:space:]]*sha256 "[0-9a-f]+"' "$cask" || true)
sha_count=$(printf '%s\n' "$sha_lines" | grep -c . || true)
if [ "$sha_count" -lt 2 ]; then
  echo "check-cask: expected at least 2 sha256 lines, found $sha_count" >&2
  exit 1
fi

digests=$(printf '%s\n' "$sha_lines" | sed -n 's/.*sha256 "\([0-9a-f]*\)".*/\1/p')

for digest in $digests; do
  len=${#digest}
  if [ "$len" -ne 64 ]; then
    echo "check-cask: sha256 digest '$digest' is $len characters, expected 64" >&2
    exit 1
  fi
  if ! grep -q "^${digest}[[:space:]]" "$checksums"; then
    echo "check-cask: sha256 digest '$digest' from the cask was not found in $checksums" >&2
    exit 1
  fi
done

echo "check-cask: ok ($sha_count sha256 digests verified against $checksums)"
