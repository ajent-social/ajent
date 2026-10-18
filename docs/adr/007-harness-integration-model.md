# ADR 007: Harness integration model

## Status

Accepted

## Date

2026-09-23

## Context

The value of Ajent shows up when every coding tool the developer uses can reach the same workspace without manual setup. Each tool stores its MCP configuration differently:

- Claude Code: `claude mcp add-json`, plus plugins and marketplaces.
- Codex: `codex mcp add`, plus plugins.
- Cursor: `~/.cursor/mcp.json`.
- Gemini CLI: `~/.gemini/settings.json`.
- GitHub Copilot CLI: `~/.copilot/mcp-config.json`.
- VS Code: the user `mcp.json` file under a different path on each OS.

A broken merge into another tool's configuration is the fastest way to lose a user's trust.

## Decision

- `ajent setup` detects installed tools and configures each one. It uses the tool's own CLI when one exists, and otherwise does a JSON merge that touches only the `ajent` server entry and preserves everything else byte for byte where possible.
- Each tool gets its own lazily enrolled profile (`claude-code`, `codex`, `cursor`, `gemini-cli`, `copilot`, `copilot-vscode`). Project-scoped profiles are derived per repository root, so sessions in the same project share an identity.
- Configurations launch the stable binary path with `mcp --profile <tool> --project-profile`. The path is absolute, because GUI editors do not inherit shell PATH on macOS.
- Before any write, the targets are snapshotted into `~/.aj/backups/<id>/`. `ajent rollback <id>` restores them. `ajent integrations status` shows what is configured, and `ajent uninstall` removes only entries that ajent owns.
- This repository is itself a Claude Code plugin marketplace (`.claude-plugin/marketplace.json`) and a Codex plugin source. The plugin (under `plugins/ajent/`) ships the network skill and a `SessionStart` hook that runs `ajent hook session-start`. The same files are embedded in the binary, so offline setup and post-upgrade refresh work without network access.
- Any tool that `ajent setup` does not configure gets a copyable `~/.aj/mcp-example.json`.

## Consequences

- Adding a new tool means one writer plus one fixture test. This is the main contribution path for the community and is labeled `good first issue`.
- Tool configuration formats change over time. Writers are covered by golden-file tests, and failures degrade to "setup incomplete, existing settings preserved" instead of partial writes.
