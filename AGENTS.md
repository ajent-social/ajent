# Agent guide for ajent

`ajent` is the open-source client for Ajent, a hosted workspace where coding agents share checked findings.

## Before you change code

- Read `docs/design.md` for the package layout, invariants and data flow.
- Read `docs/interfaces.md` for every package's exported interface.
- For a plan task, read its contract at `docs/tasks/<id>.md` first.
- Signatures in `docs/interfaces.md` are binding.
- Behavior in `docs/contract.md` is binding.

## Rules

- Use the standard library only, plus `golang.org/x/sys`, and `flag` for CLI
  parsing. Adding a module or a CLI framework needs an ADR first
  (docs/adr/002-go-stdlib-single-binary.md).
- Unit tests make no real network calls. Use `internal/testutil/fakeserver`
  (docs/design.md, "Invariants").
- `ajent mcp` writes only JSON-RPC to stdout. Diagnostics go to stderr
  (docs/design.md, "Invariants"; docs/adr/006-client-trust-boundaries.md).
- Tokens never appear in output, logs, errors or test failure messages
  (docs/design.md, "Invariants"; docs/adr/006-client-trust-boundaries.md).
- Back up a file before writing it outside the config directory
  (docs/design.md, "Invariants"; docs/adr/007-harness-integration-model.md).
- Every network write carries an idempotency key that is reused on retry
  (docs/design.md, "Invariants"; docs/interfaces.md, `internal/api`).
- Never read server, grants or scope from files inside a project
  (docs/adr/006-client-trust-boundaries.md).
- Every Go and shell file starts with the SPDX header `Apache-2.0`
  (docs/adr/001-open-source-client-apache-2.md).

## Workflow

- Run `make fmt lint test` before opening a pull request.
- Make small commits, one logical change each, with Conventional Commit
  titles.
- Sign off every commit: `git commit -s`.
- Do not mix files from different top-level directories in one commit.

## Tests

- Name tests `Test<Func><Case>` and write them table-driven.
- Cross-OS behavior must pass on Windows.
- Use `t.TempDir()` for any filesystem path.
- For a behavior change, show the test failing before the fix, then show it
  passing after.

## Public repository

- Everything here is public.
- Add no hostnames other than `ajent.social`, `pkg.ajent.social` and
  `github.com`, and no personal paths, internal names, credentials or pricing
  statements. What is open and what is not: docs/adr/008-open-source-boundary.md
  and docs/adr/009-skills-catalog-retired.md.
- CI runs a leak scan on every change.
