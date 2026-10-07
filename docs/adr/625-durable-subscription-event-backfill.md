# ADR-625: Durable subscription event backfill

- **Status:** accepted
- **Date:** 2026-10-06
- **Decision:** Allow one resumable retained-event backfill per account and
  ordinary subscription, using an immutable job target, a persisted acceptance
  cursor, and the existing independent recipient delivery machinery.
- **Why:** Preview identifies retained events but cannot deliver them. A safe
  backfill needs durable progress, a fixed cutoff, duplicate behavior, bounded
  admission, and per-target recovery without rewriting acceptance snapshots.

## Scope and contract

`POST /v1/apps/{slug}/event-subscriptions/{subscriptionID}/replays` creates a
job, `GET /v1/event-replays/{jobID}` reads its progress, and
`GET /v1/event-replays/{jobID}/items` lists per-envelope outcomes. The target
must be a current enabled ordinary subscription owned by the app and account.
Work-bound subscriptions are not supported. The job stores the subscription
declaration and revision at creation; later declaration changes do not rewrite
already accepted backfill intent.

The required `from` and `until` values select a half-open range of platform
acceptance time. `cutoff_at` is the lesser of `until` and creation time, so later
acceptances cannot enter the job. A job may cover at most the existing 30-day
settled-receipt retention window. `earliest_retained_at` is reported as a
coverage warning; jobs use surviving retained envelopes and cannot promise a
complete archive.

The only duplicate policy is `skip_existing`. Events captured for this target
in the original immutable recipient snapshot are skipped. Legacy envelopes
without a recipient snapshot are skipped as unknown. A previously materialized
recipient row is skipped, including a row from an earlier backfill. Only
matching events with a captured snapshot, a definitively absent target, no
existing target row, and a settled parent receipt can create backfill work.
Unsettled receipts are skipped to avoid racing the original whole-event router.
Pattern and filter mismatches are recorded as filtered.

Backfill work gets its own job item and lineage on the independent recipient
row. The acceptance-time `recipient_snapshot` is never modified. Settled
snapshot-backed receipts are adopted into independent recipient leases under a
parent-row lock, materializing their original checkpoints before adding the
new target. Receipts containing workflow starts are not adopted. The existing
recipient lease, capacity, invocation-id, retry, and handler lifecycle remain
authoritative. Handler execution is still at least once; handlers must make
side effects idempotent.

The scheduler examines at most 100 envelopes per page and allows no more than
100 pending or processing target deliveries for a job. Its stored tuple cursor
and fixed cutoff make restarts resumable; it waits for routing capacity by
stopping further scanning while that window is full. At most three jobs may be
running for one account, and only one may target a subscription at a time.
Active jobs temporarily protect their acceptance range from normal settled
receipt pruning. Retryable failed deliveries keep their source receipt through
the job's 30-day recovery window; other settled payloads follow ordinary
retention. Per-envelope outcome rows are metadata and remain with the job for
30 days after completion, even after a source payload expires. Unfinished jobs
are never pruned.

## Operational behavior

The job reaches `completed` after scanning is complete and all created routing
items are terminal. It reaches `completed_with_failures` when at least one item
failed, including an invalid retained envelope or a recipient that exhausted
its routing retry budget. Standard event delivery inspection remains tied to
the immutable acceptance snapshot; job status is the source of truth for
backfill outcomes, and per-envelope items preserve job lineage. The status
response distinguishes retryable routing failures. `POST
/v1/event-replays/{jobID}/retry-failed` requeues up to 100 eligible failed
routing recipients with a fresh routing generation. `enqueued` means routing
admitted an invocation, not that the handler completed. Handler retries and
dead letters remain in their existing lifecycle.

The item endpoint orders by acceptance time and a stable internal envelope
identifier, pages with an opaque cursor, and optionally filters by one outcome
state. The cursor is bound to the account, job and state filter. Each item
retains event source, ID, type, optional schema version, routing state, attempt
count and bounded failure details, but never the event payload. This identity
snapshot keeps completed job outcomes inspectable after the source envelope is
pruned. `filtered` outcomes are included alongside queued, failed and skipped
items. Pages are consistent per request, while item states can change between
requests as the job runs. For a complete state-filtered inspection, page the
job after it reaches a terminal state; retrying it can change item states again.

Jobs pin only the retained source rows needed for their bounded acceptance
range while scanning and routing. Once complete, only retryable failed source
receipts stay pinned, and only until the recovery window expires. They do not
create a durable archive, restore events that expired before job creation, or
establish FIFO ordering. Admission uses the existing per-consumer capacity
checks; a full queue pauses the bounded scan rather than materializing an
unbounded delivery queue.

## Storage and rollout

The additive migration creates job and item tables and marks backfill lineage on
independent recipient rows. Production SQL remains generated through sqlc. The
backfill worker runs only in compatible scheduler binaries; deploy the schema
and compatible apid/schedd before exposing creation. It does not enable the
global recipient-claims adoption flag. Existing receipt and snapshot formats
remain readable by older binaries, but old schedulers cannot route a queued
backfill recipient. Keep compatible schedulers running while a job is active.

The Go client includes `CreateEventReplayBackfill`, `GetEventReplayBackfill`,
`ListEventReplayBackfillItems`, and `RetryFailedEventReplayBackfill`. CLI
commands are `gregale events backfill`, `backfill-status`, `backfill-items`,
and `backfill-retry`.
