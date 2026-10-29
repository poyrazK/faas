# Migration rollback (issue #579)

Schema migrations are applied by `bin/migrate` from the release being
deployed. `deployctl deploy` runs it on the control plane after preflight and
before activating the new daemons. Migrations are never rolled back with a
down step. A bad migration is recovered by either:
- a **forward fix**, a new migration that repairs the schema; or
- a **restore** of the database to the moment before the deploy.

## What protects a deploy

- **Pre-migration backup.** When `migrate -status` reports pending migrations,
  deployctl first starts `faas-pg-basebackup.service` (a fresh local base
  backup), then `faas-pg-basebackup-push.service` (ships it off host).
  - If either unit fails, the deploy stops before any migration runs:
    `pre-migration backup failed; refusing to apply N pending migrations`.
  - A host without these units (a development box) migrates without a backup.
  - A deploy with nothing pending takes no backup.
- **Timeouts.** The migrator's sessions run with `statement_timeout = 30s` and
  `lock_timeout = 10s` (`pkg/db`, `MigratorAppName`).
  - A statement that runs past 30 s fails the deploy, and so does a lock
    that can't be taken within 10 s.
  - Each migration file also sets `SET LOCAL lock_timeout = '5s'` before
    touching an existing table (`make migration-lint`).
  - A migration that genuinely needs longer, such as a large backfill, raises
    its own bound for its transaction with `SET LOCAL statement_timeout = '…'`.
    It says why in a comment.
- **Serialization.** `db.MigrateUp` holds a Postgres advisory lock, so two
  migrators never interleave.
- **Expand/contract.** New migrations are expand/contract
  (`docs/release-compatibility.md`, `make migration-lint`). Release N-1 keeps
  working against N's schema, so a failed deploy leaves the old daemons
  serving.

## When a migration fails during deploy

1. **Read the failure.** deployctl prints `deploycontroller: migrate <release>: …`
   and leaves the current release active. The old daemons are still serving.
   - A timeout reads `canceling statement due to statement timeout` or
     `lock timeout`.
   - A refused backup reads `pre-migration backup failed`.
2. **Check what was applied.** Each migration file runs in its own
   transaction, so a failed file leaves nothing behind.
   - `DATABASE_URL=… /opt/faas/current/bin/migrate -status` shows `db_version`
     and what is still pending.
   - Earlier files of the same deploy may have committed. Under
     expand/contract they are additive, so N-1 keeps working.
3. **Fix forward.** Fix the migration in a new PR and deploy again.
   - Never edit an applied migration; `migrations/` is append-only.
   - For a lock timeout, find the blocker first:
     `SELECT pid, state, wait_event, query FROM pg_stat_activity WHERE datname = current_database();`
     Long-running queries and idle-in-transaction sessions are the usual
     cause.
4. **Backup failures.** Follow `docs/runbooks/PostgresBackup.md`. Don't bypass
   the backup to get a deploy out; fix the backup, then deploy.

## When a deployed migration damaged data

A forward fix can't recover lost rows. Restore the database to the moment
before the deploy:
1. Freeze deploys (`docs/runbooks/DeployFreeze.md`) and stop traffic to the
   control plane.
2. Restore the pre-migration base backup, the newest one before the deploy's
   start time, with WAL replay up to just before the migration. Follow the
   restore procedure and verification in `docs/runbooks/PostgresBackup.md`
   (`deploy/scripts/pg-restore-verify.sh` restores and checks a backup
   off-host first).
3. Deploy the previous release (`deployctl rollback`). Its daemons match the
   restored schema.
4. Write a postmortem (`make postmortem`) recording what was restored and from
   when.
