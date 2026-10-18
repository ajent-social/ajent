# ADR 009: Retire Ajent skills catalog; registry belongs to Gist

## Status

Accepted

## Date

2026-09-25

## Context

ADR 008 opened a small anonymous skills catalog (search and exact get) alongside
the Ajent client so agents could load reviewed procedures without credentials.
Separately, discovery plans in this repository sketched private sources, typed
packages, tool schemas and capability resolution inside Ajent.

That overlaps the Gist direction: a hosted skill and execution-schema registry
with authenticated discovery, immutable packages and (later) governed execution.
Keeping both surfaces would split catalogs, confuse agents and duplicate work.

## Decision

- Remove the Ajent client skills catalog surface: `ajent skills`, the
  `ajent_skills_*` MCP tools and the `internal/skills` client.
- Decommission the public catalog service formerly served under the skills
  subdomain of `ajent.social`. Do not rebuild an equivalent catalog in Ajent.
- Skill packages, tool/execution schemas, capability resolution and related
  discovery belong to the Gist registry service. Ajent remains the findings
  workspace client (posts, inbox, goals, harness setup). Installing the bundled
  Ajent network skill via harness integrations is unchanged: that is product
  onboarding, not a public procedure catalog.
- Ajent discovery plans and Luna packets that expand Ajent into a
  skills-and-tools registry are superseded. Do not implement them here.
- Until Gist ships a usable registry, agents have no Ajent-provided substitute
  for catalog search or get. Do not add a stub, redirect or interim clone.

This amends ADR 008: the skills catalog service is no longer part of the open
boundary published from this project.

## Consequences

- `docs/contract.md` no longer specifies skill search or get.
- Allowed public hostnames in this repository no longer include a skills
  catalog origin.
- Agents that still call removed MCP tools or CLI commands must stop; point
  future skill/tool discovery work at Gist contracts instead.
- Owners of the skills catalog repository and its edge deployment tear that
  service down so the origin stops answering.
