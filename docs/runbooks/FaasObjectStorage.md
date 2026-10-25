# FaasObjectStorage

Alerts: `FaasObjectStorageInventoryFailing`, `FaasS3GatewayErrorRateHigh`,
`FaasObjectStorageUsageExportStale`.
Sources: apid's inventory sweep (`pkg/apid/object/storage_accounting.go`),
`s3-gatewayd` (`pkg/s3gateway`) and its usage exporter
(`cmd/s3-gatewayd/main.go`).
Metrics: `faas_object_storage_inventory_scans_total{outcome}`,
`faas_object_storage_inventory_provider_requests_total`,
`s3_gateway_ops_total{op="s3_request",code}`,
`s3_gateway_usage_export_last_success_timestamp_seconds{backend}`.
Decision record: [ADR 20261023223000](../adr/20261023223000-object-storage-bucket-scoped-availability.md).
Severity: warn.

## Symptom

- **Inventory failing.** Customers see `503 Object storage accounting is
  temporarily unavailable` on one bucket while the account's other buckets
  work. A bucket needs a successful inventory every 900 s.
- **Gateway 5xx.** S3 clients get `ServiceUnavailable` (provider unreachable
  or accounting stale) or `SlowDown` (the bucket is busy settling other
  deletions and writes). Both count as 5xx.
- **Usage export stale.** With provider-report accounting, admission refuses
  transfers on every bucket of that backend once reports exceed
  `max_report_age_seconds`.

## Check

1. Inventory failures: `journalctl -u faas-apid | grep "object storage inventory failed"`
   lists the bucket and backend IDs. A bucket over about 1M keys, or one that
   cannot be listed within 45 s, never completes a scan.
2. Gateway errors: `journalctl -u faas-s3-gatewayd` around the alert window.
   Compare with the provider's status page. Many `SlowDown` answers on one
   bucket mean customer concurrency, not an outage.
3. Usage export: `s3_gateway_usage_exports_total{outcome}` by backend, and the
   exporter's provider credentials.

## Recover

1. Provider outage: wait for the provider. Do not raise ceilings.
2. One bucket that cannot be listed: ask the customer to split the bucket or
   delete keys. Keep the operator key ceiling (`max_account_keys`) below what a
   45 s scan can list.
3. Usage export: rotate or repair the provider usage credentials in the
   operator config, then restart `faas-s3-gatewayd`.
