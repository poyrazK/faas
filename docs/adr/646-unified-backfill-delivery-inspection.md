# ADR-646: Unified backfill delivery inspection

- **Status:** accepted
- **Date:** 2026-10-07
- **Decision:** Include historical backfill consumers in event receipt inspection
  and support their existing attempt, handler replay, and selective routing
  recovery surfaces without changing acceptance membership.
- **Why:** A completed backfill job proves routing settled, while its handlers
  can still be pending, failed, or dead-lettered. Operators need the same
  delivery evidence and independent recovery available for original consumers.

## Contract

`GET /v1/events/receipt` and `gregale events inspect` include captured consumers
followed by added backfill consumers. `recipient_count` and `routing_summary`
continue to describe the immutable acceptance snapshot. The optional
`backfill_recipient_count` and `backfill_routing_summary` describe additional
consumers across all pages. Each recipient identifies its `origin` as
`acceptance` or `backfill`. A retained originating job has `backfill_job_id`
and, when its target is currently available, `backfill_job_url`.

Backfill recipients use the existing execution, cancellation, dead-letter,
trusted replay lineage, handler attempt history, routing history, and recovery
action projections. Original handler failures remain visible after successful
recovery. Account ownership and acceptance-time provenance checks apply to
backfill deliveries as they do to captured deliveries. Copied guest headers
are not delivery provenance. Neither job completion nor routing `enqueued`
asserts handler completion.

The existing selective routing replay action can reset one retryable failed
backfill recipient, including while other events in its job remain pending.
It locks the originating job before the parent receipt and recipient, matching
the backfill worker. It rechecks ownership, identity, job state, item state,
and retry eligibility, starts one new routing generation, resets the job item,
and reopens the receipt atomically. Concurrent requests cannot reset the same
failure twice. Reopening a completed job observes the existing active job and
target limits. Batch backfill retry retains its existing completed-job rule.
Recovery is recorded as `operator_replay` so receipt replay counts and retained
failure history include it. Original-consumer batch recovery remains scoped
to acceptance recipients; backfill jobs retain their own batch retry endpoint.

Backfill item responses add `receipt_url` while the original outbox identity
is retained, and `attempt_history_url` when consumer provenance and current
app ownership permit the read. Items retain their bounded metadata after the
source expires, but omit these links rather than pointing at a newly accepted
event that reuses the same source and ID.

## Storage and compatibility

An additive nullable `receipt_position` column identifies added consumers and
stores stable receipt positions independently of mutable delivery timestamps
or job retention. Original recipients retain snapshot ordinality. Existing
added rows, including those whose job foreign key was cleared by pruning, are
assigned positions after the snapshot during migration. A partial unique index
enforces one added consumer per position. New positions are allocated under
the already-held parent receipt lock and append after the highest retained
position. Job pruning clears only the job link; origin and positions survive.

Deploy the migration before binaries using the new queries and keep backfill
writers on compatible binaries when exposing unified inspection. Old writers
do not assign positions. Rolling back inspection removes its positions; a
subsequent migration reconstructs them and clients must restart pagination.
These positions describe inspection order, not FIFO delivery. Acceptance
snapshots, invocation deduplication, retention periods, at-least-once execution,
and the global recipient-claims rollout flag are unchanged.

## Validation

PostgreSQL lifecycle coverage exercises creation, scanning, admission, handler
failure and trusted recovery, repeated-job deduplication, batch and selective
routing retry, concurrent retry fencing, acceptance counts, stable pagination,
job pruning, migration of retained rows without jobs, source identity reuse,
and current app ownership. HTTP coverage follows generated inspection and
recovery links; CLI and generated Node/Python contracts cover the additive
fields.
