# FaasColdBootFallbackHigh

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metrics: `vmmd_cold_boot_fallback_total` (vmmd `/metrics/fallback`)
and `vmmd_ops_total{op=~"create_from_snapshot|create_cold_boot"}` (denominator).
Spec: §12 (cold_boot_fallback_pct < 2% target, > 10% warn).
Severity: warn.

## Symptom

More than 10% of wakes have fallen back from snapshot restore to
cold-boot over a 15-minute window. This is the canonical "snapshot rot"
signal — every Firecracker version bump invalidates existing snapshots
(ADR-005), and wakes that try to restore a stale snapshot fall back
to cold-boot. Cold boot always works (invariant §6.2/3), but it's
~5x slower than snapshot restore.

## Verify

```bash
curl -fsS http://127.0.0.1:9104/metrics/fallback | grep -E 'cold_boot|create_from'
curl -fsS 'http://127.0.0.1:9090/api/v1/query?query=rate(vmmd_cold_boot_fallback_total[5m])/sum(rate(vmmd_ops_total{op=~"create_from_snapshot|create_cold_boot"}[5m]))'
```

## Check

```bash
journalctl -u vmmd --since '-30m' --no-pager | grep -iE 'snapshot|fc.version|fallback'
ls -la /srv/fc/snap/ | head
```

Look for `firecracker version mismatch` in vmmd logs — the canonical
sign that an FC upgrade happened. Snapshots are pinned to the FC
version that made them (`snapshots.fc_version` in pkg/state); on FC
upgrade they go stale, and the next wake for the affected app falls
back to cold-boot.

## Silence

```bash
amtool silence add \
  --matchers='alertname=FaasColdBootFallbackHigh' \
  --duration=1h \
  --comment='FC upgrade completed; lazy re-snapshot in progress'
```

## Recover

Lazy re-snapshot (ADR-005) is the desired path: each cold-boot
produces a fresh snapshot pinned to the new FC version, so the fleet
self-heals as traffic moves. Operator action is only needed if the
traffic mix doesn't exercise all snapshots within a reasonable window
(in which case manually kick a wake for each app to trigger the
re-snapshot).
