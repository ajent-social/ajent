# ADR 004: Release signing and verification

## Status

Accepted (the maintainer confirmed an Apple Developer ID for notarization on 2026-09-23).

## Date

2026-09-23

## Context

The client stores API credentials and rewrites other tools' configuration. A tampered binary would be a serious compromise. Users and the self-updater both need a simple way to verify releases that does not depend on third-party verifier binaries being installed.

## Decision

- Every release publishes `checksums.txt` and `checksums.txt.sig`, an Ed25519 signature produced in CI from a key held only as a GitHub Actions secret. The public key is compiled into the binary, published in `SECURITY.md`, and served at `https://pkg.ajent.social/ajent-release.pub`. Verification uses only `crypto/ed25519` and `crypto/sha256`.
- GitHub artifact attestations (SLSA build provenance) are generated for every archive and package. Users can run `gh attestation verify`.
- macOS binaries are signed with an Apple Developer ID and notarized in CI (GoReleaser `notarize.macos`). If no Developer ID is available at launch, the fallback is to ship unsigned, with the Homebrew cask clearing the quarantine attribute. That fallback is recorded as a known limitation.
- Windows binaries are not Authenticode-signed at launch. This is revisited if SmartScreen friction shows up in support requests.
- APT and RPM metadata is signed with a dedicated GPG key used only for packages. Its public key is served at `https://pkg.ajent.social/gpg.key`.
- `install.sh` and `install.ps1` always check SHA-256. They also check the Ed25519 signature when a verifier is available (`ajent` itself after download, or `openssl` 3.x). `ajent update` always checks both.

## Consequences

- The self-updater refuses any artifact whose checksum or signature does not match. A compromised release asset alone cannot push code to users on automatic updates.
- Rotating the Ed25519 key requires shipping a release that trusts both the old and new keys before retiring the old one. `SECURITY.md` documents the procedure.
- Signing secrets are the most sensitive assets in the project. They live in a protected `release` environment with required reviewers.
