# Local release preparation

This route prepares six archives on an owner-controlled workstation. It never
publishes, creates tags or keys, changes GitHub policies, or updates channels.
ADR 011 describes the local adaptation of ADR 004. A preparation marker is not
release approval or proof of hosted CI, signing custody, or landed review.

## Prerequisites

- Obtain the owner's approved vault/key reference and custody decision. Do not
  generate an official replacement key to unblock release. Complete T5.0's
  handoff and T5.3b's reviewed public trust material, including source keys,
  packaging PEM, installers and security fingerprint. Until then, signed
  preparation fails before checks or builds; stable publication is blocked.
- Use the reviewed release commit in a clean, exclusively held checkout.
  Commit all intended source first. No untracked files are allowed. The shared
  development worktree is unsuitable while another worker is editing it.
- Install the repository-pinned Go toolchain, make, golangci-lint and
  govulncheck, plus the native C compiler required by Go race tests. Prepopulate module caches separately: preparation uses the local
  toolchain with module downloads disabled. Vulnerability checking needs access
  to its vulnerability database; a lookup failure blocks preparation.
- Follow the shared host load and build-lease procedure before invoking the
  wrapper: preparation performs full lint and race tests plus six builds.
  The caller acquires and releases the lease, including on failure. This tool
  neither acquires a lease nor overrides host capacity limits.
- Run on a supported darwin/linux/windows amd64/arm64 host with the required
  make/check tools. Native execution is tested only for the current host;
  cross-compilation does not qualify execution on the other five targets.

## Prepare

Set `OUT` to a **new absolute directory outside the checkout**, whose parent
already exists. The output is private and never overwritten. Set `SHA` to the
full reviewed commit ID. Intended first Go version: `0.7.0`.

```sh
SHA=$(git rev-parse HEAD)
./scripts/release-local.sh prepare --version 0.7.0 --source-sha "$SHA" --out "$OUT"
```

Supply `RELEASE_ED25519_KEY` through the owner's approved secret injection
mechanism. Never put its value in commands, history, logs or receipts. The
accepted encoding is standard base64 of a 32-byte seed or consistent 64-byte
private key. Children do not inherit that variable or other secret variables.
The key must match the actual source `releaseKeys` and packaging PEM. Ambient
Go flags, workspace files and test-key overrides are not inherited.

For an explicitly unsigned local candidate, with no key or trust override:

```sh
./scripts/release-local.sh prepare --local --version 0.7.0 --source-sha "$SHA" --out "$OUT"
```

This records `0.7.0-local.<12-character-source>` in filenames and binaries and
produces no signature. It is never an official release. There is no command
that promotes it. `go run ./internal/tools/localrelease prepare ...` is also
supported; use the wrapper to sanitize the environment before compiling the
tool itself.

The tool snapshots tracked HEAD, runs `gofmt -l .`, `make fmt lint test` and
`govulncheck ./...`, builds all six platform archives with CGO disabled and
fixed version/commit/date linker values, and checks native binary metadata.
`verification.json` records completed local commands, source and toolchain.
Its bytes are included in `checksums.txt`. Signed candidates also contain
`checksums.txt.sig`; the native built binary checks every archive and receipt
against compiled trust. `prepared.json` is written last and binds the checksum
file digest and source. It attests only successful preparation.

Failures retain partial output, hidden logs, native binaries and the `.work`
source snapshot for diagnosis; there is no success marker. Use a different
new output directory to retry. Do not upload the entire output directory:
`.work`, logs and hidden native binaries are private working material.

## Separate owner publication gate

The preparer deliberately has no `publish` phase. Execute publication only within the owner’s explicit release authorization,
after approved signing custody, independent review, landed-source verification
and release notes disclosing the unsigned macOS fallback if used. No
release may claim GitHub attestations, notarization, SBOMs, deb/rpm or channel
availability from this route. Preserve release-please ownership of versioning
and tags; do not race the existing workflow or bypass required checks.

With that authorization, the operator must verify actual GitHub permission,
exact landed main and the existing version tag. These are live reads, not
locally asserted statuses:

```sh
REPO=ajent-social/ajent-client
VERSION=0.7.0
TAG=v$VERSION
test "$(gh api "repos/$REPO" --jq '.permissions.push')" = true
test "$(gh api "repos/$REPO/commits/main" --jq .sha)" = "$SHA"
test "$(gh api "repos/$REPO/commits/$TAG" --jq .sha)" = "$SHA"
```

Stop if the tag is missing, the SHA differs, a required repository gate is
unresolved, or a published release already exists. An existing release-please
draft is accepted only by the empty-draft handoff below. Resolve other cases
through the reviewed release process; these instructions do not authorize
inventing check statuses or replacing an existing release. Inspect `prepared.json` and signed
`verification.json`: require `signed-candidate`, version and source matching
`VERSION` and `SHA`. Verify the prepared checksum-file digest independently
and require exactly the expected six archive names plus `verification.json`.
Use the retained native binary as `VERIFIER` and verify **every** listed file:

```sh
for file in "$OUT"/ajent_"$VERSION"_*.tar.gz "$OUT"/ajent_"$VERSION"_*.zip "$OUT"/verification.json; do
  "$VERIFIER" verify-release --checksums "$OUT/checksums.txt" --sig "$OUT/checksums.txt.sig" --file "$file"
done
```

`VERIFIER` is the hidden native binary under `OUT`, named
`.OS_ARCH_ajent` (with `.exe` on Windows), built without extra trust flags.
Independently compare its embedded version and commit via `version --json`.
Re-run signed preparation if evidence or artifacts change.

Release-please must use `draft: true` and `force-tag-creation: true` for this
route so merging the reviewed version PR creates a tag and draft without
publishing an empty release. Draft releases otherwise defer tag creation until
publication, which would block this exact-tag handoff. The
local preparer must run again against that exact landed version commit. An
earlier candidate from a different source commit cannot be uploaded.

Use an explicitly approved notes file. The notes must disclose
all limitations from `verification.json` and the platform qualification scope.

Require an existing empty release-please-owned draft; stop if it is absent.
Do not create a replacement draft locally. Verify provenance before uploading:

```sh
gh run view "$RELEASE_RUN" --repo "$REPO" --json headSha,conclusion,workflowName
gh pr view "$RELEASE_PR" --repo "$REPO" --json state,mergeCommit
gh release view "$TAG" --repo "$REPO" --json author,databaseId,url,tagName,isDraft,assets
```

Require the successful `release` workflow run's `headSha` and the merged
release PR's `mergeCommit.oid` to equal `SHA`. The release author must be
`github-actions[bot]`, its tag must equal `TAG`, the tag must resolve to `SHA`,
`isDraft` must be true and the asset list must be empty. Record the workflow
run, PR, release database ID and URL. Stop for missing or conflicting
provenance, a published release, another tag or any existing assets; reconcile
rather than replacing them.
Upload only the expected nine files, without `--clobber`:

```sh
gh release upload "$TAG" --repo "$REPO" \
  "$OUT"/ajent_"$VERSION"_*.tar.gz "$OUT"/ajent_"$VERSION"_*.zip \
  "$OUT/checksums.txt" "$OUT/checksums.txt.sig" "$OUT/verification.json"
gh release edit "$TAG" --repo "$REPO" --title "ajent $TAG" --notes-file "$NOTES"
```

After upload, require the draft still has that tag and exactly those nine
asset names. Interrupted uploads require an explicit asset-by-asset provenance
check before continuing; never delete, overwrite or conceal a mismatch. Adding
assets and notes to a release-please-owned draft does not transfer version or
tag ownership to the local preparer.

Download to a separate **new** directory with `gh release download "$TAG"
--repo "$REPO" --dir "$DOWNLOAD"`. Compare downloaded checksum and signature
bytes to the prepared copies, verify every downloaded asset with `VERIFIER`,
and compare version, source, names and limitations again. Stop on any mismatch;
never use `--clobber` or replace assets to conceal one. After owner review of
that concrete draft, publish with `gh release edit "$TAG" --repo "$REPO"
--draft=false`. Download and verify the public assets again and record the
release URL and observed results. This is public-release evidence only;
installer, update, other-platform execution and distribution channel acceptance
still require their respective checks.

## Coordinator verification

The implementation session intentionally runs no heavy checks on an overloaded
host. Under the build lease, run:

```sh
go test ./internal/tools/localrelease -count=1
golangci-lint run ./internal/tools/localrelease/...
make fmt lint test
```

Tests use temporary directories and fake build/check runners, with no network
calls. They cover missing/mismatched trust, seed/full-key consistency, archive
contents, source refusal, failures without a preparation marker, checksum and
signature verification, and unsigned local separation. They do not establish
real builds, live vulnerability results, custody, or provider behavior. A
real preparation from a clean reviewed commit remains necessary. No red/green
or passing-test claim is made until commands have actually been observed.
