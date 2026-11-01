# FaasObjectStorage

Alerts: `FaasObjectStorageInventoryFailing`, `FaasS3GatewayErrorRateHigh`,
`FaasObjectStorageUsageExportStale`, `FaasObjectStorageRegionNearlyFull`,
`FaasObjectStorageRegionFull`, `FaasS3GatewayReadUsageFlushFailing`,
`FaasPublicObjectReadUsageFlushFailing`, `FaasS3GatewayPortShareSaturated`,
`FaasComputeObjectGatewayDown`, `FaasComputeObjectGatewayConnectorDisconnected`.
Sources: apid's inventory sweep (`pkg/apid/object/storage_accounting.go`),
`s3-gatewayd` (`pkg/s3gateway`), apid's provider usage exporter
(`pkg/apid/object/usage_export.go`), and public bucket reads in `gatewayd-public`
(`pkg/objectstorage/public_read.go`).
Metrics: `faas_object_storage_inventory_scans_total{outcome}`,
`faas_object_storage_inventory_provider_requests_total`,
`s3_gateway_ops_total{op="s3_request",code}`,
`faas_object_storage_usage_export_last_success_timestamp_seconds{backend}`,
`faas_object_storage_backend_buckets{region,backend}`,
`faas_object_storage_backend_bucket_limit{region,backend}`,
`faas_object_storage_region_full{region}`,
`s3_gateway_read_admission_decisions_total{source}`,
`s3_gateway_read_usage_flushes_total{outcome}`,
`s3_gateway_org_access_checks_total{source}`,
`s3_gateway_credential_resolutions_total{source}`,
`gatewayd_public_object_read_admission_decisions_total{source}`,
`gatewayd_public_object_read_usage_flushes_total{outcome}`,
`s3_gateway_transfer_bytes_total{direction}`,
`s3_gateway_transfer_throttled_seconds_total{direction}`,
`s3_gateway_transfer_limit_bytes_per_second`,
`up{job="s3-gatewayd-compute"}`, `up{job="cloudflared-s3"}`,
`cloudflared_tunnel_ha_connections{job="cloudflared-s3"}`.
Decision records: [ADR 20261023223000](../adr/20261023223000-object-storage-bucket-scoped-availability.md),
[ADR 20261030100835](../adr/20261030100835-object-storage-resumable-inventory.md) (resumable inventory),
[ADR 20261029174627](../adr/20261029174627-object-storage-backend-pools.md) (bucket placement),
[ADR 20261030075301](../adr/20261030075301-object-storage-reads-off-the-database.md) (cached read admission),
[ADR 20261031122757](../adr/20261031122757-s3-gateway-on-compute-nodes.md) (gateway on compute nodes, port share).
Severity: warn.

## Symptom

- **Inventory failing.** Customers see `503 Object storage accounting is
  temporarily unavailable` on one bucket while the account's other buckets
  work. A bucket needs a successful inventory every 900 s, or a listing that
  is still advancing and an inventory under an hour old.
- **Gateway 5xx.** S3 clients get `ServiceUnavailable` (provider unreachable
  or accounting stale) or `SlowDown` (another delete or write of the same
  key, or an inventory scan of the bucket, is still settling). Both count as
  5xx.
- **Usage export stale.** With provider-report accounting, admission refuses
  transfers on every bucket of that backend once reports exceed
  `max_report_age_seconds`.
- **Read count flushes failing.** `s3-gatewayd`, and `gatewayd-public` for
  public bucket reads, admit `GET`/`HEAD` from a per-bucket decision they
  cache for 2 s and write read counts every second.
  While flushes fail, counts wait in memory; while the decisions cannot be
  refreshed, reads keep working for 30 s and then answer `503`.
- **Region nearly full or full.** Each customer bucket is its own provider
  bucket, and a provider project holds a limited number (OVH: 100, at most
  1,000 on request). Once every backend in a region's `pools` entry reaches
  its `max_buckets`, `CreateBucket` answers `503 object_storage_region_full`.
  Existing buckets keep working.
- **Port share saturated.** A gateway on a compute node paces transfers to
  its share of the node's port (`transfer.max_bytes_per_second`, one budget
  for uploads and downloads). Transfers get slower; none fail.
- **Compute gateway or s3 connector down.** On a cell that runs the gateway
  on its compute nodes, one node's `faas-s3-gatewayd-compute` stopped
  answering its control listener (port 9096), or its s3 tunnel connector
  `faas-cloudflared-s3` is down or holds no tunnel connections. The connector
  is bound to the gateway's unit and leaves the tunnel when it stops;
  Cloudflare sends the node's share to the other connectors. A node whose s3
  tunnel token is not staged keeps its connector inactive and stays in this
  alert.

## Check

1. Inventory failures: `journalctl -u faas-apid | grep "object storage inventory failed"`
   lists the bucket and backend IDs and how many pages its listing has. A
   large bucket is listed over several attempts (`outcome="partial"`); its
   progress is in the database:
   `SELECT bucket_id, started_at, progress_at, pages, complete FROM object_storage_inventory_scans ORDER BY started_at;`
   A listing whose `progress_at` is older than 5 minutes is not advancing,
   and its bucket refuses transfers once its last inventory is 900 s old. A
   listing that cannot finish within an hour, or one over 1M keys, starts
   again.
2. Gateway errors: `journalctl -u faas-s3-gatewayd` around the alert window.
   Compare with the provider's status page. Many `SlowDown` answers on one
   bucket mean customer concurrency on the same keys, or an uncertain delete
   holding its key, not an outage.
3. Usage export: `faas_object_storage_usage_exports_total{outcome}` by
   backend, and `journalctl -u faas-apid | grep "usage export"`. A last-success
   time of 0 means the export never ran: apid logs "object storage usage
   exports not configured" with the backend and the missing OVH application
   key, or "object storage usage report directory" when it cannot create the
   report's directory.
4. Read counts: `journalctl -u faas-s3-gatewayd | grep "read usage flush failed"`
   and `s3_gateway_read_admission_decisions_total{source="failed"|"stale"}`
   for database reachability from the gateway;
   `s3_gateway_org_access_checks_total{source="failed"}` for reads under
   organization-fenced credentials. For public reads:
   `journalctl -u faas-gatewayd-public | grep "public read usage flush failed"`
   and `gatewayd_public_object_read_admission_decisions_total`.
5. Capacity: `faas_object_storage_backend_buckets` against
   `faas_object_storage_backend_bucket_limit` per backend, and
   `journalctl -u faas-apid | grep "no backend with room"` for refusals.
6. Port share: `rate(s3_gateway_transfer_bytes_total[5m])` by direction
   against `s3_gateway_transfer_limit_bytes_per_second`, and whether one
   node or every node in the cell is at its share.
7. Compute gateway: on the node, `systemctl status faas-s3-gatewayd-compute
   faas-cloudflared-s3`, `curl -fsS http://127.0.0.1:9096/readyz` and
   `journalctl -u faas-s3-gatewayd-compute -u faas-cloudflared-s3`. A
   connector reported `inactive (dead)` with an unmet condition has no token
   at `/etc/faas/secrets/cloudflared/s3-tunnel-token`.

## Recover

1. Provider outage: wait for the provider. Do not raise ceilings.
2. One bucket whose listing does not advance: check the provider for that
   bucket's backend first. A listing that fails every page keeps restarting;
   the bucket's transfers recover once one listing completes. A listing
   longer than an hour (about 1M keys at more than 1.5 s per page) never
   completes: ask the customer to split the bucket or delete keys.
3. Usage export: rotate or repair the provider usage credentials in the
   operator config, or move `usage_reports_path` into a directory apid can
   write, then restart `faas-apid`.
4. Read count flushes: restore the gateway's database connection. Counts
   held in memory are written by the next successful flush; a restart
   writes them once before exiting.
5. Region capacity: add a provider project to the region's pool.
   1. Create the OVH Public Cloud project, its two S3 users and keys (the
      bucket-admin user for apid and the gateway user with the policy in
      `deploy/ansible/roles/s3_gateway_service/README.md`), and ask OVH
      support for the project's 1,000-bucket quota.
   2. Add a backend to `/etc/faas/object-storage.json` with a new `id`, the
      project as `namespace`, its own admin and gateway key environment
      variables and `max_buckets` below the quota (950 for 1,000). Its driver, Object Lock,
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
6. Port share: one node at its share while the others are not means
   Cloudflare is sending that node more of the tunnel's traffic; it clears
   as connections spread. Every node at its share means the cell has
   outgrown gateways on compute nodes: start the dedicated gateway machines
   ADR-20261031122757 §7 describes. Do not raise the share above
   `api.ObjectGatewayNodePortSharePercent` to clear the alert; app traffic
   needs the rest of the port.
7. Compute gateway: fix what its journal names (database reachability, the
   storage seal domain, the registry) and `systemctl restart
   faas-s3-gatewayd-compute`; the connector restarts with it. A missing
   token: stage the s3 tunnel's token (root, mode 0600) and `systemctl start
   faas-cloudflared-s3`. Never put the app tunnel's token there: that node
   would answer app hosts on the s3 tunnel.
