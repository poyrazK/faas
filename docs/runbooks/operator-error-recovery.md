# Recovering from an operator error

Use this when a person, script or migration we ran changed or removed data it
should not have: a wrong `gregalectl` target, a migration with a bad
predicate, a cleanup job pointed at the wrong scope, a bad bulk operation
through the API.

The rule is **recover the affected rows, not the whole database**. Restoring
the production cluster to an earlier time throws away every write made since,
by every customer. Do that only when the damage is wider than what a
targeted repair can fix, and only with the platform owner's approval.

Severity by the matrix in [incident-response.md](incident-response.md);
confirmed customer data loss is SEV1.

## 1. Stop the damage (first 10 minutes)

1. Stop whatever is still running: cancel the command, disable the timer,
   pause the job. If it is a migration in a deploy, open a `deploy-freeze`
   issue ([DeployFreeze.md](DeployFreeze.md)) and follow
   [migration-rollback.md](migration-rollback.md).
2. If background work could spread the damage (reconcilers deleting what no
   longer matches, deletion graces expiring), pause new releases and hold
   the loops that act on the affected data.
3. **Write down the time.** The last good moment, as exactly as you can,
   in UTC. Postgres logs, `gregalectl accounts activity`, the audit log and
   the shell history of whoever ran it all help. Everything below depends on
   this timestamp.
4. Do not run more ad-hoc writes to "fix" it while diagnosing.

## 2. Check for a product-level undo first

Many deletions are soft and have their own restore path, which is safer than
anything below:

| What was lost | Undo |
|---|---|
| An app | `gregale apps restore <slug>` within the deletion grace ([FaasAppDeletionGraceDegraded.md](FaasAppDeletionGraceDegraded.md)) |
| An account that was **suspended** (not deleted) | `gregalectl accounts restore --account-id <uuid> --reason <reason> --yes` returns it to active. It does not bring back a deleted or purged account; for that, go to §3 |
| A deployment | roll back to the previous deployment through the API or CLI |
| A platform flag | set it back ([FaasPlatformFlagPastRetireBy.md](FaasPlatformFlagPastRetireBy.md)) |
| Data a stuck repair touched | [FaasDataRepairStuck.md](FaasDataRepairStuck.md) |

If one of these covers everything, use it and skip to §5.

## 3. Restore a copy to the moment before

Restore onto a **throwaway instance on another port**, never over the live
cluster. The throwaway restore in `deploy/scripts/pg-restore-verify.sh` is
the reference: it pulls a basebackup from `offhostbox`, writes
`recovery.signal` and a `restore_command` that fetches WAL with rclone, and
starts PostgreSQL on port 5433 under `/var/lib/pgsql/restore-test/`.

Do the same by hand, with two differences:

1. Pick the newest basebackup **older** than the timestamp from §1
   (`rclone lsf offhostbox:faas-pg-basebackup/`).
2. Add a recovery target before starting the instance, so replay stops
   just before the error and waits:

   ```ini
   recovery_target_time = '2026-11-07 09:41:00+00'
   recovery_target_action = 'pause'
   ```

Once it pauses, check the data in read-only queries. If the error is still
visible, the target is too late. Rebuild with an earlier time; a paused
recovery can move forward, but never back.

The restore contains every customer's data. Run it on the control plane,
keep it on the host, and delete it when you are done (§5).

## 4. Put the rows back

1. Export only what was lost, with read-only commands against the restored
   copy: `COPY (SELECT ...) TO STDOUT` or `pg_dump --data-only --table=...`
   for whole tables. Narrow by account, app or primary key.
2. Diff against live: some rows may have legitimately changed since.
3. Re-apply through a reviewed path, never by pasting SQL into a production
   session:
   - prefer a product command or API that recreates the objects;
   - otherwise write a repair in code (a data repair or a migration) that a
     second person reviews, with a dry run that prints what it would write.
4. Sealed columns come back still sealed to the identities of the restore
   point. If an identity has rotated since, the restored ciphertext needs
   the old identity to open. Do not ask customers to re-enter secrets until
   you know it cannot be recovered.
5. Objects in object storage are not in the database restore. Bucket
   contents survive a database restore, but metadata that points at them may
   not. See [ObjectStorageRestoreReconcile.md](ObjectStorageRestoreReconcile.md).

## 5. Close

1. Stop and delete the throwaway instance and its data directory.
2. Tell affected customers what was lost, for how long, and what was
   restored. Never name the operator.
3. Post-mortem is required for any customer data loss. Ask why one command
   could do this much, not who ran it, and add the missing guard (dry-run
   default, scope confirmation, a smaller blast radius).

## Whole-cluster rollback (last resort)

If the damage is too broad for a row-level repair, the cluster can be
restored to the timestamp from §1 with the full rebuild in
[ControlPlaneStateBackup.md](ControlPlaneStateBackup.md), adding the same
`recovery_target_time`. Every write after that moment is lost: deploys,
billing events, customer data. Platform owner approval, a status-page
incident and a customer notice are required before you start.
