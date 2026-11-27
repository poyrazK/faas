# ADR-20261126120000: Operator control-plane health report

Status: Accepted

Extends ADR-20261115175927 (operator console routes) and
ADR-20261124150000 (operator disk pressure report, whose Prometheus read
this follows).

## Context

The control plane is one node, and every component depends on its
Postgres. The operations console shows compute nodes, instances, wakes,
snapshots, builds, memory, gateway traffic and disks, but nothing about the
control plane itself. Today an operator learns of trouble there only from an
alert, and then has to open Grafana or a shell.

The data is already in Prometheus. These were checked against the
production fleet's Prometheus on 2026-11-26; every query answered in about
10 ms:

- `faas-postgres-connection-metrics` (textfile, job `node`) exports client
  connections against the ordinary connection capacity (123 of 185 then)
  and the WAL archive and off-host push timestamps.
- The postgres activity collector (`faas-postgres-activity-metrics`,
  textfile, job `node`) exports the rest of `faas_postgres_*`:
  - longest query, oldest transaction, idle in transaction;
  - backends by application and state, and lock waits;
  - deadlocks, cache blocks, commits and rollbacks, temp bytes;
  - database size;
  - pg_stat_statements per query id, with normalized text in
    `faas_postgres_statement_info`;
  - its own last run and per-section errors.
- Backup and WAL timestamps:
  - `apid_pg_backup_last_pushed_seconds`;
  - `faas_cp_state_backup_last_success_timestamp_seconds`;
  - `faas_postgres_wal_last_archived_timestamp_seconds`;
  - `faas_postgres_offhost_wal_*`;
  - `apid_pg_wal_prune_last_success_timestamp_seconds`;
  - `apid_pg_wal_archive_oldest_age_seconds`;
  - the backup units' `node_systemd_unit_state`.
- The delivery outboxes: `faas_notification_outbox_*` per channel and
  `schedd_webhook_event_outbox_*`.
- Every daemon's pgx pool (`<daemon>_db_pool_*`) and pg_notify hub
  (`<daemon>_db_notify_hub_*`). Compute daemons carry the discovery `node`
  label.
- `up` for the control plane's static loopback targets, which have no
  `node` label, and node_exporter for the host.

Alerts already exist for each of these signals, with thresholds.

## Decision

### One operator session read, Prometheus only

| Route | Returns |
| --- | --- |
| `GET /v1/admin/obs/control-plane` | `ObsControlPlaneReport` |

It is operator-only (operator session read), sends `no-store`, takes no
parameters (any parameter is a 400), and is excluded from spec compliance.
It needs no migration, store query or new metric.

`pkg/apid/controlplane` (ADR-982) runs 45 queries concurrently at one
pinned instant under `ObsCPPrometheusBudget` (3 s). The source is
`observed`, `partial` (the failed queries are named), `unavailable` or
`not_configured`.

Per-daemon families are read with one query each by turning the metric
name prefix into a label: `label_replace` of `__name__` gives `pool` or
`hub`. The counters are read through a `[w:1m]` subquery, so the renamed
label survives `increase()`, as the `FaasDBPoolStarved` alert already does.

### Checks copy their alerts

The report grades 17 checks as `ok`, `warning`, `critical` or `unknown`
(not read). Each check names the alert it mirrors and carries that alert's
threshold from `pkg/api/limits.go` (`ObsCP*`). The page grades the instant;
it does not wait for the alert's `for:`.

| Group | Check | Warning | Critical |
| --- | --- | --- | --- |
| postgres | connections | over 75% of capacity | over 90% |
| postgres | stats collector | ran over 300 s ago, or a section failing | — |
| postgres | oldest transaction | over 600 s | — |
| postgres | idle in transaction | over 300 s | — |
| postgres | lock waits | over 5 | — |
| postgres | deadlocks (15 min) | any | — |
| backups | base backup pushed | over 26 h ago, never, or unit failed | — |
| backups | state backup | over 26 h ago or unit failed | — |
| backups | WAL archived | over 10 min ago (ok before the first archive) | — |
| backups | WAL pushed off host | when configured: over 15 min ago, or never | — |
| backups | WAL pruned | over 2 h ago, or never | — |
| backups | oldest archived WAL | over 3 d | — |
| delivery | notification outbox | oldest over 15 min | — |
| delivery | webhook outbox | oldest over 5 min | — |
| daemons | database pools | a pool over 90% in use | an acquire canceled in 5 min |
| daemons | pg_notify listeners | subscribers but no connection | — |
| daemons | control-plane targets | — | any target down |

When the activity collector is stale, the Postgres checks it feeds are
`unknown`: its last values (for example "no open transaction") are not
current evidence. Connections come from the separate connection exporter
and stay graded. Lock waits read as zero only while the collector's
`activity` section succeeds; when that section fails the check is
`unknown`, not "0 waiting". The database pools check counts unhealthy
pools, so it carries no percent threshold. The report's `state` is the
worst check's.

### Details

- **Postgres:**
  - connections and capacity;
  - database size;
  - longest query, oldest transaction, idle in transaction;
  - cache hit share, commits per second, rollback share and temp bytes
    over `ObsCPWindow` (1 h);
  - the collector's age.
- **Backends:** counts by application and state.
- **Statements:** the `ObsCPStatementsMax` (10) that spent the most
  execution time in the window, with calls and mean time. The text is
  pg_stat_statements' normalized text, so it holds no parameter values; it
  is cut to 500 characters.
- **Pools:** each daemon pool per node: acquired, idle, max, utilization,
  canceled acquires and mean acquire wait in the window. They are sorted
  worst first.
- **pg_notify hubs:** connections, subscribers, and reconnects in the
  window.
- **Targets:** the control plane's scrape targets, down first.
- **Host:** CPUs, load and memory. The control plane's disks stay on Disk
  pressure.

Lists are capped at `ObsCPRowsMax` (200).

## Consequences

- **Console:** gets a Control plane page under On call.
- **Alerts unchanged:** the page shows the signals behind the existing
  control-plane alerts and how close each is to firing. It adds no alert or
  metric.
- **Not covered:** database nodes' managed Postgres (their own pages),
  replication (the control plane has no replica today), and per-table bloat.
