# OVHcloud Object Storage user policies

One S3 user per purpose in the production Public Cloud project, each with
the policy in this directory. The keys and their rights are listed in
`docs/runbooks/GcpToOvhMigration.md` ("Keys"); these files are the exact
policies imported on 2026-11-19. Nothing a host runs reads them.

| User | Policy | Bucket | Delete? | Key lives in |
|---|---|---|---|---|
| `gregale-cp-artifacts` | `gregale-cp-artifacts.json` | `gregale-prod-artifacts` | yes: object GC | `storage.env` on `bhs-1` |
| `gregale-bhs-2-artifacts` | `gregale-bhs-2-artifacts.json` | `gregale-prod-artifacts` | no | `storage.env` on `bhs-2` |
| `gregale-bhs-3-artifacts` | `gregale-bhs-3-artifacts.json` | `gregale-prod-artifacts` | no | `storage.env` on `bhs-3` |
| `gregale-log-archive` | `gregale-log-archive.json` | `gregale-prod-log-archive` | yes: account erasure | `log_archive.credentials_src` on the controller |
| `gregale-pg-backups` | `gregale-pg-backups.json` | `gregale-prod-pg-backups` | no: append-only | the backup `rclone.conf` on `bhs-1` |

Compute nodes never delete: they run with `FAAS_STORAGE_DELETE_MODE=defer`
and the control plane's object GC does the deleting
(ADR-20261115180000). The log archive key deletes because account
erasure removes `faas-logs/{instance}/` (`pkg/logarchive`,
`docs/runbooks/FaasAccountErasureDeferred.md`). The backup key cannot
delete, so a stolen control plane cannot erase its own backups; only the
bucket lifecycle removes recovery points (ADR-20261118203000).
`pkg/daemonunitspec` fails the build if a policy names another bucket or
if a node or backup policy gains a delete or bucket-admin action.

## Apply

In the manager: Public Cloud, Users & Roles, the user's `...` menu,
"Import JSON policy" and upload the file. The manager has no paste field.
Read it back with `GET /cloud/project/{serviceName}/user/{userId}/policy`.

The backup user owns `gregale-prod-pg-backups`, so prove OVH enforces the
policy rather than owner rights. Run `check-backup-key-append-only.sh` with
the backup key (from the user's "View the secret key"): it writes a probe
under `faas-cp-state/` and must be refused deleting it or rewriting the
lifecycle. Run it again after any key rotation.

Add `no_check_bucket = true` to the S3 remote in each `rclone.conf`: the
keys have no bucket-create right, and rclone otherwise tries to create
the bucket before its first upload.

## Buckets

All three are in `BHS`, Standard, with server-side encryption.
`gregale-prod-pg-backups` was created with Object Lock, which OVH allows
only at creation and which also turns on versioning.

| Bucket | Lifecycle |
|---|---|
| `gregale-prod-pg-backups` | `deploy/backup-lifecycle/s3.json` |
| `gregale-prod-log-archive` | `faas-logs/` expires after 90 days, older versions after 7 (`docs/runbooks/FaasLogArchiveShipperDegraded.md`) |
| `gregale-prod-artifacts` | none: the control plane's object GC owns deletes |

A lifecycle write replaces the bucket's whole configuration. The
manager's "Create rule" sends every rule it last read plus the new one,
so creating rules back to back from a stale page drops one. Prefer one
`aws s3api put-bucket-lifecycle-configuration` with the whole file;
in the manager, reload between rules and read the configuration back
with `GET /cloud/project/{serviceName}/region/BHS/storage/{name}/lifecycle`.
