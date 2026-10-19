# ADR-1421 · Node deadline count watches

Date: 2026-10-16
Status: Accepted

## Context

Node exposes the complete deadline-window read, but sustained count alert helpers
and recovery boundaries are available only to Go clients and the CLI.

## Decision

Add a deadline-only Node watch over the existing read. Support independent total
and known-reason warnings/recovery, fresh advancing observations, local monotonic
confirmation durations, progress callbacks and cooperative cancellation. Require
an expected application UUID as well as slug and subscription UUID. Validate full
coverage, scope, exact partitions, control metadata and timestamps before counting
any observation. Preserve sub-millisecond timestamp ordering.

Use a distinct versioned Node checkpoint format rather than accepting broader Go
checkpoints containing unsupported metrics. Bind resume to effective target/policy
and discard timer/sample proof. Keep active incidents unknown until fresh warning
or recovery confirmation. Retain opened/resolved notifications in a bounded outbox
with closing reservations. Unknown evidence is a progress diagnostic. Validate exact
acknowledgment pairs atomically, retain bounded idempotency receipts and require
opening acknowledgment before or together with closing acknowledgment.

Callbacks run serially with detached state and receive an abort signal. Applications
own atomic persistence, notification delivery and downstream deduplication; the SDK
sends no external notifications. Fail callback execution with a recoverable result
snapshot. Use existing SDK authentication and no additional API or schema changes.

## Consequences

Node consumers can monitor full retained cohorts without implementing polling and
proof logic themselves. Queue and sequence exhaustion remain explicit, never silent
loss of opening notifications. Durable notification delivery is at least once and
requires persistence before publishing. Checkpoints do not transfer to Go/CLI, and
Python watch parity remains separate. This change does not extend retention, replay
work or turn count recovery into proof of business completion.

See [Node configuration and limitations](../event-deadline-node-watch.md).
