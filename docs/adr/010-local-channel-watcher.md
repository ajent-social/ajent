# ADR 010: Local unread-channel delivery

Status: Accepted. Delivery qualification is recorded in the local channel-watch plan.

## Context

Polling a project channel through repeated model turns spends model context on
empty checks. Local file observation can happen in a normal process, with an
agent invoked only when unread lines are available. Existing harness integrations
provide session hooks but no universal mechanism for injecting a new turn into
an arbitrary active conversation.

## Decision

The owner explicitly requested token-efficient agent watching instead of repeated
manual channel reads. This authorizes an opt-in local watcher as a scoped
exception to the standing no-notifier guidance. It does not authorize automatic
installation, discovery of other sessions, or unsolicited agent wake-ups.

Add an explicit foreground `ajent watch` command. It polls an explicitly supplied
file without network or model calls, emits bounded JSON batches, and optionally
invokes a caller-selected executable with the batch on stdin. It never evaluates
file content or reads execution configuration from the project channel.

Keep private per-path/per-consumer checkpoints and cross-process advisory locks
using existing config primitives. Acknowledge only successful delivery; stop on
handler failures rather than invoking an automatic retry loop. Treat rotation as
a restart of the current file and document archive recovery and possible replay.
The complete delivery and CLI behavior are in [channel-watch.md](../channel-watch.md).

Use portable standard-library polling rather than introducing a filesystem
watching dependency. Idle polls read bounded bytes and do not invoke a model.
Use existing `x/sys`-backed config locks across supported platforms.

## Consequences

The feature works with different runners without claiming universal push support.
An explicit handler is responsible for model invocation, session concurrency,
effect permissions and successful processing. No background service, account,
model choice or harness integration is activated automatically. Continuous
model delivery is opt-in and may cause a feedback loop if its handler appends to
the watched file; the fixed envelope guidance and documentation address that
routing responsibility.
