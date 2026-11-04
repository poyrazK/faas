# Managed PostgreSQL SQL probe failing

This runbook covers `FaasManagedPostgresSQLProbeFailing` (page) and
`FaasManagedPostgresSQLErrorBudgetBurn` (warn), from the canary SQL probe
([ADR-20261029172812](../adr/20261029172812-managed-postgres-sql-availability-probe.md)).

Each apid opens a new connection to one operator-owned canary database per
backend, writes a row, and reads it back, once a minute by default. The probe
never connects to a customer database.

## Signals

| Metric | Meaning |
| --- | --- |
| `apid_managed_postgres_sql_probe_total{backend,outcome}` | Probes by outcome: `ok`, `connect_failed`, `write_failed`, `read_failed` or `timeout`. |
| `apid_managed_postgres_sql_probe_duration_seconds{backend}` | Time for a new connection, a write and a read. |
| `apid_managed_postgres_sql_probe_last_success_timestamp_seconds{backend}` | Latest successful probe; zero before the first. Use `max` across apid replicas. |
| `apid_managed_postgres_sql_probe_enabled` | 1 when `FAAS_MANAGED_POSTGRES_SQL_PROBE_CONFIG` is set. |

The `backend` label is the backend ID from the probe configuration. No label
holds a connection string, credential, account or provider resource.

## What the alerts mean

- **`FaasManagedPostgresSQLProbeFailing` (page).** No apid has had a successful
  probe for the backend for five minutes. Customer databases on the backend
  are likely unreachable, or accept connections but not writes.
- **`FaasManagedPostgresSQLErrorBudgetBurn` (warn).** More than 0.3% of
  probes failed over six hours (at least 180 probes). That is six times the
  rate that spends the 99.95% monthly availability budget, the GA bar.
  Short failures that never reach the page add up here.

## Triage

1. Read the failing outcome from `apid_managed_postgres_sql_probe_total`:
   - `connect_failed`: the provider's proxy or compute did not accept a
     connection. Check the provider's status page and region, and apid's
     outbound network.
   - `write_failed`: connections work but writes fail. Look for a storage
     outage, a project or branch size limit (including the storage pool), or
     a read-only switch at the provider.
   - `read_failed`: the row written moments earlier could not be read back.
     Treat it as a storage consistency incident at the provider.
   - `timeout`: the probe did not finish within `timeout_seconds`. A cold
     compute that wakes slowly or an overloaded provider both show this way.
2. Compare the backend's customer health
   (`apid_managed_postgres_health_databases`) and provider status. Provider
   metadata can read `ready` while SQL fails; that gap is why the probe
   exists.
3. Check the apid warning log line `managed postgres sql probe failed` for the
   backend and outcome. It never contains the connection string.
4. If only one apid replica fails, the problem is that host's network, not
   the provider: the page needs every replica to fail.

## The canary database

The canary is an ordinary managed database owned by the operator account,
created once per backend with the normal API, and bound with a read-write
credential. Its connection string lives in the sealed environment, in the
variable the probe configuration's `dsn_env` names. The probe keeps one row in
the `gregale_availability_probe` table and creates the table on first use.

- **Do not delete the canary** to silence the alert; the page fires on missing
  successes too. Disable the probe by unsetting
  `FAAS_MANAGED_POSTGRES_SQL_PROBE_CONFIG` and restarting apid instead.
- **Rotate the credential** by updating the variable and restarting apid.
- The canary's compute setting decides what is measured. With scale-to-zero
  and an interval longer than its suspend timeout, every probe includes a
  wake. With an always-on compute or a short interval, the probe measures a
  warm database.
