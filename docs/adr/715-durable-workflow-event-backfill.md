# ADR-715 · Durable workflow event backfill

- **Status:** accepted
- **Date:** 2026-10-08
- **Decision:** Add a durable, workflow-scoped backfill job that can admit a currently defined event-triggered workflow for retained events whose immutable acceptance snapshot definitively excluded it.
- **Why:** The workflow replay preview exposed events outside a workflow's original captured membership but could not safely act on them. Operators need bounded admission, a pinned definition, durable progress, and deduplication that survives workflow-run pruning.
- **Consequences:** The API and `gregale events workflow-backfill` create jobs in the existing replay-job framework. Jobs pin the current eligible workflow recipient at creation, scan platform acceptance time with a fixed cutoff, and admit matching runs independently. Workflow admission receipts remain the dedupe record across jobs and run retention. Existing workflow routing and run lifecycle remain unchanged.
- **Rejected alternatives:** Rewriting the original recipient snapshot would falsify acceptance-time routing. Reusing application subscription backfill would create handler-delivery rows and mix two consumer types. Recomputing a workflow definition on every retry could change historical side effects after deployment.

## Contract

`POST /v1/apps/{slug}/workflow-event-replays` requires `workflow_name`, `from`,
and `until`. The workflow must be enabled in the app's preferred live default
deployment and pass current account/app eligibility checks. The job snapshots
the workflow definition and trigger, uses a half-open range of platform
acceptance time, caps its exclusive cutoff at creation time, and limits the
range to 30 days.

The worker scans at most 100 retained envelopes per page. It admits only an
event that matches the captured trigger, has an immutable non-null recipient
snapshot, is in delivered state, and does not include the stable app-workflow
recipient ID. Unknown legacy snapshots, captured membership, unsettled
receipts, mismatches, and an existing workflow admission receipt are recorded
as skips. The event's acceptance snapshot and ordinary recipient tables are
not changed.

Admission calls the existing transactional workflow admission path with the
job's pinned recipient definition. The unique
`workflow_event_receipts(outbox_id, recipient_id)` record deduplicates admission
across workflow backfills and remains authoritative after the linked run is
pruned. `enqueued` means a run was admitted, not completed. Workflow quota and
temporary target errors are retryable through the durable job's bounded retry
operation; invalid or permanently unavailable targets remain terminal.

At most three replay jobs may run per account, and only one may be active for
the same app workflow. Active ranges are protected by existing settled-event
pruning. Backfill coverage is limited to retained events and is not a complete
archive. Progress and item reads never return event payloads or captured
workflow definitions. Subscription backfill behavior is unchanged.

## Validation

Qualification covers workflow target selection and pinning, known versus
unknown/captured membership, settled-state checks, trigger matching, durable
admission deduplication across jobs, independent job progress, retryable run
quota failures, item run links while retained, and unchanged event snapshots.
