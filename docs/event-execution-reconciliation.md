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
execution sources and terminal invocations still retained for replay. This
operation never guesses ownership, deletes evidence, reruns handlers, or repairs
historical outcomes. A busy app with unrelated retained executions may remain
blocked indefinitely.

`collection_started_at` becomes available when reconciliation completes or an
existing baseline is already known. An execution-health window must start at or
after this boundary before it can be complete. Loss markers, unknown outcomes,
retention, sample requirements and fresh observations still govern SLO eligibility.
For a five-minute health window, allow at least five minutes after the boundary
before checking eligibility. Reconciliation does not claim exactly-once delivery
or complete historical evidence.
