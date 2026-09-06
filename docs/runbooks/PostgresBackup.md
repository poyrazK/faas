# PostgresBackup — Provider-neutral off-host Postgres backup

Spec §14 M8 + issue #250. Closes the "single-disk backup" gap that
the M8 restore drill (deploy/scripts/faas-m8-restore-drill.sh)
left open — the drill proves a local restore works, but a host
loss wipes both `/var/lib/pgsql/data/` AND `/var/lib/pgsql/archive/`
AND `/var/lib/pgsql/basebackup/`. The Storage Box is the
S3-of-single-node replacement the spec explicitly forbids.

## Context

| Layer | Local path | Off-host replica |
|---|---|---|
| Cluster data | `/var/lib/pgsql/data` | n/a (live) |
| WAL archive | `/var/lib/pgsql/archive/` | `offhostbox:faas-pg-wal/` |
| Basebackup | `/var/lib/pgsql/basebackup/` | `offhostbox:faas-pg-basebackup/` |

- **Continuous WAL**: `archive_command` stores every WAL segment
  locally first, then best-effort sends it via `rclone copyto`; a
  transient remote failure cannot stall WAL recycling.
- **Nightly basebackup**: the existing `faas-pg-basebackup.timer`
  fires at 03:00 UTC; the new `faas-pg-basebackup-push.timer`
  fires at 03:30 UTC and `rclone sync`s the local tree to the
  Storage Box.
- **RPO in steady state**: bounded by `archive_command` latency,
  typically 5-30 s. A failed rclone push still produces a
  successful local archive, so PG WAL recycling is never blocked.

## Preconditions

- `/etc/faas/secrets/storage-box/rclone.conf` exists, mode `0400
  root:root` (issue #250 fail-closed assert). It defines the
  provider-specific endpoint under the stable `offhostbox` alias.
- `/etc/faas/secrets/storage-box/box-age-key` exists, mode `0400
  root:root`.
- `rclone` is on PATH (`apt install rclone` — handled by the
  `postgres_backup` ansible role).
- The Storage Box user has read+write to `faas-pg-wal/` and
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

Pulls the newest basebackup from the Storage Box, restores it
into a throwaway PG instance under `/var/lib/pgsql/restore-test/`
on port 5433, replays WAL via `rclone cat`, and asserts
`count(*)` on `accounts` / `apps` / `instances` matches the live
cluster within 5%.

### Local round-trip (M8 baseline — still required)

```bash
sudo make backup-restore-drill
```

This is destructive: it deliberately stops the FaaS daemons, wipes the live
PostgreSQL data directory, extracts the tar-format `base.tar.gz` and
`pg_wal.tar.gz` members, replays archived WAL, checks promotion and schema
migrations, compares the `accounts`, `apps`, and healthy-instance row counts
with the pre-crash values, and wakes the fixture app. The script writes a
dated PASS or FAIL record under `docs/drills/`; an M8 sign-off PR must carry
the `m8-done` label only when that record is a committed PASS from the last 30
days.

## Validation matrix

| Signal | Healthy value | How to read |
|---|---|---|
| `pg_backup_last_pushed_seconds` gauge | < 3600 | `curl -fsS http://127.0.0.1:9101/metrics \| grep '^pg_backup_last_pushed_seconds'` |
| Remote basebackup count vs local | match | `rclone lsd offhostbox:faas-pg-basebackup --config /etc/faas/secrets/storage-box/rclone.conf \| wc -l` vs `ls /var/lib/pgsql/basebackup \| wc -l` |
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
3. Remove the storage-box systemd drop-in:
   `sudo rm /etc/systemd/system/postgresql@.service.d/99-faas-off-host-backup.conf`
   and `sudo systemctl daemon-reload`.

The push is additive; reverting is two `systemctl` commands +
one `sed` rewrite.

## Escalation

| Symptom | Page? | First action |
|---|---|---|
| `pg_backup_last_pushed_seconds > 86400` | yes (page) | `journalctl -u faas-pg-basebackup-push.service` — most likely rclone auth drift; check `/etc/faas/secrets/storage-box/rclone.conf` mode + contents |
| `pg_backup_last_pushed_seconds > 3600` | warn | check remote endpoint reachability (`rclone lsd offhostbox:faas-pg-basebackup --config ...`) |
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

## Refs

- Spec §14 M8 row.
- `deploy/scripts/faas-m8-restore-drill.sh` — local-disk sibling.
- `deploy/ansible/roles/postgres_backup/tasks/main.yml` — installer.
- `deploy/systemd/faas-pg-basebackup-push.{service,timer}` — push unit pair.
- `pkg/wire/metrics.go` — `pg_backup_last_pushed_seconds` gauge.
- ADR-056 — design rationale.
