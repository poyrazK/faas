# PostgresBackup — Provider-neutral off-host Postgres backup

Spec §14 M8 + issue #250. Closes the "single-disk backup" gap that
the M8 restore drill (deploy/scripts/faas-m8-restore-drill.sh)
left open — the drill proves a local restore works, but a host
loss wipes both `/var/lib/pgsql/data/` AND `/var/lib/pgsql/archive/`
AND `/var/lib/pgsql/basebackup/`. A provider-neutral object store is the
off-host recovery source.

## Context

| Layer | Local path | Off-host replica |
|---|---|---|
| Cluster data | `/var/lib/pgsql/data` | n/a (live) |
| WAL archive | `/var/lib/pgsql/archive/` | `offhostbox:faas-pg-wal/` |
| Basebackup | `/var/lib/pgsql/basebackup/` | `offhostbox:faas-pg-basebackup/` |

- **Continuous WAL**: `archive_command` stores every WAL segment
  locally first, then best-effort sends it via `rclone copyto`; a
  transient remote failure cannot stall WAL recycling.
- **Nightly basebackup**: `faas-pg-basebackup.timer` fires at 03:00 UTC;
  `faas-pg-basebackup-push.timer` fires at 03:30 UTC, copies and verifies
  every completed backup, then keeps the newest two local recovery points.
  Remote retention is controlled by the object-store lifecycle policy.
- **Bounded local WAL**: `faas-pg-wal-prune.timer` runs hourly. It derives
  the recovery boundary from the oldest retained basebackup, copies every
  deletion candidate off-host, verifies the bytes, and only then removes
  local WAL preceding that boundary.
- **RPO in steady state**: bounded by `archive_command` latency,
  typically 5-30 s. A failed rclone push still produces a
  successful local archive, so PG WAL recycling is never blocked.

## Preconditions

- `/etc/faas/secrets/storage-box/rclone.conf` exists, mode `0400
  root:root` (issue #250 fail-closed assert). It defines the
  provider-specific endpoint under the stable `offhostbox` alias.
- `rclone` is on PATH (`apt install rclone` — handled by the
  `postgres_backup` ansible role).
- The configured object-store identity has read+write to `faas-pg-wal/` and
  `faas-pg-basebackup/`.

## Procedure

### Immediate push (one-shot)

```bash
sudo systemctl start faas-pg-basebackup-push.service
sudo journalctl -u faas-pg-basebackup-push.service -n 100
```

### Scheduled push (timer)

```bash
systemctl list-timers --all | grep faas-pg-basebackup
# Expect:
#   Tue 2026-08-04 03:30:00 UTC  faas-pg-basebackup-push.timer
#   Tue 2026-08-04 03:00:00 UTC  faas-pg-basebackup.timer
```

### T-7 restore verify (issue #250 acceptance)

```bash
sudo bash deploy/scripts/pg-restore-verify.sh
```

Pulls the newest basebackup from the off-host store, restores it
into a throwaway PG instance under `/var/lib/pgsql/restore-test/`
on port 5433, replays WAL via `rclone cat`, and asserts
`count(*)` on `accounts` / `apps` / `instances` matches the live
cluster within 5%.

### Local round-trip (M8 baseline — still required)

```bash
sudo make backup-restore-drill-preflight
sudo make backup-restore-drill
```

The preflight validates the latest backup archive, PostgreSQL, the database
migration ledger, the role's active FaaS services, host-identity consistency,
and a configurable recovery app URL. It does not stop services or modify files.

The full command is destructive: it deliberately stops the FaaS daemons that
were active on this role, captures exact row counts, forces and waits for a
quiesced WAL archive boundary, stops PostgreSQL, and wipes the live
PostgreSQL data directory, extracts tar-format `base.tar.gz` and the optional
`pg_wal.tar.gz` member, replays archived WAL, checks promotion and schema
migrations, compares the `accounts`, `apps`, and healthy-instance row counts
with the quiesced pre-crash values, and probes the recovery app. Set
`FAAS_DRILL_APP_URL=https://<healthy-app>.gregale.dev/` for split deployments.
This public probe measures full recovery only; it is outside the platform-only
snapshot restore SLO of p95 below 350 ms. The script writes a dated PASS or
FAIL record under `docs/drills/`; an M8 sign-off PR must carry the `m8-done`
label only when that record is a committed PASS from the last 30 days.

## Validation matrix

| Signal | Healthy value | How to read |
|---|---|---|
| `apid_pg_backup_last_pushed_seconds` | Unix timestamp within 26h | `curl -fsS http://127.0.0.1:9101/metrics \| grep '^apid_pg_backup_last_pushed_seconds'` |
| `apid_pg_wal_prune_last_success_timestamp_seconds` | Unix timestamp within 2h | same metrics endpoint |
| `apid_pg_wal_archive_bytes` | below 20 GiB | same metrics endpoint |
| Remote basebackup count | at least local count | `rclone lsd offhostbox:faas-pg-basebackup --config /etc/faas/secrets/storage-box/rclone.conf \| wc -l` vs `ls /var/lib/pgsql/basebackup \| wc -l` |
| Remote `du` vs local `$PG_BB_ROOT` | ±10% | provider-side storage UI |
| `archive_command` line | local copy followed by best-effort `rclone copyto` to `offhostbox:` | `grep ^archive_command /etc/postgresql/15/main/postgresql.conf` |

## Rollback

1. Stop the push timer: `sudo systemctl disable --now
   faas-pg-basebackup-push.timer`.
2. Revert `archive_command` to the local-only baseline:
   ```bash
   sudo sed -i "s|^archive_command = .*|archive_command = 'cp %p /var/lib/pgsql/archive/%f'|" \
     /etc/postgresql/15/main/postgresql.conf
   sudo systemctl reload postgresql
   ```
3. Disable local pruning: `sudo systemctl disable --now faas-pg-wal-prune.timer`.

The push is additive; reverting is two `systemctl` commands +
one `sed` rewrite.

## Escalation

| Symptom | Page? | First action |
|---|---|---|
| basebackup timestamp missing or older than 26h | yes (page) | inspect `faas-pg-basebackup-push.service`; check the configured rclone identity and destination |
| WAL prune timestamp missing or older than 2h | warn | run the dry-run below and inspect `faas-pg-wal-prune.service` |
| local WAL exceeds 20 GiB | warn | verify off-host reachability and the retained basebackup boundary |
| `archive_command` line shows old `cp` | warn | the operator has not converged the PostgreSQL role; re-run the control-plane bootstrap target |
| T-7 row-count ratio < 0.95 | page | the restore is partial — check `rclone cat offhostbox:faas-pg-wal/` returns the right segment; check `recovery.signal` + `restore_command` are in the throwaway postgresql.conf |

## Acceptance

The standing gate for production Tier 1 is:

```bash
sudo systemctl start faas-pg-basebackup-push.service && \
  sudo bash deploy/scripts/pg-restore-verify.sh
```

Both must pass within 30 min wall-clock for the off-host backup to
be considered healthy.

## WAL retention operations

Preview exactly what is eligible without deleting anything:

```bash
sudo -u root /usr/local/lib/faas/faas-pg-wal-prune.sh
```

Apply the same calculation and deletion only after its off-host copy and
byte check pass:

```bash
sudo systemctl start faas-pg-wal-prune.service
sudo journalctl -u faas-pg-wal-prune.service -n 100 --no-pager
```

If remote verification fails, stop the timer and repair access to the remote.
Do not manually delete `/var/lib/pgsql/archive` files. The pruner deliberately
fails before the first unlink when any candidate is missing or differs.

## Refs

- Spec §14 M8 row.
- `deploy/scripts/faas-m8-restore-drill.sh` — local-disk sibling.
- `deploy/ansible/roles/postgres_backup/tasks/main.yml` — installer.
- `deploy/systemd/faas-pg-basebackup-push.{service,timer}` — push unit pair.
- `deploy/scripts/faas-pg-wal-prune.sh` — verified local WAL retention.
- `pkg/wire/metrics.go` — backup and WAL-retention gauges.
- ADR-056 — design rationale.
