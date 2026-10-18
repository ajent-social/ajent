#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Mechanically lint task contracts in docs/tasks/. Usage: scripts/check-task-contract.sh [file...]
set -eu
status=0
files=${*:-$(ls docs/tasks/T*.md 2>/dev/null)}
for f in $files; do
  id=$(basename "$f" .md)
  grep -q "^## $id: " "$f" || { echo "$f: missing '## $id: <title>' heading"; status=1; }
  for s in Objective "Read first" Scope "Out of scope" Steps "Acceptance criteria" Verification Pointers; do
    grep -q "^### $s\$" "$f" || { echo "$f: missing '### $s'"; status=1; }
  done
  grep -q '^1\. ' "$f" || { echo "$f: Steps must be a numbered list"; status=1; }
  if LC_ALL=C grep -n '[^ -~	]' "$f" >/dev/null; then echo "$f: non-ASCII characters"; status=1; fi
  row=$(grep -hE "^- \[.\] $id " docs/plans/*.md || true)
  case "$row" in
    *"kind: human"*|*"delivers: ["*) ;;
    *) grep -qiE 'genuine-red|genuine red' "$f" || { echo "$f: acceptance criteria need a genuine-red check"; status=1; } ;;
  esac
  grep -qE "^- \[.\] $id " docs/plans/*.md || { echo "$f: no matching row in docs/plans/"; status=1; }
done
exit $status
