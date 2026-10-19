# FaasCustomerOperations

Alerts for Customer Operations ([ADR-1285](../adr/1285-customer-operations-telemetry.md)).
Both are warnings. schedd reads fleet-wide gauges from Postgres in its 60-second
retention tick; every schedd reports the same values, so rules and panels read
`max()`.

Dashboard: **faas Customer Operations (ADR-1285)**
(`deploy/grafana/customer-operations.json`).

```sh
curl -fsS http://127.0.0.1:9103/metrics | grep -E '^schedd_customer_operations'
journalctl -u faas-schedd --since '30 minutes ago' --no-pager | grep -E 'operation (deadline sweep|telemetry)|deadline breach'
```

If `schedd_customer_operations_telemetry_refreshed_timestamp_seconds` is more
than a few minutes old, the gauges are stale: the retention tick is not running
or its Postgres reads fail (`operation telemetry refresh failed` in the schedd
log). Fix that first; the alerts below read the last snapshot.

## FaasCustomerOperationDeadlinesOverdue

Active Customer Operations (`accepted`, `running` or `waiting`) stayed more than
three minutes past their deadline for ten minutes. A deadline is enforced by
two sweeps in the same schedd tick:

- workflow Operations by the Operation deadline sweep
  ([ADR-1196](../adr/1196-operation-deadlines-and-budgets.md)), logged as
  `operation deadline sweep`;
- HTTP Operations by the invocation deadline reaper, logged as
  `invocations deadline breach`.

Find the overdue Operations and their execution kind:

```sql
SELECT id, account_id, execution_kind, state, deadline_at, now() - deadline_at AS overdue
FROM customer_operations
WHERE state IN ('accepted', 'running', 'waiting') AND deadline_at < now() - interval '3 minutes'
ORDER BY deadline_at
LIMIT 20;
```

- **All workflow:** the deadline sweep is failing. Look for an error from
  `SettleOperationDeadlines` in the schedd log; a run whose lock is held by a
  stuck scheduler pass is retried every tick.
- **All HTTP:** the invocation deadline reaper is failing or the invocation is
  claimed by a stuck drain. Check the invocation's state and claim.
- **Nothing in the log at all:** the retention loop is not running on any
  schedd. Check that schedd is up and its loop ticker is advancing.

Overdue Operations settle on their own once the sweep runs again; no manual
change is needed.

## FaasCustomerOperationOutcomesUnknown

Three or more accounts had an Operation attempt end with an unknown outcome
(`requires_reconciliation`, `execution_outcome_unknown`) in fifteen minutes. An
unknown outcome means Gregale lost track of an attempt that may have taken
effect: the instance crashed, its lease expired, the node was lost, or the
attempt was cut by a deadline or budget. One app timing out produces this for
one account; several accounts at once point at the platform.

```sql
SELECT o.account_id, o.app_id, o.execution_kind, count(*) AS unknown, max(e.created_at) AS latest
FROM customer_operation_events e
JOIN customer_operations o ON o.id = e.operation_id
WHERE e.event_type = 'reconciliation_required' AND e.data ->> 'failure_code' = 'execution_outcome_unknown'
  AND e.created_at > now() - interval '15 minutes'
GROUP BY 1, 2, 3
ORDER BY unknown DESC;
```

- **Same node:** correlate with instance crashes and node health
  (`FaasDaemonDown`, `FaasComputeNodeFlapping`). A lost node strands every
  attempt it held.
- **Same deployment wave:** a release that restarts schedd or vmmd mid-attempt
  interrupts running steps.
- **Spread out, with deadlines or budgets:** limits cut attempts by design;
  check whether the accounts declared tight `deadline_seconds` or
  `max_execution_seconds`.

Customers resolve each Operation through recovery
(`gregale customer-operations recover`), after checking whether the effect
happened. Do not change Operation state by hand.

## FaasCustomerOperationQueueNotDraining

An Operation held by a per-customer concurrency policy
([ADR-20261018210500](../adr/20261018210500-per-customer-operation-concurrency.md))
has waited more than thirty minutes and apid started no queued Operation in
that time. apid reports `apid_customer_operation_queued`,
`apid_customer_operation_queue_oldest_seconds` and
`apid_customer_operation_queue_releases_total`; every replica reports the same
queue, so rules read `max()`.

```sql
SELECT q.account_id, q.app_id, q.scope, q.operation, q.platform_tenant_id,
       count(*) AS queued, min(q.queued_at) AS oldest,
       (SELECT count(*) FROM customer_operations o
          JOIN customer_operation_definitions d ON d.id = o.definition_id
         WHERE o.platform_tenant_id = q.platform_tenant_id AND o.app_id = q.app_id
           AND d.scope = q.scope AND d.name = q.operation
           AND o.state IN ('accepted','running','requires_reconciliation')
           AND NOT EXISTS (SELECT 1 FROM customer_operation_queue h WHERE h.operation_id = o.id)) AS in_flight
FROM customer_operation_queue q
GROUP BY 1, 2, 3, 4, 5
ORDER BY oldest;
```

- **`in_flight` at the customer's limit:** the queue is working as configured;
  the customer's earlier Operations are long-running. Check the policy with the
  account (`GET /v1/apps/{slug}/operation-concurrency`) before changing it.
- **`in_flight` below the limit:** the releaser is not running. Look for
  `operation queue release failed` or `operation queue stats failed` in the apid
  log, and check that apid is up; the releaser runs in every replica.
- **An Operation stuck `accepted` and never dispatched after release:** its
  invocation or workflow run is due but not claimed; follow the scheduler's
  dispatch alerts.
