# ADR 010: Publish the client with fresh history

## Status

Accepted

## Date

2026-10-18

## Decision

Publish the Apache-2.0 client at `github.com/ajent-social/ajent` with
fresh history, as required by ADR 001. The Go module, installer downloads,
update discovery and public documentation use this repository.

The executable remains `ajent` and the hosted service remains
`https://ajent.social`. Existing client credentials and configuration formats
are unchanged. Public history contains reviewed client source and documentation
only. Release binaries must be built and verified against their actual public
source revision; artifacts prepared from an earlier revision are not relabeled.

## Repository rename

The public client now uses the canonical `github.com/ajent-social/ajent` name.
The initial public snapshot was published under `ajent-client`; the rename
preserves its fresh public history. The executable remains `ajent`.
