# ADR-20261105090246: A booted wake answers before its bookkeeping

- Status: proposed. Built behind schedd's `async_wake_finalize` switch
  (`FAAS_ASYNC_WAKE_FINALIZE`), off by default.
- Date: 2026-11-05
- Related: ADR-097 (wake phases), ADR-1741 (owner cache, one guarded write
  per wake), ADR-051 (characterization), ADR-590 (secret delivery fences),
  ADR-1151 (publication bookkeeping), the owner rule that control-plane
  Postgres stays off the request path

## Context

A gateway wake that boots an instance waits for schedd's whole post-vmmd
window before it forwards the first request (`schedulerComplete` comes after
`wake.boot_completed`). That window has two parts:

- the RUNNING publication: the compare-and-swap that makes the instance
  routable, with its transition event and `instance_changed` notification in
  the same transaction;
- the finalize step after it: the observed workload class of a cold boot
  (`RecordObservedWorkloadClass` and an `app.characterized` audit row) and
  the secret delivery record (`RecordAppSecretDelivery`, a transaction of
  five statements plus one update per delivered secret, and a
  `secret.delivery_succeeded` audit row).

Production (rc.322, 48 h to 2026-11-05, `wake.boot_completed`):

| gateway boots | n | finalize p50 | finalize p90 |
|---|---|---|---|
| restore | 86 | 32 ms | 113 ms |
| cold boot | 8 | 91 ms | 109 ms |

Wakes for apps without secrets (most invocation and mesh restores) finalize
in 0 ms. From a compute node each statement crosses to the control plane
(~3 ms round trip) and commits with `synchronous_commit=on` on a saturated
2-vCPU host, so the finalize step costs as much as the publication's tail.

Nothing the gateway does with its answer depends on these records. The
instance is published and routable, its secrets are already inside the guest,
and both records are best-effort today: a failed write is logged and never
fails the wake.

## Decision

1. With `async_wake_finalize` on, a booted wake answers as soon as its RUNNING
   publication commits and its notification and reconcile are sent. The
   characterization record and the secret delivery record, with their audit
   rows, run in the background on a context detached from the wake and
   bounded by `wakeFinalizeTimeout` (10 s).
2. Who writes what does not change: schedd still writes both records, through
   the same store methods, fences and audit rows. The `instances` row, the
   state machine and the publication are untouched; only the order of the
   answer and the bookkeeping changes.
3. The bookkeeping keeps its order (class, then secret delivery). Once it is
   handed off, the wake counts as delivered: the deferred
   `runtime_start_failed` record does not run for it.
4. schedd waits for running bookkeeping on shutdown, within its 5 s stop
   budget, so a clean stop keeps the records (`Engine.WaitWakeFinalize`).
5. Off, the wake records both before it answers, as before.

Thaws, warm-pool promotions, prime boots and environment qualification keep
their bookkeeping inline; they are not on the gateway's measured path.

## Consequences

- A gateway restore of an app with secrets no longer waits for the secret
  delivery transaction: on production numbers about 32 ms at p50 and 113 ms
  at p90 per wake, more for cold boots. `post_rpc_finalize_ms` then reports
  only the inline part.
- For a few milliseconds after a wake answers, an app's secrets can still
  read as `pending` and its class as the scan hint. A reader that polls
  delivery status right after the first response may see the old value once;
  the next read sees the record.
- An instance that is retired before its record runs is recorded as
  delivered only if it is still active (the record's fence requires it), as
  for a record that lost a race with a park before. A schedd crash between
  the answer and the record loses the record, as a crash between the
  publication and the record did; the next boot records again.
- Records of one app's concurrent wakes are no longer serialized by schedd's
  per-app lock; Postgres orders them with the rows they lock.

## Rollout

1. This ADR and the switch, off by default.
2. Turn `async_wake_finalize` on for production-us schedd, compare
   `post_rpc_finalize_ms`, the gateway's `scheduler_wake` and secret delivery
   statuses for a day, then make it the default.
