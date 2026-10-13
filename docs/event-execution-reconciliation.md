# Reconcile existing event consumer execution coverage

Consumers created before the execution-outcome ledger can report
`ledger_baseline_unknown`. Request a new forward-only collection boundary:

```sh
gregale events subscription-reconcile-execution APP SUBSCRIPTION_ID
gregale --json events subscription-execution-reconciliation APP SUBSCRIPTION_ID
```

The matching API resource is
`/v1/apps/{slug}/event-subscriptions/{subscriptionID}/execution-reconciliation`:
POST requests reconciliation and GET reads its status. POST requires deploy/write
scope and GET requires read scope. Go exposes `RequestEventExecutionReconciliation`
and `GetEventExecutionReconciliation`; Node and Python expose the generated event
service operations from the same OpenAPI contract.

Statuses are `not_requested`, `waiting_for_lineage`, `completed`, and
`not_required`. A waiting POST returns 202. Repeating the request preserves its
original timestamp. The scheduler processes ready work on its retention cadence;
restarts resume the saved request.

`unattributed_execution_present` means an app invocation lacks authoritative
ledger lineage. Such executions block completion, including unrelated app
execution sources and terminal invocations still retained for replay. The scheduler now attempts bounded authoritative lineage repair before testing
completion. A retained enqueued receipt must uniquely match the deterministic
delivery ID, root incarnation, account/app, normal async invocation shape and
event content. Missing, incompatible or competing evidence remains unresolved.
It never guesses ownership from headers, deletes evidence, reruns handlers, or
reconstructs historical outcomes. A busy app with unrelated retained executions may remain
blocked indefinitely.

`collection_started_at` becomes available when reconciliation completes or an
existing baseline is already known. An execution-health window must start at or
after this boundary before it can be complete. Loss markers, unknown outcomes,
retention, sample requirements and fresh observations still govern SLO eligibility.
For a five-minute health window, allow at least five minutes after the boundary
before checking eligibility. Reconciliation does not claim exactly-once delivery
or complete historical evidence.

## Repair progress

The same API, CLI and SDK response reports `repair_scanned`, `repair_repaired`,
`repair_unresolved`, and `repair_conflicts`. These are cumulative examined-receipt
counts for the request, not current execution totals or live blocker counts.
Existing matching root associations are scanned but not counted as newly repaired.
Use `unattributed_execution_present` and `status` to determine current readiness.

The durable cursor and counters commit with root associations. Failed database
transactions do not advance the cursor, and restarts resume the same scan.
A scheduler pass examines at most 100 receipt candidates across all repair jobs.
Jobs rotate by their last scan so an unresolved app does not monopolize repair.
Scans move forward through retained enqueued receipts; absent or ambiguous
historical evidence is not inferred or automatically rewritten on a later retry.
Normal new admissions continue associating their own roots transactionally.

Repair records lineage only. It does not backfill prior attempt/completion
outcomes, clear loss markers or establish historical completeness. Mapped active
roots and their replay descendants record subsequent outcomes through the existing
ledger transitions. Reconciliation still establishes a new forward-only boundary
before future SLO windows can qualify.

## Inspect and retry individual cases

```sh
gregale --json events subscription-lineage-cases APP SUBSCRIPTION_ID --limit 50
# Continue with the returned next_after value:
gregale --json events subscription-lineage-cases APP SUBSCRIPTION_ID --after 123
# Review a case, then choose a new UUID for this deliberate retry:
gregale --json events subscription-lineage-retry APP SUBSCRIPTION_ID OUTBOX_ID --request-id UUID --expected-revision REVISION
```

The API uses GET on `execution-reconciliation/cases` and POST on
`execution-reconciliation/cases/{outboxID}/retry`. Retry bodies contain
`request_id` and `expected_revision`. GET requires read scope, POST deploy/write
scope. Go exposes `ListEventExecutionLineageCases` and
`RetryEventExecutionLineageCase`; Node and Python expose matching generated event
service operations.

Case reasons distinguish invalid or missing receipts/membership, missing roots,
incompatible executions, mismatched event content, competing acceptances,
conflicting lineage and changed root incarnations. Only codes and identifiers are
returned. Cases describe their last recorded evaluation; they are not current
execution status, and earlier aggregate-only scans have no individual records.
Listings cover retained recorded cases and can change between pages.

Reuse the same request UUID **and** expected revision when a retry response is
lost. The server returns its saved result without re-evaluating or incrementing
retry counts. A stale revision or UUID reused with another guard returns 409.
Refresh the case list before deliberately choosing a new request UUID and guard.
The original receipt acceptance and any known root incarnation stay fenced.

Cases and request results are retained for at least 30 days after evaluation or
retry, independently of source receipt retention. Expired cases return 404.
Expired request results can no longer be replayed, but stale revisions prevent
repeating the old evaluation. A successful retry records lineage only: it never
reruns a handler, resets the completed scan cursor, backfills historical outcomes
or changes an established coverage boundary. Scan counters remain cumulative
first-scan evidence; use case revision and retry count for explicit retries.
