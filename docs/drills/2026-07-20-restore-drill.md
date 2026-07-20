# Restore drill — 2026-07-20 (M8 acceptance, spec §14)

## Acceptance bar

> "restore drill (PG + one app back serving on a clean VM < 30 min,
> documented as executed)" — docs/faas_implementation_spec.md §14 M8 row.

## Run summary

| Field | Value |
|---|---|
| Date (UTC) | 2026-07-20T__:__:__Z |
| Operator | <name> |
| Box | <EX44 id / host IP> |
| Started | <ISO-8601> |
| Finished | <ISO-8601> |
| Total wall-clock | __ min __ s |
| RPO (max archive ts − drill start) | __ min __ s |
| Test app wake latency | __ s |
| Basebackup used | <path under /var/lib/pgsql/basebackup/> |
| Verdict | **PASS** / **FAIL** (bar = 30 min) |

## Step log

The numbers below are pasted from `deploy/scripts/faas-m8-restore-drill.sh`
output. Paste verbatim — the spec's acceptance gate checks this file.

```
<paste faas-m8-restore-drill.sh summary block here>
```

## Pre-flight notes

- Postgres role wired and converged (`wal_level=replica`, `archive_mode=on`,
  `archive_command='cp %p /var/lib/pgsql/archive/%f'`, `max_wal_senders=3`).
- Archive directory `/var/lib/pgsql/archive` populated by continuous WAL
  shipping; most-recent WAL recorded above.
- Basebackup taken via `pg_basebackup -Ft -z -D <dir>` during the nightly
  cron at <ISO-8601>.
- All eight faas units (`apid`, `gatewayd`, `githubd`, `schedd`, `vmmd`,
  `imaged`, `builderd`, `meterd`) were healthy at drill start.

## Anomalies / observations

<Anything worth flagging for the next drill: degraded cold-boot fallback
rate, failed wake, recovery stanza didn't replay all WAL, etc.>

## Follow-ups (M9 candidates)

- pgbackrest orchestration (currently a hand-rolled `cp`).
- Off-host WAL shipping to Hetzner Storage Box (RPO today = local archive
  retention window, ~24 h).
- Archive encryption at rest (gap G2 lean).
- Parallel WAL replay on a hot spare.

## Acceptance

Signed off per spec §14 M8 row, "documented as executed" requirement:
<operator signature / commit ref>