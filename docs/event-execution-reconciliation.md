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
