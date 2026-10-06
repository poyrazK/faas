# ADR-611 · Invocation-backed event delivery attempt history

- **Status:** accepted; local qualification passed
- **Date:** 2026-10-05
- **Decision:** Record async invocation claims and their trusted child replays
  in an indexed attempt ledger, atomically with execution transitions. Expose
  bounded, account-scoped history for each captured event recipient.
- **Why:** Mutable invocation attempts and last errors describe the current
  delivery budget. Retries replace errors and in-place replay resets counters,
  leaving operators unable to explain the delivery that preceded recovery.
- **Consequences:** Every recorded claim retains its invocation, attempt and
  replay-generation identity. Finished outcomes are immutable. History reads
  include surviving child replays without changing the original receipt.

## Recording and evidence

An additive migration installs `invocation_attempt_history` and an invocation
transition trigger. It records only `async_invoke` and `replay` sources, covering
published-event handlers and sharing storage behavior with other async request
invocations. Recording is part of the transaction that owns the execution row;
failed claims, quota rejection, pre-claim deferral, rollback and delivery after
an advisory wake cannot invent a committed attempt. No handler payload, result
or headers are copied into the attempt ledger.

The identity `(invocation_id, replay_generation, attempt)` is unique. A claim
starts as `running`. Completion, retry, permanent failure, dead letter and
cancellation settle that row, with timestamps, a bounded error detail and the
next retry deadline when applicable. Reclaiming an expired dispatch records
`unknown`: owning a lease does not prove that the handler ran, and losing it
does not prove that side effects failed. Closed rows are never overwritten by
later replay or delivery. Pending work rejected without a claim has no attempt.

The memory store mirrors the trigger through its invocation write boundary.
Existing invocation and recipient retry budgets are unchanged. Broker receipts,
jobs, workflows and webhook-specific attempt ledgers keep their existing paths.

## Settlement fencing

Async/replay scheduler completion and failure carry both the captured attempt
and replay generation. The PostgreSQL store validates the still-live lease
after acquiring its execution-row lock, including time spent waiting for that
lock; memory validates it under its mutex. This rejects results from an earlier
retry and from a previous replay generation whose attempt number has been
reused. Terminal redelivery remains a no-op. Keyed lane, fairness, destination
and quota transitions retain their
existing transactions and lock order.

The legacy completion interfaces remain available for existing callers. All
schedulers must be upgraded before relying on the new settlement fence;
old scheduler binaries can still call those interfaces. This protects Gregale's
ledger, not external application side effects, which still need idempotency.

## Reads and retention

`GET /v1/events/receipt/attempts?source=SOURCE&id=ID&subscription_id=SUB`
requires the receipt's existing read scope, MFA and rate limits. It checks the
owned acceptance, captured recipient and current app ownership in a repeatable
snapshot. Ledger-owned root lineage and root creation time bind attempts to
that acceptance; copied guest headers cannot attach unrelated invocations.
Deleting an intermediate parent does not sever surviving descendant history.

Pages contain at most 200 attempts, newest history ID first. The opaque cursor
binds account, source, event ID, subscription and outbox acceptance ID; it is
separate from recipient and replay cursors. It remains usable if its attempt is
pruned. New claims do not move existing entries, while a running outcome can
settle between reads. Receipt recipients link this history; the Go client and
`gregale events attempts` expose it directly.

Closed attempts expire at the earlier of invocation result retention and
30 days after settlement. The existing invocation retention tick prunes a
bounded page through `SKIP LOCKED`. Running attempts are not pruned by this
sweep. Invocation deletion cascades its own attempts; descendants have their
own rows and lifetimes. Receipt retention remains independent.

History is not backfilled: an attempt already dispatching during upgrade has
no fabricated start time or outcome. Responses declare
`coverage=recorded_attempts_only`; absence does not prove no delivery or replay
ever happened. Rollback drops the trigger and ledger, losing this evidence but
not changing invocation state, lineage or retry budgets.

## Qualification

Local qualification passed with PostgreSQL 16.15 and Go 1.25.13. PostgreSQL
and memory cases cover retry/replay identity, concurrent claim admission, lease
loss, stale completion and failure, isolated consumers, trusted lineage, stable
pagination, current ownership, bounded retention, source deletion and
transactional rollback. A PostgreSQL lock-wait case verifies that an expired
lease cannot settle after acquiring a previously blocked execution-row lock.

Migration upgrade/replay and static migration gates passed. Fresh database
schema output matches the committed schema; sqlc 1.31.1 parity and SDK route
coverage passed. The full portable state and API client suites, receipt API and
OpenAPI compliance checks, and CLI command/help/completion regressions passed.
The generated CLI reference was refreshed and its freshness gate passed.

The complete scheduler test package could not compile under local resource
pressure. Focused drain and invocation-retention cases passed using the normal
production source files and required test helpers, with test-package inlining
disabled. These include dispatch history, the retention tick, stale scheduler
settlement and compact/canonical memory IDs. Golangci-lint 2.4.0 reports no new
issues in changed production code; ADR numbering and diff checks passed.
Full Linux CI and staging restart/recovery qualification remain release gates.
