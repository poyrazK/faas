# FaasPostgresDown / FaasPostgresProbeStale

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml` (group `faas_host_resources`).
Metrics: `node_systemd_unit_state{name=~"postgresql@.+-main.service"}` and
`node_textfile_mtime_seconds{file=~".*/faas_postgres_connections.prom"}`
(node_exporter on the control plane). Severity: page.

## Symptom

- `FaasPostgresDown`: the PostgreSQL cluster unit has not been active for
  1 minute.
- `FaasPostgresProbeStale`: the unit may still be active, but
  `faas-postgres-connection-metrics.timer` has not completed a query for
  5 minutes. The probe runs every 30 seconds as the `postgres` user over
  the local socket and only rewrites its textfile after `psql` succeeds,
  so a stale file means the database is hung, refusing connections, or
  the timer stopped.

apid, schedd, meterd, outboundd and every compute daemon depend on this
database. Expect API errors, stalled wakes and a backlog of scheduler
work while it is down.

## Check

```bash
systemctl status 'postgresql@*-main' --no-pager
journalctl -u 'postgresql@*-main' --since '-30m' --no-pager | tail -40
sudo -u postgres psql -Atc 'select 1'
systemctl list-timers faas-postgres-connection-metrics.timer --no-pager
df -h /var/lib/postgresql
```

Common causes: a full root filesystem (see
[FaasHostDiskSpace](FaasHostDiskSpace.md)), an OOM kill (see
[FaasHostMemoryLow](FaasHostMemoryLow.md)), an invalid setting written by
the `postgres_capacity` role, or exhausted connection slots (see
[FaasPostgresConnectionsHigh](FaasPostgresConnectionsHigh.md)).

## Recover

- Unit failed: read the last journal lines before restarting; a config
  error will fail again. Then `sudo systemctl start postgresql@16-main`
  (adjust the major version).
- Disk full: free space first; PostgreSQL will not start without room
  for WAL.
- Hung but active: check `pg_stat_activity` for blocking locks from the
  superuser reserve (`sudo -u postgres psql`), terminate the blocker, and
  only restart the cluster if it does not answer at all.
- Probe timer stopped while the database is healthy:
  `sudo systemctl restart faas-postgres-connection-metrics.timer`.
- Data loss or corruption: stop and follow the restore procedure in
  [PostgresBackup](PostgresBackup.md) and `docs/break-glass/database-repair.md`.

## Silence

```bash
amtool --alertmanager.url=http://127.0.0.1:9094 silence add \
  'alertname=~"FaasPostgres(Down|ProbeStale)"' \
  --duration=30m --comment='planned PostgreSQL maintenance'
```
