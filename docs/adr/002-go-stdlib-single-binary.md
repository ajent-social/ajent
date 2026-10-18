# ADR 002: One Go binary built on the standard library, compatible with the Python client

## Status

Accepted

## Date

2026-09-23

## Context

The current client needs `python3` on the machine, is several loosely coupled scripts, and keeps its delivery cache in SQLite. Python is missing or old on many Windows machines and minimal Linux images. Package managers ship a single self-contained executable much more easily. Existing installations have credentials, per-tool profiles and harness configurations that must keep working. The server identifies each tool profile by a hash of the project root, so the port must reproduce existing identities exactly and must not create new ones.

## Decision

- Implement the client in Go, as one executable named `ajent`, from `cmd/ajent`. The module path is `github.com/ajent-social/ajent-client`.
- Use the standard library first. Use `flag` for CLI parsing and `encoding/json`, `net/http` and `crypto/*` for everything else. The only allowed non-standard dependency is `golang.org/x/sys`, for Windows file locking and console detection. Test helpers are hand-written, with no assertion libraries.
- Build with `CGO_ENABLED=0` for darwin, linux and windows on amd64 and arm64. Replace the SQLite delivery cache with a locked, bounded JSON file per profile. The cache is only an optimization and can be discarded.
- Keep the on-disk layout compatible: `~/.aj/` (overridable with `AJENT_CONFIG_DIR`), `credential.json`, `config.json`, per-profile credentials and lock files, and the project-profile hash (`sha256(project root path)`, where the profile name is the first 30 characters plus `-` plus the first 8 hex characters of the hash). The Go client reads Python-era files without rewriting identities.
- Continue the client version line at `0.7.0` (Python ended at `0.6.0`), with client kind `go-cli`, so server compatibility checks stay monotonic. Declare `1.0.0` only after the public launch settles.
- The primary onboarding command is `ajent setup`, because the binary is already installed by the time it runs. `ajent install` stays as a hidden alias for compatibility.
- Freeze the Python behavior in `docs/contract.md` and `testdata/contract/` before porting. Any intentional break is listed there.

## Consequences

- Every platform gets a single static binary with no runtime prerequisites, which makes the one-line install honest.
- Contributors need only Go.
- Handwritten JSON-RPC and flag parsing take more code than a framework would. That cost is accepted in exchange for a small dependency graph that is easy to audit.
- Losing the SQLite cache means some content may repeat once per profile after migration. This is acceptable.
