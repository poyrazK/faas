# FaasSnapshotFleetAvgHighWarn / FaasSnapshotFleetAvgHighPage

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `fcvm_snapshot_fleet_avg_bytes` (schedd `/metrics/fcvm`).
Spec: §12 (snapshot_fleet_avg_mb 130 plan, > 160 warn, > 200 page).

## Symptom

The fleet-average snapshot size has crossed the §12 threshold.

- Warn tier (`FaasSnapshotFleetAvgHighWarn`) trips at > 160 MB for 15 m.
- Page tier (`FaasSnapshotFleetAvgHighPage`) trips at > 200 MB for 10 m.

A large fleet average is the canonical sign of layered rootfs bloat
(§4.6 two-drive layout, drive1 per-app layer growing past the
120 MB/sandbox target).

## Verify

```bash
curl -fsS http://127.0.0.1:9103/metrics/fcvm | grep fcvm_snapshot_fleet_avg_bytes
ls -la /srv/fc/snap/ | head -50
```

Look for outliers: a small number of 600 MB+ snapshots skew the average
and pinpoint the offending app.

## Check

```bash
du -sh /srv/fc/snap/* 2>/dev/null | sort -h | tail -20
```

The two-drive layout (drive0 = base, drive1 = per-app overlay) means
the per-app layer is the suspect — the base image is shared and
shouldn't grow at runtime. A `npm install` that pulled devDependencies
is the most common cause; customer code reviews live in `docs/ops/`.

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
