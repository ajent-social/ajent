# Local channel-watch takeover

## Intent

Deliver unread project-channel content to an explicitly selected consumer while
waiting locally without model calls. Owner-authorized exception: ADR 010.

## Scope and ownership

The coordinator owns integration, contracts, documentation and final verification.
The independent reviewer inspects the exact candidate without editing it.
Preserve the original channel-watch branch and worktree.

## Delivery gates

- Bring the four existing feature commits onto current main.
- Review cursor durability, partial lines, rotation, bounded reads and handlers.
- Run formatting, vet, lint, race tests and the public-tree leak scan locally.
- Obtain independent review of the final source revision.
- Merge through the repository policy and verify the landed tree.

## Acceptance boundary

Idle monitoring makes no model or network calls. Delivery contains unread complete
lines; an explicit handler controls acknowledgement. The caller owns the process
and any model invocation. Automatic live-session injection and unattended
installation are outside this delivery.

## Verification record

The initial independent review found unstable batch identity after failed delivery
and subsequent appends. A regression failed before the fix and passes afterward
for both same-process retry and restart. Pending boundaries and digests are now
persisted before delivery without storing channel text.

The initial candidate passed local formatting, vet, lint and full race tests.
Final verification and exact-head review are required after the replay fix.
Hosted Actions are not the delivery gate while billing is unavailable.

## Parallel qualification recovery

Hosted checks ran and exposed existing release-gate failures. Independent isolated
Luna lanes own Windows test portability, feedback lock cleanup and workflow
qualification. The coordinator retains watcher code, generated docs and integration.
Each lane returns signed commits and evidence; final integration waits for all
three, full local verification and independent exact-head review.

- Windows: fixture newlines, browser argument prefix and POSIX-mode assertions.
- Feedback: deterministic lock/test lifecycle cleanup.
- Workflows: patched Go toolchain and an explicitly configured inspector profile.

Historical gitleaks findings are bounded to exact synthetic fixture/prose
fingerprints. New content and the full reachable history remain scanned.

## Combined qualification

Source revision `955b6eb` passed local formatting, vet, lint and the complete race
suite. The migration-path regression failed for backslashes, quotes and newlines
before JSON escaping was added, then passed. Windows cross-platform fixture
normalization preserves exact URL arguments and applicable permission checks.

Independent Luna review passed at `955b6eb` against `178acf2`. Markdown lint passed
all 188 documents; gitleaks passed reachable history; leak-scan self-tests passed.
The repository denylist scan skips locally when its external list is absent;
this does not establish a local denylist pass.

The installed vulnerability analyzer crashed on Go 1.27 source. Repeating the
scan with Go 1.26.6 succeeded with no vulnerabilities. Hosted Linux and macOS
tests, vulnerability scanning and cross-compilation also passed at `955b6eb`;
Windows runtime and corrected workflow setup still require fresh qualification.

Follow-up workflow fixes explicitly pass Inspector subprocess environment,
install shellcheck on macOS, and analyze PowerShell paths individually. The
trap-only cleanup annotation is limited to unused/unreachable function warnings.
Final exact-head review and landed checks remain the final gates.

## Final local evidence

Source `03b189e` passed `make fmt lint test` and a CLI build. Inspector reproduced
locally with an explicit private server config and returned all 18 tools without
a live service. The legacy-skill ownership regression failed for CRLF before the
fix and now passes for LF and CRLF while rejecting foreign/no-Python content.
The ownership predicate accepts only the exact legacy header plus a Python-client
reference; it does not broaden ownership to arbitrary skills.

Go 1.26.6 vulnerability scanning found no vulnerabilities. ShellCheck and
leak-scan self-tests pass. Exact-head review, cross-platform runtime results and
merge receipt are tracked in pull request #85. This does not install a watcher
service or publish a binary release.
