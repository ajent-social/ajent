#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Applies GitHub repository settings for ajent-social/ajent-client: description,
# homepage, topics, merge options, private vulnerability reporting,
# Discussions and branch protection on main. Idempotent: safe to rerun.
#
# Branch protection is expected to fail while the repository is private on
# a free plan (403) or before the first push creates main (404). Both cases
# are reported and treated as success; rerun this script after the
# repository goes public (E8) or after the first push. Private
# vulnerability reporting is also unavailable while private on a free plan
# (404, since it needs GitHub Advanced Security) and is handled the same
# way.
#
# Usage: scripts/repo-settings.sh
set -eu

repo=ajent-social/ajent-client
description='Open-source CLI and MCP server that connects coding agents to a shared private workspace of checked findings on ajent.social.'
homepage='https://ajent.social'

echo "repo-settings: updating description, homepage and merge options"
gh api -X PATCH "repos/${repo}" \
  -f description="${description}" \
  -f homepage="${homepage}" \
  -F has_discussions=true \
  -F has_wiki=false \
  -F allow_merge_commit=false \
  -F allow_squash_merge=true \
  -F allow_rebase_merge=true \
  -F delete_branch_on_merge=true \
  -F allow_auto_merge=true >/dev/null

echo "repo-settings: setting topics"
gh api -X PUT "repos/${repo}/topics" \
  -f 'names[]=mcp' \
  -f 'names[]=model-context-protocol' \
  -f 'names[]=mcp-server' \
  -f 'names[]=claude-code' \
  -f 'names[]=codex' \
  -f 'names[]=cursor' \
  -f 'names[]=gemini-cli' \
  -f 'names[]=github-copilot' \
  -f 'names[]=ai-agents' \
  -f 'names[]=coding-agents' \
  -f 'names[]=agent-memory' \
  -f 'names[]=developer-tools' \
  -f 'names[]=cli' \
  -f 'names[]=golang' >/dev/null

echo "repo-settings: enabling private vulnerability reporting"
set +e
pvr_out=$(gh api -X PUT "repos/${repo}/private-vulnerability-reporting" 2>&1)
pvr_rc=$?
set -e

if [ "$pvr_rc" -eq 0 ]; then
  echo "repo-settings: private vulnerability reporting enabled"
elif printf '%s' "$pvr_out" | grep -q '(HTTP 40[34])'; then
  echo "private vulnerability reporting unavailable while private; rerun at E8 after the repository is public"
else
  echo "repo-settings: private vulnerability reporting call failed unexpectedly:" >&2
  printf '%s\n' "$pvr_out" >&2
  exit 1
fi

echo "repo-settings: applying branch protection on main"
set +e
protection_out=$(gh api -X PUT "repos/${repo}/branches/main/protection" --input - 2>&1 <<'EOF'
{
  "required_status_checks": {"strict": true, "contexts": ["format", "vet", "lint", "test (ubuntu-latest)", "test (macos-latest)", "test (windows-latest)", "vuln", "gitleaks", "leak-scan", "dco"]},
  "enforce_admins": false,
  "required_pull_request_reviews": null,
  "restrictions": null,
  "required_linear_history": true,
  "allow_force_pushes": false,
  "allow_deletions": false
}
EOF
)
protection_rc=$?
set -e

if [ "$protection_rc" -eq 0 ]; then
  echo "repo-settings: branch protection applied"
elif printf '%s' "$protection_out" | grep -q '(HTTP 404)'; then
  echo "main not found; rerun after the first push"
elif printf '%s' "$protection_out" | grep -q '(HTTP 403)'; then
  echo "branch protection unavailable while private; rerun at E8 after the repository is public"
else
  echo "repo-settings: branch protection call failed unexpectedly:" >&2
  printf '%s\n' "$protection_out" >&2
  exit 1
fi

echo "repo-settings: done"
exit 0
