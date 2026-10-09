# ADR-797: Durable bulk event recovery

- **Status:** accepted
- **Date:** 2026-10-08
- **Decision:** Add account-scoped durable jobs that reopen a frozen selection of failed application event routing recipients, with a read-only preview, rate budgets, item outcomes, and cancellation.
- **Why:** Single-recipient and immediate batch replay are insufficient for controlled recovery after a consumer outage.
- **Consequences:** Jobs capture at most 10,000 retained failures, reuse the existing transactional recipient replay transition, and preserve successful sibling outcomes. At most three active jobs per account run for up to 24 hours. Terminal job metadata is retained for 30 days. A preview reports at most 100 samples and a bounded match count; creation selects current state independently.
- **Rejected alternatives:** Re-publishing envelopes could widen recipients or repeat successful consumers. Re-scanning live failures could loop over new failures indefinitely. A detached process or client-side loop would lose cancellation and progress on restart.

## Selection and processing

Exact filters cover the captured subscription, source, type, failure code, and
minimum failure age. Default selection requires the persisted retryable bit;
non-retryable failures require explicit inclusion. Empty selections complete
immediately and oversized selections are rejected atomically.

Within a job, processing follows captured routing order and snapshot positions
so an older selected lane recipient is reopened before its younger recipients.

A selected item's failure identity consists of routing state, attempt count,
update timestamp, failure classification, and retryable bit. Workers compare
this identity under the receipt lock before reopening routing. Changed, pruned,
or unavailable targets produce item-level skips. This protects new failure
generations from an older job. Job metadata contains neither envelopes nor raw
error messages.

The lock order is recovery job, receipt, routing/history rows. Each item replay
and its queued outcome commit in one transaction. Cancellation locks the same
job and marks only remaining items cancelled. Overlapping jobs skip items already
changed by a committed replay; active jobs do not pin receipt retention.

A persisted per-job one-second budget limits processing to 1–100 recipients
(default 10), including skips. Workers take job locks with `SKIP LOCKED`; delays
reset the window instead of accumulating catch-up bursts. Scheduler sweep batch
limits and existing account delivery capacity still apply.

## Coverage

The first contract is `captured_application_recipients`: terminal routing
failures from publication snapshots. It excludes historical backfill targets,
workflow targets, and post-admission execution failures, which have existing
recovery contracts. Replay keeps the original envelope identity and snapshot,
uses existing ordering/deduplication behavior, and cannot reverse already
completed younger deliveries. Queued and completed job counts describe recovery
admission, not eventual invocation success.
