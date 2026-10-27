# ADR: Manifest-managed S3 gateway and restore reconciliation

Date: 2026-10-25
Status: Accepted
Related: [ADR-143](143-deploy-configuration-contract.md), [ADR 20261023223000](20261023223000-object-storage-bucket-scoped-availability.md), [ADR 20261024132844](20261024132844-gateway-metered-object-storage-billing.md)

## Context

Two operational gaps stand between managed object storage and GA:

- **The gateway is not declared anywhere.** The `s3_gateway_service` role
  installs and health-gates `s3-gatewayd`, but only when an inventory sets
  `faas_object_storage_gateway_managed`. The deploy manifest had no field for
  it, so `gregalectl manifest ansible` never rendered those values. A gateway
  started by hand ran with config nobody could rebuild, and fleet
  verification did not notice.
- **A database restore leaves the catalog and providers apart.** The bucket
  catalog lives in PostgreSQL; buckets live at the providers. After a
  control-plane restore, every bucket created or deleted since the restore
  point disagrees, and nothing reported which. A bucket with no catalog row is
  unmetered and unbilled; a catalog bucket with no physical bucket fails its
  inventory. The recovery worker deliberately never discovers unknown
  provider buckets, so it cannot close this gap.

## Decision

1. **The manifest declares the gateway.** `object_storage.gateway: managed`,
   with absolute `config_src` and optional `provider_env_src` paths on the
   operator's machine, renders `faas_object_storage_gateway_managed: true`
   and both source paths into the control-plane host's vars. Compute hosts
   get none. Paths without `managed` fail validation, as does any value other
   than `managed`, `off` or empty.
2. **Strict fleet verification fails on an unmanaged gateway.** On a
   control-plane node where `faas-s3-gatewayd` is active but the gateway is
   not managed, `make verify-fleet` fails and the lenient release verification
   warns. Releases are not blocked; rebuilding a host is.
3. **Restore reconciliation is a read-only operator command.**
   `gregalectl object-storage reconcile` runs on a control-plane node with the
   node's provider registry and credentials. For each backend it lists the
   physical buckets whose names start with `gregale-` and the catalog rows
   bound to that backend and fingerprint, and reports three kinds of
   disagreement: `provider_only`, `catalog_missing` (a `ready` or `deleting`
   row), and `deleted_present`. Rows still `provisioning` or `failed` are not
   expected to exist. It exits 4 on findings and 3 on error. It never changes
   a bucket or a row: each finding needs a human decision about customer data.
4. **Listing buckets is an optional provider capability** (`BucketLister`),
   implemented for S3-compatible providers (`ListBuckets` with a prefix,
   filtered again client-side) and GCS (`Buckets` in the backend's project).
   A backend that cannot list fails the reconcile instead of reporting it
   clean. A listing over `api.MaxObjectStorageReconcileBuckets` (100,000)
   fails too.
5. **The restore runbook runs it.** `make object-storage-reconcile` runs the
   command on the control plane and stops for review on findings;
   `ACCEPT=1` records reviewed findings. `ControlPlaneStateBackup.md` runs it
   before `s3_enabled` is turned back on, and
   `ObjectStorageRestoreReconcile.md` gives the remedy for each finding.

## Consequences

- An existing hand-started gateway fails strict verification until its
  operator adds `object_storage.gateway: managed` to the production manifest
  and runs bootstrap. Lenient release verification only warns.
- The reconcile needs bucket-listing permission in each backend's namespace
  (GCS `storage.buckets.list` on the project; S3 `ListAllMyBuckets`), on
  the credentials the control-plane node already holds.
- The provider namespace must be Gregale's alone. A foreign bucket named
  `gregale-*` shows up as `provider_only`; on GCS its missing
  `gregale-backend` label identifies it.
- The physical-name prefix is now a named constant
  (`objectstorage.PhysicalBucketPrefix`) shared by creation and
  reconciliation.

## Validation

- `TestObjectStorageGatewayValidation` and
  `TestRenderManifestAnsibleFiles_ObjectStorageGateway` cover the manifest
  field and that only the control-plane host gets the gateway vars.
- `TestReconcileBucketsFindsEveryDisagreement` covers each finding kind and
  the states never reported missing.
- `TestS3ListBucketNamesFiltersToGregaleBuckets` checks the prefix request
  and the client-side filter; `TestGCSListBucketNamesNeedsTheRealStore`
  checks that a store which cannot list is unsupported, not empty.
- `TestReconcileObjectStorageReportsRestoreDrift` runs the command's core
  against a catalog and a provider that disagree in both directions.

## Amendment 1 (2026-10-26): the restore fences object storage

`s3_enabled` is a row in the control-plane database. A restore brings it
back as it was at the restore point, usually on, and with it every S3 key,
copy source, compute binding and access grant revoked after that point. The
runbook's `gregalectl object-storage hold` helps only when someone runs it.

- **Decision.** Object storage serves only on an accepted PostgreSQL
  timeline. `object_storage_timeline_acceptance` holds the timeline last
  accepted; the S3 gateway and the public-read edge serve while
  `s3_enabled` is on *and* the database runs on that timeline
  (`runtimeconfig.ObjectStorageFence`, re-read every 5 s, closed until its
  first read, last answer kept on a failed read). The documented restores
  recover from the WAL archive and promote, which starts a new timeline, so
  a restore fences object storage by itself until
  `gregalectl object-storage accept`, after the reconcile and the
  revocation review.
- **Who accepts.** The first start of an apid that knows the fence accepts
  the current timeline once (`bootstrap`), so later backups carry an
  acceptance. A planned switchover loses nothing, and `postgres_promote.yml`
  accepts the new timeline itself; after a failover, the operator accepts it
  once the runbook's review is done. The restore drill accepts after it has
  proved its restore lossless.
- **Reading the timeline** needs no privilege:
  `pg_walfile_name(pg_current_wal_lsn())` starts with it on a primary.
- **Limits.** A database with no acceptance (a backup taken before this
  release) is not fenced; the runbook's hold still covers it. A restore
  that does not promote onto a new timeline is not detected.
- **Copy sources.** The copy-time resolver also requires the source and
  destination buckets to be of one organization, as granting already did.
