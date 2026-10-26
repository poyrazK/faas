# FaasPostgresActivity

Covers the `postgres_activity` family. All are warnings for the daily review.

| Alert | Fires when |
|---|---|
| `FaasPostgresLongTransaction` | a transaction has been open for more than 10 minutes |
| `FaasPostgresIdleInTransaction` | a session has sat idle inside a transaction for more than 5 minutes |
| `FaasPostgresLockWaits` | more than 5 backends have waited on locks for 10 minutes |
| `FaasPostgresDeadlocks` | PostgreSQL broke a deadlock in the last 15 minutes |
| `FaasPostgresCacheHitRatioLow` | under 95 % of blocks came from `shared_buffers` for 30 minutes, under real read volume |
| `FaasPostgresActivityMetricsStale` | the collector has not written for 5 minutes |
| `FaasPostgresActivityMetricsFailing` | one collector section has failed for 15 minutes |

`faas-postgres-activity-metrics` (role `postgres_metrics`) samples the
control-plane PostgreSQL every 30 s into the node_exporter textfile collector.
The "PostgreSQL" Grafana dashboard plots everything it records:

- transactions;
- client backends by `application_name` (every daemon sets one, such as
  `faas-schedd` or `faas-apid`), client host and state;
- waits;
- the oldest transaction;
- checkpoints;
- the ten tables with the most dead tuples and the ten most read by
  sequential scans;
- the 20 statements with the most execution time, from `pg_stat_statements`.

PostgreSQL waiting for CPU is `FaasPostgresCPUStalled`, in the
`FaasCgroupResources` runbook.

## Symptom

- **A long, or idle-in-transaction, session.**
  - It holds back vacuum on every table it touched, so dead tuples pile up.
  - It may hold row locks other daemons queue behind.
  - Idle in a transaction usually means a daemon leaked a transaction on an
    error path.
- **Lock waits.** Requests that need the locked rows stall. Wake and deploy
  latency rise without CPU rising.
- **A deadlock.** Two code paths take the same locks in opposite orders, and
  one transaction was aborted, which the daemon may have retried.
- **A low cache hit ratio.** The working set no longer fits `shared_buffers`, or
  a statement started scanning a large table.
- **Stale or failing collector.** The dashboard and these alerts are blind.

## Check

1. **Who holds the session or the lock.** On the PostgreSQL dashboard, see
   "Backends by daemon and state" and "Waiting backends". Then, read-only, on
   the control plane:

   ```sql
   SELECT pid, application_name, client_addr, state, wait_event_type, wait_event,
          now() - xact_start AS xact_age, left(query, 200)
   FROM pg_stat_activity
   WHERE datname = 'faas' AND backend_type = 'client backend'
   ORDER BY xact_start NULLS LAST LIMIT 20;

   SELECT blocked.pid AS blocked, blocking.pid AS blocking, blocking.application_name,
          left(blocking.query, 200)
   FROM pg_stat_activity blocked
   JOIN pg_stat_activity blocking ON blocking.pid = ANY (pg_blocking_pids(blocked.pid));
   ```

2. **Which statement costs the time.** In "Top statements by execution time",
   `faas_postgres_statement_info` joins each `queryid` to its normalized text.
   Search for that text to find the daemon that issues it. Group by
   application on the backends panel at the same time to see the daemon and
   host.
3. **Dead tuples and sequential scans.**
   - A table at the top of "Dead tuples" with a stale autovacuum points back at
     step 1: a long transaction blocks the cleanup.
   - A table at the top of "Rows read by sequential scans" is missing an index,
     or a query filters on a column the index does not lead with. This is how
     the 2026-10-20 `instances` scans were found.
4. **Collector.**

   ```bash
   systemctl status faas-postgres-activity-metrics.timer faas-postgres-activity-metrics.service
   journalctl -u faas-postgres-activity-metrics -n 30 --no-pager
   ```

   A `statements` failure usually means `pg_stat_statements` is not loaded or
   not created in the `postgres` database (roles/postgres, #5072).

## Recover

- **Long or idle-in-transaction session:**
  - After confirming what it is, end it with `SELECT pg_terminate_backend(<pid>);`.
    The daemon reconnects.
  - Then fix the code path that left the transaction open. The
    `application_name` names the daemon.
- **Lock waits:** end the blocking session if it is stuck, as above. If many
  sessions legitimately contend, the fix is in the daemon's locking order or
  batch size, not in PostgreSQL.
- **Deadlock:** take both statements from the PostgreSQL log,
  `/var/log/postgresql/postgresql-16-main.log` (Debian's `pg_ctlcluster` writes
  the server log there, not to the journal), and make the two code paths lock
  in the same order.
- **Cache hit ratio:** fix the scanning statement (index or query). Raise
  `shared_buffers` only when the database has grown into it and the
  control-plane memory budget (spec §13) allows it.
- **Collector:** `systemctl restart faas-postgres-activity-metrics.timer`;
  converge the `postgres_metrics` role if the unit or script is missing.
- Exported statement text is the normalized DML that `pg_stat_statements`
  keeps. Utility statements, and any text mentioning a password, are never
  exported.
