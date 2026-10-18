#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Scan a directory tree (and optionally a diff range) for privately
# denylisted terms. Patterns come only from the LEAK_DENYLIST env var and
# are never printed, echoed or logged. Usage: scripts/leak-scan.sh [dir]
set +x
set -eu

dir=${1:-.}

if [ -z "${LEAK_DENYLIST:-}" ]; then
  if [ "${LEAK_SCAN_REQUIRED:-0}" = "1" ]; then
    echo "leak-scan: LEAK_DENYLIST is not configured"
    exit 2
  fi
  echo "leak-scan: skipped (no denylist available)"
  exit 0
fi

patterns=$(mktemp)
all_hits=$(mktemp)
trap 'rm -f "$patterns" "$all_hits"' EXIT

printf '%s\n' "$LEAK_DENYLIST" | while IFS= read -r line; do
  case "$line" in
    ''|'#'*) ;;
    *) printf '%s\n' "$line" ;;
  esac
done >"$patterns"

grep -rnIE -i -f "$patterns" --exclude-dir=.git --exclude-dir=dist "$dir" 2>/dev/null | cut -d: -f1,2 >>"$all_hits" || true

if [ -n "${LEAK_SCAN_DIFF_BASE:-}" ]; then
  git diff --unified=0 "$LEAK_SCAN_DIFF_BASE"...HEAD -- "$dir" 2>/dev/null | awk '
    BEGIN { file = ""; lineno = 0 }
    /^\+\+\+ / {
      f = $0
      sub(/^\+\+\+ /, "", f)
      sub(/^b\//, "", f)
      file = f
      next
    }
    /^@@ / {
      h = $0
      sub(/^@@ -[0-9]+(,[0-9]+)? \+/, "", h)
      sub(/[ ,].*/, "", h)
      lineno = h + 0
      next
    }
    /^\+/ {
      content = $0
      sub(/^\+/, "", content)
      print file ":" lineno ":" content
      lineno++
      next
    }
  ' | grep -iE -f "$patterns" 2>/dev/null | cut -d: -f1,2 >>"$all_hits" || true
fi

n=$(wc -l <"$all_hits" | tr -d ' ')
if [ "$n" -gt 0 ]; then
  cat "$all_hits"
  echo "leak-scan: $n potential leak(s)" >&2
  exit 1
fi

exit 0
