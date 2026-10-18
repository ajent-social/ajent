#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Tests for scripts/leak-scan.sh. Uses only the synthetic pattern
# zz-synthetic-leak-[0-9]+; never a real denylist term. Usage:
# sh scripts/leak-scan_test.sh
set +x
set -eu

script_dir=$(CDPATH="" cd -- "$(dirname -- "$0")" && pwd)
leak_scan="$script_dir/leak-scan.sh"

status=0
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

fail() {
  echo "FAIL: $1"
  status=1
}

# Case 1: a clean tree with a real denylist should exit 0.
echo "hello world" >"$work/clean.txt"
out=$(LEAK_DENYLIST='zz-synthetic-leak-[0-9]+' sh "$leak_scan" "$work" 2>&1) && rc=0 || rc=$?
if [ "$rc" -ne 0 ]; then
  fail "clean tree: expected exit 0, got $rc ($out)"
fi

# Case 2: a dirty tree should exit 1, reporting only file:line.
echo "zz-synthetic-leak-42" >"$work/dirty.txt"
out=$(LEAK_DENYLIST='zz-synthetic-leak-[0-9]+' sh "$leak_scan" "$work" 2>&1) && rc=0 || rc=$?
if [ "$rc" -ne 1 ]; then
  fail "dirty tree: expected exit 1, got $rc"
fi
case "$out" in
  *"dirty.txt:1"*) ;;
  *) fail "dirty tree: output missing 'dirty.txt:1' (got: $out)" ;;
esac
case "$out" in
  *"zz-synthetic-leak"*) fail "dirty tree: output must not contain the matched pattern text (got: $out)" ;;
  *) ;;
esac
rm -f "$work/dirty.txt"

# Case 3: an unconfigured denylist with LEAK_SCAN_REQUIRED=1 must fail closed.
out=$(LEAK_DENYLIST='' LEAK_SCAN_REQUIRED=1 sh "$leak_scan" "$work" 2>&1) && rc=0 || rc=$?
if [ "$rc" -ne 2 ]; then
  fail "required but unconfigured: expected exit 2, got $rc ($out)"
fi

# Case 4: an unconfigured denylist without LEAK_SCAN_REQUIRED must skip cleanly.
out=$(LEAK_DENYLIST='' sh "$leak_scan" "$work" 2>&1) && rc=0 || rc=$?
if [ "$rc" -ne 0 ]; then
  fail "unconfigured, not required: expected exit 0, got $rc ($out)"
fi

if [ "$status" -eq 0 ]; then
  echo PASS
fi
exit $status
