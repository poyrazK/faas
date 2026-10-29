# FaasObjectStorage

Alerts: `FaasObjectStorageInventoryFailing`, `FaasS3GatewayErrorRateHigh`,
`FaasObjectStorageUsageExportStale`, `FaasObjectStorageRegionNearlyFull`,
`FaasObjectStorageRegionFull`.
Sources: apid's inventory sweep (`pkg/apid/object/storage_accounting.go`),
`s3-gatewayd` (`pkg/s3gateway`) and its usage exporter
(`cmd/s3-gatewayd/main.go`).
Metrics: `faas_object_storage_inventory_scans_total{outcome}`,
`faas_object_storage_inventory_provider_requests_total`,
`s3_gateway_ops_total{op="s3_request",code}`,
`s3_gateway_usage_export_last_success_timestamp_seconds{backend}`,
`faas_object_storage_backend_buckets{region,backend}`,
`faas_object_storage_backend_bucket_limit{region,backend}`,
`faas_object_storage_region_full{region}`.
Decision records: [ADR 20261023223000](../adr/20261023223000-object-storage-bucket-scoped-availability.md),
[ADR 20261029174627](../adr/20261029174627-object-storage-backend-pools.md) (bucket placement).
Severity: warn.

## Symptom

- **Inventory failing.** Customers see `503 Object storage accounting is
  temporarily unavailable` on one bucket while the account's other buckets
  work. A bucket needs a successful inventory every 900 s.
- **Gateway 5xx.** S3 clients get `ServiceUnavailable` (provider unreachable
  or accounting stale) or `SlowDown` (another delete or write of the same
  key, or an inventory scan of the bucket, is still settling). Both count as
  5xx.
- **Usage export stale.** With provider-report accounting, admission refuses
  transfers on every bucket of that backend once reports exceed
  `max_report_age_seconds`.
- **Region nearly full or full.** Each customer bucket is its own provider
  bucket, and a provider project holds a limited number (OVH: 100, at most
  1,000 on request). Once every backend in a region's `pools` entry reaches
  its `max_buckets`, `CreateBucket` answers `503 object_storage_region_full`.
  Existing buckets keep working.

## Check

1. Inventory failures: `journalctl -u faas-apid | grep "object storage inventory failed"`
   lists the bucket and backend IDs. A bucket over about 1M keys, or one that
   cannot be listed within 45 s, never completes a scan.
2. Gateway errors: `journalctl -u faas-s3-gatewayd` around the alert window.
   Compare with the provider's status page. Many `SlowDown` answers on one
   bucket mean customer concurrency on the same keys, or an uncertain delete
   holding its key, not an outage.
3. Usage export: `s3_gateway_usage_exports_total{outcome}` by backend, and the
   exporter's provider credentials.
4. Capacity: `faas_object_storage_backend_buckets` against
   `faas_object_storage_backend_bucket_limit` per backend, and
   `journalctl -u faas-apid | grep "no backend with room"` for refusals.

## Recover

1. Provider outage: wait for the provider. Do not raise ceilings.
2. One bucket that cannot be listed: ask the customer to split the bucket or
   delete keys. Keep the operator key ceiling (`max_account_keys`) below what a
   45 s scan can list.
3. Usage export: rotate or repair the provider usage credentials in the
   operator config, then restart `faas-s3-gatewayd`.
4. Region capacity: add a provider project to the region's pool.
   1. Create the OVH Public Cloud project, its S3 user and keys, and ask OVH
      support for the project's 1,000-bucket quota.
   2. Add a backend to `/etc/faas/object-storage.json` with a new `id`, the
      project as `namespace`, its own key environment variables and
      `max_buckets` below the quota (950 for 1,000). Its driver, Object Lock,
      encryption algorithms and `allowed_origins` must match the pool's.
   3. Append its `id` to the region's `pools` list. Never reorder or remove
      a backend that still holds buckets; removing it from the pool only
      stops new placements.
   4. Put its keys in the provider environment file, then restart
      `faas-s3-gatewayd` and `faas-gatewayd-public` before `faas-apid`, so
      the gateways know the backend before apid places a bucket on it.
   5. Check `faas_object_storage_backend_bucket_limit` lists the backend and
      `faas_object_storage_region_full` is 0.
   Raising `max_buckets` above the provider's quota does not help: the
   provider then refuses the bucket and it stays in provisioning.
