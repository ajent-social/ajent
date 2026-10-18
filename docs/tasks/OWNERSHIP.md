# File ownership

Every path that any task contract creates or modifies has exactly one owning row. Only the owner creates the file. Another row may edit an owned file only where the "Shared files" table below allows it, and only inside the named region. A row that needs to touch any other file stops and reports BLOCKED. Package-level hooks and seams that let rows attach behavior without editing each other's files are declared in docs/interfaces.md ("cmd/ajent (package main): hooks and seams").

## Shared files

| Path | Owner | Other rows and the region they may edit |
| ------ | ------- | ------------------------------------------ |
| `cmd/ajent/main.go` | T2.6 | T1.1 creates the first version; T2.6 replaces it entirely (T2.6 depends on T1.1). After T2.6 nobody edits it: wiring goes through the `wire` slice from `cmd/ajent/wire_*.go`. |
| `cmd/ajent/main_test.go` | T2.6 | T1.1 creates the first version; T2.6 replaces it. |
| `go.mod` | T1.1 | T2.2 adds only the `golang.org/x/sys` require line. No other row edits it. |
| `go.sum` | T2.2 | Created by T2.2 with `golang.org/x/sys`. No other row edits it. |
| `Makefile` | T1.1 | T7.3 replaces only the body of the `docs` target. |
| `.golangci.yml` | T1.4 | T4.11 may add a narrowly scoped exclusion with a written reason, for a false positive only. |
| `.goreleaser.yaml` | T5.2 | T5.2 writes empty marker blocks. T5.3 (signs), T5.4 (notarize), T5.5 (homebrew_casks), T5.6 (nfpms) and T5.8 (scoops, winget) each edit only between their own `# ---- BEGIN <key> (T5.x) ----` and `# ---- END ... ----` markers. |
| `.github/workflows/release.yml` | T5.1 | T5.11 adds the `goreleaser` job below the T5.1 placeholder comment and replaces that comment. |
| `release-please-config.json` | T5.1 | T7.7 adds only the `changelog-sections` key. |
| `SECURITY.md` | T1.3 | T5.3b fills the release-key fingerprint placeholder and appends the section "Release signing key". |
| `internal/update/keys.go` | T5.3 | T5.3b edits only the entries of `releaseKeys`. |
| `scripts/install.sh` | T5.9 | T5.3b edits only between `# ---- BEGIN release PEM (T5.3b) ----` and `# ---- END release PEM (T5.3b) ----`. |
| `README.md` | T7.1 | T7.5 only uncomments the demo image line. |
| `internal/mcp/cache.go` | T3.2 | T3.2 ships the in-memory cache. T3.4 then owns the file and may rewrite it (T3.4 depends on T3.2). |
| `internal/mcp/cache_test.go` | T3.2 | Same handoff as cache.go: T3.4 may rewrite it. |
| `internal/mcp/tools_read.go` | T3.3 | T6.5 adds one call in the `ajent_diagnose` handler that merges `update.DiagnoseFields(...)`. Its test lives in its own file `internal/mcp/tools_diagnose_update_test.go`. |
| `cmd/ajent/cmd_update.go` | T6.3 | Nobody else edits it. T6.3 declares the `startDetached` and `autoRun` seams and the hidden `--auto-run` flag; T6.6 assigns `autoRun` from `hook_autoupdate.go`. |
| `docs/interfaces.md` | orchestrator | Tasks never edit it. A task that needs a new shared identifier stops and reports BLOCKED. |
| `docs/plans/*.md, docs/plan.md` | orchestrator | Tasks never edit plan files. |
| `Any Go file under internal/, cmd/ or the module root` | its creating row | T4.11 may change a Go file only to fix a reported lint, vet or race finding, and must not change a signature in docs/interfaces.md. Run T4.11 when no other lane is open on the same files. |

## Files in the ajent-social/packages repository

These live in a separate repository. Rows work in their own clone of it and open a PR there.

| Path | Owner | Other rows and the region they may edit |
| ------ | ------- | ------------------------------------------ |
| `README.md` | T5.7 | T5.10 adds the install-script steps, copies and assertions (T5.10 depends on T5.7). |
| `static/index.html` | T5.7 | T5.10 adds the install-script steps, copies and assertions (T5.10 depends on T5.7). |
| `scripts/build-repo.sh` | T5.7 | T5.10 adds the install-script steps, copies and assertions (T5.10 depends on T5.7). |
| `scripts/test-repo.sh` | T5.7 | T5.10 adds the install-script steps, copies and assertions (T5.10 depends on T5.7). |
| `.github/workflows/publish.yml` | T5.7 | T5.10 adds the install-script steps, copies and assertions (T5.10 depends on T5.7). |
| `.github/workflows/test.yml` | T5.7 | none |
| `tests/fixtures/nfpm.yaml` | T5.7 | none |
| `static/gpg.key` | T5.7 | none |
| `static/ajent-release.pub` | T5.7 | none |
| `static/ajent-release.pub.pem` | T5.7 | none |
| `static/ajent.sources` | T5.7 | none |
| `static/ajent.repo` | T5.7 | none |
| `static/CNAME` | T5.7 | none |

## Single-owner files in this repository

| Path | Owner |
| ------ | ------- |
| `.claude-plugin/marketplace.json` | T4.1 |
| `.editorconfig` | T1.1 |
| `.github/CODEOWNERS` | T1.3 |
| `.github/ISSUE_TEMPLATE/bug_report.yml` | T1.3 |
| `.github/ISSUE_TEMPLATE/config.yml` | T1.3 |
| `.github/ISSUE_TEMPLATE/feature_request.yml` | T1.3 |
| `.github/ISSUE_TEMPLATE/new_harness.yml` | T1.3 |
| `.github/PULL_REQUEST_TEMPLATE.md` | T1.3 |
| `.github/release-notes-header.md` | T7.7 |
| `.github/release.yml` | T7.7 |
| `.github/workflows/ci.yml` | T1.4 |
| `.github/workflows/docs-drift.yml` | T7.3 |
| `.github/workflows/docs.yml` | T7.6 |
| `.github/workflows/e2e-upgrade.yml` | T6.8 |
| `.github/workflows/install-scripts.yml` | T5.9 |
| `.github/workflows/live-smoke.yml` | T4.12 |
| `.github/workflows/macos-signing-check.yml` | T5.4 |
| `.github/workflows/mcp-inspector.yml` | T3.7 |
| `.github/workflows/rehearsal.yml` | T5.13 |
| `.github/workflows/release-snapshot.yml` | T5.2 |
| `.github/workflows/safety.yml` | T1.5 |
| `.github/workflows/verify-release.yml` | T5.12 |
| `.github/workflows/windows-manifests.yml` | T5.8 |
| `.gitignore` | T1.1 |
| `.gitleaks.toml` | T1.5 |
| `.markdownlint.yaml` | T7.6 |
| `.release-please-manifest.json` | T5.1 |
| `AGENTS.md` | T1.6 |
| `CLAUDE.md` | T1.6 |
| `cmd/ajent/cmd_feedback.go` | T2.11 |
| `cmd/ajent/cmd_feedback_test.go` | T2.11 |
| `cmd/ajent/cmd_gendocs.go` | T7.3 |
| `cmd/ajent/cmd_gendocs_test.go` | T7.3 |
| `cmd/ajent/cmd_goals.go` | T2.9 |
| `cmd/ajent/cmd_goals_test.go` | T2.9 |
| `cmd/ajent/cmd_help.go` | T2.6 |
| `cmd/ajent/cmd_hook.go` | T4.2 |
| `cmd/ajent/cmd_hook_test.go` | T4.2 |
| `cmd/ajent/cmd_integrations.go` | T4.6 |
| `cmd/ajent/cmd_integrations_test.go` | T4.6 |
| `cmd/ajent/cmd_login.go` | T4.10 |
| `cmd/ajent/cmd_login_test.go` | T4.10 |
| `cmd/ajent/cmd_mcp.go` | T3.2 |
| `cmd/ajent/cmd_mcp_test.go` | T3.2 |
| `cmd/ajent/cmd_migrate.go` | T4.9 |
| `cmd/ajent/cmd_migrate_test.go` | T4.9 |
| `cmd/ajent/cmd_read.go` | T2.7 |
| `cmd/ajent/cmd_read_test.go` | T2.7 |
| `cmd/ajent/cmd_scope.go` | T2.10 |
| `cmd/ajent/cmd_scope_test.go` | T2.10 |
| `cmd/ajent/cmd_setup.go` | T4.3 |
| `cmd/ajent/cmd_uninstall.go` | T4.8 |
| `cmd/ajent/cmd_uninstall_test.go` | T4.8 |
| `cmd/ajent/cmd_update_autorun_test.go` | T6.6 |
| `cmd/ajent/cmd_update_test.go` | T6.3 |
| `cmd/ajent/cmd_verify_release.go` | T5.3 |
| `cmd/ajent/cmd_verify_release_test.go` | T5.3 |
| `cmd/ajent/cmd_version.go` | T2.6 |
| `cmd/ajent/cmd_write.go` | T2.8 |
| `cmd/ajent/cmd_write_test.go` | T2.8 |
| `cmd/ajent/helpers.go` | T2.6 |
| `cmd/ajent/helpers_test.go` | T2.6 |
| `cmd/ajent/hook_autoupdate.go` | T6.6 |
| `cmd/ajent/hook_autoupdate_test.go` | T6.6 |
| `cmd/ajent/hook_postupgrade.go` | T6.4 |
| `cmd/ajent/hook_postupgrade_test.go` | T6.4 |
| `cmd/ajent/testenv_test.go` | T2.6 |
| `cmd/ajent/wire_feedback.go` | T2.11 |
| `cmd/ajent/wire_integrations.go` | T4.6 |
| `cmd/ajent/wire_notice.go` | T6.5 |
| `cmd/ajent/wire_notice_test.go` | T6.5 |
| `cmd/ajent/wire_profiles.go` | T3.6 |
| `cmd/ajent/wire_profiles_test.go` | T3.6 |
| `cmd/ajent/wire_scope.go` | T2.13 |
| `cmd/ajent/wire_scope_test.go` | T2.13 |
| `CODE_OF_CONDUCT.md` | T1.3 |
| `CONTRIBUTING.md` | T1.3 |
| `docs/adr/008-open-source-boundary.md` | orchestrator |
| `docs/adr/009-skills-catalog-retired.md` | orchestrator |
| `docs/agent-setup.md` | T7.9 |
| `docs/api/openapi.json` | T7.8 |
| `docs/api/README.md` | T7.8 |
| `docs/assets/demo.gif` | T7.5 |
| `docs/assets/README.md` | T7.4 |
| `docs/assets/social-preview.png` | T7.4 |
| `docs/assets/social-preview.svg` | T7.4 |
| `docs/commands.md` | T7.3 |
| `docs/contract.md` | T2.1 |
| `docs/demo.tape` | T7.5 |
| `docs/devlog.md` | T5.13 |
| `docs/harnesses.md` | T7.2 |
| `docs/install.md` | T7.2 |
| `docs/integrating.md` | T7.10 |
| `docs/mcp-tools.md` | T7.3 |
| `docs/privacy.md` | T7.2 |
| `docs/self-update.md` | T7.2 |
| `docs/troubleshooting.md` | T7.2 |
| `docs/upgrading.md` | T6.7 |
| `docs/verifying-releases.md` | T7.2 |
| `embed.go` | T4.1 |
| `embed_test.go` | T4.1 |
| `internal/api/*_test.go` | T2.4 |
| `internal/api/capabilities.go` | T2.5 |
| `internal/api/capabilities_test.go` | T2.5 |
| `internal/api/client.go` | T2.4 |
| `internal/api/errors.go` | T2.4 |
| `internal/api/observe.go` | T2.4 |
| `internal/api/origin.go` | T2.4 |
| `internal/api/retry.go` | T2.4 |
| `internal/cli/cli.go` | T2.6 |
| `internal/cli/cli_test.go` | T2.6 |
| `internal/cli/env.go` | T2.6 |
| `internal/cli/output.go` | T2.6 |
| `internal/cli/output_test.go` | T2.6 |
| `internal/cli/tty_unix.go` | T2.6 |
| `internal/cli/tty_windows.go` | T2.6 |
| `internal/config/config.go` | T2.2 |
| `internal/config/config_test.go` | T2.2 |
| `internal/config/lock.go` | T2.2 |
| `internal/config/lock_test.go` | T2.2 |
| `internal/config/lock_unix.go` | T2.2 |
| `internal/config/lock_windows.go` | T2.2 |
| `internal/config/write.go` | T2.2 |
| `internal/credentials/credential.go` | T2.12 |
| `internal/credentials/credential_test.go` | T2.12 |
| `internal/credentials/project.go` | T2.3 |
| `internal/credentials/project_test.go` | T2.3 |
| `internal/credentials/store.go` | T2.3 |
| `internal/credentials/store_test.go` | T2.3 |
| `internal/doc.go` | T1.1 |
| `internal/feedback/*_test.go` | T2.11 |
| `internal/feedback/capture.go` | T2.11 |
| `internal/feedback/improve.go` | T2.11 |
| `internal/feedback/local.go` | T2.11 |
| `internal/feedback/store.go` | T2.11 |
| `internal/gendocs/gendocs.go` | T7.3 |
| `internal/gendocs/gendocs_test.go` | T7.3 |
| `internal/integrations/agentskills.go` | T4.18 |
| `internal/integrations/agentskills_test.go` | T4.18 |
| `internal/integrations/agentskills_unix.go` | T4.18 |
| `internal/integrations/agentskills_windows.go` | T4.18 |
| `internal/integrations/backup.go` | T4.6 |
| `internal/integrations/backup_test.go` | T4.6 |
| `internal/integrations/claude.go` | T4.4 |
| `internal/integrations/claude_test.go` | T4.4 |
| `internal/integrations/codex.go` | T4.16 |
| `internal/integrations/codex_test.go` | T4.16 |
| `internal/integrations/copilot.go` | T4.14 |
| `internal/integrations/copilot_test.go` | T4.14 |
| `internal/integrations/cursor.go` | T4.5 |
| `internal/integrations/cursor_test.go` | T4.5 |
| `internal/integrations/entry.go` | T4.17 |
| `internal/integrations/entry_test.go` | T4.17 |
| `internal/integrations/gemini.go` | T4.13 |
| `internal/integrations/gemini_test.go` | T4.13 |
| `internal/integrations/harness_e2e_test.go` | T4.7 |
| `internal/integrations/jsonmerge.go` | T4.17 |
| `internal/integrations/jsonmerge_test.go` | T4.17 |
| `internal/integrations/registry.go` | T4.17 |
| `internal/integrations/registry_test.go` | T4.17 |
| `internal/integrations/run.go` | T4.17 |
| `internal/integrations/stablepath.go` | T4.17 |
| `internal/integrations/stablepath_test.go` | T4.17 |
| `internal/integrations/sync.go` | T4.6 |
| `internal/integrations/sync_test.go` | T4.6 |
| `internal/integrations/vscode.go` | T4.15 |
| `internal/integrations/vscode_test.go` | T4.15 |
| `internal/mcp/compact.go` | T3.3 |
| `internal/mcp/compact_test.go` | T3.3 |
| `internal/mcp/defs.go` | T3.2 |
| `internal/mcp/envelope.go` | T3.2 |
| `internal/mcp/envelope_test.go` | T3.2 |
| `internal/mcp/handlers_test.go` | T3.7 |
| `internal/mcp/jsonrpc.go` | T3.1 |
| `internal/mcp/jsonrpc_test.go` | T3.1 |
| `internal/mcp/registry.go` | T3.2 |
| `internal/mcp/registry_test.go` | T3.2 |
| `internal/mcp/serve.go` | T3.2 |
| `internal/mcp/serve_test.go` | T3.2 |
| `internal/mcp/session.go` | T3.2 |
| `internal/mcp/session_test.go` | T3.2 |
| `internal/mcp/tools_diagnose_update_test.go` | T6.5 |
| `internal/mcp/tools_goals.go` | T3.8 |
| `internal/mcp/tools_goals_test.go` | T3.8 |
| `internal/mcp/tools_read_test.go` | T3.3 |
| `internal/mcp/tools_write.go` | T3.5 |
| `internal/mcp/tools_write_test.go` | T3.5 |
| `internal/mcp/validate.go` | T3.2 |
| `internal/mcp/validate_test.go` | T3.2 |
| `internal/profiles/enroll.go` | T3.6 |
| `internal/profiles/enroll_test.go` | T3.6 |
| `internal/profiles/helper_test.go` | T3.6 |
| `internal/profiles/settings.go` | T3.6 |
| `internal/profiles/settings_test.go` | T3.6 |
| `internal/scope/scope.go` | T2.10 |
| `internal/scope/scope_test.go` | T2.10 |
| `internal/setup/hooks.go` | T4.3 |
| `internal/setup/link.go` | T4.3 |
| `internal/setup/link_test.go` | T4.3 |
| `internal/setup/migrate.go` | T4.9 |
| `internal/setup/migrate_test.go` | T4.9 |
| `internal/setup/setup.go` | T4.3 |
| `internal/setup/setup_test.go` | T4.3 |
| `internal/testutil/fakerelease/fakerelease.go` | T6.2 |
| `internal/testutil/fakerelease/fakerelease_test.go` | T6.2 |
| `internal/testutil/fakeserver/fakeserver.go` | T2.4 |
| `internal/testutil/fakeserver/fakeserver_test.go` | T2.4 |
| `internal/tools/openapifilter/filter.go` | T7.8 |
| `internal/tools/openapifilter/filter_test.go` | T7.8 |
| `internal/tools/openapifilter/main.go` | T7.8 |
| `internal/tools/openapifilter/testdata/source.json` | T7.8 |
| `internal/tools/signchecksums/main.go` | T5.3 |
| `internal/tools/signchecksums/main_test.go` | T5.3 |
| `internal/update/apply.go` | T6.3 |
| `internal/update/apply_test.go` | T6.3 |
| `internal/update/detach.go` | T6.3 |
| `internal/update/detach_test.go` | T6.3 |
| `internal/update/detach_unix.go` | T6.3 |
| `internal/update/detach_windows.go` | T6.3 |
| `internal/update/detect.go` | T6.1 |
| `internal/update/detect_test.go` | T6.1 |
| `internal/update/notice.go` | T6.5 |
| `internal/update/notice_test.go` | T6.5 |
| `internal/update/replace_unix.go` | T6.3 |
| `internal/update/replace_windows.go` | T6.3 |
| `internal/update/semver.go` | T6.2 |
| `internal/update/semver_test.go` | T6.2 |
| `internal/update/source.go` | T6.2 |
| `internal/update/source_test.go` | T6.2 |
| `internal/update/state.go` | T6.2 |
| `internal/update/state_test.go` | T6.2 |
| `internal/update/verify.go` | T5.3 |
| `internal/update/verify_test.go` | T5.3 |
| `internal/version/version.go` | T1.1 |
| `internal/version/version_test.go` | T1.1 |
| `LICENSE` | T1.2 |
| `lychee.toml` | T7.6 |
| `NOTICE` | T1.2 |
| `packaging/ajent-release.pub.pem` | T5.3b |
| `packaging/postinstall.sh` | T5.6 |
| `plugins/ajent/.claude-plugin/plugin.json` | T4.1 |
| `plugins/ajent/.codex-plugin/plugin.json` | T4.1 |
| `plugins/ajent/hooks/hooks.json` | T4.1 |
| `plugins/ajent/skills/network/SKILL.md` | T4.1 |
| `scripts/check-cask.sh` | T5.5 |
| `scripts/check-license-headers.sh` | T1.2 |
| `scripts/check-license-headers_test.sh` | T1.2 |
| `scripts/install.ps1` | T5.9 |
| `scripts/leak-scan.sh` | T1.5 |
| `scripts/leak-scan_test.sh` | T1.5 |
| `scripts/rehearse-cask.sh` | T5.13 |
| `scripts/repo-settings.sh` | T1.7 |
| `scripts/repo-settings_check.sh` | T1.7 |
| `scripts/test-install.ps1` | T5.9 |
| `scripts/test-install.sh` | T5.9 |
| `scripts/test-linux-packages.sh` | T5.6 |
| `scripts/test-scoop.ps1` | T5.8 |
| `scripts/testdata/license/bad.go` | T1.2 |
| `scripts/testdata/license/good.go` | T1.2 |
| `scripts/testdata/license/good.sh` | T1.2 |
| `scripts/testdata/links/broken.md` | T7.6 |
| `test/e2e/main_test.go` | T3.7 |
| `test/e2e/mcp_test.go` | T3.7 |
| `test/e2e/upgrade_helpers_test.go` | T6.8 |
| `test/e2e/upgrade_test.go` | T6.8 |
| `test/live/doc.go` | T4.12 |
| `test/live/live_test.go` | T4.12 |
| `testdata/contract/commands.json` | T2.1 |
| `testdata/contract/errors.json` | T2.1 |
| `testdata/contract/files/home/.aj/config.json` | T2.1 |
| `testdata/contract/files/home/.aj/credential.json` | T2.1 |
| `testdata/contract/files/home/.aj/profiles/<identity>/claude-code-<hash8>.json` | T2.1 |
| `testdata/contract/files/home/.aj/profiles/<identity>/terminal-<hash8>.json` | T2.1 |
| `testdata/contract/files/home/.aj/scope-grants.json` | T2.1 |
| `testdata/contract/files/project/.aj/config.json` | T2.1 |
| `testdata/contract/files/project/.git/HEAD` | T2.1 |
| `testdata/contract/golden.json` | T2.1 |
| `testdata/contract/README.md` | T2.1 |
| `testdata/contract/requests/*.json` | T2.1 |
| `testdata/contract/responses/*.json` | T2.1 |
| `testdata/contract/routes.json` | T2.1 |
| `testdata/contract/tools.json` | T2.1 |
| `testdata/integrations/claude/` (all fixtures) | T4.4 |
| `testdata/integrations/claude/claude-json-absent/` | T4.4 |
| `testdata/integrations/claude/claude-json-current/.claude.json` | T4.4 |
| `testdata/integrations/claude/claude-json-foreign/.claude.json` | T4.4 |
| `testdata/integrations/claude/claude-json-owned-old/.claude.json` | T4.4 |
| `testdata/integrations/copilot/<case>/after/` | T4.14 |
| `testdata/integrations/copilot/<case>/before/` | T4.14 |
| `testdata/integrations/cursor/<case>/after/` | T4.5 |
| `testdata/integrations/cursor/<case>/before/` | T4.5 |
| `testdata/integrations/e2e/home/` | T4.7 |
| `testdata/integrations/gemini/<case>/after/` | T4.13 |
| `testdata/integrations/gemini/<case>/before/` | T4.13 |
| `testdata/integrations/jsonmerge/` | T4.17 |
| `testdata/integrations/vscode/<case>/before/`, `after/` | T4.15 |
| `testdata/mcp/e2e_session.expect.json` | T3.7 |
| `testdata/mcp/e2e_session.in.jsonl` | T3.7 |
| `testdata/mcp/feed_mixed.json` | T3.3 |
| `testdata/mcp/goal_large.json` | T3.8 |
| `testdata/mcp/inbox_page.json` | T3.3 |
| `testdata/mcp/initialize.golden.json` | T3.2 |
| `testdata/mcp/post_large.json` | T3.3 |
| `testdata/mcp/search_large.json` | T3.3 |
| `testdata/mcp/transport/basic.in.jsonl` | T3.1 |
| `testdata/mcp/transport/basic.out.jsonl` | T3.1 |
| `testdata/mcp/transport/errors.in.jsonl` | T3.1 |
| `testdata/mcp/transport/errors.out.jsonl` | T3.1 |
| `testdata/migration/python-home/` | T4.9 |
| `testdata/plugins/schema.json` | T4.1 |

## Local channel-watch takeover

New feature files are owned by the channel-watch coordinator. Shared generated
references remain owned by T7.3; their regeneration accompanies this delivery.

| Path | Owner |
| --- | --- |
| `internal/channelwatch/watch.go` | channel-watch coordinator |
| `internal/channelwatch/watch_test.go` | channel-watch coordinator |
| `cmd/ajent/cmd_watch.go` | channel-watch coordinator |
| `cmd/ajent/cmd_watch_test.go` | channel-watch coordinator |
| `docs/channel-watch.md` | channel-watch coordinator |
| `docs/adr/010-local-channel-watcher.md` | channel-watch coordinator |
| `docs/plans/local-channel-watch.md` | channel-watch coordinator |
