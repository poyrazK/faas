# ObjectStorageRestoreReconcile

Trigger: a control-plane PostgreSQL restore (`ControlPlaneStateBackup.md`
step 6), or any time the bucket catalog is suspected to disagree with a
provider.
Tool: `gregalectl object-storage reconcile` on a control-plane node, wrapped by
`make ANSIBLE_INVENTORY=… object-storage-reconcile`
(`deploy/ansible/object_storage_reconcile.yml`). It is read-only: it lists each
backend's `gregale-*` buckets and the catalog rows bound to that backend, and
reports where they disagree. It never creates, changes or deletes a bucket.
Decision record: [ADR 20261025090000](../adr/20261025090000-object-storage-managed-gateway-and-restore-reconcile.md).
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

1. Keep `s3_enabled` false. Run the reconcile from the operator checkout:

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

   The `gregale-backend` label must match the backend; a bucket without it was
   not made by this backend. The provider's admin activity audit log records
   who created or deleted the bucket and when. The physical name is
   `gregale-` plus the bucket ID without dashes.

## Recover

Never delete a physical bucket that holds customer data without the incident
owner's sign-off.

1. **`provider_only`, created after the restore point.** The customer created
   this bucket, and the restore lost its row. Keep it. Tell the account owner
   (from the provider audit log or support records) that the bucket must be
   recreated in Gregale; copy its data into the new bucket on request. Delete
   the orphan only after the incident's retention window and sign-off.
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
   findings without stopping, and turn `s3_enabled` back on.
