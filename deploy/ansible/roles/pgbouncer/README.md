# pgbouncer — transaction-mode pooler for the compute-node path

Disabled by default (`faas_pgbouncer_enabled: false`). Enabling it changes the
endpoint every compute node dials, so it is an explicit per-fleet decision.

## Why

Per-daemon pool budgets multiply across the fleet. `postgres_capacity` derives
its `max_connections` from `pkg/db.DaemonMaxConnections` summed over the
control plane and every compute node: **610 backends at 12 nodes, 4,600 at
100**. PostgreSQL runs one process per backend, so 4,600 is roughly 108 GB of
backends before a single query runs, and far outside where the postmaster is
operable at all.

Pooling decouples the two numbers that were fused:

| | Before | With the pooler |
|---|---|---|
| Connections clients may open | `max_connections` | `max_client_conn` (5,000 default, ~2 KB each) |
| Backends PostgreSQL must run | the same number | `default_pool_size` (64 default) |

## What goes through it, and what does not

**Only the compute-node TCP path.** Control-plane daemons keep the
peer-authenticated Unix socket to the postmaster. They are ~40 connections in
total, peer auth does not survive a pooler cleanly, and routing them would gain
nothing — the fleet-scaling pressure is entirely the compute side.

**Nothing session-scoped.** Transaction pooling returns the server connection
to the pool at COMMIT, which breaks:

- `LISTEN` — the ADR-190 notify hub, the legacy `Subscribe` path, `WaitFor`.
- Session `pg_advisory_lock` (not the `_xact_` variant) — `PgStore`'s
  edge-rule mutation fence and `MigrateUp`'s migration lock.

Those resolve through `FAAS_DATABASE_URL_DIRECT` to the postmaster instead.
`pkg/db/direct.go` owns that split and `TestSessionScopedAcquires` enforces at
build time that no new session-scoped site can reach a pooled connection
without an explicit decision. **Port 5432 therefore stays open** — the pooler
is additive, not a replacement.

Transaction-scoped locks are safe and stay pooled: `pg_advisory_xact_lock`
(ADR-193's node reservation, the workflow lock, the managed-secret lock)
releases at COMMIT by definition.

## Prepared statements

`max_prepared_statements = 0` on purpose. pgx defaults to named `PREPARE`s
that a pooled transaction can outlive; rather than depend on pgbouncer's
version to track them, `pkg/db` switches the pooled path to
`QueryExecModeExec` whenever `FAAS_DATABASE_URL_DIRECT` is set. The
client-side guarantee is stronger and holds against any pooler, or none.

## Auth

Clients present the same scram credential `pg_hba` already requires of compute
nodes. `auth_query` verifies against `pg_shadow` rather than keeping a second
copy of the secret on disk; the pooler's own server leg uses the
peer-authenticated Unix socket, so it stores no database password at all.

## Operating it

`faas_pgbouncer_default_pool_size` is the number that now bounds PostgreSQL.
Raise it if `pgbouncer` shows clients waiting (`SHOW POOLS` → `cl_waiting`),
not because the fleet grew — growing the fleet raises `max_client_conn`, which
is cheap.

`application_name` no longer identifies the client daemon through the pooler,
so the `pg_stat_activity` triage in `docs/runbooks/FaasDBPoolStarved.md`
applies to the direct path only. The direct pool tags itself
`<daemon>-direct`, which is what an operator chasing a stuck LISTEN or advisory
lock actually wants.

The role refuses to finish unless `SELECT 1` succeeds through the pooler: a
pooler that is up but cannot reach the postmaster is worse than one that is
down, because clients connect and then fail on their first query.
