# Roadmap

## Now

Core client implementation is complete. Local cooperation includes bounded explicit-session queue delivery and
continuous processing in an owned Codex app-server session. Remaining work includes release operations
and broader harness qualification:

- **Release accounts and keys (T5.0).** Embed the release key (T5.3b), build APT
  and DNF repositories (T5.7), then publish and rehearse channels (T5.10–T5.13).
- **Apple Developer ID (T5.14).** macOS signing and notarization (T5.4).
- **CI on Linux and Windows.** Finish phase-1 qualification (T4.11). macOS CI
  already passes.
- **A live test credential.** Smoke against the hosted service (T4.12), then the
  demo (T7.5) and launch planning (T8.0).
- **E8 public launch.** Flip the repository public, cut v0.7.0, and point hosted
  install and onboarding materials at this tree (see
  [E8](plans/E8-launch-cutover.md)).

## Not in Ajent

Skill and tool **catalog / registry** work is not an Ajent roadmap item. The
anonymous skills catalog client was retired (ADR 009). Superseded discovery
plans and cross-project work packets have been removed from this client tree.
That product surface belongs to the Gist registry direction.

## Planned

- **More host qualification.** Extend native qualification beyond Codex 0.163.0
  on macOS. Continuous delivery into an existing interactive session remains
  separate from the owned app-server adapter; it still needs observable queue
  drain and session-lifetime ownership.

- **E8: Public launch.** Go public with a leak-clean tree, release v0.7.0 across
  install channels, cut over the hosted one-liner, and serve the setup prompt
  and API description from this repository.
- **E9: Ecosystem growth (outline).** Registry and directory listings, more
  harnesses, Homebrew core.

## Done

- **E1: Repository foundation** (2026-09-23). License, community files, CI, and
  leak and DCO gates.
- **E2: Core client** (2026-09-24). Contract fixtures, config, credentials, API
  client, profiles, scope, feedback and every CLI command.
- **E3: MCP server** (2026-09-24; skills tools retired 2026-09-25). All 18
  contract `ajent_*` tools, delivery deduplication, and an end-to-end transcript
  test.
- **E4: Setup and integrations, except live checks** (2026-09-24). Setup,
  linking, six harness writers, backups, rollback, uninstall, migration from the
  Python client and cross-OS integration tests.
- **E6: Automated upgrades** (2026-09-24). `ajent update`, notices, post-upgrade
  sync, automatic updates for script installs, the upgrading guide and an
  end-to-end upgrade test.
- **E5, partly** (2026-09-24). GoReleaser builds, Homebrew cask, Linux packages,
  Scoop and winget manifests, checksum signing and verification, and the install
  scripts. Every piece fails closed until the release key is embedded.
- **E7, partly** (2026-09-24). README, the user documentation set, generated
  references, the agent API description, the agent setup prompt and the
  developer integration guide.
- **Skills catalog retirement** (2026-09-25). Removed `ajent skills` and
  `ajent_skills_*`; decommissioned the public catalog edge deployment. See
  [ADR 009](adr/009-skills-catalog-retired.md).

- **Local channel cooperation.** Unread-line watcher plus opt-in, one-batch
  Codex 0.163.0 queue delivery, private acceptance records and explicit ambiguous
  submission recovery. See [channel watching](channel-watch.md).
