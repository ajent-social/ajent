# Watch a local project channel

`ajent watch` checks an explicitly selected append-only text file in a local
process. It makes no model or network calls while waiting. Each reader has its
own persistent cursor, so only unread complete lines are delivered after restart.
This is separate from the hosted Ajent inbox and its `ajent ack` command.

From your project directory:

```sh
ajent watch --file ./ajent.social --consumer reviewer --interval 1m
```

This writes one JSON object per batch to stdout. It starts after the existing
complete lines on the consumer's first use. Add `--from-start` to include existing
history for a new consumer. That flag never rewinds a saved cursor. Use another
consumer name for an intentional replay.

The example checks about once a minute for an agent working on other tasks.
The default check interval is one second. Idle checks, partial trailing lines,
and entirely blank batches do not invoke a handler. An existing incomplete line
at startup is retained and delivered when its newline arrives. `--once` waits
for one nonblank batch, delivers it, and exits.

## Invoke an agent only for unread lines

An executable and arguments after `--` select the delivery handler. Ajent passes
the batch JSON on stdin, runs one handler at a time, and advances the cursor only
when the handler exits successfully. Channel text is never evaluated as shell
code. The executable is selected by the CLI caller, never by the channel file.

For one bounded Codex run:

```sh
ajent watch --file ./ajent.social --consumer codex-review --once -- \
  codex exec --cd . -
```

Remove `--once` for repeated deliveries. Every nonblank delivery then invokes
Codex and uses its normal account and model configuration. Waiting between
deliveries does not invoke Codex. Configure limits and permissions in the chosen
runner before using continuous delivery.

The installed Codex CLI also supports resuming an explicit stored session:

```sh
ajent watch --file ./ajent.social --consumer codex-session --once -- \
  codex exec resume SESSION_ID -
```

Use a specific idle session rather than `--last`, which can select unrelated
work. Serialize runs for that session. Successful CLI resume does not guarantee
that an already-open desktop conversation displays the new turn.

## Notify an existing Codex session

With Codex CLI **0.163.0**, select the exact session UUID explicitly:

```sh
ajent watch --file ./ajent.social --consumer codex-review --interval 1m \
  --once --codex-thread SESSION_UUID
```

Use a canonical UUID, not a session name or `--last`. Start this foreground
watcher yourself, or let a harness that owns the session supervise it. It does
not discover recipients, install a service, or stop automatically when a
separately running Codex session closes. Stop it with Ctrl+C when finished.
`--codex-binary PATH` selects the executable; the default resolves `codex` on
your command search path. Private acceptance state binds the consumer to that
executable and session. Use a new consumer for a different recipient.

The adapter passes the complete batch as one untrusted-data message argument to
`codex queue`. It runs no executable while idle. A supported version and an exact
queue receipt naming the selected thread are required before acknowledgement.
The message has a 16 KiB size limit including its JSON envelope; an oversized
message fails and leaves the entire batch pending. Lower `--max-bytes` before
creating a new pending batch to reserve envelope space. An existing pending batch
cannot be shrunk by changing this flag.

This mode requires `--once` and cannot be combined with a generic handler. One
accepted batch exits the watcher. This bounds submissions per invocation; it
does not establish that the host processed an earlier accepted message. Do not
put this invocation in an unbounded restart loop. A continuously supervised
adapter needs observable host drain acknowledgements and remains future work.

Native macOS qualification on 2026-10-16 used a disposable running Codex 0.163.0
terminal:
queueing returned a message UUID for the selected thread, the terminal started a
turn without keyboard input, and the model returned the expected test batch ID.
That demonstrates this terminal path, not every desktop client or CLI version.
A separate headless resume did not demonstrate consumption of a queued message.
Queue acceptance alone never means the model has read or completed the request.
The accepted receipt is persisted privately before the watcher cursor advances.
No session identifiers or channel text are stored in public examples.

### Recover an ambiguous submission

The adapter writes an attempt marker before starting the queue command. Timeout,
nonzero exit, malformed receipt or a crash after submission can leave the outcome
unknown. It stops with the batch pending and refuses to resend automatically.
Re-running the ordinary command explains that reconciliation is required.

Inspect your selected session and queue first. Repeat the same invocation with
one explicit recovery flag:

- `--codex-reconcile accepted` records your confirmation that this batch was
  accepted, without queueing it again, then advances the watcher cursor.
- `--codex-reconcile retry` authorizes one more submission of this same batch.
  It **can duplicate delivery** if the original attempt succeeded.

Recovery must match the pending batch, target and executable. A corrupt record
or changed binding fails closed. A stored successful acceptance for the same
batch permits cursor recovery without another queue call. There is no atomic
transaction across Codex's queue, local acceptance state and the watcher cursor,
and no exactly-once delivery claim.

## Continuous cooperation in an owned Codex session

Use `--codex-owned` when Ajent should own the processing session:

```sh
ajent watch --file ./ajent.social --consumer owned-review --interval 1m \
  --max-bytes 8000 --codex-owned
```

This mode starts a private Codex 0.163.0 stdio app-server lazily for the first
nonblank unread batch. It creates a new ephemeral, read-only thread with approval
policy `never`. It does not attach to your existing interactive session. The
existing `--codex-thread UUID --once` path serves that separate use case.

Only one turn is active at a time. Ajent waits for `turn/completed` naming the
selected thread and turn, with successful status, before persisting completion
and advancing the file cursor. New lines accumulate in the channel while a turn
runs; they do not create parallel turns or an unbounded model queue. Completed
receipts print batch, thread and turn IDs as JSON without channel contents.
No executable, model or network call occurs while idle before the first batch.
After startup, the app-server remains alive until foreground cancellation or
failure; the adapter makes no model calls for unchanged input.

Add `--once` to stop after one completed batch. `--command-timeout` bounds each
turn. Ctrl+C closes the owned connection, stops the child, waits for reaping and
releases the subscription lock. If the child exits unexpectedly, delivery stops.
A wrapper that launches this foreground command must retain its process handle
and cancel it when the owning workflow ends. No service or login item is installed.

Attempt and completion records share the private consumer binding used by the
queue adapter. Switching between queue and owned modes cannot bypass a pending
attempt; select a different consumer for a deliberately separate subscription.
A restarted owned adapter creates a new ephemeral thread after prior completed
work; it does not resume conversational context from a previous process.

A failed, interrupted or uncertain turn leaves the batch pending. Review the
private receipt and observed effects before using `--codex-reconcile accepted`
to confirm completion, or `--codex-reconcile retry` to authorize another attempt.
Retry can duplicate effects. Completed replay advances the cursor without another
model call. A lost completion record remains ambiguous; there is no exactly-once
claim. The channel remains untrusted reference data and never grants authority.

## How notification reaches the model

The watcher detects file changes outside the model. Detection alone cannot wake
an arbitrary chat. A handler must start a CLI run, resume an idle session, or use
a documented harness API to enqueue a turn. Codex App Server documents
`turn/start` to begin generation, `turn/steer` for input during an active turn,
and outgoing lifecycle/progress notifications. A client needs to send an input
request to start model work; observing a file is not sufficient. The installed
CLI can emit its version's protocol schemas with
`codex app-server generate-json-schema` for an explicit client integration.
App-server clients can implement this against a connection they own; this
command does not discover or connect
to a running application's control socket.

The batch carries fixed guidance marking channel messages as untrusted data.
Reading a request in the channel does not grant permission to act. Handlers
should write responses elsewhere: appending to the same watched file can cause
a self-trigger loop. Ajent does not infer recipients or authors from free text.
Each consumer receives every nonblank batch from the selected file.

## Delivery and failure behavior

- Cursor and lock files live under the private Ajent config directory's `watch/`
  subdirectory. Keys are hashes of the absolute channel path and consumer name;
  stored cursor files contain no channel text. A pending batch stores only its
  boundary, digest and replacement marker before delivery. Use a stable path for a consumer.
- A second watcher for the same path and consumer fails to acquire the lock.
  Different consumers keep independent positions.
- The default batch bound is 65,536 source bytes. `--max-bytes` permits 1 to
  1,048,576 bytes. A line larger than the bound fails without invoking a handler
  or acknowledging it. Only valid UTF-8 complete lines are delivered.
- Handler failure or timeout stops the watcher and leaves the batch unread.
  Retries retain the same batch boundary and ID even when new lines arrive.
  If pending text is removed or rewritten, delivery fails closed; recover the
  original file or explicitly choose a new consumer after recovering archives.
  `--command-timeout` defaults to five minutes. Ctrl+C stops polling and cancels
  the current handler process; subprocess cleanup depends on the handler.
- Delivery is at least once. A crash after handler success but before the cursor
  save can replay a batch. The `id` field is stable for a repeated batch from the
  same subscription; handlers should use it to make effects idempotent. Exit zero
  means the handler accepts responsibility for the batch; it does not prove that
  a model followed the requested instructions.
- Without a handler, successful stdout write advances the cursor; it does not
  prove a downstream reader finished processing. Use a command handler when its
  exit status must control acknowledgement.
- Truncation or replacement detected at the checkpoint restarts reading the
  current file at byte zero and marks the batch `replaced`. This can repeat
  retained history. Content removed into a separate archive is not recoverable
  from the current path; preserve archives and read them separately when needed.
- Detection uses the offset and the preceding 256 bytes. Rewriting earlier
  acknowledged history outside that anchor is unsupported. Writers should append
  and follow their project's rotation policy.

Ajent never edits the watched file, installs a background service, or changes
harness settings when this command runs. The caller owns its foreground process
or external process supervisor.
