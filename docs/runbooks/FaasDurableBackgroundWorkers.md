# Durable background workers degraded

This alert means a durable PostgreSQL worker cannot claim work or refresh its derived state. The affected operation label identifies the customer impact:

## Symptoms

- `managed_realtime_drain_claim`: connection-drain operations may remain `running` and matching sockets may stay open.
- `job_materialization_claim`: pending job images missed by the notification fast path cannot recover, so those jobs cannot dispatch.
- `usage_daily_rollup`: daily usage and daily-cost alerts are stale; raw `usage_minutes` and provider billing remain authoritative.

## Triage

1. Confirm the emitting daemon is healthy and inspect its logs for the full PostgreSQL error.
2. Check the matching operation series and its success series:

   ```promql
   {__name__=~"apid_ops_total|imaged_ops_total|meterd_ops_total",op=~"managed_realtime_drain_claim|job_materialization_claim|usage_daily_rollup"}
   ```

3. Verify the durable backlog directly: running rows in `managed_realtime_drain_operations`, pending rows in `jobs`, or stale/missing rows in `usage_daily` compared with `usage_minutes`.
4. If a release introduced invalid SQL, roll back or deploy the corrected query. Do not delete durable rows to clear the alert.

## Recovery

- Realtime drains recover on apid's one-second claim pass.
- Pending job images recover when imaged starts and runs its reconciliation pass; restart imaged only after the query is fixed.
- Daily usage recovers on meterd's next rollup interval and overwrites the derived totals from raw minutes.

Confirm the backlog decreases and the corresponding `code="ok"` series increments before resolving the incident.
