# `postgres_backup` ansible role

Drops and enables the `faas-pg-basebackup.{service,timer}` pair (local
basebackup), the `faas-pg-basebackup-push.{service,timer}` pair
(off-host push through a provider-neutral rclone remote, issue #250), and the
hourly verified WAL-prune pair (issue #1695).

## What this role does

1. Validates the stable remote alias and logical backup paths. Provider
   credentials and endpoints stay inside the rclone configuration.
2. Installs `rclone` via apt (matches the distro PostgreSQL install pattern;
   no vendored binaries).
3. Creates `/var/lib/pgsql/basebackup` (postgres-owned, postgres group,
   mode `0750`) — the drill script's `LATEST_BB` parent.
4. Copies `faas-pg-basebackup.{service,timer}` into
   `/etc/systemd/system/`.
5. Creates `/etc/faas/secrets/storage-box/` (0700 root:root).
6. Copies `faas-pg-basebackup-push.{service,timer}` into
   `/etc/systemd/system/`.
7. Installs the verified basebackup-push and WAL-prune helpers.
8. Runs `systemctl daemon-reload`, then enables + starts the timers.
9. Asserts `/var/lib/pgsql/basebackup` mode is `≤ 0750` (spec §11).
10. Asserts any provisioned `/etc/faas/secrets/storage-box/{rclone.conf,box-age-key}`
   are 0400 root:root (issue #250 fail-closed, spec §11).

## Why no PG config changes

The `postgres` role already configures `wal_level = replica`,
`archive_mode = on` (both restart-required settings), `archive_command` (issue #250 rewrites the
local-only `cp` baseline to local-first + best-effort rclone push),
and `max_wal_senders = 3` (`roles/postgres/tasks/main.yml:118-156`).
The basebackup + push services only need those + the running
cluster — no new GUCs in this role.

## Idempotency

`copy` overwrites cleanly; `systemd` enable+start is a no-op on a converged
box; the mode assertions are read-only. Re-running on a converged host
produces zero `changed`.

## Carve-outs

- The role does NOT run `pg_basebackup` itself — only enables the timer.
  Operators who need an immediate backup use `make backup-pg` (Makefile).
- The role does NOT push itself — only enables the push timer.
  Operators who need an immediate push use `make backup-push-pg`
  (Makefile) — same `systemctl start faas-pg-basebackup-push.service`
  under the hood.
- The role does NOT manage the timer schedule — `OnCalendar` lives in
  the unit file; editing it is a one-line `copy` change.
- The basebackup push keeps the newest two local backups after remote byte
  verification. The object-store lifecycle owns longer remote retention.
- WAL pruning uses the oldest retained basebackup as its boundary and refuses
  deletion unless every candidate passes an off-host byte check.

## Refs

- Spec §14 M8 row.
- Issue #250 acceptance matrix.
- `deploy/scripts/faas-m8-restore-drill.sh` (local-disk sibling drill).
- `deploy/scripts/pg-restore-verify.sh` (off-host T-7 verify).
- `docs/runbooks/PostgresBackup.md` (operator runbook).
- `docs/drills/TEMPLATE-restore-drill.md` (drill record format).
