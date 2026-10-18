# ADR 005: Upgrade strategy

## Status

Accepted (automatic updates for script installs confirmed by the maintainer on 2026-09-23).

## Date

2026-09-23

## Context

The client and the hosted service evolve together, and the service advertises new capabilities over time. Users who fall behind get "unsupported capability" errors. Upgrades must never fight the user's package manager, must never corrupt credentials, and must keep every harness integration pointing at a working binary.

## Decision

- **Package managers own their installs.** When `ajent` detects that Homebrew, dpkg or rpm, Scoop or winget installed it, `ajent update` prints the exact upgrade command for that manager and never modifies the binary itself. Documentation covers automatic upgrades for each manager: `brew autoupdate`, the `unattended-upgrades` origin line, and a scheduled `winget upgrade`.
- **Script installs update themselves.** The install scripts record the install method in `~/.aj/install.json`. `ajent update` downloads the archive for the current platform, verifies it (ADR 004), replaces the binary atomically (on Windows it renames the running executable aside), and rolls back on failure. `--version` pins a release, and `--check` only reports.
- **Automatic by default for script installs.** At most once every 24 hours, the session-start hook starts a detached background update check and applies verified stable releases. Users opt out with `ajent update --auto off` or `AJENT_AUTO_UPDATE=0`. Automatic updates are always off in CI (`CI` is set) and for package-managed installs. Pre-releases are never applied automatically.
- **Integrations follow the binary.** On the first run of a new version, `ajent` refreshes its embedded plugin assets (skill, hook, manifests). It checks that each harness configuration points at the stable binary path (for example the Homebrew `bin` symlink, `~/.local/bin/ajent`, or the WinGet `Links` shim), never a versioned path. This runs under a lock and never writes to MCP stdout.
- **Notices.** Interactive CLI runs print at most one update notice per day, on stderr, and only when stderr is a terminal. `ajent_diagnose` reports `update_available` so agents can tell the human.

## Consequences

- Script users stay current without doing anything, which matches what users of mainstream agent CLIs expect.
- Package-manager users keep full control, and the client never races the package database.
- The update path is security-critical code and gets its own end-to-end tests: tampered checksum, tampered signature, interrupted download, and read-only install directory.
