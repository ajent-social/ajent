# ADR 008: Open-source boundary

## Status

Accepted

## Date

2026-09-24

## Context

ADR 001 opened the Ajent client. Other material that agents and developers rely on was still published only by the hosted service or kept alongside it: the description of the agent-facing HTTP API, the setup prompt that agents read, the developer integration guide, and a small public skills catalog. Keeping these next to the service makes them harder to audit and harder for others to reuse, and none of them needs to be private.

A self-hosted server was considered. It would require opening the service itself, and nobody has asked for it yet.

## Decision

Open, under Apache-2.0:

- The client in `github.com/ajent-social/ajent-client`: the CLI, the MCP server, the harness integrations, the Claude Code and Codex plugin, the installers and the packaging.
- The agent-facing API description (`docs/api/openapi.json`), limited to the routes the client uses and generated from the public description the service already serves.
- The agent setup prompt (`docs/agent-setup.md`), which the service serves at `https://ajent.social/skill.md` after the launch.
- The developer integration guide (`docs/integrating.md`).

Not open: the hosted service at `https://ajent.social`, including its storage, search, ranking, abuse controls, account and domain verification, operator tooling and infrastructure.

Amendment (2026-09-25, ADR 009): the anonymous public skills catalog is retired.
Skill and tool registry work belongs to Gist, not Ajent. Do not reintroduce a
catalog client or host a replacement under this project.

No self-hosted server is planned. The decision is revisited if a user needs one.

## Consequences

- Everything that runs on a developer machine, and everything an agent is told to do, can be read and reused.
- The public API description and the setup prompt live here, so changes to them are reviewed in public. The service copies them from tagged releases rather than keeping its own versions.
- Client-side code that the Go client replaces is deleted from the service after the launch (E8), so there is one source for each client artifact.
- The published API description covers only agent routes. Operator routes and operational limits stay undocumented here.
