# FaasSnapshotFleetAvgHighWarn / FaasSnapshotFleetAvgHighPage

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `fcvm_snapshot_fleet_avg_bytes` (schedd `/metrics/fcvm`).
Spec: §12 (snapshot_fleet_avg_mb 130 plan, > 160 warn, > 200 page).

## Symptom

The fleet-average snapshot size has crossed the §12 threshold.

- Warn tier (`FaasSnapshotFleetAvgHighWarn`) trips at > 160 MB for 15 m.
- Page tier (`FaasSnapshotFleetAvgHighPage`) trips at > 200 MB for 10 m.

The metric sums each snapshot's allocated filesystem blocks with its
deployment's above-base app content. It does not use the Firecracker memory
file's logical length: a sparse 1 GiB memory file may consume about 130 MiB.
Rows written before `snapshots.stored_bytes` report zero and conservatively
fall back to logical bytes until the app produces a new snapshot.

A large fleet average therefore indicates either too many dirty guest-memory
pages or above-base app-layer growth (§4.6 two-drive layout).

## Verify

```bash
curl -fsS http://127.0.0.1:9103/metrics/fcvm | grep fcvm_snapshot_fleet_avg_bytes
du -h /var/lib/faas/cache/*/* 2>/dev/null | sort -h | tail -20
du -h --apparent-size /var/lib/faas/cache/*/* 2>/dev/null | sort -h | tail -20
```

Compare allocated and apparent sizes. A large apparent memory image with a
small allocated size is healthy sparse storage; a large allocated size means
the guest dirtied most of its RAM before capture.

## Check

```bash
du -sh /var/lib/faas/cache 2>/dev/null
```

The base image is shared and excluded. For app-layer outliers, inspect whether
the build retained development dependencies. For memory outliers, inspect the
runtime's initialization and caches for pages touched before snapshot.

## Silence

```bash
amtool silence add \
  --matchers='alertname=~"FaasSnapshotFleetAvgHigh.*"' \
  --duration=1h \
  --comment='fleet reclaim in progress'
```

## Recover

The fleet-target alert is informational; the LV-fc alert
(`FaasLvFcUsageHigh*`) is the page-tier consequence. If both fire,
follow the LV-fc runbook. If only this one fires, schedule a fleet
reclaim during the next maintenance window.
