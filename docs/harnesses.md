# Coding tool integrations

`ajent setup` (and `ajent integrations sync`) detects the coding tools
installed on this machine and adds an `ajent` MCP server entry to each one.
It never touches settings it did not write itself, and it backs up every
file before changing it; see "Backups and rollback" below.

Every entry launches the same way: the stable, absolute path to the `ajent`
binary, running `mcp --profile <profile> --project-profile`. Only the
profile name and a few tool-specific keys differ.

## Claude Code

Config file:

- macOS and Linux: `~/.claude.json`
- Windows: `%USERPROFILE%\.claude.json`

ajent also writes `~/.claude/settings.json` (or the Windows equivalent) to
install the Ajent session-start hook, and registers this repository as a
Claude Code plugin marketplace so the bundled skill installs alongside it.

ajent never edits `.claude.json` directly. It runs the `claude` CLI
(`claude mcp add-json --scope user ajent`) with this entry, so Claude
Code's own file invariants stay intact:

```json
{
  "command": "/path/to/ajent",
  "args": ["mcp", "--profile", "claude-code", "--project-profile"],
  "type": "stdio"
}
```

Profile: `claude-code`.

Remove: `ajent uninstall`, or `claude mcp remove --scope user ajent`.

## Codex

Config file:

- macOS and Linux: `~/.codex/config.toml`
- Windows: `%USERPROFILE%\.codex\config.toml`

ajent runs the `codex` CLI (`codex mcp add ajent -- /path/to/ajent mcp
--profile codex --project-profile`) to write this file. The entry it
creates is equivalent to:

```json
{
  "command": "/path/to/ajent",
  "args": ["mcp", "--profile", "codex", "--project-profile"]
}
```

Profile: `codex`.

Remove: `ajent uninstall`, or `codex mcp remove ajent`.

## Cursor

Config file:

- macOS and Linux: `~/.cursor/mcp.json`
- Windows: `%USERPROFILE%\.cursor\mcp.json`

ajent merges an entry into this file's `mcpServers` object, preserving
every other byte:

```json
{
  "mcpServers": {
    "ajent": {
      "command": "/path/to/ajent",
      "args": ["mcp", "--profile", "cursor", "--project-profile"]
    }
  }
}
```

Profile: `cursor`.

Remove: `ajent uninstall`, or delete the `ajent` entry from `mcpServers`
yourself.

## Gemini CLI

Config file:

- macOS and Linux: `~/.gemini/settings.json`
- Windows: `%USERPROFILE%\.gemini\settings.json`

This file also holds your other Gemini CLI settings (theme, authentication
and more); ajent only touches `mcpServers.ajent`:

```json
{
  "mcpServers": {
    "ajent": {
      "command": "/path/to/ajent",
      "args": ["mcp", "--profile", "gemini-cli", "--project-profile"]
    }
  }
}
```

Profile: `gemini-cli`.

Remove: `ajent uninstall`, or delete the `ajent` entry from `mcpServers`
yourself.

## GitHub Copilot CLI

Config file:

- macOS and Linux: `~/.copilot/mcp-config.json`
- Windows: `%USERPROFILE%\.copilot\mcp-config.json`

Copilot CLI's schema needs two extra keys, `type` and `tools`:

```json
{
  "mcpServers": {
    "ajent": {
      "command": "/path/to/ajent",
      "args": ["mcp", "--profile", "copilot", "--project-profile"],
      "type": "local",
      "tools": ["*"]
    }
  }
}
```

Profile: `copilot`.

Remove: `ajent uninstall`, or delete the `ajent` entry from `mcpServers`
yourself.

## VS Code

Config file (the user `mcp.json`):

- macOS: `~/Library/Application Support/Code/User/mcp.json`
- Windows: `%APPDATA%\Code\User\mcp.json`
- Linux: `$XDG_CONFIG_HOME/Code/User/mcp.json`, or
  `~/.config/Code/User/mcp.json` when `XDG_CONFIG_HOME` is unset

VS Code's MCP schema uses `servers` instead of `mcpServers`, and needs a
`type` key:

```json
{
  "servers": {
    "ajent": {
      "command": "/path/to/ajent",
      "args": ["mcp", "--profile", "copilot-vscode", "--project-profile"],
      "type": "stdio"
    }
  }
}
```

Profile: `copilot-vscode`.

Remove: `ajent uninstall`, or delete the `ajent` entry from `servers`
yourself.

## Other MCP clients

For any other MCP-capable tool, copy the entry from `~/.aj/mcp-example.json`.
`ajent setup` and `ajent integrations sync` write this file for every
client they do not configure directly, using the profile `custom-agent`:

```json
{
  "mcpServers": {
    "ajent": {
      "command": "/path/to/ajent",
      "args": ["mcp", "--profile", "custom-agent", "--project-profile"]
    }
  }
}
```

Rename the top-level key (`mcpServers`, `servers`, or whatever your tool
expects) to match your tool's own configuration format.

## Profiles

Each coding tool gets its own credential, and each project gets its own
identity within that tool. A profile name is the tool's base name (for
example `claude-code`) plus a short hash of the project's root directory,
derived locally on your machine -- the directory path itself is never sent
to Ajent. This is why sessions in the same project, in the same tool, share
one identity, while two different projects in the same tool do not.

Profiles are created lazily: the first time a tool calls an ajent MCP tool
in a project, ajent enrolls that profile automatically. You do not need to
run any command yourself.

## Backups and rollback

Before writing to any file outside `~/.aj`, ajent snapshots it (or records
that it was absent) under `~/.aj/backups/<id>/`. If a write goes wrong, or
you simply want your previous settings back:

```sh
ajent rollback BACKUP_ID
```

See what changed, and when, with:

```sh
ajent integrations status
```

`ajent uninstall` removes every entry ajent owns, including ones left by an
earlier Python-based install, and leaves everything else untouched. Add
`--purge` to also delete your local credentials.
