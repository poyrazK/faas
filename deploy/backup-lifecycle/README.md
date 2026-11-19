# Off-host backup lifecycle

Retention for the off-host backup store (`offhostbox:`) that the
PostgreSQL base backups, the WAL archive and the control-plane state
bundles are pushed to. The hosts write with an append-only identity, so
deleting old recovery points is the bucket's job, not theirs
(ADR-20261118203000).

| File | Store | Apply with |
|---|---|---|
| `gcs.json` | Google Cloud Storage | `gcloud storage buckets update gs://<bucket> --lifecycle-file=deploy/backup-lifecycle/gcs.json` |
| `s3.json` | S3-compatible (OVHcloud Object Storage, AWS) | `aws s3api put-bucket-lifecycle-configuration --endpoint-url <endpoint> --bucket <bucket> --lifecycle-configuration file://deploy/backup-lifecycle/s3.json` |

Both files encode the same policy:

- base backups and control-plane state bundles expire after 14 days;
- WAL expires after 16 days, two days after the oldest base backup it
  could still be needed for;
- unfinished multipart uploads are aborted after 2 days.

Change the two files together. `pkg/daemonunitspec` fails the build if
they disagree, if WAL would expire before the base backups it replays
onto, or if a prefix no longer matches what the backup units write.
The procedure, the check and the rollback are in
`docs/runbooks/PostgresBackup.md` ("Remote retention").
