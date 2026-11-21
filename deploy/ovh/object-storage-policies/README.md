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
| `gregale-gcp-fleet-artifacts` | `gregale-gcp-fleet-artifacts.json` | `gregale-prod-artifacts` | yes: one key for the whole GCP fleet | the `COMPUTE_STORAGE_ENV` secret, until the cutover |
| `gregale-pg-backups` | `gregale-pg-backups.json` | `gregale-prod-pg-backups` | no: append-only | the backup `rclone.conf` on `bhs-1` |

`gregale-gcp-fleet-artifacts` exists only while the GCP fleet writes to
the OVH bucket (`GcpToOvhMigration.md` phase 1 steps 3–6). CD installs
one `storage.env` from one secret on every node and the control plane, so
that key needs the control plane's delete right, the same right the GCP
service accounts hold on the GCS bucket today. Delete the user at the
cutover.

Compute nodes on OVH are meant never to delete: they run with
`FAAS_STORAGE_DELETE_MODE=defer` and the control plane's object GC does
the deleting (ADR-20261115180000). CD cannot install per-node keys yet
(`GcpToOvhMigration.md`, "Keys"). The log archive key deletes because account
erasure removes `faas-logs/{instance}/` (`pkg/logarchive`,
`docs/runbooks/FaasAccountErasureDeferred.md`). The backup key cannot
delete, so a stolen control plane cannot erase its own backups; only the
bucket lifecycle removes recovery points (ADR-20261118203000).
`pkg/daemonunitspec` fails the build if a policy names another bucket or
if a node or backup policy gains a delete or bucket-admin action.

## Apply

Create the user in Public Cloud, Users & Roles (role ObjectStore
operator). The policy is not there: in Object Storage, the Users tab, the
user's `...` menu, "Import a user access policy (JSON)", and upload the
file (the manager has no paste field). "Enable" in the same menu creates
the S3 key.
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

## Putting a key on a host

`deploy/ovh/install-s3-key.sh` writes a key where a host reads it. It reads
the key pair in your terminal (the secret without echo) and sends it on the
SSH session's stdin, so it never reaches a command line, a log or the
screen. The program that merges it runs as root on the host, keeps every
other line, leaves the previous file as a root-only `.bak-<time>` copy and
prints key names only. Copy each secret from the user's "View the secret
key" in the manager.

| What | Command | File |
|---|---|---|
| a GCP compute node's artifact store, with the GCS read fallback (migration phase 1 step 4) | `install-s3-key.sh storage-env --ssh 'gcloud compute ssh <node> --zone <zone> --tunnel-through-iap --' --bucket gregale-prod-artifacts --fallback gcs` | `/etc/faas/storage.env`, root:faas 0440 |
| an OVH compute node's artifact store (node join) | `install-s3-key.sh storage-env --ssh 'ssh ubuntu@<host>' --bucket gregale-prod-artifacts --delete-mode defer` | same |
| the OVH control node's backup remote (phase 1 step 8) | `install-s3-key.sh rclone --ssh 'ssh ubuntu@<host>' --bucket gregale-prod-pg-backups` | `/etc/faas/secrets/storage-box/rclone.conf`, root 0400: `[offhostbox]` is an alias of `[offhostbox-s3]:<bucket>`, so the backup paths stay `offhostbox:faas-pg-*` |
| the log archive key on the Ansible controller | `install-s3-key.sh log-archive --out <log_archive.credentials_src>` | that file, 0600 |

Add `--dry-run` to see which keys would change without writing anything.
The script does not restart anything: drain, restart and undrain as the
runbook says. Tests: `install-s3-key_test.sh` (also in CI).
