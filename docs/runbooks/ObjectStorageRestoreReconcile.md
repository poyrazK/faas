# ObjectStorageRestoreReconcile

Trigger: a control-plane PostgreSQL restore (`ControlPlaneStateBackup.md`
step 6), or any time the bucket catalog is suspected to disagree with a
provider.
Tool: `gregalectl object-storage reconcile` on a control-plane node, wrapped by
`make ANSIBLE_INVENTORY=… object-storage-reconcile`
(`deploy/ansible/object_storage_reconcile.yml`). It is read-only: it lists each
backend's `gregale-*` buckets and the catalog rows bound to that backend, and
reports where they disagree. It never creates, changes or deletes a bucket.
Decision record: [ADR 20261025111015](../adr/20261025111015-object-storage-managed-gateway-and-restore-reconcile.md).
Severity: blocks re-enabling `s3_enabled` after a restore.

## Symptom

A restored catalog is as old as the backup. Every bucket created or deleted
after the restore point disagrees with its provider:

- **`provider_only`**: a physical bucket with no catalog row. Gregale cannot
  route, meter or bill it, and its customer cannot reach it. Usually a bucket
  created after the restore point; otherwise a leaked creation.
- **`catalog_missing`**: a `ready` or `deleting` catalog bucket whose physical
  bucket is gone. Usually a bucket deleted after the restore point. Its
  inventory fails, so the gateway answers 503 on that bucket only.
- **`deleted_present`**: a `deleted` catalog bucket whose physical bucket
  still exists. A deletion that did not finish at the provider. Its data is
  unbilled.

Buckets in `provisioning` or `failed` are never reported missing: the recovery
worker has not created them yet.

## Check

1. Keep `s3_enabled` false. The restore brought it back as it was at the
   restore point; `gregalectl object-storage hold --db-env
   /etc/faas/sealed.env` turns it off in the database without apid
   (`ControlPlaneStateBackup.md` step 5). Run the reconcile from the operator
   checkout:

   ```bash
   make ANSIBLE_INVENTORY=deploy/ansible/.generated/inventory/hosts.ini object-storage-reconcile
   ```

   Exit 0 means every backend agrees with the catalog. Findings stop the play
   for review; each line names the kind, backend, physical name, and, for
   catalogued buckets, the bucket ID, account ID and state. On the host,
   `gregalectl object-storage reconcile --db-env /etc/faas/sealed.env --json`
   prints the same report (exit 4 with findings, 3 on error).
2. Note the restore point (the basebackup or recovery target time).
3. For each finding, read the provider's own record, which a database restore
   does not roll back. The catalog's audit rows were restored with it. On GCS:

   ```bash
   gcloud storage buckets describe gs://<physical_name> \
     --format='value(creation_time,labels)'
   ```

   The `gregale-backend` label is the first 32 characters of the backend's
   configuration fingerprint, not its ID; a bucket without a matching label
   was not made by this backend's current configuration. The provider's admin
   activity audit log records when a bucket was created or deleted, but its
   actor is Gregale's own service account, never the customer. The physical
   name is `gregale-` plus the bucket ID without dashes. Two backends that
   share a provider account or project list each other's buckets; the
   reconcile leaves out a bucket another backend's catalog holds.

## Recover

Never delete a physical bucket that holds customer data without the incident
owner's sign-off.

1. **`provider_only`, created after the restore point.** A customer created
   this bucket, and the restore lost its row, and with it the only record of
   which account owns it: nothing at the provider names the customer. Keep
   it, and give its data to no one until an owner is proven out of band: a
   support ticket or customer record naming this bucket ID, confirmed by the
   account owner, with the incident owner's sign-off. Then tell that owner the
   bucket must be recreated in Gregale and copy its data into the new bucket
   on request. Delete the orphan only after the incident's retention window
   and sign-off.
2. **`provider_only`, older than the restore point or unlabeled.** A leak, or
   a bucket another system made in this namespace. Investigate before acting;
   the namespace is supposed to be Gregale's alone.
3. **`catalog_missing`, deleted after the restore point.** Confirm the delete
   in the provider audit log, then retire the row: the customer (or support,
   with the customer's consent) runs `gregale bucket delete <app> <bucket-id>`.
   A deletion that finds the physical bucket gone succeeds. Rows already in
   `deleting` finish on their own through the recovery worker.
4. **`catalog_missing`, not deleted by anyone.** Treat it as provider data
   loss: open a provider incident and keep the row; do not delete it.
5. **`deleted_present`.** The catalog finished a deletion the provider did not.
   With sign-off, empty and delete the physical bucket at the provider.
6. Rerun the reconcile until it exits 0, or until every remaining finding has
   a recorded decision. Then run it once more with `ACCEPT=1`, which lists the
   findings without stopping.
7. Re-apply the revocations the restore undid (next section), then turn
   `s3_enabled` back on.

## Revocations undone by the restore

The restore brought back every S3 key, copy source, compute binding and
access grant that was revoked after the restore point, and the audit rows
that recorded the revocations went with them. They work again as soon as
`s3_enabled` is on, so re-apply them first.

1. apid also logs each revocation as `object storage access revoked`, with
   its `kind` (`object_storage.s3_credential_revoked`,
   `object_storage.copy_source_revoked`,
   `object_storage.compute_binding_revoked` or
   `object_storage.access_grant_deleted`), `account_id` and IDs. The log
   pipeline keeps it outside the database. In Grafana Explore on Loki:

   ```logql
   {job="faas-journal", unit="faas-apid.service"} |= "object storage access revoked" | json
   ```

   over the window from the restore point to the failure.
2. For each line, re-apply it with the customer's consent, or as support
   with sign-off, through the same API route: `DELETE` on
   `/v1/apps/{slug}/buckets/{bucket}/s3-credentials/{credential_id}`,
   `.../s3-credentials/{credential_id}/copy-sources/{source_bucket_id}`,
   `.../compute-bindings/{binding_id}` or `.../access-grants/{key_id}`.
3. If the logs do not cover the whole window (Loki keeps 14 days), the undone
   revocations cannot be listed. Revoke every S3 key, copy source and compute
   binding issued before the restore point, with the incident owner's
   sign-off, and tell each account owner to issue new ones.
