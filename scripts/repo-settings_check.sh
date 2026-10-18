#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Verifies that GitHub repository settings for ajent-social/ajent match
# what scripts/repo-settings.sh applies. Exits 1 on drift.
#
# Branch protection is not checked here: it can legitimately be unavailable
# while the repository is private (see scripts/repo-settings.sh).
#
# Usage: scripts/repo-settings_check.sh
set -eu

repo=ajent-social/ajent
want_description='Open-source CLI and MCP server that connects coding agents to a shared private workspace of checked findings on ajent.social.'
want_homepage='https://ajent.social'
min_topics=14

description=$(gh api "repos/${repo}" --jq '.description')
homepage=$(gh api "repos/${repo}" --jq '.homepage')
has_discussions=$(gh api "repos/${repo}" --jq '.has_discussions')
allow_merge_commit=$(gh api "repos/${repo}" --jq '.allow_merge_commit')
topics_count=$(gh api "repos/${repo}/topics" --jq '.names|length')

fail=0

if [ "$description" != "$want_description" ]; then
  echo "repo-settings_check: description drift: got '${description}'" >&2
  fail=1
fi

if [ "$homepage" != "$want_homepage" ]; then
  echo "repo-settings_check: homepage drift: got '${homepage}'" >&2
  fail=1
fi

if [ "$has_discussions" != "true" ]; then
  echo "repo-settings_check: discussions are off" >&2
  fail=1
fi

if [ "$allow_merge_commit" != "false" ]; then
  echo "repo-settings_check: merge commits are allowed" >&2
  fail=1
fi

if [ "$topics_count" -lt "$min_topics" ]; then
  echo "repo-settings_check: only ${topics_count} topics set, want at least ${min_topics}" >&2
  fail=1
fi

if [ "$fail" -eq 0 ]; then
  echo "repo-settings_check: OK"
fi

exit "$fail"
