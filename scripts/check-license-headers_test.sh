#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Self-test for scripts/check-license-headers.sh. Prints PASS or FAIL: <reason>.
set -eu

script_dir=$(cd -- "$(dirname -- "$0")" && pwd)
checker="$script_dir/check-license-headers.sh"
fixtures="$script_dir/testdata/license"

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

cp "$fixtures/good.go" "$tmpdir/good.go"
cp "$fixtures/good.sh" "$tmpdir/good.sh"

# LICENSE_CHECK_INCLUDE_TESTDATA=1 keeps this self-test independent of where
# the platform's mktemp -d happens to place the temp directory.
out=$(LICENSE_CHECK_INCLUDE_TESTDATA=1 "$checker" "$tmpdir" 2>&1) && rc=0 || rc=$?
if [ "$rc" -ne 0 ]; then
  echo "FAIL: expected exit 0 on good fixtures, got $rc: $out"
  exit 1
fi

cp "$fixtures/bad.go" "$tmpdir/bad.go"

out=$(LICENSE_CHECK_INCLUDE_TESTDATA=1 "$checker" "$tmpdir" 2>&1) && rc=0 || rc=$?
if [ "$rc" -eq 0 ]; then
  echo "FAIL: expected exit 1 with bad.go present, got exit 0"
  exit 1
fi
case "$out" in
  *bad.go*) ;;
  *) echo "FAIL: expected output to mention bad.go, got: $out"; exit 1 ;;
esac

echo PASS
