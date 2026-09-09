# Restore drill — <UTC-date> (M8 acceptance, spec §14)

## Acceptance bar

> "restore drill (PG + one app back serving on a clean VM < 30 min,
>  documented as executed)" — docs/faas_implementation_spec.md §14 M8 row.

## Run summary

| Field | Value |
|---|---|
| Date (UTC) | <UTC-date> |
| Operator | <$USER> |
| Box | <hostname -f> |
| Started | <ISO-8601> |
| Finished | <ISO-8601> |
| Wall-clock total | <min> min <sec> s |
| RPO via basebackup | <min> min <sec> s |
| RPO via WAL | <min> min <sec> s |
| Wake latency | <sec>s (end-to-end recovery probe; outside the <350 ms platform snapshot-restore SLO) |
| Basebackup used | <path under /var/lib/pgsql/basebackup/> |
| Basebackup SHA-256 | <sha256sum of base.tar.gz> |
| Recovery stanza status | promoted at <ISO-8601> |
| host.age SHA-256 (preserved) | <sha256sum on a compute role, or not-present-on-this-role> |
| Verdict | **PASS** / **FAIL** (bar = 30 min) |
| Operator / commit | <$USER> @ <git rev-parse HEAD> |

## Step log (auto-captured)

```
drill-start: <ISO-8601>
basebackup:  <path> (<sha256>)
rpo-base:    <min> min <sec> s
rpo-wal:     <min> min <sec> s
host.age:    <sha256, or not-present-on-this-role>
wipe:        /var/lib/pgsql/data
recovery-app-probe: <sec>s to <FAAS_DRILL_APP_URL> (end-to-end; outside the <350 ms platform snapshot-restore SLO)
verdict:     PASS
```

## Pre-flight notes

- Postgres role wired and converged (`wal_level=replica`, `archive_mode=on`,
  `archive_command='cp %p /var/lib/pgsql/archive/%f'`, `max_wal_senders=3`).
- Postgres_backup role wired and converged (nightly `pg_basebackup` timer
  `faas-pg-basebackup.timer` enabled; `systemctl list-timers --all` shows
  the next 03:00 UTC run).
- Archive directory `/var/lib/pgsql/archive` populated by continuous WAL
  shipping; most-recent WAL recorded above.
- Basebackup taken via `pg_basebackup -Ft -z -D <dir>` during the nightly
  cron at <ISO-8601>, or via `make backup-pg` for an immediate run.
- The script records the FaaS units active on the current role and restarts
  only that set. A split control plane therefore does not start compute units.
- `host.age` and `host.age.pub` must either both exist or both be absent. The
  key pair is preserved on compute roles and is not required on a split
  control-plane-only host.

## Anomalies / observations

<Anything worth flagging for the next drill: degraded cold-boot fallback
rate, failed wake, recovery stanza didn't replay all WAL, host.age SHA
mismatch (rotate path not yet shipped), etc.>

## Follow-ups (M9 candidates)

- pgbackrest orchestration (currently a hand-rolled `cp`).
- Off-host WAL shipping to Hetzner Storage Box (RPO today = local archive
  retention window, ~24 h).
- Archive encryption at rest (gap G2 lean).
- Parallel WAL replay on a hot spare.

## Acceptance

Signed off per spec §14 M8 row, "documented as executed" requirement:
<operator signature / commit ref>
