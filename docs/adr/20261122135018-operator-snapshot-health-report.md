# ADR-20261122135018: Operator snapshot health report

Status: Accepted

Extends ADR-20261115175927 (operator console routes) and
ADR-20261121200409 (operator wake-latency report).

## Context

Snapshots are cache, not truth (ADR-005), but the disk economics depend on
them. The financial model's parked footprint target is 130 MB per sandbox
(`FleetSnapshotAvgTargetMB`, spec §1 and §8; §12 warns above 160 MB and pages
above 200 MB). Operators can see that number only as the single
`fcvm_snapshot_fleet_avg_bytes` gauge. They cannot see which snapshots make
it up, whether a Firecracker upgrade or CPU template change left snapshots
stale, or which live deployments have no usable snapshot and so cold boot on
every wake. The wake-latency report names `no_snapshot` and `stale` cold
boots, but nothing shows the snapshots behind them. A live deployment with
neither a usable snapshot nor a rootfs breaks spec invariant 6.2-3. Today
nobody would notice until its next wake fails.

## Decision

One operator session read:

| Route | Returns |
| --- | --- |
| `GET /v1/admin/obs/snapshot-health` | `ObsSnapshotHealthReport` |

It reads two new optional store queries, `state.SnapshotHealthReader`:

- `OperatorSnapshotInventory` returns every snapshot row with its app and
  parked footprint, largest first, up to `ObsSnapshotInventoryMax` (20,000).
  The footprint is allocated mem + vmstate (logical bytes from a legacy
  writer) plus the deployment's above-base layer. That is the definition
  schedd's `ListLiveSnapshotStats` uses for the gauge. `truncated` says
  more rows existed.
- `OperatorLiveDeploymentsWithoutSnapshot` returns live deployments of active
  apps whose snapshots are all missing, stale or pending deletion, up to
  `ObsSnapshotUnsnapshottedMax` (200). It reports `cold_bootable = false`
  when the deployment has no `rootfs_key` either, and lists those first.

`pkg/apid/snapshothealth` reports:

- totals: live snapshots, stale snapshots, snapshots pending deletion, the
  fleet average and nearest-rank p95 over snapshots that are not stale (as
  the gauge counts), and snapshots over the 160 MB warn line;
- live and stale counts by tier, by Firecracker version and by CPU template
  and model;
- the 25 largest live snapshots and the 25 most recently staled;
- the live deployments with no usable snapshot, and how many of them cannot
  cold boot.

## Consequences

- The console gets a Snapshot health page under On call. A
  not-cold-bootable deployment is an invariant breach worth paging on, so it
  shows as an alert at the top.
- No migration. The inventory scan is bounded and joins on primary keys. The
  unsnapshotted read uses `deployments_live_traffic_idx` and
  `snapshots_deployment_idx`. Both are read on demand by operators only.
- The gauge and its alerts stay. This report explains the gauge; it does not
  replace it.
- The route is operator-only and excluded from the public OpenAPI spec like
  the other `/v1/admin/obs/*` routes.
