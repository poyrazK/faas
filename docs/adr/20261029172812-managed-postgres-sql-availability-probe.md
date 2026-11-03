# ADR: Managed PostgreSQL SQL availability probe

Date: 2026-10-29
Status: Proposed
Related: [ADR-463](463-managed-postgres-provider-health.md) (provider health), [ADR-155](155-provider-neutral-managed-postgres.md) (provider-neutral managed PostgreSQL), [API hosting roadmap](../api-hosting-roadmap.md) (availability: beta ≥ 99.9%, GA ≥ 99.95%, measured by external probes and an error-budget policy)

## Context

The GA bar for availability is 99.95%, measured by probes and an error-budget
policy. Managed PostgreSQL has no such measurement:

- Provider health (ADR-463) reads project, branch and endpoint metadata. By
  design it never opens a SQL connection or wakes a compute, so a database
  that refuses connections, or accepts them but not writes, can read healthy.
- Every managed PostgreSQL alert is a `warn` except the fleet pause and the
  production approval expiry. Nothing pages when customers' databases stop
  answering.

Probing customer databases is not an option. A connection wakes a suspended
compute, which keeps it running and bills the customer for compute they did
not use, and it would need credentials apid otherwise never holds.

## Decision

1. **A canary database per backend.** The operator creates one ordinary
   managed database per backend under the operator account, with the normal
   API, and binds a read-write credential to it. Its connection string lives
   in the sealed environment.

2. **apid probes it with SQL.** `FAAS_MANAGED_POSTGRES_SQL_PROBE_CONFIG` names
   a JSON file:

   ```json
   {"interval_seconds": 60, "timeout_seconds": 10,
    "targets": [{"backend": "neon-eu", "dsn_env": "FAAS_MANAGED_POSTGRES_PROBE_DSN_NEON_EU"}]}
   ```

   - Each interval, apid opens a **new** connection to each canary, as a
     customer would, and writes a row (`INSERT … ON CONFLICT … RETURNING`). It
     then reads the row back and checks it is no older than the write; another
     apid replica may have written a newer one.
   - The outcome is one of `ok`, `connect_failed`, `write_failed`,
     `read_failed` or `timeout`. The probe never logs or returns the
     connection string.
   - The interval is 30–3600 s (default 60) and the timeout 1–30 s (default
     10), always below the interval.
   - An unset variable disables the probe. An invalid file, an unknown field,
     an empty variable or an unparseable connection string fails apid startup,
     like other configuration errors.
   - The configuration is its own file rather than a block in the provider
     registry. The registry is decoded strictly and feeds each backend's
     placement fingerprint, so a new field there would make older apid
     releases refuse the file and close production approvals pinned to the
     fingerprint.

3. **Metrics.** Each label set is the backend ID only:
   - `apid_managed_postgres_sql_probe_total{backend,outcome}`
   - `apid_managed_postgres_sql_probe_duration_seconds{backend}`
   - `apid_managed_postgres_sql_probe_last_success_timestamp_seconds{backend}`
   - `apid_managed_postgres_sql_probe_enabled`

4. **Alerts and the error budget.**
   - `FaasManagedPostgresSQLProbeFailing` **pages** when no apid has had a
     successful probe of a backend for five minutes. One healthy replica keeps
     it quiet, so one host's network problem does not page.
   - `FaasManagedPostgresSQLErrorBudgetBurn` **warns** when more than 0.3% of
     probes failed over six hours, with at least 180 probes. That is six times
     the failure rate that spends the 99.95% monthly budget. With one sample
     a minute, a one-hour 14.4× window would fire on a single failed probe, so
     the fast burn is left to the five-minute page.
   - Both alerts share a runbook.

5. **What is measured.** The probe measures what the canary's own
   compute setting allows. With scale-to-zero and an interval longer than the
   suspend timeout, every probe includes a wake. With an always-on compute or
   a short interval, the probe measures a warm database. The operator chooses
   per canary; at the default interval, a scale-to-zero canary stays awake.

## Consequences

- Managed PostgreSQL has an availability signal that sees SQL and pages.
  Beta (≥ 99.9%) and GA (≥ 99.95%) can be judged from the probe's history.
- Each canary costs one small database per backend. With a 0.25 CU compute
  kept awake by the default interval, that is about 180 CU-hours a month.
- The probe runs from apid on the control plane. Customer apps connect from
  compute nodes through bound egress. The probe therefore shows the provider
  is down, not that one node's path to it is broken.
- Local Prometheus keeps 15 days, so the monthly figure belongs to the durable
  metrics backend, as for the other public SLOs.

## Not covered

- Probing each customer database, or a customer-visible availability figure.
- A separate wake-latency objective for scale-to-zero databases.
- Probing from compute nodes over the bound egress path.

## Rejected alternatives

- **Probe customer databases.** It would wake suspended computes and bill
  customers for them, and give apid credentials to every database.
- **Promote the provider health alerts to `page`.** They read metadata only and
  stay healthy while SQL fails, which is the gap this decision closes.
- **A block in the provider registry.** See decision 2: strict decoding and the
  placement fingerprint make it a compatibility hazard.
