# ADR-20261118203000: Off-host backups keep fourteen days

- **Status:** proposed
- **Date:** 2026-11-18
- **Amends:** ADR-056 (off-host PostgreSQL backup)
- **Related:** ADR-1344 §8 (control-plane state backup), ADR-151
  (provider-neutral object storage)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

The control plane pushes three things to the off-host store
(`offhostbox:`):

- a base backup every night (`faas-pg-basebackup/`);
- every WAL segment, within about a minute (`faas-pg-wal/`);
- the control-plane state bundle, once it is enabled (`faas-cp-state/`).

The hosts write with an append-only identity (`deploy/gcp/backup-writer-role.yaml`
grants create, get and list, never delete), so a stolen control plane
cannot erase its own backups. ADR-056 and the backup units say remote
retention belongs to the bucket's lifecycle policy. No change ever
defined that policy, and no operator step set one.

Production as measured on 2026-11-18 (Cloud Monitoring
`storage/v2/total_bytes`, bucket `gregale-prod-280686321025-pg-backups`):

| Prefix | Size | Since |
|---|---|---|
| `faas-pg-basebackup/` | 24.7 GB, 50 nightly backups | 2026-09-30 |
| `faas-pg-wal/` | about 808 GB | the first segment |
| total | 832 GB, growing about 25 GB a day | |

The WAL rate comes from `archive_timeout = 60s` (ADR-1344): PostgreSQL
switches to a new 16 MiB segment at least once a minute, so even a quiet
server archives about 23 GB a day. Nothing is ever deleted, so the
bucket grows by about $15 a month every month at GCS list price.

## Decision

1. **Base backups and control-plane state bundles expire after 14
   days.** That gives two weeks of point-in-time recovery. A restore
   needs the database and the state bundle from the same night, so both
   use the same window.
2. **WAL expires after 16 days.** WAL is useful only after the oldest
   base backup it can replay onto. Lifecycle runs are asynchronous and
   compare whole days, so WAL is kept two days longer than the base
   backups.
3. **Unfinished multipart uploads are aborted after 2 days.** A push
   that dies halfway leaves billed parts no listing shows.
4. **The policy lives in the repository**, as `deploy/backup-lifecycle/gcs.json`
   and `deploy/backup-lifecycle/s3.json`. The operator applies it when
   provisioning the bucket, with their own credentials; the hosts never
   get delete or bucket-admin rights. `pkg/daemonunitspec` fails the
   build if the two files disagree, if WAL could expire before its base
   backups, or if a prefix no longer matches what the backup units write.

The 14-day window holds up when backups break. `PgBackupStale` fires
when the newest verified push is 26 hours old, and `PgBaseBackupFailed`
fires as soon as a nightly backup fails. Either leaves 13 days before
the last good base backup expires. GCS soft delete (7 days on the
production bucket) keeps lifecycle-deleted objects restorable for another
week.

## Cost

These are steady-state estimates at about 23 GB of WAL and 0.85 GB of
base backup a day. GCS also bills deleted objects for the 7 days of soft
delete; an unversioned OVH bucket keeps nothing after expiry. Prices are
list: GCS Standard us-east1 $0.020 and OVH Standard BHS $0.0095 per
GB-month.

| Policy | Stored GB | GCS billed GB | GCS $/month | OVH $/month |
|---|---|---|---|---|
| no lifecycle (today) | 832, +25 a day | 832, +25 a day | 16.6, +15 each month | — |
| 14 days (this ADR) | about 380 | about 550 | about 11 | about 3.6 |
| 7 days | about 215 | about 380 | about 7.6 | about 2.0 |

## Not decided here

- **Compressing WAL before upload.** A sample of 120 production segments
  (2.0 GB) compressed to 96 MB with `zstd -3`, about 21 times smaller,
  because a segment closed by `archive_timeout` is mostly zeros.
  Compression would bring the 14-day window down to about 40 GB. But it
  changes the remote format that `archive_command`,
  `faas-pg-wal-prune.sh` (`rclone check`), `pg-restore-verify.sh` and
  the restore drill rely on, so it needs its own change and restore
  drill.
- **Object lock / WORM**, still deferred as in ADR-056.

## Consequences

- Recovery points older than 14 days are gone. A request to restore
  something older cannot be met from these backups.
- Applying the policy to an existing bucket deletes everything outside
  the window within about a day. On production that is about 360 GB of
  WAL from before 2026-11-02 and the 35 base backups from before
  2026-11-04. Soft delete keeps billing those bytes for a week, so the
  bill drops a week after the policy is applied, not on the day.
- The operator check after provisioning any backup bucket, and the
  rollback, are in `docs/runbooks/PostgresBackup.md` ("Remote
  retention").
