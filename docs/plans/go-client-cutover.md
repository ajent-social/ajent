# Go client installation cutover

## Scope and status

Replace Python client launch references with the Go client while preserving
installation identity, profiles, grants, scope, disabled integrations and
unrelated harness settings. Migrate installations that the owner controls;
public distribution changes do not authorize rewriting other users' machines.

The inspected local installation now uses a Go binary built from reviewed main
revision `033a12556e00cfc0f6348affd9ee67056fc9f4c9`. Cursor, Gemini and the disabled
Codex MCP entry launch that binary directly. Local marketplace and cached plugin
session-start hooks use `ajent hook session-start`; shared skill fallback commands
use `ajent`. On the owner's subsequent deletion request, 20 local Python client
files were removed, including compiled files, plugin helpers and old client
backups. All 61 credential/profile files remained byte-identical. No Python
client process was found during the removal check.
Backups precede every external configuration/skill write. No integration was
newly enabled. Running processes must reload their MCP connection or restart to
replace an already-loaded Python process; no foreign session was terminated.

This is a source-built local installation, not a published release. Other
machines have not been inventoried or migrated. No all-installations completion
claim is justified by this local result.

## Evidence on 2026-10-05

- Installed binary reports `dev (033a125, unknown)`; its build identifies the
  reviewed source revision without claiming release provenance.
- Semantic comparisons confirm only command/argument fields changed in the
  three MCP configurations, including preservation of Codex's disabled state.
- All three launch configurations pass JSON-RPC initialization and list 18 MCP
  tools. Root authenticated reliability access succeeds.
- A saved pending project profile fails authentication with HTTP 401 and both
  Go and legacy Python profile enrollment return HTTP 409. This is a pre-existing
  profile/service conflict, not evidence of successful authenticated inbox or
  search operation. A sanitized diagnostic was submitted under existing feedback
  policy. Preserve identity and scope; do not replace tokens to conceal it.
- The live `ajent.social/install.sh` still launches Python, and the live agent
  setup prompt still documents Python commands.
- The Go client repository has no published GitHub releases. Its release workflow
  currently runs release-please without the planned GoReleaser publishing job.
  Signing credential readiness has not been established; repository secret names
  alone cannot establish environment or organization secret availability.
- Cloudflare MCP deployment tools were unavailable to the current and delegated
  session. No production installer or setup-prompt route was changed.

## Remaining gates

1. Qualify release prerequisites from E5, including the protected release
   environment, signing credentials and the T5.11 publisher. Preserve the
   configured verification key; never publish unsigned assets or replace keys
   just to unblock installation. Do not invent signing or notarization evidence.
2. Publish and independently verify a signed Go release on every supported
   platform through the existing release contract. Verify archive contents,
   checksums/signature, provenance and installer behavior against those assets.
3. With authenticated Cloudflare MCP, identify the owner and route bindings for
   installer and setup-prompt distribution. Replace Python endpoints with the
   reviewed Go installer and setup documents only after gate 2; retain a concrete
   deployment rollback and verify live downloads after cutover.
4. Inventory additional owner-controlled installations explicitly. Back up each
   affected harness configuration, replace only owned launch commands, preserve
   disabled state and credential/profile storage, and verify actual MCP startup.
   A host that cannot be reached remains unverified, not migrated.
5. Resolve the existing profile enrollment conflict separately with service-side
   evidence and the captured diagnostic. Verify inbox/search under the original
   authorized identity before calling that profile healthy.
6. Reload owner-controlled MCP connections at a safe session boundary. The
   inspected local Python implementation files have been deleted at the owner's
   request. Retain credential/configuration rollback records; do not reintroduce
   the Python client from an old installer or plugin cache.

## Acceptance

A local migration is complete only when owned launch references use the Go
binary, unrelated settings and disabled flags are unchanged, rollback exists,
and MCP startup succeeds. Authenticated operation is a separate gate. Public
cutover requires verified release assets and live endpoint evidence. A running
session notification adapter remains design work in
[running-agent-notifications.md](running-agent-notifications.md).

## Python retirement boundary

The Go client repository contains no Python Ajent implementation. Its legacy
installation recognition and migration test fixtures remain: they are needed to
recognize older user installations and do not distribute an executable Python
client. Operational or infrastructure Python tooling is separate from the
retired Ajent client. Removal from the hosted service source and production
rollout require their own reviewed change and live verification; this local
file deletion does not prove either. Git history is retained.
