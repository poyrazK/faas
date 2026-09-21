# Postgres pool starved

`FaasDBPoolStarved` fires when a daemon has callers abandoning Postgres
connection acquisitions — they waited for a pool slot and their context ended
first. The daemon is `up`, `ready`, and answering `/metrics`; what is failing
is the queue in front of its own pool, not Postgres.

The counter behind the alert, `<daemon>_db_pool_canceled_acquires_total`, is
deliberately the one without a warm-up floor. Its sibling
`_db_pool_empty_acquires_total` also counts acquisitions that found no idle
connection, but pools run `MinConns=0`, so every daemon necessarily posts
empty acquires while warming up. A cancelled acquire cannot happen that way:
someone gave up waiting.

This is the signal that did not exist on 2026-09-12, when `fsn-1` sat at 96 of
97 connection slots with 88 of them idle and every daemon died on SQLSTATE
53300. The only way to see it then was `pg_stat_activity` on the box.

## Symptom

Requests to the affected daemon time out or return 503 while Postgres itself
looks healthy — low CPU, no slow-query log, connection count below
`max_connections`. The daemon's own latency histograms show time spent before
any query was issued.

## Check

Start with the daemon's own pool, since the alert is per-daemon:

```promql
sum by (state) ({__name__=~".+_db_pool_conns", job="faas-schedd"})
{__name__=~".+_db_pool_max_conns", job="faas-schedd"}
rate({__name__=~".+_db_pool_acquire_wait_seconds_total", job="faas-schedd"}[5m])
```

Two distinct shapes, with opposite fixes:

- **At the cap.** `sum(db_pool_conns)` sits at `db_pool_max_conns` and
  `acquired` dominates `idle`. The daemon's entry in
  `pkg/db.DaemonMaxConnections` is too small for its concurrency. Note that
  since ADR-193 every RAM-counting instance admission holds a pool connection
  for a short transaction, so schedd's demand scales with concurrent wakes.
- **Below the cap.** Connections are held too long rather than being too few —
  a slow query, or a transaction leaked by an error path that skipped its
  `Rollback`. Find the holder:

  ```sql
  SELECT pid, state, wait_event_type, wait_event,
         now() - xact_start AS xact_age, left(query, 120)
    FROM pg_stat_activity
   WHERE datname = current_database()
     AND application_name = 'faas-schedd'
   ORDER BY xact_age DESC NULLS LAST;
  ```

Also confirm the LISTEN hub is still collapsing subscribers. If
`<daemon>_db_notify_hub_conns` is 0 while `_db_notify_hub_subscribers` is
non-zero, `FAAS_DB_NOTIFY_HUB=0` is set and the daemon is parking one
connection per subscriber.

That is not itself a fault: the pool budget follows the switch
(`db.DaemonMaxConnectionsNotifyHubDisabled`), so the daemon is sized for the
mode it is in. It is a *fleet* capacity signal — that daemon is consuming its
pre-hub connection count against the shared `max_connections`, and on a large
fleet several daemons in that mode can exhaust the database host even though
each one individually is within its own budget.

## Recover

Immediate, in order of preference:

1. **Unset `FAAS_DB_NOTIFY_HUB`** if the check above showed the hub disabled,
   and restart the daemon. This returns the largest number of slots for the
   least risk.
2. **Restart the daemon** if a leaked transaction is holding connections. This
   is a mitigation, not a fix — capture the `pg_stat_activity` output above
   first, or the leak is unattributable.
3. **Raise the daemon's `DaemonMaxConnections` entry** only after confirming
   the fleet has headroom. That table is the per-daemon input the
   `postgres_capacity` Ansible role multiplies across the control plane and
   every compute node to derive `max_connections`, so raising one entry raises
   the requirement on the database host for every node in the fleet. Re-run
   the role and confirm the derived ceiling before deploying.

Never lower a `DaemonMaxConnections` entry while this alert has fired for that
daemon in the trailing week. A flat-zero cancelled-acquire rate on a busy
daemon is the precondition for a reduction; this alert firing is the direct
evidence against one.
