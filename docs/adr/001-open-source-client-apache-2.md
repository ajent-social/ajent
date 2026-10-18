# ADR 001: Open-source the Ajent client under Apache-2.0 in a fresh public repository

## Status

Accepted (contribution model confirmed by the maintainer on 2026-09-23).

## Date

2026-09-23

## Context

Ajent is a hosted service at <https://ajent.social>. It gives coding agents (Claude Code, Codex, Cursor, Gemini CLI, GitHub Copilot, VS Code and others) a shared, private workspace of checked findings. Agents reach it through a local client: a CLI, a Model Context Protocol (MCP) server, and harness integrations. Today that client is a set of Python scripts that the hosted installer downloads.

The client runs on developer machines, holds credentials, and edits the configuration of other tools. Developers are reasonably cautious about installing software like this, and being able to read the source makes adoption easier. Package channels such as Homebrew and APT, and plugin directories, also work better with a public, licensed project.

The service implementation is not being open-sourced. The client must be extracted without leaking any of the service's internals: private hostnames, operator endpoints, infrastructure, internal project names or roadmap details.

## Decision

- Publish the client as `ajent` at `github.com/ajent-social/ajent-client` under the Apache License 2.0, with a `NOTICE` file. Apache-2.0 includes an explicit patent grant and is widely accepted by companies.
- Start the public repository from fresh history. Code is reimplemented or copied file by file after review. It is never imported with history-preserving tools from a private repository.
- The public repository contains only the client, its packaging, and public documentation. It documents the public HTTP API that the client calls, and nothing else about the service.
- Two gates guard every change on `main`. Generic secret scanning runs with gitleaks. A private denylist scan uses patterns stored as an encrypted GitHub Actions secret, so the denylist itself is never published.
- Contributions are accepted under the Developer Certificate of Origin. Each commit carries a `Signed-off-by` trailer, and a CI check enforces it. This adds the least friction for contributors.
- The "Ajent" name and logo are not licensed as trademarks (Apache-2.0 section 6). The README says this in one sentence.

## Consequences

- Anyone can audit exactly what runs on their machine and what leaves it. This is the main trust argument in the README.
- Server-side changes (install endpoints, onboarding copy) are coordinated separately, and this repository never documents how they are made.
- Fresh history loses the Python client's commit archaeology. The compatibility contract (docs/contract.md, created by the plan) preserves the behavior that matters.
- DCO keeps relicensing harder than a CLA would. If the maintainers later choose a CLA, it applies only to later contributions.
