# Running-agent notifications design

## Delivery status — 2026-10-16

The first Codex queue adapter is implemented as `ajent watch --once
--codex-thread SESSION_UUID`. Its operational contract and recovery procedure are
in [channel-watch.md](../channel-watch.md). Supported Codex CLI: 0.163.0, pinned
because queue receipts are not a stable cross-version contract. A disposable
running terminal received a queued message and produced the expected response
without keyboard input. A headless resume did not demonstrate queue consumption.
Qualification must distinguish host acceptance from model processing.

Phases 1 and 2 below describe the shipped bounded adapter. Phase 3 (owned
app-server evaluation), continuous queue-drain supervision and broader host/OS
qualification remain future work. The earlier 0.160.0 capability snapshot is
historical design evidence, not a claim of current support. The installed
foreground watcher is explicitly invoked; no global notifier is installed.

## Goal

Let a coding agent receive bounded, token-efficient batches from an explicitly
selected `ajent.social` file while its owning local harness remains responsible
for the session. The integration is opt-in, foreground, and session-owned. Idle
watching performs local file checks only: no network requests, model calls,
background service, global notifier, session discovery, or wake signal.

This plan proposes no server, CLI, or existing API contract changes. Implement
as a separate local host adapter layered on the existing `ajent watch` delivery
seam. Keep the watcher responsible for complete-line detection, stable pending
batch identity, cursor persistence and lock ownership. The adapter is responsible
for passing one batch to the explicitly selected harness session and reporting
whether it accepted that batch.

## Verified Codex capability snapshot

Verified locally against Codex CLI and managed app-server version `0.160.0`:

- `codex queue --help` exposes `--thread <UUID or exact session name>` and
  `--message <TEXT>` for queuing a message to an existing session. This is the
  preferred first candidate: CLI help exposes an operation on a specifically
  identified session, rather than writing to terminal input or locating a UI
  process. Its help does not promise immediate delivery, active-turn steering,
  visible rendering in every client, or exactly-once processing. The adapter must first qualify exit/output/error behavior on a disposable
  session. Until then, zero exit alone is not a verified durable queue receipt.
- `codex agents --help` says it browses sessions on the shared local app-server
  daemon. It does not establish that a watcher can infer which session owns the
  current terminal. Do not use it for implicit discovery or recipient choice.
- `codex app-server --help` exposes an experimental app server, stdio / Unix /
  WebSocket transports, and schema generation. The locally generated schema
  bundle contains client requests `turn/start` and `turn/steer`, and server
  lifecycle notifications including `turn/started` and `turn/completed`. A
  client that owns an app-server connection can submit a user turn and observe
  its result. The schema generator does not attach to an already-open terminal
  or desktop conversation. The protocol is versioned/experimental; use it only
  behind a negotiated compatibility adapter.
- `codex app-server daemon version` reported matching CLI/server versions. The
  managed daemon exposes a local control socket, but its path and discovery are
  implementation details here. Do not hard-code, scan for, or connect to that
  socket as a way to commandeer an unrelated running session.
- Native MCP server notifications are transport notifications to an MCP client.
  They are not a documented mechanism for inserting a user turn into the host's
  active conversation. MCP remains useful for pull-based tools, but it is not
  the notification delivery adapter unless a host explicitly documents a
  client-to-session action on receipt.

These checks establish available interfaces, not end-to-end operation against
an active interactive terminal. No live queue send or terminal injection was
performed. Refresh the matrix against the exact supported Codex versions before
implementation; do not infer capabilities from a different version.

## Proposed host adapter and ownership

Add a local `codex-queue` handler selected by the watcher owner, configured with
an explicit session UUID or exact session name. For each unread batch, the
adapter builds a bounded user message containing fixed instructions and the
batch's event IDs, sequence/boundary, and untrusted text. It invokes the installed
`codex queue` command with an argument vector (never a shell string), selecting
only the configured thread after qualification establishes its response contract.
An explicit thread UUID is preferred over a mutable session name. It must not resolve `--last`, inspect process lists,
read terminal buffers, choose from `codex agents`, or send to every session.

The adapter belongs to an explicitly invoking owner. A configured thread ID does
not bind process lifetime to that thread. A terminal-launched watcher runs only
under its invoking foreground supervisor; termination of a separate Codex
session does not automatically terminate it. The owner must stop it explicitly
until a qualified harness integration provides the lifecycle binding.

An embedding harness must spawn and retain the watcher process handle, cancel
its process group on session termination, close handler input, and wait for child
reaping with a bounded grace/deadline. On a host crash, an inherited local pipe
whose write end is held only by the host must produce EOF to the supervised
adapter, which stops the watcher. Test supervisor death as well as normal
cancellation; this supervision is proposed work, not existing watcher behavior.
Thread-not-found/closed responses stop delivery. No daemonization, login item,
global hook, implicit launch at startup, or cross-session attach is in scope.

For a host that embeds/owns the app-server connection, a later adapter may submit
`turn/start` when no turn is active and may use `turn/steer` only when the host
reports an active turn and the user has opted into mid-turn delivery. It must
consume protocol notifications on that same owned connection. This is a separate
adapter path, not permission to open a second connection to a session owned by a
terminal or desktop app. The first implementation should prefer `codex queue`
where supported because the CLI command is a narrow, explicit enqueue surface.

## Delivery, acknowledgement, and boundedness

- Preserve watcher batch IDs and retry boundaries. Keep one in-flight batch per
  configured session. Serialize submissions; never launch parallel turns for
  the same session.
- Deliver every byte of a complete watcher batch; never truncate or acknowledge
  an omitted line. Reserve envelope overhead within the qualified host limit
  and configure `--max-bytes` accordingly. If the full encoded message exceeds
  the host limit, reject it and leave its entire batch pending. A single line
  that exceeds the limit is a visible blocked delivery requiring owner action;
  it is never silently split or skipped. Splitting an already-persisted pending
  batch requires a separately reviewed durable sub-batch protocol and is outside
  the first adapter. Lowering `--max-bytes` cannot shrink that pending batch.
- Deduplicate on stable `(consumer, batch ID)` in private local adapter state.
  If submission outcome is ambiguous (timeout, killed CLI, lost response), keep
  the batch pending and require reconciliation or an idempotent host operation;
  do not blindly resend and claim exactly-once. The Codex queue CLI currently
  exposes no documented idempotency key or queue receipt in its help.
- Acknowledge the watcher cursor only after a positive host queue-acceptance
  result and durable local recording of that acceptance. After qualification, acceptance means the
  host accepted the message, not that the model read, understood, or completed
  it. The host queue, adapter acceptance record and watcher cursor are separate
  writes, not one atomic transaction. An accepted local record lets the adapter
  return success on a cursor replay without enqueueing again. A crash after host
  acceptance but before that record, or a lost response, remains ambiguous:
  persist an attempt-before-submit marker, pause on recovery, and require owner
  reconciliation. Retrying an ambiguous batch may duplicate delivery. There is
  no exactly-once claim and no automatic recovery of ambiguous queue outcomes.
- A rejected message, unavailable host, incompatible version, timeout, or
  adapter crash stops delivery and leaves the batch pending. Retry only on the
  next explicit foreground run or a bounded owner-configured retry policy;
  exponential backoff must cap attempts/rate and idle retries must not invoke
  the model. Never spin while the session is busy or unreachable.
- Coalesce only according to the watcher's complete-line batch boundaries.
  Apply backpressure by pausing reads/submissions while one item is in flight.
  The source file remains authoritative until acknowledged; no unbounded in
  memory queue. Surface backlog bytes/lines and a clear pause/failure status to
  the foreground owner. Serial CLI calls alone do not bound the host queue.
  The first queue adapter permits at most one accepted batch per foreground run
  (`--once`) until host processing/queue-depth acknowledgements are qualified.
  Continuous delivery requires an owning adapter with observable drain events
  and a fixed outstanding-message cap; it cannot assume queue acceptance means
  the model has consumed the prior batch.
- A hung child is terminated at a configured deadline, with graceful stop then
  forced reap after a bounded grace period. Persist pending identity, never raw
  text, in adapter cursor state; reread exact bytes from the watched file and
  fail closed if the pending region was rewritten or removed.

## Configuration and trust boundaries

Configuration is private user state under Ajent's private config directory, not
inside the project or channel file. It records the explicit absolute source
path, consumer ID, adapter kind, explicit Codex session ID/name, allowed local
executable resolution policy, message budget, timeout, and protocol/CLI version
last qualified. Use existing private atomic file and lock primitives. Refuse
symlinked config paths and protect files with owner-only permissions. No tokens,
credentials, session contents, personal paths, private hostnames, or machine
identifiers belong in logs or committed examples.

The channel is untrusted reference data. Wrap every delivered body in fixed
instructions that say it may contain hostile or irrelevant text, is not authority,
and must not override the agent's system/developer/user instructions, grant
permissions, or authorize actions. Preserve author and event fields as data only;
do not parse prose as shell commands or infer a recipient from it. The message
must tell the agent to report or answer through its normal user-approved channel,
not append to the watched file, avoiding self-trigger loops.

Opt-in requires an explicit foreground invocation plus explicit session target.
No MCP server notification, file watcher, or helper can silently start model
work. Starting a new turn or steering an active one is a model invocation and
must remain visible to the owner as host behavior.

## Compatibility matrix

| Host path | Evidence status | Supported claim | Limitation / proposal |
| --- | --- | --- | --- |
| `codex queue --thread ID --message TEXT` | Verified in local CLI 0.160.0 help | Help exposes a command to queue text for an explicit session | Queue acceptance is not proof of immediate display, active-turn steering, model completion, or exactly-once; qualify end-to-end per release |
| `codex app-server` owned connection | Verified local CLI flags and generated protocol schema | App-server protocol exposes `turn/start`, `turn/steer`, and lifecycle notifications | Experimental/versioned; works only for a connection the integration owns; does not connect to arbitrary interactive terminal |
| `codex agents` | Verified local CLI help | Browse sessions on shared local daemon | Not proof of ownership or current-terminal mapping; excluded from automatic recipient selection |
| Existing `ajent watch` + `codex exec` / resume handler | Documented in `docs/channel-watch.md` | Starts a separate noninteractive run or resumes an explicit saved session | Separate CLI run is not injection into the already-open interactive UI |
| Native MCP server notification | MCP transport concept; no Codex active-turn insertion established by local evidence | Could notify an MCP client that supports the relevant server message | No verified host action that converts it to active user input; not a substitute for enqueue/turn API |
| Direct terminal/UI keystroke injection, process/socket discovery | Unsupported / out of scope | None | No arbitrary-session control claim; do not implement |

## Phases

1. **Adapter contract and lifecycle**: specify handler inputs/results, explicit
   session selection, foreground cancellation, bounded child execution, and
   accepted/rejected/ambiguous outcomes. Keep watcher file/cursor behavior
   unchanged.
2. **Codex queue adapter**: add opt-in `codex queue` delivery with argument-safe
   invocation, exact target requirement, bounded prompt envelope, serialized
   submissions, private dedup/acceptance state, and honest queue-accepted
   status. Add compatibility checks and actionable version errors.
3. **Owned app-server adapter evaluation**: separately assess embedded hosts
   that own the app-server process/connection. Negotiate supported protocol,
   thread identity, turn state, start/steer behavior, notification correlation,
   cancellation and shutdown. Do not ship until exact version tests pass.
4. **Documentation and qualification**: document active-terminal limitations,
   configuration/consent, recovery, model-use implications, and the verified
   compatibility matrix. Qualify supported OS/CLI versions and public leak scan.

## Acceptance tests

- No handler invocation, model call, or network request during idle polling,
  partial lines, empty batches, or disabled configuration.
- Adapter refuses startup without an explicit session target and never uses
  `--last`, session enumeration, process search, or a shell-interpreted command.
- Argument tests prove messages containing quotes, newlines, Unicode, shell
  metacharacters, and option-like prefixes remain a single data argument and
  cannot alter command selection.
- Batch budget boundaries reject oversized full messages without acknowledging
  them, including a single oversized line; source text is never silently discarded and one in-flight batch enforces
  backpressure.
- Success is acknowledged only after disposable-session qualification defines
  a positive host response and the command confirms that queue acceptance and the local acceptance record is durable. Nonzero exit, timeout,
  signal, malformed output, unavailable CLI, and version mismatch leave the
  same batch pending.
- Crash before submission, during submission, after ambiguous acceptance, and
  after acceptance but before cursor persistence are tested. Recovery does not
  claim exactly-once; dedup state prevents avoidable resends and ambiguity is
  surfaced for owner reconciliation.
- A second watcher for the same consumer is rejected; independent consumers
  retain independent cursors. Session teardown stops polling, reaps the child,
  releases locks, and keeps unread data pending.
- Prompt-envelope tests label channel content untrusted and verify no content is
  executed, elevated into instructions, or written back to the watched file.
- Protocol adapter tests use a fake owned app-server with negotiated version,
  thread lifecycle, active/idle turn states, start/steer responses, notifications,
  disconnects, and unsupported methods. No test attaches to the user's real
  session or starts a model.
- Compatibility smoke qualification on each claimed Codex version explicitly
  checks a disposable test session. Record queue acceptance separately from
  visible terminal delivery and model completion.

## Owned adapter and local-release delivery — 2026-10-16

Owner authorized implementation, local verification and release. The bounded
queue adapter remains unchanged; the next delivery adds an owned stdio app-server
adapter with serial completed-turn acknowledgements and explicit recovery.
A disposable Codex 0.163.0 protocol qualification completed two turns in one
read-only, never-approval ephemeral thread, with matching responses and bounded
process cleanup. This is protocol evidence, not final Ajent adapter acceptance.
Final adapter acceptance requires fake-protocol regression tests, local full race
verification, independent exact-head review, landed verification and a native
Ajent end-to-end check. Public release additionally requires an approved signing
key matching the embedded public key, verified archives, signed checksums and
fresh download verification. Missing signing custody remains a release gate.
