# ADR 011: Owner-controlled local release preparation

## Status

Local preparation is authorized by the owner’s explicit local verification and
release instruction. Official preparation requires approved key custody and
matching public trust material that has been independently reviewed and landed.

## Context

ADR 004 assumes signing in a protected CI environment. The owner has authorized
local verification and release, while preserving repository policy and the
existing verifier trust boundary. An absent signing key or empty `releaseKeys`
is a release blocker, not permission to generate replacement official keys.

## Decision

Allow a local, owner-controlled preparation route in addition to ADR 004's CI
route. The owner supplies an approved vault/key reference and controls access,
review and revocation. No tooling imports signing identities, reads a password
manager, generates official keys or changes GitHub protection. T5.0 custody and
T5.3 public-key handoff remain prerequisites. Review and land the matching
`releaseKeys`, packaging PEM, installer trust material and security fingerprint
before official preparation. Private material enters through
`RELEASE_ED25519_KEY`, never arguments or files written by this tool.

`localrelease prepare` requires a clean exact HEAD and snapshots its tracked
files using `git archive`. Checks and cross-compilation run on that snapshot,
with a fixed environment, local installed toolchain, cached modules and fixed
linker flags. It executes formatting, lint, race tests and vulnerability
checking itself. It accepts no externally asserted passing receipt. The caller
holds the build lease. A failed check, build or native self-check leaves private
diagnostics and no final preparation marker.

Official candidates contain six archives with the established names and
LICENSE, NOTICE and README, SHA-256 checksums and the existing T5.3 Ed25519
signature format. The supplied private key must match the literal source
`releaseKeys` list and packaging PEM. The built native binary must verify all
archives and the local evidence receipt using its compiled trust. No
`extraKeyB64` linker flag is accepted or generated.

`--local` explicitly permits an unsigned candidate with a `-local.<source>`
version suffix, no signature, and an `unsigned-local-only` receipt. It cannot
be promoted by renaming files or adding a signature later: rerun signed
preparation against the reviewed release commit. There is no CLI key-generation
or test-key official-signing mode.

Publication is a separate owner action described in the release runbook. Local
checks are never posted as hosted check statuses. Reviewed landed source,
existing exact tag, authorized GitHub access, draft download verification and
explicit publication approval remain required. Release-please retains version
and tag ownership; GitHub branch protection and token defaults are unchanged.
Release-please uses `draft: true` and `force-tag-creation: true`: its reviewed version PR owns the tag and empty draft, then the
operator verifies the exact tag/source and empty asset list before uploading the
matching signed candidate. No assets are replaced. Owner review of the concrete
draft precedes publication. This route does not establish T5.11 completion.

## Consequences and limits

This bounded route prepares archives only. It does not generate SBOMs,
attestations, deb/rpm packages or channel updates, and must not advertise those
outputs. It does not sign with Developer ID, notarize or Authenticode-sign.
The unsigned macOS launch fallback under ADR 004 must be disclosed if used;
availability of a Developer ID does not authorize automatic key import. Local receipts establish observed command execution
on the trusted workstation, not independent provenance or hosted CI.

No stable public release is allowed while approved custody or embedded trust
is missing. Existing T5.0/T5.3/T5.3b gates and installer requirements are not
waived. Future changes to broader distribution require their own verification.
