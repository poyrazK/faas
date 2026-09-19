# GCS artifact migration

Gregale's GCP beta fleet keeps its canonical Firecracker artifacts in a
private, regional Cloud Storage bucket. The bucket and all production VMs are
in `europe-west3`, avoiding cross-location data transfer. Daemons authenticate
with Application Default Credentials from the service account attached to the
VM; service-account key files are forbidden.

## Infrastructure gate

Run the public-beta identity convergence before changing `storage.env`. It
creates `gregale-artifacts-5ae37259` with uniform bucket-level access and
public access prevention, and grants bucket-scoped `roles/storage.objectUser`
to only the Gregale compute and control-plane service accounts. The control
plane must use `gregale-control`, not the default Compute Engine identity.

The read-only audit must report the bucket in `EUROPE-WEST3`, storage class
`STANDARD`, both required IAM members, and no public IAM member.

## Reader-first cutover

1. Roll out a release containing the GCS backend while production still uses
   `FAAS_STORAGE_BACKEND=oci`.
2. Converge the bucket, bucket IAM, and dedicated VM identities.
3. Replace the fleet-wide storage contract with:

   ```text
   FAAS_STORAGE_BACKEND=gcs
   FAAS_GCS_BUCKET=gregale-artifacts-5ae37259
   FAAS_STORAGE_FALLBACK_BACKEND=oci
   FAAS_OCI_REGISTRY=https://ghcr.io
   FAAS_OCI_REPO_PREFIX=poyrazk/faas-e2e-20260818
   FAAS_STORAGE_LOCAL_PREFIXES=none
   FAAS_REQUIRE_SHARED_ARTIFACTS=1
   FAAS_STORAGE_CACHE_SERVE_STALE=0
   FAAS_STORAGE_SNAPSHOT_COMPRESSION=zstd
   ```

4. Restart one drained compute node, verify kernel publication, deploy, park,
   wake, rollback, and snapshot GC, then continue the rolling restart.

New artifacts are written only to GCS. A key missing from GCS is read from
OCI so pre-cutover deployments remain rollbackable. Permission, timeout, and
other GCS errors do not fall back: they fail visibly rather than serving a
potentially stale generation. Deletes and listings cover both stores during
the migration.

## Retire GHCR

Keep the fallback for at least the platform rollback-retention window. Before
removal, verify no live or rollback-eligible deployment references an
OCI-only key. Then remove `FAAS_STORAGE_FALLBACK_BACKEND` and all `FAAS_OCI_*`
settings from `storage.env` and remove the imaged-only GHCR credential. Do not
delete the GHCR packages until a pure-GCS rollout and rollback drill pass.
