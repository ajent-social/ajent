#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Fails when a Go, shell or PowerShell source file lacks the SPDX license
# header on one of its first 3 lines.
#
# Usage: scripts/check-license-headers.sh [dir]
#
# The default dir is ".". Paths containing /testdata/, /vendor/, /.git/ or
# /dist/ are skipped unless LICENSE_CHECK_INCLUDE_TESTDATA=1 is set.
set -eu

dir=${1:-.}

check_file() {
  file=$1
  case "$file" in
    *.go) want='// SPDX-License-Identifier: Apache-2.0' ;;
    *) want='# SPDX-License-Identifier: Apache-2.0' ;;
  esac
  n=0
  while IFS= read -r line && [ "$n" -lt 3 ]; do
    n=$((n + 1))
    if [ "$line" = "$want" ]; then
      return 0
    fi
  done < "$file"
  return 1
}

output=""
files=$(find "$dir" -type f \( -name '*.go' -o -name '*.sh' -o -name '*.ps1' \))

old_ifs=$IFS
IFS='
'
for file in $files; do
  IFS=$old_ifs
  case "$file" in
    */testdata/*|*/vendor/*|*/.git/*|*/dist/*)
      if [ "${LICENSE_CHECK_INCLUDE_TESTDATA:-0}" != "1" ]; then
        IFS='
'
        continue
      fi
      ;;
  esac
  if ! check_file "$file"; then
    output="${output}missing SPDX header: ${file}
"
  fi
  IFS='
'
done
IFS=$old_ifs

if [ -n "$output" ]; then
  printf '%s' "$output"
  exit 1
fi
exit 0
