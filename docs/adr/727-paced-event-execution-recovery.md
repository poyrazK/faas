# ADR-727: Paced event execution recovery

- **Status:** accepted
- **Date:** 2026-10-08
- **Decision:** Extend durable event recovery jobs with explicit execution mode, reusing existing replay admission transactions.
- **Why:** Routing recovery cannot repair handlers that failed after admission. Operators need a bounded, cancellable recovery path across several consumers.

## Selection and coverage

The existing preview/create/job/items/cancel API and CLI accept `mode: execution`
(`--mode execution`). Omitted mode remains routing recovery. Execution filters
are exact subscription ID, event source, event type, minimum failure age, and
optional `outcome: failed|dead_letter`; omitting outcome selects both.
Routing-only `failure_code` and `include_non_retryable` are rejected in execution
mode. Items expose the selected failure's `invocation_id`; `failure_code` is its
terminal state and `retryable` denotes replay eligibility at selection time.

Use retained enqueued publication recipients and materialized backfill recipients,
excluding workflows, object notifications, cancellation-only commands, other
accounts/apps, environment work, and customer operations. Compute the stable
original invocation ID from the envelope and consumer. Select the newest retained
execution generation in its root/replay lineage, using in-place replay time
before creation time, before filtering terminal states, so a
successful or active child prevents selection of an older failed parent. Replay
identity ledgers exclude parents whose child was pruned. Failed bound queue work
uses its queue-specific recovery surface; dead letters use the unified ledger.
Expired keyed work is not selected.

Scan receipt roots in bounded keyset pages up to a frozen outbox high-water mark;
stop once the 10,001st eligible item proves that the job limit is exceeded.
Request timeouts bound broad scans. Preview uses a read-only repeatable-read
transaction and returns up to 100 samples. Creation freezes its own observations;
preview is advisory. Coverage is `retained_application_executions`, never a
complete historical census: missing receipts, executions, or legacy lineage
cannot be reconstructed. No payloads, work keys, or raw handler errors appear in
job metadata.

## Processing and atomicity

Persist mode in the existing job selection JSON and the selected invocation's
ID, state, attempts, replay generation, creation/completion times, and dead-letter
ID in the existing item checkpoint JSON. Existing tables, clone exclusions,
quotas (three active jobs/account), 10,000-item bound, 1–100 items/second budget,
24-hour job lifetime, and 30-day metadata retention are shared across modes.
No columns or tables are added. An index-only migration supports account-scoped
legacy replay-parent lookup without repeated invocation table scans.

A worker holds the job lock, then the work lane before the invocation and app,
then the dead-letter ledger when applicable. Recheck ownership, active app,
retained receipt, failure identity, replay child ledgers, and absolute work/start
deadlines. Changed or missing executions become `skipped: changed`, pruned
receipts `receipt_expired`, deleted apps or rejected suspended-tenant child
admissions `target_unavailable`, and
expired work `expired`. Failure identity changes invalidate the item even when a
later generation fails again. New failures are outside the frozen selection.

Plain and keyed failed work reuse transaction helpers extracted from their
existing replay APIs, including child deduplication, lane sequencing, and event
capacity admission. Plain/keyed children preserve captured headers, retry policy,
and original execution/result durations; absolute work/start deadlines remain
unchanged. Bulk recovery does not resolve a new deployment version or refresh a
captured version pin. Dead letters reuse the unified in-place replay, preserving
identity and incrementing generation; its existing tenant claim gate still
applies after replay. This is an explicit operator retry; it
does not guarantee handler idempotency or undo completed effects.

A savepoint rolls back item-level admission side effects on skips or capacity
waits. Replay admission and the item result commit in one outer transaction.
Capacity waits leave the item pending, spend a pacing permit, and retry after
five seconds; a blocked head delays that job until capacity or job expiry.
Unexpected errors roll back the transaction and keep the item pending. The
in-memory store mirrors selection and replay under its mutex.

Successful sibling consumers remain untouched. Keyed ordering follows the
existing replay path; recovery cannot reverse younger completed work. Routing
pause only controls new routing and does not block explicit execution recovery.
Cancellation waits for an in-flight item transaction; admitted replays continue.
`queued` means replay admission succeeded, and `completed` means the selection
was queued/skipped, not that handlers succeeded. Existing read/deploy scopes,
MFA, creation idempotency, and account ownership checks apply unchanged.
